package launch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type CommandError struct{ Code int }

func (e CommandError) Error() string { return "metadata command failed" }

// ExecRunner executes only fixed metadata/tmux tools, with bounded output and no
// shell. A timeout of the tmux client is not cancellation of a launched member.
type ExecRunner struct{}
type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > 32768-b.Len() {
		return 0, errors.New("output too large")
	}
	return b.Buffer.Write(p)
}
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "tmux" && name != "ps" && name != "pgrep" {
		return nil, errors.New("unsupported metadata tool")
	}
	ctx, cancel := context.WithTimeout(ctx, 29*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New("home unavailable")
	}
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.Getenv("TMPDIR"), "LC_ALL=C"}
	for _, k := range []string{"TMUX", "TMUX_TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdout, stderr := &limitedOutput{}, &limitedOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		var ex *exec.ExitError
		if errors.As(err, &ex) {
			return nil, CommandError{ex.ExitCode()}
		}
		return nil, errors.New("metadata command unavailable")
	}
	return stdout.Bytes(), nil
}

// SystemBackend is the concrete default-server tmux/process adapter. Its
// successful filtered list is a complete target-only absence observation. A
// missing/unreachable server is unknown; it never creates a server as a probe.
type SystemBackend struct{ Runner CommandRunner }

func (b SystemBackend) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if b.Runner == nil {
		return nil, errors.New("runner unavailable")
	}
	return b.Runner.Run(ctx, name, args...)
}
func (b SystemBackend) Create(ctx context.Context, i Intent, command []string) error {
	real, err := filepath.EvalSymlinks(i.Workspace)
	if err != nil || real != i.Workspace {
		return errors.New("workspace unavailable")
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return errors.New("workspace unavailable")
	}
	args := []string{"new-session", "-d", "-s", i.TmuxSession, "-c", i.Workspace, "-e", "ROUTER_LAUNCH_OPERATION=" + i.Digest(), "--"}
	args = append(args, command...)
	_, err = b.run(ctx, "tmux", args...)
	return err
}
func (b SystemBackend) Snapshot(ctx context.Context, name string) (Snapshot, bool, error) {
	if !sessionName.MatchString(name) {
		return Snapshot{}, false, errors.New("invalid session")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := b.run(ctx, "tmux", "list-sessions", "-F", "#{session_name}", "-f", "#{==:#{session_name},"+name+"}")
	if err != nil {
		return Snapshot{}, false, errors.New("session query unavailable")
	}
	if strings.TrimSpace(string(data)) == "" {
		return Snapshot{}, false, nil
	}
	if string(data) != name+"\n" {
		return Snapshot{}, false, errors.New("session query mismatch")
	}
	s := Snapshot{Session: name}
	data, err = b.run(ctx, "tmux", "show-environment", "-t", "="+name, "ROUTER_LAUNCH_OPERATION")
	if err != nil {
		return s, true, errors.New("operation binding unavailable")
	}
	tag := strings.TrimSuffix(string(data), "\n")
	const prefix = "ROUTER_LAUNCH_OPERATION="
	if !strings.HasPrefix(tag, prefix) || !digestPattern.MatchString(strings.TrimPrefix(tag, prefix)) {
		return s, true, errors.New("operation binding invalid")
	}
	s.OperationDigest = strings.TrimPrefix(tag, prefix)
	data, err = b.run(ctx, "tmux", "display-message", "-p", "-t", "="+name, "#{session_name}\t#{pane_id}\t#{pane_pid}")
	if err != nil {
		return s, true, errors.New("pane unavailable")
	}
	parts := strings.Split(strings.TrimSuffix(string(data), "\n"), "\t")
	if len(parts) != 3 || parts[0] != name || !panePattern.MatchString(parts[1]) {
		return s, true, errors.New("pane mismatch")
	}
	s.Pane = parts[1]
	s.PanePID, err = strconv.Atoi(parts[2])
	if err != nil || s.PanePID <= 0 {
		return s, true, errors.New("invalid pane process")
	}
	queue := []int{s.PanePID}
	seen := map[int]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] || len(seen) >= 32 {
			return s, true, errors.New("process projection too large")
		}
		seen[pid] = true
		data, err = b.run(ctx, "ps", "-o", "pid=", "-o", "ppid=", "-o", "lstart=", "-o", "comm=", "-p", strconv.Itoa(pid))
		if err != nil {
			return s, true, errors.New("process unavailable")
		}
		fields := strings.Fields(string(data))
		if len(fields) < 8 {
			return s, true, errors.New("process metadata invalid")
		}
		actual, e1 := strconv.Atoi(fields[0])
		parent, e2 := strconv.Atoi(fields[1])
		if e1 != nil || e2 != nil || actual != pid || parent <= 0 {
			return s, true, errors.New("process identity mismatch")
		}
		s.Processes = append(s.Processes, Process{PID: pid, ParentPID: parent, Birth: strings.Join(fields[2:7], " "), Executable: strings.Join(fields[7:], " ")})
		data, err = b.run(ctx, "pgrep", "-P", strconv.Itoa(pid))
		if err != nil {
			var ex CommandError
			if errors.As(err, &ex) && ex.Code == 1 {
				continue
			}
			return s, true, errors.New("child query unavailable")
		}
		for _, value := range strings.Fields(string(data)) {
			child, e := strconv.Atoi(value)
			if e != nil || child <= 0 {
				return s, true, errors.New("invalid child identity")
			}
			queue = append(queue, child)
		}
	}
	return s, true, nil
}
