package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Adapter interface {
	ValidateSources(context.Context, Request, Policy) string
	Post(context.Context, Request, Policy, string) (string, error)
	VerifyEvent(context.Context, Request, Policy, string, string) string
}

type Engine struct {
	Adapter Adapter
	// OpenJournal allows isolated fault controls. Nil selects the durable filesystem journal.
	OpenJournal func(string) (Journal, error)
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func notice(r Request) string {
	return fmt.Sprintf("Queued task notice\nProject: %s\nTask: %s — %s\nRecipient: %s\nCorrelation: %s\nThis is a notification only; acceptance and execution are not established.", r.Project, r.TaskID, r.TaskTitle, r.Member, r.CorrelationID)
}

func intentHash(r Request, policyDigest, body string) string {
	return hash(struct {
		Request      Request
		PolicyDigest string
		Notice       string
	}{r, policyDigest, body})
}

func (e Engine) Execute(ctx context.Context, r Request, p Policy, dir string) Result {
	result := Result{Contract: Contract, Status: "refused"}
	if reason := validate(r, p); reason != "" {
		result.Reason = reason
		return result
	}
	result.CorrelationID = r.CorrelationID
	if e.Adapter == nil {
		result.Reason = "adapter_unavailable"
		return result
	}
	body := notice(r)
	if r.Action == "plan" {
		if reason := e.Adapter.ValidateSources(ctx, r, p); reason != "" {
			result.Reason = reason
			return result
		}
		result.Status, result.Notice = "planned", body
		return result
	}
	open := e.OpenJournal
	if open == nil {
		open = OpenJournal
	}
	j, err := open(dir)
	if err != nil {
		result.Reason = "journal_unavailable_or_locked"
		if errors.Is(err, errJournalParentNotDurable) {
			result.Reason = "journal_parent_not_durable"
		}
		return result
	}
	defer j.Close()
	policyDigest := hash(p)
	digest := intentHash(r, policyDigest, body)
	rec, exists, err := j.Load(r.CorrelationID)
	if err != nil {
		result.Reason = "journal_corrupt_or_unreadable"
		return result
	}
	if exists {
		if rec.validate() != nil {
			result.Reason = "journal_corrupt_or_unreadable"
			return result
		}
		if rec.IntentDigest != digest {
			result.Status, result.Reason = "conflict", "correlation_intent_changed"
			return result
		}
		if rec.Phase == "verified" {
			// A prior Save may have installed these bytes before its final sync
			// failed. Resolve that durability before trusting a historical receipt.
			if j.Sync(r.CorrelationID) != nil {
				result.Status, result.EventID, result.Reason = "unknown", rec.EventID, "verified_receipt_not_durable"
				return result
			}
			result.Status, result.EventID = "queued_notification", rec.EventID
			return result
		}
		// An intent may already have sent even when it has no returned event ID.
		if rec.EventID == "" {
			result.Status, result.Reason = "unknown", "prior_attempt_requires_external_reconciliation"
			return result
		}
		return e.reconcile(ctx, r, p, rec, j, result)
	}
	if reason := e.Adapter.ValidateSources(ctx, r, p); reason != "" {
		result.Reason = reason
		return result
	}
	rec = Record{Contract: Contract, Request: r, PolicyDigest: policyDigest, IntentDigest: digest, Notice: body, Phase: "intent"}
	if j.Save(r.CorrelationID, rec) != nil {
		result.Reason = "intent_not_durable"
		return result
	}
	out, err := e.Adapter.Post(ctx, r, p, body)
	if err != nil || !eventID.MatchString(strings.TrimSpace(out)) {
		rec.Phase = "unknown"
		_ = j.Save(r.CorrelationID, rec)
		result.Status, result.Reason = "unknown", "post_result_unknown"
		return result
	}
	rec.EventID, rec.Phase = strings.TrimSpace(out), "sent"
	if j.Save(r.CorrelationID, rec) != nil {
		result.Status, result.EventID, result.Reason = "unknown", rec.EventID, "post_receipt_not_durable"
		return result
	}
	return e.reconcile(ctx, r, p, rec, j, result)
}

func (e Engine) reconcile(ctx context.Context, r Request, p Policy, rec Record, j Journal, result Result) Result {
	result.Status, result.EventID = "unknown", rec.EventID
	if reason := e.Adapter.VerifyEvent(ctx, r, p, rec.EventID, rec.Notice); reason != "" {
		rec.Phase = "unknown"
		_ = j.Save(r.CorrelationID, rec)
		result.Reason = reason
		return result
	}
	rec.Phase = "verified"
	if j.Save(r.CorrelationID, rec) != nil {
		result.Reason = "verified_receipt_not_durable"
		return result
	}
	result.Status, result.Reason = "queued_notification", ""
	return result
}
