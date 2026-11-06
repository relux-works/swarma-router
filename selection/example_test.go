package selection_test

import (
	"fmt"
	"time"

	"relux.works/swarma-router/selection"
)

// Select proposes reuse only with all thirteen fresh, exactly bound checks.
// Making just the ready-response evidence unknown withholds selection. These
// synthetic allow witnesses illustrate normalized input, not source verification
// or permission to launch, assign or deliver work.
func ExampleSelect() {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	basis := selection.Basis{
		Caller: "example-caller", Project: "example-project", RequestDigest: "request-digest",
		TaskRevision: "task-r1", CardsDigest: "cards-digest", PolicyRevision: "policy-r1",
		InventoryDigest: "inventory-digest", QualificationDigest: "qualification-digest",
	}
	candidate := selection.Candidate{
		Identity: selection.Identity{
			ID: "candidate-a", Member: "example-member", Workbench: "example-workbench", CardRevision: "card-r1",
			Configuration: selection.Configuration{
				Runtime: "codex_cli", Vendor: "example-vendor", Model: "example-model",
				Effort: "high", Profile: "example-profile", Lock: "example-lock",
			},
		},
		State: selection.Ready,
	}
	// The checks come from the documented reuse contract, not implementation lists.
	for _, gate := range []selection.Gate{
		"caller_authority", "task_constraints", "card_member", "profile_runtime", "quality",
		"provider_limits", "manager_custody", "board_assignment", "messaging_admission",
		"current_configuration", "exact_generation", "ready_response", "occupancy",
	} {
		candidate.Checks = append(candidate.Checks, selection.Check{
			Gate: gate, Verdict: selection.Allow,
			Evidence: selection.Evidence{
				Reference: "example-witness-" + string(gate), Basis: basis, Candidate: candidate.Identity,
				ObservedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
			},
		})
	}
	in := selection.Input{Basis: basis, Scope: selection.Partial, Now: now, Candidates: []selection.Candidate{candidate}}
	good := selection.Select(in)
	fmt.Println(good.Action, good.Reason, good.Selected.ID, good.Scope)
	fmt.Println("valid until:", good.ValidUntil.Format(time.RFC3339))

	for i := range in.Candidates[0].Checks {
		if in.Candidates[0].Checks[i].Gate == "ready_response" {
			in.Candidates[0].Checks[i].Verdict = selection.Unknown
			in.Candidates[0].Checks[i].Evidence = selection.Evidence{}
		}
	}
	unknown := selection.Select(in)
	fmt.Println(unknown.Action, unknown.Reason, "selected absent:", unknown.Selected == nil)
	fmt.Println("no validity:", unknown.ValidUntil.IsZero(), "evidence:", len(unknown.Evidence))
	// Output:
	// reuse eligible candidate-a partial
	// valid until: 2026-01-02T12:01:00Z
	// insufficient_evidence required_evidence_unavailable selected absent: true
	// no validity: true evidence: 0
}
