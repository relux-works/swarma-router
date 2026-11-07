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

// Exercise the actual CLI decoder and Engine policy path; the adapter is inert
// and the separate CLIAdapter tests bind the source wire and its negative cases.
func TestCLIConfiguredAssignmentID(t *testing.T) {
	dir := t.TempDir()
	r := handoff.Request{Contract: handoff.Contract, Action: "plan", CorrelationID: "configured", Project: "example", TaskID: "JOB-240101-0042", TaskTitle: "Example assignment", Member: "recipient", Room: "example", Topic: "work", ReplyTo: "$reply"}
	p := handoff.Policy{Contract: handoff.Contract, Project: "example", ProjectRoot: "/example", BoardDir: "/example/.task-board", Server: "example.test", Sender: "caller", Room: "example", RoomID: "!room", Topic: "work", ReplyTo: "$reply", Tasks: []handoff.TaskBinding{{ID: r.TaskID, Title: r.TaskTitle, Members: []string{r.Member}}}}
	for _, tc := range []struct {
		config *handoff.AssignmentIDPolicy
		reason string
	}{
		{nil, "invalid_request"},
		{&handoff.AssignmentIDPolicy{Pattern: `JOB-[0-9]{6}-[0-9]{4}`, MaxBytes: 32}, ""},
		{&handoff.AssignmentIDPolicy{Pattern: "[", MaxBytes: 32}, "invalid_policy"},
	} {
		p.AssignmentID = tc.config
		for name, value := range map[string]any{"request": r, "policy": p} {
			b, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
		a := &inertAdapter{}
		var out bytes.Buffer
		exit := runWithEngine(context.Background(), []string{"--request", filepath.Join(dir, "request"), "--policy", filepath.Join(dir, "policy"), "--journal", filepath.Join(dir, "journal")}, &out, handoff.Engine{Adapter: a})
		var got handoff.Result
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Reason != tc.reason || a.posts != 0 {
			t.Fatal(exit, got, a)
		}
		if tc.reason == "" {
			if exit != 0 || got.Status != "planned" || a.checks != 1 {
				t.Fatal(exit, got, a)
			}
		} else if exit != 2 || got.Status != "refused" || a.checks != 0 {
			t.Fatal(exit, got, a)
		}
		if _, err := os.Stat(filepath.Join(dir, "journal")); !os.IsNotExist(err) {
			t.Fatal("plan/refusal wrote journal", err)
		}
	}
}
