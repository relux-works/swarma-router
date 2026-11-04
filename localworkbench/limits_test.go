package localworkbench

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Relevant unknown/not_required is an omission, not a positive check; a caller
// denial is preserved as denial and cannot be laundered onto an observed gate.
func TestClosedUnionAndExplicitDenial(t *testing.T) {
	r, in := fixture("reuse")
	for _, g := range gates("reuse") {
		bad := clone(in)
		gate(&bad, g).Evidence = Evidence{Kind: "unknown", Reason: "not_required"}
		if o, e := run(r, bad); e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
			t.Fatalf("required gate waived: %s", g)
		}
	}
	basis := clone(gate(&in, "caller_authority").Evidence)
	basis.Statement = "I explicitly deny TASK-1 for @worker:one.example."
	gate(&in, "caller_authority").Evidence = Evidence{Kind: "deny", Reason: "explicit_denial", Basis: &basis}
	o, e := run(r, in)
	if e != nil || o.Action != "refuse" || o.PreparedWorkbench != nil {
		t.Fatalf("explicit deny %+v/%v", o, e)
	}
	r, in = fixture("reuse")
	gate(&in, "occupancy").Evidence = Evidence{Kind: "deny", Reason: "explicit_denial", Basis: &basis}
	if o, e := run(r, in); e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
		t.Fatal("asserted deny on observed-only gate")
	}
	for _, mutation := range []func(*Evidence){
		func(x *Evidence) { x.Source = &Source{} }, func(x *Evidence) { x.AssertedBy = "@manager:two.example" }, func(x *Evidence) { x.RecordedAt = nil }, func(x *Evidence) { x.RecordedAt = pointer(now.Add(time.Nanosecond)) }, func(x *Evidence) { x.Statement = "" }, func(x *Evidence) { x.Statement = strings.Repeat("a", 4097) },
	} {
		r, in = fixture("reuse")
		mutation(&gate(&in, "caller_authority").Evidence)
		if o, e := run(r, in); e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
			t.Fatal("malformed assertion allowed")
		}
	}
}

// A live or unknown seat cannot be made absent by caller statements. The four
// common statements alone do not change its observed running/starting state.
func TestBusyAndUnknownState(t *testing.T) {
	for _, state := range []string{"running", "starting", "unknown", "conflict"} {
		r, in := fixture("reuse")
		in.Targets[0].Binding.State = state
		for i := range in.Targets[0].Checks {
			g := &in.Targets[0].Checks[i]
			if !contains(common, g.Gate) {
				g.Evidence = Evidence{Kind: "unknown", Reason: "not_required"}
			}
		}
		bind(r, &in)
		o, e := run(r, in)
		want := "defer"
		if state == "unknown" || state == "conflict" {
			want = "insufficient_evidence"
		}
		if e != nil || o.Action != want || o.PreparedWorkbench != nil {
			t.Fatalf("state%s %+v/%v", state, o, e)
		}
	}
}

// Maximum accepted text/identity bounds retain valid opaque punctuation and
// multilingual data; unsafe display scalars or over-budget frames reject.
func TestTextAndRequestBudgets(t *testing.T) {
	for _, s := range []string{"with space / * : =", "Հայերեն", "русский", strings.Repeat("a", 256)} {
		if !ref(s) {
			t.Fatalf("valid ref rejected: %q", s)
		}
	}
	for _, s := range []string{"", strings.Repeat("a", 257), "x\x00y", "x\u202ey", "x\u200by", "x\ue000y", "x\uffffy", "x\u2028y", string([]byte{0xff})} {
		if ref(s) {
			t.Fatalf("unsafe ref accepted: %q", s)
		}
	}
	r, in := fixture("reuse")
	statement := strings.Repeat("a", 4096)
	gate(&in, "caller_authority").Evidence.Statement = statement
	r.Objective = statement
	bind(r, &in)
	o, e := run(r, in)
	if e != nil || o.Action != "reuse" {
		t.Fatalf("valid text limit %+v/%v", o, e)
	}
	if _, e := o.Bytes(); e != nil {
		t.Fatal("valid maximum objective could not be encoded", e)
	}
	// HTML-safe JSON escaping is part of canonical byte accounting. This valid
	// per-field input crosses the complete request limit; it must fail before IO.
	base, _ := fixture("start")
	r = base
	r.Cards = nil
	r.Configurations = nil
	for i := 0; i < 16; i++ {
		c := base.Configurations[0]
		c.Member = fmt.Sprintf("member%d", i)
		c.MXID = fmt.Sprintf("@worker%d:one.example", i)
		c.Runtime = strings.Repeat("<", 256)
		c.Vendor = c.Runtime
		c.Model = c.Runtime
		c.Profile = c.Runtime
		c.Placement = c.Runtime
		c.Effort = c.Runtime
		r.Cards = append(r.Cards, Card{c.Member, c.MXID, base.Cards[0].SHA256})
		r.Configurations = append(r.Configurations, c)
	}
	if len(encode(r)) <= MaxRequestBytes {
		t.Fatal("oversize control was not applied")
	}
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := LocalPlanner{Local: LocalContext{r.Caller, r.Project}, Room: "!room:one.example", Clock: func() time.Time { return now }, Reader: readerFunc(func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error) {
		calls++
		return in, nil
	})}
	o, e = p.Recommend(ctx, r)
	if e == nil || e.Error() != "invalid_request" || calls != 0 || o.PreparedWorkbench != nil {
		t.Fatal("request budget did not reject before acquisition")
	}
}

// The complete snapshot cap is independent of per-statement bounds. No valid
// prefix of an oversized reader result can yield a prepared workbench.
func TestCompleteSnapshotBudget(t *testing.T) {
	r, in := fixture("start")
	in.Targets = nil
	r.Cards = nil
	r.Configurations = nil
	for i := 0; i < 16; i++ {
		rr, ii := alternative("start", fmt.Sprintf("member%d", i), fmt.Sprintf("@worker%d:one.example", i))
		r.Cards = append(r.Cards, rr.Cards...)
		r.Configurations = append(r.Configurations, rr.Configurations...)
		for j := range ii.Targets[0].Checks {
			e := &ii.Targets[0].Checks[j].Evidence
			if e.Kind == "caller-asserted" {
				e.Statement = strings.Repeat("<", 4096)
			}
		}
		in.Targets = append(in.Targets, ii.Targets...)
	}
	bind(r, &in)
	if len(encode(in)) <= MaxSnapshotBytes {
		t.Fatal("oversize snapshot plant not applied")
	}
	o, e := run(r, in)
	if e == nil || e.Error() != "output_budget" || o.PreparedWorkbench != nil || o.Selected != nil {
		t.Fatalf("partial positive on oversized source %+v/%v", o, e)
	}
}

// Dynamic source currentness belongs to the admitted reader, but the planner
// independently rejects malformed bindings/times and preserves source UNKNOWN.
func TestSourceAndConfigurationBoundaries(t *testing.T) {
	for _, mutate := range []func(*LocalOwnerInputs){
		func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Source.Kind = "caller_statement" },
		func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Source.Locator = string([]byte{0xff}) },
		func(x *LocalOwnerInputs) { gate(x, "caller_authority").Evidence.Statement = string([]byte{0xff}) },
		func(x *LocalOwnerInputs) {
			gate(x, "card_member").Evidence.Projection.Declaration.Role = string([]byte{0xff})
		},
		func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Source.ContentSHA256 = "bad" },
		func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Source.ReadAt = now.Add(time.Nanosecond) },
		func(x *LocalOwnerInputs) {
			gate(x, "current_configuration").Evidence.Projection.Live.Configuration.Model = "wrong"
		},
		func(x *LocalOwnerInputs) {
			gate(x, "current_configuration").Evidence.Projection.Live.Execution.Generation = "wrong"
		},
		func(x *LocalOwnerInputs) {
			gate(x, "ready_response").Evidence.Projection.Response.Sender = "@worker:two.example"
		},
		func(x *LocalOwnerInputs) {
			gate(x, "ready_response").Evidence.Projection.Response.To = "@manager:two.example"
		},
		func(x *LocalOwnerInputs) {
			gate(x, "ready_response").Evidence.Projection.Response.TaskID = "TASK-other"
		},
		func(x *LocalOwnerInputs) {
			gate(x, "ready_response").Evidence.Projection.Response.RequestDigest = strings.Repeat("e", 64)
		},
	} {
		r, in := fixture("reuse")
		mutate(&in)
		o, e := run(r, in)
		if e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
			t.Fatalf("source conflict promoted %+v/%v", o, e)
		}
	}
	for _, reason := range []string{"missing", "unavailable", "partial", "stale", "expired", "future", "binding_mismatch", "source_time_unknown", "predicate_unproven"} {
		r, in := fixture("reuse")
		gate(&in, "occupancy").Evidence = Evidence{Kind: "unknown", Reason: reason}
		o, e := run(r, in)
		if e != nil || o.Action != "insufficient_evidence" || o.PreparedWorkbench != nil {
			t.Fatalf("unknown%s %+v/%v", reason, o, e)
		}
	}
}

// A real source-backed statement is a distinct observation class. These
// synthetic controls exercise that representation, without authenticating it.
func TestObservedCommonStatements(t *testing.T) {
	for _, action := range []string{"reuse", "start"} {
		r, in := fixture(action)
		for _, g := range gates(action) {
			if !assertionGate(g) {
				continue
			}
			statement := gate(&in, g).Evidence.Statement
			gate(&in, g).Evidence = Evidence{Kind: "observed", Source: &Source{"a2a", "addressed-response:exact", strings.Repeat("c", 64), now, nil, nil}, Projection: &Projection{Subject: Subject{in.Targets[0].Binding.Member, in.Targets[0].Binding.MXID, in.Targets[0].Binding.CardSHA256}, Configuration: pointer(in.Targets[0].Binding.Configuration), TaskID: pointer(r.TaskID), Statement: &statement}}
		}
		o, e := run(r, in)
		if e != nil || o.Action != action {
			t.Fatalf("observed %s %+v/%v", action, o, e)
		}
	}
}

// Invalid typed UTF-8 and cycles are rejected before JSON can repair/replace
// their contents or recursively encode a partial positive snapshot.
func TestTypedEvidenceCycle(t *testing.T) {
	r, in := fixture("reuse")
	e := &gate(&in, "caller_authority").Evidence
	e.Kind = "deny"
	e.Reason = "explicit_denial"
	e.Basis = e
	o, err := run(r, in)
	if err == nil || err.Error() != "source_conflict" || o.PreparedWorkbench != nil {
		t.Fatal("cycle not rejected")
	}
}

// Expiry prevents eligibility without rewriting the source record. Stale
// evidence still has to obey the closed projection and timestamp schema.
func TestExpiredEvidencePreservationAndShape(t *testing.T) {
	r, in := fixture("reuse")
	gate(&in, "occupancy").Evidence.Source.ExpiresAt = pointer(now)
	want := string(encode(gate(&in, "occupancy").Evidence))
	o, err := run(r, in)
	if err != nil || o.Action != "insufficient_evidence" || o.Selected != nil || o.PreparedWorkbench != nil {
		t.Fatalf("expired evidence became eligible: %+v/%v", o, err)
	}
	found := false
	for _, record := range o.Evidence {
		if record.Gate == "occupancy" {
			found = true
			if string(encode(record.Evidence)) != want {
				t.Fatal("expired source record was rewritten")
			}
		}
	}
	if !found {
		t.Fatal("source evidence omitted")
	}
	for _, mutate := range []func(*Evidence){
		func(e *Evidence) { e.Projection.Statement = pointer("unrelated field") },
		func(e *Evidence) { e.Projection.Subject.MXID = "@worker:two.example" },
		func(e *Evidence) { e.Source.ObservedAt = pointer(now.Add(-time.Second + time.Nanosecond)) },
	} {
		bad := clone(in)
		mutate(&gate(&bad, "occupancy").Evidence)
		if o, err := run(r, bad); err == nil || err.Error() != "source_conflict" || o.PreparedWorkbench != nil {
			t.Fatalf("expired malformed evidence accepted: %+v/%v", o, err)
		}
	}
}

// Permuting target and evidence sets preserves the complete canonical result,
// including the selected same-action target and recommendation digest.
func TestCanonicalTargetOrdering(t *testing.T) {
	r, in := alternative("reuse", "z", "@z:one.example")
	rr, ii := alternative("reuse", "a", "@a:one.example")
	r.Cards = append(r.Cards, rr.Cards...)
	r.Configurations = append(r.Configurations, rr.Configurations...)
	in.Targets = append(in.Targets, ii.Targets...)
	bind(r, &in)
	a, err := run(r, in)
	if err != nil || a.Action != "reuse" || a.Selected.Member != "a" {
		t.Fatalf("canonical selection: %+v/%v", a, err)
	}
	in.Targets[0], in.Targets[1] = in.Targets[1], in.Targets[0]
	for _, target := range in.Targets {
		for i, j := 0, len(target.Checks)-1; i < j; i, j = i+1, j-1 {
			target.Checks[i], target.Checks[j] = target.Checks[j], target.Checks[i]
		}
	}
	b, err := run(r, in)
	if err != nil || string(encode(a)) != string(encode(b)) {
		t.Fatalf("permutation changed canonical output: %v", err)
	}
}
