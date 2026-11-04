package localworkbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var common = []string{"caller_authority", "task_constraints", "card_member", "profile_runtime", "quality", "provider_limits", "manager_custody", "board_assignment", "messaging_admission"}
var reuse = []string{"current_configuration", "exact_generation", "ready_response", "occupancy"}
var start = []string{"complete_owned_absence", "preparation_authority", "launch_capability"}

func failure(code string) error { return errors.New(code) }

// Check typed input before encoding/json can replace malformed Go UTF-8 strings.
// The depth bound also refuses cyclic evidence instead of serializing a prefix.
func goStrings(v reflect.Value, depth int) bool {
	if depth > 64 {
		return false
	}
	if !v.IsValid() {
		return true
	}
	if v.Type() == reflect.TypeOf(time.Time{}) {
		return true
	}
	switch v.Kind() {
	case reflect.String:
		return v.Len() <= MaxSnapshotBytes && utf8.ValidString(v.String())
	case reflect.Pointer, reflect.Interface:
		return v.IsNil() || goStrings(v.Elem(), depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" && !goStrings(v.Field(i), depth+1) {
				return false
			}
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !goStrings(v.Index(i), depth+1) {
				return false
			}
		}
	}
	return true
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var tree any
	if d.Decode(&tree) != nil {
		return nil
	}
	canonicalTimes(tree)
	b, _ = json.Marshal(tree)
	return b
}
func canonicalTimes(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if contains([]string{"as_of", "read_at", "observed_at", "expires_at", "recorded_at", "birth"}, k) {
				if s, ok := v.(string); ok {
					if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
						x[k] = t.UTC().Format("2006-01-02T15:04:05.000Z")
					}
				}
			} else {
				canonicalTimes(v)
			}
		}
	case []any:
		for _, v := range x {
			canonicalTimes(v)
		}
	}
}
func hashString(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func text(s string, limit int, multiline bool) bool {
	if len(s) == 0 || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) && !(multiline && (c == '\n' || c == '\t')) || unicode.In(c, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) || c >= 0xfdd0 && c <= 0xfdef || c&0xffff >= 0xfffe {
			return false
		}
	}
	return true
}
func ref(s string) bool { return text(s, 256, false) }
func mxid(s string) bool {
	if !ref(s) || !strings.HasPrefix(s, "@") {
		return false
	}
	a, b, ok := strings.Cut(s[1:], ":")
	if !ok || a == "" || b == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func instant(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && offset == 0 && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000000 == 0
}
func config(c Configuration) bool {
	return ref(c.Member) && mxid(c.MXID) && ref(c.Runtime) && ref(c.Vendor) && ref(c.Model) && ref(c.Effort) && ref(c.Profile) && hashString(c.ProfileLock) && ref(c.Placement)
}
func execution(e *Execution, now time.Time) bool {
	return e != nil && ref(e.NativeID) && ref(e.Thread) && instant(e.Birth) && !e.Birth.After(now) && ref(e.Generation)
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func assertionGate(g string) bool {
	return contains([]string{"caller_authority", "task_constraints", "manager_custody", "board_assignment", "preparation_authority"}, g)
}
func gates(action string) []string {
	x := append([]string(nil), common...)
	if action == "reuse" {
		x = append(x, reuse...)
	}
	if action == "start" {
		x = append(x, start...)
	}
	return x
}

// RequestDigest canonicalizes semantic sets without modifying or retaining them.
// It is content identity only; it does not authenticate the caller or task.
func RequestDigest(r LocalRequest) (string, error) {
	r, err := normalize(r)
	if err != nil {
		return "", err
	}
	return digest(encode(r)), nil
}
func normalize(r LocalRequest) (LocalRequest, error) {
	bad := func() (LocalRequest, error) { return LocalRequest{}, failure("invalid_request") }
	if !mxid(r.Caller) || !ref(r.Project) || !ref(r.CorrelationID) || !ref(r.TaskID) || !text(r.Objective, 4096, true) || len(r.Cards) < 1 || len(r.Cards) > 16 || len(r.Configurations) < 1 || len(r.Configurations) > 16 {
		return bad()
	}
	r.Cards = append([]Card(nil), r.Cards...)
	r.Configurations = append([]Configuration(nil), r.Configurations...)
	members, identities := map[string]Card{}, map[string]bool{}
	for _, c := range r.Cards {
		if !ref(c.Member) || !mxid(c.MXID) || !hashString(c.SHA256) || identities[c.MXID] {
			return bad()
		}
		if _, exists := members[c.Member]; exists {
			return bad()
		}
		members[c.Member] = c
		identities[c.MXID] = true
	}
	sort.Slice(r.Cards, func(i, j int) bool { return string(encode(r.Cards[i])) < string(encode(r.Cards[j])) })
	seen := map[string]bool{}
	for _, c := range r.Configurations {
		card, ok := members[c.Member]
		key := string(encode(c))
		if !ok || !config(c) || card.MXID != c.MXID || seen[key] {
			return bad()
		}
		seen[key] = true
	}
	sort.Slice(r.Configurations, func(i, j int) bool { return string(encode(r.Configurations[i])) < string(encode(r.Configurations[j])) })
	if len(encode(r)) > MaxRequestBytes {
		return bad()
	}
	return r, nil
}
func TargetDigest(b Binding) string { return digest(encode(b)) }
func clone[T any](v T) T {
	var out T
	b, err := json.Marshal(v)
	if err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// DecodeInputs reads only the internal snapshot representation. This is not a
// public prepare/status envelope or an acquisition/authentication operation.
// Duplicate decoded keys, invalid scalars, unknown fields and null/omitted
// required structure are rejected, rather than normalized into positive facts.
func DecodeInputs(raw []byte) (LocalOwnerInputs, error) {
	var out LocalOwnerInputs
	if len(raw) == 0 || len(raw) > MaxSnapshotBytes || !utf8.Valid(raw) || !scalars(raw) {
		return out, failure("source_conflict")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !jsonValue(d, 0) {
		return out, failure("source_conflict")
	}
	if _, err := d.Token(); err != io.EOF {
		return out, failure("source_conflict")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil {
		return LocalOwnerInputs{}, failure("source_conflict")
	}
	if out.Contract != Contract || !hashString(out.RequestDigest) || !contains([]string{"partial", "complete"}, out.Scope) || len(out.Targets) > 16 {
		return LocalOwnerInputs{}, failure("source_conflict")
	}
	var actual, expected any
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&actual)
	d = json.NewDecoder(bytes.NewReader(encode(out)))
	d.UseNumber()
	if d.Decode(&expected) != nil || !sameShape(actual, expected, "") {
		return LocalOwnerInputs{}, failure("source_conflict")
	}
	return out, nil
}
func sameShape(a, b any, key string) bool {
	switch x := b.(type) {
	case map[string]any:
		y, ok := a.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !sameShape(w, v, k) {
				return false
			}
		}
		return true
	case []any:
		y, ok := a.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameShape(y[i], x[i], key) {
				return false
			}
		}
		return true
	case string:
		y, ok := a.(string)
		if !ok {
			return false
		}
		if contains([]string{"as_of", "read_at", "observed_at", "expires_at", "recorded_at", "birth"}, key) {
			return x == y
		}
		return true
	case bool:
		_, ok := a.(bool)
		return ok
	case json.Number:
		_, ok := a.(json.Number)
		return ok
	case nil:
		return a == nil
	}
	return false
}
func jsonValue(d *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	t, err := d.Token()
	if err != nil {
		return false
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			s, ok := k.(string)
			if err != nil || !ok || seen[s] {
				return false
			}
			seen[s] = true
			if !jsonValue(d, depth+1) {
				return false
			}
		}
		t, err = d.Token()
		return err == nil && t == json.Delim('}')
	case json.Delim('['):
		for d.More() {
			if !jsonValue(d, depth+1) {
				return false
			}
		}
		t, err = d.Token()
		return err == nil && t == json.Delim(']')
	default:
		_, delim := t.(json.Delim)
		return !delim
	}
}

// JSON permits surrogate escape pairs but not lone surrogates in this contract.
func scalars(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] != '"' {
			continue
		}
		for i++; i < len(b) && b[i] != '"'; i++ {
			if b[i] != '\\' {
				continue
			}
			i++
			if i >= len(b) {
				return false
			}
			if b[i] != 'u' {
				continue
			}
			if i+4 >= len(b) {
				return false
			}
			n, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
			if err != nil || n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			i += 4
			if n >= 0xd800 && n <= 0xdbff {
				if i+6 >= len(b) || string(b[i+1:i+3]) != `\u` {
					return false
				}
				low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return true
}
