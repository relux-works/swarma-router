package handoff

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Exact canonical names decode at every closed level; case aliases and nested
// unknown fields refuse before assigning any output, including journal records.
func TestExactClosedFieldNames(t *testing.T) {
	r, p := inputs()
	rec := Record{Contract: Contract, Request: r, PolicyDigest: hash(p), Notice: notice(r), Phase: "intent"}
	rec.IntentDigest = intentHash(r, rec.PolicyDigest, rec.Notice)
	for _, tc := range []struct {
		name  string
		value any
		field string
		alias string
	}{
		{"request", r, "action", "Action"},
		{"policy", p, "sender", "Sender"},
		{"nested-policy", p, "members", "Members"},
		{"record", rec, "phase", "Phase"},
		{"nested-request", rec, "action", "ACTION"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical := string(encoded(tc.value))
			out := reflect.New(reflect.TypeOf(tc.value))
			if err := Decode([]byte(canonical), out.Interface()); err != nil || !reflect.DeepEqual(out.Elem().Interface(), tc.value) {
				t.Fatal("canonical input failed", err)
			}
			for _, key := range []string{tc.alias, fmt.Sprintf(`\u%04x%s`, tc.field[0]-32, tc.field[1:])} {
				bad := strings.Replace(canonical, `"`+tc.field+`":`, `"`+key+`":`, 1)
				out := reflect.New(reflect.TypeOf(tc.value))
				if err := Decode([]byte(bad), out.Interface()); err == nil || !out.Elem().IsZero() {
					t.Fatal("alias accepted or assigned output", key, err)
				}
			}
		})
	}
	var policy Policy
	bad := strings.Replace(string(encoded(p)), `"members":`, `"unrelated":true,"members":`, 1)
	if Decode([]byte(bad), &policy) == nil {
		t.Fatal("nested unknown key accepted")
	}
}

// A persisted record with a phase alias must be treated as corrupt, kept intact,
// and never turned into permission to send again. Canonical replay is tested nearby.
func TestJournalAliasRefusesWithoutEffects(t *testing.T) {
	r, p := inputs()
	dir := filepath.Join(realTemp(t), "journal")
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
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	b := []byte(strings.Replace(string(encoded(rec)), `"phase":`, `"Phase":`, 1))
	if err := os.WriteFile(files[0], b, 0600); err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{}
	got := (Engine{Adapter: a}).Execute(context.Background(), r, p, dir)
	if got.Status != "refused" || got.Reason != "journal_corrupt_or_unreadable" || a.checks != 0 || a.posts != 0 || a.reads != 0 {
		t.Fatal(got, a)
	}
	after, err := os.ReadFile(files[0])
	if err != nil || string(after) != string(b) || !json.Valid(after) {
		t.Fatal("journal changed", err)
	}
}
