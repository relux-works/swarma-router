package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// CLIAdapter selects the policy's source seam. A Runner is trusted code,
// never request-controlled executable names, flags, environment or command text.
type CLIAdapter struct{ Runner Runner }

func (a CLIAdapter) board(ctx context.Context, p Policy, query string) ([]byte, error) {
	return a.Runner.Run(ctx, p.ProjectRoot, "task-board", "--board-dir", p.BoardDir, "q", "--format", "json", query)
}

func localBinding(data []byte, p Policy) bool {
	var c struct {
		Mode           string `json:"effective_mode"`
		Configured     string `json:"configured_mode"`
		Root           string `json:"project_root"`
		RemoteOverride *bool  `json:"explicit_remote_override"`
		Local          struct {
			Active *bool  `json:"active"`
			Dir    string `json:"board_dir"`
		} `json:"local"`
		Remote struct {
			Active *bool `json:"active"`
		} `json:"remote"`
	}
	if decode(data, &c, false) != nil {
		return false
	}
	dir := c.Local.Dir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.Root, dir)
	}
	return c.Mode == "local" && c.Configured == "local" && c.Root == p.ProjectRoot && c.Local.Active != nil && *c.Local.Active && c.Remote.Active != nil && !*c.Remote.Active && c.RemoteOverride != nil && !*c.RemoteOverride && c.Local.Dir != "" && filepath.Clean(dir) == p.BoardDir
}

type memberProjection struct {
	Type         string `json:"type"`
	RoomID       string `json:"room_id"`
	Caller       string `json:"caller"`
	Filter       string `json:"membership_filter"`
	Completeness string `json:"completeness"`
	Members      []struct {
		UserID     string `json:"user_id"`
		Membership string `json:"membership"`
	} `json:"members"`
}

type message struct {
	Type    string   `json:"type"`
	EventID string   `json:"event_id"`
	RoomID  string   `json:"room_id"`
	Sender  string   `json:"sender"`
	Kind    string   `json:"kind"`
	Topic   string   `json:"topic"`
	To      []string `json:"to"`
	Re      string   `json:"re"`
	Body    string   `json:"body"`
}

func (a CLIAdapter) ValidateSources(ctx context.Context, r Request, p Policy) string {
	// Keep direct adapter callers behind the same ID and binding checks as
	// Engine before constructing the bare-ID board query.
	if reason := validate(r, p); reason != "" {
		return reason
	}
	if a.Runner == nil {
		return "adapter_unavailable"
	}
	if p.JSONBoard != nil {
		if reason := validateJSONBoardSource(r, p); reason != "" {
			return reason
		}
	} else if reason := a.validateNativeBoard(ctx, r, p); reason != "" {
		return reason
	}
	return a.validateMessaging(ctx, r, p)
}

func (a CLIAdapter) validateNativeBoard(ctx context.Context, r Request, p Policy) string {
	// project_config is always local; do not query an element on a known remote board.
	out, err := a.board(ctx, p, "project_config()")
	if err != nil || !localBinding(out, p) {
		return "board_authority_not_local"
	}
	out, err = a.board(ctx, p, "project_config(); get("+r.TaskID+") { id title }")
	var pair []json.RawMessage
	if err != nil || decode(out, &pair, false) != nil || len(pair) != 2 || !localBinding(pair[0], p) {
		return "task_source_unavailable"
	}
	var task struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if decode(pair[1], &task, false) != nil {
		return "task_source_unavailable"
	}
	if task.ID != r.TaskID || task.Title != r.TaskTitle {
		return "task_source_mismatch"
	}
	return ""
}

func (a CLIAdapter) validateMessaging(ctx context.Context, r Request, p Policy) string {
	// Select the already pinned room directly; a logical slug may resolve on a
	// different homeserver after account migration. Readbacks still bind its ID.
	out, err := a.Runner.Run(ctx, p.ProjectRoot, "a2a", "room", "members", p.RoomID, "--membership", "join", "--json")
	var members memberProjection
	if err != nil || decode(out, &members, false) != nil {
		return "membership_unavailable"
	}
	caller := "@" + p.Sender + ":" + p.Server
	if members.Type != "room-members" || members.RoomID != p.RoomID || members.Filter != "join" || members.Completeness != "complete-response" || members.Caller != caller {
		return "membership_binding_mismatch"
	}
	seen := map[string]bool{}
	for _, m := range members.Members {
		if seen[m.UserID] || m.Membership != "join" {
			return "membership_binding_mismatch"
		}
		seen[m.UserID] = true
	}
	if !seen[caller] {
		return "caller_not_joined"
	}
	if !seen["@"+r.Member+":"+p.Server] {
		return "recipient_not_joined"
	}
	out, err = a.Runner.Run(ctx, p.ProjectRoot, "a2a", "read", p.RoomID, "--event", r.ReplyTo, "--json")
	var reply message
	if err != nil || decode(out, &reply, false) != nil {
		return "reply_unavailable"
	}
	if reply.Type != "message" || reply.EventID != r.ReplyTo || reply.RoomID != p.RoomID || reply.Topic != r.Topic {
		return "reply_binding_mismatch"
	}
	sender := reply.Sender
	if name.MatchString(sender) {
		sender = "@" + sender + ":" + p.Server
	}
	if sender == "" || !seen[sender] {
		return "reply_sender_not_joined"
	}
	if p.JSONBoard != nil {
		return validateJSONBoardSource(r, p)
	}
	return ""
}

func (a CLIAdapter) Post(ctx context.Context, r Request, p Policy, body string) (string, error) {
	if reason := validate(r, p); reason != "" {
		return "", errors.New("invalid handoff binding")
	}
	if a.Runner == nil {
		return "", errors.New("adapter unavailable")
	}
	if p.JSONBoard != nil {
		if reason := validateJSONBoardSource(r, p); reason != "" {
			return "", errors.New("board source no longer admitted")
		}
	}
	out, err := a.Runner.Run(ctx, p.ProjectRoot, "a2a", "post", p.RoomID, "--topic", r.Topic, "--kind", "notice", "--to", r.Member, "--re", r.ReplyTo, body)
	return strings.TrimSpace(string(out)), err
}

func (a CLIAdapter) VerifyEvent(ctx context.Context, r Request, p Policy, id, body string) string {
	if a.Runner == nil {
		return "event_readback_unavailable"
	}
	out, err := a.Runner.Run(ctx, p.ProjectRoot, "a2a", "read", p.RoomID, "--event", id, "--json")
	var event message
	if err != nil || decode(out, &event, false) != nil {
		return "event_readback_unavailable"
	}
	if event.Type != "message" || event.EventID != id || event.RoomID != p.RoomID || event.Sender != p.Sender || event.Kind != "notice" || event.Topic != r.Topic || len(event.To) != 1 || event.To[0] != r.Member || event.Re != r.ReplyTo || event.Body != body {
		return "event_binding_mismatch"
	}
	return ""
}
