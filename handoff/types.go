// Package handoff implements the explicitly reduced queued task notification contract.
package handoff

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
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
	AssignmentID *AssignmentIDPolicy `json:"assignment_id,omitempty"`
	Contract     string              `json:"contract"`
	Project      string              `json:"project"`
	ProjectRoot  string              `json:"project_root"`
	BoardDir     string              `json:"board_dir"`
	Server       string              `json:"server"`
	Sender       string              `json:"sender"`
	Room         string              `json:"room"`
	RoomID       string              `json:"room_id"`
	Topic        string              `json:"topic"`
	ReplyTo      string              `json:"reply_to"`
	Tasks        []TaskBinding       `json:"tasks"`
}

// AssignmentIDPolicy narrows the safe bare-ID alphabet for the existing source
// adapter. It does not select a provider or establish assignment authority.
type AssignmentIDPolicy struct {
	Pattern  string `json:"pattern"`
	MaxBytes int    `json:"max_bytes"`
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
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.TrimSpace(s) == s && strings.IndexFunc(s, func(r rune) bool {
		return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) ||
			(r >= 0xfdd0 && r <= 0xfdef) || r&0xffff >= 0xfffe
	}) == -1
}

func validate(r Request, p Policy) string {
	ids, err := assignmentIDs(p.AssignmentID)
	if err != nil {
		return "invalid_policy"
	}
	if r.Contract != Contract || (r.Action != "plan" && r.Action != "dispatch") || !key.MatchString(r.CorrelationID) || !slug.MatchString(r.Project) || !ids.matches(r.TaskID) || !textOK(r.TaskTitle) || !name.MatchString(r.Member) || !slug.MatchString(r.Room) || !slug.MatchString(r.Topic) || !eventID.MatchString(r.ReplyTo) {
		return "invalid_request"
	}
	if p.Contract != Contract || !slug.MatchString(p.Project) || !filepath.IsAbs(p.ProjectRoot) || filepath.Clean(p.ProjectRoot) != p.ProjectRoot || !filepath.IsAbs(p.BoardDir) || filepath.Clean(p.BoardDir) != p.BoardDir || !server.MatchString(p.Server) || !name.MatchString(p.Sender) || !slug.MatchString(p.Room) || !roomID.MatchString(p.RoomID) || !slug.MatchString(p.Topic) || !eventID.MatchString(p.ReplyTo) || len(p.Tasks) == 0 || len(p.Tasks) > 256 {
		return "invalid_policy"
	}
	seen := map[string]bool{}
	var selected *TaskBinding
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if !ids.matches(t.ID) || !textOK(t.Title) || seen[t.ID] || len(t.Members) == 0 || len(t.Members) > 64 {
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

// Decode requires exact tagged field names at every closed object level and rejects
// duplicate keys, trailing values and invalid UTF-8. Open adapter projections allow
// unrelated metadata, but never case aliases of their recognized binding fields.
func Decode(data []byte, out any) error { return decode(data, out, true) }

func decode(data []byte, out any, closed bool) error {
	if len(data) > 2*1024*1024 || !utf8.Valid(data) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return errors.New("JSON output must be a nonnil pointer")
	}
	if err := exactFields(data, t.Elem(), closed); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	if closed {
		d.DisallowUnknownFields()
	}
	return d.Decode(out)
}

// The allowlist comes only from explicit JSON tags, before encoding/json can
// assign a case-insensitive match. Raw projections are checked when decoded into
// their concrete binding type; unknown open metadata is never assigned to one.
func exactFields(data []byte, t reflect.Type, closed bool) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" || f.Anonymous || fields[name] != nil {
				return errors.New("unsupported JSON schema")
			}
			fields[name] = f.Type
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
		for key, value := range object {
			field, ok := fields[key]
			if !ok {
				if closed {
					return errors.New("unknown JSON field")
				}
				for canonical := range fields {
					if strings.EqualFold(key, canonical) {
						return errors.New("noncanonical JSON field")
					}
				}
				continue
			}
			if err := exactFields(value, field, closed); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactFields(value, t.Elem(), closed); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueValue(d *json.Decoder, depth int) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == json.Delim('{') || t == json.Delim('[') {
		depth++
		if depth > 64 {
			return errors.New("JSON nesting too deep")
		}
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
			if err := uniqueValue(d, depth); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid object")
		}
	case json.Delim('['):
		for d.More() {
			if err := uniqueValue(d, depth); err != nil {
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
