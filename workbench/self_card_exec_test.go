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

// This child-only fixture reports public context or exercises transport faults;
// no real card, source binary, account or member is accessed by these tests.
func TestSelfCardExecHelper(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--self-card-helper" && i+1 < len(os.Args) {
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
		out := map[string]any{"cwd": cwd, "agent": os.Getenv("A2A_AGENT"), "home": os.Getenv("HOME"), "path": os.Getenv("PATH"), "parent": os.Getppid(), "child": os.Getpid()}
		for _, key := range []string{"A2A_CONFIG_DIR", "TASK_BOARD_REMOTE", "TB_REMOTE", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
			out[key] = os.Getenv(key) != ""
		}
		if json.NewEncoder(os.Stdout).Encode(out) != nil {
			os.Exit(4)
		}
	case "stdout-limit":
		fmt.Print(strings.Repeat("x", MaxSelfCardSourceOutput))
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", MaxSelfCardSourceOutput+1))
	case "stderr-limit":
		fmt.Fprint(os.Stderr, strings.Repeat("x", maxSelfCardSourceStderr))
		fmt.Print("ok")
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", maxSelfCardSourceStderr+1))
	case "fail":
		fmt.Print("private failure output")
		fmt.Fprint(os.Stderr, "private failure diagnostic")
		os.Exit(17)
	case "block":
		time.Sleep(time.Minute)
	default:
		os.Exit(4)
	}
	os.Exit(0)
}

func selfCardExecCommand(t *testing.T, s PulseSelfFacts, mode string) SelfCardCommand {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env, ok := selfCardEnvironment(s.Environment, s.Caller)
	if !ok {
		t.Fatal("invalid positive fixture env")
	}
	return SelfCardCommand{Executable: executable, Directory: s.Directory, Caller: s.Caller, Args: []string{"-test.run=^TestSelfCardExecHelper$", "--", "--self-card-helper", mode}, Environment: env}
}

// The real runner preserves only the same public caller/environment and reaps
// its fixture child; ambient source selectors and native markers are absent.
func TestSelfCardExecEnvironment(t *testing.T) {
	s, ctx, _ := selfCardFixture(t)
	for _, key := range []string{"A2A_CONFIG_DIR", "TASK_BOARD_REMOTE", "TB_REMOTE", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(key, "isolated-marker")
	}
	out, err := (SelfCardExecRunner{}).Run(ctx, selfCardExecCommand(t, s, "public"))
	if err != nil || len(out.Stderr) != 0 {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(out.Stdout, &got) != nil {
		t.Fatal("bad helper output")
	}
	root, err := filepath.EvalSymlinks(s.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if got["cwd"] != root || got["agent"] != s.Caller || got["home"] != s.Environment["HOME"] || got["path"] != s.Environment["PATH"] || got["parent"] != float64(os.Getpid()) {
		t.Fatal("public context changed", got)
	}
	for _, key := range []string{"A2A_CONFIG_DIR", "TASK_BOARD_REMOTE", "TB_REMOTE", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if got[key] != false {
			t.Fatal("ambient selector inherited", key)
		}
	}
	t.Logf("fixture child PID %.0f reaped", got["child"])
}

// Exact capacities are transport successes, including stderr capacity. The
// observation tests separately prove ANY stderr refuses successful facts.
// Overflows/failure/cancellation return zero bytes; no child diagnostics leak.
func TestSelfCardExecLimits(t *testing.T) {
	for _, mode := range []string{"stdout-limit", "stderr-limit", "stdout-overflow", "stderr-overflow", "fail", "block", "nil-context", "bad-env", "duplicate-env", "wrong-caller", "missing-caller-env", "relative-executable", "relative-directory"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, _ := selfCardFixture(t)
			cmd := selfCardExecCommand(t, s, mode)
			switch mode {
			case "block":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
				defer cancel()
			case "nil-context":
				ctx = nil
			case "bad-env":
				cmd.Environment = append(cmd.Environment, "CODEX_HOME=/forbidden")
				cmd.Args[len(cmd.Args)-1] = "public"
			case "duplicate-env":
				cmd.Environment = append(cmd.Environment, "A2A_AGENT=fixture.self")
				cmd.Args[len(cmd.Args)-1] = "public"
			case "wrong-caller":
				cmd.Caller = "other"
				cmd.Args[len(cmd.Args)-1] = "public"
			case "missing-caller-env":
				cmd.Environment = cmd.Environment[:len(cmd.Environment)-1]
				cmd.Args[len(cmd.Args)-1] = "public"
			case "relative-executable":
				cmd.Executable = "fixture"
			case "relative-directory":
				cmd.Directory = "."
			}
			start := time.Now()
			out, err := (SelfCardExecRunner{}).Run(ctx, cmd)
			if mode == "stdout-limit" {
				if err != nil || !bytes.Equal(out.Stdout, bytes.Repeat([]byte("x"), MaxSelfCardSourceOutput)) || len(out.Stderr) != 0 {
					t.Fatal("exact stdout capacity refused", err)
				}
				return
			}
			if mode == "stderr-limit" {
				if err != nil || string(out.Stdout) != "ok" || !bytes.Equal(out.Stderr, bytes.Repeat([]byte("x"), maxSelfCardSourceStderr)) {
					t.Fatal("exact stderr capacity refused", err)
				}
				return
			}
			if err == nil || out.Stdout != nil || out.Stderr != nil || strings.Contains(err.Error(), "private") {
				t.Fatal("failure returned partial/raw output", err)
			}
			if mode == "block" && (ctx.Err() != context.DeadlineExceeded || time.Since(start) > 3*time.Second) {
				t.Fatal("child deadline not bounded", ctx.Err())
			}
		})
	}
}
