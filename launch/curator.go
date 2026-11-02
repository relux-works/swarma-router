package launch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// CuratorConfig is trusted embedding configuration, never arbitrary request argv/env.
// Pins include permissions, network selection and target child identity metadata.
type CuratorConfig struct {
	Executable   string `json:"executable"`
	Home         string `json:"home"`
	Path         string `json:"path"`
	TempDir      string `json:"temp_dir"`
	Permissions  string `json:"permissions"`
	YoloRefusal  string `json:"yolo_refusal"`
	Network      string `json:"network"`
	Agent        string `json:"agent"`
	Server       string `json:"server"`
	A2AConfigDir string `json:"a2a_config_dir"`
}

func (c CuratorConfig) digest() string {
	b, _ := json.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func absolute(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsAny(s, "\x00\r\n\t")
}
func (c CuratorConfig) valid() bool {
	if !absolute(c.Executable) || !absolute(c.Home) || !absolute(c.TempDir) || c.Path == "" {
		return false
	}
	for _, p := range strings.Split(c.Path, ":") {
		if !absolute(p) {
			return false
		}
	}
	if c.Permissions != "yolo" && !(c.Permissions == "native" && safeArgument.MatchString(c.YoloRefusal)) {
		return false
	}
	if c.Permissions == "yolo" && c.YoloRefusal != "" {
		return false
	}
	if c.Network != "" && !safeArgument.MatchString(c.Network) {
		return false
	}
	if c.Agent == "" {
		return c.Server == "" && c.A2AConfigDir == ""
	}
	return safeName.MatchString(c.Agent) && validServer(c.Server) && absolute(c.A2AConfigDir)
}

// Keep the positive server grammar independent of a denylist of shell metacharacters.
func validServer(s string) bool {
	if s == "" || len(s) > 253 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

type Snapshot struct {
	Session         string
	Pane            string
	PanePID         int
	OperationDigest string
	Processes       []Process
}

// Backend observes only the exact owned session. Present=false requires a complete
// successful query; tool failure is an error, never proof of absence.
type Backend interface {
	Snapshot(context.Context, string) (Snapshot, bool, error)
	Create(context.Context, Intent, []string) error
}

// Ready is an admitted owner-source adapter. It authenticates the actual binding
// and lane event (or native assignment/handoff); these refs are not request claims.
type Ready interface {
	Read(context.Context, Intent, Snapshot) (Readiness, error)
}
type Readiness struct {
	TaskID, Member, Session, Pane           string
	Runtime                                 Process
	CheckedAt, ExpiresAt                    time.Time
	BindingRuntime                          Process
	Thread, BindingRef, LaneRef, LaneSender string
	NativeAssignment, Handoff               string
}

type Curator struct {
	Config  CuratorConfig
	Backend Backend
	Ready   Ready
	Now     func() time.Time
}

func (c Curator) ID() string            { return "curator/0.1" }
func (c Curator) Configuration() string { return c.Config.digest() }
func (c Curator) Prepare(i Intent) (Attempt, error) {
	a := Attempt{Profile: i.Profile, ProfileLock: i.ProfileLock, OperationDigest: i.Digest(), Session: i.TmuxSession}
	if !i.valid() || i.Plugin != c.ID() || i.PluginConfig != c.Configuration() || !c.Config.valid() || (c.Config.Agent != "" && c.Config.Agent != i.Member) {
		return a, errors.New("unsupported configuration")
	}
	a.Command = []string{c.Config.Executable, "run", i.Runtime, "--native", "--profile", i.Profile, "--model", i.Model, "--effort", i.Effort, "--permissions", c.Config.Permissions}
	if c.Config.Network != "" {
		a.Command = append(a.Command, "--network", c.Config.Network)
	}
	tail := []string{}
	if c.Config.Permissions == "native" {
		if i.Runtime == "codex_cli" {
			tail = append(tail, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			tail = append(tail, "--dangerously-skip-permissions")
		}
	}
	if i.Mode == "resume" {
		if i.Runtime == "codex_cli" {
			tail = append(tail, "resume", i.NativeSession)
		} else {
			tail = append(tail, "--resume", i.NativeSession)
		}
	}
	if len(tail) > 0 {
		a.Command = append(a.Command, "--")
		a.Command = append(a.Command, tail...)
	}
	return a, nil
}

func (c Curator) Start(ctx context.Context, i Intent) (Attempt, string, error) {
	a, err := c.Prepare(i)
	if err != nil || c.Backend == nil {
		return a, "refused", err
	}
	_, present, err := c.Backend.Snapshot(ctx, i.TmuxSession)
	if err != nil || present {
		return a, "refused", nil
	}
	// env -i also defeats stale runtime/identity variables held by the tmux server.
	command := []string{"/usr/bin/env", "-i", "HOME=" + c.Config.Home, "PATH=" + c.Config.Path, "TMPDIR=" + c.Config.TempDir, "LANG=en_US.UTF-8"}
	if c.Config.Agent != "" {
		command = append(command, "A2A_CONFIG_DIR="+c.Config.A2AConfigDir, "A2A_AGENT="+c.Config.Agent)
	}
	command = append(command, a.Command...)
	if ctx.Err() != nil {
		return a, "refused", nil
	}
	if err = c.Backend.Create(ctx, i, command); err != nil {
		return a, "unknown", errors.New("start outcome unknown")
	}
	s, present, err := c.Backend.Snapshot(ctx, i.TmuxSession)
	if err != nil || !present {
		return a, "unknown", errors.New("start metadata unknown")
	}
	a, _, err = c.bind(i, a, s)
	if err != nil {
		return a, "unknown", err
	}
	return a, "attempted", nil
}

func (c Curator) bind(i Intent, a Attempt, s Snapshot) (Attempt, Process, error) {
	if s.Session != i.TmuxSession || s.OperationDigest != i.Digest() || !panePattern.MatchString(s.Pane) || s.PanePID <= 0 {
		return a, Process{}, errors.New("session binding unknown")
	}
	if a.Pane != "" && (a.Pane != s.Pane || a.PanePID != s.PanePID) {
		return a, Process{}, errors.New("pane changed")
	}
	byID := map[int]Process{}
	for _, p := range s.Processes {
		if !p.valid() || byID[p.PID].PID != 0 {
			return a, Process{}, errors.New("invalid process projection")
		}
		byID[p.PID] = p
	}
	// Require a complete parent chain rooted in the retained pane process.
	descends := func(p Process, root int) bool {
		seen := map[int]bool{}
		for depth := 0; depth < 32; depth++ {
			if p.PID == root {
				return true
			}
			if seen[p.PID] {
				return false
			}
			seen[p.PID] = true
			var ok bool
			p, ok = byID[p.ParentPID]
			if !ok {
				return false
			}
		}
		return false
	}
	var provider, runtime Process
	for _, p := range s.Processes {
		if filepath.Base(p.Executable) == "curator-run" && descends(p, s.PanePID) {
			if provider.PID != 0 {
				return a, Process{}, errors.New("ambiguous launcher")
			}
			provider = p
		}
	}
	if !provider.valid() || (a.CuratorRun.PID != 0 && !a.CuratorRun.same(provider)) {
		return a, Process{}, errors.New("launcher identity unknown")
	}
	a.Pane, a.PanePID, a.CuratorRun = s.Pane, s.PanePID, provider
	name := "codex"
	if i.Runtime == "claude_code" {
		name = "claude"
	}
	for _, p := range s.Processes {
		if filepath.Base(p.Executable) == name && descends(p, provider.PID) {
			if runtime.PID != 0 {
				return a, Process{}, errors.New("ambiguous runtime")
			}
			runtime = p
		}
	}
	if a.Runtime.PID != 0 && !a.Runtime.same(runtime) {
		return a, Process{}, errors.New("runtime changed")
	}
	a.Runtime = runtime
	return a, runtime, nil
}

func (c Curator) Observe(ctx context.Context, i Intent, prior *Attempt) (Attempt, string, error) {
	a, err := c.Prepare(i)
	if err != nil || c.Backend == nil {
		return a, "unknown", errors.New("observation unavailable")
	}
	if prior != nil {
		if !prior.bound(i) || !reflect.DeepEqual(prior.Command, a.Command) {
			return a, "unknown", errors.New("attempt mismatch")
		}
		a = *prior
	}
	s, present, err := c.Backend.Snapshot(ctx, i.TmuxSession)
	if err != nil || !present {
		return a, "unknown", errors.New("absence not independently established")
	}
	a, runtime, err := c.bind(i, a, s)
	if err != nil {
		return a, "unknown", err
	}
	if !runtime.valid() || c.Ready == nil || c.Now == nil {
		return a, "pending", nil
	}
	p, err := c.Ready.Read(ctx, i, s)
	now := c.Now()
	if err != nil {
		return a, "pending", nil
	}
	if p.TaskID != i.TaskID || p.Member != i.Member || p.Session != s.Session || p.Pane != s.Pane || !p.Runtime.same(runtime) || p.CheckedAt.IsZero() || p.CheckedAt.After(now) || !now.Before(p.ExpiresAt) || !p.CheckedAt.Before(p.ExpiresAt) {
		return a, "unknown", errors.New("readiness binding invalid")
	}
	if c.Config.Agent != "" {
		if !p.BindingRuntime.same(runtime) || p.Thread == "" || p.BindingRef == "" || p.LaneRef == "" || p.LaneSender != "@"+c.Config.Agent+":"+c.Config.Server || p.NativeAssignment != "" || p.Handoff != "" {
			return a, "pending", nil
		}
	} else if p.NativeAssignment == "" || p.Handoff == "" || p.BindingRef != "" || p.LaneRef != "" {
		return a, "pending", nil
	}
	return a, "running", nil
}
