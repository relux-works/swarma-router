package launchbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"relux.works/swarma-router/launch"
)

type runner struct {
	members, event []byte
	fail           string
	calls          [][]string
}

func (r *runner) Run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{dir, name}, args...))
	if name != "a2a" {
		return nil, errors.New("unexpected tool")
	}
	if args[0] == "room" {
		if r.fail == "members" {
			return nil, errors.New("private diagnostic")
		}
		return r.members, nil
	}
	if args[0] == "read" {
		if r.fail == "event" {
			return nil, errors.New("private diagnostic")
		}
		return r.event, nil
	}
	return nil, errors.New("unexpected effect")
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func fixture(t *testing.T) (Policy, launch.Intent, Custody, *runner, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Manager: "manager", Project: "example", JournalDir: filepath.Join(parent, "journal"),
		Event:   EventRef{Server: "example.test", Caller: "@caller:example.test", RoomID: "!room:example.test", Topic: "launch", Root: "$root", EventID: "$custody", Sender: "@manager:example.test", Kind: "handover", ReplyTo: "$request"},
		Config:  launch.CuratorConfig{Executable: "/tools/curator", Home: "/home/user", Path: "/usr/bin:/bin", TempDir: "/tmp", Permissions: "yolo", Network: "proxy", Agent: "member", Server: "example.test", A2AConfigDir: "/home/user/a2a"},
		ReadSet: []string{"exact-event", "exact-session", "owner-projection"}, WriteSet: []string{"exact-journal", "exact-session-create"}}
	p.WriteSet = append(p.WriteSet, p.JournalDir)
	i := launch.Intent{Contract: launch.Contract, CorrelationID: "fixture-operation", Project: p.Project, TaskID: "TASK-260101-abc", Member: "member", Manager: p.Manager, Plugin: "curator/0.1", Runtime: "codex_cli", Profile: "role", ProfileLock: strings.Repeat("a", 64), Model: "model", Effort: "high", Workspace: "/workspace", TmuxSession: "member-session", Mode: "resume", NativeSession: "thread"}
	i.PluginConfig = (launch.Curator{Config: p.Config}).Configuration()
	c := Custody{Contract: CustodyContract, IntentDigest: i.Digest(), Configuration: i.PluginConfig, ProfileLock: i.ProfileLock, TaskID: i.TaskID, Project: i.Project, Manager: i.Manager, Member: i.Member, Session: i.TmuxSession, Authority: "admitted", Holds: []string{}, ReadSet: append([]string{}, p.ReadSet...), WriteSet: append([]string{}, p.WriteSet...), CheckedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(30 * time.Minute).Format(time.RFC3339Nano)}
	r := &runner{members: []byte(`{"type":"room-members","room_id":"!room:example.test","caller":"@caller:example.test","membership_filter":"join","completeness":"complete-response","members":[{"user_id":"@caller:example.test","membership":"join"},{"user_id":"@manager:example.test","membership":"join"}]}`)}
	setEvent(t, r, p, c, now)
	return p, i, c, r, now
}

func setEvent(t *testing.T, r *runner, p Policy, c Custody, at time.Time) {
	t.Helper()
	r.event = encode(t, map[string]any{"type": "message", "room": "#example:example.test", "room_id": p.Event.RoomID, "event_id": p.Event.EventID, "sender": "manager", "at": at, "kind": p.Event.Kind, "topic": p.Event.Topic, "root": p.Event.Root, "to": []string{"caller", "member"}, "re": p.Event.ReplyTo, "subject": "example", "body": string(encode(t, c))})
}

// These cases establish the accepted current CLI forms and exact read arguments;
// controlled responses are not real custody or live identity evidence.
func TestCurrentEventForms(t *testing.T) {
	for _, full := range []bool{false, true} {
		p, i, _, r, now := fixture(t)
		if full {
			var e map[string]any
			json.Unmarshal(r.event, &e)
			e["sender"] = p.Event.Sender
			e["to"] = []string{p.Event.Caller, "@member:example.test"}
			e["attachment"] = nil
			e["room"] = p.Event.RoomID
			r.event = encode(t, e)
		}
		c, err := (CLIEvents{r, "/workspace"}).read(context.Background(), p.Event, now)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.validate(i, p, now); err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"/workspace", "a2a", "room", "members", p.Event.RoomID, "--membership", "join", "--json"}, {"/workspace", "a2a", "read", p.Event.RoomID, "--event", p.Event.EventID, "--json"}}
		if !reflect.DeepEqual(r.calls, want) {
			t.Fatalf("unexpected tool calls: %v", r.calls)
		}
	}
}

// A nearby valid fixture above prevents an always-refusing reader from passing;
// each neighbor must fail for the intended binding or syntax reason.
func TestEventRefusals(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(map[string]any)
	}{
		{"sender", "event_mismatch", func(e map[string]any) { e["sender"] = "other" }},
		{"other-server", "event_mismatch", func(e map[string]any) { e["sender"] = "@manager:other.test" }},
		{"room", "event_mismatch", func(e map[string]any) { e["room_id"] = "!other:example.test" }},
		{"event", "event_mismatch", func(e map[string]any) { e["event_id"] = "$other" }},
		{"root", "event_mismatch", func(e map[string]any) { e["root"] = "$other" }},
		{"topic", "event_mismatch", func(e map[string]any) { e["topic"] = "other" }},
		{"reply", "event_mismatch", func(e map[string]any) { e["re"] = "$other" }},
		{"kind", "event_mismatch", func(e map[string]any) { e["kind"] = "notice" }},
		{"future", "event_mismatch", func(e map[string]any) {
			at, _ := time.Parse(time.RFC3339, e["at"].(string))
			e["at"] = at.Add(time.Second)
		}},
		{"not-addressed", "event_not_addressed", func(e map[string]any) { e["to"] = []string{"member"} }},
		{"duplicate-recipient", "event_recipients_invalid", func(e map[string]any) { e["to"] = []string{"caller", "@caller:example.test"} }},
		{"attachment", "event_mismatch", func(e map[string]any) { e["attachment"] = map[string]any{"name": "receipt"} }},
		{"unknown-key", "event_unavailable", func(e map[string]any) { e["grant"] = true }},
		{"case-shadow", "event_unavailable", func(e map[string]any) { e["Sender"] = "manager" }},
		{"body-alias", "custody_invalid", func(e map[string]any) { e["body"] = `{"Contract":"router-launch-custody/0.1.0"}` }},
		{"disclosure", "custody_invalid", func(e map[string]any) {
			var body map[string]any
			json.Unmarshal([]byte(e["body"].(string)), &body)
			body["native_thread"] = "must-remain-private"
			e["body"] = string(encode(t, body))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _, r, now := fixture(t)
			var e map[string]any
			json.Unmarshal(r.event, &e)
			tc.mutate(e)
			r.event = encode(t, e)
			_, err := (CLIEvents{r, "/workspace"}).read(context.Background(), p.Event, now)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
		})
	}
	for _, plant := range []string{"duplicate-key", "oversized", "member-caller", "member-incomplete", "member-duplicate", "member-source-absent", "tool-members", "tool-event"} {
		t.Run(plant, func(t *testing.T) {
			p, _, _, r, now := fixture(t)
			want := "event_unavailable"
			switch plant {
			case "duplicate-key":
				r.event = append([]byte(`{"sender":"wrong",`), r.event[1:]...)
			case "oversized":
				r.event = []byte(strings.Repeat(" ", MaxProjectionBytes+1))
			case "tool-members":
				r.fail = "members"
				want = "membership_unavailable"
			case "tool-event":
				r.fail = "event"
			default:
				var m map[string]any
				json.Unmarshal(r.members, &m)
				switch plant {
				case "member-caller":
					m["caller"] = "@other:example.test"
					want = "membership_unavailable"
				case "member-incomplete":
					m["completeness"] = "partial"
					want = "membership_unavailable"
				case "member-duplicate":
					m["members"] = []map[string]string{{"user_id": "@caller:example.test", "membership": "join"}, {"user_id": "@caller:example.test", "membership": "join"}}
					want = "membership_invalid"
				case "member-source-absent":
					m["members"] = []map[string]string{{"user_id": "@caller:example.test", "membership": "join"}}
					want = "source_not_joined"
				}
				r.members = encode(t, m)
			}
			_, err := (CLIEvents{r, "/workspace"}).read(context.Background(), p.Event, now)
			if err == nil || err.Error() != want {
				t.Fatalf("got %v want %s", err, want)
			}
		})
	}
}

// Exact custody binds each authority/scope/time neighbor independently of JSON
// parsing; source unavailability cannot be interpreted as an empty hold list.
func TestCustodyRefusals(t *testing.T) {
	for _, field := range []string{"contract", "digest", "configuration", "lock", "task", "project", "manager", "member", "session", "thread", "authority", "holds", "missing-holds", "read-set", "write-set", "future", "expired", "zero-time"} {
		t.Run(field, func(t *testing.T) {
			p, i, c, r, now := fixture(t)
			want := "custody_scope_mismatch"
			switch field {
			case "contract":
				c.Contract = "other"
			case "digest":
				c.IntentDigest = strings.Repeat("b", 64)
			case "configuration":
				c.Configuration = strings.Repeat("b", 64)
			case "lock":
				c.ProfileLock = strings.Repeat("b", 64)
			case "task":
				c.TaskID = "TASK-260101-other"
			case "project":
				c.Project = "other"
			case "manager":
				c.Manager = "other"
			case "member":
				c.Member = "other"
			case "session":
				c.Session = "other"
			case "thread":
				i.NativeSession = "other-private-thread"
			case "authority":
				c.Authority = "unknown"
				want = "authority_unavailable"
			case "holds":
				c.Holds = []string{"hold"}
				want = "authority_unavailable"
			case "missing-holds":
				c.Holds = nil
				want = "authority_unavailable"
			case "read-set":
				c.ReadSet = []string{"other"}
				want = "custody_access_mismatch"
			case "write-set":
				c.WriteSet = []string{"other"}
				want = "custody_access_mismatch"
			case "future":
				c.CheckedAt = now.Add(time.Second).Format(time.RFC3339Nano)
				want = "custody_expired_or_future"
			case "expired":
				c.ExpiresAt = now.Format(time.RFC3339Nano)
				want = "custody_expired_or_future"
			case "zero-time":
				c.CheckedAt = ""
				want = "custody_expired_or_future"
			}
			if err := c.validate(i, p, now); err == nil || err.Error() != want {
				t.Fatalf("got %v want %s", err, want)
			}
			setEvent(t, r, p, c, now)
			b := &backend{}
			router, err := Assemble(p, CLIEvents{r, "/workspace"}, b, &owner{}, nil, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result := router.Start(context.Background(), i)
			if result.Status != "refused" || result.Reason != "admission_invalid" || b.creates != 0 {
				t.Fatalf("invalid custody reached effect: %+v creates=%d", result, b.creates)
			}
		})
	}
}

type backend struct {
	present bool
	err     error
	creates int
}

func (b *backend) Snapshot(context.Context, string) (launch.Snapshot, bool, error) {
	return launch.Snapshot{}, b.present, b.err
}
func (b *backend) Create(context.Context, launch.Intent, []string) error { b.creates++; return nil }

type owner struct {
	err   error
	calls int
}

func (o *owner) Check(context.Context, launch.Intent, Custody) error { o.calls++; return o.err }

// Pure assembly and Prepare never open a journal or acquire sources. A missing
// owner source and failed direct observations cannot yield Admission or Create.
func TestAssemblyAndGuard(t *testing.T) {
	p, i, _, r, now := fixture(t)
	b := &backend{}
	o := &owner{}
	router, err := Assemble(p, CLIEvents{r, "/workspace"}, b, o, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if got := router.Prepare(i); got.Status != "prepared" {
		t.Fatalf("prepare: %+v", got)
	}
	if len(r.calls) != 0 || o.calls != 0 || b.creates != 0 {
		t.Fatal("pure preparation acquired sources")
	}
	if _, err := os.Stat(p.JournalDir); !os.IsNotExist(err) {
		t.Fatalf("journal created: %v", err)
	}
	a, err := router.Guard.Check(context.Background(), i, i.Digest(), now)
	if err != nil || a.State != "stopped" || a.CheckedAt != now || a.ExpiresAt.IsZero() {
		t.Fatalf("controlled guard: %+v %v", a, err)
	}
	for _, neighbor := range []string{"owner-missing", "owner-error", "live", "unknown", "expired-during-acquisition", "cancelled"} {
		t.Run(neighbor, func(t *testing.T) {
			p, i, c, r, now := fixture(t)
			b := &backend{}
			o := &owner{}
			var source OwnerSource = o
			clock := func() time.Time { return now }
			want := "owner_source_unknown"
			ctx := context.Background()
			switch neighbor {
			case "owner-missing":
				source = nil
				want = "owner_source_unavailable"
			case "owner-error":
				o.err = errors.New("unknown")
			case "live":
				b.present = true
				want = "session_live"
			case "unknown":
				b.err = errors.New("unavailable")
				want = "session_unknown"
			case "expired-during-acquisition":
				n := 0
				clock = func() time.Time {
					n++
					if n > 1 {
						expires, _ := time.Parse(time.RFC3339Nano, c.ExpiresAt)
						return expires
					}
					return now
				}
				want = "custody_expired_or_cancelled"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = "custody_expired_or_cancelled"
			}
			router, err := Assemble(p, CLIEvents{r, "/workspace"}, b, source, nil, clock)
			if err != nil {
				t.Fatal(err)
			}
			_, err = router.Guard.Check(ctx, i, i.Digest(), now)
			if err == nil || err.Error() != want {
				t.Fatalf("got %v want %s", err, want)
			}
			if b.creates != 0 {
				t.Fatal("unexpected Create")
			}
			result := router.Start(ctx, i)
			if result.Status != "refused" || b.creates != 0 {
				t.Fatalf("invalid source reached effect: %+v creates=%d", result, b.creates)
			}
			files, err := filepath.Glob(filepath.Join(p.JournalDir, "launch-*.json"))
			if err != nil || len(files) != 0 {
				t.Fatalf("forbidden attempt receipt: %v %v", files, err)
			}
		})
	}
}

// A real isolated FileStore retains one uncertain attempt; Observe through the
// assembled router never calls Create again. This does not qualify live sources.
func TestOneAttemptThenObserve(t *testing.T) {
	p, i, _, r, now := fixture(t)
	b := &backend{}
	o := &owner{}
	router, err := Assemble(p, CLIEvents{r, "/workspace"}, b, o, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	first := router.Start(context.Background(), i)
	if first.Status != "unknown" || b.creates != 1 {
		t.Fatalf("first: %+v creates=%d", first, b.creates)
	}
	observed := router.Observe(context.Background(), i)
	if observed.Status == "running" || b.creates != 1 {
		t.Fatalf("observe: %+v creates=%d", observed, b.creates)
	}
	files, err := filepath.Glob(filepath.Join(p.JournalDir, "launch-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("retained records: %v %v", files, err)
	}
}

// A matching envelope cannot authorize writes through a constructor whose
// actual journal directory is outside its independently pinned write set.
func TestJournalMustBeInWriteSet(t *testing.T) {
	p, _, _, r, now := fixture(t)
	p.JournalDir = filepath.Join(filepath.Dir(p.JournalDir), "different-journal")
	_, err := Assemble(p, CLIEvents{r, "/workspace"}, &backend{}, &owner{}, nil, func() time.Time { return now })
	if err == nil || err.Error() != "embedding_unavailable" {
		t.Fatalf("unbound journal: %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatal("invalid assembly acquired sources")
	}
	if _, err := os.Stat(p.JournalDir); !os.IsNotExist(err) {
		t.Fatalf("invalid assembly wrote journal: %v", err)
	}
}

// Public custody binds the private native session through the full intent
// digest, without serializing the session or allowing another custody kind.
func TestPrivateSessionAndHandover(t *testing.T) {
	p, i, c, r, now := fixture(t)
	i.NativeSession = "private-native-session"
	c.IntentDigest = i.Digest()
	setEvent(t, r, p, c, now)
	if strings.Contains(string(r.event), "native_thread") || strings.Contains(string(r.event), i.NativeSession) {
		t.Fatal("public event disclosed private native session")
	}
	got, err := (CLIEvents{r, "/workspace"}).read(context.Background(), p.Event, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.validate(i, p, now); err != nil {
		t.Fatal(err)
	}
	i.NativeSession = "different-private-session"
	if err := got.validate(i, p, now); err == nil || err.Error() != "custody_scope_mismatch" {
		t.Fatalf("private session not bound: %v", err)
	}
	for _, kind := range []string{"", "decision", "answer", "notice"} {
		p.Event.Kind = kind
		calls := len(r.calls)
		_, err := Assemble(p, CLIEvents{r, "/workspace"}, &backend{}, &owner{}, nil, func() time.Time { return now })
		if err == nil || err.Error() != "embedding_unavailable" {
			t.Fatalf("non-handover %q: %v", kind, err)
		}
		if len(r.calls) != calls {
			t.Fatal("invalid custody kind acquired sources")
		}
	}
}
