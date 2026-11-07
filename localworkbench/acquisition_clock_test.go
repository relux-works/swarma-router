package localworkbench

import (
	"context"
	"testing"
	"time"
)

// A wall-clock rollback during the single admitted read must not revive source
// evidence that was already expired when acquisition began. Both entry points
// use the same boundary; a retained target does not bypass it.
func TestAcquisitionClockRollback(t *testing.T) {
	for _, action := range []string{"reuse", "start"} {
		for _, retained := range []bool{false, true} {
			for _, mode := range []string{"steady", "forward", "rollback", "revived-expiry"} {
				t.Run(action+"/"+map[bool]string{false: "recommend", true: "revalidate"}[retained]+"/"+mode, func(t *testing.T) {
					r, in := fixture(action)
					before, after := now, now
					switch mode {
					case "forward":
						after = now.Add(2 * time.Minute)
					case "rollback":
						before = now.Add(time.Second)
					case "revived-expiry":
						before = now.Add(2 * time.Minute)
					}
					clock := before
					reads := 0
					p := LocalPlanner{
						Local: LocalContext{r.Caller, r.Project}, Room: "!room:one.example",
						Clock: func() time.Time { return clock },
						Reader: readerFunc(func(_ context.Context, _ LocalRequest, selected *LocalSelection) (LocalOwnerInputs, error) {
							reads++
							if (selected != nil) != retained {
								t.Fatal("changed retained-target read scope")
							}
							clock = after
							return in, nil
						}),
					}
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					var out LocalRecommendation
					var err error
					if retained {
						d, e := RequestDigest(r)
						if e != nil {
							t.Fatal(e)
						}
						out, err = p.Revalidate(ctx, r, LocalSelection{RequestDigest: d, Target: in.Targets[0].Binding})
					} else {
						out, err = p.Recommend(ctx, r)
					}
					if reads != 1 {
						t.Fatalf("source reads = %d, want one", reads)
					}
					if mode == "rollback" || mode == "revived-expiry" {
						if err == nil || err.Error() != "source_unavailable" || out.Selected != nil || out.PreparedWorkbench != nil {
							t.Fatalf("clock rollback returned a recommendation: action=%s error=%v", out.Action, err)
						}
					} else if mode == "forward" {
						if err != nil || out.Action != "insufficient_evidence" || out.Selected != nil || out.PreparedWorkbench != nil {
							t.Fatalf("forward expiry: action=%s error=%v", out.Action, err)
						}
					} else if err != nil || out.Action != action || out.Selected == nil || out.PreparedWorkbench == nil || out.ExecutionAuthority {
						t.Fatalf("steady-clock positive control: action=%s error=%v", out.Action, err)
					}
				})
			}
		}
	}
}
