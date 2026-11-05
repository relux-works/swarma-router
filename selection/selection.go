// Package selection proposes reuse or start from trusted, normalized evidence.
// It performs no source observation or execution, and authenticates no caller.
package selection

import (
	"sort"
	"time"
	"unicode"
	"unicode/utf8"
)

type State string

const (
	Ready         State = "ready"
	Stopped       State = "stopped"
	Running       State = "running"
	Starting      State = "starting"
	StateUnknown  State = "unknown"
	StateConflict State = "conflict"
)

type Action string

const (
	Reuse                Action = "reuse"
	Start                Action = "start"
	Defer                Action = "defer"
	InsufficientEvidence Action = "insufficient_evidence"
	Refuse               Action = "refuse"
)

type Scope string

const (
	Partial  Scope = "partial"
	Complete Scope = "complete"
)

type Verdict string

const (
	Allow    Verdict = "allow"
	Deny     Verdict = "deny"
	Unknown  Verdict = "unknown"
	Conflict Verdict = "conflict"
)

// Gate names are normalized requirements, not source commands or permissions.
type Gate string

var common = []Gate{"caller_authority", "task_constraints", "card_member", "profile_runtime", "quality", "provider_limits", "manager_custody", "board_assignment", "messaging_admission"}
var reuse = []Gate{"current_configuration", "exact_generation", "ready_response", "occupancy"}
var start = []Gate{"complete_owned_absence", "preparation_authority", "launch_capability"}

// Basis identifies immutable normalized inputs. Digests/revisions are opaque
// source references; their presence is not cryptographic verification.
type Basis struct {
	Caller, Project, RequestDigest, TaskRevision                      string
	CardsDigest, PolicyRevision, InventoryDigest, QualificationDigest string
}

type Configuration struct {
	Runtime, Vendor, Model, Effort, Profile, Lock string
}

type Identity struct {
	ID, Member, Workbench, CardRevision string
	Configuration
}

// Evidence must come from an admitted normalizer. The normalizer must preserve
// unknown source observation/expiry, rather than manufacture them at collection.
// Binding an arbitrary string or decoding JSON does not establish source trust.
type Evidence struct {
	Reference             string
	Basis                 Basis
	Candidate             Identity
	ObservedAt, ExpiresAt time.Time
}

type Check struct {
	Gate     Gate
	Verdict  Verdict
	Evidence Evidence
}

type Candidate struct {
	Identity
	State  State
	Checks []Check
}

// Input is an in-process trusted normalized projection, not an authenticated
// wire request. Now and source scope are explicit; no ambient clock is read.
type Input struct {
	Basis      Basis
	Scope      Scope
	Now        time.Time
	Candidates []Candidate
}

type EvidenceRef struct {
	Gate                  Gate
	Reference             string
	ObservedAt, ExpiresAt time.Time
}

// Proposal grants no launch, assignment, reservation or readiness authority.
// Execution must independently revalidate the bound sources and existing custody.
type Proposal struct {
	Action     Action
	Reason     string
	Basis      Basis
	Scope      Scope
	Selected   *Identity
	ValidUntil time.Time
	Evidence   []EvidenceRef
}

// Select checks hard eligibility before applying reuse > start preference.
// Equal-action candidates sort by opaque ID, without a quality/price score or
// project-wide optimality claim. The function does not mutate or retain input.
func Select(in Input) Proposal {
	p := Proposal{Action: Refuse, Reason: "invalid_input", Basis: in.Basis, Scope: in.Scope}
	if !validInput(in) {
		return p
	}
	p.Reason = "no_eligible_candidate"
	type option struct {
		candidate Identity
		action    Action
		until     time.Time
		evidence  []EvidenceRef
	}
	var options []option
	var incomplete, live bool
	for _, c := range in.Candidates {
		if c.State == Running || c.State == Starting {
			live = true
			continue
		}
		if c.State == StateUnknown || c.State == StateConflict {
			incomplete = true
			continue
		}
		action, required := Reuse, reuse
		if c.State == Stopped {
			action, required = Start, start
		}
		checks := make(map[Gate]Check, len(c.Checks))
		for _, check := range c.Checks {
			checks[check.Gate] = check
		}
		var refs []EvidenceRef
		var until time.Time
		denied, missing := false, false
		for _, gates := range [][]Gate{common, required} {
			for _, gate := range gates {
				check, present := checks[gate]
				if present && check.Verdict == Deny {
					denied = true
					continue
				}
				if !present || check.Verdict != Allow || !fresh(check.Evidence, in, c.Identity) {
					missing = true
					continue
				}
				e := check.Evidence
				refs = append(refs, EvidenceRef{gate, e.Reference, e.ObservedAt, e.ExpiresAt})
				if until.IsZero() || e.ExpiresAt.Before(until) {
					until = e.ExpiresAt
				}
			}
		}
		if denied {
			continue
		}
		if missing {
			incomplete = true
			continue
		}
		options = append(options, option{c.Identity, action, until, refs})
	}
	if len(options) == 0 {
		if incomplete {
			p.Action, p.Reason = InsufficientEvidence, "required_evidence_unavailable"
		} else if live {
			p.Action, p.Reason = Defer, "change_unavailable"
		}
		return p
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].action != options[j].action {
			return options[i].action == Reuse
		}
		return options[i].candidate.ID < options[j].candidate.ID
	})
	chosen := options[0]
	p.Action, p.Reason, p.Selected, p.ValidUntil, p.Evidence = chosen.action, "eligible", &chosen.candidate, chosen.until, chosen.evidence
	return p
}

func fresh(e Evidence, in Input, candidate Identity) bool {
	return validRef(e.Reference) && e.Basis == in.Basis && e.Candidate == candidate && !e.ObservedAt.IsZero() && !e.ObservedAt.After(in.Now) && e.ExpiresAt.After(in.Now)
}

func validInput(in Input) bool {
	b := in.Basis
	if !refs(b.Caller, b.Project, b.RequestDigest, b.TaskRevision, b.CardsDigest, b.PolicyRevision, b.InventoryDigest, b.QualificationDigest) || in.Now.IsZero() || (in.Scope != Partial && in.Scope != Complete) {
		return false
	}
	ids := make(map[string]bool, len(in.Candidates))
	members := make(map[string]Candidate, len(in.Candidates))
	for _, c := range in.Candidates {
		cfg := c.Configuration
		if !refs(c.ID, c.Member, c.Workbench, c.CardRevision, cfg.Runtime, cfg.Vendor, cfg.Model, cfg.Effort, cfg.Profile, cfg.Lock) || ids[c.ID] {
			return false
		}
		ids[c.ID] = true
		// Multiple proposed configurations for one stopped seat are valid; they
		// cannot conceal contradictory state, workbench, card or live configuration.
		if prior, exists := members[c.Member]; exists {
			if prior.State != c.State || prior.Workbench != c.Workbench || prior.CardRevision != c.CardRevision || (c.State != Stopped && prior.Configuration != c.Configuration) {
				return false
			}
		}
		members[c.Member] = c
		switch c.State {
		case Ready, Stopped, Running, Starting, StateUnknown, StateConflict:
		default:
			return false
		}
		seen := make(map[Gate]bool, len(c.Checks))
		for _, check := range c.Checks {
			if !knownGate(check.Gate) || seen[check.Gate] {
				return false
			}
			seen[check.Gate] = true
			switch check.Verdict {
			case Allow, Deny, Unknown, Conflict:
			default:
				return false
			}
		}
	}
	return true
}

func knownGate(g Gate) bool {
	for _, list := range [][]Gate{common, reuse, start} {
		for _, candidate := range list {
			if candidate == g {
				return true
			}
		}
	}
	return false
}

func refs(values ...string) bool {
	for _, value := range values {
		if !validRef(value) {
			return false
		}
	}
	return true
}

func validRef(value string) bool {
	if len(value) == 0 || len(value) > 512 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
