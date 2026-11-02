package launch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func curatorFixture() (Curator, Intent, *fakeBackend, *fakeReady) {
	cfg := CuratorConfig{Executable: "/tool/curator", Home: "/home/operator", Path: "/tool:/usr/bin:/bin", TempDir: "/tmp", Permissions: "yolo", Agent: "worker", Server: "example.test", A2AConfigDir: "/config/worker"}
	i := intent()
	i.Plugin = "curator/0.1"
	i.PluginConfig = cfg.digest()
	b := &fakeBackend{}
	ready := &fakeReady{}
	c := Curator{Config: cfg, Backend: b, Ready: ready, Now: func() time.Time { return testTime }}
	b.snap = snapshot(i)
	return c, i, b, ready
}
func snapshot(i Intent) Snapshot {
	return Snapshot{Session: i.TmuxSession, Pane: "%1", PanePID: 100, OperationDigest: i.Digest(), Processes: []Process{{100, 1, "birth", "/tool/curator"}, {101, 100, "birth", "/tool/curator-run"}, {102, 101, "birth", "/tool/codex"}}}
}

type fakeBackend struct {
	snap          Snapshot
	active        bool
	creates       int
	command       []string
	err           bool
	createErr     bool
	afterSnapshot func()
}

func (b *fakeBackend) Snapshot(context.Context, string) (Snapshot, bool, error) {
	if b.afterSnapshot != nil {
		b.afterSnapshot()
	}
	if b.err {
		return Snapshot{}, false, errors.New("unknown")
	}
	return b.snap, b.active, nil
}
func (b *fakeBackend) Create(_ context.Context, _ Intent, cmd []string) error {
	b.creates++
	b.command = append([]string{}, cmd...)
	b.active = true
	if b.createErr {
		return errors.New("lost reply")
	}
	return nil
}

type fakeReady struct {
	change func(*Readiness)
	err    bool
}

func (p *fakeReady) Read(_ context.Context, i Intent, s Snapshot) (Readiness, error) {
	v := Readiness{TaskID: i.TaskID, Member: i.Member, Session: s.Session, Pane: s.Pane, Runtime: s.Processes[2], BindingRuntime: s.Processes[2], CheckedAt: testTime, ExpiresAt: testTime.Add(time.Minute), Thread: "native-thread", BindingRef: "verified-binding", LaneRef: "verified-event", LaneSender: "@worker:example.test"}
	if p.change != nil {
		p.change(&v)
	}
	if p.err {
		return v, errors.New("unavailable")
	}
	return v, nil
}

// The production Curator plugin is exercised through an isolated backend trace:
// exact argv/clean child environment and attempt-only status, with no real session.
func TestCuratorPluginTrace(t *testing.T) {
	c, i, b, _ := curatorFixture()
	a, status, err := c.Start(context.Background(), i)
	if err != nil || status != "attempted" || !a.complete(i) || b.creates != 1 {
		t.Fatalf("%+v %s %v", a, status, err)
	}
	want := []string{"/usr/bin/env", "-i", "HOME=/home/operator", "PATH=/tool:/usr/bin:/bin", "TMPDIR=/tmp", "LANG=en_US.UTF-8", "A2A_CONFIG_DIR=/config/worker", "A2A_AGENT=worker", "/tool/curator", "run", "codex_cli", "--native", "--profile", "development-member", "--model", "gpt-6.1-sol", "--effort", "high", "--permissions", "yolo"}
	if !reflect.DeepEqual(b.command, want) {
		t.Fatalf("command\n%q\nwant\n%q", b.command, want)
	}
	if got, st, e := c.Observe(context.Background(), i, &a); e != nil || st != "running" || b.creates != 1 || got.Runtime.PID != 102 {
		t.Fatalf("%+v %s %v", got, st, e)
	}
}

// Both runtime resume forms and explicit native fallback preserve exact values;
// unrecorded fallback and malformed configuration cannot produce a command.
func TestCuratorTranslationCompatibility(t *testing.T) {
	for _, runtime := range []string{"codex_cli", "claude_code"} {
		for _, permission := range []string{"yolo", "native"} {
			t.Run(runtime+permission, func(t *testing.T) {
				c, i, _, _ := curatorFixture()
				i.Runtime = runtime
				i.Mode = "resume"
				i.NativeSession = "existing-thread"
				c.Config.Permissions = permission
				if permission == "native" {
					c.Config.YoloRefusal = "recorded-refusal"
				}
				c.Config.Network = "gateway-profile"
				i.PluginConfig = c.Configuration()
				a, err := c.Prepare(i)
				if err != nil {
					t.Fatal(err)
				}
				joined := strings.Join(a.Command, " ")
				suffix := "-- resume existing-thread"
				if runtime == "claude_code" {
					suffix = "-- --resume existing-thread"
				}
				if permission == "yolo" && !strings.HasSuffix(joined, suffix) {
					t.Fatal(joined)
				}
				if permission == "native" {
					flag := "--dangerously-bypass-approvals-and-sandbox resume existing-thread"
					if runtime == "claude_code" {
						flag = "--dangerously-skip-permissions --resume existing-thread"
					}
					if !strings.HasSuffix(joined, "-- "+flag) {
						t.Fatal(joined)
					}
				}
				if !strings.Contains(joined, "--network gateway-profile -- ") {
					t.Fatal(joined)
				}
			})
		}
	}
	for _, change := range []func(*CuratorConfig){func(c *CuratorConfig) { c.Permissions = "native" }, func(c *CuratorConfig) { c.Path = ":" }, func(c *CuratorConfig) { c.Agent = "other" }, func(c *CuratorConfig) { c.Server = "bad;host" }, func(c *CuratorConfig) { c.Network = "--bad" }, func(c *CuratorConfig) { c.Executable = "curator" }} {
		c, i, _, _ := curatorFixture()
		change(&c.Config)
		i.PluginConfig = c.Configuration()
		if _, err := c.Prepare(i); err == nil {
			t.Fatal("accepted bad configuration")
		}
	}
}

// Fresh absence is required; unknown and existing-session probes make zero creates.
func TestCuratorPreStartRefusals(t *testing.T) {
	for _, state := range []string{"running", "unknown"} {
		c, i, b, _ := curatorFixture()
		b.active = state == "running"
		b.err = state == "unknown"
		_, status, err := c.Start(context.Background(), i)
		if err != nil || status != "refused" || b.creates != 0 {
			t.Fatalf("%s %s %v", state, status, err)
		}
	}
	c, i, b, _ := curatorFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.afterSnapshot = cancel
	if _, status, err := c.Start(ctx, i); status != "refused" || err != nil || b.creates != 0 {
		t.Fatalf("cancelled lookup: %s %v creates=%d", status, err, b.creates)
	}
}

// A lost create reply retains possible effect; there is no fallback invocation.
func TestCuratorLostReply(t *testing.T) {
	c, i, b, _ := curatorFixture()
	b.createErr = true
	a, status, err := c.Start(context.Background(), i)
	if status != "unknown" || err == nil || b.creates != 1 {
		t.Fatal(status, err)
	}
	_, status, err = c.Observe(context.Background(), i, &a)
	if status != "running" || err != nil || b.creates != 1 {
		t.Fatal(status, err)
	}
}

// Runtime metadata alone, a wrong binding or stale owner evidence cannot mean running.
func TestReadinessNegatives(t *testing.T) {
	cases := map[string]func(*Readiness){"wrong_task": func(p *Readiness) { p.TaskID = "TASK-261102-other" }, "wrong_member": func(p *Readiness) { p.Member = "other" }, "wrong_pane": func(p *Readiness) { p.Pane = "%2" }, "wrong_birth": func(p *Readiness) { p.Runtime.Birth = "reused" }, "future": func(p *Readiness) { p.CheckedAt = testTime.Add(time.Second) }, "expired": func(p *Readiness) { p.ExpiresAt = testTime }, "wrong_binding": func(p *Readiness) { p.BindingRuntime.PID++ }, "no_thread": func(p *Readiness) { p.Thread = "" }, "no_event": func(p *Readiness) { p.LaneRef = "" }, "wrong_sender": func(p *Readiness) { p.LaneSender = "@other:example.test" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c, i, b, p := curatorFixture()
			b.active = true
			p.change = change
			a, _, _ := c.bind(i, attemptFromCurator(t, c, i), b.snap)
			_, status, _ := c.Observe(context.Background(), i, &a)
			if status == "running" || b.creates != 0 {
				t.Fatal(status)
			}
		})
	}
	c, i, b, _ := curatorFixture()
	b.active = true
	c.Ready = nil
	if _, status, err := c.Observe(context.Background(), i, nil); status != "pending" || err != nil {
		t.Fatal(status, err)
	}
}
func attemptFromCurator(t *testing.T, c Curator, i Intent) Attempt {
	t.Helper()
	a, err := c.Prepare(i)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Non-chat members need both their native assignment and durable handoff.
func TestNativeReadiness(t *testing.T) {
	c, i, b, p := curatorFixture()
	c.Config.Agent = ""
	c.Config.Server = ""
	c.Config.A2AConfigDir = ""
	i.PluginConfig = c.Configuration()
	b.snap = snapshot(i)
	b.active = true
	p.change = func(p *Readiness) {
		p.BindingRef = ""
		p.LaneRef = ""
		p.NativeAssignment = "assignment"
		p.Handoff = "handoff"
	}
	if _, st, err := c.Observe(context.Background(), i, nil); st != "running" || err != nil {
		t.Fatal(st, err)
	}
	p.change = func(p *Readiness) { p.BindingRef = ""; p.LaneRef = ""; p.NativeAssignment = "assignment" }
	if _, st, _ := c.Observe(context.Background(), i, nil); st == "running" {
		t.Fatal(st)
	}
}

// Different operation, lineage, reused launcher/runtime PID or pane never resolves
// an old operation. A second matching runtime is ambiguity, not a selection rule.
func TestProcessBindingNegatives(t *testing.T) {
	for _, kind := range []string{"tag", "pane", "launcher_birth", "runtime_birth", "lineage", "duplicate_runtime"} {
		t.Run(kind, func(t *testing.T) {
			c, i, b, _ := curatorFixture()
			b.active = true
			a, _, err := c.bind(i, attemptFromCurator(t, c, i), b.snap)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "tag":
				b.snap.OperationDigest = strings.Repeat("c", 64)
			case "pane":
				b.snap.Pane = "%2"
			case "launcher_birth":
				b.snap.Processes[1].Birth = "reused"
			case "runtime_birth":
				b.snap.Processes[2].Birth = "reused"
			case "lineage":
				b.snap.Processes[1].ParentPID = 999
			case "duplicate_runtime":
				b.snap.Processes = append(b.snap.Processes, Process{103, 101, "birth", "/tool/codex"})
			}
			if _, st, err := c.Observe(context.Background(), i, &a); st != "unknown" || err == nil || b.creates != 0 {
				t.Fatal(st, err)
			}
		})
	}
}
