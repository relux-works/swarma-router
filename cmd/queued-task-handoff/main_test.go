package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"relux.works/swarma-router/handoff"
)

type inertAdapter struct{ checks, posts int }

func (a *inertAdapter) ValidateSources(context.Context, handoff.Request, handoff.Policy) string {
	a.checks++
	return ""
}
func (a *inertAdapter) Post(context.Context, handoff.Request, handoff.Policy, string) (string, error) {
	a.posts++
	return "$sent", nil
}
func (*inertAdapter) VerifyEvent(context.Context, handoff.Request, handoff.Policy, string, string) string {
	return ""
}

// CLI intake accepts the explicitly supplied private policy and emits JSON for a
// valid plan; malformed flags/files/extra fields refuse before the inert adapter.
func TestCLIIntake(t *testing.T) {
	dir := t.TempDir()
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
}
