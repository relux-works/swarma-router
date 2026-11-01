package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relux.works/swarma-router/handoff"
)

type inertAdapter struct {
	checks, posts int
	verifyReason  string
}

func (a *inertAdapter) ValidateSources(context.Context, handoff.Request, handoff.Policy) string {
	a.checks++
	return ""
}
func (a *inertAdapter) Post(context.Context, handoff.Request, handoff.Policy, string) (string, error) {
	a.posts++
	return "$sent", nil
}
func (a *inertAdapter) VerifyEvent(context.Context, handoff.Request, handoff.Policy, string, string) string {
	return a.verifyReason
}

// CLI intake accepts the explicitly supplied private policy and emits JSON for a
// valid plan; malformed flags/files/extra fields refuse before the inert adapter.
func TestCLIIntake(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := filepath.Join(dir, "request.json")
	pol := filepath.Join(dir, "policy.json")
	journal := filepath.Join(dir, "journal")
	r := handoff.Request{Contract: handoff.Contract, Action: "plan", CorrelationID: "first", Project: "example", TaskID: "TASK-261031-example", TaskTitle: "TASK-261031-example: Example task", Member: "recipient", Room: "example", Topic: "work", ReplyTo: "$reply"}
	p := handoff.Policy{Contract: handoff.Contract, Project: "example", ProjectRoot: "/example", BoardDir: "/example/.task-board", Server: "example.test", Sender: "caller", Room: "example", RoomID: "!room", Topic: "work", ReplyTo: "$reply", Tasks: []handoff.TaskBinding{{ID: r.TaskID, Title: r.TaskTitle, Members: []string{r.Member}}}}
	write := func(path string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(req, r)
	write(pol, p)
	args := []string{"--request", req, "--policy", pol, "--journal", journal}
	a := &inertAdapter{}
	var out bytes.Buffer
	if exit := runWithEngine(context.Background(), args, &out, handoff.Engine{Adapter: a}); exit != 0 {
		t.Fatal(exit, out.String())
	}
	var result handoff.Result
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "planned" || a.posts != 0 || a.checks != 1 {
		t.Fatal(out.String(), a)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("plan created journal")
	}
	for _, bad := range [][]string{nil, {"--request", req}, append(append([]string{}, args...), "--as", "borrowed"), append(append([]string{}, args...), "extra")} {
		out.Reset()
		before := a.checks
		if exit := runWithEngine(context.Background(), bad, &out, handoff.Engine{Adapter: a}); exit != 2 || a.checks != before || a.posts != 0 {
			t.Fatal(exit, out.String(), a)
		}
	}
	if err := os.Chmod(pol, 0644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	before := a.checks
	if exit := runWithEngine(context.Background(), args, &out, handoff.Engine{Adapter: a}); exit != 2 || a.checks != before || a.posts != 0 {
		t.Fatal(exit, out.String())
	}
	if err := os.Chmod(pol, 0600); err != nil {
		t.Fatal(err)
	}
	// Case aliases, in either order or alone, must refuse before source checks,
	// posts or journal creation. Canonical plan above remains the positive control.
	rawRequest, _ := json.Marshal(r)
	rawPolicy, _ := json.Marshal(p)
	for _, tc := range []struct{ name, request, policy, reason string }{
		{"plan-then-alias-dispatch", strings.Replace(string(rawRequest), `"action":"plan"`, `"action":"plan","Action":"dispatch"`, 1), string(rawPolicy), "invalid_request_file"},
		{"alias-dispatch-then-plan", strings.Replace(string(rawRequest), `"action":"plan"`, `"Action":"dispatch","action":"plan"`, 1), string(rawPolicy), "invalid_request_file"},
		{"alias-only", strings.Replace(string(rawRequest), `"action"`, `"Action"`, 1), string(rawPolicy), "invalid_request_file"},
		{"escaped-alias", strings.Replace(string(rawRequest), `"action"`, `"\u0041ction"`, 1), string(rawPolicy), "invalid_request_file"},
		{"policy-alias", string(rawRequest), strings.Replace(string(rawPolicy), `"sender"`, `"Sender"`, 1), "invalid_policy_file"},
		{"nested-policy-alias", string(rawRequest), strings.Replace(string(rawPolicy), `"members"`, `"Members"`, 1), "invalid_policy_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(req, []byte(tc.request), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pol, []byte(tc.policy), 0600); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			before := a.checks
			if exit := runWithEngine(context.Background(), args, &out, handoff.Engine{Adapter: a}); exit != 2 || a.checks != before || a.posts != 0 {
				t.Fatal("alias reached engine", exit, out.String(), a)
			}
			var result handoff.Result
			if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "refused" || result.Reason != tc.reason {
				t.Fatal(out.String())
			}
			if _, err := os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("rejected input created journal", err)
			}
		})
	}
	// Canonical dispatch still posts once; its matching replay makes no new call.
	r.Action = "dispatch"
	write(req, r)
	write(pol, p)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	args = []string{"--request", req, "--policy", pol, "--journal", filepath.Join(realDir, "journal")}
	before = a.checks
	for i := 0; i < 2; i++ {
		out.Reset()
		if exit := runWithEngine(context.Background(), args, &out, handoff.Engine{Adapter: a}); exit != 0 {
			t.Fatal(exit, out.String())
		}
		var result handoff.Result
		if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "queued_notification" || result.EventID != "$sent" {
			t.Fatal(out.String())
		}
	}
	if a.posts != 1 || a.checks != before+1 {
		t.Fatal("dispatch/replay duplicated effects", a)
	}
	// A known posted event with failed verification is JSON unknown/exit 3;
	// restoring readback reconciles that event without a second post.
	r.CorrelationID = "uncertain"
	write(req, r)
	a.verifyReason = "event_readback_unavailable"
	out.Reset()
	priorPosts := a.posts
	if exit := runWithEngine(context.Background(), args, &out, handoff.Engine{Adapter: a}); exit != 3 {
		t.Fatal(exit, out.String())
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "unknown" || result.EventID != "$sent" || result.Reason != "event_readback_unavailable" || a.posts != priorPosts+1 {
		t.Fatal(out.String(), a)
	}
	a.verifyReason = ""
	out.Reset()
	if exit := runWithEngine(context.Background(), args, &out, handoff.Engine{Adapter: a}); exit != 0 || a.posts != priorPosts+1 {
		t.Fatal(exit, out.String(), a)
	}

}
