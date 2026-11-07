// Package launchbridge assembles the reduced launch library with authenticated
// custody acquisition. The native owner projection remains a separate source.
package launchbridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"relux.works/swarma-router/handoff"
	"relux.works/swarma-router/launch"
)

const CustodyContract = "router-launch-custody/0.1.0"
const MaxProjectionBytes = 64 * 1024

type Refusal string

func (r Refusal) Error() string { return string(r) }

// EventRef is trusted local configuration, never a selector taken from a body.
// Server is the authenticated caller's server, used for a2a's short-name form.
type EventRef struct {
	Server, Caller, RoomID, Topic, Root, EventID, Sender, Kind, ReplyTo string
}

// Custody binds authority and the exact accepted write/read set. It contains no
// claimed stopped state or native binding verdict.
type Custody struct {
	Contract      string   `json:"contract"`
	IntentDigest  string   `json:"intent_digest"`
	Configuration string   `json:"configuration"`
	ProfileLock   string   `json:"profile_lock"`
	TaskID        string   `json:"task_id"`
	Project       string   `json:"project"`
	Manager       string   `json:"manager"`
	Member        string   `json:"member"`
	Session       string   `json:"session"`
	Authority     string   `json:"authority"`
	Holds         []string `json:"holds"`
	ReadSet       []string `json:"read_set"`
	WriteSet      []string `json:"write_set"`
	CheckedAt     string   `json:"checked_at"`
	ExpiresAt     string   `json:"expires_at"`
}

type event struct {
	Type       string          `json:"type"`
	Room       string          `json:"room"`
	RoomID     string          `json:"room_id"`
	EventID    string          `json:"event_id"`
	Sender     string          `json:"sender"`
	At         string          `json:"at"`
	Kind       string          `json:"kind"`
	Topic      string          `json:"topic"`
	Root       string          `json:"root"`
	To         []string        `json:"to"`
	Re         string          `json:"re"`
	Subject    string          `json:"subject"`
	Body       string          `json:"body"`
	Attachment json.RawMessage `json:"attachment"`
}

var shortName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func server(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func mxid(s, localServer string) string {
	if shortName.MatchString(s) {
		return "@" + s + ":" + localServer
	}
	if !strings.HasPrefix(s, "@") {
		return ""
	}
	name, host, ok := strings.Cut(s[1:], ":")
	if !ok || !shortName.MatchString(name) || !server(host) {
		return ""
	}
	return "@" + name + ":" + host
}

func reference(s string, prefix byte) bool {
	if len(s) < 2 || len(s) > 1024 || s[0] != prefix {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}

func (r EventRef) valid() bool {
	return server(r.Server) && strings.HasSuffix(r.Caller, ":"+r.Server) && strings.HasPrefix(r.Caller, "@") && mxid(r.Caller, r.Server) == r.Caller &&
		strings.HasPrefix(r.Sender, "@") && mxid(r.Sender, r.Server) == r.Sender &&
		reference(r.RoomID, '!') && reference(r.EventID, '$') && reference(r.Root, '$') &&
		(r.ReplyTo == "" || reference(r.ReplyTo, '$')) && shortName.MatchString(r.Topic) &&
		r.Kind == "handover"
}

// CLIEvents uses the existing admitted runner only for these two fixed reads.
// Membership checks bind the actual CLI caller before reading the custody event.
type CLIEvents struct {
	Runner handoff.Runner
	Dir    string
}

func (c CLIEvents) read(ctx context.Context, r EventRef, now time.Time) (Custody, error) {
	var empty Custody
	if c.Runner == nil || !r.valid() || !filepath.IsAbs(c.Dir) || filepath.Clean(c.Dir) != c.Dir {
		return empty, Refusal("event_source_unavailable")
	}
	// Both reads share this deadline. The existing runner also bounds each tool
	// to 29 seconds plus one second for output-pipe closure, without a shell.
	ctx, cancel := context.WithTimeout(ctx, 29*time.Second)
	defer cancel()
	b, err := c.Runner.Run(ctx, c.Dir, "a2a", "room", "members", r.RoomID, "--membership", "join", "--json")
	var members struct {
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
	if err != nil || len(b) > MaxProjectionBytes || handoff.Decode(b, &members) != nil || members.Type != "room-members" || members.RoomID != r.RoomID || members.Caller != r.Caller || members.Filter != "join" || members.Completeness != "complete-response" {
		return empty, Refusal("membership_unavailable")
	}
	seen := map[string]bool{}
	for _, m := range members.Members {
		if m.Membership != "join" || mxid(m.UserID, r.Server) != m.UserID || seen[m.UserID] {
			return empty, Refusal("membership_invalid")
		}
		seen[m.UserID] = true
	}
	if !seen[r.Caller] || !seen[r.Sender] {
		return empty, Refusal("source_not_joined")
	}
	b, err = c.Runner.Run(ctx, c.Dir, "a2a", "read", r.RoomID, "--event", r.EventID, "--json")
	var e event
	if err != nil || len(b) > MaxProjectionBytes || handoff.Decode(b, &e) != nil {
		return empty, Refusal("event_unavailable")
	}
	at, timeErr := time.Parse(time.RFC3339Nano, e.At)
	if e.Type != "message" || e.RoomID != r.RoomID || e.EventID != r.EventID || mxid(e.Sender, r.Server) != r.Sender || e.Topic != r.Topic || e.Root != r.Root || e.Kind != r.Kind || e.Re != r.ReplyTo || timeErr != nil || at.IsZero() || at.After(now) || len(e.Attachment) != 0 && string(e.Attachment) != "null" {
		return empty, Refusal("event_mismatch")
	}
	addressed := false
	seen = map[string]bool{}
	for _, to := range e.To {
		id := mxid(to, r.Server)
		if id == "" || seen[id] {
			return empty, Refusal("event_recipients_invalid")
		}
		seen[id] = true
		addressed = addressed || id == r.Caller
	}
	if !addressed {
		return empty, Refusal("event_not_addressed")
	}
	var custody Custody
	if handoff.Decode([]byte(e.Body), &custody) != nil {
		return empty, Refusal("custody_invalid")
	}
	// CLI timestamps may be rendered to seconds; compare at that exact precision.
	checked, _, timeErr := custody.times()
	if timeErr != nil || checked.Truncate(time.Second).After(at.Truncate(time.Second)) {
		return empty, Refusal("custody_time_invalid")
	}
	return custody, nil
}

func (c Custody) times() (time.Time, time.Time, error) {
	checked, e1 := time.Parse(time.RFC3339Nano, c.CheckedAt)
	expires, e2 := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if e1 != nil || e2 != nil || checked.IsZero() || expires.IsZero() {
		return time.Time{}, time.Time{}, Refusal("custody_time_invalid")
	}
	return checked, expires, nil
}

type Policy struct {
	Event             EventRef
	Config            launch.CuratorConfig
	Manager, Project  string
	JournalDir        string
	ReadSet, WriteSet []string
}

func (c Custody) validate(i launch.Intent, p Policy, now time.Time) error {
	issuer, _, _ := strings.Cut(strings.TrimPrefix(p.Event.Sender, "@"), ":")
	if c.Contract != CustodyContract || c.IntentDigest != i.Digest() || c.Configuration != i.PluginConfig || c.Configuration != (launch.Curator{Config: p.Config}).Configuration() || c.ProfileLock != i.ProfileLock || c.TaskID != i.TaskID || c.Project != i.Project || c.Project != p.Project || c.Manager != i.Manager || c.Manager != p.Manager || issuer != i.Manager || c.Member != i.Member || c.Session != i.TmuxSession {
		return Refusal("custody_scope_mismatch")
	}
	if c.Authority != "admitted" || c.Holds == nil || len(c.Holds) != 0 {
		return Refusal("authority_unavailable")
	}
	if len(p.ReadSet) == 0 || len(p.WriteSet) == 0 || !reflect.DeepEqual(c.ReadSet, p.ReadSet) || !reflect.DeepEqual(c.WriteSet, p.WriteSet) {
		return Refusal("custody_access_mismatch")
	}
	checked, expires, err := c.times()
	if err != nil || checked.After(now) || !checked.Before(expires) || !now.Before(expires) {
		return Refusal("custody_expired_or_future")
	}
	return nil
}

// OwnerSource must acquire current task/holds and complete native/identity
// absence through the separately admitted custodian operation. Its production
// observation schema is deliberately unresolved. Nil is never positive evidence.
type OwnerSource interface {
	Check(context.Context, launch.Intent, Custody) error
}

type guard struct {
	policy  Policy
	events  CLIEvents
	backend launch.Backend
	owner   OwnerSource
	now     func() time.Time
}

func (g guard) Check(ctx context.Context, i launch.Intent, digest string, now time.Time) (launch.Admission, error) {
	var empty launch.Admission
	if ctx.Err() != nil {
		return empty, Refusal("custody_expired_or_cancelled")
	}
	if digest != i.Digest() || g.owner == nil {
		return empty, Refusal("owner_source_unavailable")
	}
	c, err := g.events.read(ctx, g.policy.Event, now)
	if err != nil {
		return empty, err
	}
	if err := c.validate(i, g.policy, g.now()); err != nil {
		return empty, err
	}
	_, expires, _ := c.times() // validate above already checked both timestamps.
	ctx, cancel := context.WithDeadline(ctx, expires)
	defer cancel()
	_, present, err := g.backend.Snapshot(ctx, i.TmuxSession)
	if err != nil {
		return empty, Refusal("session_unknown")
	}
	if present {
		return empty, Refusal("session_live")
	}
	if err := g.owner.Check(ctx, i, c); err != nil {
		return empty, Refusal("owner_source_unknown")
	}
	checked := g.now()
	if err := c.validate(i, g.policy, checked); err != nil || ctx.Err() != nil || checked.Before(now) {
		return empty, Refusal("custody_expired_or_cancelled")
	}
	// Router checks admission against the time passed into Check. Use that
	// conservative acquisition-start bound, after rechecking expiry at completion.
	return launch.Admission{Digest: digest, Manager: i.Manager, Member: i.Member, ProfileLock: i.ProfileLock, State: "stopped", CheckedAt: now, ExpiresAt: expires}, nil
}

// Assemble is pure. The production caller still needs the admitted OwnerSource
// and Ready implementations; passing nil cannot qualify or start a member.
func Assemble(p Policy, events CLIEvents, backend launch.Backend, owner OwnerSource, ready launch.Ready, clock func() time.Time) (launch.Router, error) {
	if !p.Event.valid() || !shortName.MatchString(p.Manager) || !shortName.MatchString(p.Project) || backend == nil || events.Runner == nil || clock == nil || !filepath.IsAbs(p.JournalDir) || filepath.Clean(p.JournalDir) != p.JournalDir || len(p.ReadSet) == 0 || !slices.Contains(p.WriteSet, p.JournalDir) {
		return launch.Router{}, Refusal("embedding_unavailable")
	}
	p.ReadSet = append([]string(nil), p.ReadSet...)
	p.WriteSet = append([]string(nil), p.WriteSet...)
	plugin := launch.Curator{Config: p.Config, Backend: backend, Ready: ready, Now: clock}
	g := guard{policy: p, events: events, backend: backend, owner: owner, now: clock}
	return launch.Router{Manager: p.Manager, Project: p.Project, Plugin: plugin, Guard: g, Now: clock, OpenStore: func() (launch.Store, error) { return launch.OpenFileStore(p.JournalDir) }}, nil
}
