package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type runnerFunc func(context.Context, string, string, ...string) ([]byte, error)

func (f runnerFunc) Run(c context.Context, d, n string, args ...string) ([]byte, error) {
	return f(c, d, n, args...)
}

func encoded(v any) []byte { b, _ := json.Marshal(v); return b }
func localConfig(p Policy) map[string]any {
	return map[string]any{"effective_mode": "local", "configured_mode": "local", "project_root": p.ProjectRoot, "explicit_remote_override": false, "local": map[string]any{"active": true, "board_dir": p.BoardDir}, "remote": map[string]any{"active": false}}
}

// All four messaging calls use the policy's exact room ID, independently of
// alias availability. The isolated valid controls cover opaque and server-qualified
// IDs; every planted source mismatch prevents all post calls.
func TestCLIAdapterAuthorityAndReplyControls(t *testing.T) {
	cases := []struct{ plant, reason string }{
		{"valid", ""}, {"opaque-room-id", ""}, {"server-qualified-room-id", ""},
		{"remote", "board_authority_not_local"}, {"storage", "board_authority_not_local"},
		{"unrelated-metadata", ""}, {"board-alias-only", "board_authority_not_local"},
		{"board-alias-shadow", "board_authority_not_local"}, {"nested-active-shadow", "board_authority_not_local"},
		{"task-alias", "task_source_unavailable"}, {"member-alias", "membership_unavailable"},
		{"reply-alias", "reply_unavailable"},
		{"task-title", "task_source_mismatch"}, {"task-id", "task_source_mismatch"},
		{"task-absent", "task_source_unavailable"}, {"partial-members", "membership_binding_mismatch"},
		{"wrong-caller", "membership_binding_mismatch"}, {"missing-caller", "caller_not_joined"},
		{"missing-recipient", "recipient_not_joined"}, {"wrong-room", "membership_binding_mismatch"},
		{"wrong-reply-topic", "reply_binding_mismatch"}, {"wrong-reply-id", "reply_binding_mismatch"},
		{"reply-absent", "reply_unavailable"}, {"reply-sender", "reply_sender_not_joined"},
		{"read-timeout", "reply_unavailable"},
		{"configured-remote", "board_authority_not_local"}, {"project-root", "board_authority_not_local"},
		{"remote-active", "board_authority_not_local"}, {"remote-override", "board_authority_not_local"},
		{"second-binding-drift", "task_source_unavailable"}, {"wrong-filter", "membership_binding_mismatch"},
		{"invited-member", "membership_binding_mismatch"}, {"duplicate-member", "membership_binding_mismatch"},
		{"wrong-reply-type", "reply_binding_mismatch"}, {"wrong-reply-room", "reply_binding_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.plant, func(t *testing.T) {
			r, p := inputs()
			switch tc.plant {
			case "opaque-room-id":
				p.RoomID = "!existing_Opaque-Room123"
			case "server-qualified-room-id":
				p.RoomID = "!existing:original.example"
			}
			posts := 0
			runner := runnerFunc(func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
				if dir != p.ProjectRoot {
					t.Fatal("incorrect cwd", dir)
				}
				if name == "task-board" {
					if len(args) != 6 || !reflect.DeepEqual(args[:5], []string{"--board-dir", p.BoardDir, "q", "--format", "json"}) {
						t.Fatal("unsafe board wire", args)
					}
					cfg := localConfig(p)
					switch tc.plant {
					case "configured-remote":
						cfg["configured_mode"] = "remote"
					case "project-root":
						cfg["project_root"] = "/another"
					case "remote-active":
						cfg["remote"] = map[string]any{"active": true}
					case "remote-override":
						cfg["explicit_remote_override"] = true
					case "second-binding-drift":
						if args[5] != "project_config()" {
							cfg["configured_mode"] = "remote"
						}
					}
					if tc.plant == "unrelated-metadata" {
						cfg["unrelated"] = map[string]any{"Action": "metadata", "nested": []any{1, true}}
					}
					if tc.plant == "board-alias-only" || tc.plant == "board-alias-shadow" {
						delete(cfg, "effective_mode")
						cfg["Effective_Mode"] = "local"
						b := encoded(cfg)
						if tc.plant == "board-alias-shadow" {
							b = append([]byte(`{"effective_mode":"remote",`), b[1:]...)
						}
						return b, nil
					}
					if tc.plant == "nested-active-shadow" {
						cfg["local"] = json.RawMessage(`{"active":false,"Active":true,"board_dir":` + string(encoded(p.BoardDir)) + `}`)
					}
					if tc.plant == "remote" {
						cfg["effective_mode"] = "remote"
					}
					if tc.plant == "storage" {
						cfg["local"] = map[string]any{"active": true, "board_dir": "/another"}
					}
					if args[5] == "project_config()" {
						return encoded(cfg), nil
					}
					if args[5] != "project_config(); get("+r.TaskID+") { id title }" {
						t.Fatal("incorrect projection", args)
					}
					if tc.plant == "task-absent" {
						return nil, errors.New("absent")
					}
					task := map[string]string{"id": r.TaskID, "title": r.TaskTitle}
					if tc.plant == "task-alias" {
						delete(task, "title")
						task["Title"] = r.TaskTitle
					}
					if tc.plant == "task-title" {
						task["title"] = "unprefixed guessed title"
					}
					if tc.plant == "task-id" {
						task["id"] = "TASK-261031-other"
					}
					return encoded([]any{cfg, task}), nil
				}
				if name != "a2a" {
					t.Fatal("unexpected tool", name)
				}
				if args[0] == "room" {
					if !reflect.DeepEqual(args, []string{"room", "members", p.RoomID, "--membership", "join", "--json"}) {
						t.Fatal(args)
					}
					members := []map[string]string{{"user_id": "@caller:example.test", "membership": "join"}, {"user_id": "@recipient:example.test", "membership": "join"}, {"user_id": "@author:example.test", "membership": "join"}}
					if tc.plant == "invited-member" {
						members[1]["membership"] = "invite"
					}
					if tc.plant == "duplicate-member" {
						members = append(members, members[1])
					}
					if tc.plant == "member-alias" {
						delete(members[0], "user_id")
						members[0]["User_ID"] = "@caller:example.test"
					}
					if tc.plant == "missing-caller" {
						members = members[1:]
					}
					if tc.plant == "missing-recipient" {
						members = append(members[:1], members[2:]...)
					}
					v := map[string]any{"type": "room-members", "room_id": p.RoomID, "caller": "@caller:example.test", "membership_filter": "join", "completeness": "complete-response", "members": members}
					if tc.plant == "wrong-filter" {
						v["membership_filter"] = "join,invite"
					}
					if tc.plant == "wrong-caller" {
						v["caller"] = "@other:example.test"
					}
					if tc.plant == "partial-members" {
						v["completeness"] = "partial"
					}
					if tc.plant == "wrong-room" {
						v["room_id"] = "!other"
					}
					return encoded(v), nil
				}
				if args[0] == "post" {
					posts++
					if !reflect.DeepEqual(args, []string{"post", p.RoomID, "--topic", r.Topic, "--kind", "notice", "--to", r.Member, "--re", r.ReplyTo, notice(r)}) {
						t.Fatal("post wire", args)
					}
					return []byte("$sent\n"), nil
				}
				if args[0] != "read" || len(args) != 5 || args[1] != p.RoomID || args[2] != "--event" || args[4] != "--json" {
					t.Fatal("read wire", args)
				}
				v := map[string]any{"type": "message", "event_id": args[3], "room_id": p.RoomID, "sender": "author", "topic": r.Topic}
				if args[3] == r.ReplyTo {
					switch tc.plant {
					case "wrong-reply-type":
						v["type"] = "topic"
					case "wrong-reply-room":
						v["room_id"] = "!other"
					case "wrong-reply-topic":
						v["topic"] = "other"
					case "wrong-reply-id":
						v["event_id"] = "$other"
					case "reply-sender":
						v["sender"] = "absent"
					case "reply-absent", "read-timeout":
						return nil, context.DeadlineExceeded
					case "reply-alias":
						delete(v, "topic")
						v["Topic"] = r.Topic
					}
				} else {
					v["sender"] = p.Sender
					v["kind"] = "notice"
					v["to"] = []string{r.Member}
					v["re"] = r.ReplyTo
					v["body"] = notice(r)
				}
				return encoded(v), nil
			})
			// TempDir's existing leaf may be 0755; create the journal's own private leaf.
			got := (Engine{Adapter: CLIAdapter{Runner: runner}}).Execute(context.Background(), r, p, filepath.Join(realTemp(t), "journal"))
			if tc.reason == "" {
				if got.Status != "queued_notification" || posts != 1 {
					t.Fatalf("valid %+v posts%d", got, posts)
				}
			} else if got.Status != "refused" || got.Reason != tc.reason || posts != 0 {
				t.Fatalf("plant %+v posts%d", got, posts)
			}
		})
	}
}

// Every receipt-binding field is checked independently; a real returned event ID
// does not convert a wrong body/recipient/thread/sender into a queued receipt.
func TestReceiptBindingPlants(t *testing.T) {
	r, p := inputs()
	for _, field := range []string{"valid", "metadata", "alias-only", "alias-shadow", "unicode-alias", "type", "event_id", "room_id", "sender", "kind", "topic", "to", "re", "body", "duplicate-key", "timeout"} {
		t.Run(field, func(t *testing.T) {
			v := map[string]any{"type": "message", "event_id": "$sent", "room_id": p.RoomID, "sender": p.Sender, "kind": "notice", "topic": r.Topic, "to": []string{r.Member}, "re": r.ReplyTo, "body": notice(r)}
			if field == "metadata" {
				v["unrelated"] = map[string]any{"Sender": "metadata"}
			}
			if field == "alias-only" || field == "alias-shadow" || field == "unicode-alias" {
				delete(v, "sender")
				alias := "Sender"
				if field == "unicode-alias" {
					alias = "ſender"
				}
				v[alias] = p.Sender
			}
			if field != "valid" && field != "metadata" && field != "alias-only" && field != "alias-shadow" && field != "unicode-alias" && field != "duplicate-key" && field != "timeout" {
				if field == "to" {
					v[field] = []string{r.Member, "extra"}
				} else {
					v[field] = "wrong"
				}
			}
			a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) {
				if field == "timeout" {
					return nil, context.DeadlineExceeded
				}
				b := encoded(v)
				if field == "duplicate-key" {
					b = append([]byte(`{"sender":"wrong",`), b[1:]...)
				}
				if field == "alias-shadow" {
					b = append([]byte(`{"sender":"wrong",`), b[1:]...)
				}
				return b, nil
			})}
			got := a.VerifyEvent(context.Background(), r, p, "$sent", notice(r))
			if field == "valid" || field == "metadata" {
				if got != "" {
					t.Fatal(got)
				}
			} else if strings.Contains(field, "alias") && got != "event_readback_unavailable" {
				t.Fatal("alias was not rejected by decoder", got)
			} else if got == "" {
				t.Fatal("known binding violation not detected")
			}
		})
	}
}

// Closed inputs reject duplicate/unknown/trailing data instead of letting a
// decoder silently pick a more permissive field; the encoded neighbor decodes.
func TestClosedJSON(t *testing.T) {
	r, _ := inputs()
	valid := encoded(r)
	var good Request
	if Decode(valid, &good) != nil || good != r {
		t.Fatal("valid input rejected")
	}
	for _, b := range [][]byte{
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		append([]byte(`{"action":"launch",`), valid[1:]...),
		append([]byte(`{"execute":"arbitrary",`), valid[1:]...),
		append(valid, []byte(strings.Repeat(" ", 2*1024*1024-len(valid)+1))...),
		[]byte(strings.Replace(string(valid), "Example task", "Example "+string([]byte{0xff})+" task", 1)),
	} {
		var got Request
		if Decode(b, &got) == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

// Task-board selectors are removed only from its child's environment; unrelated
// settings survive, and an all-selector environment remains explicitly empty.
func TestBoardEnvironment(t *testing.T) {
	env := []string{"PATH=/tools", "TASK_BOARD_REMOTE=https://invalid.example", "TASK_BOARD_DIR=/foreign", "TASK_BOARD_CONFIG=other", "TB_CONFIG=other", "A2A_AGENT=caller"}
	got := boardEnvironment(env)
	if !reflect.DeepEqual(got, []string{"PATH=/tools", "A2A_AGENT=caller"}) || len(env) != 6 {
		t.Fatal(got, env)
	}
	if got := boardEnvironment([]string{"TASK_BOARD_ID=other"}); got == nil || len(got) != 0 {
		t.Fatal("nil inherits ambient environment", got)
	}
}

// The output limit detects an overrun without persisting its excess; a nearby
// in-limit write remains intact. It is an IO bound, not secret classification.
func TestAdapterOutputLimit(t *testing.T) {
	b := &boundedBuffer{limit: 4}
	if n, err := b.Write([]byte("safe")); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := b.Write([]byte("excess")); n != 0 || err == nil || b.b.String() != "safe" {
		t.Fatal(n, err, b.b.String())
	}
}
