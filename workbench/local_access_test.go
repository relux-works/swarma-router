package workbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func accessRule(k OperationKey, now time.Time) map[string]any {
	return map[string]any{"caller": k.Caller, "project": k.Project, "correlation_id": k.CorrelationID, "read": "allow", "prepare": "allow", "observe": "allow", "valid_from": now.Add(-time.Minute).Format(wireTime), "expires_at": now.Add(time.Minute).Format(wireTime)}
}
func accessBytes(t *testing.T, rules ...map[string]any) []byte {
	t.Helper()
	if rules == nil {
		rules = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"contract": LocalAccessContract, "rules": rules})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func accessWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	// Only isolated fixture paths are written. Atomic replacement tests that a
	// completed new policy is legitimate between, but not within, stable reads.
	if err := os.WriteFile(path+".next", b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
}

type fixturePolicyAccess struct {
	*PolicyAccess
	*LocalPolicySource
}

func accessFixture(t *testing.T) (*fixturePolicyAccess, OperationKey, context.Context, time.Time) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC)
	k := OperationKey{"caller", "project", "example"}
	source := &LocalPolicySource{Path: filepath.Join(parent, "policy.json"), OwnerUID: uint32(os.Getuid())}
	a := &fixturePolicyAccess{&PolicyAccess{Local: LocalContext{k.Caller, k.Project}, Clock: func() time.Time { return now }, Source: source}, source}
	accessWrite(t, a.Path, accessBytes(t, accessRule(k, now)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return a, k, ctx, now
}
func accessReason(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("valid local grant refused: %v", err)
		}
		return
	}
	r, ok := err.(Refusal)
	if !ok || r.Reason != want {
		t.Fatalf("wanted closed %s, got %T %v", want, err, err)
	}
}

// Each explicit action is independent; exact scope and canonical source validity
// matter. All source data are isolated local fixtures, not production authority.
func TestLocalPolicyActionsAndExactRefs(t *testing.T) {
	for _, action := range []string{"read", "prepare", "observe"} {
		for _, decision := range []string{"allow", "deny"} {
			t.Run(action+"/"+decision, func(t *testing.T) {
				a, k, ctx, now := accessFixture(t)
				r := accessRule(k, now)
				r[action] = decision
				accessWrite(t, a.Path, accessBytes(t, r))
				methods := map[string]func(context.Context, OperationKey) error{"read": a.Read, "prepare": a.Prepare, "observe": a.Observe}
				for name, method := range methods {
					want := ""
					if name == action && decision == "deny" {
						want = "authority_denied"
					}
					accessReason(t, method(ctx, k), want)
				}
			})
		}
	}
	for _, value := range []string{"*", "թեստ 世界\x00\n ", strings.Repeat("界", 85)} {
		t.Run(fmt.Sprintf("ref-%x", []byte(value)[:1]), func(t *testing.T) {
			a, k, ctx, now := accessFixture(t)
			k.Caller, k.Project, k.CorrelationID = value, value, value
			a.Local = LocalContext{k.Caller, k.Project}
			accessWrite(t, a.Path, accessBytes(t, accessRule(k, now)))
			accessReason(t, a.Read(ctx, k), "")
			wrong := k
			wrong.CorrelationID += "x"
			accessReason(t, a.Read(ctx, wrong), "authority_unknown")
		})
	}
}

// Whole-policy validation rejects bad nonmatching rules too. Complete empty
// policy and inactive rules grant nothing; only active explicit deny is denial.
func TestLocalPolicySchemaAndValidity(t *testing.T) {
	cases := []string{"empty", "missing-rule", "duplicate-tuple", "unknown-top", "unknown-rule", "missing-field", "bad-decision", "duplicate-key", "escaped-duplicate", "null-rules", "bad-contract", "noncanonical-time", "bad-interval", "not-yet", "expired", "invalid-other-rule", "trailing-object", "invalid-utf8", "surrogate", "too-large"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			a, k, ctx, now := accessFixture(t)
			r := accessRule(k, now)
			b := accessBytes(t, r)
			switch mode {
			case "empty":
				b = accessBytes(t)
			case "missing-rule":
				r["correlation_id"] = "another"
				b = accessBytes(t, r)
			case "duplicate-tuple":
				b = accessBytes(t, r, r)
			case "unknown-top":
				b = bytes.Replace(b, []byte(`"rules":`), []byte(`"extra":true,"rules":`), 1)
			case "unknown-rule":
				r["extra"] = "allow"
				b = accessBytes(t, r)
			case "missing-field":
				delete(r, "observe")
				b = accessBytes(t, r)
			case "bad-decision":
				r["read"] = true
				b = accessBytes(t, r)
			case "duplicate-key":
				b = bytes.Replace(b, []byte(`"read":"allow"`), []byte(`"read":"allow","read":"allow"`), 1)
			case "escaped-duplicate":
				b = bytes.Replace(b, []byte(`"read":"allow"`), []byte(`"read":"allow","\u0072ead":"deny"`), 1)
			case "null-rules":
				b = []byte(`{"contract":"swarma-router-local-access-1","rules":null}`)
			case "bad-contract":
				b = bytes.Replace(b, []byte(LocalAccessContract), []byte("unknown"), 1)
			case "noncanonical-time":
				r["valid_from"] = now.Add(-time.Minute).Format(time.RFC3339)
				b = accessBytes(t, r)
			case "bad-interval":
				r["valid_from"] = r["expires_at"]
				b = accessBytes(t, r)
			case "not-yet":
				r["valid_from"] = now.Add(time.Millisecond).Format(wireTime)
				b = accessBytes(t, r)
			case "expired":
				r["expires_at"] = now.Format(wireTime)
				b = accessBytes(t, r)
			case "invalid-other-rule":
				other := accessRule(OperationKey{"other", "other", "other"}, now)
				other["read"] = "ALLOW"
				b = accessBytes(t, r, other)
			case "trailing-object":
				b = append(b, []byte(` {}`)...)
			case "invalid-utf8":
				b = append(b, 0xff)
			case "surrogate":
				b = bytes.Replace(b, []byte(`"caller":"caller"`), []byte(`"caller":"\ud800"`), 1)
			case "too-large":
				b = append(b, bytes.Repeat([]byte(" "), MaxLocalPolicyBytes-len(b)+1)...)
			}
			accessWrite(t, a.Path, b)
			accessReason(t, a.Read(ctx, k), "authority_unknown")
		})
	}
	// Closed boundaries retain valid_from equality, maximum file bytes, and more
	// than the wire parser's unrelated 64-item array bound for this source schema.
	a, k, ctx, now := accessFixture(t)
	r := accessRule(k, now)
	r["valid_from"] = now.Format(wireTime)
	b := accessBytes(t, r)
	b = append(b, bytes.Repeat([]byte(" "), MaxLocalPolicyBytes-len(b))...)
	accessWrite(t, a.Path, b)
	accessReason(t, a.Read(ctx, k), "")
	rules := []map[string]any{r}
	for i := 0; i < 65; i++ {
		rules = append(rules, accessRule(OperationKey{"caller", "project", fmt.Sprintf("other-%d", i)}, now))
	}
	accessWrite(t, a.Path, accessBytes(t, rules...))
	accessReason(t, a.Read(ctx, k), "")
}

// Unsafe/unavailable paths refuse without repair or fallback. Configured scope
// mismatch and canceled/unbounded calls refuse before reading a policy.
func TestLocalPolicyCustodyAndContext(t *testing.T) {
	for _, mode := range []string{"missing", "file-mode", "parent-mode", "owner", "symlink", "parent-symlink", "directory", "relative", "caller", "project", "canceled", "nil-context", "unbounded", "nil-clock", "zero-clock", "special-mode", "fifo"} {
		t.Run(mode, func(t *testing.T) {
			a, k, ctx, _ := accessFixture(t)
			want := "authority_unknown"
			switch mode {
			case "missing":
				if err := os.Remove(a.Path); err != nil {
					t.Fatal(err)
				}
			case "special-mode":
				if err := os.Chmod(a.Path, 0600|os.ModeSetuid); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(a.Path); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(a.Path, 0600); err != nil {
					t.Fatal(err)
				}
			case "file-mode":
				if err := os.Chmod(a.Path, 0644); err != nil {
					t.Fatal(err)
				}
			case "parent-mode":
				if err := os.Chmod(filepath.Dir(a.Path), 0755); err != nil {
					t.Fatal(err)
				}
			case "owner":
				a.OwnerUID++
			case "symlink":
				if err := os.Rename(a.Path, a.Path+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(a.Path+".real", a.Path); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				link := filepath.Join(t.TempDir(), "parent")
				if err := os.Symlink(filepath.Dir(a.Path), link); err != nil {
					t.Fatal(err)
				}
				a.Path = filepath.Join(link, filepath.Base(a.Path))
			case "directory":
				a.Path = filepath.Dir(a.Path)
			case "relative":
				a.Path = "policy.json"
			case "caller":
				k.Caller = "other"
				want = "authority_denied"
			case "project":
				k.Project = "other"
				want = "authority_denied"
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "nil-context":
				ctx = nil
			case "unbounded":
				ctx = context.Background()
			case "nil-clock":
				a.Clock = nil
			case "zero-clock":
				a.Clock = func() time.Time { return time.Time{} }
			}
			accessReason(t, a.Read(ctx, k), want)
		})
	}
}

// Replacement/in-place mutation/parent permission change during the captured
// read refuses; a completed replacement before a later check is read afresh.
func TestLocalPolicyStableReadAndReplacement(t *testing.T) {
	for _, mode := range []string{"replace", "in-place", "parent-mode", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a, k, ctx, now := accessFixture(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			a.step = func(string) {
				switch mode {
				case "replace":
					accessWrite(t, a.Path, accessBytes(t, accessRule(k, now)))
				case "in-place":
					if err := os.WriteFile(a.Path, accessBytes(t), 0600); err != nil {
						t.Fatal(err)
					}
				case "parent-mode":
					if err := os.Chmod(filepath.Dir(a.Path), 0755); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
			}
			accessReason(t, a.Read(ctx, k), "authority_unknown")
			if _, ok := a.LastRead(); ok {
				t.Fatal("unstable read recorded as successful")
			}
		})
	}
	a, k, ctx, now := accessFixture(t)
	accessReason(t, a.Read(ctx, k), "")
	first, ok := a.LastRead()
	if !ok {
		t.Fatal("missing successful read evidence")
	}
	r := accessRule(k, now)
	r["prepare"] = "deny"
	b := accessBytes(t, r)
	accessWrite(t, a.Path, b)
	accessReason(t, a.Prepare(ctx, k), "authority_denied")
	last, ok := a.LastRead()
	expected := sha256.Sum256(b)
	if !ok || last.SHA256 != hex.EncodeToString(expected[:]) || first.SHA256 == last.SHA256 {
		t.Fatal("replacement did not produce fresh read evidence")
	}
}

// Real Engine control paths use the actual file adapter, while the remaining
// owner/lifecycle ports stay isolated fixtures. No process or message is created.
func TestLocalPolicyEngineAttenuation(t *testing.T) {
	for _, action := range []string{"start", "reuse"} {
		t.Run(action, func(t *testing.T) {
			f, e, req := newOperationFixture(t, action)
			a, k, _, _ := accessFixture(t)
			k = req.Key()
			a.Local = LocalContext{k.Caller, k.Project}
			a.Clock = func() time.Time { return f.now }
			e.Access = a
			accessWrite(t, a.Path, accessBytes(t, accessRule(k, f.now)))
			response(t, invocation(t, e, req), "receipt", "ready", "ok", 0)
			original := bytes.Clone(f.raw)
			r := accessRule(k, f.now)
			r["prepare"], r["observe"] = "deny", "deny"
			accessWrite(t, a.Path, accessBytes(t, r))
			pure := e
			pure.Planner, pure.Lifecycle, pure.Clock = nil, nil, nil
			got := response(t, invocation(t, pure, req), "receipt", "ready", "ok", 3)
			if got["historical"] != true || got["ready"] != nil || !bytes.Equal(original, f.raw) {
				t.Fatal("read-only replay changed history or required action grants")
			}
			observed, commits := f.observe, f.commits
			response(t, invocation(t, e, statusRequest(t, req)), "recovery", "unknown", "authority_denied", 3)
			if f.observe != observed || f.commits != commits || !bytes.Equal(original, f.raw) {
				t.Fatal("denied Status invoked observation or mutated state")
			}
			r["observe"] = "allow"
			accessWrite(t, a.Path, accessBytes(t, r))
			response(t, invocation(t, e, statusRequest(t, req)), "receipt", "ready", "ok", 0)
			if f.observe != observed+1 || f.resolve != 1 || f.start > 1 {
				t.Fatal("Observe grant selected or started again")
			}
		})
	}
	for _, mode := range []string{"read-deny", "prepare-deny", "missing", "malformed"} {
		t.Run("no-effects/"+mode, func(t *testing.T) {
			f, e, req := newOperationFixture(t, "start")
			a, _, _, _ := accessFixture(t)
			a.Clock = func() time.Time { return f.now }
			e.Access = a
			r := accessRule(req.Key(), f.now)
			switch mode {
			case "read-deny":
				r["read"] = "deny"
			case "prepare-deny":
				r["prepare"] = "deny"
			}
			accessWrite(t, a.Path, accessBytes(t, r))
			if mode == "missing" {
				if err := os.Remove(a.Path); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "malformed" {
				accessWrite(t, a.Path, []byte(`{"private-diagnostic":"never echo"}`))
			}
			out := invocation(t, e, req)
			if out.ExitCode() != 2 || bytes.Contains(out.Bytes(), []byte("private-diagnostic")) || f.raw != nil || f.resolve != 0 || f.prepare != 0 || f.start != 0 || f.commits != 0 {
				t.Fatal("local refusal leaked source data or performed work")
			}
			var frame map[string]any
			if json.Unmarshal(out.Bytes(), &frame) != nil {
				t.Fatal("invalid result")
			}
			code := frame["result"].(map[string]any)["code"]
			want := "authority_denied"
			if mode == "missing" || mode == "malformed" {
				want = "authority_unknown"
			}
			if code != want {
				t.Fatalf("wanted %s, got %v", want, code)
			}
		})
	}
}

// A valid positive control starts once; replacing the policy at the retained
// lower mapping denies/unknowns the SECOND Prepare check and permits zero Start.
func TestLocalPolicyPreStartRevocation(t *testing.T) {
	for _, mode := range []string{"valid", "deny", "remove", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			f, e, req := newOperationFixture(t, "start")
			a, _, _, _ := accessFixture(t)
			a.Clock = func() time.Time { return f.now }
			e.Access = a
			accessWrite(t, a.Path, accessBytes(t, accessRule(req.Key(), f.now)))
			changed := false
			f.onCommit = func(record operationRecord) {
				if changed || record.Lower == nil {
					return
				}
				changed = true
				switch mode {
				case "deny":
					r := accessRule(req.Key(), f.now)
					r["prepare"] = "deny"
					accessWrite(t, a.Path, accessBytes(t, r))
				case "remove":
					if err := os.Remove(a.Path); err != nil {
						t.Fatal(err)
					}
				case "malformed":
					accessWrite(t, a.Path, []byte(`{}`))
				}
			}
			out := invocation(t, e, req)
			if !changed {
				t.Fatal("revocation control not applied")
			}
			if mode == "valid" {
				response(t, out, "receipt", "ready", "ok", 0)
				if f.start != 1 {
					t.Fatal("valid start missing")
				}
				return
			}
			code := "authority_unknown"
			if mode == "deny" {
				code = "authority_denied"
			}
			response(t, out, "receipt", "refused", code, 2)
			if f.start != 0 {
				t.Fatal("revoked policy reached Start")
			}
		})
	}
}

// Concurrent checks share only sanitized last-read evidence, never a cached
// grant. This is race coverage on local fixture reads, not concurrent policy edits.
func TestLocalPolicyConcurrentEvidence(t *testing.T) {
	a, k, ctx, _ := accessFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Read(ctx, k); err != nil {
				t.Error(err)
			}
			a.LastRead()
		}()
	}
	wg.Wait()
}
