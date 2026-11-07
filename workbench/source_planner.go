package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

	"relux.works/swarma-router/inventory"
	"relux.works/swarma-router/selection"
)

const SourceSnapshotContract = "router-workbench-source-snapshot/0.1.0"

// OwnerReader is admitted in-process wiring, not a wire decoder or source name.
// It must read actual allowed task/card/policy/custody/qualification sources and
// validate their receipts. The structs below do not authenticate their producer.
// only narrows Revalidate to the retained selection; it never requests discovery.
// Read honors ctx, completes synchronously and retains no mutable arguments.
type OwnerReader interface {
	Read(ctx context.Context, request PrepareRequest, only *SelectedBinding) (OwnerInputs, error)
}

type OwnerInputs struct {
	Task    RevisionRef
	Policy  RevisionRef
	Cards   []CardRef
	Targets []OwnerTarget
}

// TargetBinding preserves requested artifacts and their distinct qualified
// native names. Mapping binds that translation; an artifact digest is not a
// profile lock. State comes from the current owning source, never Host activity.
type TargetBinding struct {
	Member, OwnerMember, OwnerProject, Workbench string
	Card                                         RevisionRef
	Configuration                                Configuration
	Model, Profile                               string
	Mapping                                      RevisionRef
	State                                        selection.State
	Execution                                    map[string]string
}

// BoundWitness names an actual owner receipt, exact operation/target and source
// validity. Unknown times stay nil. TargetDigest is TargetBindingDigest, not a
// substitute for source verification. No collection-clock validity is invented.
type BoundWitness struct {
	Key                         OperationKey
	RequestDigest, TargetDigest string
	Reference                   RevisionRef
	ObservedAt, ExpiresAt       *time.Time
}

type OwnerCheck struct {
	Gate    selection.Gate
	Verdict selection.Verdict
	Witness BoundWitness
}

// HostInput contains only an already admitted source and expected binding.
// Neither is selectable through the Prepare envelope. Production Feed and owner
// receipt acquisition remain the embedding's responsibility.
type HostInput struct {
	Source  inventory.Source
	Binding inventory.Binding
}

type OwnerTarget struct {
	Binding TargetBinding
	Witness BoundWitness
	Checks  []OwnerCheck
	Host    *HostInput
}

type SourcePlanner struct {
	Local LocalContext
	Owner OwnerReader
	Clock func() time.Time
}

func TargetBindingDigest(binding TargetBinding) string { return hash(canonical(object(binding))) }

type normalizedTarget struct {
	Binding TargetBinding
	Witness BoundWitness
	Checks  []OwnerCheck
}

type hostRead struct {
	TargetDigest string
	Binding      inventory.Binding
	Result       inventory.Result
}

type sourceDocument struct {
	Contract, RequestDigest string
	Task, Policy            RevisionRef
	Cards                   []CardRef
	Targets                 []normalizedTarget
	Inventory               []hostRead
	Proposal                selection.Proposal
}

type normalizedInputs struct {
	doc   sourceDocument
	input selection.Input
}

func (p SourcePlanner) request(ctx context.Context, in PrepareRequest) (Request, error) {
	if ctx == nil || p.Owner == nil || p.Clock == nil {
		return Request{}, Refusal{"source_unavailable"}
	}
	if _, ok := ctx.Deadline(); !ok || ctx.Err() != nil {
		return Request{}, Refusal{"authority_unknown"}
	}
	// Check raw Go strings before JSON's replacement of invalid UTF-8.
	if in.Contract != Contract || in.Operation != "prepare_workbench" || !ref(in.Caller) || !ref(in.Project) || !ref(in.CorrelationID) || !validRevision(in.Task) || !validRevision(in.Constraints.Policy) || !ref(in.Constraints.Objective) {
		return Request{}, Refusal{"invalid_request"}
	}
	for _, c := range in.Cards {
		if !ref(c.Member) || !validRevision(c.Card) {
			return Request{}, Refusal{"invalid_request"}
		}
	}
	for _, c := range in.Constraints.Configurations {
		if !ref(c.Member) || !ref(c.Runtime) || !ref(c.Vendor) || !ref(c.Effort) || !ref(c.Placement) || !validRevision(c.Model) || !validRevision(c.Profile) || !digest(c.ProfileLock) {
			return Request{}, Refusal{"invalid_request"}
		}
	}
	if in.Caller != p.Local.Caller || in.Project != p.Local.Project {
		return Request{}, Refusal{"authority_denied"}
	}
	m := object(in).(map[string]any)
	if !validPrepare(m) {
		return Request{}, Refusal{"invalid_request"}
	}
	b := canonical(m) // validPrepare normalizes semantic sets, including permutations.
	if len(b) > MaxRecordBytes {
		return Request{}, Refusal{"output_budget"}
	}
	var normalized PrepareRequest
	if json.Unmarshal(b, &normalized) != nil {
		return Request{}, Refusal{"invalid_request"}
	}
	r := Request{key: OperationKey{in.Caller, in.Project, in.CorrelationID}, operation: in.Operation, prepare: &normalized, canonical: b, digest: hash(b)}
	r.recovery = recoveryTemplate(r)
	if len(r.recovery) > MaxRecoveryBytes {
		return Request{}, Refusal{"output_budget"}
	}
	// Wider valid wire identities cannot be silently truncated into this selector.
	if !selectionText(in.Caller) || !selectionText(in.Project) {
		return Request{}, Refusal{"unsupported_configuration"}
	}
	return r, nil
}

func (p SourcePlanner) Resolve(ctx context.Context, in PrepareRequest) (Plan, error) {
	r, err := p.request(ctx, in)
	if err != nil {
		return Plan{}, err
	}
	n, err := p.read(ctx, r, nil)
	if err != nil {
		return Plan{}, err
	}
	decision := selection.Select(n.input)
	if decision.Reason == "invalid_input" {
		return Plan{}, Refusal{"source_conflict"}
	}
	// Owner state/mapping validity is an additional dependency, never extended
	// beyond its actual expiry by otherwise longer-lived required checks.
	if decision.Selected != nil {
		for _, target := range n.doc.Targets {
			if candidateIdentity(target.Binding) == *decision.Selected && target.Witness.ExpiresAt.Before(decision.ValidUntil) {
				decision.ValidUntil = *target.Witness.ExpiresAt
			}
		}
	}
	n.doc.Proposal = decision
	data := canonical(object(n.doc))
	if len(data) > MaxRecordBytes {
		return Plan{}, Refusal{"output_budget"}
	}
	proposal := proposalReference(r.Digest(), decision)
	refs := uniqueReferences(r.prepare.Task, r.prepare.Constraints.Policy, RevisionRef{Source: "swarma-router-normalizer", Object: "snapshot-sha256:" + hash(data), Revision: SourceSnapshotContract, Digest: hash(data)})
	plan := Plan{Sources: &Snapshot{Data: data, Digest: hash(data), References: refs}, Proposal: &proposal}
	switch decision.Action {
	case selection.Reuse, selection.Start:
		for _, target := range n.doc.Targets {
			if candidateIdentity(target.Binding) == *decision.Selected {
				plan.Selected = &SelectedBinding{Member: target.Binding.Member, Card: target.Binding.Card, Configuration: target.Binding.Configuration, Proposal: proposal}
				plan.Action, plan.Status, plan.Reason = string(decision.Action), "selected", "ok"
				return plan, nil
			}
		}
		return Plan{}, Refusal{"source_conflict"}
	case selection.Defer:
		plan.Status, plan.Reason = "deferred", "change_unavailable"
	case selection.InsufficientEvidence:
		plan.Status, plan.Reason = "unavailable", "evidence_insufficient"
	default:
		plan.Status, plan.Reason = "refused", "no_eligible_candidate"
	}
	return plan, nil
}

// Revalidate never invokes Select. It obtains current evidence for the retained
// exact target and checks only that action's required predicates, without a
// replacement target, preparation, assignment, message or lifecycle operation.
func (p SourcePlanner) Revalidate(ctx context.Context, view View) error {
	r, err := p.request(ctx, view.Request())
	if err != nil {
		return err
	}
	selected, sources := view.Selected(), view.Sources()
	if selected == nil || sources == nil || hash(sources.Data) != sources.Digest || r.Digest() != view.Digest() {
		return Refusal{"source_conflict"}
	}
	doc, ok := restoreSourceDocument(sources.Data)
	if !ok || doc.RequestDigest != r.Digest() || doc.Proposal.Selected == nil || proposalReference(r.Digest(), doc.Proposal) != selected.Proposal {
		return Refusal{"source_conflict"}
	}
	var original *TargetBinding
	for _, t := range doc.Targets {
		if candidateIdentity(t.Binding) == *doc.Proposal.Selected && t.Binding.Member == selected.Member && t.Binding.Card == selected.Card && t.Binding.Configuration == selected.Configuration {
			b := t.Binding
			original = &b
		}
	}
	if original == nil || string(doc.Proposal.Action) != view.record.Action {
		return Refusal{"source_conflict"}
	}
	n, err := p.read(ctx, r, selected)
	if err != nil {
		return err
	}
	for _, c := range n.input.Candidates {
		if c.Identity != *doc.Proposal.Selected {
			continue
		}
		for _, t := range n.doc.Targets {
			if same(t.Binding, *original) && requiredChecksAllow(c, n.input, doc.Proposal.Action) {
				return nil
			}
		}
	}
	return Refusal{"evidence_insufficient"}
}

func (p SourcePlanner) read(ctx context.Context, r Request, only *SelectedBinding) (normalizedInputs, error) {
	started := p.Clock()
	if started.IsZero() {
		return normalizedInputs{}, Refusal{"authority_unknown"}
	}
	request, _ := r.Prepare()
	owner, err := p.Owner.Read(ctx, request, clone(only))
	if err != nil {
		return normalizedInputs{}, Refusal{reason(err, "source_unavailable")}
	}
	if ctx.Err() != nil {
		return normalizedInputs{}, Refusal{"authority_unknown"}
	}
	if owner.Task != request.Task || owner.Policy != request.Constraints.Policy || len(owner.Targets) > 16 {
		return normalizedInputs{}, Refusal{"source_conflict"}
	}
	cards := append([]CardRef{}, owner.Cards...)
	sort.Slice(cards, func(i, j int) bool { return cards[i].Member < cards[j].Member })
	if !same(cards, request.Cards) {
		return normalizedInputs{}, Refusal{"card_conflict"}
	}
	doc := sourceDocument{Contract: SourceSnapshotContract, RequestDigest: r.Digest(), Task: owner.Task, Policy: owner.Policy, Cards: cards, Targets: []normalizedTarget{}, Inventory: []hostRead{}}
	seen := map[string]bool{}
	members := map[string]TargetBinding{}
	for _, t := range owner.Targets {
		b := t.Binding
		if !targetInRequest(b, request) || len(t.Checks) > 16 || !validWitness(t.Witness) {
			return normalizedInputs{}, Refusal{"source_conflict"}
		}
		id := candidateIdentity(b).ID
		if seen[id] {
			return normalizedInputs{}, Refusal{"source_conflict"}
		}
		seen[id] = true
		if prior, exists := members[b.OwnerMember]; exists {
			if prior.Member != b.Member || prior.OwnerProject != b.OwnerProject || prior.State != b.State || prior.Workbench != b.Workbench || prior.Card != b.Card || (b.State != selection.Stopped && (prior.Configuration != b.Configuration || !same(prior.Execution, b.Execution))) {
				return normalizedInputs{}, Refusal{"source_conflict"}
			}
		}
		members[b.OwnerMember] = clone(b)
		checks := append([]OwnerCheck{}, t.Checks...)
		sort.Slice(checks, func(i, j int) bool { return checks[i].Gate < checks[j].Gate })
		for i, c := range checks {
			if !knownPlannerGate(c.Gate) || !validWitness(c.Witness) || (i > 0 && checks[i-1].Gate == c.Gate) || (c.Verdict != selection.Allow && c.Verdict != selection.Deny && c.Verdict != selection.Unknown && c.Verdict != selection.Conflict) {
				return normalizedInputs{}, Refusal{"source_conflict"}
			}
		}
		doc.Targets = append(doc.Targets, normalizedTarget{clone(b), clone(t.Witness), clone(checks)})
		if t.Host != nil {
			expected := t.Host.Binding
			if expected.Member != b.OwnerMember || expected.Project != b.OwnerProject || expected.Workbench != b.Workbench {
				return normalizedInputs{}, Refusal{"source_conflict"}
			}
			if b.Execution["kind"] == "host" && (expected.Execution != b.Execution["execution"] || expected.RunnerInstance != b.Execution["runner_instance"]) {
				return normalizedInputs{}, Refusal{"source_conflict"}
			}
			expected.SourceVersions = append([]string{}, expected.SourceVersions...)
			sort.Strings(expected.SourceVersions)
			result := inventory.Read(ctx, t.Host.Source, expected)
			doc.Inventory = append(doc.Inventory, hostRead{TargetBindingDigest(b), expected, result})
		}
	}
	sort.Slice(doc.Targets, func(i, j int) bool {
		return candidateIdentity(doc.Targets[i].Binding).ID < candidateIdentity(doc.Targets[j].Binding).ID
	})
	sort.Slice(doc.Inventory, func(i, j int) bool { return doc.Inventory[i].TargetDigest < doc.Inventory[j].TargetDigest })
	if ctx.Err() != nil {
		return normalizedInputs{}, Refusal{"authority_unknown"}
	}
	basis := selection.Basis{Caller: r.Key().Caller, Project: r.Key().Project, RequestDigest: r.Digest(), TaskRevision: revisionToken(request.Task), CardsDigest: hash(canonical(object(cards))), PolicyRevision: revisionToken(request.Constraints.Policy), InventoryDigest: hash(canonical(object(doc.Inventory))), QualificationDigest: hash(canonical(object(doc.Targets)))}
	in := selection.Input{Basis: basis, Scope: selection.Partial, Now: p.Clock(), Candidates: []selection.Candidate{}}
	if ctx.Err() != nil || in.Now.IsZero() {
		return normalizedInputs{}, Refusal{"authority_unknown"}
	}
	// Cover the complete owner and optional Host acquisition. Evaluating at an
	// earlier clock could revive an expiry that had already elapsed at entry.
	// Compare wall time even if the injected clock retains monotonic readings.
	if in.Now.Before(started.Round(0)) {
		return normalizedInputs{}, Refusal{"source_stale"}
	}
	for _, t := range doc.Targets {
		b := t.Binding
		identity := candidateIdentity(b)
		state := b.State
		if !witnessBound(t.Witness, r, b, in.Now) {
			state = selection.StateUnknown
		}
		for _, h := range doc.Inventory {
			if h.TargetDigest != TargetBindingDigest(b) {
				continue
			}
			if h.Result.Class == "conflict" {
				state = selection.StateConflict
			} else if h.Result.Class != "usable" {
				state = selection.StateUnknown
			} else if state == selection.Ready && hostReuseConflict(h.Result.Projection, b) {
				state = selection.StateConflict
			} else if state == selection.Ready && b.Execution["kind"] != "host" {
				// The a2a union carries no Host execution/runner identity. A
				// requested Host frame cannot qualify this unlinked execution.
				state = selection.StateUnknown
			}
		}
		candidate := selection.Candidate{Identity: identity, State: state, Checks: []selection.Check{}}
		for _, c := range t.Checks {
			verdict := c.Verdict
			if !witnessBound(c.Witness, r, b, in.Now) {
				verdict = selection.Unknown
			}
			e := selection.Evidence{Reference: revisionToken(c.Witness.Reference), Basis: basis, Candidate: identity}
			if c.Witness.ObservedAt != nil {
				e.ObservedAt = *c.Witness.ObservedAt
			}
			if c.Witness.ExpiresAt != nil {
				e.ExpiresAt = *c.Witness.ExpiresAt
			}
			candidate.Checks = append(candidate.Checks, selection.Check{Gate: c.Gate, Verdict: verdict, Evidence: e})
		}
		in.Candidates = append(in.Candidates, candidate)
	}
	return normalizedInputs{doc, in}, nil
}

func revisionToken(r RevisionRef) string { return "revision-sha256:" + hash(canonical(object(r))) }
func candidateIdentity(b TargetBinding) selection.Identity {
	return selection.Identity{ID: "candidate-sha256:" + TargetBindingDigest(b), Member: b.OwnerMember, Workbench: b.Workbench, CardRevision: revisionToken(b.Card), Configuration: selection.Configuration{Runtime: b.Configuration.Runtime, Vendor: b.Configuration.Vendor, Model: b.Model, Effort: b.Configuration.Effort, Profile: b.Profile, Lock: b.Configuration.ProfileLock}}
}
func proposalReference(digest string, p selection.Proposal) RevisionRef {
	h := hash(canonical(object(p)))
	return RevisionRef{Source: "swarma-router-selection", Object: "request-sha256:" + digest, Revision: "proposal-sha256:" + h, Digest: h}
}
func uniqueReferences(refs ...RevisionRef) []RevisionRef {
	m := map[string]RevisionRef{}
	for _, r := range refs {
		m[string(canonical(object(r)))] = r
	}
	out := []RevisionRef{}
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return string(canonical(object(out[i]))) < string(canonical(object(out[j]))) })
	return out
}
func selectionText(s string) bool {
	if len(s) == 0 || len(s) > 512 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func targetInRequest(b TargetBinding, p PrepareRequest) bool {
	if !ref(b.Member) || !validRevision(b.Card) || !validRevision(b.Mapping) || !selectionText(b.OwnerMember) || !selectionText(b.OwnerProject) || !selectionText(b.Workbench) || !selectionText(b.Model) || !selectionText(b.Profile) {
		return false
	}
	card, config := false, false
	for _, c := range p.Cards {
		card = card || (c.Member == b.Member && c.Card == b.Card)
	}
	for _, c := range p.Constraints.Configurations {
		config = config || (c == b.Configuration && c.Member == b.Member)
	}
	switch b.State {
	case selection.Ready, selection.Stopped, selection.Running, selection.Starting, selection.StateUnknown, selection.StateConflict:
	default:
		return false
	}
	if b.State == selection.Ready && !validExecution(b.Execution) || b.State == selection.Stopped && b.Execution != nil || b.Execution != nil && !validExecution(b.Execution) {
		return false
	}
	return card && config
}
func validWitness(w BoundWitness) bool {
	if w == (BoundWitness{}) {
		return true
	}
	for _, value := range []*time.Time{w.ObservedAt, w.ExpiresAt} {
		if value != nil {
			if _, err := value.MarshalJSON(); err != nil {
				return false
			}
		}
	}
	return ref(w.Key.Caller) && ref(w.Key.Project) && ref(w.Key.CorrelationID) && digest(w.RequestDigest) && digest(w.TargetDigest) && validRevision(w.Reference)
}
func witnessBound(w BoundWitness, r Request, b TargetBinding, now time.Time) bool {
	return w.Key == r.Key() && w.RequestDigest == r.Digest() && w.TargetDigest == TargetBindingDigest(b) && w.ObservedAt != nil && w.ExpiresAt != nil && !w.ObservedAt.IsZero() && !w.ObservedAt.After(now) && w.ExpiresAt.After(now)
}

var plannerCommon = []selection.Gate{"caller_authority", "task_constraints", "card_member", "profile_runtime", "quality", "provider_limits", "manager_custody", "board_assignment", "messaging_admission"}
var plannerReuse = []selection.Gate{"current_configuration", "exact_generation", "ready_response", "occupancy"}
var plannerStart = []selection.Gate{"complete_owned_absence", "preparation_authority", "launch_capability"}

func knownPlannerGate(g selection.Gate) bool {
	for _, list := range [][]selection.Gate{plannerCommon, plannerReuse, plannerStart} {
		for _, v := range list {
			if g == v {
				return true
			}
		}
	}
	return false
}
func requiredChecksAllow(c selection.Candidate, in selection.Input, action selection.Action) bool {
	specific := plannerReuse
	if action == selection.Start {
		if c.State != selection.Stopped {
			return false
		}
		specific = plannerStart
	} else if action != selection.Reuse || c.State != selection.Ready {
		return false
	}
	for _, list := range [][]selection.Gate{plannerCommon, specific} {
		for _, gate := range list {
			found := false
			for _, check := range c.Checks {
				e := check.Evidence
				if check.Gate == gate && check.Verdict == selection.Allow && e.Basis == in.Basis && e.Candidate == c.Identity && !e.ObservedAt.IsZero() && !e.ObservedAt.After(in.Now) && e.ExpiresAt.After(in.Now) {
					found = true
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}
func hostReuseConflict(p *inventory.Projection, b TargetBinding) bool {
	if p == nil {
		return true
	}
	for _, name := range []string{"security_stopped", "stop_requested"} {
		f := p.Facts[name]
		if f.Known && f.Value == true {
			return true
		}
	}
	for name, want := range map[string]string{"configured_vendor": b.Configuration.Vendor, "configured_model": b.Model, "profile": b.Profile, "profile_lock": b.Configuration.ProfileLock, "placement": b.Configuration.Placement} {
		f := p.Facts[name]
		if f.Known && f.Value != want {
			return true
		}
	}
	return false
}
func restoreSourceDocument(raw []byte) (sourceDocument, bool) {
	if len(raw) == 0 || len(raw) > MaxRecordBytes || !utf8.Valid(raw) || !scalarEscapes(raw) {
		return sourceDocument{}, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return sourceDocument{}, false
	}
	if _, err = d.Token(); err != io.EOF {
		return sourceDocument{}, false
	}
	var out sourceDocument
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || out.Contract != SourceSnapshotContract || !bytes.Equal(canonical(v), canonical(object(out))) {
		return sourceDocument{}, false
	}
	return out, true
}

var _ Planner = SourcePlanner{}
