package workbench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relux.works/swarma-router/launch"
	"relux.works/swarma-router/selection"
)

// All native/session/owner facts below are isolated fixtures. Create records an
// in-memory snapshot only; it never executes Curator, tmux, a runtime or a post.
type dockingBackend struct {
	present                        bool
	snapshot                       launch.Snapshot
	creates, snapshots, readyReads int
	command                        []string
	clock                          func() time.Time
	lastReady                      *launch.Readiness
	readyChange                    func(*launch.Readiness)
}

func (b *dockingBackend) Snapshot(_ context.Context, session string) (launch.Snapshot, bool, error) {
	b.snapshots++
	if b.present && session != b.snapshot.Session {
		return launch.Snapshot{}, false, errors.New("wrong fixture session")
	}
	return clone(b.snapshot), b.present, nil
}
func (b *dockingBackend) Create(_ context.Context, i launch.Intent, command []string) error {
	if b.present {
		return errors.New("fixture already present")
	}
	b.creates++
	b.present = true
	b.command = append([]string{}, command...)
	b.snapshot = launch.Snapshot{Session: i.TmuxSession, Pane: "%31", PanePID: 100, OperationDigest: i.Digest(), Processes: []launch.Process{{PID: 100, ParentPID: 99, Birth: "shell-birth", Executable: "/fixture/zsh"}, {PID: 101, ParentPID: 100, Birth: "launcher-birth", Executable: "/fixture/curator-run"}, {PID: 102, ParentPID: 101, Birth: "runtime-birth", Executable: "/fixture/codex"}}}
	return nil
}
func (b *dockingBackend) Read(_ context.Context, i launch.Intent, s launch.Snapshot) (launch.Readiness, error) {
	b.readyReads++
	runtime := s.Processes[len(s.Processes)-1]
	now := b.clock()
	r := launch.Readiness{TaskID: i.TaskID, Member: i.Member, Session: s.Session, Pane: s.Pane, Runtime: runtime, BindingRuntime: runtime, CheckedAt: now.Add(-time.Millisecond), ExpiresAt: now.Add(time.Minute), Thread: "original-thread", BindingRef: "fixture-binding", LaneRef: "fixture-lane", LaneSender: "@" + i.Member + ":fixture.invalid"}
	if b.readyChange != nil {
		b.readyChange(&r)
	}
	copy := clone(r)
	b.lastReady = &copy
	return r, nil
}

type dockingOwner struct {
	*lowerFixture
	plugin  launch.Curator
	backend *dockingBackend
}

func (o *dockingOwner) Specification(ctx context.Context, v View) (LaunchSpecification, error) {
	s, err := o.lowerFixture.Specification(ctx, v)
	s.Intent.Plugin = o.plugin.ID()
	s.Intent.PluginConfig = o.plugin.Configuration()
	return s, err
}
func (o *dockingOwner) Witness(ctx context.Context, v View, result *launch.Result) (LifecycleWitness, error) {
	if result != nil && result.Status == "running" {
		ready := o.backend.lastReady
		if ready == nil || result.Attempt == nil || result.Attempt.Runtime != ready.Runtime || ready.Runtime != ready.BindingRuntime || ready.Thread != "original-thread" || ready.LaneRef != "fixture-lane" {
			return LifecycleWitness{}, Refusal{"source_conflict"}
		}
	}
	return o.lowerFixture.Witness(ctx, v, result)
}

type dockingSystem struct {
	engine     Engine
	request    Request
	repository FileRepository
	lowerDir   string
	source     *ownerFixture
	owner      *dockingOwner
	backend    *dockingBackend
}

func newDockingSystem(t *testing.T, state selection.State) *dockingSystem {
	t.Helper()
	f, l, _, planner, r, _, source := lowerTestFixture(t, state)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upper := FileRepository{Directory: filepath.Join(parent, "upper")}
	lower := filepath.Join(parent, "lower")
	backend := &dockingBackend{clock: l.Clock}
	config := launch.CuratorConfig{Executable: "/fixture/curator", Home: "/fixture/home", Path: "/fixture/bin", TempDir: "/fixture/tmp", Permissions: "yolo", Agent: f.target.OwnerMember, Server: "fixture.invalid", A2AConfigDir: "/fixture/a2a"}
	plugin := launch.Curator{Config: config, Backend: backend, Ready: backend, Now: l.Clock}
	owner := &dockingOwner{lowerFixture: f, plugin: plugin, backend: backend}
	l.Owner = owner
	l.Router.Plugin = plugin
	l.Router.OpenStore = func() (launch.Store, error) { return launch.OpenFileStore(lower) }
	access, _, _ := newOperationFixture(t, "start")
	engine := Engine{Repository: upper, Access: fixtureAccess{access}, Planner: planner, Lifecycle: l, Clock: l.Clock}
	return &dockingSystem{engine: engine, request: r, repository: upper, lowerDir: lower, source: source, owner: owner, backend: backend}
}
func (s *dockingSystem) upperPath() string {
	return filepath.Join(s.repository.Directory, "workbench-v2-"+hash(canonical(object(s.request.Key())))+".json")
}
func (s *dockingSystem) record(t *testing.T) operationRecord {
	t.Helper()
	raw, err := os.ReadFile(s.upperPath())
	if err != nil {
		t.Fatal(err)
	}
	r, _, ok := decodeOperation(raw, s.request.Key())
	if !ok {
		t.Fatal("fixture record invalid")
	}
	if len(raw) > MaxRecordBytes {
		t.Fatal("aggregate record exceeded capacity")
	}
	return r
}
func (s *dockingSystem) lowerPath(t *testing.T) string {
	t.Helper()
	r := s.record(t)
	if r.Lower == nil {
		t.Fatal("missing original lower")
	}
	i := r.Lower.Intent
	return filepath.Join(s.lowerDir, "launch-"+hash([]byte(i.Manager+"/"+i.Project+"/"+i.CorrelationID))+".json")
}

// Complete real-code docking uses both private stores and the real Curator
// plugin, with only backend/owner facts mocked. Disk reopen and inert historical
// Prepare retain exact bytes. Reuse never opens a lower store or creates a seat.
func TestWorkbenchDockingPrivateStores(t *testing.T) {
	for _, state := range []selection.State{selection.Stopped, selection.Ready} {
		t.Run(string(state), func(t *testing.T) {
			s := newDockingSystem(t, state)
			first := invocation(t, s.engine, s.request)
			if state == selection.Stopped {
				response(t, first, "receipt", "pending", "readiness_unknown", 3)
			} else {
				response(t, first, "receipt", "ready", "ok", 0)
			}
			before, err := os.ReadFile(s.upperPath())
			if err != nil {
				t.Fatal(err)
			}
			reads, witnesses, creates := s.source.calls, s.owner.witnessCalls, s.backend.creates
			replay := s.engine
			replay.Repository = FileRepository{Directory: s.repository.Directory}
			replay.Planner = nil
			replay.Lifecycle = nil
			replay.Clock = nil
			historical := invocation(t, replay, s.request)
			if historical.ExitCode() != 3 {
				t.Fatal("historical replay claimed fresh ready")
			}
			after, _ := os.ReadFile(s.upperPath())
			if !bytes.Equal(before, after) || s.source.calls != reads || s.owner.witnessCalls != witnesses || s.backend.creates != creates {
				t.Fatal("disk replay changed original state or sources")
			}
			s.engine.Repository = FileRepository{Directory: s.repository.Directory}
			response(t, invocation(t, s.engine, statusRequest(t, s.request)), "receipt", "ready", "ok", 0)
			s.record(t)
			if s.source.calls != reads {
				t.Fatal("Status reselected")
			}
			if state == selection.Stopped {
				if s.backend.creates != 1 || s.backend.readyReads != 1 || s.owner.specCalls != 1 || s.owner.admitCalls != 1 {
					t.Fatal("real lower counts", s.backend.creates, s.backend.readyReads, s.owner.specCalls, s.owner.admitCalls)
				}
				lowerRaw, err := os.ReadFile(s.lowerPath(t))
				if err != nil || len(lowerRaw) == 0 {
					t.Fatal("missing durable original lower", err)
				}
				if len(s.backend.command) < 4 || s.backend.command[0] != "/usr/bin/env" || s.backend.command[1] != "-i" {
					t.Fatal("actual Curator clean command path not used")
				}
			} else if s.backend.creates+s.backend.snapshots+s.backend.readyReads+s.owner.specCalls != 0 {
				t.Fatal("reuse touched native/lower")
			} else if _, err := os.Stat(s.lowerDir); !os.IsNotExist(err) {
				t.Fatal("reuse created lower store", err)
			}
		})
	}
}

// Changed canonical content conflicts under the same disk key. A missing-key
// Status cannot reserve or observe another operation; the original still works.
func TestWorkbenchDockingLookupIdentity(t *testing.T) {
	s := newDockingSystem(t, selection.Stopped)
	response(t, invocation(t, s.engine, s.request), "receipt", "pending", "readiness_unknown", 3)
	before, err := os.ReadFile(s.upperPath())
	if err != nil {
		t.Fatal(err)
	}
	reads, witnesses, snapshots := s.source.calls, s.owner.witnessCalls, s.backend.snapshots
	m := base(t)
	m["constraints"].(map[string]any)["objective"] = "different objective"
	changed := mustParse(t, frame(t, m), local)
	if changed.Key() != s.request.Key() || changed.Digest() == s.request.Digest() {
		t.Fatal("invalid conflict fixture")
	}
	response(t, invocation(t, s.engine, changed), "receipt", "conflict", "idempotency_conflict", 2)
	missing := mustParse(t, frame(t, map[string]any{"contract": Contract, "operation": "status", "correlation_id": "missing-operation", "caller": s.request.Key().Caller, "project": s.request.Key().Project, "expected_request_digest": s.request.Digest()}), local)
	result := response(t, invocation(t, s.engine, missing), "error", "", "", 2)
	if result["code"] != "operation_not_found" {
		t.Fatal("missing key did not remain absent", result)
	}
	after, err := os.ReadFile(s.upperPath())
	if err != nil || !bytes.Equal(before, after) || s.source.calls != reads || s.owner.witnessCalls != witnesses || s.backend.snapshots != snapshots || s.backend.creates != 1 {
		t.Fatal("lookup mismatch changed original operation or observed/repeated effect", err)
	}
	missingPath := filepath.Join(s.repository.Directory, "workbench-v2-"+hash(canonical(object(missing.Key())))+".json")
	if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatal("missing Status reserved an operation", err)
	}
	response(t, invocation(t, s.engine, statusRequest(t, s.request)), "receipt", "ready", "ok", 0)
}

// A real lower running result cannot override a missing/stale/misbound upper
// witness or changed original generation. Lower runtime/lock/task/route checks
// also remain active. Every failed observation retains one original effect/key.
func TestWorkbenchDockingReadinessBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "no-upper", "wrong-result", "upper-expired", "generation", "runtime", "lock", "lower-task", "lower-route", "owner-task-refusal"} {
		t.Run(mode, func(t *testing.T) {
			s := newDockingSystem(t, selection.Stopped)
			response(t, invocation(t, s.engine, s.request), "receipt", "pending", "readiness_unknown", 3)
			original := s.record(t).Lower
			reads := s.source.calls
			if mode == "generation" {
				response(t, invocation(t, s.engine, statusRequest(t, s.request)), "receipt", "ready", "ok", 0)
			}
			switch mode {
			case "no-upper":
				s.owner.witnessErr = errors.New("fixture upper unavailable")
			case "wrong-result":
				s.owner.witnessChange = func(w *LifecycleWitness) { w.ResultDigest = strings.Repeat("0", 64) }
			case "upper-expired":
				s.owner.witnessChange = func(w *LifecycleWitness) { w.Observation.Ready.ValidUntil = s.owner.now.Format(wireTime) }
			case "generation":
				s.owner.witnessChange = func(w *LifecycleWitness) { w.Observation.Ready.Execution["generation"] = "different" }
			case "runtime":
				s.backend.snapshot.Processes[2].Birth = "different-runtime-birth"
			case "lock":
				s.backend.snapshot.OperationDigest = strings.Repeat("0", 64)
			case "lower-task":
				s.backend.readyChange = func(r *launch.Readiness) { r.TaskID = "TASK-261103-other" }
			case "lower-route":
				s.backend.readyChange = func(r *launch.Readiness) { r.LaneSender = "@other:fixture.invalid" }
			case "owner-task-refusal":
				s.owner.witnessErr = Refusal{"task_state_changed"}
			}
			out := invocation(t, s.engine, statusRequest(t, s.request))
			if mode == "valid" {
				response(t, out, "receipt", "ready", "ok", 0)
			} else if out.ExitCode() == 0 || bytes.Contains(out.Bytes(), []byte(`"status":"ready"`)) {
				t.Fatal("invalid docking readiness admitted", mode, string(out.Bytes()))
			}
			r := s.record(t)
			if !same(r.Lower, original) || s.source.calls != reads || s.backend.creates != 1 || s.owner.specCalls != 1 || s.owner.admitCalls != 1 {
				t.Fatal("failed observation changed operation/repeated effect")
			}
		})
	}
}

// Busy/corrupt private storage is never absence or a retry invitation. Fixtures
// alone are corrupted; no real journal, session or data is modified. Upper errors
// precede sources; lower errors stay attached to the retained original intent.
func TestWorkbenchDockingStorageRefusals(t *testing.T) {
	for _, mode := range []string{"upper-busy", "lower-busy", "upper-corrupt", "lower-corrupt"} {
		t.Run(mode, func(t *testing.T) {
			s := newDockingSystem(t, selection.Stopped)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			switch mode {
			case "upper-busy":
				reads := s.source.calls
				held, err := s.repository.Open(ctx, s.request.Key())
				if err != nil {
					t.Fatal(err)
				}
				out := invocation(t, s.engine, s.request)
				if err = held.Close(); err != nil {
					t.Fatal(err)
				}
				if out.ExitCode() != 3 || s.backend.creates != 0 || s.source.calls != reads {
					t.Fatal("busy upper admitted work")
				}
				return
			case "lower-busy":
				held, err := launch.OpenFileStore(s.lowerDir)
				if err != nil {
					t.Fatal(err)
				}
				out := invocation(t, s.engine, s.request)
				if err = held.Close(); err != nil {
					t.Fatal(err)
				}
				if out.ExitCode() != 3 || s.backend.creates != 0 {
					t.Fatal("busy lower admitted effect")
				}
				out = invocation(t, s.engine, statusRequest(t, s.request))
				if out.ExitCode() != 3 || s.backend.creates != 0 || s.owner.specCalls != 1 {
					t.Fatal("missing lower receipt retried effect")
				}
				return
			}
			invocation(t, s.engine, s.request)
			reads := s.source.calls
			path := s.upperPath()
			if mode == "lower-corrupt" {
				path = s.lowerPath(t)
			}
			invalid := []byte(`{"corrupt":"fixture"}`)
			if err := os.WriteFile(path, invalid, 0600); err != nil {
				t.Fatal(err)
			}
			out := invocation(t, s.engine, statusRequest(t, s.request))
			if out.ExitCode() != 3 || s.backend.creates != 1 || s.source.calls != reads {
				t.Fatal("corrupt state reselected/retried/claimed ready")
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, invalid) {
				t.Fatal("corrupt fixture reset/rewritten", err)
			}
		})
	}
}

type dockingRepositoryFault struct {
	FileRepository
	after func(string) error
}

func (r dockingRepositoryFault) Open(ctx context.Context, k OperationKey) (Journal, error) {
	j, err := r.FileRepository.Open(ctx, k)
	if err == nil {
		j.(*fileOperationJournal).step = r.after
	}
	return j, err
}

type dockingLowerFault struct {
	launch.Store
	writes *int
}

func (s dockingLowerFault) Save(k string, r launch.Record) error {
	err := s.Store.Save(k, r)
	*s.writes++
	if err == nil && *s.writes == 2 {
		return errors.New("fixture lost lower acknowledgement")
	}
	return err
}

// Actual files survive unacknowledged upper and lower writes after Create.
// Reopening and original-key Status recover without a second backend Create.
func TestWorkbenchDockingLostAcknowledgement(t *testing.T) {
	for _, mode := range []string{"upper", "lower"} {
		t.Run(mode, func(t *testing.T) {
			s := newDockingSystem(t, selection.Stopped)
			if mode == "upper" {
				failed := false
				s.engine.Repository = dockingRepositoryFault{FileRepository: s.repository, after: func(stage string) error {
					if !failed && s.backend.creates == 1 && stage == "acknowledge" {
						failed = true
						return errors.New("fixture lost upper acknowledgement")
					}
					return nil
				}}
			} else {
				lower := s.engine.Lifecycle.(LowerLifecycle)
				writes := 0
				lower.Router.OpenStore = func() (launch.Store, error) {
					store, err := launch.OpenFileStore(s.lowerDir)
					if err != nil {
						return nil, err
					}
					return dockingLowerFault{Store: store, writes: &writes}, nil
				}
				s.engine.Lifecycle = lower
			}
			out := invocation(t, s.engine, s.request)
			if out.ExitCode() != 3 || s.backend.creates != 1 {
				t.Fatal("lost acknowledgement did not retain uncertainty", out.ExitCode(), s.backend.creates)
			}
			original := s.record(t).Lower
			s.engine.Repository = s.repository
			lower := s.engine.Lifecycle.(LowerLifecycle)
			lower.Router.OpenStore = func() (launch.Store, error) { return launch.OpenFileStore(s.lowerDir) }
			s.engine.Lifecycle = lower
			response(t, invocation(t, s.engine, statusRequest(t, s.request)), "receipt", "ready", "ok", 0)
			if !same(s.record(t).Lower, original) || s.backend.creates != 1 || s.owner.specCalls != 1 {
				t.Fatal("reconciliation changed operation or repeated effect")
			}
		})
	}
}

// A source snapshot that fits its own budget can exceed the aggregate durable
// record budget after encoding. The engine refuses before lower preparation or
// Create, while the adjacent one-target control remains usable.
func TestWorkbenchDockingAggregateBudget(t *testing.T) {
	s := newDockingSystem(t, selection.Stopped)
	m := base(t)
	// Full legitimate refs increase retained receipt content without changing
	// wire/source limits or introducing unknown fields merely to pad a test.
	task := m["task"].(map[string]any)
	for _, field := range []string{"source", "object", "revision"} {
		task[field] = strings.Repeat("r", 100)
	}
	config := cfg(m)
	configs := []any{}
	for i := 0; i < 16; i++ {
		c := clone(config)
		c["model"].(map[string]any)["revision"] = fmt.Sprintf("revision-%d", i)
		configs = append(configs, c)
	}
	m["constraints"].(map[string]any)["configurations"] = configs
	r := mustParse(t, frame(t, m), local)
	planner := s.engine.Planner.(SourcePlanner)
	s.source.data.Targets = nil
	for _, config := range r.prepare.Constraints.Configurations {
		b := s.owner.target
		b.Configuration = config
		target := OwnerTarget{Binding: b}
		bindPlannerTarget(t, &target, r, *s.owner.now)
		s.source.data.Targets = append(s.source.data.Targets, target)
	}
	s.source.data.Task = r.prepare.Task
	s.source.data.Policy = r.prepare.Constraints.Policy
	s.source.data.Cards = r.prepare.Cards
	s.request = r
	plan, err := planner.Resolve(contextWithDockingDeadline(t), *r.prepare)
	if err != nil {
		t.Fatal("valid snapshot failed its own budget", err)
	}
	if len(plan.Sources.Data) >= MaxRecordBytes || len(plan.Sources.Data)*4/3 < MaxRecordBytes {
		t.Fatal("fixture does not distinguish aggregate capacity", len(plan.Sources.Data))
	}
	out := invocation(t, s.engine, r)
	if out.ExitCode() != 3 || s.backend.creates != 0 || s.owner.specCalls != 0 {
		t.Fatal("oversize aggregate reached preparation/effect", out.ExitCode())
	}
	record := s.record(t)
	if record.PossibleEffect || record.Lower != nil {
		t.Fatal("aggregate failure retained effect intent")
	}
}
func contextWithDockingDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
