package launchbridge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"relux.works/swarma-router/handoff"
	"relux.works/swarma-router/launch"
)

const AssociationSchema = "a2a.terminal-association.proposed/1"
const AssociationBudget = 250 * time.Millisecond

// These phase budgets belong to the source comparator. A consumer cannot
// verify them from the closed public response; fixture timing is not native
// qualification of state-lock interoperability or kernel I/O cancellation.
const AssociationLockBudget = 100 * time.Millisecond
const SnapshotBudget = 100 * time.Millisecond

// AssociationTransport is an admitted launcher capability, not an arbitrary
// command runner or a request-selected executable. Exchange must deliver frame
// exclusively to the already selected own-context child via the contract's
// private read-only FD 3 and the fixed bind association command. The launcher
// authenticates origin; descriptor ownership/seals and the source do not.
// Implementations must bound output to 2048 bytes, require empty stderr, honor
// cancellation without a retry, and return the actual command exit code.
// There is deliberately no installed/native implementation until the source
// command lands and the exact handoff route is admitted and qualified.
type AssociationTransport interface {
	Exchange(context.Context, []byte) (stdout, stderr []byte, exit int, err error)
}

// BindingAcquisition obtains an independently authenticated current native
// runtime/thread, exact expected stored tuple, and operation-bound lane read.
// A stored association, process digest or declaration alone cannot implement
// this capability. Nil refuses. These Go values are private embedding inputs;
// they introduce no wire fields or new authority/registry protocol.
type BindingAcquisition interface {
	Acquire(context.Context, launch.Intent, launch.Snapshot) (AcquiredBinding, error)
}

// OriginalIntent is the retained complete original intent from the admitted
// private policy, not a reconstruction from the stored tuple. For the lower
// launch contract its bytes must agree with that contract's fixed digest.
type AcquiredBinding struct {
	Expected       ExpectedBinding
	Witness        launch.Readiness
	OriginalIntent []byte
}

type AssociationTarget struct {
	Socket     string `json:"socket"`
	SocketID   string `json:"socket_id"`
	Pane       string `json:"pane"`
	PanePID    int    `json:"pane_pid"`
	PaneBirth  string `json:"pane_birth"`
	OwnerPID   int    `json:"owner_pid"`
	OwnerBirth string `json:"owner_birth"`
	Runtime    string `json:"runtime"`
}

type ExpectedBinding struct {
	Generation    string            `json:"generation"`
	ConsumerPID   int               `json:"consumer_pid"`
	ConsumerBirth string            `json:"consumer_birth"`
	OwnerPID      int               `json:"owner_pid"`
	OwnerBirth    string            `json:"owner_birth"`
	Runtime       string            `json:"runtime"`
	SessionID     string            `json:"session_id"`
	Target        AssociationTarget `json:"target"`
}

type associationRequest struct {
	Schema     string          `json:"schema"`
	Identity   string          `json:"source_identity"`
	Connection string          `json:"source_connection"`
	Digest     string          `json:"launch_intent_sha256"`
	Issued     int64           `json:"issued_at_ms"`
	Expires    int64           `json:"expires_at_ms"`
	Expected   ExpectedBinding `json:"expected"`
}

// AssociationReady implements launch.Ready for a chat member. Both acquisition
// capabilities and the selected own identity/connection are trusted embedding
// configuration, never fields read from a launch request or a public receipt.
type AssociationReady struct {
	Identity, Connection, Server string
	Acquire                      BindingAcquisition
	Transport                    AssociationTransport
	Now                          func() time.Time
}

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var targetPane = regexp.MustCompile(`^%[0-9]{1,10}$`)
var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

const maxAssociationTime int64 = 4102444800000

func privateString(s string, max int) bool {
	if len(s) == 0 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func validPID(pid int) bool { return pid > 0 && int64(pid) <= 2147483647 }

func (e ExpectedBinding) valid() bool {
	t := e.Target
	return hex32.MatchString(e.Generation) && validPID(e.ConsumerPID) && validPID(e.OwnerPID) &&
		privateString(e.ConsumerBirth, 128) && privateString(e.OwnerBirth, 128) &&
		privateString(e.SessionID, 200) && (e.Runtime == "codex" || e.Runtime == "claude") &&
		privateString(t.Socket, 512) && privateString(t.SocketID, 128) && targetPane.MatchString(t.Pane) &&
		validPID(t.PanePID) && privateString(t.PaneBirth, 128) && t.OwnerPID == e.OwnerPID &&
		t.OwnerBirth == e.OwnerBirth && t.Runtime == e.Runtime
}

func sameProcess(p, q launch.Process) bool {
	return validPID(p.PID) && validPID(p.ParentPID) && privateString(p.Birth, 99) && p.Executable != "" && p == q
}

func witnessBound(i launch.Intent, s launch.Snapshot, e ExpectedBinding, w launch.Readiness, now time.Time, identity, host string) bool {
	runtime := "codex"
	if i.Runtime == "claude_code" {
		runtime = "claude"
	} else if i.Runtime != "codex_cli" {
		return false
	}
	if !e.valid() || e.Runtime != runtime || identity != i.Member || s.Session != i.TmuxSession ||
		s.OperationDigest != i.Digest() || e.Target.Pane != s.Pane || e.Target.PanePID != s.PanePID ||
		w.TaskID != i.TaskID || w.Member != i.Member || w.Session != s.Session || w.Pane != s.Pane ||
		w.Thread != e.SessionID || (i.Mode == "resume" && i.NativeSession != e.SessionID) ||
		w.Runtime.PID != e.OwnerPID || w.Runtime.Birth != e.OwnerBirth || !sameProcess(w.Runtime, w.BindingRuntime) ||
		w.BindingRef == "" || w.LaneRef == "" || w.NativeAssignment != "" || w.Handoff != "" ||
		w.CheckedAt.IsZero() || w.CheckedAt.After(now) || !now.Before(w.ExpiresAt) || !w.CheckedAt.Before(w.ExpiresAt) {
		return false
	}
	if !server(host) || w.LaneSender != "@"+identity+":"+host {
		return false
	}
	owner, pane := false, false
	for _, p := range s.Processes {
		if p.PID == e.OwnerPID {
			if owner || !sameProcess(p, w.Runtime) {
				return false
			}
			owner = true
		}
		if p.PID == s.PanePID {
			if pane || !validPID(p.ParentPID) || p.Birth != e.Target.PaneBirth || p.Executable == "" {
				return false
			}
			pane = true
		}
	}
	return owner && pane
}

// Read never starts a member or acquires an absence admission. In particular,
// every unavailable/mismatch/stopped response returns no Readiness whatsoever.
func (a AssociationReady) Read(ctx context.Context, i launch.Intent, s launch.Snapshot) (launch.Readiness, error) {
	var empty launch.Readiness
	if ctx.Err() != nil || a.Now == nil || a.Acquire == nil || a.Transport == nil ||
		!privateString(a.Identity, 200) || !privateString(a.Connection, 128) {
		return empty, Refusal("association_source_unavailable")
	}
	started := a.Now()
	acquired, err := a.Acquire.Acquire(ctx, i, s)
	e, w := acquired.Expected, acquired.Witness
	now := a.Now()
	if err != nil || len(acquired.OriginalIntent) == 0 || len(acquired.OriginalIntent) > MaxProjectionBytes ||
		fmt.Sprintf("%x", sha256.Sum256(acquired.OriginalIntent)) != i.Digest() ||
		now.Before(started) || ctx.Err() != nil || !witnessBound(i, s, e, w, now, a.Identity, a.Server) {
		return empty, Refusal("readiness_acquisition_unavailable")
	}
	expires := now.Add(30 * time.Second)
	if w.ExpiresAt.Before(expires) {
		expires = w.ExpiresAt
	}
	if now.UnixMilli() < 0 || expires.UnixMilli() > maxAssociationTime || now.UnixMilli() >= expires.UnixMilli() {
		return empty, Refusal("association_input_invalid")
	}
	request := associationRequest{AssociationSchema, a.Identity, a.Connection, i.Digest(), now.UnixMilli(), expires.UnixMilli(), e}
	body, err := json.Marshal(request)
	if err != nil || len(body) == 0 || len(body) > 8192 {
		return empty, Refusal("association_input_invalid")
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	operationStart := time.Now() // monotonic budget is independent of the UTC clock
	bounded, cancel := context.WithTimeout(ctx, AssociationBudget)
	defer cancel()
	out, stderr, exit, err := a.Transport.Exchange(bounded, frame)
	after := a.Now()
	if err != nil || len(stderr) != 0 || bounded.Err() != nil || time.Since(operationStart) >= AssociationBudget || after.Before(now) || !after.Before(expires) {
		return empty, Refusal("association_unavailable")
	}
	if err := acceptAssociation(out, exit, request, after); err != nil {
		return empty, err
	}
	finished := a.Now()
	if bounded.Err() != nil || time.Since(operationStart) >= AssociationBudget || finished.Before(after) || !finished.Before(expires) {
		return empty, Refusal("association_unavailable")
	}
	// The association observation bounds this witness, but does not supply any
	// native/thread/lane fields or extend the acquired witness's lifetime.
	w.ExpiresAt = expires
	return w, nil
}

type storedAssociation struct {
	Generation    string  `json:"generation"`
	ConsumerPID   int     `json:"consumer_pid"`
	ConsumerBirth string  `json:"consumer_birth_sha256"`
	OwnerPID      int     `json:"owner_pid"`
	OwnerBirth    string  `json:"owner_birth_sha256"`
	Runtime       string  `json:"runtime"`
	Stopped       *bool   `json:"stopped"`
	StoppedReason *string `json:"stopped_reason"`
}

type associationOutput struct {
	Schema   string             `json:"schema"`
	Scope    string             `json:"scope"`
	Source   string             `json:"source"`
	Observed *int64             `json:"observed_at_ms"`
	Digest   *string            `json:"launch_intent_sha256"`
	Result   string             `json:"result"`
	Reason   string             `json:"reason"`
	Stored   *storedAssociation `json:"stored"`
	Native   string             `json:"native_evidence"`
}

func hashBirth(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// handoff.Decode already rejects duplicate/noncanonical/unknown fields. Exact
// cardinality additionally enforces required null fields and explicit false.
func requiredObject(raw []byte, count int) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != count {
		return nil
	}
	return m
}

func acceptAssociation(raw []byte, exit int, q associationRequest, now time.Time) error {
	invalid := Refusal("association_output_invalid")
	var o associationOutput
	if len(raw) < 3 || len(raw) > 2048 || raw[0] != '{' || raw[len(raw)-2] != '}' || raw[len(raw)-1] != '\n' || !utf8.Valid(raw) || handoff.Decode(raw, &o) != nil {
		return invalid
	}
	m := requiredObject(raw, 9)
	if m == nil || o.Schema != AssociationSchema || o.Scope != "stored-own-binding" || o.Source != "selected-own-state-snapshot" || o.Native != "not-observed-by-this-interface" {
		return invalid
	}
	if o.Reason == "clock_unavailable" {
		if o.Observed != nil {
			return invalid
		}
	} else if o.Observed == nil || !decimal.Match(m["observed_at_ms"]) || *o.Observed < 0 || *o.Observed > maxAssociationTime {
		return invalid
	}
	if o.Digest != nil && (!hex64.MatchString(*o.Digest) || *o.Digest != q.Digest) {
		return invalid
	}
	if o.Stored != nil {
		s := o.Stored
		sm := requiredObject(m["stored"], 8)
		if sm == nil || !hex32.MatchString(s.Generation) || !validPID(s.ConsumerPID) || !validPID(s.OwnerPID) ||
			!decimal.Match(sm["consumer_pid"]) || !decimal.Match(sm["owner_pid"]) ||
			!hex64.MatchString(s.ConsumerBirth) || !hex64.MatchString(s.OwnerBirth) ||
			(s.Runtime != "codex" && s.Runtime != "claude") || s.Stopped == nil ||
			(*s.Stopped && (s.StoppedReason == nil || *s.StoppedReason != "recorded-stop-cause-ambiguous")) ||
			(!*s.Stopped && s.StoppedReason != nil) {
			return invalid
		}
	}
	projection := o.Stored != nil && o.Digest != nil
	switch o.Result {
	case "unavailable":
		if exit != 2 {
			return invalid
		}
		switch o.Reason {
		case "input_untrusted", "input_invalid", "input_expired", "context_unavailable", "clock_unavailable", "privacy_collision":
			if o.Stored != nil || o.Digest != nil {
				return invalid
			}
		case "budget_exceeded", "snapshot_unavailable", "binding_missing", "binding_invalid", "stale_binding":
			if o.Stored != nil || o.Digest == nil {
				return invalid
			}
		case "binding_stopped":
			if !projection || !*o.Stored.Stopped {
				return invalid
			}
		default:
			return invalid
		}
		return Refusal("association_" + o.Reason)
	case "mismatch":
		if exit != 1 || !projection || *o.Stored.Stopped || (o.Reason != "generation_mismatch" && o.Reason != "tuple_mismatch") {
			return invalid
		}
		return Refusal("association_" + o.Reason)
	case "match":
		if exit != 0 || o.Reason != "stored_tuple_equal" || !projection || *o.Stored.Stopped || *o.Observed < q.Issued || *o.Observed > now.UnixMilli() || *o.Observed >= q.Expires {
			return invalid
		}
	default:
		return invalid
	}
	s, e := o.Stored, q.Expected
	if s.Generation != e.Generation || s.ConsumerPID != e.ConsumerPID || s.ConsumerBirth != hashBirth(e.ConsumerBirth) ||
		s.OwnerPID != e.OwnerPID || s.OwnerBirth != hashBirth(e.OwnerBirth) || s.Runtime != e.Runtime {
		return invalid
	}
	// Conservative local collision defense includes numeric observation formatting.
	// The source must independently apply its full private-value collision rule.
	values := []string{fmt.Sprint(*o.Observed), *o.Digest, s.Generation, fmt.Sprint(s.ConsumerPID), s.ConsumerBirth, fmt.Sprint(s.OwnerPID), s.OwnerBirth, s.Runtime}
	for _, private := range []string{e.SessionID, e.ConsumerBirth, e.OwnerBirth, e.Target.Socket, e.Target.SocketID, e.Target.Pane, e.Target.PaneBirth} {
		for _, public := range values {
			if strings.Contains(public, private) {
				return Refusal("association_privacy_collision")
			}
		}
	}
	return nil
}

// AssembleAssociation keeps the lower Router/Curator/journal and WIP custody
// path. Pure construction and Prepare acquire neither source. Missing native
// capabilities remain unavailable, without selecting an alternate CLI or identity.
func AssembleAssociation(p Policy, events CLIEvents, backend launch.Backend, owner OwnerSource, acquisition BindingAcquisition, transport AssociationTransport, connection string, clock func() time.Time) (launch.Router, error) {
	ready := AssociationReady{Identity: p.Config.Agent, Connection: connection, Server: p.Config.Server, Acquire: acquisition, Transport: transport, Now: clock}
	return Assemble(p, events, backend, owner, ready, clock)
}
