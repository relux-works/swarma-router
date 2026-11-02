package workbench

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"relux.works/swarma-router/launch"
	"relux.works/swarma-router/selection"
)

// These fixtures exercise the actual lower Router/journal protocol and upper
// engine/selector. They never launch a process or acquire production receipts.
type lowerFixture struct {
	t                                                                             *testing.T
	now                                                                           *time.Time
	target                                                                        TargetBinding
	specCalls, admitCalls, witnessCalls, prepares, starts, observes, opens, saves int
	record                                                                        *launch.Record
	failSave                                                                      int
	specChange                                                                    func(*LaunchSpecification)
	permitChange                                                                  func(*LowerPermit)
	witnessChange                                                                 func(*LifecycleWitness)
	witnessErr                                                                    error
	forceReady                                                                    bool
	lowerStatus                                                                   string
}

func (f *lowerFixture) ID() string            { return "curator" }
func (f *lowerFixture) Configuration() string { return strings.Repeat("2", 64) }
func (f *lowerFixture) attempt(i launch.Intent) launch.Attempt {
	return launch.Attempt{Command: []string{"/fixture/curator", "run", i.Runtime}, Profile: i.Profile, ProfileLock: i.ProfileLock, OperationDigest: i.Digest(), Session: i.TmuxSession, Pane: "%17", PanePID: 10, CuratorRun: launch.Process{PID: 20, ParentPID: 10, Birth: "launcher-birth", Executable: "/fixture/curator-run"}, Runtime: launch.Process{PID: 30, ParentPID: 20, Birth: "runtime-birth", Executable: "/fixture/native"}}
}
func (f *lowerFixture) Prepare(i launch.Intent) (launch.Attempt, error) {
	f.prepares++
	return f.attempt(i), nil
}
func (f *lowerFixture) Start(_ context.Context, i launch.Intent) (launch.Attempt, string, error) {
	f.starts++
	return f.attempt(i), "attempted", nil
}
func (f *lowerFixture) Observe(_ context.Context, i launch.Intent, _ *launch.Attempt) (launch.Attempt, string, error) {
	f.observes++
	return f.attempt(i), f.lowerStatus, nil
}
func (f *lowerFixture) Load(_ string) (launch.Record, bool, error) {
	if f.record == nil {
		return launch.Record{}, false, nil
	}
	return clone(*f.record), true, nil
}
func (f *lowerFixture) Save(_ string, r launch.Record) error {
	f.saves++
	if f.saves == f.failSave {
		return errors.New("isolated lost lower acknowledgement")
	}
	x := clone(r)
	f.record = &x
	return nil
}
func (f *lowerFixture) Sync(string) error { return nil }
func (f *lowerFixture) Close() error      { return nil }
func (f *lowerFixture) Specification(_ context.Context, v View) (LaunchSpecification, error) {
	f.specCalls++
	b := f.target
	spec := LaunchSpecification{Key: v.Key(), RequestDigest: v.Digest(), TargetDigest: TargetBindingDigest(b), Mapping: v.Request().Task, ObservedAt: f.now.Add(-time.Second), ExpiresAt: f.now.Add(time.Minute), Intent: launch.Intent{Contract: launch.Contract, Manager: "caller", Project: b.OwnerProject, TaskID: "TASK-261103-ip4l70", Member: b.OwnerMember, Plugin: f.ID(), PluginConfig: f.Configuration(), Runtime: b.Configuration.Runtime, Profile: b.Profile, ProfileLock: b.Configuration.ProfileLock, Model: b.Model, Effort: b.Configuration.Effort, Workspace: "/isolated/fixture", TmuxSession: "fixture-session", Mode: "start"}}
	if f.specChange != nil {
		f.specChange(&spec)
	}
	return spec, nil
}
func (f *lowerFixture) Admit(_ context.Context, v View, i launch.Intent) (LowerPermit, error) {
	f.admitCalls++
	p := LowerPermit{Key: v.Key(), RequestDigest: v.Digest(), TargetDigest: TargetBindingDigest(f.target), IntentDigest: i.Digest(), Selected: *v.Selected(), Mapping: v.Lower().Mapping, Reference: v.Request().Task, State: "stopped", CheckedAt: f.now.Add(-time.Second), ExpiresAt: f.now.Add(time.Minute)}
	if f.permitChange != nil {
		f.permitChange(&p)
	}
	return p, nil
}
func (f *lowerFixture) Witness(_ context.Context, v View, result *launch.Result) (LifecycleWitness, error) {
	f.witnessCalls++
	if f.witnessErr != nil {
		return LifecycleWitness{}, f.witnessErr
	}
	expiry := f.now.Add(time.Minute).Format(wireTime)
	ref := v.Request().Task
	o := Observation{Key: v.Key(), RequestDigest: v.Digest(), Selected: *v.Selected(), Lower: lowerBinding(v.record), Status: "unknown", Reason: "observation_unresolved", Evidence: []RevisionRef{}}
	if result == nil || result.Status == "running" || f.forceReady {
		execution := f.target.Execution
		if execution == nil {
			execution = map[string]string{"kind": "a2a", "session": "original-session", "thread": "original-thread", "generation": "original-generation"}
		}
		o.Status = "ready"
		o.Reason = "ok"
		o.CheckedAt = f.now.Format(wireTime)
		o.ValidUntil = expiry
		o.Evidence = []RevisionRef{ref}
		o.Ready = &ReadyBinding{Selected: *v.Selected(), ExecutionOwner: "owner", ManagerCustody: ref, TaskState: ref, RuntimeEvidence: ref, MessagingRoute: ref, LaneReply: ref, Execution: clone(execution), ValidUntil: expiry}
	} else if result.Status == "attempted" || result.Status == "pending" {
		o.Status = "pending"
		o.Reason = "readiness_unknown"
		o.CheckedAt = f.now.Format(wireTime)
		o.ValidUntil = expiry
		o.Evidence = []RevisionRef{ref}
	}
	w := LifecycleWitness{ResultDigest: LowerResultDigest(result), Observation: o}
	if f.witnessChange != nil {
		f.witnessChange(&w)
	}
	return w, nil
}
func lowerTestFixture(t *testing.T, state selection.State) (*lowerFixture, LowerLifecycle, View, SourcePlanner, Request, context.Context, *ownerFixture) {
	t.Helper()
	owner, planner, r, ctx, now := plannerFixture(t, state)
	// The real lower Router attenuates context with actual source expiry, so
	// its fixture clock must share the wall-clock domain used by context.
	*now = time.Now().UTC().Truncate(time.Millisecond)
	bindPlannerTarget(t, &owner.data.Targets[0], r, *now)
	plan := mustPlan(t, planner, ctx, r)
	v := plannerView(t, r, plan)
	f := &lowerFixture{t: t, now: now, target: owner.data.Targets[0].Binding, lowerStatus: "running"}
	router := launch.Router{Manager: "caller", Project: f.target.OwnerProject, Plugin: f, OpenStore: func() (launch.Store, error) { f.opens++; return f, nil }, Now: func() time.Time { return *now }}
	l := LowerLifecycle{Local: local, Router: router, Owner: f, Clock: func() time.Time { return *now }}
	return f, l, v, planner, r, ctx, owner
}
func mappedLower(t *testing.T, l LowerLifecycle, ctx context.Context, v View) View {
	t.Helper()
	m, err := l.Prepare(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	v.record.Lower = &m
	v.record.PossibleEffect = true
	return v
}

// Real lower/upper docking: a start returns actual pending, Status observes the
// same operation and can become ready; replay never calls sources or lower ports.
// Reuse directly checks its original execution and never opens a lower store.
func TestLowerLifecycleEngineDocking(t *testing.T) {
	for _, state := range []selection.State{selection.Ready, selection.Stopped} {
		t.Run(string(state), func(t *testing.T) {
			f, l, _, planner, r, _, owner := lowerTestFixture(t, state)
			store, e, _ := newOperationFixture(t, "start")
			store.now = *f.now
			e.Clock = l.Clock
			e.Planner = planner
			e.Lifecycle = l
			if state == selection.Stopped {
				response(t, invocation(t, e, r), "receipt", "pending", "readiness_unknown", 3)
			} else {
				response(t, invocation(t, e, r), "receipt", "ready", "ok", 0)
			}
			raw := bytes.Clone(store.raw)
			reads, witnesses := owner.calls, f.witnessCalls
			pure := e
			pure.Planner = nil
			pure.Lifecycle = nil
			pure.Clock = nil
			invocation(t, pure, r)
			if !bytes.Equal(raw, store.raw) || owner.calls != reads || f.witnessCalls != witnesses {
				t.Fatal("historical replay reacquired or wrote")
			}
			response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
			response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
			if owner.calls != reads {
				t.Fatal("Status reselected")
			}
			if state == selection.Stopped {
				if f.starts != 1 || f.specCalls != 1 || f.admitCalls != 1 || f.observes != 2 {
					t.Fatal("lower action counts", f.starts, f.specCalls, f.admitCalls, f.observes)
				}
			} else if f.starts+f.specCalls+f.admitCalls+f.opens+f.prepares+f.observes != 0 {
				t.Fatal("reuse touched lower")
			}
		})
	}
}

// Owner mappings remain exact, current and independently admitted. Lower grammar
// refuses unsupported values without a store open or plugin effect. Qualified
// identical templates produce the same full key, without truncating wide refs.
func TestLowerLifecycleSpecificationBoundaries(t *testing.T) {
	cases := []string{"key", "digest", "target", "mapping", "future", "expired", "zero", "correlation", "model", "profile", "lock", "runtime", "project", "member", "task", "plugin-config", "workspace", "invalid-utf8"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Stopped)
			f.specChange = func(s *LaunchSpecification) {
				switch mode {
				case "key":
					s.Key.Caller = "other"
				case "digest":
					s.RequestDigest = strings.Repeat("0", 64)
				case "target":
					s.TargetDigest = strings.Repeat("0", 64)
				case "mapping":
					s.Mapping.Digest = "bad"
				case "future":
					s.ObservedAt = f.now.Add(time.Second)
				case "expired":
					s.ExpiresAt = *f.now
				case "zero":
					s.ObservedAt = time.Time{}
				case "correlation":
					s.Intent.CorrelationID = "foreign"
				case "model":
					s.Intent.Model = "other"
				case "profile":
					s.Intent.Profile = "other"
				case "lock":
					s.Intent.ProfileLock = strings.Repeat("0", 64)
				case "runtime":
					s.Intent.Runtime = "claude_code"
				case "project":
					s.Intent.Project = "other"
				case "member":
					s.Intent.Member = "other"
				case "task":
					s.Intent.TaskID = "not-a-native-task"
				case "plugin-config":
					s.Intent.PluginConfig = strings.Repeat("0", 64)
				case "workspace":
					s.Intent.Workspace = "relative"
				case "invalid-utf8":
					s.Intent.Workspace = "/bad/\xff"
				}
			}
			if _, err := l.Prepare(ctx, v); err == nil {
				t.Fatal("invalid specification admitted", mode)
			}
			if f.starts != 0 || f.opens != 0 {
				t.Fatal("bad pure mapping performed effect/store access")
			}
		})
	}
	f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Stopped)
	one, err := l.Prepare(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	two, err := l.Prepare(ctx, v)
	if err != nil || !same(one, two) || !strings.HasPrefix(one.Intent.CorrelationID, "wb-") || len(one.Intent.CorrelationID) != 67 || one.Binding.IntentDigest != one.Intent.Digest() {
		t.Fatal("unstable lower identity", err)
	}
	f.specChange = func(s *LaunchSpecification) { s.Intent.TmuxSession = "other-session" }
	three, err := l.Prepare(ctx, v)
	if err != nil || one.Intent.CorrelationID == three.Intent.CorrelationID {
		t.Fatal("template change collapsed", err)
	}
}

// Every full-bound permit field is required before the one lower effect. Exact
// valid permit control launches once; invalid permits leave the lower store
// absent and never call the plugin, even though the upper marker already exists.
func TestLowerLifecycleGuardBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "key", "request", "target", "intent", "selected", "mapping", "reference", "state", "future", "expired", "zero", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Stopped)
			v = mappedLower(t, l, ctx, v)
			f.permitChange = func(p *LowerPermit) {
				switch mode {
				case "key":
					p.Key.Project = "other"
				case "request":
					p.RequestDigest = strings.Repeat("0", 64)
				case "target":
					p.TargetDigest = strings.Repeat("0", 64)
				case "intent":
					p.IntentDigest = strings.Repeat("0", 64)
				case "selected":
					p.Selected.Configuration.Model.Source = "other"
				case "mapping":
					p.Mapping.Revision = "other"
				case "reference":
					p.Reference.Digest = "bad"
				case "state":
					p.State = "running"
				case "future":
					p.CheckedAt = f.now.Add(time.Second)
				case "expired":
					p.ExpiresAt = *f.now
				case "zero":
					p.CheckedAt = time.Time{}
				}
			}
			if mode == "canceled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			o, err := l.Start(ctx, v)
			if mode == "valid" {
				if err != nil || o.Status != "pending" || f.starts != 1 {
					t.Fatal("valid permit failed", err, o.Status, f.starts)
				}
			} else if f.starts != 0 || f.record != nil || o.Ready != nil {
				t.Fatal("invalid permit caused lower effect/write", mode, err)
			}
		})
	}
}

// Ready requires a complete fresh owner witness, exact result binding and actual
// lower running. Reuse additionally retains its original source generation.
// Wrong-result is the assertion used by the isolated guard-removal control.
func TestLowerLifecycleWitnessBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-result", "key", "digest", "selected", "lower", "generation", "future", "expired", "ready-expired", "missing-ref", "no-witness", "lower-pending", "historical"} {
		t.Run(mode, func(t *testing.T) {
			f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Stopped)
			v = mappedLower(t, l, ctx, v)
			if _, err := l.Start(ctx, v); err != nil {
				t.Fatal(err)
			}
			if mode == "no-witness" {
				f.witnessErr = errors.New("private diagnostic")
			}
			if mode == "lower-pending" {
				f.lowerStatus = "pending"
				f.forceReady = true
			}
			f.witnessChange = func(w *LifecycleWitness) {
				o := &w.Observation
				switch mode {
				case "wrong-result":
					w.ResultDigest = strings.Repeat("0", 64)
				case "key":
					o.Key.Caller = "other"
				case "digest":
					o.RequestDigest = strings.Repeat("0", 64)
				case "selected":
					o.Selected.Card.Revision = "other"
				case "lower":
					o.Lower.IntentDigest = strings.Repeat("0", 64)
				case "generation":
					o.Ready.Execution["generation"] = "changed"
				case "future":
					o.CheckedAt = f.now.Add(time.Second).Format(wireTime)
				case "expired":
					o.ValidUntil = f.now.Format(wireTime)
					o.Ready.ValidUntil = o.ValidUntil
				case "ready-expired":
					o.Ready.ValidUntil = f.now.Format(wireTime)
				case "missing-ref":
					o.Ready.LaneReply.Digest = "bad"
				}
			}
			if mode == "generation" {
				v.record.KnownExecution = map[string]string{"kind": "a2a", "session": "original-session", "thread": "original-thread", "generation": "original-generation"}
			}
			if mode == "historical" {
				f.record.Result.Status = "refused"
				f.record.Result.Reason = "plugin_refused"
				f.forceReady = true
			}
			o, err := l.Observe(ctx, v)
			if mode == "valid" {
				if err != nil || o.Ready == nil {
					t.Fatal("valid witness refused", err)
				}
			} else if err == nil && o.Ready != nil {
				t.Fatal("invalid witness admitted", mode)
			}
			if f.starts != 1 || f.specCalls != 1 || f.admitCalls != 1 {
				t.Fatal("observation reprepared/restarted")
			}
		})
	}
	for _, field := range []string{"session", "thread", "generation"} {
		t.Run("reuse-"+field, func(t *testing.T) {
			f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Ready)
			f.witnessChange = func(w *LifecycleWitness) { w.Observation.Ready.Execution[field] = "changed" }
			if _, err := l.Observe(ctx, v); reason(err, "") != "source_conflict" {
				t.Fatal("reuse original execution changed", field, err)
			}
			if f.opens != 0 || f.specCalls != 0 {
				t.Fatal("reuse touched lower")
			}
		})
	}
}

// Lost lower or upper persistence acknowledgement preserves the same original
// operation. Status can reconcile it, but never repeats the one plugin effect.
func TestLowerLifecycleUncertaintyRecovery(t *testing.T) {
	for _, mode := range []string{"lower-write", "upper-write", "missing-witness"} {
		t.Run(mode, func(t *testing.T) {
			f, l, _, planner, r, _, _ := lowerTestFixture(t, selection.Stopped)
			store, e, _ := newOperationFixture(t, "start")
			store.now = *f.now
			e.Clock = l.Clock
			e.Planner = planner
			e.Lifecycle = l
			if mode == "lower-write" {
				f.failSave = 2
			}
			if mode == "missing-witness" {
				f.witnessErr = errors.New("unavailable")
			}
			if mode == "upper-write" {
				store.onCommit = func(rec operationRecord) {
					if rec.PossibleEffect && f.starts == 0 {
						store.failAt = store.commits + 1
						store.failMode = "before"
					}
				}
			}
			out := invocation(t, e, r)
			if out.ExitCode() != 3 || f.starts != 1 {
				t.Fatal("uncertainty did not retain one attempt", out.ExitCode(), f.starts)
			}
			f.failSave = 0
			f.witnessErr = nil
			store.failAt = 0
			store.onCommit = nil
			response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
			invocation(t, e, r)
			if f.starts != 1 || f.specCalls != 1 || f.admitCalls != 1 {
				t.Fatal("recovery repeated effect or mapping", f.starts, f.specCalls, f.admitCalls)
			}
		})
	}
}

// Invalid direct API state/context or retained mapping never reaches an effect.
// Missing owner witness stays unknown with no fabricated source timestamps.
func TestLowerLifecycleAdmission(t *testing.T) {
	for _, mode := range []string{"nil-clock", "nil-context", "no-deadline", "canceled", "caller", "source", "selected", "unmarked", "lower-key", "lower-model", "transplanted-lower-key"} {
		t.Run(mode, func(t *testing.T) {
			f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Stopped)
			v = mappedLower(t, l, ctx, v)
			switch mode {
			case "nil-clock":
				l.Clock = nil
			case "nil-context":
				ctx = nil
			case "no-deadline":
				ctx = context.Background()
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "caller":
				l.Local.Caller = "other"
			case "source":
				v.record.Sources.Data[0] = 'x'
			case "selected":
				v.record.Selected.Card.Revision = "other"
			case "unmarked":
				v.record.PossibleEffect = false
			case "lower-key":
				v.record.Lower.Binding.OperationRef = "other"
			case "transplanted-lower-key":
				v.record.Lower.Intent.CorrelationID = "foreign-key"
				v.record.Lower.Binding.IntentDigest = v.record.Lower.Intent.Digest()
				v.record.Lower.Binding.OperationRef = lowerOperationReference(v.record.Lower.Intent)
			case "lower-model":
				v.record.Lower.Intent.Model = "other"
				v.record.Lower.Binding.IntentDigest = v.record.Lower.Intent.Digest()
			}
			if _, err := l.Start(ctx, v); err == nil {
				t.Fatal("invalid direct call admitted", mode)
			}
			if f.starts != 0 || f.opens != 0 {
				t.Fatal("invalid direct call reached lower")
			}
		})
	}
	_, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Ready)
	l.Owner = nil
	o, err := l.Observe(ctx, v)
	if err != nil || o.Ready != nil || o.Status != "unknown" || o.CheckedAt != "" || o.ValidUntil != "" {
		t.Fatal("missing source fabricated validity", err, o)
	}
}

// A separately configured lower guard keeps its authority. Its refusal or
// mismatched/stale admission cannot be replaced by a valid upper permit.
type lowerGuardFunc func(context.Context, launch.Intent, string, time.Time) (launch.Admission, error)

func (f lowerGuardFunc) Check(c context.Context, i launch.Intent, d string, n time.Time) (launch.Admission, error) {
	return f(c, i, d, n)
}
func TestLowerLifecyclePreservesLowerGuard(t *testing.T) {
	for _, mode := range []string{"valid", "deny", "member", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f, l, v, _, _, ctx, _ := lowerTestFixture(t, selection.Stopped)
			v = mappedLower(t, l, ctx, v)
			calls := 0
			l.Router.Guard = lowerGuardFunc(func(_ context.Context, i launch.Intent, d string, n time.Time) (launch.Admission, error) {
				calls++
				a := launch.Admission{Digest: d, Manager: i.Manager, Member: i.Member, ProfileLock: i.ProfileLock, State: "stopped", CheckedAt: n.Add(-time.Millisecond), ExpiresAt: n.Add(time.Second)}
				switch mode {
				case "deny":
					return a, errors.New("owner hold")
				case "member":
					a.Member = "other"
				case "expired":
					a.ExpiresAt = n
				}
				return a, nil
			})
			_, _ = l.Start(ctx, v)
			if calls != 1 {
				t.Fatal("configured lower guard bypassed")
			}
			if mode == "valid" {
				if f.starts != 1 {
					t.Fatal("valid composed guards refused")
				}
			} else if f.starts != 0 || f.record != nil {
				t.Fatal("lower refusal bypassed", mode)
			}
		})
	}
}

// A legitimate owner-native value at the lower boundary is preserved. One byte
// beyond that existing subset is unsupported, never truncated or invalid wire.
func TestLowerLifecycleNativeSubset(t *testing.T) {
	for _, size := range []int{128, 129} {
		t.Run(strings.Repeat("x", size-127), func(t *testing.T) {
			f, l, _, planner, r, ctx, owner := lowerTestFixture(t, selection.Stopped)
			owner.data.Targets[0].Binding.Model = strings.Repeat("m", size)
			bindPlannerTarget(t, &owner.data.Targets[0], r, *f.now)
			f.target = owner.data.Targets[0].Binding
			v := plannerView(t, r, mustPlan(t, planner, ctx, r))
			m, err := l.Prepare(ctx, v)
			if size == 128 {
				if err != nil || m.Intent.Model != f.target.Model {
					t.Fatal("legitimate native value changed/refused", err)
				}
			} else if reason(err, "") != "unsupported_configuration" {
				t.Fatal("unrepresentable native value not explicitly refused", err)
			}
			if f.opens+f.starts != 0 {
				t.Fatal("native subset check caused effect")
			}
		})
	}
}

// Host native identity and its messaging route stay distinct; reuse binds every
// original union field and does not synthesize an a2a-only identity.
func TestLowerLifecycleHostReuse(t *testing.T) {
	f, l, _, planner, r, ctx, owner := lowerTestFixture(t, selection.Ready)
	execution := map[string]string{"kind": "host", "execution": "native-execution", "runner_instance": "runner", "native_thread": "native-thread", "controller_epoch": "18446744073709551615", "messaging_session": "message-session", "messaging_thread": "message-thread", "messaging_generation": "message-generation"}
	owner.data.Targets[0].Binding.Execution = execution
	bindPlannerTarget(t, &owner.data.Targets[0], r, *f.now)
	f.target = owner.data.Targets[0].Binding
	v := plannerView(t, r, mustPlan(t, planner, ctx, r))
	o, err := l.Observe(ctx, v)
	if err != nil || !same(o.Ready.Execution, execution) || f.opens != 0 {
		t.Fatal("Host identity collapsed", err)
	}
	f.witnessChange = func(w *LifecycleWitness) { w.Observation.Ready.Execution["messaging_generation"] = "other" }
	if _, err = l.Observe(ctx, v); reason(err, "") != "source_conflict" {
		t.Fatal("Host messaging generation changed", err)
	}
}
