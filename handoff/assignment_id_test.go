package handoff

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func configuredInputs() (Request, Policy) {
	r, p := inputs()
	r.TaskID = "JOB-240101-0042"
	r.TaskTitle = "Example assignment"
	p.Tasks[0].ID = r.TaskID
	p.Tasks[0].Title = r.TaskTitle
	p.AssignmentID = &AssignmentIDPolicy{Pattern: `JOB-[0-9]{6}-[0-9]{4}`, MaxBytes: 32}
	return r, p
}

func TestAssignmentIDAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Request, *Policy)
		want string
	}{
		{"configured", func(*Request, *Policy) {}, "planned"},
		{"unconfigured", func(r *Request, p *Policy) { p.AssignmentID = nil }, "invalid_request"},
		{"request-pattern", func(r *Request, p *Policy) { r.TaskID = "JOB-240101-other" }, "invalid_request"},
		{"policy-pattern", func(r *Request, p *Policy) { p.Tasks[0].ID = "TASK-240101-other" }, "invalid_policy"},
		{"missing-binding", func(r *Request, p *Policy) { p.Tasks[0].ID = "JOB-240101-0043" }, "task_not_authorized"},
		{"exact-title", func(r *Request, p *Policy) { r.TaskTitle = "Different" }, "task_binding_mismatch"},
		{"member", func(r *Request, p *Policy) { r.Member = "unlisted" }, "member_not_authorized"},
		{"room", func(r *Request, p *Policy) { r.Room = "other" }, "destination_not_authorized"},
		{"opaque-room", func(r *Request, p *Policy) { r.Room = p.RoomID }, "invalid_request"},
		{"reply", func(r *Request, p *Policy) { r.ReplyTo = "$other" }, "destination_not_authorized"},
		{"full-match-prefix", func(r *Request, p *Policy) { r.TaskID += "extra" }, "invalid_request"},
		{"full-match-alternation", func(r *Request, p *Policy) { p.AssignmentID.Pattern = `JOB|0042` }, "invalid_request"},
		{"byte-bound", func(r *Request, p *Policy) { p.AssignmentID.MaxBytes = len(r.TaskID) - 1 }, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, p := configuredInputs()
			r.Action = "plan"
			tc.edit(&r, &p)
			a := &fakeAdapter{}
			dir := filepath.Join(realTemp(t), "journal")
			got := (Engine{Adapter: a}).Execute(context.Background(), r, p, dir)
			wantChecks := 0
			if tc.want == "planned" {
				wantChecks = 1
				if got.Status != "planned" || got.Reason != "" {
					t.Fatalf("%+v", got)
				}
			} else if got.Status != "refused" || got.Reason != tc.want {
				t.Fatalf("%+v", got)
			}
			if a.checks != wantChecks || a.posts != 0 {
				t.Fatal(a)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("plan/refusal wrote journal", err)
			}
		})
	}
	for _, id := range []string{"TASK-240101-example", "BUG-240101-example"} {
		r, p := inputs()
		r.TaskID, p.Tasks[0].ID = id, id
		if reason := validate(r, p); reason != "" {
			t.Fatal(id, reason)
		}
	}
	r, p := inputs()
	r.TaskID, p.Tasks[0].ID = "STORY-240101-example", "STORY-240101-example"
	if reason := validate(r, p); reason != "invalid_request" {
		t.Fatal(reason)
	}
}

func TestAssignmentIDConfigurationErrors(t *testing.T) {
	for _, config := range []AssignmentIDPolicy{
		{}, {Pattern: ".*"}, {Pattern: ".*", MaxBytes: -1}, {Pattern: ".*", MaxBytes: 257},
		{Pattern: "[", MaxBytes: 32}, {Pattern: `(a)\1`, MaxBytes: 32},
		{Pattern: `(?=JOB)JOB`, MaxBytes: 32}, {Pattern: strings.Repeat("a", 513), MaxBytes: 32},
		{Pattern: string([]byte{0xff}), MaxBytes: 32},
		{Pattern: `JOB)|(?:SAFE`, MaxBytes: 32}, {Pattern: `)|(?:`, MaxBytes: 32},
	} {
		r, p := inputs()
		p.AssignmentID = &config
		calls := 0
		a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) { calls++; return nil, nil })}
		if got := (Engine{Adapter: a}).Execute(context.Background(), r, p, "unused"); got.Reason != "invalid_policy" {
			t.Fatal(got)
		}
		if reason := a.ValidateSources(context.Background(), r, p); reason != "invalid_policy" {
			t.Fatal(reason)
		}
		if _, err := a.Post(context.Background(), r, p, "unused"); err == nil || calls != 0 {
			t.Fatal(err, calls)
		}
	}
	// Closed nested fields retain canonical names and reject unknown/duplicate aliases.
	for _, raw := range []string{
		`{"assignment_id":{"pattern":".*","max_bytes":32,"Max_Bytes":256}}`,
		`{"assignment_id":{"pattern":".*","max_bytes":32,"max_bytes":256}}`,
		`{"assignment_id":{"pattern":".*","max_bytes":32,"provider":"other"}}`,
	} {
		var p Policy
		if Decode([]byte(raw), &p) == nil {
			t.Fatal("accepted invalid configuration", raw)
		}
	}
}

// Configuration is standalone Go RE2 syntax. Quoting and flags must retain
// their meaning when full-string anchors are added, including an open \Q quote.
func TestAssignmentIDRE2Semantics(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, id string
		match             bool
	}{
		{"open-quote", `\QJOB`, "JOB", true},
		{"closed-quote", `\QJOB\E`, "JOB", true},
		{"quoted-prefix", `\QJOB`, "JOB-extra", false},
		{"quoted-suffix", `\QJOB`, "extraJOB", false},
		{"quoted-operators", `\QJOB|SAFE`, "JOB", false},
		{"quoted-group", `\QJOB)|(?:SAFE`, "JOB", false},
		{"quote-then-alternation", `\QJOB\E|SAFE`, "SAFE", true},
		{"alternation", `JOB|SAFE`, "JOB", true},
		{"alternation-prefix", `JOB|SAFE`, "JOB-extra", false},
		{"alternation-suffix", `JOB|SAFE`, "extraSAFE", false},
		{"case-flag", `(?i)job`, "JOB", true},
		{"scoped-flags", `(?i:job)|SAFE`, "JOB", true},
		{"scoped-flags-end", `(?i:job)|SAFE`, "safe", false},
		{"multiline", `(?m)^JOB$`, "JOB", true},
		{"multiline-prefix", `(?m)^JOB$`, "JOB-extra", false},
		{"dotall", `(?s)J.*`, "JOB", true},
		{"ungreedy", `(?U)J.+`, "JOB", true},
		{"pattern-byte-bound", strings.Repeat(`(?:)`, 127) + `JOB?`, "JOB", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := regexp.Compile(tc.pattern); err != nil {
				t.Fatal("invalid standalone positive syntax control", err)
			}
			r, p := configuredInputs()
			r.Action, r.TaskID, p.Tasks[0].ID = "plan", tc.id, tc.id
			p.AssignmentID.Pattern = tc.pattern
			a := &fakeAdapter{}
			got := (Engine{Adapter: a}).Execute(context.Background(), r, p, "unused")
			if tc.match {
				if got.Status != "planned" || a.checks != 1 || a.posts != 0 {
					t.Fatalf("valid pattern: %+v adapter=%+v", got, a)
				}
			} else if got.Status != "refused" || got.Reason != "invalid_request" || a.checks != 0 || a.posts != 0 {
				t.Fatalf("whole-string rejection: %+v adapter=%+v", got, a)
			}

			// Only inert runners are used: a valid pattern reaches the source
			// check, while a rejected ID reaches neither direct adapter runner.
			calls := 0
			direct := CLIAdapter{Runner: runnerFunc(func(_ context.Context, _, name string, _ ...string) ([]byte, error) {
				calls++
				if name == "a2a" {
					return []byte("$inert"), nil
				}
				return nil, nil
			})}
			reason := direct.ValidateSources(context.Background(), r, p)
			if tc.match {
				if reason != "board_authority_not_local" || calls != 1 {
					t.Fatal(reason, calls)
				}
			} else if reason != "invalid_request" || calls != 0 {
				t.Fatal(reason, calls)
			}
			calls = 0
			event, err := direct.Post(context.Background(), r, p, "inert")
			if tc.match {
				if err != nil || event != "$inert" || calls != 1 {
					t.Fatal(event, err, calls)
				}
			} else if err == nil || calls != 0 {
				t.Fatal(event, err, calls)
			}
		})
	}
}

func TestAssignmentIDMalformedRE2NoEffects(t *testing.T) {
	for _, pattern := range []string{`JOB)|(?:SAFE`, `)|(?:`, `\QJOB\E)|(?:SAFE`, `\QJOB\E(`} {
		if _, err := regexp.Compile(pattern); err == nil {
			t.Fatal("malformed standalone syntax control unexpectedly compiled")
		}
		for _, action := range []string{"plan", "dispatch"} {
			t.Run(pattern+"/"+action, func(t *testing.T) {
				r, p := configuredInputs()
				r.Action, p.AssignmentID.Pattern = action, pattern
				calls, opens := 0, 0
				a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) {
					calls++
					return []byte("$inert"), nil
				})}
				got := (Engine{Adapter: a, OpenJournal: func(string) (Journal, error) {
					opens++
					return nil, os.ErrPermission
				}}).Execute(context.Background(), r, p, "unused")
				if got.Status != "refused" || got.Reason != "invalid_policy" || calls != 0 || opens != 0 {
					t.Fatal(got, calls, opens)
				}
				if reason := a.ValidateSources(context.Background(), r, p); reason != "invalid_policy" || calls != 0 {
					t.Fatal(reason, calls)
				}
				if event, err := a.Post(context.Background(), r, p, "inert"); err == nil || event != "" || calls != 0 {
					t.Fatal(event, err, calls)
				}
			})
		}
	}
}

func TestAssignmentIDUnsafeValues(t *testing.T) {
	for _, id := range []string{"", "-option", "_option", "../task", "a/b", `a\b`, "a.b", "a); other()", "a\nTask: forged", "a\rlog", "a\tvalue", "a\x00value", "a value", "a\u202evalue", "é", "a\"b", "a'b", strings.Repeat("a", 257)} {
		r, p := configuredInputs()
		p.AssignmentID = &AssignmentIDPolicy{Pattern: `(?s).*`, MaxBytes: 256}
		r.TaskID = id
		p.Tasks[0].ID = id
		calls := 0
		a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) { calls++; return nil, nil })}
		if reason := a.ValidateSources(context.Background(), r, p); reason != "invalid_request" || calls != 0 {
			t.Fatal(id, reason, calls)
		}
		r.TaskID = "safe"
		if reason := validate(r, p); reason != "invalid_policy" {
			t.Fatal(id, reason)
		}
	}
	r, p := configuredInputs()
	r.TaskID = strings.Repeat("a", 256)
	p.Tasks[0].ID = r.TaskID
	p.AssignmentID = &AssignmentIDPolicy{Pattern: `a+`, MaxBytes: 256}
	if reason := validate(r, p); reason != "" {
		t.Fatal("inclusive bound", reason)
	}
}

func TestAssignmentIDJournalBinding(t *testing.T) {
	r, p := configuredInputs()
	a := &fakeAdapter{verifyReason: "event_readback_unavailable"}
	e := Engine{Adapter: a}
	dir := filepath.Join(realTemp(t), "journal")
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "unknown" || a.posts != 1 {
		t.Fatal(got, a)
	}
	for _, config := range []AssignmentIDPolicy{
		{Pattern: p.AssignmentID.Pattern, MaxBytes: 33},
		{Pattern: `JOB-[0-9]+-[0-9]+`, MaxBytes: 32},
	} {
		changed := p
		changed.AssignmentID = &config
		if got := e.Execute(context.Background(), r, changed, dir); got.Status != "conflict" || a.posts != 1 || a.reads != 1 {
			t.Fatal(got, a)
		}
	}
	a.verifyReason = ""
	for i := 0; i < 2; i++ {
		if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" || got.EventID != "$sent" || a.posts != 1 || a.reads != 2 {
			t.Fatal(got, a)
		}
	}
}

func TestLegacyPolicySerialization(t *testing.T) {
	// Original field order/bytes are part of retained whole-policy journal hashes.
	const legacy = `{"contract":"queued-task-handoff/0.1.0","project":"example","project_root":"/example","board_dir":"/example/.task-board","server":"example.test","sender":"caller","room":"example","room_id":"!room","topic":"work","reply_to":"$reply","tasks":[{"id":"TASK-261031-example","title":"TASK-261031-example: Example task","members":["recipient","other"]}]}`
	_, p := inputs()
	got, err := json.Marshal(p)
	if err != nil || string(got) != legacy {
		t.Fatal(string(got), err)
	}
}
