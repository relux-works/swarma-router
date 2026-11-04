package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type taskRunnerFunc func(context.Context, TaskFactsCommand) ([]byte, error)

func (f taskRunnerFunc) Run(ctx context.Context, c TaskFactsCommand) ([]byte, error) {
	return f(ctx, c)
}

func taskMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func taskConfig(s TaskBoardFacts) map[string]any {
	return map[string]any{"effective_mode": "local", "configured_mode": "local", "project_root": s.ProjectRoot, "explicit_remote_override": false, "local": map[string]any{"active": true, "board_dir": s.BoardDir}, "remote": map[string]any{"active": false}}
}
func taskFields(s TaskBoardFacts) map[string]any {
	return map[string]any{"id": s.AdmittedTaskID, "title": s.AdmittedTaskID + ": native task title", "status": "backlog", "assignee": ""}
}
func taskFixture(t *testing.T) (TaskBoardFacts, context.Context) {
	t.Helper()
	root := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := TaskBoardFacts{Executable: "/trusted/task-board", ProjectRoot: root, BoardDir: filepath.Join(root, ".task-board"), AdmittedTaskID: "TASK-261104-fixture", Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "LC_ALL": "C", "TMPDIR": os.TempDir()}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return s, ctx
}
func taskResult(t *testing.T, got TaskObservation, err error, reason string) {
	t.Helper()
	accessReason(t, err, reason)
	if reason != "" && got != (TaskObservation{}) {
		t.Fatalf("failure returned partial facts: %#v", got)
	}
}

// A successful partial read preserves all four facts and hashes the exact task
// object, with one get, fixed argv/cwd/board and a shared bounded context. Extra
// config metadata is legitimate; none is part of the returned task digest.
func TestTaskFactsObservationAndBinding(t *testing.T) {
	for _, mode := range []string{"absolute", "relative", "unicode", "unknown-status", "whitespace", "escaped-id", "max-task"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx := taskFixture(t)
			config, fields := taskConfig(s), taskFields(s)
			if mode == "relative" {
				config["local"].(map[string]any)["board_dir"] = ".task-board"
			}
			if mode == "unicode" {
				fields["title"], fields["assignee"] = "native \x00\nթեստ 世界", "operator\tβ"
			}
			if mode == "unknown-status" {
				fields["status"] = "unfamiliar-native-status"
			}
			if mode == "max-task" {
				fields["title"] = strings.Repeat("x", MaxTaskProjectionBytes-len(taskMarshal(t, fields))+len(fields["title"].(string)))
			}
			config["unrelated"] = map[string]any{"numbers": []int{0, 1, 2}, "many": make([]string, 80)}
			rawConfig, rawTask := taskMarshal(t, config), taskMarshal(t, fields)
			if mode == "whitespace" {
				rawTask = bytes.Replace(rawTask, []byte(`,"status"`), []byte(", \n\t\"status\""), 1)
			}
			if mode == "escaped-id" {
				rawTask = bytes.Replace(rawTask, []byte(`"id"`), []byte(`"\u0069d"`), 1)
			}
			calls := []TaskFactsCommand{}
			var shared context.Context
			s.Runner = taskRunnerFunc(func(c context.Context, cmd TaskFactsCommand) ([]byte, error) {
				calls = append(calls, cmd)
				deadline, ok := c.Deadline()
				if !ok || time.Until(deadline) > 29*time.Second || c.Err() != nil {
					t.Fatal("missing aggregate command bound")
				}
				if shared != nil && shared != c {
					t.Fatal("command budget was reset")
				}
				shared = c
				if len(calls) == 1 {
					return rawConfig, nil
				}
				return append(append(append([]byte("["), rawConfig...), ','), append(rawTask, ']')...), nil
			})
			got, err := s.Observe(ctx, s.AdmittedTaskID)
			taskResult(t, got, err, "")
			want := TaskFacts{fields["id"].(string), fields["title"].(string), fields["status"].(string), fields["assignee"].(string)}
			if got.Facts != want || got.ProjectionSHA256 != hash(rawTask) || len(calls) != 2 {
				t.Fatal("exact task projection/digest/call count changed")
			}
			env, _ := taskEnvironment(s.Environment)
			for i, call := range calls {
				query := "project_config()"
				if i == 1 {
					query = `project_config(); get("TASK-261104-fixture") { id title status assignee }`
				}
				if call.Executable != s.Executable || call.Directory != s.ProjectRoot || !reflect.DeepEqual(call.Environment, env) || !reflect.DeepEqual(call.Args, []string{"--board-dir", s.BoardDir, "q", "--format", "json", query}) {
					t.Fatalf("source command changed: %#v", call)
				}
			}
			for _, source := range []any{s, &s} {
				if _, implementsOwner := source.(OwnerReader); implementsOwner {
					t.Fatal("partial task facts acquired a positive OwnerReader bridge")
				}
			}
		})
	}
}

// Preflight refuses unknown/foreign local authority before any get. Binding
// changes in the batch also refuse. Readable config metadata cannot override
// missing/wrong/null canonical predicates or become a fallback remote source.
func TestTaskFactsConfigRefusals(t *testing.T) {
	for _, stage := range []string{"preflight", "batch"} {
		for _, mode := range []string{"remote", "configured", "root", "board", "local-active", "remote-active", "override", "null", "missing", "type", "alias", "duplicate", "error"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				s, ctx := taskFixture(t)
				good := taskMarshal(t, taskConfig(s))
				config := taskConfig(s)
				want := "source_conflict"
				switch mode {
				case "remote":
					config["effective_mode"] = "remote"
				case "configured":
					config["configured_mode"] = "auto"
				case "root":
					config["project_root"] = s.ProjectRoot + "-other"
				case "board":
					config["local"].(map[string]any)["board_dir"] = "other"
				case "local-active":
					config["local"].(map[string]any)["active"] = false
				case "remote-active":
					config["remote"].(map[string]any)["active"] = true
				case "override":
					config["explicit_remote_override"] = true
				case "null":
					config["explicit_remote_override"] = nil
				case "missing":
					delete(config, "explicit_remote_override")
				case "type":
					config["local"].(map[string]any)["active"] = "true"
				case "alias":
					delete(config, "configured_mode")
					config["Configured_mode"] = "local"
				case "error":
					config = map[string]any{"error": map[string]any{"message": "private failure"}}
					want = "source_unavailable"
				}
				bad := taskMarshal(t, config)
				if mode == "duplicate" {
					bad = bytes.Replace(bad, []byte(`"effective_mode":"local"`), []byte(`"effective_mode":"local","effective_mode":"local"`), 1)
					want = "source_unavailable"
				}
				calls := 0
				s.Runner = taskRunnerFunc(func(context.Context, TaskFactsCommand) ([]byte, error) {
					calls++
					if stage == "preflight" {
						return bad, nil
					}
					if calls == 1 {
						return good, nil
					}
					return taskMarshal(t, []json.RawMessage{bad, taskMarshal(t, taskFields(s))}), nil
				})
				got, err := s.Observe(ctx, s.AdmittedTaskID)
				taskResult(t, got, err, want)
				wantCalls := 1
				if stage == "batch" {
					wantCalls = 2
				}
				if calls != wantCalls {
					t.Fatal("source retried or queried task after failed preflight")
				}
			})
		}
	}
}

// Exit zero with an error envelope or malformed/foreign task is not success.
// All failures return zero facts, not a usable partial projection or authority.
func TestTaskFactsProjectionRefusals(t *testing.T) {
	for _, mode := range []string{"wrong-id", "missing", "name", "unknown", "alias", "null", "number", "duplicate", "escaped-duplicate", "invalid-utf8", "surrogate", "trailing", "truncated", "error", "absent", "batch-short", "batch-long", "oversize-task", "oversize-output", "run-error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx := taskFixture(t)
			config := taskMarshal(t, taskConfig(s))
			fields := taskFields(s)
			want := "source_unavailable"
			switch mode {
			case "wrong-id":
				fields["id"] = "TASK-261104-other"
				want = "source_conflict"
			case "missing":
				delete(fields, "assignee")
			case "name":
				delete(fields, "title")
				fields["name"] = "native-task-title"
			case "unknown":
				fields["revision"] = "invented"
			case "alias":
				delete(fields, "title")
				fields["Title"] = "native title"
			case "null":
				fields["assignee"] = nil
			case "number":
				fields["status"] = 1
			case "error":
				fields = map[string]any{"error": map[string]any{"message": "private diagnostic"}}
			case "oversize-task":
				fields["title"] = strings.Repeat("x", MaxTaskProjectionBytes)
			}
			raw := taskMarshal(t, fields)
			if mode == "duplicate" {
				raw = bytes.Replace(raw, []byte(`"id":`), []byte(`"id":"wrong","id":`), 1)
			}
			if mode == "escaped-duplicate" {
				raw = bytes.Replace(raw, []byte(`"id":`), []byte(`"\u0069d":"wrong","id":`), 1)
			}
			if mode == "surrogate" {
				raw = bytes.Replace(raw, []byte(`"assignee":""`), []byte(`"assignee":"\ud800"`), 1)
			}
			batch := taskMarshal(t, []json.RawMessage{config, raw})
			switch mode {
			case "invalid-utf8":
				batch = append(batch, 0xff)
			case "trailing":
				batch = append(batch, []byte(` {}`)...)
			case "truncated":
				batch = batch[:len(batch)-2]
			case "absent":
				batch = append(append([]byte("["), config...), []byte(",null]")...)
			case "batch-short":
				batch = taskMarshal(t, []json.RawMessage{config})
			case "batch-long":
				batch = taskMarshal(t, []json.RawMessage{config, raw, raw})
			case "oversize-output":
				batch = bytes.Repeat([]byte(" "), MaxTaskSourceOutput+1)
			}
			calls := 0
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				s.Runner = taskRunnerFunc(func(context.Context, TaskFactsCommand) ([]byte, error) { calls++; cancel(); return config, nil })
				want = "authority_unknown"
			} else {
				s.Runner = taskRunnerFunc(func(context.Context, TaskFactsCommand) ([]byte, error) {
					calls++
					if calls == 1 {
						return config, nil
					}
					if mode == "run-error" {
						return batch, errors.New("private diagnostic")
					}
					return batch, nil
				})
			}
			got, err := s.Observe(ctx, s.AdmittedTaskID)
			taskResult(t, got, err, want)
			wantCalls := 2
			if mode == "cancel" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("unexpected retry/fallback")
			}
		})
	}
}

// Trusted-input rejection happens before any command. Positive public env is
// explicit: selectors, native identity and credential-like keys are never passed.
func TestTaskFactsTrustedInputs(t *testing.T) {
	for _, mode := range []string{"wrong-task", "nil-context", "unbounded", "canceled", "empty-id", "relative-exe", "relative-root", "relative-board", "nil-runner", "missing-env", "selector", "identity", "native-home", "credential", "duplicate-path", "relative-path", "fake-home", "nul-env", "locale"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx := taskFixture(t)
			id := s.AdmittedTaskID
			want := "source_unavailable"
			calls := 0
			s.Runner = taskRunnerFunc(func(context.Context, TaskFactsCommand) ([]byte, error) {
				calls++
				t.Fatal("rejected input executed source")
				return nil, nil
			})
			switch mode {
			case "wrong-task":
				id += `"); list(); get("x`
				want = "authority_denied"
			case "nil-context":
				ctx = nil
				want = "authority_unknown"
			case "unbounded":
				ctx = context.Background()
				want = "authority_unknown"
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
				want = "authority_unknown"
			case "empty-id":
				id = ""
				s.AdmittedTaskID = ""
			case "relative-exe":
				s.Executable = "task-board"
			case "relative-root":
				s.ProjectRoot = "relative"
			case "relative-board":
				s.BoardDir = ".task-board"
			case "nil-runner":
				s.Runner = nil
			case "missing-env":
				s.Environment = nil
			case "selector":
				s.Environment["TASK_BOARD_REMOTE"] = "https://fixture.invalid"
			case "identity":
				s.Environment["A2A_AGENT"] = "fixture"
			case "native-home":
				s.Environment["CODEX_HOME"] = "/fixture"
			case "credential":
				s.Environment["AUTH_TOKEN"] = "fixture-not-a-secret"
			case "duplicate-path":
				s.Environment["PATH"] = "/usr/bin::/bin"
			case "relative-path":
				s.Environment["PATH"] = "/usr/bin:relative"
			case "fake-home":
				s.Environment["HOME"] = "/fixture-not-actual-home"
			case "nul-env":
				s.Environment["LANG"] = "C\x00"
			case "locale":
				s.Environment["LANG"] = "not a locale"
			}
			got, err := s.Observe(ctx, id)
			taskResult(t, got, err, want)
			if calls != 0 {
				t.Fatal("source called on rejected config")
			}
		})
	}
}
