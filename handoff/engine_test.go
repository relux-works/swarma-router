package handoff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inputs() (Request, Policy) {
	r := Request{Contract: Contract, Action: "dispatch", CorrelationID: "request-1", Project: "example", TaskID: "TASK-261031-example", TaskTitle: "TASK-261031-example: Example task", Member: "recipient", Room: "example", Topic: "work", ReplyTo: "$reply"}
	p := Policy{Contract: Contract, Project: "example", ProjectRoot: "/example", BoardDir: "/example/.task-board", Server: "example.test", Sender: "caller", Room: "example", RoomID: "!room", Topic: "work", ReplyTo: "$reply", Tasks: []TaskBinding{{ID: r.TaskID, Title: r.TaskTitle, Members: []string{"recipient", "other"}}}}
	return r, p
}

func realTemp(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeAdapter struct {
	checks, posts, reads      int
	checkReason, verifyReason string
	postError                 error
	beforePost                func()
}

func (a *fakeAdapter) ValidateSources(context.Context, Request, Policy) string {
	a.checks++
	return a.checkReason
}
func (a *fakeAdapter) Post(context.Context, Request, Policy, string) (string, error) {
	a.posts++
	if a.beforePost != nil {
		a.beforePost()
	}
	return "$sent", a.postError
}
func (a *fakeAdapter) VerifyEvent(context.Context, Request, Policy, string, string) string {
	a.reads++
	return a.verifyReason
}

// Plan validates without creating a journal or posting; dispatch persists intent before
// its one post, and matching replay returns the historical receipt without another call.
func TestPlanDispatchReplay(t *testing.T) {
	r, p := inputs()
	a := &fakeAdapter{}
	e := Engine{Adapter: a}
	dir := filepath.Join(realTemp(t), "journal")
	r.Action = "plan"
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "planned" || got.Notice == "" {
		t.Fatalf("plan: %+v", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plan wrote journal: %v", err)
	}
	if a.posts != 0 {
		t.Fatal("plan posted")
	}
	r.Action = "dispatch"
	a.beforePost = func() {
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		if len(files) != 1 {
			t.Fatal("intent absent before post")
		}
		b, err := ReadFile(files[0], true)
		var rec Record
		if err != nil || Decode(b, &rec) != nil || rec.Phase != "intent" || rec.Request != r {
			t.Fatal("incorrect pre-call intent")
		}
	}
	for i := 0; i < 2; i++ {
		if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" || got.EventID != "$sent" {
			t.Fatalf("dispatch/replay: %+v", got)
		}
	}
	if a.posts != 1 || a.reads != 1 || a.checks != 2 {
		t.Fatalf("calls: %+v", a)
	}
}

// Each malformed/unauthorized variant refuses before adapter validation or send;
// the nearby unmodified request proves the configured path is accepted.
func TestIntentValidationAndConflict(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*Request, *Policy)
		reason string
	}{
		{"action", func(r *Request, p *Policy) { r.Action = "launch" }, "invalid_request"},
		{"malformed-task", func(r *Request, p *Policy) { r.TaskID = "TASK); other()" }, "invalid_request"},
		{"unknown-task", func(r *Request, p *Policy) { r.TaskID = "TASK-261031-unknown" }, "task_not_authorized"},
		{"title", func(r *Request, p *Policy) { r.TaskTitle = "another title" }, "task_binding_mismatch"},
		{"project", func(r *Request, p *Policy) { r.Project = "other" }, "destination_not_authorized"},
		{"room", func(r *Request, p *Policy) { r.Room = "other" }, "destination_not_authorized"},
		{"topic", func(r *Request, p *Policy) { r.Topic = "other" }, "destination_not_authorized"},
		{"reply", func(r *Request, p *Policy) { r.ReplyTo = "$other" }, "destination_not_authorized"},
		{"member", func(r *Request, p *Policy) { r.Member = "unknown" }, "member_not_authorized"},
		{"control-title", func(r *Request, p *Policy) { r.TaskTitle = "private\nmore" }, "invalid_request"},
		{"policy-server", func(r *Request, p *Policy) { p.Server = "@bad" }, "invalid_policy"},
		{"policy-room-id", func(r *Request, p *Policy) { p.RoomID = "room" }, "invalid_policy"},
		{"duplicate-policy-member", func(r *Request, p *Policy) { p.Tasks[0].Members = []string{"recipient", "recipient"} }, "invalid_policy"},
		{"policy-members-cap", func(r *Request, p *Policy) {
			p.Tasks[0].Members = nil
			for i := 0; i < 65; i++ {
				p.Tasks[0].Members = append(p.Tasks[0].Members, fmt.Sprintf("member%d", i))
			}
		}, "invalid_policy"},
		{"policy-tasks-cap", func(r *Request, p *Policy) {
			for i := 0; i < 256; i++ {
				p.Tasks = append(p.Tasks, TaskBinding{ID: fmt.Sprintf("TASK-261031-task%d", i), Title: "Other task", Members: []string{"recipient"}})
			}
		}, "invalid_policy"},
		{"long-title", func(r *Request, p *Policy) { r.TaskTitle = strings.Repeat("a", 257) }, "invalid_request"},
		{"untrimmed-title", func(r *Request, p *Policy) { r.TaskTitle = " title" }, "invalid_request"},
		{"relative-storage", func(r *Request, p *Policy) { p.BoardDir = ".task-board" }, "invalid_policy"},
		{"duplicate-policy-task", func(r *Request, p *Policy) { p.Tasks = append(p.Tasks, p.Tasks[0]) }, "invalid_policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, p := inputs()
			tc.edit(&r, &p)
			a := &fakeAdapter{}
			got := (Engine{Adapter: a}).Execute(context.Background(), r, p, filepath.Join(realTemp(t), "j"))
			if got.Status != "refused" || got.Reason != tc.reason || a.posts != 0 || a.checks != 0 {
				t.Fatalf("%+v calls%+v", got, a)
			}
		})
	}
	r, p := inputs()
	a := &fakeAdapter{}
	e := Engine{Adapter: a}
	dir := filepath.Join(realTemp(t), "j")
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" {
		t.Fatal(got)
	}
	r.Member = "other" // Still allowed by the same policy; same correlation cannot change its intent.
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "conflict" || got.Reason != "correlation_intent_changed" || a.posts != 1 {
		t.Fatalf("changed intent: %+v calls%+v", got, a)
	}
}

// Both lost post result and lost readback retain uncertainty. Only an existing
// known event is reconciled on replay; neither path sends the notice twice.
func TestUncertainPostAndReadback(t *testing.T) {
	for _, lostPost := range []bool{true, false} {
		t.Run(map[bool]string{true: "post-result", false: "readback"}[lostPost], func(t *testing.T) {
			r, p := inputs()
			a := &fakeAdapter{}
			if lostPost {
				a.postError = errors.New("result lost after send")
			} else {
				a.verifyReason = "event_readback_unavailable"
			}
			e := Engine{Adapter: a}
			dir := filepath.Join(realTemp(t), "j")
			got := e.Execute(context.Background(), r, p, dir)
			if got.Status != "unknown" || a.posts != 1 {
				t.Fatalf("lost result %+v %+v", got, a)
			}
			a.postError = nil
			a.verifyReason = ""
			got = e.Execute(context.Background(), r, p, dir)
			want := "queued_notification"
			if lostPost {
				want = "unknown"
			}
			if got.Status != want || a.posts != 1 {
				t.Fatalf("replay %+v %+v", got, a)
			}
			j, err := OpenJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			rec, ok, err := j.Load(r.CorrelationID)
			if err != nil || !ok {
				t.Fatal("lost journal", err)
			}
			if lostPost && rec.Phase != "unknown" {
				t.Fatal(rec.Phase)
			}
			if !lostPost && rec.Phase != "verified" {
				t.Fatal(rec.Phase)
			}
		})
	}
}

type memoryJournal struct {
	record        Record
	exists        bool
	saves, failAt int
}

func (m *memoryJournal) Load(string) (Record, bool, error) { return m.record, m.exists, nil }
func (m *memoryJournal) Save(_ string, r Record) error {
	m.saves++
	if m.saves == m.failAt {
		return errors.New("injected write failure")
	}
	m.record = r
	m.exists = true
	return nil
}
func (*memoryJournal) Close() error      { return nil }
func (*memoryJournal) Sync(string) error { return nil }

// An injected write failure has different pre/post-effect outcomes. A preserved
// pre-send intent forbids re-posting even if later receipt persistence failed.
func TestDurabilityFailures(t *testing.T) {
	for _, at := range []int{1, 2, 3} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			r, p := inputs()
			a := &fakeAdapter{}
			j := &memoryJournal{failAt: at}
			e := Engine{Adapter: a, OpenJournal: func(string) (Journal, error) { return j, nil }}
			got := e.Execute(context.Background(), r, p, "ignored")
			if at == 1 {
				if got.Status != "refused" || got.Reason != "intent_not_durable" || a.posts != 0 {
					t.Fatal(got, a)
				}
				return
			}
			if got.Status != "unknown" || a.posts != 1 {
				t.Fatal(got, a)
			}
			j.failAt = 0
			got = e.Execute(context.Background(), r, p, "ignored")
			want := "unknown"
			if at == 3 {
				want = "queued_notification"
			}
			if got.Status != want || a.posts != 1 {
				t.Fatal(got, a)
			}
		})
	}
}

// A crash after intent but before its call is conservatively unknown. Corrupt
// bytes and symlink records refuse without any adapter effect; a fresh neighbor succeeds.
func TestCrashCorruptAndSymlink(t *testing.T) {
	r, p := inputs()
	dir := filepath.Join(realTemp(t), "j")
	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{Contract: Contract, Request: r, PolicyDigest: hash(p), Notice: notice(r), Phase: "intent"}
	rec.IntentDigest = intentHash(r, rec.PolicyDigest, rec.Notice)
	if err := j.Save(r.CorrelationID, rec); err != nil {
		t.Fatal(err)
	}
	j.Close()
	a := &fakeAdapter{}
	e := Engine{Adapter: a}
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "unknown" || a.posts != 0 || a.checks != 0 {
		t.Fatal(got, a)
	}
	f, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if err := os.WriteFile(f[0], []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := e.Execute(context.Background(), r, p, dir); got.Reason != "journal_corrupt_or_unreadable" || a.posts != 0 {
		t.Fatal(got, a)
	}
	if err := os.Remove(f[0]); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realTemp(t), "target")
	os.WriteFile(target, []byte("do not change"), 0600)
	if err := os.Symlink(target, f[0]); err != nil {
		t.Fatal(err)
	}
	if got := e.Execute(context.Background(), r, p, dir); got.Reason != "journal_corrupt_or_unreadable" || a.posts != 0 {
		t.Fatal(got, a)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "do not change" {
		t.Fatal("symlink target changed")
	}
	if got := e.Execute(context.Background(), r, p, filepath.Join(realTemp(t), "fresh")); got.Status != "queued_notification" {
		t.Fatal(got)
	}
}

// The actual filesystem lock rejects overlap, releases on close, and unsafe
// permissions/symlink roots do not become alternative retry journals.
func TestJournalLockAndCustody(t *testing.T) {
	root := realTemp(t)
	dir := filepath.Join(root, "j")
	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenJournal(dir); err == nil {
		other.Close()
		t.Fatal("overlap accepted")
	}
	j.Close()
	j, err = OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenJournal(dir); err == nil {
		other.Close()
		t.Fatal("public journal accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenJournal(link); err == nil {
		other.Close()
		t.Fatal("symlink accepted")
	}
}
