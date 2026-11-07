package workbench

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"relux.works/swarma-router/selection"
)

func TestSourcePlannerHostAcquisitionClockRollback(t *testing.T) {
	for _, state := range []selection.State{selection.Ready, selection.Stopped} {
		for _, retained := range []bool{false, true} {
			t.Run(string(state)+"/"+map[bool]string{false: "resolve", true: "revalidate"}[retained], func(t *testing.T) {
				o, p, r, ctx, _, calls := plannerHostFixture(t, state)
				clock := p.Clock()
				p.Clock = func() time.Time { return clock }
				initial := mustPlan(t, p, ctx, r)
				host := o.data.Targets[0].Host
				original := host.Source
				clock = clock.Add(2 * time.Minute)
				host.Source = sourceFunc(func(ctx context.Context, w io.Writer) error {
					clock = clock.Add(-2 * time.Minute)
					return original.Feed(ctx, w)
				})
				var plan Plan
				var err error
				if retained {
					err = p.Revalidate(ctx, plannerView(t, r, initial))
				} else {
					req, _ := r.Prepare()
					plan, err = p.Resolve(ctx, req)
				}
				if !errors.Is(err, Refusal{"source_stale"}) || plan.Selected != nil || plan.Sources != nil || o.calls != 2 || *calls != 2 {
					t.Fatalf("Host read revived expired evidence or retried: action=%s error=%v owner=%d host=%d", plan.Action, err, o.calls, *calls)
				}
			})
		}
	}
}

// A current read may refresh the retained target, but it cannot move the
// evaluation clock behind the beginning of that same acquisition.
func TestSourcePlannerAcquisitionClockRollback(t *testing.T) {
	for _, state := range []selection.State{selection.Ready, selection.Stopped} {
		for _, retained := range []bool{false, true} {
			for _, mode := range []string{"steady", "forward", "rollback", "revived-expiry"} {
				t.Run(string(state)+"/"+map[bool]string{false: "resolve", true: "revalidate"}[retained]+"/"+mode, func(t *testing.T) {
					o, p, r, ctx, clock := plannerFixture(t, state)
					initial := mustPlan(t, p, ctx, r)
					base := *clock
					before, after := base, base
					switch mode {
					case "forward":
						after = base.Add(2 * time.Minute)
					case "rollback":
						before = base.Add(time.Second)
					case "revived-expiry":
						before = base.Add(2 * time.Minute)
					}
					*clock = before
					reads := o.calls
					o.onRead = func(only *SelectedBinding) {
						if (only != nil) != retained {
							t.Fatal("changed retained-target read scope")
						}
						*clock = after
					}
					var plan Plan
					var err error
					if retained {
						err = p.Revalidate(ctx, plannerView(t, r, initial))
					} else {
						req, _ := r.Prepare()
						plan, err = p.Resolve(ctx, req)
					}
					if o.calls != reads+1 {
						t.Fatal("clock failure retried source acquisition")
					}
					if mode == "rollback" || mode == "revived-expiry" {
						if !errors.Is(err, Refusal{"source_stale"}) || plan.Selected != nil || plan.Sources != nil {
							t.Fatalf("clock rollback accepted: action=%s error=%v", plan.Action, err)
						}
					} else if mode == "forward" {
						if retained {
							if !errors.Is(err, Refusal{"evidence_insufficient"}) {
								t.Fatalf("forward expiry accepted: %v", err)
							}
						} else if err != nil || plan.Selected != nil || plan.Status != "unavailable" || plan.Reason != "evidence_insufficient" {
							t.Fatalf("forward expiry: status=%s reason=%s error=%v", plan.Status, plan.Reason, err)
						}
					} else if err != nil || !retained && (plan.Selected == nil || plan.Action != initial.Action) {
						t.Fatalf("steady-clock positive control: action=%s error=%v", plan.Action, err)
					}
				})
			}
		}
	}
}
