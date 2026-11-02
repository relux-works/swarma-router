// Package launch provides the reduced, explicitly admitted launch orchestration boundary.
// It does not implement the proposed full Route or Apply operations.
package launch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const Contract = "router-launch/0.1.0"

type Intent struct {
	Contract      string `json:"contract"`
	CorrelationID string `json:"correlation_id"`
	Project       string `json:"project"`
	TaskID        string `json:"task_id"`
	Member        string `json:"member"`
	Manager       string `json:"manager"`
	Plugin        string `json:"plugin"`
	PluginConfig  string `json:"plugin_config"`
	Runtime       string `json:"runtime"`
	Profile       string `json:"profile"`
	ProfileLock   string `json:"profile_lock"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	Workspace     string `json:"workspace"`
	TmuxSession   string `json:"tmux_session"`
	Mode          string `json:"mode"`
	NativeSession string `json:"native_session"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)
var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,100}$`)
var safeArgument = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var taskPattern = regexp.MustCompile(`^(TASK|BUG)-[0-9]{6}-[a-z0-9]+$`)

func (i Intent) valid() bool {
	for _, s := range []string{i.CorrelationID, i.Project, i.Member, i.Manager, i.Profile, i.TmuxSession} {
		if !safeName.MatchString(s) {
			return false
		}
	}
	return i.Contract == Contract && safeArgument.MatchString(i.Plugin) && digestPattern.MatchString(i.PluginConfig) && taskPattern.MatchString(i.TaskID) &&
		sessionName.MatchString(i.TmuxSession) &&
		(i.Runtime == "codex_cli" || i.Runtime == "claude_code") && digestPattern.MatchString(i.ProfileLock) &&
		safeArgument.MatchString(i.Model) && safeName.MatchString(i.Effort) &&
		filepath.IsAbs(i.Workspace) && filepath.Clean(i.Workspace) == i.Workspace &&
		!strings.ContainsAny(i.Workspace, "\x00\r\n\t") &&
		((i.Mode == "start" && i.NativeSession == "") || (i.Mode == "resume" && safeName.MatchString(i.NativeSession)))
}

func (i Intent) Digest() string { b, _ := json.Marshal(i); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func (i Intent) key() string    { return i.Manager + "/" + i.Project + "/" + i.CorrelationID }

// Guard is supplied by the trusted embedding manager, independently of request data.
// It checks the real task, exact selected configuration, current holds and exclusive
// member custody. State=stopped requires both tmux and identity/native absence proof.
type Guard interface {
	Check(context.Context, Intent, string, time.Time) (Admission, error)
}
type Admission struct {
	Digest, Manager, Member, ProfileLock, State string
	CheckedAt, ExpiresAt                        time.Time
}

type Process struct {
	PID        int    `json:"pid"`
	ParentPID  int    `json:"parent_pid"`
	Birth      string `json:"birth"`
	Executable string `json:"executable"`
}

func (p Process) valid() bool {
	return p.PID > 0 && p.ParentPID > 0 && p.Birth != "" && len(p.Birth) < 100 && p.Executable != ""
}
func (p Process) same(q Process) bool {
	return p.valid() && q.valid() && p.PID == q.PID && p.ParentPID == q.ParentPID && p.Birth == q.Birth && p.Executable == q.Executable
}

type Attempt struct {
	Command         []string `json:"command"`
	Profile         string   `json:"profile"`
	ProfileLock     string   `json:"profile_lock"`
	OperationDigest string   `json:"operation_digest"`
	Session         string   `json:"session"`
	Pane            string   `json:"pane"`
	PanePID         int      `json:"pane_pid"`
	CuratorRun      Process  `json:"curator_run"`
	Runtime         Process  `json:"runtime"`
}

func (a Attempt) bound(i Intent) bool {
	return a.Profile == i.Profile && a.ProfileLock == i.ProfileLock && a.OperationDigest == i.Digest() && a.Session == i.TmuxSession
}
func (a Attempt) complete(i Intent) bool {
	return a.bound(i) && len(a.Command) > 0 && panePattern.MatchString(a.Pane) && a.PanePID > 0 && a.CuratorRun.valid()
}

var panePattern = regexp.MustCompile(`^%[0-9]+$`)

type Result struct {
	Contract      string   `json:"contract"`
	CorrelationID string   `json:"correlation_id"`
	RequestDigest string   `json:"request_digest"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason"`
	Historical    bool     `json:"historical"`
	Attempt       *Attempt `json:"attempt"`
}

type Plugin interface {
	ID() string
	Configuration() string
	Prepare(Intent) (Attempt, error)
	Start(context.Context, Intent) (Attempt, string, error)
	Observe(context.Context, Intent, *Attempt) (Attempt, string, error)
}

type Record struct {
	Intent Intent `json:"intent"`
	Result Result `json:"result"`
}

func (r Record) valid() bool {
	if !r.Intent.valid() || r.Result.Contract != Contract || r.Result.RequestDigest != r.Intent.Digest() || r.Result.CorrelationID != r.Intent.CorrelationID || r.Result.Historical {
		return false
	}
	if r.Result.Attempt != nil && !r.Result.Attempt.bound(r.Intent) {
		return false
	}
	switch r.Result.Status {
	case "unknown", "refused":
		return true
	case "attempted", "pending", "running":
		return r.Result.Attempt != nil && r.Result.Attempt.complete(r.Intent)
	}
	return false
}

type Store interface {
	Load(string) (Record, bool, error)
	Save(string, Record) error
	Sync(string) error
	Close() error
}
