package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type selfCardRunFunc func(context.Context, SelfCardCommand) (SelfCardOutput, error)

func (f selfCardRunFunc) Run(ctx context.Context, c SelfCardCommand) (SelfCardOutput, error) {
	return f(ctx, c)
}

func selfCardFixture(t *testing.T) (PulseSelfFacts, context.Context, map[string]any) {
	t.Helper()
	t.Setenv("A2A_AGENT", "fixture.self")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	project := "fixture/project"
	s := PulseSelfFacts{Executable: "/admitted/pulse", Directory: t.TempDir(), CardPath: "/admitted/product-card.yaml", CardSHA256: strings.Repeat("a", 64), Caller: "fixture.self", ExpectedMember: "fixture/self", ExpectedName: "@fixture.self:example.test", ExpectedRole: "orchestrator", ExpectedProject: &project, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "LC_ALL": "C", "A2A_AGENT": "fixture.self"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	doc := map[string]any{
		"schema": "pulse-view/v1", "mode": "local-unsigned", "member": s.ExpectedMember,
		"known_limit":       "caller not cryptographically verified; single-user local trust",
		"caller":            map[string]any{"agent": s.Caller, "cryptographically_verified": false, "trust": "single-user local trust"},
		"source":            map[string]any{"schema": "product-card/v3", "sha256": s.CardSHA256, "revision": "sha256:" + s.CardSHA256, "card_root": filepath.Dir(s.CardPath), "freshness": "unknown", "observed_at": nil},
		"self":              map[string]any{"key": s.ExpectedMember, "resolved_name": s.ExpectedName, "role": s.ExpectedRole, "project": project, "project_state": "declared", "profile": map[string]any{"selected": map[string]any{"version": "declaration only"}}},
		"query_observed_at": "2026-11-04T00:00:00Z", "observations": map[string]any{"runtime": map[string]any{"state": "unknown"}}, "peers": []any{},
	}
	return s, ctx, doc
}

func selfCardRaw(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func selfCardRefusal(t *testing.T, got SelfCardObservation, err error, want string) {
	t.Helper()
	var refusal Refusal
	if !errors.As(err, &refusal) || refusal.Reason != want || !reflect.DeepEqual(got, SelfCardObservation{}) {
		t.Fatalf("wanted %s with zero facts; got %#v / %v", want, got, err)
	}
}

// Valid declared and undeclared self projects preserve source facts and an exact
// fixed one-call command, with no profile/runtime/clock fields in the port.
func TestSelfCardFactsPositive(t *testing.T) {
	for _, mode := range []string{"declared", "undeclared", "unicode", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, doc := selfCardFixture(t)
			self := doc["self"].(map[string]any)
			switch mode {
			case "undeclared":
				s.ExpectedProject = nil
				self["project"], self["project_state"] = nil, "undeclared"
				s.ExpectedRole, self["role"] = "keeper", "keeper"
			case "unicode":
				project := "fixture/проект"
				s.ExpectedProject = &project
				self["project"] = project
			case "metadata":
				doc["extra"] = map[string]any{"合法": []any{true, nil, "opaque"}}
				self["future_metadata"] = "ignored"
			}
			calls := 0
			s.Runner = selfCardRunFunc(func(runCtx context.Context, c SelfCardCommand) (SelfCardOutput, error) {
				calls++
				deadline, _ := runCtx.Deadline()
				if time.Until(deadline) > 29*time.Second || c.Executable != s.Executable || c.Directory != s.Directory || c.Caller != s.Caller || !reflect.DeepEqual(c.Args, []string{"me", "--local-unsigned", "--card", s.CardPath, "--revision", "sha256:" + s.CardSHA256, "--format", "json"}) {
					t.Fatal("fixed command/deadline changed", c)
				}
				env, ok := selfCardEnvironment(s.Environment, s.Caller)
				if !ok || !reflect.DeepEqual(c.Environment, env) {
					t.Fatal("explicit environment changed")
				}
				return SelfCardOutput{Stdout: selfCardRaw(t, doc)}, nil
			})
			got, err := s.Observe(ctx)
			if err != nil || calls != 1 || got.SourceSHA256 != s.CardSHA256 || got.Facts.MemberKey != s.ExpectedMember || got.Facts.ResolvedName != s.ExpectedName || got.Facts.Role != s.ExpectedRole || !reflect.DeepEqual(got.Facts.Project, s.ExpectedProject) {
				t.Fatal("valid observation changed/refused", got, err, calls)
			}
			if s.ExpectedProject != nil {
				*s.ExpectedProject = "changed after call"
				if *got.Facts.Project == *s.ExpectedProject {
					t.Fatal("retained mutable configuration pointer")
				}
			}
		})
	}
}

// Each well-formed neighboring conflict yields the intended source_conflict and
// zero facts; the adjacent Positive tests establish the same source can succeed.
func TestSelfCardFactsConflicts(t *testing.T) {
	cases := []struct {
		name, object, key string
		value             any
	}{
		{"schema", "root", "schema", "pulse-view/v2"}, {"mode", "root", "mode", "signed"}, {"member", "root", "member", "fixture/other"}, {"limit", "root", "known_limit", "verified"},
		{"caller", "caller", "agent", "other"}, {"verified", "caller", "cryptographically_verified", true}, {"null-verified", "caller", "cryptographically_verified", nil}, {"trust", "caller", "trust", "cryptographic"},
		{"wrong-pin", "source", "sha256", strings.Repeat("b", 64)}, {"wrong-revision", "source", "revision", "sha256:" + strings.Repeat("b", 64)}, {"card-schema", "source", "schema", "product-card/v4"}, {"card-root", "source", "card_root", "/elsewhere"}, {"freshness", "source", "freshness", "current"}, {"observed", "source", "observed_at", "2026-11-04T00:00:00Z"},
		{"key", "self", "key", "fixture/other"}, {"domain", "self", "resolved_name", "@fixture.self:other.test"}, {"role", "self", "role", "keeper"}, {"project", "self", "project", "fixture/other"}, {"null-project", "self", "project", nil}, {"project-state", "self", "project_state", "undeclared"},
		{"null-member", "root", "member", nil}, {"number-role", "self", "role", 5}, {"object-source-pin", "source", "sha256", map[string]any{}}, {"exit0-error", "root", "error", "not a success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx, doc := selfCardFixture(t)
			object := doc
			if tc.object != "root" {
				object = doc[tc.object].(map[string]any)
			}
			object[tc.key] = tc.value
			calls := 0
			s.Runner = selfCardRunFunc(func(context.Context, SelfCardCommand) (SelfCardOutput, error) {
				calls++
				return SelfCardOutput{Stdout: selfCardRaw(t, doc)}, nil
			})
			got, err := s.Observe(ctx)
			selfCardRefusal(t, got, err, "source_conflict")
			if calls != 1 {
				t.Fatal("retry or no source read", calls)
			}
		})
	}
	for _, path := range []string{"schema", "member", "caller.agent", "caller.cryptographically_verified", "source.sha256", "source.revision", "source.observed_at", "self.key", "self.project", "self.project_state"} {
		t.Run("missing-"+path, func(t *testing.T) {
			s, ctx, doc := selfCardFixture(t)
			parts := strings.Split(path, ".")
			object := doc
			if len(parts) == 2 {
				object = doc[parts[0]].(map[string]any)
			}
			delete(object, parts[len(parts)-1])
			s.Runner = selfCardRunFunc(func(context.Context, SelfCardCommand) (SelfCardOutput, error) {
				return SelfCardOutput{Stdout: selfCardRaw(t, doc)}, nil
			})
			got, err := s.Observe(ctx)
			selfCardRefusal(t, got, err, "source_conflict")
		})
	}
}

// Generic JSON validation includes ignored metadata; transport success alone
// cannot admit aliases, malformed scalars, duplicates, trailing data or stderr.
func TestSelfCardFactsEncoding(t *testing.T) {
	for _, mode := range []string{"duplicate", "ignored-duplicate", "alias-root", "alias-source", "alias-self", "invalid-utf8", "surrogate", "trailing", "truncated", "depth", "output-overflow", "stderr", "stderr-capacity", "error-envelope", "array"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, doc := selfCardFixture(t)
			raw := selfCardRaw(t, doc)
			stderr := []byte(nil)
			switch mode {
			case "duplicate":
				raw = append([]byte(`{"schema":"pulse-view/v1",`), raw[1:]...)
			case "ignored-duplicate":
				raw = append([]byte(`{"ignored":{"x":1,"x":2},`), raw[1:]...)
			case "alias-root":
				doc["Schema"] = "pulse-view/v1"
				raw = selfCardRaw(t, doc)
			case "alias-source":
				doc["source"].(map[string]any)["SHA256"] = s.CardSHA256
				raw = selfCardRaw(t, doc)
			case "alias-self":
				doc["self"].(map[string]any)["Role"] = s.ExpectedRole
				raw = selfCardRaw(t, doc)
			case "invalid-utf8":
				raw = append([]byte("{\"ignored\":\"\xff\","), raw[1:]...)
			case "surrogate":
				raw = append([]byte(`{"ignored":"\ud800",`), raw[1:]...)
			case "trailing":
				raw = append(raw, []byte(` {}`)...)
			case "truncated":
				raw = raw[:len(raw)-1]
			case "depth":
				raw = append([]byte(`{"ignored":`+strings.Repeat("[", 65)+"0"+strings.Repeat("]", 65)+","), raw[1:]...)
			case "output-overflow":
				raw = []byte(strings.Repeat(" ", MaxSelfCardSourceOutput+1))
			case "stderr":
				stderr = []byte("diagnostic")
			case "stderr-capacity":
				stderr = []byte(strings.Repeat("x", maxSelfCardSourceStderr))
			case "error-envelope":
				raw = []byte(`{"error":"unavailable"}`)
			case "array":
				raw = []byte(`[]`)
			}
			s.Runner = selfCardRunFunc(func(context.Context, SelfCardCommand) (SelfCardOutput, error) {
				return SelfCardOutput{Stdout: raw, Stderr: stderr}, nil
			})
			got, err := s.Observe(ctx)
			selfCardRefusal(t, got, err, "source_conflict")
		})
	}
}

// Invalid trusted configuration/context refuses before a call; cancellation or
// source failure after a single call never yields partial facts or retries.
func TestSelfCardFactsAdmission(t *testing.T) {
	for _, mode := range []string{"nil-context", "unbounded", "canceled", "identity", "missing-identity", "bad-env", "missing-env-agent", "home", "path", "nil-runner", "relative-tool", "relative-card", "relative-directory", "bad-pin", "bad-member", "bad-project", "source-error", "cancel-in-source"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, doc := selfCardFixture(t)
			calls := 0
			want := "source_unavailable"
			s.Runner = selfCardRunFunc(func(context.Context, SelfCardCommand) (SelfCardOutput, error) {
				calls++
				if mode == "source-error" {
					return SelfCardOutput{Stdout: []byte("private")}, errors.New("private")
				}
				return SelfCardOutput{Stdout: selfCardRaw(t, doc)}, nil
			})
			switch mode {
			case "nil-context":
				ctx = nil
				want = "authority_unknown"
			case "unbounded":
				ctx = context.Background()
				want = "authority_unknown"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = "authority_unknown"
			case "identity":
				s.Caller = "other"
				want = "authority_denied"
			case "missing-identity":
				t.Setenv("A2A_AGENT", "")
				want = "authority_denied"
			case "bad-env":
				s.Environment["A2A_CONFIG_DIR"] = "/forbidden"
			case "missing-env-agent":
				delete(s.Environment, "A2A_AGENT")
			case "home":
				s.Environment["HOME"] = "/fake"
			case "path":
				s.Environment["PATH"] = "relative"
			case "nil-runner":
				s.Runner = nil
			case "relative-tool":
				s.Executable = "pulse"
			case "relative-card":
				s.CardPath = "card.yaml"
			case "relative-directory":
				s.Directory = "."
			case "bad-pin":
				s.CardSHA256 = "not-a-pin"
			case "bad-member":
				s.ExpectedMember = ""
			case "bad-project":
				v := ""
				s.ExpectedProject = &v
			case "cancel-in-source":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				s.Runner = selfCardRunFunc(func(context.Context, SelfCardCommand) (SelfCardOutput, error) {
					calls++
					cancel()
					return SelfCardOutput{Stdout: selfCardRaw(t, doc)}, nil
				})
				want = "authority_unknown"
			}
			got, err := s.Observe(ctx)
			selfCardRefusal(t, got, err, want)
			expected := 0
			if mode == "source-error" || mode == "cancel-in-source" {
				expected = 1
			}
			if calls != expected {
				t.Fatal("unexpected runner count", calls)
			}
		})
	}
}
