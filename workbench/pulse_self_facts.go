package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const MaxSelfCardSourceOutput = 2 * 1024 * 1024
const maxSelfCardSourceStderr = 64 * 1024

// SelfCardCommand is trusted synchronous runner input, not a public request.
type SelfCardCommand struct {
	Executable, Directory, Caller string
	Args, Environment             []string
}

// SelfCardOutput keeps transport capacity distinct from successful observation:
// the source refuses every nonempty Stderr, including an in-limit diagnostic.
type SelfCardOutput struct{ Stdout, Stderr []byte }

type SelfCardRunner interface {
	Run(context.Context, SelfCardCommand) (SelfCardOutput, error)
}

// PulseSelfFacts reads a fixed in-place card with an admitted executable. Fields
// are trusted embedding configuration, never request selectors. Configure once;
// do not mutate concurrently. A nil ExpectedProject means declared absence.
type PulseSelfFacts struct {
	Executable, Directory, CardPath, CardSHA256        string
	Caller, ExpectedMember, ExpectedName, ExpectedRole string
	ExpectedProject                                    *string
	Environment                                        map[string]string
	Runner                                             SelfCardRunner
}

var _ SelfCardFactsSource = PulseSelfFacts{}

func selfCardEnvironment(in map[string]string, caller string) ([]string, bool) {
	if caller == "" || caller != os.Getenv("A2A_AGENT") || in["A2A_AGENT"] != caller {
		return nil, false
	}
	public := make(map[string]string, len(in))
	for k, v := range in {
		if k != "A2A_AGENT" {
			public[k] = v
		}
	}
	env, ok := taskEnvironment(public)
	if !ok {
		return nil, false
	}
	return append(env, "A2A_AGENT="+caller), true
}

func (s PulseSelfFacts) Observe(ctx context.Context) (SelfCardObservation, error) {
	zero := SelfCardObservation{}
	if !policyContext(ctx) {
		return zero, Refusal{"authority_unknown"}
	}
	if s.Caller == "" || s.Caller != os.Getenv("A2A_AGENT") {
		return zero, Refusal{"authority_denied"}
	}
	env, ok := selfCardEnvironment(s.Environment, s.Caller)
	if !ok || s.Runner == nil || !taskAbsolute(s.Executable) || !taskAbsolute(s.Directory) || !taskAbsolute(s.CardPath) || !digest(s.CardSHA256) || !ref(s.ExpectedMember) || !ref(s.ExpectedName) || !ref(s.ExpectedRole) || s.ExpectedProject != nil && !ref(*s.ExpectedProject) {
		return zero, Refusal{"source_unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, 29*time.Second)
	defer cancel()
	out, err := s.Runner.Run(ctx, SelfCardCommand{Executable: s.Executable, Directory: s.Directory, Caller: s.Caller,
		Args: []string{"me", "--local-unsigned", "--card", s.CardPath, "--revision", "sha256:" + s.CardSHA256, "--format", "json"}, Environment: env})
	if !policyContext(ctx) {
		return zero, Refusal{"authority_unknown"}
	}
	if err != nil {
		return zero, Refusal{reason(err, "source_unavailable")}
	}
	if len(out.Stdout) > MaxSelfCardSourceOutput || len(out.Stderr) != 0 {
		return zero, Refusal{"source_conflict"}
	}
	facts, err := s.decode(out.Stdout)
	if err != nil {
		return zero, err
	}
	if !policyContext(ctx) {
		return zero, Refusal{"authority_unknown"}
	}
	return SelfCardObservation{Facts: facts, SourceSHA256: s.CardSHA256}, nil
}

// Open projections accept unrelated shipped metadata while refusing aliases of
// every field this reader interprets. The shared strict decoder checks the full
// JSON tree (including ignored metadata) for duplicates/UTF-8/depth/EOF errors.
func selfCardObject(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if taskSourceJSON(raw, &m) != nil || m == nil {
		return nil, false
	}
	for key := range m {
		for _, known := range keys {
			if key != known && strings.EqualFold(key, known) {
				return nil, false
			}
		}
	}
	return m, true
}

func selfCardString(m map[string]json.RawMessage, key string) (string, bool) {
	var s *string
	err := json.Unmarshal(m[key], &s)
	if err != nil || s == nil {
		return "", false
	}
	return *s, true
}

func (s PulseSelfFacts) decode(raw []byte) (SelfCardFacts, error) {
	zero, bad := SelfCardFacts{}, Refusal{"source_conflict"}
	root, ok := selfCardObject(raw, "schema", "mode", "member", "caller", "self", "source", "known_limit", "error")
	if !ok || root["error"] != nil {
		return zero, bad
	}
	check := func(m map[string]json.RawMessage, key, want string) bool {
		got, ok := selfCardString(m, key)
		return ok && got == want
	}
	if !check(root, "schema", "pulse-view/v1") || !check(root, "mode", "local-unsigned") || !check(root, "member", s.ExpectedMember) || !check(root, "known_limit", "caller not cryptographically verified; single-user local trust") {
		return zero, bad
	}
	caller, ok := selfCardObject(root["caller"], "agent", "trust", "cryptographically_verified")
	var verified *bool
	if !ok || !check(caller, "agent", s.Caller) || !check(caller, "trust", "single-user local trust") || json.Unmarshal(caller["cryptographically_verified"], &verified) != nil || verified == nil || *verified {
		return zero, bad
	}
	source, ok := selfCardObject(root["source"], "schema", "sha256", "revision", "card_root", "freshness", "observed_at")
	if !ok || !check(source, "schema", "product-card/v3") || !check(source, "revision", "sha256:"+s.CardSHA256) || !check(source, "card_root", filepath.Dir(s.CardPath)) || !check(source, "freshness", "unknown") || !bytes.Equal(bytes.TrimSpace(source["observed_at"]), []byte("null")) {
		return zero, bad
	}
	sourceHash, ok := selfCardString(source, "sha256")
	if !ok || sourceHash != s.CardSHA256 {
		return zero, bad
	}
	self, ok := selfCardObject(root["self"], "key", "resolved_name", "role", "project", "project_state")
	if !ok || !check(self, "key", s.ExpectedMember) || !check(self, "resolved_name", s.ExpectedName) || !check(self, "role", s.ExpectedRole) {
		return zero, bad
	}
	var project *string
	if json.Unmarshal(self["project"], &project) != nil {
		return zero, bad
	}
	if s.ExpectedProject == nil {
		if project != nil || !check(self, "project_state", "undeclared") {
			return zero, bad
		}
	} else if project == nil || *project != *s.ExpectedProject || !check(self, "project_state", "declared") {
		return zero, bad
	}
	return SelfCardFacts{MemberKey: s.ExpectedMember, ResolvedName: s.ExpectedName, Role: s.ExpectedRole, Project: project}, nil
}

type selfCardBuffer struct {
	b        bytes.Buffer
	limit    int
	exceeded bool
}

func (b *selfCardBuffer) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.b.Len() {
		b.exceeded = true
		return 0, errors.New("source output limit")
	}
	return b.b.Write(raw)
}

// SelfCardExecRunner has its own positive environment policy. In particular it
// admits only the same public caller; TaskFactsExecRunner's A2A ban is unchanged.
// Run returns bounded diagnostics for source-layer refusal, never to a consumer.
type SelfCardExecRunner struct{}

func (SelfCardExecRunner) Run(ctx context.Context, c SelfCardCommand) (SelfCardOutput, error) {
	zero := SelfCardOutput{}
	if !policyContext(ctx) || !taskAbsolute(c.Executable) || !taskAbsolute(c.Directory) || c.Environment == nil {
		return zero, Refusal{"source_unavailable"}
	}
	public := map[string]string{}
	for _, entry := range c.Environment {
		key, value, ok := strings.Cut(entry, "=")
		_, duplicate := public[key]
		if !ok || duplicate {
			return zero, Refusal{"source_unavailable"}
		}
		public[key] = value
	}
	if _, ok := selfCardEnvironment(public, c.Caller); !ok {
		return zero, Refusal{"source_unavailable"}
	}
	cmd := exec.CommandContext(ctx, c.Executable, c.Args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = c.Directory, append([]string{}, c.Environment...), time.Second
	stdout, stderr := &selfCardBuffer{limit: MaxSelfCardSourceOutput}, &selfCardBuffer{limit: maxSelfCardSourceStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return zero, Refusal{"source_conflict"}
	}
	if err != nil {
		return zero, Refusal{"source_unavailable"}
	}
	return SelfCardOutput{Stdout: stdout.b.Bytes(), Stderr: stderr.b.Bytes()}, nil
}
