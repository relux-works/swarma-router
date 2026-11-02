package workbench

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"relux.works/swarma-router/launch"
)

// LaunchSpecification is an owner-verified immutable mapping, not a request
// extension. Intent.CorrelationID must be empty: the adapter binds it to the
// complete original operation. Mapping authenticates nothing by itself.
type LaunchSpecification struct {
	Key                         OperationKey
	RequestDigest, TargetDigest string
	Intent                      launch.Intent
	Mapping                     RevisionRef
	ObservedAt, ExpiresAt       time.Time
}

// LowerPermit is actual current owner authority for this retained operation.
// The source checks task/card/policy/configuration/holds/custody/positive absence;
// these typed values and receipt references do not authenticate their producer.
type LowerPermit struct {
	Key                                       OperationKey
	RequestDigest, TargetDigest, IntentDigest string
	Selected                                  SelectedBinding
	Mapping, Reference                        RevisionRef
	State                                     string
	CheckedAt, ExpiresAt                      time.Time
}

// LifecycleWitness must come from an admitted owner source that verifies actual
// custody/task/configuration/runtime/lock/messaging/generation/lane evidence and
// correlates it with the exact lower result's process and original operation.
// ResultDigest binds that complete result (nil for reuse), not just "running".
type LifecycleWitness struct {
	ResultDigest string
	Observation  Observation
}

// LifecycleOwner performs only admitted synchronous reads, honoring ctx and
// retaining no mutable inputs. Production acquisition is not implemented here.
// Specification is pure mapping; Admit is called by the existing lower Guard.
// Witness never discovers or substitutes another target and performs no effect.
type LifecycleOwner interface {
	Specification(context.Context, View) (LaunchSpecification, error)
	Admit(context.Context, View, launch.Intent) (LowerPermit, error)
	Witness(context.Context, View, *launch.Result) (LifecycleWitness, error)
}

// LowerLifecycle docks the existing launch.Router to one retained workbench.
// Router, store/plugin wiring, Local, Owner and Clock are admitted out of band.
// It never mutates the configured Router or selects a store from request JSON.
type LowerLifecycle struct {
	Local  LocalContext
	Router launch.Router
	Owner  LifecycleOwner
	Clock  func() time.Time
}

// LowerResultDigest binds all sanitized lower result fields, including the full
// Attempt/process identity. It is a content binding, not source authentication.
func LowerResultDigest(result *launch.Result) string {
	// launch.Result is a fixed Go struct with integer process IDs, not an upper
	// wire object. Preserve its complete JSON encoding without float conversion.
	raw, _ := json.Marshal(result)
	return hash(raw)
}

func (l LowerLifecycle) target(ctx context.Context, v View) (TargetBinding, error) {
	if ctx == nil || l.Clock == nil || v.binding.request.prepare == nil {
		return TargetBinding{}, Refusal{"source_unavailable"}
	}
	if _, ok := ctx.Deadline(); !ok || ctx.Err() != nil {
		return TargetBinding{}, Refusal{"authority_unknown"}
	}
	if v.Key().Caller != l.Local.Caller || v.Key().Project != l.Local.Project {
		return TargetBinding{}, Refusal{"authority_denied"}
	}
	selected, sources := v.Selected(), v.Sources()
	if !validSelected(selected, v.Request()) || sources == nil || hash(sources.Data) != sources.Digest {
		return TargetBinding{}, Refusal{"source_conflict"}
	}
	doc, ok := restoreSourceDocument(sources.Data)
	if !ok || doc.RequestDigest != v.Digest() || doc.Proposal.Selected == nil || proposalReference(v.Digest(), doc.Proposal) != selected.Proposal || string(doc.Proposal.Action) != v.record.Action {
		return TargetBinding{}, Refusal{"source_conflict"}
	}
	for _, t := range doc.Targets {
		b := t.Binding
		if candidateIdentity(b) == *doc.Proposal.Selected && b.Member == selected.Member && b.Card == selected.Card && b.Configuration == selected.Configuration && targetInRequest(b, v.Request()) {
			return b, nil
		}
	}
	return TargetBinding{}, Refusal{"source_conflict"}
}

func actualValidity(checked, expires, now time.Time) bool {
	_, a := checked.MarshalJSON()
	_, b := expires.MarshalJSON()
	_, c := now.MarshalJSON()
	return a == nil && b == nil && c == nil && !now.IsZero() && !checked.IsZero() && !checked.After(now) && expires.After(now)
}

func (l LowerLifecycle) Prepare(ctx context.Context, v View) (LowerMapping, error) {
	target, err := l.target(ctx, v)
	if err != nil {
		return LowerMapping{}, err
	}
	if l.Owner == nil {
		return LowerMapping{}, Refusal{"source_unavailable"}
	}
	if v.record.Action != "start" || v.Lower() != nil || v.record.PossibleEffect || target.State != "stopped" {
		return LowerMapping{}, Refusal{"source_conflict"}
	}
	spec, err := l.Owner.Specification(ctx, copiedView(v))
	if err != nil {
		return LowerMapping{}, Refusal{reason(err, "source_unavailable")}
	}
	if ctx.Err() != nil {
		return LowerMapping{}, Refusal{"authority_unknown"}
	}
	i := spec.Intent
	if spec.Key != v.Key() || spec.RequestDigest != v.Digest() || spec.TargetDigest != TargetBindingDigest(target) || !validRevision(spec.Mapping) || !actualValidity(spec.ObservedAt, spec.ExpiresAt, l.Clock()) || i.CorrelationID != "" {
		return LowerMapping{}, Refusal{"source_conflict"}
	}
	if i.Contract != launch.Contract || i.Manager != l.Router.Manager || i.Project != l.Router.Project || i.Project != target.OwnerProject || i.Member != target.OwnerMember || i.Runtime != target.Configuration.Runtime || i.Profile != target.Profile || i.Model != target.Model || i.ProfileLock != target.Configuration.ProfileLock || i.Effort != target.Configuration.Effort {
		return LowerMapping{}, Refusal{"source_conflict"}
	}
	// The full owner template (including workspace/session/plugin), upper tuple,
	// requested artifacts, target mapping and receipt determine the safe key.
	i.CorrelationID = lowerCorrelation(v, spec.TargetDigest, i, spec.Mapping)
	mapping := LowerMapping{Binding: LowerBinding{Plugin: i.Plugin, OperationRef: lowerOperationReference(i), IntentDigest: i.Digest()}, Intent: i, Mapping: spec.Mapping}
	if !validLower(&mapping, v.Selected()) {
		return LowerMapping{}, Refusal{"unsupported_configuration"}
	}
	result := l.Router.Prepare(i) // existing grammar and pure command validation
	if !boundLowerResult(result, i) || result.Status != "prepared" {
		return LowerMapping{}, Refusal{"unsupported_configuration"}
	}
	if ctx.Err() != nil || !actualValidity(spec.ObservedAt, spec.ExpiresAt, l.Clock()) {
		return LowerMapping{}, Refusal{"authority_unknown"}
	}
	return mapping, nil
}

func lowerCorrelation(v View, targetDigest string, intent launch.Intent, mapping RevisionRef) string {
	intent.CorrelationID = ""
	return "wb-" + hash(canonical(object(struct {
		Key           OperationKey
		RequestDigest string
		Selected      SelectedBinding
		TargetDigest  string
		Specification launch.Intent
		Mapping       RevisionRef
	}{v.Key(), v.Digest(), *v.Selected(), targetDigest, intent, mapping})))
}

func lowerOperationReference(i launch.Intent) string {
	return "launch-operation-sha256:" + hash(canonical(object(struct{ Manager, Project, CorrelationID string }{i.Manager, i.Project, i.CorrelationID})))
}
func copiedView(v View) View { return View{record: clone(v.record), binding: v.binding} }

func (l LowerLifecycle) retained(v View, target TargetBinding) error {
	m := v.Lower()
	if !validLower(m, v.Selected()) || m.Binding.OperationRef != lowerOperationReference(m.Intent) || m.Intent.CorrelationID != lowerCorrelation(v, TargetBindingDigest(target), m.Intent, m.Mapping) {
		return Refusal{"source_conflict"}
	}
	i := m.Intent
	if i.Manager != l.Router.Manager || i.Project != l.Router.Project || i.Project != target.OwnerProject || i.Member != target.OwnerMember || i.Profile != target.Profile || i.Model != target.Model || i.Effort != target.Configuration.Effort {
		return Refusal{"source_conflict"}
	}
	return nil
}

type workbenchGuard struct {
	base   launch.Guard
	owner  LifecycleOwner
	view   View
	target TargetBinding
}

func (g workbenchGuard) Check(ctx context.Context, intent launch.Intent, digest string, now time.Time) (launch.Admission, error) {
	v := g.view
	m := v.Lower()
	if g.owner == nil || m == nil || !same(m.Intent, intent) || digest != m.Binding.IntentDigest {
		return launch.Admission{}, Refusal{"authority_unknown"}
	}
	p, err := g.owner.Admit(ctx, copiedView(v), intent)
	if err != nil {
		return launch.Admission{}, err
	}
	if ctx.Err() != nil || p.Key != v.Key() || p.RequestDigest != v.Digest() || p.TargetDigest != TargetBindingDigest(g.target) || p.IntentDigest != digest || p.Selected != *v.Selected() || p.Mapping != m.Mapping || !validRevision(p.Reference) || p.State != "stopped" || !actualValidity(p.CheckedAt, p.ExpiresAt, now) {
		return launch.Admission{}, Refusal{"authority_unknown"}
	}
	admission := launch.Admission{Digest: digest, Manager: intent.Manager, Member: intent.Member, ProfileLock: intent.ProfileLock, State: p.State, CheckedAt: p.CheckedAt, ExpiresAt: p.ExpiresAt}
	// An upper permit cannot bypass an independently configured lower guard.
	// Intersect actual validity, preserving each source's meaning and refusals.
	if g.base != nil {
		lower, err := g.base.Check(ctx, intent, digest, now)
		if err != nil {
			return launch.Admission{}, err
		}
		if ctx.Err() != nil || lower.Digest != digest || lower.Manager != intent.Manager || lower.Member != intent.Member || lower.ProfileLock != intent.ProfileLock || lower.State != "stopped" || !actualValidity(lower.CheckedAt, lower.ExpiresAt, now) {
			return launch.Admission{}, Refusal{"authority_unknown"}
		}
		if lower.CheckedAt.After(admission.CheckedAt) {
			admission.CheckedAt = lower.CheckedAt
		}
		if lower.ExpiresAt.Before(admission.ExpiresAt) {
			admission.ExpiresAt = lower.ExpiresAt
		}
	}
	return admission, nil
}

func (l LowerLifecycle) Start(ctx context.Context, v View) (Observation, error) {
	target, err := l.target(ctx, v)
	if err != nil {
		return Observation{}, err
	}
	if v.record.Action != "start" || !v.record.PossibleEffect || target.State != "stopped" {
		return Observation{}, Refusal{"source_conflict"}
	}
	if err = l.retained(v, target); err != nil {
		return Observation{}, err
	}
	router := l.Router // wrapper is per-call, never shared mutable configuration
	router.Now = l.Clock
	router.Guard = workbenchGuard{base: l.Router.Guard, owner: l.Owner, view: copiedView(v), target: target}
	result := router.Start(ctx, v.Lower().Intent)
	// No implicit Observe or second Start; an attempted effect needs Status for
	// actual lower liveness. Uncertain lower outcomes retain the exact same key.
	return l.witness(ctx, v, target, &result)
}
func (l LowerLifecycle) Observe(ctx context.Context, v View) (Observation, error) {
	target, err := l.target(ctx, v)
	if err != nil {
		return Observation{}, err
	}
	if v.record.Action == "reuse" {
		if v.Lower() != nil || target.State != "ready" {
			return Observation{}, Refusal{"source_conflict"}
		}
		return l.witness(ctx, v, target, nil)
	}
	if v.record.Action != "start" {
		return Observation{}, Refusal{"source_conflict"}
	}
	if err = l.retained(v, target); err != nil {
		return Observation{}, err
	}
	result := l.Router.Observe(ctx, v.Lower().Intent)
	return l.witness(ctx, v, target, &result)
}

func unknownLowerObservation(v View) Observation {
	return Observation{Key: v.Key(), RequestDigest: v.Digest(), Selected: *v.Selected(), Lower: lowerBinding(v.record), Status: "unknown", Reason: "observation_unresolved", Evidence: []RevisionRef{}}
}
func (l LowerLifecycle) witness(ctx context.Context, v View, target TargetBinding, result *launch.Result) (Observation, error) {
	if ctx.Err() != nil {
		return Observation{}, Refusal{"authority_unknown"}
	}
	if result != nil && !boundLowerResult(*result, v.Lower().Intent) {
		return Observation{}, Refusal{"source_conflict"}
	}
	if l.Owner == nil {
		return unknownLowerObservation(v), nil
	}
	witness, err := l.Owner.Witness(ctx, copiedView(v), clone(result))
	if err != nil {
		return unknownLowerObservation(v), nil
	}
	if ctx.Err() != nil {
		return Observation{}, Refusal{"authority_unknown"}
	}
	o := witness.Observation
	if witness.ResultDigest != LowerResultDigest(result) || !observationShapeUnordered(o, v.record, v.binding) {
		return Observation{}, Refusal{"source_conflict"}
	}
	if !observationFresh(o, l.Clock()) {
		return Observation{}, Refusal{"readiness_expired"}
	}
	if o.Ready != nil {
		if result != nil {
			if result.Historical || result.Status != "running" || !completeLowerAttempt(result.Attempt, v.Lower().Intent) {
				return Observation{}, Refusal{"readiness_unknown"}
			}
		} else if !same(o.Ready.Execution, target.Execution) {
			return Observation{}, Refusal{"source_conflict"}
		}
	}
	return clone(o), nil
}
func boundLowerResult(r launch.Result, i launch.Intent) bool {
	if r.Contract != launch.Contract || r.CorrelationID != i.CorrelationID || r.RequestDigest != i.Digest() || !utf8.ValidString(r.Reason) {
		return false
	}
	switch r.Status {
	case "prepared", "refused", "unknown", "conflict", "attempted", "pending", "running":
	default:
		return false
	}
	if r.Attempt != nil {
		a := r.Attempt
		for _, s := range append(append([]string{}, a.Command...), a.Profile, a.ProfileLock, a.OperationDigest, a.Session, a.Pane, a.CuratorRun.Birth, a.CuratorRun.Executable, a.Runtime.Birth, a.Runtime.Executable) {
			if !utf8.ValidString(s) {
				return false
			}
		}
		if a.Profile != i.Profile || a.ProfileLock != i.ProfileLock || a.OperationDigest != i.Digest() || a.Session != i.TmuxSession {
			return false
		}
	}
	return true
}
func completeLowerAttempt(a *launch.Attempt, i launch.Intent) bool {
	if a == nil || len(a.Command) == 0 || !strings.HasPrefix(a.Pane, "%") || a.PanePID <= 0 {
		return false
	}
	pane := strings.TrimPrefix(a.Pane, "%")
	if pane == "" {
		return false
	}
	for _, c := range pane {
		if c < '0' || c > '9' {
			return false
		}
	}
	for _, p := range []launch.Process{a.CuratorRun, a.Runtime} {
		if p.PID <= 0 || p.ParentPID <= 0 || p.Birth == "" || len(p.Birth) >= 100 || p.Executable == "" {
			return false
		}
	}
	return a.Profile == i.Profile && a.ProfileLock == i.ProfileLock && a.OperationDigest == i.Digest() && a.Session == i.TmuxSession
}

var _ Lifecycle = LowerLifecycle{}
