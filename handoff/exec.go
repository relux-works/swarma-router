package handoff

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Runner interface {
	Run(context.Context, string, string, ...string) ([]byte, error)
}

// ExecRunner only executes fixed supported tools; it never invokes a shell.
// Its environment retains the caller's already admitted a2a identity.
type ExecRunner struct{}

type boundedBuffer struct {
	b     bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.b.Len() {
		return 0, errors.New("adapter output too large")
	}
	return b.b.Write(p)
}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	if name != "a2a" && name != "task-board" {
		return nil, errors.New("unsupported tool")
	}
	// Reserve one second of the 30s bound for closing inherited output pipes.
	ctx, cancel := context.WithTimeout(ctx, 29*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	if name == "task-board" {
		cmd.Env = boardEnvironment(os.Environ())
	}

	stdout := &boundedBuffer{limit: 2 * 1024 * 1024}
	stderr := &boundedBuffer{limit: 64 * 1024}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("adapter command failed")
	}
	return stdout.b.Bytes(), nil
}

func boardEnvironment(env []string) []string {
	clean := []string{}
	for _, item := range env {
		k, _, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(k, "TASK_BOARD_") && !strings.HasPrefix(k, "TB_") {
			clean = append(clean, item)
		}
	}
	return clean
}
