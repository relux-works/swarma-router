package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"relux.works/swarma-router/handoff"
)

const MaxTaskSourceOutput = 2 * 1024 * 1024
const MaxTaskProjectionBytes = 65536
const maxTaskSourceStderr = 64 * 1024

// TaskFactsCommand is fixed by trusted source configuration, never request JSON.
// A runner completes synchronously and must not retain mutable arguments.
type TaskFactsCommand struct {
	Executable, Directory string
	Args, Environment     []string
}

type TaskFactsRunner interface {
	Run(context.Context, TaskFactsCommand) ([]byte, error)
}

// TaskBoardFacts observes one explicitly admitted local native task. Configure
// before use, do not mutate concurrently. Runner is an admitted implementation;
// no executable, source or environment is discovered as a fallback.
type TaskBoardFacts struct {
	Executable, ProjectRoot, BoardDir, AdmittedTaskID string
	Environment                                       map[string]string
	Runner                                            TaskFactsRunner
}

var _ TaskFactsSource = TaskBoardFacts{}

func taskAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0)
}

// taskEnvironment accepts only public process settings. HOME is the actual
// caller home, not a fabricated native home. No ambient vector is copied.
func taskEnvironment(in map[string]string) ([]string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || in["HOME"] != home || !taskAbsolute(home) || in["PATH"] == "" {
		return nil, false
	}
	keys := []string{}
	for key, value := range in {
		if strings.ContainsRune(value, 0) || value == "" {
			return nil, false
		}
		switch key {
		case "HOME":
		case "PATH":
			for _, entry := range strings.Split(value, ":") {
				if !taskAbsolute(entry) {
					return nil, false
				}
			}
		case "TMPDIR":
			if !filepath.IsAbs(value) {
				return nil, false
			}
		case "LANG", "LC_ALL":
			for _, c := range value {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.@-", c)) {
					return nil, false
				}
			}
		default:
			return nil, false
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+in[key])
	}
	return out, true
}

// Observe performs one local-binding preflight and one batch with exactly one
// task get. No successful observation is a verified task revision or allow gate.
func (s TaskBoardFacts) Observe(ctx context.Context, id string) (TaskObservation, error) {
	zero := TaskObservation{}
	if !policyContext(ctx) {
		return zero, Refusal{"authority_unknown"}
	}
	if id != s.AdmittedTaskID {
		return zero, Refusal{"authority_denied"}
	}
	env, ok := taskEnvironment(s.Environment)
	if !ok || s.Runner == nil || !taskAbsolute(s.Executable) || !taskAbsolute(s.ProjectRoot) || !taskAbsolute(s.BoardDir) || !ref(s.AdmittedTaskID) {
		return zero, Refusal{"source_unavailable"}
	}
	// Use only the DSL's documented literal escapes. A native ID that needs
	// unsupported Go escapes is unavailable; the public wire Ref stays unchanged.
	quoted := strconv.Quote(s.AdmittedTaskID)
	for i := 0; i < len(quoted); i++ {
		if quoted[i] == '\\' {
			i++
			if !strings.ContainsRune("\\\"nt", rune(quoted[i])) {
				return zero, Refusal{"source_unavailable"}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 29*time.Second)
	defer cancel()
	read := func(query string) ([]byte, error) {
		if !policyContext(ctx) {
			return nil, Refusal{"authority_unknown"}
		}
		out, err := s.Runner.Run(ctx, TaskFactsCommand{Executable: s.Executable, Directory: s.ProjectRoot, Args: []string{"--board-dir", s.BoardDir, "q", "--format", "json", query}, Environment: append([]string{}, env...)})
		if !policyContext(ctx) {
			return nil, Refusal{"authority_unknown"}
		}
		if err != nil || len(out) > MaxTaskSourceOutput {
			return nil, Refusal{"source_unavailable"}
		}
		return out, nil
	}
	out, err := read("project_config()")
	if err != nil {
		return zero, err
	}
	if err = s.localBinding(out); err != nil {
		return zero, err
	}
	out, err = read("project_config(); get(" + quoted + ") { id title status assignee }")
	if err != nil {
		return zero, err
	}
	var pair []json.RawMessage
	if taskSourceJSON(out, &pair) != nil || len(pair) != 2 {
		return zero, Refusal{"source_unavailable"}
	}
	if err = s.localBinding(pair[0]); err != nil {
		return zero, err
	}
	var fields map[string]json.RawMessage
	if len(pair[1]) > MaxTaskProjectionBytes || taskSourceJSON(pair[1], &fields) != nil || len(fields) != 4 {
		return zero, Refusal{"source_unavailable"}
	}
	var facts TaskFacts
	for _, field := range []struct {
		key   string
		value *string
	}{{"id", &facts.ID}, {"title", &facts.Title}, {"status", &facts.Status}, {"assignee", &facts.Assignee}} {
		var value *string
		if json.Unmarshal(fields[field.key], &value) != nil || value == nil {
			return zero, Refusal{"source_unavailable"}
		}
		*field.value = *value
	}
	if facts.ID != id {
		return zero, Refusal{"source_conflict"}
	}
	if !policyContext(ctx) {
		return zero, Refusal{"authority_unknown"}
	}
	return TaskObservation{Facts: facts, ProjectionSHA256: hash(pair[1])}, nil
}

func taskSourceJSON(raw []byte, out any) error {
	if !scalarEscapes(raw) {
		return errors.New("invalid source JSON")
	}
	return handoff.Decode(raw, out)
}

func (s TaskBoardFacts) localBinding(raw []byte) error {
	var fields map[string]json.RawMessage
	if taskSourceJSON(raw, &fields) != nil || fields["error"] != nil {
		return Refusal{"source_unavailable"}
	}
	var mode, configured, root *string
	var override *bool
	var local, remote map[string]json.RawMessage
	if json.Unmarshal(fields["effective_mode"], &mode) != nil || json.Unmarshal(fields["configured_mode"], &configured) != nil || json.Unmarshal(fields["project_root"], &root) != nil || json.Unmarshal(fields["explicit_remote_override"], &override) != nil || json.Unmarshal(fields["local"], &local) != nil || json.Unmarshal(fields["remote"], &remote) != nil {
		return Refusal{"source_conflict"}
	}
	var localActive, remoteActive *bool
	var dir *string
	if json.Unmarshal(local["active"], &localActive) != nil || json.Unmarshal(local["board_dir"], &dir) != nil || json.Unmarshal(remote["active"], &remoteActive) != nil || mode == nil || configured == nil || root == nil || override == nil || localActive == nil || remoteActive == nil || dir == nil || *dir == "" {
		return Refusal{"source_conflict"}
	}
	board := *dir
	if !filepath.IsAbs(board) {
		board = filepath.Join(s.ProjectRoot, board)
	}
	if *mode != "local" || *configured != "local" || *root != s.ProjectRoot || *override || !*localActive || *remoteActive || filepath.Clean(board) != s.BoardDir {
		return Refusal{"source_conflict"}
	}
	return nil
}

type taskFactsBuffer struct {
	b     bytes.Buffer
	limit int
}

func (b *taskFactsBuffer) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.b.Len() {
		return 0, errors.New("source output limit")
	}
	return b.b.Write(raw)
}

// TaskFactsExecRunner executes trusted command data directly, with an explicit
// environment and bounded pipes. It never invokes a shell or copies ambient env.
// Observe supplies the shared 29s context; pipe cleanup adds at most one second.
type TaskFactsExecRunner struct{}

func (TaskFactsExecRunner) Run(ctx context.Context, command TaskFactsCommand) ([]byte, error) {
	if !policyContext(ctx) || !taskAbsolute(command.Executable) || !taskAbsolute(command.Directory) || command.Environment == nil {
		return nil, errors.New("source command unavailable")
	}
	public := map[string]string{}
	for _, entry := range command.Environment {
		key, value, ok := strings.Cut(entry, "=")
		_, exists := public[key]
		if !ok || exists {
			return nil, errors.New("source command unavailable")
		}
		public[key] = value
	}
	if _, ok := taskEnvironment(public); !ok {
		return nil, errors.New("source command unavailable")
	}
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = command.Directory, append([]string{}, command.Environment...), time.Second
	stdout, stderr := &taskFactsBuffer{limit: MaxTaskSourceOutput}, &taskFactsBuffer{limit: maxTaskSourceStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("source command unavailable")
	}
	return stdout.b.Bytes(), nil
}
