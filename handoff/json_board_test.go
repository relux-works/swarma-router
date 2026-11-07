package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jsonBoardInputs(t *testing.T) (Request, Policy, []byte) {
	t.Helper()
	r, p := configuredInputs()
	r.Action = "plan"
	p.Tasks[0].Members = []string{r.Member}
	p.ProjectRoot = realTemp(t)
	p.BoardDir = filepath.Join(p.ProjectRoot, "board")
	if err := os.Mkdir(p.BoardDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := encoded(keeperAssignmentV1{ID: r.TaskID, Title: r.TaskTitle, AccountableOwner: p.Sender, AssignedTo: r.Member, AuthorityEvent: "$authority", CreatedAt: "2025-01-01T12:00:00Z", Scope: "One bounded offline validation", Status: "ASSIGNED"})
	p.JSONBoard = &JSONBoardPolicy{Schema: "keeper-assignment/1", Context: JSONBoardContext{Project: p.Project, ProjectRoot: p.ProjectRoot, BoardDir: p.BoardDir, RoomAlias: "#" + p.Room, RoomID: p.RoomID, Topic: p.Topic, ReplyTo: p.ReplyTo}, Elements: []JSONBoardElement{{ID: r.TaskID, File: r.TaskID + ".json", AssignedTo: r.Member}}}
	writeJSONBoard(t, &p, data)
	return r, p, data
}

func writeJSONBoard(t *testing.T, p *Policy, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File), data, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	p.JSONBoard.Elements[0].SHA256 = hex.EncodeToString(h[:])
}

// These responses are synthetic controls, never captured runtime qualification.
func jsonMessagingRunner(t *testing.T, r Request, p Policy, plant string, calls *int) Runner {
	return runnerFunc(func(_ context.Context, dir, tool string, args ...string) ([]byte, error) {
		*calls++
		if tool != "a2a" || dir != p.ProjectRoot {
			t.Fatalf("unexpected provider: %s %s", dir, tool)
		}
		if args[0] == "room" {
			members := []map[string]string{{"user_id": "@" + p.Sender + ":" + p.Server, "membership": "join"}, {"user_id": "@" + r.Member + ":" + p.Server, "membership": "join"}}
			if plant == "missing-recipient" {
				members = members[:1]
			}
			v := map[string]any{"type": "room-members", "room_id": p.RoomID, "caller": "@" + p.Sender + ":" + p.Server, "membership_filter": "join", "completeness": "complete-response", "members": members}
			if plant == "partial" {
				v["completeness"] = "partial"
			}
			return encoded(v), nil
		}
		if args[0] != "read" {
			t.Fatalf("effect in plan: %v", args)
		}
		v := map[string]string{"type": "message", "room_id": p.RoomID, "event_id": r.ReplyTo, "sender": p.Sender, "topic": r.Topic}
		if plant == "topic-snapshot" {
			v["type"] = "topic"
		}
		if plant == "reply-room" {
			v["room_id"] = "!other"
		}
		if plant == "drift-during-messaging" {
			if err := os.WriteFile(filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return encoded(v), nil
	})
}

func TestJSONBoardPlanAndMessagingGates(t *testing.T) {
	for _, tc := range []struct{ plant, want string }{{"valid", ""}, {"missing-recipient", "recipient_not_joined"}, {"partial", "membership_binding_mismatch"}, {"topic-snapshot", "reply_binding_mismatch"}, {"reply-room", "reply_binding_mismatch"}, {"drift-during-messaging", "board_element_changed"}} {
		t.Run(tc.plant, func(t *testing.T) {
			r, p, _ := jsonBoardInputs(t)
			calls := 0
			a := CLIAdapter{Runner: jsonMessagingRunner(t, r, p, tc.plant, &calls)}
			dir := filepath.Join(p.ProjectRoot, "journal")
			got := (Engine{Adapter: a, OpenJournal: func(string) (Journal, error) { t.Fatal("plan opened journal"); return nil, nil }}).Execute(context.Background(), r, p, dir)
			if tc.want == "" {
				if got.Status != "planned" || got.Reason != "" || got.Notice != notice(r) {
					t.Fatal(got)
				}
			} else if got.Status != "refused" || got.Reason != tc.want {
				t.Fatal(got)
			}
			if calls < 1 || calls > 2 {
				t.Fatal(calls)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("plan wrote journal", err)
			}
		})
	}
}

func TestJSONBoardSourceRefusalsBeforeMessaging(t *testing.T) {
	cases := []struct{ plant, want string }{
		{"changed", "board_element_changed"}, {"missing", "board_element_unavailable"}, {"oversize", "board_element_unavailable"},
		{"duplicate", "board_element_malformed"}, {"unknown", "board_element_malformed"}, {"alias", "board_element_malformed"}, {"type", "board_element_malformed"}, {"trailing", "board_element_malformed"}, {"null", "board_element_fields_missing"}, {"null-field", "board_element_fields_missing"}, {"missing-field", "board_element_fields_missing"},
		{"id", "board_element_mismatch"}, {"title", "board_element_mismatch"}, {"assignee", "board_element_mismatch"}, {"owner", "board_element_mismatch"}, {"status", "board_element_mismatch"}, {"time", "board_element_malformed"},
		{"leaf-symlink", "board_element_unavailable"}, {"board-symlink", "board_element_unavailable"}, {"root-symlink", "board_element_unavailable"}, {"hardlink", "board_element_unavailable"}, {"directory", "board_element_unavailable"}, {"fifo", "board_element_unavailable"}, {"writable-file", "board_element_unavailable"}, {"writable-board", "board_element_unavailable"}, {"writable-root", "board_element_unavailable"},
		{"traversal", "board_elements_invalid"}, {"absolute", "board_elements_invalid"}, {"other-file", "board_elements_invalid"}, {"duplicate-element", "board_elements_invalid"}, {"missing-element", "board_elements_invalid"}, {"hash-case", "board_elements_invalid"}, {"hash-invalid", "board_elements_invalid"}, {"unbound-assignee", "board_elements_invalid"}, {"multiple-members", "board_elements_invalid"},
		{"context-project", "board_context_mismatch"}, {"context-room", "board_context_mismatch"}, {"context-topic", "board_context_mismatch"}, {"context-reply", "board_context_mismatch"}, {"schema", "board_schema_not_supported"}, {"outside", "board_path_not_contained"},
	}
	for _, tc := range cases {
		t.Run(tc.plant, func(t *testing.T) {
			r, p, data := jsonBoardInputs(t)
			path := filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File)
			var task map[string]any
			if err := json.Unmarshal(data, &task); err != nil {
				t.Fatal(err)
			}
			switch tc.plant {
			case "changed":
				if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				writeJSONBoard(t, &p, []byte(strings.Repeat(" ", FileLimit+1)))
			case "duplicate":
				writeJSONBoard(t, &p, append([]byte(`{"id":"other",`), data[1:]...))
			case "unknown":
				task["project"] = "guessed"
				writeJSONBoard(t, &p, encoded(task))
			case "alias":
				task["ID"] = task["id"]
				writeJSONBoard(t, &p, encoded(task))
			case "type":
				task["assigned_to"] = []string{r.Member}
				writeJSONBoard(t, &p, encoded(task))
			case "trailing":
				writeJSONBoard(t, &p, append(data, []byte(" {}")...))
			case "null":
				writeJSONBoard(t, &p, []byte("null"))
			case "null-field":
				task["assigned_to"] = nil
				writeJSONBoard(t, &p, encoded(task))
			case "missing-field":
				delete(task, "assigned_to")
				writeJSONBoard(t, &p, encoded(task))
			case "id", "title", "assignee", "owner", "status", "time":
				field := map[string]string{"id": "id", "title": "title", "assignee": "assigned_to", "owner": "accountable_owner", "status": "status", "time": "created_at"}[tc.plant]
				task[field] = "different"
				writeJSONBoard(t, &p, encoded(task))
			case "leaf-symlink":
				other := filepath.Join(p.ProjectRoot, "other")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			case "board-symlink", "root-symlink":
				old := p.BoardDir
				if tc.plant == "root-symlink" {
					old = p.ProjectRoot
				}
				if err := os.Rename(old, old+"-real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(old+"-real", old); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Remove(old); err != nil {
						t.Error(err)
					}
					if err := os.Rename(old+"-real", old); err != nil {
						t.Error(err)
					}
				})
			case "hardlink":
				if err := os.Link(path, filepath.Join(p.ProjectRoot, "link")); err != nil {
					t.Fatal(err)
				}
			case "directory", "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if tc.plant == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					makeBoardFIFO(t, path)
				}
			case "writable-file":
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			case "writable-board":
				if err := os.Chmod(p.BoardDir, 0777); err != nil {
					t.Fatal(err)
				}
			case "writable-root":
				if err := os.Chmod(p.ProjectRoot, 0777); err != nil {
					t.Fatal(err)
				}
			case "traversal":
				p.JSONBoard.Elements[0].File = "../" + r.TaskID + ".json"
			case "absolute":
				p.JSONBoard.Elements[0].File = path
			case "other-file":
				p.JSONBoard.Elements[0].File = "other.json"
			case "duplicate-element":
				p.JSONBoard.Elements = append(p.JSONBoard.Elements, p.JSONBoard.Elements[0])
			case "missing-element":
				p.JSONBoard.Elements = nil
			case "hash-case":
				p.JSONBoard.Elements[0].SHA256 = strings.ToUpper(p.JSONBoard.Elements[0].SHA256)
			case "hash-invalid":
				p.JSONBoard.Elements[0].SHA256 = "no"
			case "unbound-assignee":
				p.JSONBoard.Elements[0].AssignedTo = "other"
			case "multiple-members":
				p.Tasks[0].Members = append(p.Tasks[0].Members, "other")
			case "context-project":
				p.JSONBoard.Context.Project = "other"
			case "context-room":
				p.JSONBoard.Context.RoomID = "!other"
			case "context-topic":
				p.JSONBoard.Context.Topic = "other"
			case "context-reply":
				p.JSONBoard.Context.ReplyTo = "$other"
			case "schema":
				p.JSONBoard.Schema = "unknown"
			case "outside":
				p.BoardDir = filepath.Dir(p.ProjectRoot)
				p.JSONBoard.Context.BoardDir = p.BoardDir
			}
			calls := 0
			a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) { calls++; return nil, nil })}
			got := (Engine{Adapter: a}).Execute(context.Background(), r, p, filepath.Join(p.ProjectRoot, "journal"))
			if got.Reason != tc.want || got.Status != "refused" || calls != 0 {
				t.Fatal(got, calls)
			}
		})
	}
}

func TestJSONBoardMissingContext(t *testing.T) {
	for _, field := range []string{"project", "project_root", "board_dir", "room_alias", "room_id", "topic", "reply_to"} {
		t.Run(field, func(t *testing.T) {
			r, p, _ := jsonBoardInputs(t)
			raw := encoded(p.JSONBoard.Context)
			var c map[string]any
			json.Unmarshal(raw, &c)
			c[field] = ""
			json.Unmarshal(encoded(c), &p.JSONBoard.Context)
			if got := validate(r, p); got != "board_context_"+field+"_missing" {
				t.Fatal(got)
			}
		})
	}
}

func TestJSONBoardPolicyBindingAndClosedConfiguration(t *testing.T) {
	_, p, _ := jsonBoardInputs(t)
	original := hash(p)
	p.JSONBoard.Elements[0].SHA256 = strings.Repeat("0", 64)
	if hash(p) == original {
		t.Fatal("source hash absent from policy digest")
	}
	for _, raw := range []string{`{"json_board":{"schema":"keeper-assignment/1","extra":true}}`, `{"json_board":{"context":{"room_alias":"#example","Room_Alias":"#other"}}}`, `{"json_board":{"elements":[{"id":"JOB-240101-0042","id":"other"}]}}`} {
		var p Policy
		if Decode([]byte(raw), &p) == nil {
			t.Fatal("open configuration", raw)
		}
	}
}

func TestJSONBoardRecheckBeforePost(t *testing.T) {
	r, p, _ := jsonBoardInputs(t)
	calls := 0
	a := CLIAdapter{Runner: jsonMessagingRunner(t, r, p, "valid", &calls)}
	if reason := a.ValidateSources(context.Background(), r, p); reason != "" {
		t.Fatal(reason)
	}
	if err := os.WriteFile(filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Post(context.Background(), r, p, "inert"); err == nil || calls != 2 {
		t.Fatal(err, calls)
	}
}

func TestJSONBoardDispatchRefusesBeforeIntentOrPost(t *testing.T) {
	r, p, _ := jsonBoardInputs(t)
	r.Action = "dispatch"
	if err := os.Remove(filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) { calls++; return nil, nil })}
	dir := filepath.Join(p.ProjectRoot, "journal")
	got := (Engine{Adapter: a}).Execute(context.Background(), r, p, dir)
	if got.Status != "refused" || got.Reason != "board_element_unavailable" || calls != 0 {
		t.Fatal(got, calls)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 0 {
		t.Fatal("source refusal wrote intent", files, err)
	}
}

func TestJSONBoardPolicyChangeConflictsWithRetainedIntent(t *testing.T) {
	r, p, _ := jsonBoardInputs(t)
	r.Action = "dispatch"
	a := &fakeAdapter{verifyReason: "event_readback_unavailable"}
	e := Engine{Adapter: a}
	dir := filepath.Join(p.ProjectRoot, "journal")
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "unknown" || a.posts != 1 {
		t.Fatal(got, a)
	}
	original := p.JSONBoard.Elements[0].SHA256
	p.JSONBoard.Elements[0].SHA256 = strings.Repeat("0", 64)
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "conflict" || got.Reason != "correlation_intent_changed" || a.posts != 1 || a.reads != 1 {
		t.Fatal(got, a)
	}
	p.JSONBoard.Elements[0].SHA256 = original
	a.verifyReason = ""
	if got := e.Execute(context.Background(), r, p, dir); got.Status != "queued_notification" || a.posts != 1 || a.reads != 2 {
		t.Fatal(got, a)
	}
}

func TestJSONBoardPathBounds(t *testing.T) {
	for _, plant := range []string{"root", "length", "depth"} {
		t.Run(plant, func(t *testing.T) {
			r, p, _ := jsonBoardInputs(t)
			switch plant {
			case "root":
				p.ProjectRoot = "/"
			case "length":
				p.BoardDir = "/" + strings.Repeat("x", 4096)
			case "depth":
				p.BoardDir = p.ProjectRoot + strings.Repeat("/d", 129)
			}
			p.JSONBoard.Context.ProjectRoot = p.ProjectRoot
			p.JSONBoard.Context.BoardDir = p.BoardDir
			if got := validate(r, p); got != "board_path_not_contained" {
				t.Fatal(got)
			}
		})
	}
}

func TestJSONBoardPublishedExample(t *testing.T) {
	read := func(path string) []byte {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var p Policy
	if err := Decode(read("../examples/queued-handoff/json-board/policy.json"), &p); err != nil {
		t.Fatal(err)
	}
	r := Request{Contract: Contract, Action: "plan", CorrelationID: "example-plan", Project: p.Project, TaskID: p.Tasks[0].ID, TaskTitle: p.Tasks[0].Title, Member: p.Tasks[0].Members[0], Room: p.Room, Topic: p.Topic, ReplyTo: p.ReplyTo}
	root := realTemp(t)
	p.ProjectRoot = root
	p.BoardDir = filepath.Join(root, "board")
	p.JSONBoard.Context.ProjectRoot = root
	p.JSONBoard.Context.BoardDir = p.BoardDir
	if err := os.Mkdir(p.BoardDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.BoardDir, p.JSONBoard.Elements[0].File), read("../examples/queued-handoff/json-board/assignment.json"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	got := (CLIAdapter{Runner: jsonMessagingRunner(t, r, p, "valid", &calls)}).ValidateSources(context.Background(), r, p)
	if got != "" || calls != 2 {
		t.Fatal(got, calls)
	}
}
