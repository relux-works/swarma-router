package selection

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var instant = time.Date(2026, 11, 2, 13, 45, 0, 0, time.UTC)

// Independent fixture enumerates the design's required checks, rather than using
// the implementation's lists as the test oracle.
func fixture(state State, id string) Input {
	b := Basis{"manager", "project", "request-digest", "task-r1", "cards-digest", "policy-r1", "inventory-digest", "qualification-digest"}
	c := Candidate{Identity: Identity{id, "member-" + id, "workbench-" + id, "card-r1", Configuration{"codex", "openai", "qualified-model", "high", "profile", "lock-digest"}}, State: state}
	checks := []Gate{"caller_authority", "task_constraints", "card_member", "profile_runtime", "quality", "provider_limits", "manager_custody", "board_assignment", "messaging_admission"}
	if state == Ready {
		checks = append(checks, "current_configuration", "exact_generation", "ready_response", "occupancy")
	} else {
		checks = append(checks, "complete_owned_absence", "preparation_authority", "launch_capability")
	}
	for _, gate := range checks {
		c.Checks = append(c.Checks, Check{Gate: gate, Verdict: Allow, Evidence: Evidence{"source-" + string(gate), b, c.Identity, instant.Add(-time.Minute), instant.Add(time.Minute)}})
	}
	return Input{Basis: b, Scope: Partial, Now: instant, Candidates: []Candidate{c}}
}

func edit(in *Input, gate Gate, mutate func(*Check)) {
	for i := range in.Candidates[0].Checks {
		if in.Candidates[0].Checks[i].Gate == gate {
			mutate(&in.Candidates[0].Checks[i])
			return
		}
	}
	panic("fixture gate missing")
}

// Required positive proposals retain exact bindings/scope/evidence but confer no
// readiness or execution authority; all inputs remain caller-owned.
func TestPositiveProposals(t *testing.T) {
	for _, row := range []struct {
		state  State
		action Action
	}{{Ready, Reuse}, {Stopped, Start}} {
		in := fixture(row.state, "a")
		before := fixture(row.state, "a")
		got := Select(in)
		if got.Action != row.action || got.Reason != "eligible" || got.Selected == nil || *got.Selected != in.Candidates[0].Identity || got.Basis != in.Basis || got.Scope != Partial || !got.ValidUntil.Equal(instant.Add(time.Minute)) || len(got.Evidence) != len(in.Candidates[0].Checks) {
			t.Fatalf("unexpected proposal: %+v", got)
		}
		if !reflect.DeepEqual(in, before) {
			t.Fatal("input mutated")
		}
		got.Selected.Member = "changed"
		got.Evidence[0].Reference = "changed"
		if !reflect.DeepEqual(in, before) {
			t.Fatal("result aliases input")
		}
	}
}

// Preference runs after eligibility, and deterministic ties do not depend on
// source ordering or unknown facts on an unselected alternative.
func TestEligibilityBeforePreference(t *testing.T) {
	for _, state := range []Verdict{Allow, Deny, Unknown, Conflict} {
		in := fixture(Ready, "z")
		edit(&in, "quality", func(c *Check) { c.Verdict = state })
		in.Candidates = append(in.Candidates, fixture(Stopped, "a").Candidates[0])
		want := Start
		if state == Allow {
			want = Reuse
		}
		for pass := 0; pass < 2; pass++ {
			got := Select(in)
			if got.Action != want {
				t.Fatalf("%s: got %+v", state, got)
			}
			in.Candidates[0], in.Candidates[1] = in.Candidates[1], in.Candidates[0]
		}
	}
	for _, state := range []State{Ready, Stopped} {
		in := fixture(state, "z")
		in.Candidates = append(in.Candidates, fixture(state, "a").Candidates[0])
		for pass := 0; pass < 2; pass++ {
			if got := Select(in); got.Selected == nil || got.Selected.ID != "a" {
				t.Fatalf("tie: %+v", got)
			}
			in.Candidates[0], in.Candidates[1] = in.Candidates[1], in.Candidates[0]
		}
	}
}

// Every required gate has nearby allow controls above. Each deny excludes its
// candidate; unknown/conflict/missing evidence cannot be converted to approval.
func TestEveryRequiredGate(t *testing.T) {
	for _, state := range []State{Ready, Stopped} {
		for _, check := range fixture(state, "a").Candidates[0].Checks {
			for _, verdict := range []Verdict{Deny, Unknown, Conflict} {
				t.Run(string(state)+"/"+string(check.Gate)+"/"+string(verdict), func(t *testing.T) {
					in := fixture(state, "a")
					edit(&in, check.Gate, func(c *Check) { c.Verdict = verdict })
					want := InsufficientEvidence
					if verdict == Deny {
						want = Refuse
					}
					assertNoSelection(t, Select(in), want)
				})
			}
			in := fixture(state, "a")
			for i, c := range in.Candidates[0].Checks {
				if c.Gate == check.Gate {
					in.Candidates[0].Checks = append(in.Candidates[0].Checks[:i], in.Candidates[0].Checks[i+1:]...)
					break
				}
			}
			assertNoSelection(t, Select(in), InsufficientEvidence)
		}
	}
}

// No declaration, mismatched witness, collection-only timestamp or stale receipt
// can supply the exact fresh normalized requirement needed for a proposal.
func TestWitnessRefusals(t *testing.T) {
	rows := []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"missing-reference", func(e *Evidence) { e.Reference = "" }},
		{"foreign-caller", func(e *Evidence) { e.Basis.Caller = "other" }},
		{"foreign-project", func(e *Evidence) { e.Basis.Project = "other" }},
		{"request-drift", func(e *Evidence) { e.Basis.RequestDigest = "other" }},
		{"task-drift", func(e *Evidence) { e.Basis.TaskRevision = "other" }},
		{"cards-drift", func(e *Evidence) { e.Basis.CardsDigest = "other" }},
		{"policy-drift", func(e *Evidence) { e.Basis.PolicyRevision = "other" }},
		{"inventory-drift", func(e *Evidence) { e.Basis.InventoryDigest = "other" }},
		{"qualification-drift", func(e *Evidence) { e.Basis.QualificationDigest = "other" }},
		{"candidate-drift", func(e *Evidence) { e.Candidate.ID = "other" }},
		{"member-drift", func(e *Evidence) { e.Candidate.Member = "other" }},
		{"workbench-drift", func(e *Evidence) { e.Candidate.Workbench = "other" }},
		{"card-drift", func(e *Evidence) { e.Candidate.CardRevision = "other" }},
		{"runtime-drift", func(e *Evidence) { e.Candidate.Configuration.Runtime = "other" }},
		{"vendor-drift", func(e *Evidence) { e.Candidate.Configuration.Vendor = "other" }},
		{"model-drift", func(e *Evidence) { e.Candidate.Configuration.Model = "other" }},
		{"effort-drift", func(e *Evidence) { e.Candidate.Configuration.Effort = "other" }},
		{"profile-drift", func(e *Evidence) { e.Candidate.Configuration.Profile = "other" }},
		{"lock-drift", func(e *Evidence) { e.Candidate.Configuration.Lock = "other" }},
		{"no-observation", func(e *Evidence) { e.ObservedAt = time.Time{} }},
		{"future-observation", func(e *Evidence) { e.ObservedAt = instant.Add(time.Nanosecond) }},
		{"no-expiry", func(e *Evidence) { e.ExpiresAt = time.Time{} }},
		{"expiry-boundary", func(e *Evidence) { e.ExpiresAt = instant }},
		{"stale", func(e *Evidence) { e.ExpiresAt = instant.Add(-time.Second) }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			in := fixture(Ready, "a")
			edit(&in, "ready_response", func(c *Check) { row.mutate(&c.Evidence) })
			assertNoSelection(t, Select(in), InsufficientEvidence)
		})
	}
	in := fixture(Ready, "a")
	edit(&in, "ready_response", func(c *Check) { c.Evidence.ExpiresAt = instant.Add(time.Second) })
	if got := Select(in); got.Action != Reuse || !got.ValidUntil.Equal(instant.Add(time.Second)) {
		t.Fatalf("earliest expiry lost: %+v", got)
	}
}

// Optional facts outside the selected action are not prerequisites. Unknown
// configuration facts cannot prevent a fully evidenced start, nor prove reuse.
func TestOnlyRelevantRequirements(t *testing.T) {
	in := fixture(Stopped, "a")
	in.Candidates[0].Checks = append(in.Candidates[0].Checks, Check{Gate: "current_configuration", Verdict: Unknown})
	if got := Select(in); got.Action != Start || len(got.Evidence) != 12 {
		t.Fatalf("optional gated start: %+v", got)
	}
	in = fixture(Ready, "a")
	in.Candidates[0].Checks = append(in.Candidates[0].Checks, Check{Gate: "complete_owned_absence", Verdict: Deny})
	if got := Select(in); got.Action != Reuse {
		t.Fatalf("unneeded absence gated reuse: %+v", got)
	}
}

// A live/starting workbench has no change transition in this component; missing
// rows and unknown source state are never positive absence or readiness.
func TestStateAndScope(t *testing.T) {
	for _, row := range []struct {
		state  State
		action Action
	}{{Running, Defer}, {Starting, Defer}, {StateUnknown, InsufficientEvidence}, {StateConflict, InsufficientEvidence}} {
		in := fixture(row.state, "a")
		assertNoSelection(t, Select(in), row.action)
	}
	for _, scope := range []Scope{Partial, Complete} {
		in := fixture(Stopped, "a")
		in.Scope = scope
		in.Candidates = nil
		got := Select(in)
		assertNoSelection(t, got, Refuse)
		if got.Scope != scope || got.Reason != "no_eligible_candidate" {
			t.Fatalf("invented absence/completeness: %+v", got)
		}
	}
}

// Alternative tuples for the same positively stopped seat are legitimate;
// contradictory live/state/card/workbench declarations fail closed instead.
func TestSameMemberAlternatives(t *testing.T) {
	for _, row := range []struct {
		name   string
		mutate func(*Candidate)
		valid  bool
	}{
		{"stopped-model-options", func(c *Candidate) { c.Configuration.Model = "other" }, true},
		{"live-versus-stopped", func(c *Candidate) { c.State = Running }, false},
		{"different-workbench", func(c *Candidate) { c.Workbench = "other" }, false},
		{"different-card", func(c *Candidate) { c.CardRevision = "other" }, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			in := fixture(Stopped, "a")
			other := in.Candidates[0]
			other.Checks = append([]Check(nil), other.Checks...)
			other.ID = "b"
			row.mutate(&other)
			for i := range other.Checks {
				other.Checks[i].Evidence.Candidate = other.Identity
			}
			in.Candidates = append(in.Candidates, other)
			got := Select(in)
			if row.valid {
				if got.Action != Start {
					t.Fatalf("valid options: %+v", got)
				}
			} else {
				assertNoSelection(t, got, Refuse)
				if got.Reason != "invalid_input" {
					t.Fatalf("contradiction: %+v", got)
				}
			}
		})
	}
	in := fixture(Ready, "a")
	other := in.Candidates[0]
	other.ID = "b"
	other.Configuration.Model = "other"
	in.Candidates = append(in.Candidates, other)
	assertNoSelection(t, Select(in), Refuse)
}

// References are opaque UTF-8 data with a byte bound, never paths/argv to run.
// Evidence validity uses the earliest expiry; gate ordering does not change it.
func TestReferenceAndTimeBoundaries(t *testing.T) {
	in := fixture(Ready, "a")
	edit(&in, "ready_response", func(c *Check) { c.Evidence.Reference = strings.Repeat("é", 256); c.Evidence.ObservedAt = instant })
	want := Select(in)
	if want.Action != Reuse {
		t.Fatalf("valid boundary: %+v", want)
	}
	for i, j := 0, len(in.Candidates[0].Checks)-1; i < j; i, j = i+1, j-1 {
		in.Candidates[0].Checks[i], in.Candidates[0].Checks[j] = in.Candidates[0].Checks[j], in.Candidates[0].Checks[i]
	}
	if got := Select(in); !reflect.DeepEqual(got, want) {
		t.Fatal("gate order changed proposal")
	}
	edit(&in, "ready_response", func(c *Check) { c.Evidence.Reference += "a" })
	assertNoSelection(t, Select(in), InsufficientEvidence)
	in = fixture(Ready, "a")
	edit(&in, "quality", func(c *Check) { c.Verdict = Deny })
	edit(&in, "ready_response", func(c *Check) { c.Verdict = Unknown })
	assertNoSelection(t, Select(in), Refuse)
}

// Closed input structure rejects malformed identifiers, ambiguous duplicate
// candidates/checks and unknown enums; diagnostics never echo their payload.
func TestInvalidInput(t *testing.T) {
	rows := []struct {
		name   string
		mutate func(*Input)
	}{
		{"caller", func(i *Input) { i.Basis.Caller = "" }},
		{"clock", func(i *Input) { i.Now = time.Time{} }},
		{"scope", func(i *Input) { i.Scope = "invented-secret" }},
		{"state", func(i *Input) { i.Candidates[0].State = "invented-secret" }},
		{"empty-model", func(i *Input) { i.Candidates[0].Configuration.Model = "" }},
		{"control", func(i *Input) { i.Candidates[0].Member = "private\nsecret" }},
		{"invalid-utf8", func(i *Input) { i.Basis.Project = string([]byte{0xff}) }},
		{"long-reference", func(i *Input) { i.Basis.RequestDigest = string(make([]byte, 513)) }},
		{"duplicate-candidate", func(i *Input) { i.Candidates = append(i.Candidates, i.Candidates[0]) }},
		{"duplicate-check", func(i *Input) { i.Candidates[0].Checks = append(i.Candidates[0].Checks, i.Candidates[0].Checks[0]) }},
		{"unknown-check", func(i *Input) { i.Candidates[0].Checks[0].Gate = "invented-secret" }},
		{"unknown-verdict", func(i *Input) { i.Candidates[0].Checks[0].Verdict = "invented-secret" }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			in := fixture(Ready, "a")
			row.mutate(&in)
			got := Select(in)
			assertNoSelection(t, got, Refuse)
			if got.Reason != "invalid_input" {
				t.Fatalf("unbounded reason: %+v", got)
			}
		})
	}
}

func assertNoSelection(t *testing.T, p Proposal, action Action) {
	t.Helper()
	if p.Action != action || p.Selected != nil || !p.ValidUntil.IsZero() || len(p.Evidence) != 0 {
		t.Fatalf("expected %s without selected data, got %+v", action, p)
	}
}
