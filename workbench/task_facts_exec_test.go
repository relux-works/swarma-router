package workbench

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

// Child-only fixture emits bounded observations of public env/cwd, overflow,
// failure or a blocked process. The ordinary test run creates no child here.
func TestTaskFactsExecHelper(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--task-facts-helper" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "public":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(4)
		}
		out := map[string]any{"cwd": cwd, "home": os.Getenv("HOME"), "path": os.Getenv("PATH"), "locale": os.Getenv("LC_ALL"), "parent": os.Getppid(), "child": os.Getpid()}
		for _, name := range []string{"TASK_BOARD_REMOTE", "TB_REMOTE", "A2A_AGENT", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
			out[name] = os.Getenv(name) != ""
		}
		if json.NewEncoder(os.Stdout).Encode(out) != nil {
			os.Exit(4)
		}
	case "limit":
		fmt.Print(strings.Repeat("x", MaxTaskSourceOutput))
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", MaxTaskSourceOutput+1))
	case "stderr-limit":
		fmt.Fprint(os.Stderr, strings.Repeat("x", maxTaskSourceStderr))
		fmt.Print("ok")
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", maxTaskSourceStderr+1))
	case "fail":
		fmt.Fprint(os.Stdout, `{"private":"not an observation"}`)
		os.Exit(17)
	case "block":
		time.Sleep(time.Minute)
	default:
		os.Exit(4)
	}
	os.Exit(0)
}

// The real exec runner forwards only the admitted public environment and reaps
// its child. Ambient selectors/native markers cannot alter the command. These
// are test-binary subprocesses, not task-board/Curator/member invocations.
func TestTaskFactsExecPublicEnvironment(t *testing.T) {
	s, ctx := taskFixture(t)
	for _, name := range []string{"TASK_BOARD_REMOTE", "TB_REMOTE", "A2A_AGENT", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(name, "isolated-fixture-marker")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env, ok := taskEnvironment(s.Environment)
	if !ok {
		t.Fatal("invalid positive environment")
	}
	cmd := TaskFactsCommand{Executable: executable, Directory: s.ProjectRoot, Args: []string{"-test.run=^TestTaskFactsExecHelper$", "--", "--task-facts-helper", "public"}, Environment: env}
	raw, err := (TaskFactsExecRunner{}).Run(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil {
		t.Fatal("invalid child response")
	}
	root, err := filepath.EvalSymlinks(s.ProjectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got["cwd"] != root || got["home"] != s.Environment["HOME"] || got["path"] != s.Environment["PATH"] || got["locale"] != "C" || got["parent"] != float64(os.Getpid()) {
		t.Fatal("public command binding changed", got)
	}
	for _, name := range []string{"TASK_BOARD_REMOTE", "TB_REMOTE", "A2A_AGENT", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if got[name] != false {
			t.Fatal("ambient selector/identity inherited", name)
		}
	}
	t.Logf("fixture child PID %.0f reaped", got["child"])
}

// Positive exact-limit output succeeds; overflow/failure/cancellation yield no
// partial output or raw child diagnostics. Cancellation is exercised on a real
// blocked test child; this is not a kernel-I/O preemption or daemon claim.
func TestTaskFactsExecLimits(t *testing.T) {
	for _, mode := range []string{"limit", "stderr-limit", "stdout-overflow", "stderr-overflow", "fail", "block", "nil-context", "bad-env", "duplicate-env"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx := taskFixture(t)
			env, _ := taskEnvironment(s.Environment)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := TaskFactsCommand{Executable: executable, Directory: s.ProjectRoot, Args: []string{"-test.run=^TestTaskFactsExecHelper$", "--", "--task-facts-helper", mode}, Environment: env}
			switch mode {
			case "block":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
				defer cancel()
			case "nil-context":
				ctx = nil
			case "bad-env":
				cmd.Environment = append(cmd.Environment, "A2A_AGENT=fixture")
				cmd.Args[len(cmd.Args)-1] = "public"
			case "duplicate-env":
				cmd.Environment = append([]string{"HOME="}, cmd.Environment...)
				cmd.Args[len(cmd.Args)-1] = "public"
			}
			start := time.Now()
			raw, err := (TaskFactsExecRunner{}).Run(ctx, cmd)
			if mode == "stderr-limit" {
				if err != nil || string(raw) != "ok" {
					t.Fatal("valid stderr limit refused", err)
				}
				return
			}
			if mode == "limit" {
				if err != nil || len(raw) != MaxTaskSourceOutput || !bytes.Equal(raw, bytes.Repeat([]byte("x"), MaxTaskSourceOutput)) {
					t.Fatal("valid limit refused", err)
				}
				return
			}
			if err == nil || raw != nil || strings.Contains(err.Error(), "private") {
				t.Fatal("failure leaked/returned partial output", err)
			}
			if mode == "block" && (ctx.Err() != context.DeadlineExceeded || time.Since(start) > 3*time.Second) {
				t.Fatal("deadline failed to bound child", ctx.Err())
			}
		})
	}
}
