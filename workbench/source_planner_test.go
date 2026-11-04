package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"relux.works/swarma-router/inventory"
	"relux.works/swarma-router/selection"
)

type ownerFixture struct {
	data            OwnerInputs
	err             error
	calls, narrowed int
	onRead          func(*SelectedBinding)
}

func (o *ownerFixture) Read(_ context.Context, _ PrepareRequest, only *SelectedBinding) (OwnerInputs, error) {
	o.calls++
	if only != nil {
		o.narrowed++
	}
	if o.onRead != nil {
		o.onRead(only)
	}
	return o.data, o.err
}

type sourceFunc func(context.Context, io.Writer) error

func (f sourceFunc) Feed(ctx context.Context, w io.Writer) error { return f(ctx, w) }

func plannerFixture(t *testing.T, state selection.State) (*ownerFixture, SourcePlanner, Request, context.Context, *time.Time) {
	t.Helper()
	r := mustParse(t, []byte(goldenPrepare+"\n"), local)
	req, _ := r.Prepare()
	now := time.Date(2026, 11, 2, 20, 0, 0, 0, time.UTC)
	b := TargetBinding{Member: req.Cards[0].Member, OwnerMember: "native-member", OwnerProject: req.Project, Workbench: "workbench", Card: req.Cards[0].Card, Configuration: req.Constraints.Configurations[0], Model: "native-model", Profile: "native-profile", Mapping: req.Task, State: state}
	if state == selection.Ready {
		b.Execution = map[string]string{"kind": "a2a", "session": "original-session", "thread": "original-thread", "generation": "original-generation"}
	}
	owner := &ownerFixture{data: OwnerInputs{Task: req.Task, Policy: req.Constraints.Policy, Cards: req.Cards, Targets: []OwnerTarget{{Binding: b}}}}
	bindPlannerTarget(t, &owner.data.Targets[0], r, now)
	p := SourcePlanner{Local: local, Owner: owner, Clock: func() time.Time { return now }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return owner, p, r, ctx, &now
}
func bindPlannerTarget(t *testing.T, target *OwnerTarget, r Request, now time.Time) {
	t.Helper()
	observed, expiry := now.Add(-time.Second), now.Add(time.Minute)
	w := BoundWitness{Key: r.Key(), RequestDigest: r.Digest(), TargetDigest: TargetBindingDigest(target.Binding), Reference: r.prepare.Task, ObservedAt: &observed, ExpiresAt: &expiry}
	target.Witness = w
	target.Checks = nil
	for _, list := range [][]selection.Gate{plannerCommon, plannerReuse, plannerStart} {
		for _, g := range list {
			target.Checks = append(target.Checks, OwnerCheck{g, selection.Allow, w})
		}
	}
}
func plannerView(t *testing.T, r Request, plan Plan) View {
	t.Helper()
	b, err := RetainRequest(r, "operation", "revision")
	if err != nil {
		t.Fatal(err)
	}
	return View{record: operationRecord{Contract: OperationContract, Selected: plan.Selected, Sources: plan.Sources, Proposal: plan.Proposal, Action: plan.Action}, binding: b}
}
func mustPlan(t *testing.T, p SourcePlanner, ctx context.Context, r Request) Plan {
	t.Helper()
	req, _ := r.Prepare()
	out, err := p.Resolve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Qualified owner receipts permit start/reuse; proposal/snapshot retain complete
// artifact and distinct native/execution identity. Revalidate narrows the source
// read to the same selection, with no alternative selection or native effect.
func TestSourcePlannerResolveAndRevalidate(t *testing.T) {
	for _, state := range []selection.State{selection.Ready, selection.Stopped} {
		t.Run(string(state), func(t *testing.T) {
			o, p, r, ctx, _ := plannerFixture(t, state)
			plan := mustPlan(t, p, ctx, r)
			want := "reuse"
			if state == selection.Stopped {
				want = "start"
			}
			if plan.Action != want || plan.Status != "selected" || plan.Reason != "ok" || plan.Selected.Member != r.prepare.Cards[0].Member || plan.Selected.Configuration != r.prepare.Constraints.Configurations[0] || plan.Sources.Digest != hash(plan.Sources.Data) {
				t.Fatal("selection mapping", plan)
			}
			doc, ok := restoreSourceDocument(plan.Sources.Data)
			if !ok || doc.Proposal.Scope != selection.Partial || doc.Targets[0].Binding.Model != "native-model" || doc.Targets[0].Binding.Configuration.Model != r.prepare.Constraints.Configurations[0].Model || doc.Proposal.Basis.TaskRevision != revisionToken(r.prepare.Task) {
				t.Fatal("lost complete/source identity")
			}
			if err := p.Revalidate(ctx, plannerView(t, r, plan)); err != nil || o.calls != 2 || o.narrowed != 1 {
				t.Fatal("retained revalidation", err, o.calls, o.narrowed)
			}
			plan.Sources.Data[0] = 'x'
			if o.data.Targets[0].Binding.Model != "native-model" {
				t.Fatal("input aliased")
			}
		})
	}
}

// Required checks are independently enumerated here; every missing/deny/unknown/
// conflict/misbound/future/expired witness excludes its relevant action. Optional
// checks for the other action do not become new gates. State proof has its own
// validity and may only shorten, never extend the proposal's dependency expiry.
func TestSourcePlannerEvidenceBoundaries(t *testing.T) {
	common := []selection.Gate{"caller_authority", "task_constraints", "card_member", "profile_runtime", "quality", "provider_limits", "manager_custody", "board_assignment", "messaging_admission"}
	for _, state := range []selection.State{selection.Ready, selection.Stopped} {
		specific := []selection.Gate{"current_configuration", "exact_generation", "ready_response", "occupancy"}
		if state == selection.Stopped {
			specific = []selection.Gate{"complete_owned_absence", "preparation_authority", "launch_capability"}
		}
		for _, gate := range append(common, specific...) {
			for _, mode := range []string{"missing", "deny", "unknown", "conflict", "wrong-key", "wrong-digest", "wrong-target", "future", "expired", "no-times", "zero-witness"} {
				t.Run(string(state)+"/"+string(gate)+"/"+mode, func(t *testing.T) {
					o, p, r, ctx, now := plannerFixture(t, state)
					checks := o.data.Targets[0].Checks
					for i, c := range checks {
						if c.Gate != gate {
							continue
						}
						switch mode {
						case "missing":
							checks = append(checks[:i], checks[i+1:]...)
						case "deny":
							checks[i].Verdict = selection.Deny
						case "unknown":
							checks[i].Verdict = selection.Unknown
						case "conflict":
							checks[i].Verdict = selection.Conflict
						case "wrong-key":
							checks[i].Witness.Key.CorrelationID = "another"
						case "wrong-digest":
							checks[i].Witness.RequestDigest = strings.Repeat("0", 64)
						case "wrong-target":
							checks[i].Witness.TargetDigest = strings.Repeat("0", 64)
						case "future":
							x := now.Add(time.Second)
							checks[i].Witness.ObservedAt = &x
						case "expired":
							x := *now
							checks[i].Witness.ExpiresAt = &x
						case "no-times":
							checks[i].Witness.ObservedAt = nil
							checks[i].Witness.ExpiresAt = nil
						case "zero-witness":
							checks[i].Witness = BoundWitness{}
						}
						break
					}
					o.data.Targets[0].Checks = checks
					plan := mustPlan(t, p, ctx, r)
					if plan.Selected != nil || plan.Action != "" {
						t.Fatal("required invalid evidence selected", mode, gate)
					}
				})
			}
		}
	}
	for _, mode := range []string{"expired", "missing", "earlier", "optional"} {
		t.Run(mode, func(t *testing.T) {
			o, p, r, ctx, now := plannerFixture(t, selection.Stopped)
			switch mode {
			case "expired":
				x := *now
				o.data.Targets[0].Witness.ExpiresAt = &x
			case "missing":
				o.data.Targets[0].Witness = BoundWitness{}
			case "earlier":
				x := now.Add(time.Second)
				o.data.Targets[0].Witness.ExpiresAt = &x
			case "optional":
				for i, c := range o.data.Targets[0].Checks {
					if c.Gate == "current_configuration" {
						o.data.Targets[0].Checks[i].Verdict = selection.Deny
					}
				}
			}
			plan := mustPlan(t, p, ctx, r)
			if mode == "expired" || mode == "missing" {
				if plan.Selected != nil {
					t.Fatal("invalid state evidence selected")
				}
			} else if plan.Selected == nil {
				t.Fatal("valid/optional control refused")
			} else if mode == "earlier" {
				doc, _ := restoreSourceDocument(plan.Sources.Data)
				if !doc.Proposal.ValidUntil.Equal(now.Add(time.Second)) {
					t.Fatal("state validity extended")
				}
			}
		})
	}
}

// Native labels never collapse different complete requested artifact identities.
// Semantic permutations preserve the exact full snapshot/proposal. Alternate
// configurations for one stopped member remain valid, without implied cost rank.
func TestSourcePlannerFullIdentityAndPermutation(t *testing.T) {
	o, p, r, ctx, now := plannerFixture(t, selection.Stopped)
	m := base(t)
	first := clone(cfg(m))
	second := clone(first)
	second["model"].(map[string]any)["source"] = "different-source"
	second["profile"].(map[string]any)["revision"] = "different-revision"
	m["constraints"].(map[string]any)["configurations"] = []any{second, first}
	r = mustParse(t, frame(t, m), local)
	o.data.Targets = nil
	for _, c := range r.prepare.Constraints.Configurations {
		b := TargetBinding{Member: c.Member, OwnerMember: "native-member", OwnerProject: r.Key().Project, Workbench: "workbench", Card: r.prepare.Cards[0].Card, Configuration: c, Model: "same-native-model", Profile: "same-native-profile", Mapping: r.prepare.Task, State: selection.Stopped}
		target := OwnerTarget{Binding: b}
		bindPlannerTarget(t, &target, r, *now)
		o.data.Targets = append(o.data.Targets, target)
	}
	one := mustPlan(t, p, ctx, r)
	doc, _ := restoreSourceDocument(one.Sources.Data)
	if len(doc.Targets) != 2 || candidateIdentity(doc.Targets[0].Binding).ID == candidateIdentity(doc.Targets[1].Binding).ID || doc.Targets[0].Binding.Configuration == doc.Targets[1].Binding.Configuration {
		t.Fatal("full artifact identity collapsed")
	}
	o.data.Targets[0], o.data.Targets[1] = o.data.Targets[1], o.data.Targets[0]
	for i := range o.data.Targets {
		checks := o.data.Targets[i].Checks
		for a, b := 0, len(checks)-1; a < b; a, b = a+1, b-1 {
			checks[a], checks[b] = checks[b], checks[a]
		}
	}
	two := mustPlan(t, p, ctx, r)
	if !bytes.Equal(one.Sources.Data, two.Sources.Data) || *one.Proposal != *two.Proposal || !same(one.Selected, two.Selected) {
		t.Fatal("permutation changed proposal")
	}
	for _, change := range []func(*RevisionRef){func(v *RevisionRef) { v.Source = "other" }, func(v *RevisionRef) { v.Object = "other" }, func(v *RevisionRef) { v.Revision = "other" }, func(v *RevisionRef) { v.Digest = strings.Repeat("0", 64) }} {
		a := r.prepare.Task
		b := a
		change(&b)
		if revisionToken(a) == revisionToken(b) {
			t.Fatal("full ref collapsed")
		}
	}
	// Distinct artifact options are legitimate for stopped seats, but cannot
	// masquerade as two different current configurations of one ready seat.
	for i := range o.data.Targets {
		o.data.Targets[i].Binding.State = selection.Ready
		o.data.Targets[i].Binding.Execution = map[string]string{"kind": "a2a", "session": "s", "thread": "t", "generation": "g"}
		bindPlannerTarget(t, &o.data.Targets[i], r, *now)
	}
	if _, err := p.Resolve(ctx, *r.prepare); reason(err, "") != "source_conflict" {
		t.Fatal("contradictory full live configurations accepted", err)
	}
}

// One live member cannot have contradictory native mappings for the same full
// artifact configuration. A single qualified mapping remains a valid control.
func TestSourcePlannerConflictingNativeMapping(t *testing.T) {
	for _, field := range []string{"model", "profile"} {
		t.Run(field, func(t *testing.T) {
			o, p, r, ctx, now := plannerFixture(t, selection.Ready)
			if mustPlan(t, p, ctx, r).Selected == nil {
				t.Fatal("valid mapping refused")
			}
			second := clone(o.data.Targets[0])
			if field == "model" {
				second.Binding.Model = "other-native-model"
			} else {
				second.Binding.Profile = "other-native-profile"
			}
			bindPlannerTarget(t, &second, r, *now)
			o.data.Targets = append(o.data.Targets, second)
			if _, err := p.Resolve(ctx, *r.prepare); reason(err, "") != "source_conflict" {
				t.Fatal("contradictory native mapping accepted", err)
			}
		})
	}
}

// Revalidation refuses changed source/configuration/messaging identity, expired
// receipts and malformed retained data. A newly eligible alternative cannot
// replace the original candidate. Header changes never get silently repinned.
func TestSourcePlannerRevalidationRefusals(t *testing.T) {
	for _, mode := range []string{"generation", "thread", "mapping", "model", "card", "task", "policy", "expiry", "authority", "alternative", "corrupt", "digest", "no-selection"} {
		t.Run(mode, func(t *testing.T) {
			o, p, r, ctx, now := plannerFixture(t, selection.Ready)
			plan := mustPlan(t, p, ctx, r)
			v := plannerView(t, r, plan)
			switch mode {
			case "generation":
				o.data.Targets[0].Binding.Execution["generation"] = "changed"
				bindPlannerTarget(t, &o.data.Targets[0], r, *now)
			case "thread":
				o.data.Targets[0].Binding.Execution["thread"] = "changed"
				bindPlannerTarget(t, &o.data.Targets[0], r, *now)
			case "mapping":
				o.data.Targets[0].Binding.Mapping.Revision = "changed"
				bindPlannerTarget(t, &o.data.Targets[0], r, *now)
			case "model":
				o.data.Targets[0].Binding.Model = "changed"
				bindPlannerTarget(t, &o.data.Targets[0], r, *now)
			case "card":
				o.data.Cards[0].Card.Revision = "changed"
			case "task":
				o.data.Task.Revision = "changed"
			case "policy":
				o.data.Policy.Revision = "changed"
			case "expiry":
				*now = now.Add(time.Minute)
			case "authority":
				o.data.Targets[0].Checks[0].Verdict = selection.Deny
			case "alternative":
				o.data.Targets[0].Binding.Workbench = "other"
				bindPlannerTarget(t, &o.data.Targets[0], r, *now)
			case "corrupt":
				v.record.Sources.Data = []byte("{}")
				v.record.Sources.Digest = hash(v.record.Sources.Data)
			case "digest":
				v.record.Sources.Digest = strings.Repeat("0", 64)
			case "no-selection":
				v.record.Selected = nil
			}
			if err := p.Revalidate(ctx, v); err == nil {
				t.Fatal("changed/unavailable retained target accepted", mode)
			}
		})
	}
}

// Actual DATA parsing preserves all facts/times/provenance and partial scope.
// Host unknown readiness/activity cannot qualify stopped or ready; only separate
// owner evidence can. Failed/conflicting DATA never supplies an action gate.
func TestSourcePlannerInventoryBoundary(t *testing.T) {
	for _, mode := range []string{"valid-owner", "unknown-owner", "feed-failure", "binding-conflict", "configuration-conflict"} {
		t.Run(mode, func(t *testing.T) {
			o, p, r, ctx, _ := plannerFixture(t, selection.Ready)
			data, err := os.ReadFile("../inventory/testdata/source-compatible.json")
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if json.Unmarshal(data, &m) != nil {
				t.Fatal("fixture")
			}
			b := &o.data.Targets[0].Binding
			b.OwnerMember = m["member"].(string)
			b.OwnerProject = m["project"].(string)
			b.Workbench = m["workbench"].(string)
			facts := m["facts"].(map[string]any)
			b.Model = facts["configured_model"].(map[string]any)["value"].(string)
			b.Configuration.Vendor = facts["configured_vendor"].(map[string]any)["value"].(string)
			// The request itself must name the same vendor, rather than changing it
			// through owner mapping. Other DATA fields remain genuine fixture facts.
			rm := base(t)
			cfg(rm)["vendor"] = b.Configuration.Vendor
			r = mustParse(t, frame(t, rm), local)
			b.Configuration = r.prepare.Constraints.Configurations[0]
			for name, value := range map[string]string{"profile": b.Profile, "profile_lock": b.Configuration.ProfileLock, "placement": b.Configuration.Placement} {
				f := facts[name].(map[string]any)
				if f["known"] == true {
					f["value"] = value
				}
			}
			if mode == "configuration-conflict" {
				facts["configured_model"].(map[string]any)["value"] = "different"
				facts["native_model"].(map[string]any)["value"] = "different"
			}
			payload := frame(t, m)
			hostCalls := 0
			expected := inventory.Binding{SourceVersions: []string{m["source_version"].(string)}, OwnerBindingRef: m["owner_binding_ref"].(string), Member: b.OwnerMember, Project: b.OwnerProject, Workbench: b.Workbench, Execution: m["execution"].(string), RunnerInstance: m["runner_instance"].(string)}
			b.Execution = plannerHostExecution(expected)
			if mode == "binding-conflict" {
				m["execution"] = "another"
				payload = frame(t, m)
			}
			o.data.Targets[0].Host = &HostInput{Binding: expected, Source: sourceFunc(func(ctx context.Context, w io.Writer) error {
				hostCalls++
				if mode == "feed-failure" {
					return errors.New("private diagnostic")
				}
				_, err := w.Write(payload)
				return err
			})}
			bindPlannerTarget(t, &o.data.Targets[0], r, p.Clock())
			if mode == "unknown-owner" {
				o.data.Targets[0].Witness = BoundWitness{}
			}
			plan := mustPlan(t, p, ctx, r)
			if hostCalls != 1 {
				t.Fatal("producer count", hostCalls)
			}
			if mode == "valid-owner" {
				if plan.Action != "reuse" {
					t.Fatal("qualified owner with useful DATA refused", plan.Reason)
				}
				doc, ok := restoreSourceDocument(plan.Sources.Data)
				if !ok || len(doc.Inventory) != 1 || doc.Proposal.Scope != selection.Partial || doc.Inventory[0].Result.Projection.Facts["readiness"].Known || doc.Inventory[0].Result.Projection.Facts["effective_effort"].Known {
					t.Fatal("unknown DATA promoted")
				}
			} else if plan.Selected != nil {
				t.Fatal("ineligible DATA/owner promoted", mode)
			}
			if bytes.Contains(plan.Sources.Data, []byte("private diagnostic")) {
				t.Fatal("source diagnostic retained")
			}
		})
	}
}

// The engine docks with the source planner on real request/record/selector paths.
// Ports are fixtures: this proves ordering and replay isolation, not an actual
// native source implementation, workbench, readiness or live launch.
func TestSourcePlannerEngineDocking(t *testing.T) {
	for _, action := range []string{"reuse", "start"} {
		t.Run(action, func(t *testing.T) {
			state := selection.Ready
			if action == "start" {
				state = selection.Stopped
			}
			o, p, req, _, _ := plannerFixture(t, state)
			f, e, r := newOperationFixture(t, action)
			if req.Digest() != r.Digest() {
				t.Fatal("fixture digest")
			}
			p.Clock = func() time.Time { return f.now }
			e.Planner = p
			response(t, invocation(t, e, r), "receipt", "ready", "ok", 0)
			before := bytes.Clone(f.raw)
			reads := o.calls
			pure := e
			pure.Planner, pure.Lifecycle, pure.Clock = nil, nil, nil
			response(t, invocation(t, pure, r), "receipt", "ready", "ok", 3)
			if !bytes.Equal(before, f.raw) || o.calls != reads || reads != 2 || o.narrowed != 1 {
				t.Fatal("replay reread sources")
			}
			response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
			if o.calls != reads || f.start > 1 {
				t.Fatal("Status reselected/restarted")
			}
		})
	}
}

// Malformed owner/schema data fails closed, with nearby qualified controls.
// Legitimate wider wire identities are unavailable to this narrower selector,
// rather than truncated or mislabeled as an invalid wire frame.
func TestSourcePlannerAdmission(t *testing.T) {
	for _, mode := range []string{"nil-owner", "nil-clock", "no-deadline", "cancel", "caller", "project", "invalid-utf8", "unknown-gate", "duplicate-gate", "unknown-verdict", "invalid-reference", "too-many", "duplicate-target", "missing-execution", "execution-on-stopped", "wide-native", "owner-error"} {
		t.Run(mode, func(t *testing.T) {
			o, p, r, ctx, _ := plannerFixture(t, selection.Ready)
			req, _ := r.Prepare()
			switch mode {
			case "nil-owner":
				p.Owner = nil
			case "nil-clock":
				p.Clock = nil
			case "no-deadline":
				ctx = context.Background()
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "caller":
				p.Local.Caller = "different"
			case "project":
				p.Local.Project = "different"
			case "invalid-utf8":
				req.Constraints.Objective = string([]byte{0xff})
			case "unknown-gate":
				o.data.Targets[0].Checks[0].Gate = "invented"
			case "duplicate-gate":
				o.data.Targets[0].Checks[1] = o.data.Targets[0].Checks[0]
			case "unknown-verdict":
				o.data.Targets[0].Checks[0].Verdict = "invented"
			case "invalid-reference":
				o.data.Targets[0].Witness.Reference.Digest = "bad"
			case "too-many":
				for len(o.data.Targets) <= 16 {
					o.data.Targets = append(o.data.Targets, o.data.Targets[0])
				}
			case "duplicate-target":
				o.data.Targets = append(o.data.Targets, o.data.Targets[0])
			case "missing-execution":
				o.data.Targets[0].Binding.Execution = nil
			case "execution-on-stopped":
				o.data.Targets[0].Binding.State = selection.Stopped
			case "wide-native":
				o.data.Targets[0].Binding.Model = strings.Repeat("x", 513)
			case "owner-error":
				o.err = errors.New("private owner error")
			}
			if _, err := p.Resolve(ctx, req); err == nil {
				t.Fatal("invalid source/API accepted", mode)
			}
		})
	}
	o, p, r, ctx, _ := plannerFixture(t, selection.Stopped)
	before := clone(o.data.Targets[0].Binding)
	plan := mustPlan(t, p, ctx, r)
	if plan.Selected == nil || !reflect.DeepEqual(before, o.data.Targets[0].Binding) {
		t.Fatal("valid native control mutated")
	}
	wide := base(t)
	wide["caller"] = "\x00"
	x := mustParse(t, frame(t, wide), LocalContext{"\x00", local.Project})
	p.Local.Caller = "\x00"
	if _, err := p.Resolve(ctx, *x.prepare); reason(err, "") != "unsupported_configuration" {
		t.Fatal("wide valid wire identity truncated/mislabeled", err)
	}
}
