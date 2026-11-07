package handoff

import (
	"context"
	"reflect"
	"testing"
)

// The read --event CLI projection permits two types, with identical exact
// identity and membership requirements. These are synthetic provider controls.
func TestReplyProjectionBindings(t *testing.T) {
	for _, typ := range []string{"message", "topic"} {
		for _, plant := range []string{"valid-short", "valid-full", "wrong-room", "missing-room", "wrong-topic", "missing-topic", "wrong-event", "missing-event", "wrong-sender", "missing-sender", "foreign-full-alias", "foreign-short-alias", "foreign-suffix-alias", "unknown-type", "missing-type", "matrix-type", "subscription-type", "wrong-parent", "alias-field", "duplicate-field", "partial-members", "unjoined-recipient"} {
			t.Run(typ+"/"+plant, func(t *testing.T) {
				r, p := inputs()
				reply := map[string]any{"type": typ, "event_id": r.ReplyTo, "room_id": p.RoomID, "topic": r.Topic, "sender": p.Sender}
				want := "reply_binding_mismatch"
				switch plant {
				case "valid-short":
					want = ""
				case "valid-full":
					reply["sender"] = "@" + p.Sender + ":" + p.Server
					want = ""
				case "wrong-room":
					reply["room_id"] = "!other"
				case "missing-room":
					delete(reply, "room_id")
				case "wrong-topic":
					reply["topic"] = "other"
				case "missing-topic":
					delete(reply, "topic")
				case "wrong-event":
					reply["event_id"] = "$other"
				case "missing-event":
					delete(reply, "event_id")
				case "wrong-sender":
					reply["sender"] = "absent"
					want = "reply_sender_not_joined"
				case "missing-sender":
					delete(reply, "sender")
					want = "reply_sender_not_joined"
				case "foreign-full-alias":
					reply["sender"] = "@" + p.Sender + ":foreign.test"
					want = "reply_sender_not_joined"
				case "foreign-short-alias":
					reply["sender"] = p.Sender + ":foreign.test"
					want = "reply_sender_not_joined"
				case "foreign-suffix-alias":
					reply["sender"] = "@" + p.Sender + ":" + p.Server + ".foreign.test"
					want = "reply_sender_not_joined"
				case "unknown-type":
					reply["type"] = "reaction"
				case "missing-type":
					delete(reply, "type")
				case "matrix-type":
					reply["type"] = "m.room.message"
				case "subscription-type":
					reply["type"] = "to"
				case "wrong-parent":
					reply["root"] = "$different-parent"
					if typ == "message" {
						want = ""
					}
				case "alias-field":
					delete(reply, "topic")
					reply["Topic"] = r.Topic
					want = "reply_unavailable"
				case "duplicate-field":
					want = "reply_unavailable"
				case "partial-members":
					want = "membership_binding_mismatch"
				case "unjoined-recipient":
					want = "recipient_not_joined"
				}
				calls := 0
				a := CLIAdapter{Runner: runnerFunc(func(_ context.Context, dir, tool string, args ...string) ([]byte, error) {
					calls++
					if tool != "a2a" || dir != p.ProjectRoot {
						t.Fatal("unexpected provider", tool, dir)
					}
					if calls == 1 {
						if !reflect.DeepEqual(args, []string{"room", "members", p.RoomID, "--membership", "join", "--json"}) {
							t.Fatal(args)
						}
						members := []map[string]string{{"user_id": "@" + p.Sender + ":" + p.Server, "membership": "join"}, {"user_id": "@" + r.Member + ":" + p.Server, "membership": "join"}}
						if plant == "unjoined-recipient" {
							members = members[:1]
						}
						complete := "complete-response"
						if plant == "partial-members" {
							complete = "partial"
						}
						return encoded(map[string]any{"type": "room-members", "room_id": p.RoomID, "caller": "@" + p.Sender + ":" + p.Server, "membership_filter": "join", "completeness": complete, "members": members}), nil
					}
					if calls != 2 || !reflect.DeepEqual(args, []string{"read", p.RoomID, "--event", r.ReplyTo, "--json"}) {
						t.Fatal("effect or wrong read", args)
					}
					raw := encoded(reply)
					if plant == "duplicate-field" {
						raw = append([]byte(`{"event_id":"$other",`), raw[1:]...)
					}
					return raw, nil
				})}
				if got := a.validateMessaging(context.Background(), r, p); got != want {
					t.Fatalf("got %s want %s", got, want)
				}
				expectedCalls := 2
				if plant == "partial-members" || plant == "unjoined-recipient" {
					expectedCalls = 1
				}
				if calls != expectedCalls {
					t.Fatal("membership gate bypassed", calls)
				}
			})
		}
	}
}

// A root can be a reply prerequisite, but cannot masquerade as the sent notice.
func TestTopicProjectionCannotVerifyPostedNotice(t *testing.T) {
	r, p := inputs()
	event := message{Type: "topic", EventID: "$sent", RoomID: p.RoomID, Sender: p.Sender, Kind: "notice", Topic: r.Topic, To: []string{r.Member}, Re: r.ReplyTo, Body: notice(r)}
	a := CLIAdapter{Runner: runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) { return encoded(event), nil })}
	if got := a.VerifyEvent(context.Background(), r, p, "$sent", notice(r)); got != "event_binding_mismatch" {
		t.Fatal(got)
	}
}
