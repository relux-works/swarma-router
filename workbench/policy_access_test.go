package workbench

import (
	"context"
	"testing"
	"time"
)

type testPolicySource struct {
	decision PolicyDecision
	calls    int
	key      OperationKey
	action   PolicyAction
	now      time.Time
	cancel   context.CancelFunc
}

func (s *testPolicySource) Decide(ctx context.Context, k OperationKey, action PolicyAction, clock func() time.Time) PolicyDecision {
	s.calls++
	s.key, s.action, s.now = k, action, clock()
	if s.cancel != nil {
		s.cancel()
	}
	return s.decision
}

// An independently injected source supplies exactly one decision for the
// unchanged tuple/action/clock; invalid and absent sources never grant access.
func TestPolicySourceInterchangeability(t *testing.T) {
	for _, action := range []PolicyAction{PolicyRead, PolicyPrepare, PolicyObserve} {
		for _, decision := range []PolicyDecision{PolicyAllow, PolicyDeny, PolicyUnknown, 255} {
			t.Run(string(action)+"/"+time.Duration(decision).String(), func(t *testing.T) {
				_, k, ctx, now := accessFixture(t)
				s := &testPolicySource{decision: decision}
				a := &PolicyAccess{Local: LocalContext{k.Caller, k.Project}, Clock: func() time.Time { return now }, Source: s}
				method := map[PolicyAction]func(context.Context, OperationKey) error{PolicyRead: a.Read, PolicyPrepare: a.Prepare, PolicyObserve: a.Observe}[action]
				want := "authority_unknown"
				if decision == PolicyAllow {
					want = ""
				}
				if decision == PolicyDeny {
					want = "authority_denied"
				}
				accessReason(t, method(ctx, k), want)
				if s.calls != 1 || s.key != k || s.action != action || s.now != now {
					t.Fatal("source received a different scope, action, time or call count")
				}
			})
		}
	}
}

// Scope/time/context refusal occurs before source invocation; cancellation by a
// source overrides even its allow. Zero values and nil receivers fail closed.
func TestPolicySourceBoundary(t *testing.T) {
	for _, mode := range []string{"caller", "project", "key", "nil-source", "nil-clock", "zero-clock", "bad-clock", "nil-context", "unbounded", "canceled", "source-cancel"} {
		t.Run(mode, func(t *testing.T) {
			_, k, ctx, now := accessFixture(t)
			s := &testPolicySource{decision: PolicyAllow}
			a := &PolicyAccess{Local: LocalContext{k.Caller, k.Project}, Clock: func() time.Time { return now }, Source: s}
			want := "authority_unknown"
			calls := 0
			switch mode {
			case "caller":
				k.Caller = "other"
				want = "authority_denied"
			case "project":
				k.Project = "other"
				want = "authority_denied"
			case "key":
				k.CorrelationID = ""
			case "nil-source":
				a.Source = nil
			case "nil-clock":
				a.Clock = nil
			case "zero-clock":
				a.Clock = func() time.Time { return time.Time{} }
			case "bad-clock":
				a.Clock = func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }
			case "nil-context":
				ctx = nil
			case "unbounded":
				ctx = context.Background()
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "source-cancel":
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				ctx = c
				s.cancel = cancel
				calls = 1
			}
			accessReason(t, a.Read(ctx, k), want)
			if s.calls != calls {
				t.Fatal("refusal crossed source boundary")
			}
		})
	}
	var a *PolicyAccess
	_, k, ctx, _ := accessFixture(t)
	accessReason(t, a.Read(ctx, k), "authority_unknown")
	accessReason(t, (&PolicyAccess{}).Prepare(ctx, k), "authority_unknown")
}

// File acquisition samples time after validating bytes, so expiry/invalid time
// reached during the read does not retain an earlier allow. No atomic guarantee
// is made for revocation after that final current check.
func TestLocalPolicyTimeAfterRead(t *testing.T) {
	for _, mode := range []string{"valid", "expired", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			a, k, ctx, now := accessFixture(t)
			current := now
			a.Clock = func() time.Time { return current }
			a.step = func(string) {
				if mode == "expired" {
					current = now.Add(time.Minute)
				}
				if mode == "invalid" {
					current = time.Time{}
				}
			}
			want := "authority_unknown"
			if mode == "valid" {
				want = ""
			}
			accessReason(t, a.Read(ctx, k), want)
		})
	}
}
