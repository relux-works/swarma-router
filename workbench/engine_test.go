package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"relux.works/swarma-router/launch"
)

// This serialized memory fixture models the documented custody/CAS protocol,
// not production storage, crash durability, a real source or a live launch.
type operationFixture struct {
	t                                                                                       *testing.T
	mu                                                                                      sync.Mutex
	raw                                                                                     []byte
	key                                                                                     OperationKey
	commits, resolve, prepare, start, observe, revalidate                                   int
	failAt                                                                                  int
	failMode                                                                                string
	readErr, openErr, closeErr, resolveErr, prepareErr, startErr, observeErr, revalidateErr error
	prepareAccessErr, observeAccessErr                                                      error
	plan                                                                                    Plan
	observation                                                                             func(View) Observation
	onResolve                                                                               func()
	onStart                                                                                 func(View)
	states                                                                                  []string
	now                                                                                     time.Time
}
type fixtureJournal struct{ f *operationFixture }

func (f *operationFixture) Open(_ context.Context, key OperationKey) (Journal, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.mu.Lock()
	f.key = key
	return fixtureJournal{f}, nil
}
func (j fixtureJournal) Close() error { j.f.mu.Unlock(); return j.f.closeErr }
func (j fixtureJournal) Read(context.Context) ([]byte, error) {
	return bytes.Clone(j.f.raw), j.f.readErr
}
func (j fixtureJournal) Commit(_ context.Context, expected string, build func(string) ([]byte, error)) error {
	f := j.f
	f.commits++
	if f.failAt == f.commits && (f.failMode == "before" || f.failMode == "cas") {
		return errors.New("injected persistence refusal")
	}
	if f.raw == nil {
		if expected != "" {
			return errors.New("CAS absent")
		}
	} else {
		_, b, ok := decodeOperation(f.raw, f.key)
		if !ok || b.DurableRevision() != expected {
			return errors.New("CAS mismatch")
		}
	}
	b, err := build(fmt.Sprintf("revision-%d", f.commits))
	if err != nil {
		return err
	}
	if f.failAt == f.commits && f.failMode == "twice" {
		_, err = build("illegal-second-revision")
		return err
	}
	f.raw = bytes.Clone(b)
	r, _, ok := decodeOperation(b, f.key)
	if !ok {
		f.t.Fatal("fixture received invalid committed record")
	}
	f.states = append(f.states, r.Phase)
	if f.failAt == f.commits && f.failMode == "after" {
		return errors.New("injected lost commit acknowledgement")
	}
	if f.failAt == f.commits && f.failMode == "crash" {
		panic("injected caller interruption")
	}
	return nil
}

// Separate named adapters keep the authorization and lifecycle Prepare methods
// distinct, rather than weakening either interface to make the fixture fit.
type fixtureAccess struct{ f *operationFixture }

func (a fixtureAccess) Read(context.Context, OperationKey) error    { return nil }
func (a fixtureAccess) Prepare(context.Context, OperationKey) error { return a.f.prepareAccessErr }
func (a fixtureAccess) Observe(context.Context, OperationKey) error { return a.f.observeAccessErr }
func (f *operationFixture) Resolve(_ context.Context, _ PrepareRequest) (Plan, error) {
	f.resolve++
	r, _, ok := decodeOperation(f.raw, f.key)
	if !ok || r.Phase != "resolving" {
		f.t.Fatal("source acquired before durable reservation")
	}
	if f.onResolve != nil {
		f.onResolve()
	}
	return clone(f.plan), f.resolveErr
}
func (f *operationFixture) Revalidate(context.Context, View) error {
	f.revalidate++
	return f.revalidateErr
}

type fixtureLifecycle struct{ f *operationFixture }

func (l fixtureLifecycle) Prepare(_ context.Context, v View) (LowerMapping, error) {
	f := l.f
	f.prepare++
	r, _, ok := decodeOperation(f.raw, f.key)
	if !ok || r.Phase != "preparing" || r.Selected == nil || r.Sources == nil || r.Proposal == nil || r.PossibleEffect {
		f.t.Fatal("pure preparation lacks retained selection")
	}
	i := launch.Intent{Contract: launch.Contract, CorrelationID: "lower-correlation", Project: "project", TaskID: "TASK-261102-lvr1go", Member: "member", Manager: "caller", Plugin: "curator", PluginConfig: strings.Repeat("2", 64), Runtime: "codex_cli", Profile: "profile", ProfileLock: v.Selected().Configuration.ProfileLock, Model: "model", Effort: "high", Workspace: "/example", TmuxSession: "example", Mode: "start"}
	return LowerMapping{Binding: LowerBinding{"curator", "original-lower-operation", i.Digest()}, Intent: i, Mapping: v.Request().Task}, f.prepareErr
}
func (l fixtureLifecycle) Start(_ context.Context, v View) (Observation, error) {
	f := l.f
	f.start++
	r, _, ok := decodeOperation(f.raw, f.key)
	if !ok || !r.PossibleEffect || r.Lower == nil || r.Phase != "preparing" || !same(r.Lower, v.Lower()) {
		f.t.Fatal("Start lacks durable exact lower binding and possible-effect marker")
	}
	if f.onStart != nil {
		f.onStart(v)
	}
	return f.observation(v), f.startErr
}
func (l fixtureLifecycle) Observe(_ context.Context, v View) (Observation, error) {
	l.f.observe++
	return l.f.observation(v), l.f.observeErr
}

func newOperationFixture(t *testing.T, action string) (*operationFixture, Engine, Request) {
	t.Helper()
	r := mustParse(t, []byte(goldenPrepare+"\n"), local)
	p, _ := r.Prepare()
	snapshot := []byte(`{"complete":"sanitized scoped test snapshot"}`)
	f := &operationFixture{t: t, now: time.Date(2026, 11, 2, 20, 0, 0, 0, time.UTC)}
	s := SelectedBinding{Member: p.Cards[0].Member, Card: p.Cards[0].Card, Configuration: p.Constraints.Configurations[0], Proposal: p.Task}
	f.plan = Plan{Sources: &Snapshot{snapshot, hash(snapshot), []RevisionRef{p.Task}}, Proposal: &p.Task, Selected: &s, Action: action, Status: "selected", Reason: "ok"}
	f.observation = func(v View) Observation {
		ref := v.Request().Task
		expiry := f.now.Add(time.Minute).Format(wireTime)
		ready := &ReadyBinding{Selected: *v.Selected(), ExecutionOwner: "existing-owner", ManagerCustody: ref, TaskState: ref, RuntimeEvidence: ref, MessagingRoute: ref, LaneReply: ref, Execution: map[string]string{"kind": "a2a", "session": "original-session", "thread": "original-thread", "generation": "original-generation"}, ValidUntil: expiry}
		return Observation{Key: v.Key(), RequestDigest: v.Digest(), Selected: *v.Selected(), Lower: lowerBinding(v.record), Status: "ready", Reason: "ok", CheckedAt: f.now.Format(wireTime), ValidUntil: expiry, Ready: ready, Evidence: []RevisionRef{ref}}
	}
	e := Engine{Repository: f, Access: fixtureAccess{f}, Planner: f, Lifecycle: fixtureLifecycle{f}, Clock: func() time.Time { return f.now }}
	return f, e, r
}
func invocation(t *testing.T, e Engine, r Request) Output {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, err := e.Run(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func response(t *testing.T, o Output, kind, status, code string, exit int) map[string]any {
	t.Helper()
	var m map[string]any
	if json.Unmarshal(o.Bytes(), &m) != nil || len(o.Bytes()) == 0 || o.Bytes()[len(o.Bytes())-1] != '\n' || o.ExitCode() != exit {
		t.Fatalf("bad output exit %d: %s", o.ExitCode(), o.Bytes())
	}
	r := m["result"].(map[string]any)
	if r["kind"] != kind || status != "" && r["status"] != status || code != "" && r["reason"] != code {
		t.Fatalf("unexpected result %s", o.Bytes())
	}
	if kind == "recovery" && len(o.Bytes()) > MaxRecoveryBytes {
		t.Fatal("unbounded recovery")
	}
	return r
}
func statusRequest(t *testing.T, r Request) Request {
	t.Helper()
	return mustParse(t, frame(t, map[string]any{"contract": Contract, "operation": "status", "correlation_id": r.Key().CorrelationID, "caller": r.Key().Caller, "project": r.Key().Project, "expected_request_digest": r.Digest()}), local)
}
func retained(t *testing.T, f *operationFixture) (operationRecord, RetainedRequest) {
	t.Helper()
	r, b, ok := decodeOperation(f.raw, f.key)
	if !ok {
		t.Fatalf("invalid retained operation: %s", f.raw)
	}
	return r, b
}

// Proves both start/reuse positive controls, durable sequencing, inert historical
// replay, and same-operation Status with no selection, Prepare or second Start.
func TestOperationReadyReplayAndStatus(t *testing.T) {
	for _, action := range []string{"start", "reuse"} {
		t.Run(action, func(t *testing.T) {
			f, e, r := newOperationFixture(t, action)
			out := invocation(t, e, r)
			got := response(t, out, "receipt", "ready", "ok", 0)
			if got["historical"] != false || got["ready"] == nil || f.resolve != 1 || f.revalidate != 1 {
				t.Fatal("missing fresh qualified result")
			}
			if action == "start" && (f.prepare != 1 || f.start != 1 || f.observe != 0) {
				t.Fatal("start counts")
			}
			if action == "reuse" && (f.prepare != 0 || f.start != 0 || f.observe != 1) {
				t.Fatal("reuse caused preparation/start")
			}
			original := bytes.Clone(f.raw)
			counts := []int{f.resolve, f.prepare, f.start, f.observe, f.revalidate, f.commits}
			configured := response(t, invocation(t, e, r), "receipt", "ready", "ok", 3)
			if configured["historical"] != true || configured["ready"] != nil || !bytes.Equal(original, f.raw) || !reflect.DeepEqual(counts, []int{f.resolve, f.prepare, f.start, f.observe, f.revalidate, f.commits}) {
				t.Fatal("configured replay called a source or mutated the operation")
			}
			// Replay works without any source, lifecycle or clock port at all.
			pure := e
			pure.Planner = nil
			pure.Lifecycle = nil
			pure.Clock = nil
			h := response(t, invocation(t, pure, r), "receipt", "ready", "ok", 3)
			if h["historical"] != true || h["ready"] != nil || !bytes.Equal(original, f.raw) || !reflect.DeepEqual(counts, []int{f.resolve, f.prepare, f.start, f.observe, f.revalidate, f.commits}) {
				t.Fatal("replay refreshed or mutated")
			}
			fresh := response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
			if fresh["durable_revision"] == got["durable_revision"] || fresh["historical"] != false || f.resolve != 1 || f.prepare != counts[1] || f.start != counts[2] || f.observe != counts[3]+1 || f.revalidate != 1 {
				t.Fatal("status changed lifecycle or did not durably observe")
			}
			state, _ := retained(t, f)
			if state.KnownExecution["generation"] != "original-generation" {
				t.Fatal("generation not retained before ready")
			}
		})
	}
}

// Proves digest conflicts preserve all original bytes, a missing Status never
// reserves, and unreadable/corrupt stores never become an absent new operation.
func TestOperationLookupFailures(t *testing.T) {
	for _, mode := range []string{"conflict", "missing", "read-error", "open-error", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f, e, r := newOperationFixture(t, "start")
			input := r
			switch mode {
			case "conflict":
				invocation(t, e, r)
				m := base(t)
				m["constraints"].(map[string]any)["objective"] = "changed"
				input = mustParse(t, frame(t, m), local)
			case "missing":
				input = statusRequest(t, r)
			case "read-error":
				f.readErr = errors.New("private diagnostic must not escape")
			case "open-error":
				f.openErr = errors.New("private diagnostic must not escape")
			case "corrupt":
				f.raw = []byte("{\"invalid\":true}\n")
			}
			original := bytes.Clone(f.raw)
			counts := []int{f.resolve, f.prepare, f.start, f.observe, f.commits}
			out := invocation(t, e, input)
			switch mode {
			case "conflict":
				response(t, out, "receipt", "conflict", "idempotency_conflict", 2)
			case "missing":
				x := response(t, out, "error", "", "", 2)
				if x["code"] != "operation_not_found" {
					t.Fatal(x)
				}
			case "corrupt":
				response(t, out, "recovery", "unknown", "store_invalid", 3)
			default:
				response(t, out, "recovery", "unknown", "store_unavailable", 3)
			}
			if !bytes.Equal(original, f.raw) || !reflect.DeepEqual(counts, []int{f.resolve, f.prepare, f.start, f.observe, f.commits}) || bytes.Contains(out.Bytes(), []byte("private diagnostic")) {
				t.Fatal("lookup failure changed original or leaked diagnostic")
			}
		})
	}
}

// Proves terminal no-effect decisions never reopen, including source failures
// and current guard denial after pure mapping but before the one Start marker.
func TestOperationTerminalNoEffect(t *testing.T) {
	for _, mode := range []string{"deferred", "source-error", "prepare-error", "guard-denied", "bad-snapshot", "bad-selection"} {
		t.Run(mode, func(t *testing.T) {
			f, e, r := newOperationFixture(t, "start")
			status, code := "refused", "unsupported_configuration"
			switch mode {
			case "deferred":
				f.plan = Plan{Status: "deferred", Reason: "no_eligible_candidate"}
				status, code = "deferred", "no_eligible_candidate"
			case "source-error":
				f.resolveErr = errors.New("sensitive")
				status, code = "unavailable", "source_unavailable"
			case "prepare-error":
				f.prepareErr = Refusal{"unsupported_configuration"}
			case "guard-denied":
				f.revalidateErr = Refusal{"task_conflict"}
				code = "task_conflict"
			case "bad-snapshot":
				f.plan.Sources.Digest = strings.Repeat("f", 64)
				status, code = "unavailable", "source_conflict"
			case "bad-selection":
				f.plan.Selected.Configuration.Effort = "unrequested"
				status, code = "unavailable", "source_conflict"
			}
			response(t, invocation(t, e, r), "receipt", status, code, 2)
			state, _ := retained(t, f)
			if !state.TerminalNoEffect || state.PossibleEffect || f.start != 0 {
				t.Fatal("pre-effect refusal not terminal/no effect")
			}
			original := bytes.Clone(f.raw)
			counts := []int{f.resolve, f.prepare, f.start, f.observe, f.commits}
			for _, input := range []Request{r, statusRequest(t, r)} {
				x := response(t, invocation(t, e, input), "receipt", status, code, 2)
				if x["historical"] != true {
					t.Fatal("not historical")
				}
			}
			if !bytes.Equal(original, f.raw) || !reflect.DeepEqual(counts, []int{f.resolve, f.prepare, f.start, f.observe, f.commits}) {
				t.Fatal("terminal decision reopened")
			}
		})
	}
}

// Proves interruption after reservation cannot rerun selection, and interrupted
// pure preparation with no retained lower binding cannot discover/start a target.
func TestOperationInterruptedBeforeBinding(t *testing.T) {
	for _, at := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			f, e, r := newOperationFixture(t, "start")
			f.failAt = at
			f.failMode = "crash"
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("control interruption did not happen")
					}
				}()
				invocation(t, e, r)
			}()
			original := bytes.Clone(f.raw)
			counts := []int{f.resolve, f.prepare, f.start, f.observe, f.commits}
			for _, input := range []Request{r, statusRequest(t, r)} {
				x := response(t, invocation(t, e, input), "receipt", "unknown", "readiness_unknown", 3)
				if x["ready"] != nil {
					t.Fatal("invented ready")
				}
			}
			if !bytes.Equal(original, f.raw) || !reflect.DeepEqual(counts, []int{f.resolve, f.prepare, f.start, f.observe, f.commits}) {
				t.Fatal("interruption caused discovery/preparation")
			}
		})
	}
}

// Proves each write refusal/lost acknowledgement preserves one identity and
// never retries Start, including refusal before/after the possible-effect marker
// and after a successful external result. Post-commit errors stay uncertainty.
func TestOperationPersistenceFaults(t *testing.T) {
	for _, mode := range []string{"before", "after", "cas", "twice"} {
		for at := 1; at <= 7; at++ {
			t.Run(fmt.Sprintf("%s-%d", mode, at), func(t *testing.T) {
				f, e, r := newOperationFixture(t, "start")
				f.failMode = mode
				f.failAt = at
				out := invocation(t, e, r)
				x := response(t, out, "recovery", "unknown", "", 3)
				if x["key"].(map[string]any)["correlation_id"] != r.Key().CorrelationID || x["request_digest"] != r.Digest() || f.commits != at || f.start > 1 {
					t.Fatal("failed write lost identity or retried")
				}
				if at <= 5 && f.start != 0 {
					t.Fatal("Start before marker acknowledgement")
				}
				count := f.start
				if f.raw != nil {
					invocation(t, e, r)
					invocation(t, e, statusRequest(t, r))
				} // read-only reconciliation may update the original record.
				if f.start != count || f.resolve > 1 || f.prepare > 1 {
					t.Fatal("fault replay repeated lifecycle")
				}
			})
		}
	}
}

// Proves a lost Start result retains the original intent/marker and Status may
// learn the first qualified generation, while Prepare replay cannot retry Start.
func TestOperationLostStartResult(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	f.startErr = errors.New("lost result")
	response(t, invocation(t, e, r), "receipt", "unknown", "preparation_unresolved", 3)
	state, b := retained(t, f)
	if !state.PossibleEffect || state.Lower == nil || state.KnownExecution != nil {
		t.Fatal("lost original lower binding")
	}
	response(t, invocation(t, e, r), "receipt", "unknown", "preparation_unresolved", 3)
	response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
	current, next := retained(t, f)
	if f.start != 1 || f.prepare != 1 || f.resolve != 1 || f.observe != 1 || next.OperationRef() != b.OperationRef() || !same(state.Lower, current.Lower) || current.KnownExecution == nil {
		t.Fatal("reconciliation replaced operation")
	}
}

// Proves current witness identity, complete union and expiry are actual gates,
// rather than treating lower success or an old ready receipt as fresh authority.
func TestOperationReadinessRefusals(t *testing.T) {
	for _, mode := range []string{"generation", "thread", "wrong-key", "wrong-digest", "wrong-lower", "wrong-selected", "missing-ready", "bad-union", "expired", "future", "ready-after-source", "duplicate-evidence"} {
		t.Run(mode, func(t *testing.T) {
			f, e, r := newOperationFixture(t, "start")
			response(t, invocation(t, e, r), "receipt", "ready", "ok", 0)
			original, _ := retained(t, f)
			valid := f.observation
			f.observation = func(v View) Observation {
				o := valid(v)
				switch mode {
				case "generation":
					o.Ready.Execution["generation"] = "replacement"
				case "thread":
					o.Ready.Execution["thread"] = "replacement"
				case "wrong-key":
					o.Key.CorrelationID = "other"
				case "wrong-digest":
					o.RequestDigest = strings.Repeat("0", 64)
				case "wrong-lower":
					o.Lower.OperationRef = "other"
				case "wrong-selected":
					o.Selected.Member = "other"
				case "missing-ready":
					o.Ready = nil
				case "bad-union":
					o.Ready.Execution["controller_epoch"] = "not-a-generation"
				case "expired":
					o.CheckedAt = f.now.Add(-time.Minute).Format(wireTime)
					o.ValidUntil = f.now.Format(wireTime)
					o.Ready.ValidUntil = o.ValidUntil
				case "future":
					o.CheckedAt = f.now.Add(time.Second).Format(wireTime)
				case "ready-after-source":
					o.Ready.ValidUntil = f.now.Add(2 * time.Minute).Format(wireTime)
				case "duplicate-evidence":
					o.Evidence = append(o.Evidence, o.Evidence[0])
				}
				return o
			}
			code := "source_conflict"
			if mode == "expired" || mode == "future" {
				code = "readiness_expired"
			}
			x := response(t, invocation(t, e, statusRequest(t, r)), "receipt", "unknown", code, 3)
			current, _ := retained(t, f)
			if x["ready"] != nil || !same(original.KnownExecution, current.KnownExecution) || f.start != 1 || f.resolve != 1 || f.prepare != 1 {
				t.Fatal("bad witness rebound or restarted")
			}
		})
	}
}

// Proves interruption inside Start leaves the durable marker/binding. Resumed
// Prepare cannot call Start, and Status only observes that original operation.
func TestOperationInterruptedStart(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	f.onStart = func(View) { panic("injected lost caller") }
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("interruption did not run")
			}
		}()
		invocation(t, e, r)
	}()
	state, _ := retained(t, f)
	if !state.PossibleEffect || state.Phase != "preparing" || state.Lower == nil {
		t.Fatal("interruption lost durable marker")
	}
	response(t, invocation(t, e, r), "receipt", "unknown", "readiness_unknown", 3)
	response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
	if f.start != 1 || f.prepare != 1 || f.resolve != 1 || f.observe != 1 {
		t.Fatal("interruption repeated action")
	}
}

// Proves pending/unknown can later become ready by observing the same retained
// target, with separate Host native/messaging identities and no extra Start.
func TestOperationPendingAndHostWitness(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	valid := f.observation
	f.observation = func(v View) Observation {
		o := valid(v)
		o.Status = "pending"
		o.Reason = "readiness_unknown"
		o.Ready = nil
		return o
	}
	response(t, invocation(t, e, r), "receipt", "pending", "readiness_unknown", 3)
	response(t, invocation(t, e, r), "receipt", "pending", "readiness_unknown", 3)
	f.observation = func(v View) Observation {
		o := valid(v)
		o.Ready.Execution = map[string]string{"kind": "host", "execution": "execution", "runner_instance": "runner", "native_thread": "native-thread", "controller_epoch": "epoch", "messaging_session": "message-session", "messaging_thread": "message-thread", "messaging_generation": "message-generation"}
		return o
	}
	x := response(t, invocation(t, e, statusRequest(t, r)), "receipt", "ready", "ok", 0)
	ident := x["ready"].(map[string]any)["execution"].(map[string]any)
	if ident["native_thread"] == ident["messaging_thread"] || ident["controller_epoch"] == ident["messaging_generation"] || f.start != 1 {
		t.Fatal("identity families collapsed")
	}
}

// Proves two cooperating callers serialize the same key: only one resolves or
// starts; the other sees a historical receipt instead of executing a second time.
func TestOperationConcurrentPrepare(t *testing.T) {
	f, e, r := newOperationFixture(t, "start")
	outputs := make(chan Output, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			o, err := e.Run(ctx, r)
			outputs <- o
			errs <- err
		}()
	}
	wg.Wait()
	close(outputs)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	counts := map[int]int{}
	for o := range outputs {
		counts[o.ExitCode()]++
	}
	if counts[0] != 1 || counts[3] != 1 || f.start != 1 || f.resolve != 1 || f.prepare != 1 {
		t.Fatal("duplicate effect under serialized custody")
	}
}
