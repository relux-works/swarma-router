package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type commandCall struct {
	name string
	args []string
}
type scriptedRunner struct {
	calls []commandCall
	run   func(string, []string) ([]byte, error)
}

func (r *scriptedRunner) Run(_ context.Context, n string, a ...string) ([]byte, error) {
	r.calls = append(r.calls, commandCall{n, append([]string{}, a...)})
	return r.run(n, a)
}

// The real system adapter emits a direct multi-argument tmux command, binding the
// operation at creation. A shell string, -A or send-keys would fail this trace.
func TestSystemCreateTrace(t *testing.T) {
	i := intent()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	i.Workspace = real
	r := &scriptedRunner{run: func(string, []string) ([]byte, error) { return nil, nil }}
	b := SystemBackend{Runner: r}
	cmd := []string{"/usr/bin/env", "-i", "HOME=/home/operator", "/tool/curator", "run", "codex_cli", "--native"}
	if err := b.Create(context.Background(), i, cmd); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"new-session", "-d", "-s", i.TmuxSession, "-c", real, "-e", "ROUTER_LAUNCH_OPERATION=" + i.Digest(), "--"}, cmd...)
	if len(r.calls) != 1 || r.calls[0].name != "tmux" || !reflect.DeepEqual(r.calls[0].args, want) {
		t.Fatalf("%+v", r.calls)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	i.Workspace = link
	if err := b.Create(context.Background(), i, cmd); err == nil || len(r.calls) != 1 {
		t.Fatal("symlink workspace executed")
	}
}

func metadataRunner(i Intent) *scriptedRunner {
	return &scriptedRunner{run: func(n string, a []string) ([]byte, error) {
		if n == "tmux" {
			switch a[0] {
			case "list-sessions":
				return []byte(i.TmuxSession + "\n"), nil
			case "show-environment":
				return []byte("ROUTER_LAUNCH_OPERATION=" + i.Digest() + "\n"), nil
			case "display-message":
				return []byte(i.TmuxSession + "\t%1\t100\n"), nil
			}
		}
		if n == "ps" {
			pid := a[len(a)-1]
			parent, name := "1", "curator"
			if pid == "101" {
				parent, name = "100", "curator-run"
			}
			if pid == "102" {
				parent, name = "101", "codex"
			}
			return []byte(fmt.Sprintf("%s %s Mon Nov 2 12:00:00 2026 /tool/%s\n", pid, parent, name)), nil
		}
		if n == "pgrep" {
			switch a[1] {
			case "100":
				return []byte("101\n"), nil
			case "101":
				return []byte("102\n"), nil
			default:
				return nil, CommandError{1}
			}
		}
		return nil, errors.New("unexpected command")
	}}
}

// A complete target-only lookup and bounded child traversal establish process
// metadata, not liveness; failed/malformed tools must not establish absence.
func TestSystemMetadataTrace(t *testing.T) {
	i := intent()
	runner := metadataRunner(i)
	b := SystemBackend{Runner: runner}
	s, found, err := b.Snapshot(context.Background(), i.TmuxSession)
	if err != nil || !found || len(s.Processes) != 3 || s.Processes[2].PID != 102 || s.OperationDigest != i.Digest() {
		t.Fatalf("%+v %v", s, err)
	}
	if !reflect.DeepEqual(runner.calls[0].args, []string{"list-sessions", "-F", "#{session_name}", "-f", "#{==:#{session_name}," + i.TmuxSession + "}"}) {
		t.Fatal(runner.calls[0])
	}
	for _, kind := range []string{"no_server", "extra_session", "tag", "pane", "ps", "child_error", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			r := metadataRunner(i)
			base := r.run
			r.run = func(n string, a []string) ([]byte, error) {
				switch kind {
				case "no_server":
					if n == "tmux" && a[0] == "list-sessions" {
						return nil, CommandError{1}
					}
				case "extra_session":
					if n == "tmux" && a[0] == "list-sessions" {
						return []byte("another\n"), nil
					}
				case "tag":
					if n == "tmux" && a[0] == "show-environment" {
						return []byte("-ROUTER_LAUNCH_OPERATION\n"), nil
					}
				case "pane":
					if n == "tmux" && a[0] == "display-message" {
						return []byte("wrong\t%1\t100\n"), nil
					}
				case "ps":
					if n == "ps" {
						return []byte("100 invalid"), nil
					}
				case "child_error":
					if n == "pgrep" {
						return nil, CommandError{3}
					}
				case "cycle":
					if n == "pgrep" {
						return []byte("100\n"), nil
					}
				}
				return base(n, a)
			}
			_, present, err := (SystemBackend{Runner: r}).Snapshot(context.Background(), i.TmuxSession)
			if err == nil {
				t.Fatalf("invalid projection admitted present=%v", present)
			}
		})
	}
	r := &scriptedRunner{run: func(string, []string) ([]byte, error) { return nil, nil }}
	_, present, err := (SystemBackend{Runner: r}).Snapshot(context.Background(), i.TmuxSession)
	if present || err != nil || len(r.calls) != 1 {
		t.Fatal(present, err)
	}
}

// Unsupported executables never execute, and native secrets cannot appear in the
// public helper error. No actual tmux session or provider is started by this test.
func TestExecutorAllowlist(t *testing.T) {
	_, err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "PRIVATE")
	if err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal(err)
	}
}

// The concrete executor reads only this test process's non-secret identity, and
// a cancelled context returns a sanitized failure. This starts no tmux/provider.
func TestMetadataExecutor(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	data, err := (ExecRunner{}).Run(context.Background(), "ps", "-o", "pid=", "-p", pid)
	if err != nil || strings.TrimSpace(string(data)) != pid {
		t.Fatalf("own process lookup failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (ExecRunner{}).Run(ctx, "ps", "PRIVATE"); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("cancelled metadata lookup did not fail safely")
	}
}

// The bounded output writer accepts its exact limit and refuses the next byte.
func TestMetadataOutputBound(t *testing.T) {
	b := &limitedOutput{}
	if n, err := b.Write(make([]byte, 32768)); n != 32768 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := b.Write([]byte("x")); n != 0 || err == nil || b.Len() != 32768 {
		t.Fatal(n, err)
	}
}
