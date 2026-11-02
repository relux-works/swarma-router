package workbench

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// A fresh fast commit/close returns ready. Exact witness/ready expiry or caller
// cancellation during either successful persistence boundary cannot return a
// fresh-ready binding. The acknowledged original record and known identity stay
// intact, and subsequent historical replay performs no new work. The injected
// clock/cancellation is deterministic; no sleeps or actual runtime effects occur.
func TestOperationReturnBoundary(t *testing.T) {
	for _, action := range []string{"start", "reuse", "status"} {
		for _, stage := range []string{"commit", "close"} {
			for _, mode := range []string{"valid", "witness-expiry", "ready-expiry", "cancel"} {
				t.Run(action+"/"+stage+"/"+mode, func(t *testing.T) {
					fixtureAction := action
					if action == "status" {
						fixtureAction = "start"
					}
					f, e, req := newOperationFixture(t, fixtureAction)
					if action == "status" {
						response(t, invocation(t, e, req), "receipt", "ready", "ok", 0)
					}
					originalReq := req
					if action == "status" {
						req = statusRequest(t, req)
					}
					start := f.now
					observe := f.observation
					if mode == "ready-expiry" {
						f.observation = func(v View) Observation {
							o := observe(v)
							o.Ready.ValidUntil = start.Add(30 * time.Second).Format(wireTime)
							return o
						}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					boundaryCalls := 0
					change := func() {
						boundaryCalls++
						switch mode {
						case "valid":
							f.now = start.Add(time.Millisecond)
						case "witness-expiry":
							f.now = start.Add(time.Minute)
						case "ready-expiry":
							f.now = start.Add(30 * time.Second)
						case "cancel":
							cancel()
						}
					}
					if stage == "commit" {
						f.onCommit = func(r operationRecord) {
							if r.Phase == "ready" {
								change()
							}
						}
					} else {
						f.onClose = change
					}
					out, err := e.Run(ctx, req)
					if err != nil || boundaryCalls != 1 {
						t.Fatal("boundary not exercised", err, boundaryCalls)
					}
					state, binding := retained(t, f)
					if state.Phase != "ready" || state.KnownExecution["generation"] != "original-generation" || binding.Key() != originalReq.Key() || binding.Digest() != originalReq.Digest() {
						t.Fatal("acknowledged original record changed")
					}
					if mode == "valid" {
						response(t, out, "receipt", "ready", "ok", 0)
					} else {
						reason := "readiness_expired"
						if mode == "cancel" {
							reason = "authority_unknown"
						}
						x := response(t, out, "recovery", "unknown", reason, 3)
						if x["ready"] != nil || x["operation_ref"] != binding.OperationRef() || x["last_durable_revision"] != binding.DurableRevision() || !same(x["key"], object(originalReq.Key())) || x["request_digest"] != originalReq.Digest() {
							t.Fatal("return boundary exposed ready or lost recovery identity")
						}
					}
					f.onCommit, f.onClose = nil, nil
					before := bytes.Clone(f.raw)
					counts := []int{f.resolve, f.prepare, f.start, f.observe, f.commits}
					pure := e
					pure.Planner, pure.Lifecycle, pure.Clock = nil, nil, nil
					x := response(t, invocation(t, pure, originalReq), "receipt", "ready", "ok", 3)
					if x["historical"] != true || x["ready"] != nil || !bytes.Equal(before, f.raw) || !sameCounters(counts, f) || f.start > 1 || f.prepare > 1 {
						t.Fatal("recovery replay changed bytes or executed again")
					}
				})
			}
		}
	}
}
