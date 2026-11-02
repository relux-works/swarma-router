package launch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var testTime = time.Now().UTC()

func intent() Intent {
	return Intent{Contract: Contract, CorrelationID: "launch-1", Project: "project", TaskID: "TASK-261102-example", Member: "worker", Manager: "manager", Plugin: "fake/0.1", PluginConfig: strings.Repeat("a", 64), Runtime: "codex_cli", Profile: "development-member", ProfileLock: strings.Repeat("b", 64), Model: "gpt-6.1-sol", Effort: "high", Workspace: "/work/project", TmuxSession: "member-worker", Mode: "start"}
}
func attempt(i Intent) Attempt {
	return Attempt{Command: []string{"/tool/curator", "run", i.Runtime, "--native"}, Profile: i.Profile, ProfileLock: i.ProfileLock, OperationDigest: i.Digest(), Session: i.TmuxSession, Pane: "%1", PanePID: 100, CuratorRun: Process{101, 100, "birth", "/tool/curator-run"}}
}

type memoryStore struct {
	record        Record
	found         bool
	saves         int
	failSave      int
	visibleOnFail bool
	syncErr       bool
	afterSave     func()
}

func (s *memoryStore) Load(string) (Record, bool, error) { return s.record, s.found, nil }
func (s *memoryStore) Save(_ string, r Record) error {
	s.saves++
	if s.failSave == s.saves {
		if s.visibleOnFail {
			s.record, s.found = r, true
		}
		return errors.New("injected sync failure")
	}
	s.record, s.found = r, true
	if s.afterSave != nil {
		s.afterSave()
	}
	return nil
}
func (s *memoryStore) Sync(string) error {
	if s.syncErr {
		return errors.New("sync failed")
	}
	return nil
}
func (s *memoryStore) Close() error { return nil }

type fakeGuard struct {
	calls  int
	change func(*Admission)
	err    bool
}

func (g *fakeGuard) Check(_ context.Context, i Intent, d string, now time.Time) (Admission, error) {
	g.calls++
	a := Admission{d, i.Manager, i.Member, i.ProfileLock, "stopped", now, now.Add(time.Minute)}
	if g.change != nil {
		g.change(&a)
	}
	if g.err {
		return a, errors.New("PRIVATE TOKEN must not escape")
	}
	return a, nil
}

type fakePlugin struct {
	starts, observes, prepares int
	startErr                   bool
	wrongReceipt               bool
}

func (p *fakePlugin) ID() string                        { return "fake/0.1" }
func (p *fakePlugin) Configuration() string             { return strings.Repeat("a", 64) }
func (p *fakePlugin) Prepare(i Intent) (Attempt, error) { p.prepares++; return attempt(i), nil }
func (p *fakePlugin) Start(_ context.Context, i Intent) (Attempt, string, error) {
	p.starts++
	a := attempt(i)
	if p.wrongReceipt {
		a.OperationDigest = strings.Repeat("0", 64)
	}
	if p.startErr {
		return a, "unknown", errors.New("PRIVATE OUTPUT")
	}
	return a, "attempted", nil
}
func (p *fakePlugin) Observe(_ context.Context, i Intent, _ *Attempt) (Attempt, string, error) {
	p.observes++
	return attempt(i), "running", nil
}
func routerFixture() (Router, *fakePlugin, *fakeGuard, *memoryStore) {
	p, g, s := &fakePlugin{}, &fakeGuard{}, &memoryStore{}
	return Router{Manager: "manager", Project: "project", Plugin: p, Guard: g, OpenStore: func() (Store, error) { return s, nil }, Now: func() time.Time { return testTime }}, p, g, s
}

// A valid launch invokes Start once; replay and Observe cannot invoke it again.
func TestSingleAttemptReplayAndObserve(t *testing.T) {
	r, p, g, s := routerFixture()
	i := intent()
	opened := 0
	r.OpenStore = func() (Store, error) { opened++; return s, nil }
	if got := r.Prepare(i); got.Status != "prepared" || opened != 0 || g.calls != 0 || p.starts != 0 {
		t.Fatalf("prepare effects: %+v", got)
	}
	got := r.Start(context.Background(), i)
	if got.Status != "attempted" || got.Historical || p.starts != 1 || g.calls != 1 {
		t.Fatalf("start: %+v", got)
	}
	got = r.Start(context.Background(), i)
	if got.Status != "attempted" || !got.Historical || p.starts != 1 || g.calls != 1 {
		t.Fatalf("replay starts=%d guard=%d %+v", p.starts, g.calls, got)
	}
	got = r.Observe(context.Background(), i)
	if got.Status != "running" || got.Historical || p.starts != 1 || p.observes != 1 {
		t.Fatalf("observe: %+v", got)
	}
	i.Model = "another-model"
	got = r.Start(context.Background(), i)
	if got.Status != "conflict" || p.starts != 1 {
		t.Fatalf("conflict: %+v", got)
	}
}

// Each invalid admission is paired with the valid control above and causes zero Start.
func TestAdmissionRefusals(t *testing.T) {
	cases := map[string]func(*Admission){"digest": func(a *Admission) { a.Digest = "wrong" }, "manager": func(a *Admission) { a.Manager = "other" }, "member": func(a *Admission) { a.Member = "other" }, "lock": func(a *Admission) { a.ProfileLock = "wrong" }, "future": func(a *Admission) { a.CheckedAt = testTime.Add(time.Second) }, "expired": func(a *Admission) { a.ExpiresAt = testTime }, "unknown": func(a *Admission) { a.State = "unknown" }, "running": func(a *Admission) { a.State = "running" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r, p, g, _ := routerFixture()
			g.change = change
			got := r.Start(context.Background(), intent())
			if got.Status != "refused" || p.starts != 0 {
				t.Fatalf("%+v starts=%d", got, p.starts)
			}
		})
	}
	r, p, g, _ := routerFixture()
	g.err = true
	if got := r.Start(context.Background(), intent()); got.Status != "refused" || strings.Contains(got.Reason, "PRIVATE") || p.starts != 0 {
		t.Fatal(got)
	}
}

// Cancellation or expiry during admission/persistence causes zero plugin Starts;
// a durable marker cannot silently become permission to launch after its deadline.
func TestAdmissionExpiresBeforeEffect(t *testing.T) {
	for _, phase := range []string{"cancel_before", "expire_guard", "expire_save", "cancel_save"} {
		t.Run(phase, func(t *testing.T) {
			r, p, g, s := routerFixture()
			now := testTime
			r.Now = func() time.Time { return now }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch phase {
			case "cancel_before":
				cancel()
			case "expire_guard":
				g.change = func(a *Admission) { now = a.ExpiresAt }
			case "expire_save":
				s.afterSave = func() { now = testTime.Add(time.Minute) }
			case "cancel_save":
				s.afterSave = cancel
			}
			got := r.Start(ctx, intent())
			if got.Status != "refused" || p.starts != 0 {
				t.Fatalf("%+v starts=%d", got, p.starts)
			}
			if s.found && s.record.Result.Status != "refused" {
				t.Fatalf("retained state: %+v", s.record)
			}
		})
	}
}

// Request/configuration mistakes cannot reach a plugin or create an attempt marker.
func TestInputAndCapabilityRefusals(t *testing.T) {
	cases := map[string]func(*Intent){"version": func(i *Intent) { i.Contract = "other" }, "scope": func(i *Intent) { i.Project = "other" }, "plugin": func(i *Intent) { i.Plugin = "other/0.1" }, "config": func(i *Intent) { i.PluginConfig = strings.Repeat("c", 64) }, "argv": func(i *Intent) { i.Model = "--bad" }, "shell": func(i *Intent) { i.TmuxSession = "a;touch b" }, "session_selector": func(i *Intent) { i.TmuxSession = "member.worker" }, "relative": func(i *Intent) { i.Workspace = "." }, "resume": func(i *Intent) { i.Mode = "resume" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r, p, _, s := routerFixture()
			i := intent()
			change(&i)
			got := r.Start(context.Background(), i)
			if got.Status != "refused" || p.starts != 0 || s.saves != 0 {
				t.Fatal(got)
			}
		})
	}
}

// Both pre-effect failure and post-effect uncertainty retain a no-retry record.
func TestDurabilityAndLostReply(t *testing.T) {
	for _, failure := range []string{"intent", "receipt", "reply", "receipt_binding", "replay_sync"} {
		t.Run(failure, func(t *testing.T) {
			r, p, _, s := routerFixture()
			switch failure {
			case "intent":
				s.failSave = 1
				s.visibleOnFail = true
			case "receipt":
				s.failSave = 2
			case "reply":
				p.startErr = true
			case "receipt_binding":
				p.wrongReceipt = true
			}
			got := r.Start(context.Background(), intent())
			wantStarts := 1
			if failure == "intent" {
				wantStarts = 0
				if got.Status != "refused" {
					t.Fatal(got)
				}
			} else if failure != "replay_sync" && got.Status != "unknown" {
				t.Fatal(got)
			}
			if failure == "replay_sync" {
				s.syncErr = true
			}
			got = r.Start(context.Background(), intent())
			if got.Status != "unknown" || p.starts != wantStarts {
				t.Fatalf("replay %+v starts=%d", got, p.starts)
			}
		})
	}
}

// Missing or corrupt state cannot be repaired by Observe launching anything.
func TestUnavailableObservation(t *testing.T) {
	r, p, _, s := routerFixture()
	got := r.Observe(context.Background(), intent())
	if got.Status != "unknown" || p.starts != 0 || p.observes != 0 {
		t.Fatal(got)
	}
	s.found = true
	s.record = Record{}
	got = r.Start(context.Background(), intent())
	if got.Status != "unknown" || p.starts != 0 {
		t.Fatal(got)
	}
}
