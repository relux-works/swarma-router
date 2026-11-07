package launchbridge

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"relux.works/swarma-router/handoff"
	"relux.works/swarma-router/launch"
)

// These capabilities are controlled fixtures. They authenticate nothing on a
// host, never execute a2a and never inspect a real member or stored state file.
type acquiredBinding struct {
	expected ExpectedBinding
	witness  launch.Readiness
	original []byte
	err      error
	calls    int
}

func (a *acquiredBinding) Acquire(context.Context, launch.Intent, launch.Snapshot) (AcquiredBinding, error) {
	a.calls++
	return AcquiredBinding{Expected: a.expected, Witness: a.witness, OriginalIntent: a.original}, a.err
}

type controlledAssociation struct {
	t               *testing.T
	intent          launch.Intent
	stored          ExpectedBinding
	localHost, host string // source-side premises, deliberately absent from JSON
	holder          string
	leaseExpires    int64
	now             time.Time
	refuse          string
	mutate          func(map[string]any)
	rawTransform    func([]byte) []byte
	exit            *int
	err             error
	stderr          []byte
	delay           time.Duration
	lockPath        string
	calls           int
	deadline        time.Duration
}

func projection(e ExpectedBinding, stopped bool) map[string]any {
	var stop any
	if stopped {
		stop = "recorded-stop-cause-ambiguous"
	}
	return map[string]any{"generation": e.Generation, "consumer_pid": e.ConsumerPID,
		"consumer_birth_sha256": hashBirth(e.ConsumerBirth), "owner_pid": e.OwnerPID,
		"owner_birth_sha256": hashBirth(e.OwnerBirth), "runtime": e.Runtime,
		"stopped": stopped, "stopped_reason": stop}
}

func (c *controlledAssociation) Exchange(ctx context.Context, frame []byte) ([]byte, []byte, int, error) {
	c.t.Helper()
	c.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > AssociationBudget {
		c.t.Fatal("missing bounded operation deadline")
	}
	c.deadline = time.Until(deadline)
	if len(frame) < 5 || len(frame) > 8196 || int(binary.BigEndian.Uint32(frame)) != len(frame)-4 {
		c.t.Fatal("private framing invalid")
	}
	var q associationRequest
	if err := handoff.Decode(frame[4:], &q); err != nil {
		c.t.Fatal("private schema invalid")
	}
	if q.Schema != AssociationSchema || q.Identity != c.intent.Member || q.Connection != "selected-connection" ||
		q.Digest != c.intent.Digest() || q.Issued != c.now.UnixMilli() || q.Expires <= q.Issued || q.Expires-q.Issued > 30000 {
		c.t.Fatal("private request not bound to complete launch intent/context/time")
	}
	if c.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, nil, -1, ctx.Err()
		case <-time.After(c.delay):
		}
	}
	reason := c.refuse
	// Model the amended source Host/lease rules. This is not an implementation
	// of the source state decoder or evidence of actual native Host equality.
	if reason == "" && (c.host != c.localHost || c.holder != c.stored.SessionID || c.leaseExpires <= c.now.UnixMilli()) {
		reason = "stale_binding"
	}
	if c.lockPath != "" && !fixtureLock(ctx, c.lockPath) {
		reason = "snapshot_unavailable"
	}
	o := map[string]any{"schema": AssociationSchema, "scope": "stored-own-binding", "source": "selected-own-state-snapshot",
		"observed_at_ms": c.now.UnixMilli(), "launch_intent_sha256": q.Digest,
		"result": "match", "reason": "stored_tuple_equal", "stored": projection(c.stored, false), "native_evidence": "not-observed-by-this-interface"}
	exit := 0
	if reason != "" {
		o["result"], o["reason"], o["stored"], exit = "unavailable", reason, nil, 2
		switch reason {
		case "input_untrusted", "input_invalid", "input_expired", "context_unavailable", "clock_unavailable", "privacy_collision":
			o["launch_intent_sha256"] = nil
		case "binding_stopped":
			o["stored"] = projection(c.stored, true)
		}
		if reason == "clock_unavailable" {
			o["observed_at_ms"] = nil
		}
	} else if q.Expected.Generation != c.stored.Generation {
		o["result"], o["reason"], exit = "mismatch", "generation_mismatch", 1
	} else if !reflect.DeepEqual(q.Expected, c.stored) {
		o["result"], o["reason"], exit = "mismatch", "tuple_mismatch", 1
	}
	if c.mutate != nil {
		c.mutate(o)
	}
	if c.exit != nil {
		exit = *c.exit
	}
	raw := append(encode(c.t, o), '\n')
	if c.rawTransform != nil {
		raw = c.rawTransform(raw)
	}
	return raw, c.stderr, exit, c.err
}

// Real flock on a precreated, private fixture inode, opened without O_CREATE.
// Missing/unavailable lock returns no snapshot. It never touches source state.
func fixtureLock(ctx context.Context, path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	started := time.Now()
	for {
		if ctx.Err() != nil || time.Since(started) >= AssociationLockBudget {
			return false
		}
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			unix.Flock(int(f.Fd()), unix.LOCK_UN)
			return true
		} else if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

type bridgeBackend struct {
	snap      launch.Snapshot
	present   bool
	creates   int
	createErr error
}

func (b *bridgeBackend) Snapshot(context.Context, string) (launch.Snapshot, bool, error) {
	return b.snap, b.present, nil
}
func (b *bridgeBackend) Create(context.Context, launch.Intent, []string) error {
	b.creates++
	b.present = true
	return b.createErr
}

type bridgeFixture struct {
	p      Policy
	i      launch.Intent
	events *runner
	now    time.Time
	b      *bridgeBackend
	a      *acquiredBinding
	c      *controlledAssociation
	r      AssociationReady
}

func associationFixture(t *testing.T) bridgeFixture {
	t.Helper()
	p, i, custody, events, now := fixture(t)
	i.NativeSession = "fixture-native-thread"
	custody.IntentDigest = i.Digest()
	setEvent(t, events, p, custody, now)
	s := launch.Snapshot{Session: i.TmuxSession, Pane: "%7", PanePID: 100, OperationDigest: i.Digest(), Processes: []launch.Process{
		{PID: 100, ParentPID: 1, Birth: "fixture-pane-birth", Executable: "/tools/curator"},
		{PID: 101, ParentPID: 100, Birth: "fixture-launcher-birth", Executable: "/tools/curator-run"},
		{PID: 102, ParentPID: 101, Birth: "fixture-owner-birth", Executable: "/tools/codex"},
	}}
	e := ExpectedBinding{Generation: strings.Repeat("a", 32), ConsumerPID: 200, ConsumerBirth: "fixture-consumer-birth",
		OwnerPID: 102, OwnerBirth: "fixture-owner-birth", Runtime: "codex", SessionID: i.NativeSession,
		Target: AssociationTarget{Socket: "/fixture/socket", SocketID: "fixture-socket-id", Pane: "%7", PanePID: 100,
			PaneBirth: "fixture-pane-birth", OwnerPID: 102, OwnerBirth: "fixture-owner-birth", Runtime: "codex"}}
	w := launch.Readiness{TaskID: i.TaskID, Member: i.Member, Session: s.Session, Pane: s.Pane, Runtime: s.Processes[2], BindingRuntime: s.Processes[2],
		CheckedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), Thread: e.SessionID, BindingRef: "fixture-binding-read", LaneRef: "fixture-authenticated-lane-read", LaneSender: "@member:example.test"}
	a := &acquiredBinding{expected: e, witness: w, original: encode(t, i)}
	c := &controlledAssociation{t: t, intent: i, stored: e, host: "fixture-host", localHost: "fixture-host", holder: e.SessionID, leaseExpires: now.Add(time.Minute).UnixMilli(), now: now}
	r := AssociationReady{Identity: i.Member, Server: p.Config.Server, Connection: "selected-connection", Acquire: a, Transport: c, Now: func() time.Time { return now }}
	return bridgeFixture{p, i, events, now, &bridgeBackend{snap: s}, a, c, r}
}

func (f bridgeFixture) assemble(t *testing.T, owner OwnerSource) launch.Router {
	t.Helper()
	r, err := AssembleAssociation(f.p, CLIEvents{f.events, "/workspace"}, f.b, owner, f.r.Acquire, f.r.Transport, f.r.Connection, f.r.Now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func assertReady(t *testing.T, f bridgeFixture) {
	t.Helper()
	w, err := f.r.Read(context.Background(), f.i, f.b.snap)
	if err != nil || w.Thread != f.a.witness.Thread || w.LaneRef != f.a.witness.LaneRef || !w.ExpiresAt.Equal(f.now.Add(30*time.Second)) {
		t.Fatalf("valid neighbor did not retain independently acquired readiness: %v", err)
	}
}

func TestAssociationValidAdjacentPaths(t *testing.T) {
	for _, mode := range []string{"start", "resume"} {
		for _, runtime := range []string{"codex_cli", "claude_code"} {
			t.Run(mode+"/"+runtime, func(t *testing.T) {
				f := associationFixture(t)
				f.i.Mode, f.i.Runtime = mode, runtime
				if mode == "start" {
					f.i.NativeSession = ""
				}
				if runtime == "claude_code" {
					f.a.expected.Runtime, f.a.expected.Target.Runtime = "claude", "claude"
					f.b.snap.Processes[2].Executable = "/tools/claude"
					f.a.witness.Runtime, f.a.witness.BindingRuntime = f.b.snap.Processes[2], f.b.snap.Processes[2]
				}
				f.c.intent, f.c.stored = f.i, f.a.expected
				f.a.original = encode(t, f.i)
				f.b.snap.OperationDigest = f.i.Digest()
				assertReady(t, f)
			})
		}
	}
}

func TestAssociationRefusalsReturnNoEvidence(t *testing.T) {
	for _, reason := range []string{"input_untrusted", "input_invalid", "input_expired", "context_unavailable", "clock_unavailable", "privacy_collision", "budget_exceeded", "snapshot_unavailable", "binding_missing", "binding_invalid", "stale_binding", "binding_stopped", "generation_mismatch", "tuple_mismatch"} {
		t.Run(reason, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			f.c.refuse = reason
			if reason == "generation_mismatch" {
				f.c.refuse, f.c.stored.Generation = "", strings.Repeat("b", 32)
			}
			if reason == "tuple_mismatch" {
				f.c.refuse, f.c.stored.Target.Socket = "", "/fixture/other-socket"
			}
			w, err := f.r.Read(context.Background(), f.i, f.b.snap)
			if err == nil || err.Error() != "association_"+reason || w != (launch.Readiness{}) {
				t.Fatalf("refusal produced evidence or wrong reason: %v", err)
			}
			r := f.assemble(t, &owner{})
			if got := r.Start(context.Background(), f.i); got.Status != "attempted" || f.b.creates != 1 {
				t.Fatalf("controlled Start: %s", got.Status)
			}
			if got := r.Observe(context.Background(), f.i); got.Status != "pending" || f.b.creates != 1 {
				t.Fatalf("refusal became running/effect: %s", got.Status)
			}
		})
	}
}

func TestIndependentWitnessRequired(t *testing.T) {
	for _, neighbor := range []string{"missing", "error", "task", "member", "session", "pane", "runtime-birth", "binding-birth", "thread", "binding-ref", "lane-ref", "sender", "sender-server", "future", "expired", "handoff", "target-pane-birth", "target-owner", "missing-pane", "duplicate-owner", "digest", "identity", "generation", "invalid-pid", "oversize-socket", "invalid-utf8"} {
		t.Run(neighbor, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			calls := f.c.calls
			switch neighbor {
			case "missing":
				f.r.Acquire = nil
			case "error":
				f.a.err = errors.New("fixture-private-error")
			case "task":
				f.a.witness.TaskID = "TASK-260101-other"
			case "member":
				f.a.witness.Member = "other"
			case "session":
				f.a.witness.Session = "other"
			case "pane":
				f.a.witness.Pane = "%8"
			case "runtime-birth":
				f.a.witness.Runtime.Birth = "replaced-owner"
			case "binding-birth":
				f.a.witness.BindingRuntime.Birth = "replaced-owner"
			case "thread":
				f.a.witness.Thread = "foreign-native-thread"
			case "binding-ref":
				f.a.witness.BindingRef = ""
			case "lane-ref":
				f.a.witness.LaneRef = ""
			case "sender":
				f.a.witness.LaneSender = "@other:example.test"
			case "sender-server":
				f.a.witness.LaneSender = "@member:other.test"
			case "future":
				f.a.witness.CheckedAt = f.now.Add(time.Second)
			case "expired":
				f.a.witness.ExpiresAt = f.now
			case "handoff":
				f.a.witness.Handoff = "unexpected-native-handoff"
			case "target-pane-birth":
				f.a.expected.Target.PaneBirth = "replaced-pane"
			case "target-owner":
				f.a.expected.Target.OwnerPID++
			case "missing-pane":
				f.b.snap.Processes = f.b.snap.Processes[1:]
			case "duplicate-owner":
				f.b.snap.Processes = append(f.b.snap.Processes, f.b.snap.Processes[2])
			case "digest":
				f.b.snap.OperationDigest = strings.Repeat("b", 64)
			case "identity":
				f.r.Identity = "other"
			case "generation":
				f.a.expected.Generation = "bad"
			case "invalid-pid":
				f.a.expected.ConsumerPID = 0
			case "oversize-socket":
				f.a.expected.Target.Socket = strings.Repeat("x", 513)
			case "invalid-utf8":
				f.a.expected.ConsumerBirth = string([]byte{0xff})
			}
			w, err := f.r.Read(context.Background(), f.i, f.b.snap)
			if err == nil || w != (launch.Readiness{}) || f.c.calls != calls || strings.Contains(err.Error(), "fixture-private") {
				t.Fatalf("stored match substituted for independent witness: %v calls=%d", err, f.c.calls-calls)
			}
		})
	}
}

func TestClosedAssociationOutput(t *testing.T) {
	for _, neighbor := range []string{"schema", "scope", "native", "digest", "missing-null", "extra", "case-alias", "stored-extra", "stored-missing-false", "stored-null", "stored-pid", "stored-birth", "stored-generation", "observed-future", "observed-expired", "observed-null", "reason", "exit", "stderr", "tool-error", "null-reason"} {
		t.Run(neighbor, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			f.c.mutate = func(o map[string]any) {
				s := o["stored"].(map[string]any)
				switch neighbor {
				case "schema":
					o["schema"] = "future/2"
				case "scope":
					o["scope"] = "native-current"
				case "native":
					o["native_evidence"] = "absent"
				case "digest":
					o["launch_intent_sha256"] = strings.Repeat("b", 64)
				case "missing-null":
					delete(s, "stopped_reason")
				case "extra":
					o["host"] = "forbidden-protocol-addition"
				case "case-alias":
					o["Schema"] = o["schema"]
					delete(o, "schema")
				case "stored-extra":
					s["session_id"] = "forbidden-private-string"
				case "stored-missing-false":
					delete(s, "stopped")
				case "stored-null":
					o["stored"] = nil
				case "stored-pid":
					s["owner_pid"] = 103
				case "stored-birth":
					s["consumer_birth_sha256"] = strings.Repeat("b", 64)
				case "stored-generation":
					s["generation"] = strings.Repeat("b", 32)
				case "observed-future":
					o["observed_at_ms"] = f.now.Add(time.Second).UnixMilli()
				case "observed-expired":
					o["observed_at_ms"] = f.now.Add(30 * time.Second).UnixMilli()
				case "observed-null":
					o["observed_at_ms"] = nil
				case "reason":
					o["reason"] = "unknown"
				case "null-reason":
					o["reason"] = nil
				}
			}
			switch neighbor {
			case "exit":
				n := 1
				f.c.exit = &n
			case "stderr":
				f.c.stderr = []byte("fixture-private-diagnostic")
			case "tool-error":
				f.c.err = errors.New("fixture-private-diagnostic")
			}
			w, err := f.r.Read(context.Background(), f.i, f.b.snap)
			if err == nil || w != (launch.Readiness{}) || strings.Contains(err.Error(), "fixture-private") {
				t.Fatalf("malformed source became readiness: %v", err)
			}
		})
	}
}

func TestAssociationHostLeaseAndLock(t *testing.T) {
	for _, neighbor := range []string{"other-host", "pid-holder", "expired-lease", "missing-lock", "held-lock"} {
		t.Run(neighbor, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			want := "association_stale_binding"
			switch neighbor {
			case "other-host":
				f.c.host = "other-host"
			case "pid-holder":
				f.c.holder = "pid:200:fixture-consumer-birth"
			case "expired-lease":
				f.c.leaseExpires = f.now.UnixMilli()
			default:
				want = "association_snapshot_unavailable"
				f.c.lockPath = filepath.Join(t.TempDir(), "own.lock")
				file, err := os.OpenFile(f.c.lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				assertReady(t, f) // exact adjacent preexisting, unlocked inode
				if neighbor == "held-lock" {
					if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
						t.Fatal(err)
					}
					defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
				} else {
					if err := file.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(f.c.lockPath); err != nil {
						t.Fatal(err)
					}
				}
			}
			start := time.Now()
			w, err := f.r.Read(context.Background(), f.i, f.b.snap)
			elapsed := time.Since(start)
			if err == nil || err.Error() != want || w != (launch.Readiness{}) {
				t.Fatalf("unavailable became evidence: %v", err)
			}
			if elapsed > AssociationBudget || f.c.deadline > AssociationBudget {
				t.Fatal("operation budget exceeded")
			}
			if neighbor == "missing-lock" {
				if _, err := os.Stat(f.c.lockPath); !os.IsNotExist(err) {
					t.Fatal("missing lock was created")
				}
			}
			if neighbor == "held-lock" {
				t.Logf("fixture lock refusal elapsed=%s; retry budget=%s", elapsed, AssociationLockBudget)
				if elapsed < AssociationLockBudget {
					t.Fatal("lock control did not exercise retry bound")
				}
			}
		})
	}
}

func TestAssociationBudgetExpiryAndPrivacy(t *testing.T) {
	for _, neighbor := range []string{"deadline", "expiry", "rollback", "expiry-at-delivery", "rollback-at-delivery", "observation-collision", "missing-transport", "cancelled"} {
		t.Run(neighbor, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			ctx := context.Background()
			switch neighbor {
			case "deadline":
				f.c.delay = AssociationBudget + time.Second
			case "expiry", "rollback", "expiry-at-delivery", "rollback-at-delivery":
				calls := 0
				cutoff := 2
				if strings.HasSuffix(neighbor, "at-delivery") {
					cutoff = 3
				}
				f.r.Now = func() time.Time {
					calls++
					if calls > cutoff {
						if strings.HasPrefix(neighbor, "expiry") {
							return f.now.Add(30 * time.Second)
						}
						return f.now.Add(-time.Millisecond)
					}
					return f.now
				}
			case "observation-collision":
				// Use the exact observation token, exercising numeric formatting.
				f.a.expected.ConsumerBirth = strconv.FormatInt(f.now.UnixMilli(), 10)
				f.c.stored = f.a.expected
			case "missing-transport":
				f.r.Transport = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			w, err := f.r.Read(ctx, f.i, f.b.snap)
			if err == nil || w != (launch.Readiness{}) {
				t.Fatalf("budget/time/privacy bypass: %v", err)
			}
			if neighbor == "observation-collision" && err.Error() != "association_privacy_collision" {
				t.Fatalf("wrong collision reason: %v", err)
			}
		})
	}
}

func TestAssembledBridgeOneStartUnknownReplay(t *testing.T) {
	f := associationFixture(t)
	f.b.createErr = errors.New("controlled lost reply")
	f.c.refuse = "snapshot_unavailable"
	o := &owner{}
	r := f.assemble(t, o)
	if got := r.Prepare(f.i); got.Status != "prepared" || o.calls != 0 || f.c.calls != 0 || len(f.events.calls) != 0 {
		t.Fatal("Prepare acquired sources")
	}
	first := r.Start(context.Background(), f.i)
	if first.Status != "unknown" || f.b.creates != 1 {
		t.Fatalf("lost reply: %s", first.Status)
	}
	// Destroy source availability after uncertainty; retained replay must not
	// consult it or make another Start. Observe also has no Start path.
	f.events.fail = "members"
	o.err = errors.New("source now unavailable")
	for n := 0; n < 3; n++ {
		if got := r.Start(context.Background(), f.i); got.Status != "unknown" || !got.Historical || f.b.creates != 1 || o.calls != 1 {
			t.Fatalf("uncertainty replay: %s", got.Status)
		}
	}
	observed := r.Observe(context.Background(), f.i)
	if observed.Status != "pending" || f.b.creates != 1 {
		t.Fatalf("uncertainty observation: %s", observed.Status)
	}
	// Observe may reconcile metadata to pending; a replay retains that result
	// and still cannot turn it into a second launch attempt.
	if got := r.Start(context.Background(), f.i); got.Status != "pending" || !got.Historical || f.b.creates != 1 || o.calls != 1 {
		t.Fatalf("post-observation replay: %s", got.Status)
	}
	files, err := filepath.Glob(filepath.Join(f.p.JournalDir, "launch-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("original uncertainty record not retained")
	}
}

func TestAssembledBridgeValidAndUnavailableSources(t *testing.T) {
	for _, neighbor := range []string{"valid", "owner-missing", "ready-missing", "native-command-unavailable"} {
		t.Run(neighbor, func(t *testing.T) {
			f := associationFixture(t)
			var source OwnerSource = &owner{}
			switch neighbor {
			case "owner-missing":
				source = nil
			case "ready-missing":
				f.r.Acquire = nil
			case "native-command-unavailable":
				f.r.Transport = nil
			}
			r := f.assemble(t, source)
			first := r.Start(context.Background(), f.i)
			if neighbor == "owner-missing" {
				if first.Status != "refused" || f.b.creates != 0 {
					t.Fatal("missing owner authorized Start")
				}
				return
			}
			if first.Status != "attempted" || f.b.creates != 1 {
				t.Fatalf("Start: %s", first.Status)
			}
			got := r.Observe(context.Background(), f.i)
			want := "pending"
			if neighbor == "valid" {
				want = "running"
			}
			if got.Status != want || f.b.creates != 1 {
				t.Fatalf("Observe: %s want %s", got.Status, want)
			}
		})
	}
}

func TestAssociationRawOutputBoundaries(t *testing.T) {
	for _, plant := range []string{"duplicate", "nested-duplicate", "suffix", "missing-lf", "oversize", "invalid-utf8", "numeric-exponent", "numeric-fraction", "partial-json"} {
		t.Run(plant, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			f.c.rawTransform = func(raw []byte) []byte {
				switch plant {
				case "duplicate":
					return append([]byte(`{"schema":"shadow",`), raw[1:]...)
				case "nested-duplicate":
					return []byte(strings.Replace(string(raw), `"stored":{`, `"stored":{"runtime":"claude",`, 1))
				case "suffix":
					return append(raw, []byte("{}\n")...)
				case "missing-lf":
					return raw[:len(raw)-1]
				case "oversize":
					return append([]byte(strings.Repeat(" ", 2048)), raw...)
				case "invalid-utf8":
					return append([]byte{0xff}, raw...)
				case "numeric-exponent":
					return []byte(strings.Replace(string(raw), `"owner_pid":102`, `"owner_pid":102e0`, 1))
				case "numeric-fraction":
					return []byte(strings.Replace(string(raw), `"owner_pid":102`, `"owner_pid":102.0`, 1))
				default:
					return raw[:len(raw)/2]
				}
			}
			w, err := f.r.Read(context.Background(), f.i, f.b.snap)
			if err == nil || err.Error() != "association_output_invalid" || w != (launch.Readiness{}) {
				t.Fatalf("invalid wire projection accepted: %v", err)
			}
		})
	}
}

func TestCompleteOriginalIntentCustody(t *testing.T) {
	for _, neighbor := range []string{"missing", "tuple-only", "reserialized", "different-model", "oversize"} {
		t.Run(neighbor, func(t *testing.T) {
			f := associationFixture(t)
			assertReady(t, f)
			calls := f.c.calls
			switch neighbor {
			case "missing":
				f.a.original = nil
			case "tuple-only":
				f.a.original = encode(t, f.a.expected)
			case "reserialized":
				f.a.original = append([]byte(" "), f.a.original...)
			case "different-model":
				other := f.i
				other.Model = "other-model"
				f.a.original = encode(t, other)
			case "oversize":
				f.a.original = []byte(strings.Repeat("x", MaxProjectionBytes+1))
			}
			w, err := f.r.Read(context.Background(), f.i, f.b.snap)
			if err == nil || w != (launch.Readiness{}) || f.c.calls != calls {
				t.Fatal("original intent replaced by a reconstruction or tuple")
			}
		})
	}
}

func TestGuardAcquisitionClockBounds(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(strconv.FormatBool(rollback), func(t *testing.T) {
			f := associationFixture(t)
			clock := func() time.Time {
				if rollback {
					return f.now.Add(-time.Millisecond)
				}
				return f.now.Add(time.Millisecond)
			}
			r, err := Assemble(f.p, CLIEvents{f.events, "/workspace"}, f.b, &owner{}, nil, clock)
			if err != nil {
				t.Fatal(err)
			}
			a, err := r.Guard.Check(context.Background(), f.i, f.i.Digest(), f.now)
			if rollback {
				if err == nil || a != (launch.Admission{}) {
					t.Fatal("rollback admitted custody")
				}
			} else if err != nil || !a.CheckedAt.Equal(f.now) || a.State != "stopped" {
				t.Fatalf("acquisition completion changed caller cutoff: %v", err)
			}
		})
	}
}
