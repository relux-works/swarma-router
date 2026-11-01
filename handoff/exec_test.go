package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Re-execution is an isolated tool fixture: it never calls real a2a/task-board.
func TestExecRunnerHelper(t *testing.T) {
	if os.Getenv("ROUTER_EXEC_HELPER") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "environment":
		json.NewEncoder(os.Stdout).Encode(map[string]string{"board": os.Getenv("TASK_BOARD_DIR"), "tb": os.Getenv("TB_REMOTE"), "identity": os.Getenv("A2A_AGENT"), "cwd": mustWorkingDirectory()})
	case "limit":
		os.Stdout.Write(bytes.Repeat([]byte("x"), 2*1024*1024))
	case "oversize":
		os.Stdout.Write(bytes.Repeat([]byte("x"), 2*1024*1024+1))
	case "stderr-overflow":
		os.Stderr.Write(bytes.Repeat([]byte("x"), 64*1024+1))
	case "failure":
		fmt.Fprint(os.Stderr, "private tool diagnostic")
		os.Exit(7)
	case "deadline":
		time.Sleep(35 * time.Second)
	default:
		os.Exit(9)
	}
	os.Exit(0)
}

func mustWorkingDirectory() string { d, _ := os.Getwd(); return d }

// Real subprocess wiring preserves the a2a environment, scrubs board selectors
// for task-board, enforces name/output/deadline bounds and hides native errors.
// The deadline case exercises the actual 29s limit, not a shortened test seam.
func TestExecRunnerBoundaries(t *testing.T) {
	dir := realTemp(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a2a", "task-board"} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("ROUTER_EXEC_HELPER", "1")
	t.Setenv("TASK_BOARD_DIR", "/foreign-board")
	t.Setenv("TB_REMOTE", "foreign-selector")
	t.Setenv("A2A_AGENT", "test-own-identity")
	run := func(name, mode string) ([]byte, error) {
		return (ExecRunner{}).Run(context.Background(), dir, name, "-test.run=^TestExecRunnerHelper$", "--", mode)
	}
	for _, name := range []string{"a2a", "task-board"} {
		out, err := run(name, "environment")
		var got map[string]string
		if err != nil || json.Unmarshal(out, &got) != nil || got["identity"] != "test-own-identity" || got["cwd"] != dir {
			t.Fatal(name, string(out), err)
		}
		if name == "task-board" {
			if got["board"] != "" || got["tb"] != "" {
				t.Fatal("board selectors leaked", got)
			}
		} else if got["board"] != "/foreign-board" || got["tb"] != "foreign-selector" {
			t.Fatal("a2a environment changed", got)
		}
	}
	if out, err := run("a2a", "limit"); err != nil || len(out) != 2*1024*1024 {
		t.Fatal("valid boundary", len(out), err)
	}
	for _, mode := range []string{"oversize", "stderr-overflow", "failure"} {
		out, err := run("a2a", mode)
		if err == nil || err.Error() != "adapter command failed" || len(out) != 0 {
			t.Fatal(mode, len(out), err)
		}
	}
	if out, err := run("unapproved-tool", "environment"); err == nil || err.Error() != "unsupported tool" || len(out) != 0 {
		t.Fatal(out, err)
	}
	start := time.Now()
	out, err := run("a2a", "deadline")
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "adapter command failed") || len(out) != 0 || elapsed < 28*time.Second || elapsed > 33*time.Second {
		t.Fatal("deadline", elapsed, len(out), err)
	}
}
