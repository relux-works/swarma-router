package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Proves a valid complete record is accepted and mutations of closed schema,
// embedded request, snapshot, mapping, observation and receipt are rejected
// without mutation or source/lifecycle calls. This is integrity, not authenticity.
func TestOperationRecordRejections(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	invocation(t, e, r)
	original := bytes.Clone(f.raw)
	var source map[string]any
	if json.Unmarshal(original, &source) != nil {
		t.Fatal("fixture")
	}
	changes := map[string]func(map[string]any){
		"unknown-field":  func(m map[string]any) { m["extra"] = nil },
		"missing-field":  func(m map[string]any) { delete(m, "known_execution") },
		"case-alias":     func(m map[string]any) { m["Phase"] = m["phase"]; delete(m, "phase") },
		"wrong-phase":    func(m map[string]any) { m["phase"] = "preparing" },
		"wrong-contract": func(m map[string]any) { m["contract"] = "future" },
		"request-digest": func(m map[string]any) { m["request"].(map[string]any)["request_digest"] = strings.Repeat("0", 64) },
		"request-key": func(m map[string]any) {
			m["request"].(map[string]any)["key"].(map[string]any)["correlation_id"] = "other"
		},
		"snapshot":  func(m map[string]any) { m["initial_sources"].(map[string]any)["digest"] = strings.Repeat("0", 64) },
		"proposal":  func(m map[string]any) { m["proposal"].(map[string]any)["revision"] = "other" },
		"selection": func(m map[string]any) { m["selected_binding"].(map[string]any)["member"] = "other" },
		"lower": func(m map[string]any) {
			m["lower_launch_binding"].(map[string]any)["binding"].(map[string]any)["intent_digest"] = strings.Repeat("0", 64)
		},
		"lost-effect":        func(m map[string]any) { m["terminal_no_effect"] = true },
		"changed-generation": func(m map[string]any) { m["known_execution"].(map[string]any)["generation"] = "other" },
		"observation-key": func(m map[string]any) {
			m["last_observation"].(map[string]any)["key"].(map[string]any)["caller"] = "other"
		},
		"receipt-revision":   func(m map[string]any) { m["receipt"].(map[string]any)["durable_revision"] = "other" },
		"receipt-historical": func(m map[string]any) { m["receipt"].(map[string]any)["historical"] = true },
		"receipt-ready":      func(m map[string]any) { m["receipt"].(map[string]any)["ready"] = nil },
		"receipt-drift": func(m map[string]any) {
			m["receipt"].(map[string]any)["ready"].(map[string]any)["lane_reply"].(map[string]any)["revision"] = "other"
		},
		"receipt-evidence":    func(m map[string]any) { m["receipt"].(map[string]any)["evidence"] = []any{} },
		"null-evidence":       func(m map[string]any) { m["receipt"].(map[string]any)["evidence"] = nil },
		"missing-observation": func(m map[string]any) { m["last_observation"] = nil },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			m := clone(source)
			change(m)
			f.raw = frame(t, m)
			counts := []int{f.resolve, f.prepare, f.start, f.observe, f.commits}
			response(t, invocation(t, e, r), "recovery", "unknown", "store_invalid", 3)
			if !sameCounters(counts, f) {
				t.Fatal("invalid record caused work")
			}
		})
	}
	for name, raw := range map[string][]byte{
		"duplicate": append([]byte(`{"phase":"ready",`), original[1:]...),
		"number":    bytes.Replace(original, []byte(`"possible_effect":true`), []byte(`"possible_effect":1`), 1),
		"surrogate": bytes.Replace(original, []byte(`"phase":"ready"`), []byte(`"phase":"\ud800"`), 1),
		"trailing":  append(bytes.Clone(original), []byte("{}\n")...),
		"crlf":      append(bytes.Clone(original[:len(original)-1]), '\r', '\n'),
	} {
		t.Run(name, func(t *testing.T) {
			f.raw = raw
			response(t, invocation(t, e, r), "recovery", "unknown", "store_invalid", 3)
		})
	}
	f.raw = original
	response(t, invocation(t, e, r), "receipt", "ready", "ok", 3)
	// Exact aggregate bound is accepted; one more byte is rejected.
	padding := bytes.Repeat([]byte(" "), MaxRecordBytes-len(original))
	bounded := append(append([]byte{}, padding...), original...)
	if _, _, ok := decodeOperation(bounded, r.Key()); !ok {
		t.Fatal("exact record bound rejected")
	}
	if _, _, ok := decodeOperation(append([]byte(" "), bounded...), r.Key()); ok {
		t.Fatal("oversized record accepted")
	}
}
func sameCounters(c []int, f *operationFixture) bool {
	return c[0] == f.resolve && c[1] == f.prepare && c[2] == f.start && c[3] == f.observe && c[4] == f.commits
}

// Proves only the documented forward phases are usable and the first retained
// source/proposal/selection/lower/execution and effect marker cannot change.
func TestOperationImmutableTransitions(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	invocation(t, e, r)
	original, _ := retained(t, f)
	if !immutableTransition(original, clone(original)) {
		t.Fatal("valid same-observation update rejected")
	}
	changes := map[string]func(*operationRecord){
		"snapshot":  func(n *operationRecord) { n.Sources.Data = []byte("different") },
		"proposal":  func(n *operationRecord) { n.Proposal.Revision = "different" },
		"selected":  func(n *operationRecord) { n.Selected.Configuration.Effort = "different" },
		"lower":     func(n *operationRecord) { n.Lower.Binding.OperationRef = "different" },
		"execution": func(n *operationRecord) { n.KnownExecution["thread"] = "different" },
		"action":    func(n *operationRecord) { n.Action = "reuse" },
		"effect":    func(n *operationRecord) { n.PossibleEffect = false },
		"phase":     func(n *operationRecord) { n.Phase = "selected" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			n := clone(original)
			change(&n)
			if immutableTransition(original, n) {
				t.Fatal("immutable binding changed")
			}
		})
	}
}

// Proves finite-deadline admission, current observe authority and errors never
// leak diagnostics or call source/lifecycle when their own boundary is absent.
func TestOperationAdmission(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	if _, err := e.Run(context.Background(), r); err == nil || err.Error() != "authority_unknown" {
		t.Fatal("unbounded context admitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := e.Run(ctx, Request{}); err == nil {
		t.Fatal("zero request admitted")
	}
	f.prepareAccessErr = Refusal{"authority_denied"}
	x := response(t, invocation(t, e, r), "error", "", "", 2)
	if x["code"] != "authority_denied" || f.raw != nil || f.resolve != 0 || f.start != 0 {
		t.Fatal("permission refusal caused reservation/effect")
	}
	f.prepareAccessErr = nil
	invocation(t, e, r)
	before := bytes.Clone(f.raw)
	f.observeAccessErr = &Refusal{"authority_denied"}
	response(t, invocation(t, e, statusRequest(t, r)), "recovery", "unknown", "authority_denied", 3)
	if !bytes.Equal(before, f.raw) || f.observe != 0 {
		t.Fatal("observe permission loss leaked/refreshed result")
	}
	if reason(Refusal{"ok"}, "authority_unknown") != "authority_unknown" || reason(Refusal{"invented"}, "authority_unknown") != "authority_unknown" || reason(errors.New("private detail"), "source_unavailable") != "source_unavailable" {
		t.Fatal("unrecognized error promoted")
	}
}

// Proves a failed custody release is reported as recovery while preserving the
// acknowledged ready record; a later replay cannot turn it into another Start.
func TestOperationCloseFailure(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	f.closeErr = errors.New("release failure")
	x := response(t, invocation(t, e, r), "recovery", "unknown", "store_unavailable", 3)
	state, b := retained(t, f)
	if state.Phase != "ready" || x["last_durable_revision"] != b.DurableRevision() || x["operation_ref"] != b.OperationRef() || f.start != 1 {
		t.Fatal("release failure lost committed identity")
	}
	f.closeErr = nil
	response(t, invocation(t, e, r), "receipt", "ready", "ok", 3)
	if f.start != 1 {
		t.Fatal("release recovery repeated Start")
	}
}

// Proves each adapter view and output accessor returns independent nested data;
// adapter-side mutation cannot replace retained source/selection/execution data.
func TestOperationViewCopies(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	out := invocation(t, e, r)
	state, b := retained(t, f)
	v := View{state, b}
	original := bytes.Clone(f.raw)
	p := v.Request()
	p.Cards[0].Card.Revision = "changed"
	p.Constraints.Configurations[0].Effort = "changed"
	s := v.Selected()
	s.Proposal.Revision = "changed"
	l := v.Lower()
	l.Intent.Model = "changed"
	k := v.KnownExecution()
	k["generation"] = "changed"
	sources := v.Sources()
	sources.Data[0] = 'x'
	sources.References[0].Revision = "changed"
	data := out.Bytes()
	data[0] = 'x'
	if !bytes.Equal(original, f.raw) || v.Request().Cards[0].Card.Revision == "changed" || v.Selected().Proposal.Revision == "changed" || v.Lower().Intent.Model == "changed" || v.KnownExecution()["generation"] == "changed" || bytes.Equal(data, out.Bytes()) || same(sources, v.Sources()) {
		t.Fatal("mutable view escaped")
	}
}

// Proves terminal decisions can retain their truthful first normalized snapshot
// and proposal without a selected target, while Status cannot reopen the decision.
func TestOperationTerminalSnapshot(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	f.plan.Action = ""
	f.plan.Selected = nil
	f.plan.Status = "deferred"
	f.plan.Reason = "no_eligible_candidate"
	response(t, invocation(t, e, r), "receipt", "deferred", "no_eligible_candidate", 2)
	state, _ := retained(t, f)
	if state.Sources == nil || state.Proposal == nil || state.Selected != nil || f.start != 0 || f.prepare != 0 {
		t.Fatal("terminal snapshot lost or prepared target")
	}
	response(t, invocation(t, e, statusRequest(t, r)), "receipt", "deferred", "no_eligible_candidate", 2)
	if f.resolve != 1 || f.observe != 0 {
		t.Fatal("terminal source decision reopened")
	}
}

// Proves post-effect record overflow returns bounded same-key recovery without
// erasing its original possible-effect marker/intent; later status can reconcile.
func TestOperationOversizedObservation(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	valid := f.observation
	// Fit a real initial snapshot but leave too little space for a large witness.
	data := bytes.Repeat([]byte("x"), 170000)
	f.plan.Sources.Data = data
	f.plan.Sources.Digest = hash(data)
	f.observation = func(v View) Observation {
		o := valid(v)
		for i := 0; i < 16; i++ {
			ref := v.Request().Task
			ref.Source = strings.Repeat("\x00", 255) + string(rune('a'+i))
			ref.Object = strings.Repeat("\x00", 256)
			ref.Revision = strings.Repeat("\x00", 256)
			o.Evidence = append(o.Evidence, ref)
		}
		// Replace the original small entry to stay within the legitimate count.
		o.Evidence = o.Evidence[1:]
		return o
	}
	response(t, invocation(t, e, r), "recovery", "unknown", "persistence_unresolved", 3)
	state, b := retained(t, f)
	if f.start != 1 || !state.PossibleEffect || state.Lower == nil || state.Receipt != nil || len(f.raw) > MaxRecordBytes {
		t.Fatal("overflow discarded/forged retained effect")
	}
	f.observation = valid
	response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
	_, next := retained(t, f)
	if next.OperationRef() != b.OperationRef() || f.start != 1 {
		t.Fatal("overflow caused replacement")
	}
}

// Proves response overflow independently of record overflow: optional references
// are dropped only from Recovery, never the exact lookup tuple/digest or record.
func TestOperationOutputBounds(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	invocation(t, e, r)
	state, b := retained(t, f)
	o := operation{engine: e, request: r, record: state, binding: b}
	receipt := o.receipt("ready", "ok")
	receipt.Ready = clone(state.Receipt.Ready)
	for i := 0; i < 16; i++ {
		ref := r.prepare.Task
		ref.Source = strings.Repeat("\x00", 255) + string(rune('a'+i))
		ref.Object = strings.Repeat("\x00", 256)
		ref.Revision = strings.Repeat("\x00", 256)
		receipt.Evidence = append(receipt.Evidence, ref)
	}
	// A complete valid wire ready variant cannot exceed this budget with the
	// golden selected tuple. Grow a legitimate selected tuple, not arbitrary tags.
	wide := strings.Repeat("\x00", 256)
	receipt.Ready.Execution = map[string]string{"kind": "host", "execution": wide, "runner_instance": wide, "native_thread": wide, "controller_epoch": wide, "messaging_session": wide, "messaging_thread": wide, "messaging_generation": wide}
	big := RevisionRef{wide, wide, wide, strings.Repeat("1", 64)}
	receipt.Ready.ManagerCustody = big
	receipt.Ready.TaskState = big
	receipt.Ready.RuntimeEvidence = big
	receipt.Ready.MessagingRoute = big
	receipt.Ready.LaneReply = big
	// Explicit encoder budget control; it checks the full frame regardless of
	// internal candidate provenance. Fresh-ready validation is tested separately.
	receipt.Selected = clone(&receipt.Ready.Selected)
	receipt.Selected.Card = big
	receipt.Selected.Proposal = big
	receipt.Selected.Configuration.Model = big
	receipt.Selected.Configuration.Profile = big
	receipt.Ready.Selected = *receipt.Selected
	original := bytes.Clone(f.raw)
	if len(envelope(r, object(receipt))) <= MaxResponseBytes {
		t.Fatal("output control did not exceed budget")
	}
	x := response(t, o.output(receipt), "recovery", "unknown", "persistence_unresolved", 3)
	if x["key"].(map[string]any)["caller"] != r.Key().Caller || x["request_digest"] != r.Digest() || !bytes.Equal(original, f.raw) {
		t.Fatal("output budget erased identity")
	}
	// Largest parser-admitted echo: all optional safe refs must be expendable.
	m := base(t)
	m["caller"] = strings.Repeat("\x00", 100)
	m["project"] = strings.Repeat("\x00", 100)
	wideReq := mustParse(t, frame(t, m), LocalContext{strings.Repeat("\x00", 100), strings.Repeat("\x00", 100)})
	binding, _ := RetainRequest(wideReq, wide, wide)
	largeState := state
	largeState.Lower = clone(state.Lower)
	largeState.Lower.Binding.Plugin = wide
	largeState.Lower.Binding.OperationRef = wide
	x = response(t, e.recovery(wideReq, "persistence_unresolved", binding, largeState), "recovery", "unknown", "persistence_unresolved", 3)
	if x["lower_launch"] != nil || x["key"].(map[string]any)["caller"] != wideReq.Key().Caller || x["request_digest"] != wideReq.Digest() {
		t.Fatal("recovery truncation violated key binding")
	}
}
