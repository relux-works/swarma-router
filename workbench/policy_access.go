package workbench

import (
	"context"
	"time"
)

// PolicyAction names only the three existing Access permissions.
type PolicyAction string

const (
	PolicyRead    PolicyAction = "read"
	PolicyPrepare PolicyAction = "prepare"
	PolicyObserve PolicyAction = "observe"
)

// PolicyDecision is closed; unknown (including the zero value) grants nothing.
type PolicyDecision uint8

const (
	PolicyUnknown PolicyDecision = iota
	PolicyAllow
	PolicyDeny
)

// PolicySource is a router-owned current-permission seam. Trusted embedding
// selects it outside request JSON. Implementations honor the bounded context,
// validate their whole source, then sample the trusted clock and return a current
// exact decision. The clock is embedding configuration, never request data. They
// never supply task, readiness or effect authority. No fallback is selected.
type PolicySource interface {
	Decide(context.Context, OperationKey, PolicyAction, func() time.Time) PolicyDecision
}

// PolicyAccess binds trusted scope and time to any admitted policy source.
// Configure before use; do not mutate concurrently. There is no cached grant or
// atomic revocation-after-last-check promise. A source is independently admitted;
// implementing this interface alone does not authenticate its decisions.
type PolicyAccess struct {
	Local  LocalContext
	Clock  func() time.Time
	Source PolicySource
}

var _ Access = (*PolicyAccess)(nil)

func (a *PolicyAccess) Read(ctx context.Context, k OperationKey) error {
	return a.check(ctx, k, PolicyRead)
}
func (a *PolicyAccess) Prepare(ctx context.Context, k OperationKey) error {
	return a.check(ctx, k, PolicyPrepare)
}
func (a *PolicyAccess) Observe(ctx context.Context, k OperationKey) error {
	return a.check(ctx, k, PolicyObserve)
}

func policyContext(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	_, bounded := ctx.Deadline()
	return bounded
}
func policyTime(now time.Time) bool {
	if now.IsZero() {
		return false
	}
	_, err := now.MarshalJSON()
	return err == nil
}
func validPolicyAction(action PolicyAction) bool {
	return action == PolicyRead || action == PolicyPrepare || action == PolicyObserve
}
func (a *PolicyAccess) check(ctx context.Context, k OperationKey, action PolicyAction) error {
	unknown := Refusal{"authority_unknown"}
	if a == nil || !policyContext(ctx) || a.Clock == nil || !ref(a.Local.Caller) || !ref(a.Local.Project) {
		return unknown
	}
	if k.Caller != a.Local.Caller || k.Project != a.Local.Project {
		return Refusal{"authority_denied"}
	}
	if !ref(k.CorrelationID) || a.Source == nil {
		return unknown
	}
	now := a.Clock()
	if !policyContext(ctx) || !policyTime(now) {
		return unknown
	}
	decision := a.Source.Decide(ctx, k, action, a.Clock)
	if !policyContext(ctx) {
		return unknown
	}
	switch decision {
	case PolicyAllow:
		return nil
	case PolicyDeny:
		return Refusal{"authority_denied"}
	default:
		return unknown
	}
}
