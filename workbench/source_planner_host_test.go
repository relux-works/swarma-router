package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"relux.works/swarma-router/inventory"
	"relux.works/swarma-router/selection"
)

func plannerHostExecution(b inventory.Binding) map[string]string {
	return map[string]string{
		"kind": "host", "execution": b.Execution, "runner_instance": b.RunnerInstance,
		"native_thread": "fixture-thread", "controller_epoch": "0",
		"messaging_session": "original-session", "messaging_thread": "original-thread", "messaging_generation": "original-generation",
	}
}

// This fixture feeds actual DATA parsing with a complete, separately bound Host
// owner target. It makes no production receipt or native-readiness claim.
func plannerHostFixture(t *testing.T, state selection.State) (*ownerFixture, SourcePlanner, Request, context.Context, map[string]any, *int) {
	t.Helper()
	o, p, r, ctx, _ := plannerFixture(t, state)
	data, err := os.ReadFile("../inventory/testdata/source-compatible.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	b := &o.data.Targets[0].Binding
	b.OwnerMember = m["member"].(string)
	b.OwnerProject = m["project"].(string)
	b.Workbench = m["workbench"].(string)
	facts := m["facts"].(map[string]any)
	b.Model = facts["configured_model"].(map[string]any)["value"].(string)
	rm := base(t)
	cfg(rm)["vendor"] = facts["configured_vendor"].(map[string]any)["value"]
	r = mustParse(t, frame(t, rm), local)
	b.Configuration = r.prepare.Constraints.Configurations[0]
	expected := inventory.Binding{SourceVersions: []string{m["source_version"].(string)}, OwnerBindingRef: m["owner_binding_ref"].(string), Member: b.OwnerMember, Project: b.OwnerProject, Workbench: b.Workbench, Execution: m["execution"].(string), RunnerInstance: m["runner_instance"].(string)}
	if state != selection.Stopped {
		b.Execution = plannerHostExecution(expected)
	}
	calls := new(int)
	o.data.Targets[0].Host = &HostInput{Binding: expected, Source: sourceFunc(func(_ context.Context, w io.Writer) error {
		*calls++
		_, err := w.Write(frame(t, m))
		return err
	})}
	bindPlannerTarget(t, &o.data.Targets[0], r, p.Clock())
	return o, p, r, ctx, m, calls
}

// Either known true stop fact conflicts with Ready reuse. False/unknown facts
// leave separate qualified owner evidence usable; they never establish Ready.
// The same contradiction is checked again on the retained target.
func TestSourcePlannerHostStopFacts(t *testing.T) {
	for _, fact := range []string{"security_stopped", "stop_requested"} {
		for _, value := range []string{"false", "true", "unknown"} {
			t.Run(fact+"/"+value, func(t *testing.T) {
				o, p, r, ctx, m, calls := plannerHostFixture(t, selection.Ready)
				initial := mustPlan(t, p, ctx, r)
				if initial.Action != "reuse" || initial.Selected == nil {
					t.Fatal("exact Host positive control refused", initial)
				}
				facts := m["facts"].(map[string]any)
				if value == "unknown" {
					facts[fact] = map[string]any{"known": false, "reason": "not-observed"}
				} else {
					facts[fact].(map[string]any)["value"] = value == "true"
				}
				plan := mustPlan(t, p, ctx, r)
				doc, ok := restoreSourceDocument(plan.Sources.Data)
				if !ok || doc.Inventory[0].Result.Class != "usable" {
					t.Fatal("stop case did not exercise usable DATA", plan)
				}
				wantState := selection.Ready
				if value == "true" {
					wantState = selection.StateConflict
					if plan.Selected != nil || plan.Action != "" {
						t.Fatal("known stop fact permitted reuse", fact, plan)
					}
				} else if plan.Action != "reuse" || plan.Selected == nil {
					t.Fatal("false/unknown stop fact overrode owner Ready", value, plan)
				}
				n, err := p.read(ctx, r, nil)
				if err != nil || n.input.Candidates[0].State != wantState {
					t.Fatal("wrong stop classification", wantState, n.input, err)
				}
				err = p.Revalidate(ctx, plannerView(t, r, initial))
				if value == "true" {
					if !errors.Is(err, Refusal{"evidence_insufficient"}) {
						t.Fatal("retained stop contradiction not refused", err)
					}
				} else if err != nil {
					t.Fatal("valid retained Host refused", err)
				}
				if *calls != 4 || o.narrowed != 1 {
					t.Fatal("unexpected Host reads or retained narrowing", *calls, o.narrowed)
				}
			})
		}
	}
}

// An otherwise self-consistent frame for another run is refused against the
// owner's known Host identity before Feed. Both identity fields are tested
// with/without an additional stop contradiction; exact bindings reuse normally.
func TestSourcePlannerHostExecutionBinding(t *testing.T) {
	for _, field := range []string{"execution", "runner_instance"} {
		for _, stop := range []bool{false, true} {
			name := field + "/clear"
			if stop {
				name = field + "/stopped"
			}
			t.Run(name, func(t *testing.T) {
				o, p, r, ctx, m, calls := plannerHostFixture(t, selection.Ready)
				initial := mustPlan(t, p, ctx, r)
				if initial.Action != "reuse" {
					t.Fatal("exact Host positive control refused", initial)
				}
				// Keep the source frame and its expected binding consistent. Only
				// comparison with the independent target can detect this mismatch.
				m[field] = "another-run"
				if field == "execution" {
					o.data.Targets[0].Host.Binding.Execution = "another-run"
				} else {
					o.data.Targets[0].Host.Binding.RunnerInstance = "another-run"
				}
				m["facts"].(map[string]any)["security_stopped"].(map[string]any)["value"] = stop
				req, _ := r.Prepare()
				plan, err := p.Resolve(ctx, req)
				if !errors.Is(err, Refusal{"source_conflict"}) || plan.Selected != nil || plan.Sources != nil {
					t.Fatal("another Host run not refused", plan, err)
				}
				if err := p.Revalidate(ctx, plannerView(t, r, initial)); !errors.Is(err, Refusal{"source_conflict"}) {
					t.Fatal("retained another-run binding accepted", err)
				}
				if *calls != 1 {
					t.Fatal("mismatched Host was read", *calls)
				}
			})
		}
	}
}

// a2a execution cannot be equated with Host-native identifiers. Requested Host
// DATA without that link cannot qualify reuse, while no-Host a2a still can.
// A historical Host row does not undo separately qualified stopped absence.
func TestSourcePlannerHostIdentityCompatibility(t *testing.T) {
	for _, mode := range []string{"host-ready", "a2a-no-host", "a2a-with-host", "stopped-historical-host"} {
		t.Run(mode, func(t *testing.T) {
			state := selection.Ready
			if mode == "stopped-historical-host" {
				state = selection.Stopped
			}
			o, p, r, ctx, m, calls := plannerHostFixture(t, state)
			if mode == "a2a-no-host" || mode == "a2a-with-host" {
				o.data.Targets[0].Binding.Execution = map[string]string{"kind": "a2a", "session": "original-session", "thread": "original-thread", "generation": "original-generation"}
				if mode == "a2a-no-host" {
					o.data.Targets[0].Host = nil
				}
				bindPlannerTarget(t, &o.data.Targets[0], r, p.Clock())
			}
			if state == selection.Stopped {
				facts := m["facts"].(map[string]any)
				facts["configured_model"].(map[string]any)["value"] = "historical-model"
				facts["native_model"].(map[string]any)["value"] = "historical-model"
				facts["security_stopped"].(map[string]any)["value"] = true
				facts["stop_requested"].(map[string]any)["value"] = true
			}
			plan := mustPlan(t, p, ctx, r)
			if mode == "a2a-with-host" {
				if plan.Selected != nil || plan.Status != "unavailable" || plan.Reason != "evidence_insufficient" {
					t.Fatal("unlinked Host identity permitted reuse", plan)
				}
				n, err := p.read(ctx, r, nil)
				if err != nil || n.input.Candidates[0].State != selection.StateUnknown {
					t.Fatal("unlinked Host did not remain unknown", n.input, err)
				}
			} else {
				want := "reuse"
				if state == selection.Stopped {
					want = "start"
				}
				if plan.Action != want || plan.Selected == nil {
					t.Fatal("valid identity form refused", mode, plan)
				}
				if err := p.Revalidate(ctx, plannerView(t, r, plan)); err != nil {
					t.Fatal("valid identity revalidation refused", err)
				}
			}
			wantCalls := 2
			if mode == "a2a-no-host" {
				wantCalls = 0
			}
			if *calls != wantCalls {
				t.Fatal("unexpected Host reads", *calls, wantCalls)
			}
		})
	}
}
