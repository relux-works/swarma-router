// Package handoff implements the explicitly reduced queued task notification contract.
package handoff

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Contract = "queued-task-handoff/0.1.0"

type Request struct {
	Contract      string `json:"contract"`
	Action        string `json:"action"`
	CorrelationID string `json:"correlation_id"`
	Project       string `json:"project"`
	TaskID        string `json:"task_id"`
	TaskTitle     string `json:"task_title"`
	Member        string `json:"member"`
	Room          string `json:"room"`
	Topic         string `json:"topic"`
	ReplyTo       string `json:"reply_to"`
}

// Policy is an explicit local admission binding, not a task-assignment service.
type Policy struct {
	Contract    string        `json:"contract"`
	Project     string        `json:"project"`
	ProjectRoot string        `json:"project_root"`
	BoardDir    string        `json:"board_dir"`
	Server      string        `json:"server"`
	Sender      string        `json:"sender"`
	Room        string        `json:"room"`
	RoomID      string        `json:"room_id"`
	Topic       string        `json:"topic"`
	ReplyTo     string        `json:"reply_to"`
	Tasks       []TaskBinding `json:"tasks"`
}

type TaskBinding struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Members []string `json:"members"`
}

type Result struct {
	Contract      string `json:"contract"`
	Status        string `json:"status"`
	CorrelationID string `json:"correlation_id"`
	EventID       string `json:"event_id,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

func (r Result) ExitCode() int {
	switch r.Status {
	case "planned", "queued_notification":
		return 0
	case "unknown":
		return 3
	default:
		return 2
	}
}

var (
	slug    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,100}$`)
	name    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	key     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	taskID  = regexp.MustCompile(`^(TASK|BUG)-[0-9]{6}-[a-z0-9]+$`)
	eventID = regexp.MustCompile(`^\$[A-Za-z0-9_+/=-]{1,200}$`)
	server  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)
	roomID  = regexp.MustCompile(`^![A-Za-z0-9_:./=-]{1,250}$`)
)

func textOK(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) == -1
}

func validate(r Request, p Policy) string {
	if r.Contract != Contract || (r.Action != "plan" && r.Action != "dispatch") || !key.MatchString(r.CorrelationID) || !slug.MatchString(r.Project) || !taskID.MatchString(r.TaskID) || !textOK(r.TaskTitle) || !name.MatchString(r.Member) || !slug.MatchString(r.Room) || !slug.MatchString(r.Topic) || !eventID.MatchString(r.ReplyTo) {
		return "invalid_request"
	}
	if p.Contract != Contract || !slug.MatchString(p.Project) || !filepath.IsAbs(p.ProjectRoot) || filepath.Clean(p.ProjectRoot) != p.ProjectRoot || !filepath.IsAbs(p.BoardDir) || filepath.Clean(p.BoardDir) != p.BoardDir || !server.MatchString(p.Server) || !name.MatchString(p.Sender) || !slug.MatchString(p.Room) || !roomID.MatchString(p.RoomID) || !slug.MatchString(p.Topic) || !eventID.MatchString(p.ReplyTo) || len(p.Tasks) == 0 || len(p.Tasks) > 256 {
		return "invalid_policy"
	}
	seen := map[string]bool{}
	var selected *TaskBinding
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if !taskID.MatchString(t.ID) || !textOK(t.Title) || seen[t.ID] || len(t.Members) == 0 || len(t.Members) > 64 {
			return "invalid_policy"
		}
		seen[t.ID] = true
		members := map[string]bool{}
		for _, m := range t.Members {
			if !name.MatchString(m) || members[m] {
				return "invalid_policy"
			}
			members[m] = true
		}
		if t.ID == r.TaskID {
			selected = t
		}
	}
	if r.Project != p.Project || r.Room != p.Room || r.Topic != p.Topic || r.ReplyTo != p.ReplyTo {
		return "destination_not_authorized"
	}
	if selected == nil {
		return "task_not_authorized"
	}
	if selected.Title != r.TaskTitle {
		return "task_binding_mismatch"
	}
	for _, m := range selected.Members {
		if m == r.Member {
			return ""
		}
	}
	return "member_not_authorized"
}

// Decode rejects duplicate keys, trailing values, unknown fields and invalid UTF-8.
// Adapter decoding uses the same syntax checks while allowing unrelated metadata.
func Decode(data []byte, out any) error { return decode(data, out, true) }

func decode(data []byte, out any, closed bool) error {
	if len(data) > 2*1024*1024 || !utf8.Valid(data) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueValue(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	if closed {
		d.DisallowUnknownFields()
	}
	return d.Decode(out)
}

func uniqueValue(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return errors.New("duplicate JSON key")
			}
			seen[s] = true
			if err := uniqueValue(d); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid object")
		}
	case json.Delim('['):
		for d.More() {
			if err := uniqueValue(d); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid array")
		}
	}
	return nil
}
