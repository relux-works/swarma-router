package launch

import (
	"context"
	"time"
)

type Router struct {
	Manager   string
	Project   string
	Plugin    Plugin
	Guard     Guard
	OpenStore func() (Store, error)
	Now       func() time.Time
}

func (r Router) base(i Intent) Result {
	return Result{Contract: Contract, CorrelationID: i.CorrelationID, RequestDigest: i.Digest(), Status: "refused"}
}
func (r Router) validate(i Intent) string {
	if !i.valid() {
		return "invalid_intent"
	}
	if i.Manager != r.Manager || i.Project != r.Project {
		return "scope_mismatch"
	}
	if r.Plugin == nil || r.Plugin.ID() != i.Plugin || r.Plugin.Configuration() != i.PluginConfig {
		return "capability_unavailable"
	}
	return ""
}

// Prepare is pure plugin translation. It neither calls Guard nor opens the store.
func (r Router) Prepare(i Intent) Result {
	out := r.base(i)
	if out.Reason = r.validate(i); out.Reason != "" {
		return out
	}
	a, err := r.Plugin.Prepare(i)
	if err != nil || !a.bound(i) {
		out.Reason = "unsupported_configuration"
		return out
	}
	out.Status, out.Attempt = "prepared", &a
	return out
}

// Start holds the store lock through the one effect attempt. A matching replay
// returns retained historical facts; only Observe can check current liveness.
func (r Router) Start(ctx context.Context, i Intent) Result {
	out := r.base(i)
	if out.Reason = r.validate(i); out.Reason != "" {
		return out
	}
	if ctx.Err() != nil {
		out.Reason = "context_cancelled"
		return out
	}
	if r.OpenStore == nil {
		out.Reason = "store_unavailable"
		return out
	}
	s, err := r.OpenStore()
	if err != nil {
		out.Status, out.Reason = "unknown", "store_unavailable"
		return out
	}
	defer s.Close()
	prior, found, err := s.Load(i.key())
	if err != nil {
		out.Status, out.Reason = "unknown", "store_unreadable"
		return out
	}
	if found {
		if !prior.valid() {
			out.Status, out.Reason = "unknown", "store_invalid"
			return out
		}
		if prior.Result.RequestDigest != i.Digest() {
			out.Status, out.Reason = "conflict", "intent_conflict"
			return out
		}
		if s.Sync(i.key()) != nil {
			out.Status, out.Reason, out.Attempt = "unknown", "receipt_not_durable", prior.Result.Attempt
			return out
		}
		out = prior.Result
		out.Historical = true
		return out
	}
	if r.Guard == nil || r.Now == nil {
		out.Reason = "admission_unavailable"
		return out
	}
	now := r.Now()
	a, err := r.Guard.Check(ctx, i, i.Digest(), now)
	if err != nil || a.Digest != i.Digest() || a.Manager != i.Manager || a.Member != i.Member || a.ProfileLock != i.ProfileLock || a.CheckedAt.IsZero() || a.CheckedAt.After(now) || !now.Before(a.ExpiresAt) || !a.CheckedAt.Before(a.ExpiresAt) {
		out.Reason = "admission_invalid"
		return out
	}
	if a.State != "stopped" {
		out.Reason = "insufficient_absence_evidence"
		return out
	}
	if !r.Now().Before(a.ExpiresAt) {
		out.Reason = "admission_expired"
		return out
	}
	ctx, cancel := context.WithDeadline(ctx, a.ExpiresAt)
	defer cancel()
	// Persist uncertainty before the call, including a crash immediately after this save.
	out.Status, out.Reason = "unknown", "attempt_unresolved"
	if s.Save(i.key(), Record{i, out}) != nil {
		out.Status, out.Reason = "refused", "intent_not_durable"
		return out
	}
	if ctx.Err() != nil || !r.Now().Before(a.ExpiresAt) {
		out.Status, out.Reason = "refused", "admission_expired_or_cancelled"
		if s.Save(i.key(), Record{i, out}) != nil {
			out.Status, out.Reason = "unknown", "receipt_not_durable"
		}
		return out
	}
	attempt, status, err := r.Plugin.Start(ctx, i)
	if attempt.bound(i) {
		out.Attempt = &attempt
	}
	if err == nil && status == "refused" {
		out.Status, out.Reason = "refused", "plugin_refused"
	} else if err == nil && status == "attempted" && attempt.complete(i) {
		out.Status, out.Reason = "attempted", "liveness_unverified"
	}
	if s.Save(i.key(), Record{i, out}) != nil {
		out.Status, out.Reason = "unknown", "receipt_not_durable"
	}
	return out
}

// Observe never calls Start. Lost or incomplete original receipts remain attached
// to their original operation; lack of a readiness source cannot become running.
func (r Router) Observe(ctx context.Context, i Intent) Result {
	out := r.base(i)
	if out.Reason = r.validate(i); out.Reason != "" {
		return out
	}
	if r.OpenStore == nil {
		out.Status, out.Reason = "unknown", "store_unavailable"
		return out
	}
	s, err := r.OpenStore()
	if err != nil {
		out.Status, out.Reason = "unknown", "store_unavailable"
		return out
	}
	defer s.Close()
	prior, found, err := s.Load(i.key())
	if err != nil || !found || !prior.valid() {
		out.Status, out.Reason = "unknown", "operation_unavailable"
		return out
	}
	if prior.Result.RequestDigest != i.Digest() {
		out.Status, out.Reason = "conflict", "intent_conflict"
		return out
	}
	if prior.Result.Status == "refused" {
		out = prior.Result
		out.Historical = true
		return out
	}
	out = prior.Result
	a, status, err := r.Plugin.Observe(ctx, i, prior.Result.Attempt)
	out.Status, out.Reason = "unknown", "observation_unresolved"
	if a.bound(i) {
		out.Attempt = &a
	}
	if err == nil && a.complete(i) && (status == "pending" || status == "running") {
		out.Status, out.Reason = status, ""
	}
	if s.Save(i.key(), Record{i, out}) != nil {
		out.Status, out.Reason = "unknown", "receipt_not_durable"
	}
	return out
}
