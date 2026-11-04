package workbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const LocalAccessContract = "swarma-router-local-access-1"
const MaxLocalPolicyBytes = 65536
const MaxLocalPolicyRules = 1024

// PolicyReadReceipt identifies one successful stable policy read, not a grant.
// It contains no policy contents and is available only to the trusted embedding.
type PolicyReadReceipt struct {
	SHA256    string
	CheckedAt time.Time
}

// LocalPolicySource reads one explicit owner-managed policy. Configure its
// exported fields before use and do not mutate them concurrently. It must not be
// copied after use. No path or expected UID comes from request JSON.
// This is single-user attenuation, not authentication against the same UID.
type LocalPolicySource struct {
	Path     string
	OwnerUID uint32
	mu       sync.Mutex
	last     PolicyReadReceipt
	hasLast  bool
	// Internal deterministic read-boundary fault seam, never request/config data.
	step func(string)
}

var _ PolicySource = (*LocalPolicySource)(nil)

// LastRead is process evidence only. No decision is cached or reused by a check.
func (a *LocalPolicySource) LastRead() (PolicyReadReceipt, bool) {
	if a == nil {
		return PolicyReadReceipt{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.hasLast
}

// Decide reads and validates the whole current policy for this exact action.
// Invalid input, unavailable source and inactive/missing rules stay unknown.
func (a *LocalPolicySource) Decide(ctx context.Context, k OperationKey, action PolicyAction, clock func() time.Time) PolicyDecision {
	if a == nil || !policyContext(ctx) || !validPolicyAction(action) || !ref(k.Caller) || !ref(k.Project) || !ref(k.CorrelationID) || clock == nil {
		return PolicyUnknown
	}
	raw, ok := a.read(ctx)
	if !ok {
		return PolicyUnknown
	}
	rules, ok := parseLocalPolicy(raw)
	if !ok || !policyContext(ctx) {
		return PolicyUnknown
	}
	now := clock()
	if !policyContext(ctx) || !policyTime(now) {
		return PolicyUnknown
	}
	digest := sha256.Sum256(raw)
	a.mu.Lock()
	a.last, a.hasLast = PolicyReadReceipt{hex.EncodeToString(digest[:]), now}, true
	a.mu.Unlock()
	for _, r := range rules {
		if r.key != k || now.Before(r.from) || !now.Before(r.until) {
			continue
		}
		if r.decisions[string(action)] == "deny" {
			return PolicyDeny
		}
		return PolicyAllow
	}
	return PolicyUnknown
}

type localPolicyRule struct {
	key       OperationKey
	decisions map[string]string
	from      time.Time
	until     time.Time
}

func parseLocalPolicy(raw []byte) ([]localPolicyRule, bool) {
	if len(raw) == 0 || len(raw) > MaxLocalPolicyBytes || !utf8.Valid(raw) || !scalarEscapes(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, false
	}
	seen := map[string]bool{}
	seenKeys := map[OperationKey]bool{}
	rules := []localPolicyRule{}
	for d.More() {
		t, err = d.Token()
		name, ok := t.(string)
		if err != nil || !ok || seen[name] {
			return nil, false
		}
		seen[name] = true
		switch name {
		case "contract":
			t, err = d.Token()
			if err != nil || t != LocalAccessContract {
				return nil, false
			}
		case "rules":
			t, err = d.Token()
			if err != nil || t != json.Delim('[') {
				return nil, false
			}
			for d.More() {
				if len(rules) == MaxLocalPolicyRules {
					return nil, false
				}
				v, err := readValue(d, 2)
				m, ok := v.(map[string]any)
				if err != nil || !ok || !keys(m, "caller", "project", "correlation_id", "read", "prepare", "observe", "valid_from", "expires_at") || !ref(m["caller"]) || !ref(m["project"]) || !ref(m["correlation_id"]) {
					return nil, false
				}
				r := localPolicyRule{key: OperationKey{m["caller"].(string), m["project"].(string), m["correlation_id"].(string)}, decisions: map[string]string{}}
				if seenKeys[r.key] {
					return nil, false
				}
				seenKeys[r.key] = true
				for _, action := range []string{"read", "prepare", "observe"} {
					value, _ := m[action].(string)
					if value != "allow" && value != "deny" {
						return nil, false
					}
					r.decisions[action] = value
				}
				from, ok1 := m["valid_from"].(string)
				until, ok2 := m["expires_at"].(string)
				var valid1, valid2 bool
				r.from, valid1 = timestamp(from)
				r.until, valid2 = timestamp(until)
				if !ok1 || !ok2 || !valid1 || !valid2 || !r.from.Before(r.until) {
					return nil, false
				}
				rules = append(rules, r)
			}
			t, err = d.Token()
			if err != nil || t != json.Delim(']') {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	t, err = d.Token()
	if err != nil || t != json.Delim('}') || len(seen) != 2 {
		return nil, false
	}
	_, err = d.Token()
	return rules, err == io.EOF
}

func policyPrivate(f *os.File, info os.FileInfo, uid uint32, directory bool) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return ok && st.Uid == uid && info.IsDir() == directory && (directory || info.Mode().IsRegular()) && info.Mode().Perm() == mode && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && operationNoExtendedACL(f)
}

func policySame(a, b os.FileInfo) bool {
	x, ok1 := a.Sys().(*syscall.Stat_t)
	y, ok2 := b.Sys().(*syscall.Stat_t)
	return ok1 && ok2 && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime() == b.ModTime() && policyChangeTime(x) == policyChangeTime(y) && x.Uid == y.Uid && x.Gid == y.Gid && x.Nlink == y.Nlink
}

// read anchors the final name to an opened trusted private parent. O_NONBLOCK
// prevents a policy FIFO from blocking before its regular-file check. Filesystem
// operations are synchronous; context is checked around them, not kernel-preempted.
func (a *LocalPolicySource) read(ctx context.Context) ([]byte, bool) {
	if !filepath.IsAbs(a.Path) || filepath.Clean(a.Path) != a.Path || filepath.Base(a.Path) == "." || filepath.Base(a.Path) == string(filepath.Separator) {
		return nil, false
	}
	parentPath, name := filepath.Dir(a.Path), filepath.Base(a.Path)
	parent, err := os.OpenFile(parentPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer parent.Close()
	beforeParent, err := parent.Stat()
	if err != nil || !policyPrivate(parent, beforeParent, a.OwnerUID, true) || !policyContext(ctx) {
		return nil, false
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !policyPrivate(f, before, a.OwnerUID, false) || before.Size() > MaxLocalPolicyBytes || !policyContext(ctx) {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxLocalPolicyBytes+1))
	if a.step != nil {
		a.step("after-read")
	}
	after, statErr := f.Stat()
	if err != nil || statErr != nil || len(raw) > MaxLocalPolicyBytes || int64(len(raw)) != before.Size() || !policySame(before, after) || !policyContext(ctx) {
		return nil, false
	}
	// Re-open only the anchored name and compare its identity, never contents.
	checkFD, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false
	}
	check := os.NewFile(uintptr(checkFD), name)
	current, statErr := check.Stat()
	closeErr := check.Close()
	currentParent, parentErr := os.Lstat(parentPath)
	afterParent, parentStatErr := parent.Stat()
	if statErr != nil || closeErr != nil || parentErr != nil || parentStatErr != nil || !policySame(after, current) || !os.SameFile(beforeParent, currentParent) || !policyPrivate(parent, afterParent, a.OwnerUID, true) || !policyContext(ctx) {
		return nil, false
	}
	return raw, true
}
