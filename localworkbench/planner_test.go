package localworkbench

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type readerFunc func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error)

func (f readerFunc) Read(c context.Context, r LocalRequest, s *LocalSelection) (LocalOwnerInputs, error) {
	return f(c, r, s)
}
func pointer[T any](v T) *T { return &v }

var now = time.Date(2026, 11, 4, 5, 30, 0, 0, time.UTC)

func fixture(action string) (LocalRequest, LocalOwnerInputs) {
	c := Configuration{"member", "@worker:one.example", "codex", "openai", "model", "high", "profile", strings.Repeat("a", 64), "local"}
	r := LocalRequest{"@manager:one.example", "project", "correlation", "TASK-1", []Card{{c.Member, c.MXID, strings.Repeat("b", 64)}}, []Configuration{c}, "Do the task"}
	d, _ := RequestDigest(r)
	b := Binding{c.Member, c.MXID, r.Cards[0].SHA256, "owned-seat", c, "ready", &Execution{"pid-1", "thread-1", now.Add(-time.Hour), "generation-1"}}
	if action == "start" {
		b.State = "stopped"
		b.Execution = nil
	}
	target := Target{Binding: b}
	td := TargetDigest(b)
	source := func(kind string) *Source {
		return &Source{kind, "source-contract:fixed", strings.Repeat("c", 64), now, pointer(now.Add(-time.Second)), pointer(now.Add(time.Minute))}
	}
	for _, g := range append(append(append([]string{}, common...), reuse...), start...) {
		e := Evidence{Kind: "unknown", Reason: "not_required"}
		if contains(gates(action), g) {
			if assertionGate(g) {
				e = Evidence{Kind: "caller-asserted", AssertedBy: r.Caller, Statement: "I admit " + r.TaskID + " for " + b.MXID + " within this exact request.", RecordedAt: pointer(now)}
			} else {
				p := &Projection{Subject: Subject{b.Member, b.MXID, b.CardSHA256}}
				kind := "a2a"
				switch g {
				case "card_member":
					kind = "pulse_self"
					p.Declaration = &Declaration{"developer", pointer(r.Project)}
				case "profile_runtime":
					kind = "curator_catalog"
					p.Configuration = pointer(c)
				case "quality":
					kind = "curator_catalog"
					p.Configuration = pointer(c)
					p.Qualification = &Qualification{"qualification:exact", strings.Repeat("d", 64), true}
				case "provider_limits":
					kind = "host"
					p.Configuration = pointer(c)
					p.Quota = &Quota{2, 1, "session", "provider/model", true}
				case "messaging_admission":
					p.Membership = &Membership{"!room:one.example", b.MXID, true, true}
				case "current_configuration":
					kind = "pulse_environment"
					p.Configuration = pointer(c)
					p.Live = &LiveConfiguration{*source("a2a"), c, *b.Execution}
				case "exact_generation":
					p.Execution = clone(b.Execution)
				case "ready_response":
					p.Execution = clone(b.Execution)
					p.Response = &Response{b.MXID, r.Caller, r.TaskID, d, true}
				case "occupancy":
					kind = "host"
					p.Execution = clone(b.Execution)
					p.Available = pointer(true)
				case "complete_owned_absence":
					p.Absence = &Absence{b.Seat, b.MXID, "exact-owned-seat", true, true}
				case "launch_capability":
					kind = "curator_catalog"
					p.Capability = &Capability{"curator:qualified", c, true}
				}
				e = Evidence{Kind: "observed", Source: source(kind), Projection: p}
			}
		}
		target.Checks = append(target.Checks, GateRecord{g, d, td, e})
	}
	return r, LocalOwnerInputs{Contract, d, "partial", []Target{target}}
}
func gate(in *LocalOwnerInputs, g string) *GateRecord {
	for i := range in.Targets[0].Checks {
		if in.Targets[0].Checks[i].Gate == g {
			return &in.Targets[0].Checks[i]
		}
	}
	panic(g)
}
func run(r LocalRequest, in LocalOwnerInputs) (LocalRecommendation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := LocalPlanner{Local: LocalContext{r.Caller, r.Project}, Room: "!room:one.example", Clock: func() time.Time { return now }, Reader: readerFunc(func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error) { return in, nil })}
	return p.Recommend(ctx, r)
}

// These synthetic controls prove action relevance and inert output only. No
// fixture statement/source establishes real source, target or launch admission.
func TestActionRelevanceAndInertOutput(t *testing.T) {
	for _, action := range []string{"reuse", "start"} {
		t.Run(action, func(t *testing.T) {
			r, in := fixture(action)
			before := string(encode(in))
			out, err := run(r, in)
			if err != nil || out.Action != action || out.Selected == nil || out.PreparedWorkbench == nil || out.ExecutionAuthority {
				t.Fatalf("%+v / %v", out, err)
			}
			if string(encode(in)) != before {
				t.Fatal("reader input mutated")
			}
			count := 0
			for _, g := range out.Evidence {
				if g.Evidence.Kind == "caller-asserted" {
					count++
				}
			}
			want := 4
			if action == "start" {
				want = 5
			}
			if count != want {
				t.Fatalf("assertions %d != %d", count, want)
			}
			if out.PreparedWorkbench.DeliveryDraft.Recipient != r.Cards[0].MXID || !strings.Contains(out.PreparedWorkbench.DeliveryDraft.Text, r.TaskID) {
				t.Fatal("draft binding")
			}
			for _, g := range append(append(append([]string{}, common...), reuse...), start...) {
				t.Run("omit-"+g, func(t *testing.T) {
					bad := clone(in)
					for i, x := range bad.Targets[0].Checks {
						if x.Gate == g {
							bad.Targets[0].Checks = append(bad.Targets[0].Checks[:i], bad.Targets[0].Checks[i+1:]...)
							break
						}
					}
					o, e := run(r, bad)
					if e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
						t.Fatalf("omission accepted %s", g)
					}
				})
			}
			for _, g := range append(append(append([]string{}, common...), reuse...), start...) {
				t.Run("assert-"+g, func(t *testing.T) {
					bad := clone(in)
					gate(&bad, g).Evidence = clone(gate(&bad, "caller_authority").Evidence)
					o, e := run(r, bad)
					if assertionGate(g) && contains(gates(action), g) {
						if e != nil || o.Action != action {
							t.Fatalf("valid assertion rejected %v", e)
						}
					} else if e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
						t.Fatal("forbidden assertion accepted")
					}
				})
			}
		})
	}
}

// Removing authority, current dynamic evidence, or full identity cannot leave a
// positive draft. Nearby complete fixtures above establish positive eligibility.
func TestEvidenceRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*LocalOwnerInputs)
		want   string
	}{
		{"authority", func(x *LocalOwnerInputs) {
			gate(x, "caller_authority").Evidence = Evidence{Kind: "unknown", Reason: "missing"}
		}, "insufficient_evidence"},
		{"occupancy", func(x *LocalOwnerInputs) {
			gate(x, "occupancy").Evidence = Evidence{Kind: "unknown", Reason: "partial"}
		}, "insufficient_evidence"},
		{"expired", func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Source.ExpiresAt = pointer(now) }, "insufficient_evidence"},
		{"future", func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Source.ReadAt = now.Add(time.Second) }, "insufficient_evidence"},
		{"unknown-time", func(x *LocalOwnerInputs) {
			gate(x, "occupancy").Evidence.Source.ObservedAt = nil
			gate(x, "occupancy").Evidence.Source.ExpiresAt = nil
		}, "insufficient_evidence"},
		{"wrong-domain", func(x *LocalOwnerInputs) {
			gate(x, "messaging_admission").Evidence.Projection.Membership.MXID = "@worker:two.example"
		}, "error"},
		{"wrong-target", func(x *LocalOwnerInputs) { gate(x, "occupancy").TargetDigest = strings.Repeat("e", 64) }, "error"},
		{"wrong-request", func(x *LocalOwnerInputs) { gate(x, "occupancy").RequestDigest = strings.Repeat("e", 64) }, "error"},
		{"wrong-generation", func(x *LocalOwnerInputs) {
			gate(x, "ready_response").Evidence.Projection.Execution.Generation = "other"
		}, "error"},
		{"wrong-lock", func(x *LocalOwnerInputs) {
			gate(x, "profile_runtime").Evidence.Projection.Configuration.ProfileLock = strings.Repeat("e", 64)
		}, "error"},
		{"qualification-missing", func(x *LocalOwnerInputs) { gate(x, "quality").Evidence = Evidence{Kind: "unknown", Reason: "missing"} }, "insufficient_evidence"},
		{"not-accepted", func(x *LocalOwnerInputs) { gate(x, "ready_response").Evidence.Projection.Response.Accepted = false }, "error"},
		{"irrelevant-observed", func(x *LocalOwnerInputs) {
			gate(x, "preparation_authority").Evidence = clone(gate(x, "card_member").Evidence)
		}, "error"},
		{"duplicate", func(x *LocalOwnerInputs) { x.Targets[0].Checks[0] = x.Targets[0].Checks[1] }, "error"},
		{"extra-projection", func(x *LocalOwnerInputs) {
			gate(x, "card_member").Evidence.Projection.Statement = pointer("not allowed")
		}, "error"},
		{"bad-kind", func(x *LocalOwnerInputs) { gate(x, "occupancy").Evidence.Kind = "allow" }, "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, in := fixture("reuse")
			c.change(&in)
			o, e := run(r, in)
			if c.want == "error" {
				if e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
					t.Fatalf("wanted rejection got %+v/%v", o, e)
				}
			} else if e != nil || o.Action != c.want || o.PreparedWorkbench != nil || o.Selected != nil {
				t.Fatalf("%+v/%v", o, e)
			}
		})
	}
}

// Caller/project binding and bounded cancellation fail before acquisition;
// source failures and cancellation during acquisition cannot return a draft.
func TestAcquisitionBoundary(t *testing.T) {
	r, in := fixture("reuse")
	calls := 0
	p := LocalPlanner{Local: LocalContext{r.Caller, r.Project}, Room: "!room:one.example", Clock: func() time.Time { return now }, Reader: readerFunc(func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error) {
		calls++
		return in, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	bad := r
	bad.Caller = "@other:one.example"
	if _, e := p.Recommend(ctx, bad); e == nil || calls != 0 {
		t.Fatal("caller mismatch acquired")
	}
	if _, e := p.Recommend(context.Background(), r); e == nil || calls != 0 {
		t.Fatal("unbounded acquired")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, e := p.Recommend(canceled, r); e == nil || calls != 0 {
		t.Fatal("canceled acquired")
	}
	p.Reader = readerFunc(func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error) {
		return LocalOwnerInputs{}, errors.New("private detail")
	})
	o, e := p.Recommend(ctx, r)
	if e == nil || e.Error() != "source_unavailable" || o.PreparedWorkbench != nil {
		t.Fatal("source error leaked/promoted")
	}
	p.Reader = readerFunc(func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error) {
		cancel()
		return in, nil
	})
	o, e = p.Recommend(ctx, r)
	if e == nil || o.PreparedWorkbench != nil {
		t.Fatal("post-read cancellation")
	}
}
