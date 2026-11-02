package workbench

import (
	"bytes"
	"context"
	"errors"
	"strings"
)

// Run is one synchronous bounded invocation. Its admitted ports must honor the
// caller's finite deadline. It does not start a watchdog, goroutine or retry.
// Invalid API values are programmer errors, not fabricated wire requests.
func (e Engine) Run(ctx context.Context, req Request) (out Output, runErr error) {
	if ctx == nil || req.operation == "" || len(req.recovery) == 0 {
		return Output{}, errors.New("invalid_request")
	}
	if _, ok := ctx.Deadline(); !ok {
		return Output{}, errors.New("authority_unknown")
	}
	if ctx.Err() != nil {
		return e.recovery(req, "store_unavailable", RetainedRequest{}, operationRecord{}), nil
	}
	if e.Access == nil {
		return errorOutput(req, "authority_unknown"), nil
	}
	if err := e.Access.Read(ctx, req.Key()); err != nil {
		return errorOutput(req, reason(err, "authority_unknown")), nil
	}
	if e.Repository == nil {
		return e.recovery(req, "store_unavailable", RetainedRequest{}, operationRecord{}), nil
	}
	j, err := e.Repository.Open(ctx, req.Key())
	if err != nil || j == nil {
		return e.recovery(req, "store_unavailable", RetainedRequest{}, operationRecord{}), nil
	}
	op := operation{engine: e, journal: j, request: req}
	// Failed release does not revoke a commit, but it is still an operational
	// failure. Preserve confirmed references and report recovery without retry.
	defer func() {
		if j.Close() != nil {
			out = op.recovery("store_unavailable")
		}
	}()
	raw, err := j.Read(ctx)
	if err != nil {
		return e.recovery(req, "store_unavailable", RetainedRequest{}, operationRecord{}), nil
	}
	if raw != nil {
		var ok bool
		op.record, op.binding, ok = decodeOperation(raw, req.Key())
		if !ok {
			return e.recovery(req, "store_invalid", RetainedRequest{}, operationRecord{}), nil
		}
		if op.binding.Digest() != req.Digest() {
			r := op.receipt("conflict", "idempotency_conflict")
			return op.output(r), nil
		}
		if req.Operation() == "prepare_workbench" || op.record.TerminalNoEffect {
			return op.historical(), nil
		}
		return op.observe(ctx), nil
	}
	if req.Operation() == "status" {
		return errorOutput(req, "operation_not_found"), nil
	}
	if err = e.Access.Prepare(ctx, req.Key()); err != nil {
		return errorOutput(req, reason(err, "authority_unknown")), nil
	}
	if e.Planner == nil || e.Lifecycle == nil || e.Clock == nil {
		return errorOutput(req, "source_unavailable"), nil
	}
	// The reference binds the complete tuple, not an ambiguous concatenation.
	op.operationRef = "operation-sha256:" + hash(canonical(object(req.Key())))
	if !op.save(ctx, operationRecord{Contract: OperationContract, Phase: "resolving"}) {
		return op.recovery("reservation_unresolved"), nil
	}
	return op.prepare(ctx), nil
}

type operation struct {
	engine       Engine
	journal      Journal
	request      Request
	record       operationRecord
	binding      RetainedRequest
	operationRef string
}

// save changes in-memory state only after an acknowledged durable transaction.
// A failed/lost acknowledgement is never retried, even if build ran successfully.
func (o *operation) save(ctx context.Context, next operationRecord) bool {
	if !immutableTransition(o.record, next) {
		return false
	}
	old := o.binding
	operationRef := o.operationRef
	if old.OperationRef() != "" {
		operationRef = old.OperationRef()
	}
	var built operationRecord
	var binding RetainedRequest
	calls := 0
	err := o.journal.Commit(ctx, old.DurableRevision(), func(rev string) ([]byte, error) {
		calls++
		if calls != 1 || !ref(rev) || rev == old.DurableRevision() {
			return nil, errors.New("store_invalid")
		}
		original := o.request
		if old.request.prepare != nil {
			original = old.request
		}
		var err error
		binding, err = RetainRequest(original, operationRef, rev)
		if err != nil {
			return nil, err
		}
		if old.request.prepare != nil && (old.Key() != binding.Key() || old.Digest() != binding.Digest() || old.OperationRef() != binding.OperationRef() || !bytes.Equal(old.CanonicalPrepare(), binding.CanonicalPrepare())) {
			return nil, errors.New("store_invalid")
		}
		var b []byte
		built, b, err = encodeOperation(next, binding)
		if err != nil {
			return nil, err
		}
		return b, nil
	})
	if err != nil || calls != 1 {
		return false
	}
	o.record, o.binding = built, binding
	return true
}

func (o *operation) view() View { return View{clone(o.record), o.binding} }
func (o *operation) receipt(status, reason string) Receipt {
	return Receipt{Kind: "receipt", OperationRef: o.binding.OperationRef(), DurableRevision: o.binding.DurableRevision(), Status: status, Reason: reason, Selected: clone(o.record.Selected), Lower: lowerBinding(o.record), Evidence: []RevisionRef{}}
}
func (o *operation) historical() Output {
	r := o.receipt("unknown", "readiness_unknown")
	if o.record.Receipt != nil {
		r = clone(*o.record.Receipt)
	}
	r.Historical = true
	r.Ready = nil
	return o.output(r)
}
func (o *operation) output(r Receipt) Output {
	b := envelope(o.request, object(r))
	if len(b) > MaxResponseBytes {
		return o.recovery("persistence_unresolved")
	}
	exit := 2
	if r.Status == "ready" && !r.Historical && r.Ready != nil {
		exit = 0
	} else if r.Status == "ready" || r.Status == "pending" || r.Status == "unknown" {
		exit = 3
	}
	return Output{b, exit}
}
func (o *operation) recovery(reason string) Output {
	return o.engine.recovery(o.request, reason, o.binding, o.record)
}

func (o *operation) terminal(ctx context.Context, status, code string) Output {
	return o.finishNoEffect(ctx, clone(o.record), status, code)
}
func (o *operation) finishNoEffect(ctx context.Context, n operationRecord, status, code string) Output {
	n.Phase = status
	n.TerminalNoEffect = true
	r := o.receipt(status, code)
	n.Receipt = &r
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	return o.output(*o.record.Receipt)
}

func (o *operation) prepare(ctx context.Context) Output {
	p, _ := o.binding.Prepare()
	plan, err := o.engine.Planner.Resolve(ctx, p)
	if err != nil {
		return o.terminal(ctx, "unavailable", reason(err, "source_unavailable"))
	}
	// A terminal decision is retained once; it cannot reopen via status. Partial
	// source evidence is allowed, but a selected proposal must be complete.
	if plan.Action == "" {
		if plan.Selected != nil || !validReason(plan.Reason) || plan.Reason == "ok" || (plan.Status != "deferred" && plan.Status != "refused" && plan.Status != "unavailable" && plan.Status != "unknown") {
			return o.terminal(ctx, "unavailable", "source_conflict")
		}
		n := clone(o.record)
		if plan.Sources != nil {
			refs, ok := normalizeReferences(plan.Sources.References)
			if !ok || !digest(plan.Sources.Digest) || len(plan.Sources.Data) == 0 || hash(plan.Sources.Data) != plan.Sources.Digest {
				return o.terminal(ctx, "unavailable", "source_conflict")
			}
			n.Sources = clone(plan.Sources)
			n.Sources.References = refs
		}
		if plan.Proposal != nil {
			if !validRevision(*plan.Proposal) || n.Sources == nil {
				return o.terminal(ctx, "unavailable", "source_conflict")
			}
			n.Proposal = clone(plan.Proposal)
		}
		return o.finishNoEffect(ctx, n, plan.Status, plan.Reason)
	}
	if (plan.Action != "start" && plan.Action != "reuse") || plan.Status != "selected" || plan.Reason != "ok" || plan.Sources == nil || plan.Proposal == nil || !validSelected(plan.Selected, p) {
		return o.terminal(ctx, "unavailable", "source_conflict")
	}
	n := clone(o.record)
	n.Phase = "selected"
	n.Sources = clone(plan.Sources)
	n.Proposal = clone(plan.Proposal)
	n.Selected = clone(plan.Selected)
	n.Action = plan.Action
	var ok bool
	n.Sources.References, ok = normalizeReferences(n.Sources.References)
	if !ok || !recordShape(n, o.binding) {
		return o.terminal(ctx, "unavailable", "source_conflict")
	}
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	if n.Action == "reuse" {
		n.Phase = "observing"
		if !o.save(ctx, n) {
			return o.recovery("persistence_unresolved")
		}
		if err = o.engine.Planner.Revalidate(ctx, o.view()); err != nil {
			return o.terminal(ctx, "refused", reason(err, "authority_unknown"))
		}
		return o.callObserve(ctx)
	}
	n.Phase = "preparing"
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	mapping, err := o.engine.Lifecycle.Prepare(ctx, o.view())
	if err != nil {
		return o.terminal(ctx, "refused", reason(err, "unsupported_configuration"))
	}
	if !validLower(&mapping, o.record.Selected) {
		return o.terminal(ctx, "refused", "source_conflict")
	}
	n = clone(o.record)
	n.Lower = clone(&mapping)
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	if err = o.engine.Access.Prepare(ctx, o.binding.Key()); err != nil {
		return o.terminal(ctx, "refused", reason(err, "authority_unknown"))
	}
	if err = o.engine.Planner.Revalidate(ctx, o.view()); err != nil {
		return o.terminal(ctx, "refused", reason(err, "authority_unknown"))
	}
	// Verify a minimal uncertainty receipt fits with the retained full snapshot
	// and mapping before effects. Larger actual observations still fail closed to
	// the separately admitted recovery envelope; no stored identity is discarded.
	probe := clone(o.record)
	probe.Phase = "unknown"
	r := o.receipt("unknown", "preparation_unresolved")
	probe.Receipt = &r
	// A future opaque revision can use the full Ref budget and all escaped
	// controls. Measure that exact shape, rather than reserve a guessed margin.
	future, _ := RetainRequest(o.binding.request, o.binding.OperationRef(), strings.Repeat("\x00", 256))
	if _, _, err := encodeOperation(probe, future); err != nil {
		return o.terminal(ctx, "refused", "output_budget")
	}
	if ctx.Err() != nil {
		return o.terminal(ctx, "refused", "authority_unknown")
	}
	n = clone(o.record)
	n.PossibleEffect = true
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	observed, err := o.engine.Lifecycle.Start(ctx, o.view())
	if err != nil {
		return o.unresolved(ctx, reason(err, "preparation_unresolved"))
	}
	n = clone(o.record)
	n.Phase = "observing"
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	return o.acceptObservation(ctx, observed)
}

func (o *operation) observe(ctx context.Context) Output {
	// A crash before selection/mapping never triggers discovery or preparation.
	if o.record.Selected == nil || (o.record.Action == "start" && o.record.Lower == nil) {
		return o.historical()
	}
	if o.engine.Lifecycle == nil || o.engine.Clock == nil {
		return o.recovery("observation_unresolved")
	}
	if err := o.engine.Access.Observe(ctx, o.binding.Key()); err != nil {
		return o.recovery(reason(err, "authority_unknown"))
	}
	if o.record.Phase == "selected" || o.record.Phase == "preparing" {
		n := clone(o.record)
		n.Phase = "observing"
		if !o.save(ctx, n) {
			return o.recovery("persistence_unresolved")
		}
	}
	return o.callObserve(ctx)
}
func (o *operation) callObserve(ctx context.Context) Output {
	observed, err := o.engine.Lifecycle.Observe(ctx, o.view())
	if err != nil {
		return o.unresolved(ctx, reason(err, "observation_unresolved"))
	}
	return o.acceptObservation(ctx, observed)
}
func (o *operation) unresolved(ctx context.Context, code string) Output {
	n := clone(o.record)
	n.Phase = "unknown"
	n.LastObservation = nil
	r := o.receipt("unknown", code)
	n.Receipt = &r
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	return o.output(*o.record.Receipt)
}
func (o *operation) acceptObservation(ctx context.Context, observed Observation) Output {
	// Validate raw Go strings before JSON copying can replace invalid UTF-8.
	if !observationShapeUnordered(observed, o.record, o.binding) {
		return o.unresolved(ctx, "source_conflict")
	}
	observed = clone(observed)
	var ok bool
	observed.Evidence, ok = normalizeReferences(observed.Evidence)
	if !ok || !observationShape(&observed, o.record, o.binding) {
		return o.unresolved(ctx, "source_conflict")
	}
	if !observationFresh(observed, o.engine.Clock()) {
		return o.unresolved(ctx, "readiness_expired")
	}
	n := clone(o.record)
	n.LastObservation = &observed
	n.Phase = observed.Status
	if observed.Status == "pending" {
		n.Phase = "observing"
	}
	if observed.Ready != nil {
		n.KnownExecution = clone(observed.Ready.Execution)
	}
	r := o.receipt(observed.Status, observed.Reason)
	r.Ready = clone(observed.Ready)
	r.Evidence = clone(observed.Evidence)
	n.Receipt = &r
	if !o.save(ctx, n) {
		return o.recovery("persistence_unresolved")
	}
	return o.output(*o.record.Receipt)
}

func reason(err error, fallback string) string {
	var r Refusal
	if errors.As(err, &r) && validReason(r.Reason) && r.Reason != "ok" {
		return r.Reason
	}
	var pointer *Refusal
	if errors.As(err, &pointer) && pointer != nil && validReason(pointer.Reason) && pointer.Reason != "ok" {
		return pointer.Reason
	}
	return fallback
}
func envelope(req Request, result any) []byte {
	k := req.Key()
	return append(canonical(map[string]any{"contract": Contract, "operation": req.Operation(), "correlation_id": k.CorrelationID, "caller": k.Caller, "project": k.Project, "request_digest": req.Digest(), "result": result}), '\n')
}
func errorOutput(req Request, code string) Output {
	return Output{envelope(req, map[string]any{"kind": "error", "code": code}), 2}
}
func (e Engine) recovery(req Request, code string, b RetainedRequest, r operationRecord) Output {
	var op, rev any
	if ref(b.OperationRef()) {
		op = b.OperationRef()
	}
	if ref(b.DurableRevision()) {
		rev = b.DurableRevision()
	}
	m := map[string]any{"kind": "recovery", "operation_ref": op, "last_durable_revision": rev, "status": "unknown", "reason": code, "historical": false, "key": object(req.Key()), "request_digest": req.Digest(), "lower_launch": object(lowerBinding(r))}
	for _, field := range []string{"", "lower_launch", "last_durable_revision", "operation_ref"} {
		if field != "" {
			m[field] = nil
		}
		b := envelope(req, m)
		if len(b) <= MaxRecoveryBytes {
			return Output{b, 3}
		}
	}
	// Parse/RestoreRetained proved this exact minimal envelope fits with the
	// longest reason. Reaching here would violate an internal invariant.
	panic("invalid recovery capacity")
}
