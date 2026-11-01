package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The JSON intake accepts 64 nested containers and rejects a deeper otherwise
// valid value before assigning output; an ordinary projection remains valid.
func TestJSONDepthBound(t *testing.T) {
	for _, depth := range []int{1, 64, 65, 1024} {
		data := []byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth))
		var out json.RawMessage
		err := Decode(data, &out)
		if depth <= 64 {
			if err != nil || string(out) != string(data) {
				t.Fatalf("valid depth %d: %v", depth, err)
			}
		} else if err == nil || err.Error() != "JSON nesting too deep" || out != nil {
			t.Fatalf("depth %d: error=%v output=%d", depth, err, len(out))
		}
	}
}

// Near the full adapter stdout limit, adversarial nesting returns the intended
// depth refusal instead of exhausting the stack; it never assigns output.
func TestJSONLargeNesting(t *testing.T) {
	var out json.RawMessage
	if err := Decode([]byte(strings.Repeat("[", 2*1024*1024)), &out); err == nil || err.Error() != "JSON nesting too deep" || out != nil {
		t.Fatal(err, len(out))
	}
}

// Existing contract binds an immutable whole policy. An unrelated allowlist
// edit conflicts without effects; restoring the original policy reconciles the
// saved event without reposting. This is the documented per-task-policy option.
func TestImmutablePolicyRecovery(t *testing.T) {
	r, p := inputs()
	a := &fakeAdapter{verifyReason: "event_readback_unavailable"}
	e := Engine{Adapter: a}
	dir := filepath.Join(realTemp(t), "journal")
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "unknown" || got.EventID != "$sent" || a.posts != 1 {
		t.Fatal(got, a)
	}
	changed := p
	changed.Tasks = append(append([]TaskBinding(nil), p.Tasks...), TaskBinding{ID: "TASK-261031-next", Title: "Another task", Members: []string{r.Member}})
	if got := e.Execute(context.Background(), r, changed, dir); got.Status != "conflict" || got.Reason != "correlation_intent_changed" || a.posts != 1 || a.reads != 1 {
		t.Fatal(got, a)
	}
	a.verifyReason = ""
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" || got.EventID != "$sent" || a.posts != 1 || a.reads != 2 {
		t.Fatal(got, a)
	}
}

// Policy caps are inclusive; independently violating either cap is covered in
// TestIntentValidationAndConflict. A policy at both limits can still plan.
func TestPolicyCapacityValidControl(t *testing.T) {
	r, p := inputs()
	r.Action = "plan"
	p.Tasks[0].Members = []string{r.Member}
	for i := 1; i < 64; i++ {
		p.Tasks[0].Members = append(p.Tasks[0].Members, fmt.Sprintf("other%d", i))
	}
	for i := 1; i < 256; i++ {
		p.Tasks = append(p.Tasks, TaskBinding{ID: fmt.Sprintf("TASK-261031-other%d", i), Title: "Other task", Members: []string{r.Member}})
	}
	a := &fakeAdapter{}
	if got := (Engine{Adapter: a}).Execute(context.Background(), r, p, "unused"); got.Status != "planned" || a.checks != 1 || a.posts != 0 {
		t.Fatal(got, a)
	}
}

// A symlink ancestor cannot create a journal in its target, and a public lock
// cannot be used even inside a private real directory. Real private roots work.
func TestJournalAncestorAndLockPlants(t *testing.T) {
	root := realTemp(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if j, err := OpenJournal(filepath.Join(link, "journal")); err == nil {
		j.Close()
		t.Fatal("symlink ancestor accepted")
	}
	if _, err := os.Lstat(filepath.Join(target, "journal")); !os.IsNotExist(err) {
		t.Fatal("wrote symlink target", err)
	}
	dir := filepath.Join(target, "journal")
	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
	if err := os.Chmod(filepath.Join(dir, ".lock"), 0644); err != nil {
		t.Fatal(err)
	}
	if j, err := OpenJournal(dir); err == nil {
		j.Close()
		t.Fatal("public lock accepted")
	}
}

// Load binds the requested key independently of record integrity. Save must
// sync the temp file before rename; an injected sync failure installs no record.
func TestReceiptKeyAndTempSyncPlants(t *testing.T) {
	r, p := inputs()
	dir := filepath.Join(realTemp(t), "journal")
	j0, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	j := j0.(*fileJournal)
	defer j.Close()
	rec := Record{Contract: Contract, Request: r, PolicyDigest: hash(p), Notice: notice(r), Phase: "intent"}
	rec.IntentDigest = intentHash(r, rec.PolicyDigest, rec.Notice)
	called := false
	j.syncFile = func(f *os.File) error {
		if strings.HasPrefix(filepath.Base(f.Name()), ".receipt-") {
			called = true
			if _, err := os.Lstat(j.path(r.CorrelationID)); !os.IsNotExist(err) {
				t.Fatal("rename preceded temp sync", err)
			}
			return errors.New("planted temp sync failure")
		}
		return f.Sync()
	}
	if err := j.Save(r.CorrelationID, rec); err == nil || !called {
		t.Fatal("temp sync failure not detected", err, called)
	}
	if _, err := os.Lstat(j.path(r.CorrelationID)); !os.IsNotExist(err) {
		t.Fatal("failed save installed record", err)
	}
	j.syncFile = (*os.File).Sync
	if err := j.Save(r.CorrelationID, rec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := j.Load(r.CorrelationID); err != nil || !ok {
		t.Fatal("valid load", ok, err)
	}
	if err := os.WriteFile(j.path("another-key"), encoded(rec), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := j.Load("another-key"); err == nil || !ok {
		t.Fatal("key mismatch accepted", ok, err)
	}
}

// Display-safe multilingual titles remain accepted; line separators, format
// controls, private-use and noncharacters refuse before checks, writes or posts.
func TestNoticeUnicodeBoundary(t *testing.T) {
	for _, tc := range []struct {
		title string
		valid bool
	}{
		{"English / Հայերեն / Русский / 日本語 😀", true},
		{"cafe\u0301", true},
		{"line\u2028Recipient: other", false}, {"line\u2029more", false},
		{"bidi\u202eother", false}, {"zero\u200bwidth", false},
		{"private\ue000", false}, {"noncharacter\ufdd0", false},
		{"noncharacter\U0001fffe", false}, {"noncharacter\U0010ffff", false},
		{" raw", false}, {strings.Repeat("a", 257), false},
	} {
		t.Run(tc.title, func(t *testing.T) {
			r, p := inputs()
			r.TaskTitle, p.Tasks[0].Title = tc.title, tc.title
			r.Action = "plan"
			a := &fakeAdapter{}
			dir := filepath.Join(realTemp(t), "journal")
			got := (Engine{Adapter: a}).Execute(context.Background(), r, p, dir)
			if tc.valid {
				if got.Status != "planned" || a.checks != 1 || a.posts != 0 {
					t.Fatal(got, a)
				}
			} else if got.Status != "refused" || got.Reason != "invalid_request" || a.checks != 0 || a.posts != 0 {
				t.Fatal(got, a)
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatal("title validation/plan wrote a journal", err)
			}
		})
	}
}
