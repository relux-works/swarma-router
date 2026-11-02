// Package workbench implements the pure request boundary for the local
// workbench draft. Parsing establishes representation and local context equality,
// not source authenticity, execution authority or current readiness.
package workbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"
)

const Contract = "swarma-router/0.2.0-draft.2"
const MaxRequestBytes = 65536
const MaxRecoveryBytes = 4096

// LocalContext is supplied by the admitted embedding, never selected by stdin.
// Equality is only the initial local single-user binding, not authentication
// against a hostile process running as the same user.
type LocalContext struct{ Caller, Project string }

// OperationKey is a tuple, not a delimiter-concatenated string or a new claim.
type OperationKey struct {
	Caller        string `json:"caller"`
	Project       string `json:"project"`
	CorrelationID string `json:"correlation_id"`
}

type RevisionRef struct {
	Source   string `json:"source"`
	Object   string `json:"object"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

type CardRef struct {
	Member string      `json:"member"`
	Card   RevisionRef `json:"card"`
}

type Configuration struct {
	Member      string      `json:"member"`
	Runtime     string      `json:"runtime"`
	Vendor      string      `json:"vendor"`
	Model       RevisionRef `json:"model"`
	Effort      string      `json:"effort"`
	Profile     RevisionRef `json:"profile"`
	ProfileLock string      `json:"profile_lock"`
	Placement   string      `json:"placement"`
}

type Constraints struct {
	Policy         RevisionRef     `json:"policy"`
	Objective      string          `json:"objective"`
	Configurations []Configuration `json:"configurations"`
}

type PrepareRequest struct {
	Contract      string      `json:"contract"`
	Operation     string      `json:"operation"`
	CorrelationID string      `json:"correlation_id"`
	Caller        string      `json:"caller"`
	Project       string      `json:"project"`
	Task          RevisionRef `json:"task"`
	Cards         []CardRef   `json:"cards"`
	Constraints   Constraints `json:"constraints"`
}

// Request is immutable through its public methods. The zero value is invalid.
// Status retains the original expected digest; it never gets a new prepare hash.
type Request struct {
	key       OperationKey
	operation string
	digest    string
	canonical []byte
	prepare   *PrepareRequest
	recovery  []byte
}

func (r Request) Key() OperationKey { return r.key }
func (r Request) Operation() string { return r.operation }
func (r Request) Digest() string    { return r.digest }

// CanonicalPrepare returns a copy without LF, or nil for status/invalid values.
func (r Request) CanonicalPrepare() []byte { return bytes.Clone(r.canonical) }

// RecoveryTemplate returns a copied, bounded JSON+LF envelope with null optional
// references and the longest closed reason. This proves initial echo capacity;
// a later engine must supply the actual reason and fit each actual response.
func (r Request) RecoveryTemplate() []byte { return bytes.Clone(r.recovery) }

func (r Request) Prepare() (PrepareRequest, bool) {
	if r.prepare == nil {
		return PrepareRequest{}, false
	}
	p := *r.prepare
	p.Cards = append([]CardRef(nil), p.Cards...)
	p.Constraints.Configurations = append([]Configuration(nil), p.Constraints.Configurations...)
	return p, true
}

// Parse validates one complete frame, binds caller/project, normalizes semantic
// sets, hashes the complete prepare and checks recovery capacity. It has no IO,
// journal, source, selection or execution ports. Errors contain only closed codes.
func Parse(frame []byte, local LocalContext) (Request, error) {
	fail := func(code string) (Request, error) { return Request{}, errors.New(code) }
	if len(frame) < 3 || len(frame) > MaxRequestBytes || frame[len(frame)-1] != '\n' || frame[len(frame)-2] != '}' || !utf8.Valid(frame) || !scalarEscapes(frame) {
		return fail("invalid_request")
	}
	d := json.NewDecoder(bytes.NewReader(frame[:len(frame)-1]))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return fail("invalid_request")
	}
	if _, err := d.Token(); err != io.EOF {
		return fail("invalid_request")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fail("invalid_request")
	}
	version, ok := m["contract"].(string)
	if !ok {
		return fail("invalid_request")
	}
	if version != Contract {
		return fail("unsupported_version")
	}
	op, _ := m["operation"].(string)
	if !ref(m["correlation_id"]) || !ref(m["caller"]) || !ref(m["project"]) {
		return fail("invalid_request")
	}
	switch op {
	case "prepare_workbench":
		if !validPrepare(m) {
			return fail("invalid_request")
		}
	case "status":
		if !keys(m, "contract", "operation", "correlation_id", "caller", "project", "expected_request_digest") || !digest(m["expected_request_digest"]) {
			return fail("invalid_request")
		}
	default:
		return fail("invalid_request")
	}
	if !ref(local.Caller) || !ref(local.Project) {
		return fail("authority_unknown")
	}
	if m["caller"] != local.Caller {
		return fail("caller_mismatch")
	}
	if m["project"] != local.Project {
		return fail("project_mismatch")
	}
	r := Request{key: OperationKey{local.Caller, local.Project, m["correlation_id"].(string)}, operation: op}
	if op == "prepare_workbench" {
		r.canonical = canonical(m)
		h := sha256.Sum256(r.canonical)
		r.digest = hex.EncodeToString(h[:])
		r.prepare = new(PrepareRequest)
		if err := json.Unmarshal(r.canonical, r.prepare); err != nil {
			return fail("invalid_request")
		}
	} else {
		r.digest = m["expected_request_digest"].(string)
	}
	r.recovery = recoveryTemplate(r)
	if len(r.recovery) > MaxRecoveryBytes {
		return fail("output_budget")
	}
	return r, nil
}

func validPrepare(m map[string]any) bool {
	if !keys(m, "contract", "operation", "correlation_id", "caller", "project", "task", "cards", "constraints") || !revision(m["task"]) {
		return false
	}
	cards, ok := m["cards"].([]any)
	if !ok || len(cards) < 1 || len(cards) > 64 {
		return false
	}
	members := map[string]bool{}
	for _, v := range cards {
		c, ok := v.(map[string]any)
		if !ok || !keys(c, "member", "card") || !ref(c["member"]) || !revision(c["card"]) {
			return false
		}
		member := c["member"].(string)
		if members[member] {
			return false
		}
		members[member] = true
	}
	c, ok := m["constraints"].(map[string]any)
	if !ok || !keys(c, "policy", "objective", "configurations") || !revision(c["policy"]) || !ref(c["objective"]) {
		return false
	}
	configs, ok := c["configurations"].([]any)
	if !ok || len(configs) < 1 || len(configs) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range configs {
		cfg, ok := v.(map[string]any)
		if !ok || !keys(cfg, "member", "runtime", "vendor", "model", "effort", "profile", "profile_lock", "placement") {
			return false
		}
		for _, k := range []string{"member", "runtime", "vendor", "effort", "placement"} {
			if !ref(cfg[k]) {
				return false
			}
		}
		if !members[cfg["member"].(string)] || !revision(cfg["model"]) || !revision(cfg["profile"]) || !digest(cfg["profile_lock"]) {
			return false
		}
		b := string(canonical(cfg))
		if seen[b] {
			return false
		}
		seen[b] = true
	}
	// Normalize only after validating semantic uniqueness. Multiple different
	// configurations for one declared member are legitimate alternatives.
	sort.Slice(cards, func(i, j int) bool {
		return cards[i].(map[string]any)["member"].(string) < cards[j].(map[string]any)["member"].(string)
	})
	sort.Slice(configs, func(i, j int) bool { return bytes.Compare(canonical(configs[i]), canonical(configs[j])) < 0 })
	return true
}

func ref(v any) bool {
	s, ok := v.(string)
	return ok && len(s) > 0 && len(s) <= 256 && utf8.ValidString(s)
}

func digest(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) != 64 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func revision(v any) bool {
	m, ok := v.(map[string]any)
	return ok && keys(m, "source", "object", "revision", "digest") && ref(m["source"]) && ref(m["object"]) && ref(m["revision"]) && digest(m["digest"])
}

func keys(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := m[n]; !ok {
			return false
		}
	}
	return true
}

// Closed vocabulary shared by initial recovery sizing. A later response encoder
// must validate its actual union, reason and total budget separately.
var reasons = []string{"ok", "invalid_request", "unsupported_version", "unsupported_configuration", "caller_mismatch", "project_mismatch", "authority_denied", "authority_unknown", "task_conflict", "dependencies_not_ready", "card_conflict", "source_unavailable", "source_stale", "source_conflict", "evidence_insufficient", "no_eligible_candidate", "change_unavailable", "idempotency_conflict", "operation_not_found", "store_unavailable", "store_invalid", "reservation_unresolved", "preparation_unresolved", "observation_unresolved", "persistence_unresolved", "readiness_unknown", "readiness_expired", "output_budget"}

func recoveryTemplate(r Request) []byte {
	longest := ""
	for _, s := range reasons {
		if len(s) > len(longest) {
			longest = s
		}
	}
	key := map[string]any{"caller": r.key.Caller, "project": r.key.Project, "correlation_id": r.key.CorrelationID}
	result := map[string]any{"kind": "recovery", "operation_ref": nil, "last_durable_revision": nil, "status": "unknown", "reason": longest, "historical": false, "key": key, "request_digest": r.digest, "lower_launch": nil}
	return append(canonical(map[string]any{"contract": Contract, "operation": r.operation, "correlation_id": r.key.CorrelationID, "caller": r.key.Caller, "project": r.key.Project, "request_digest": r.digest, "result": result}), '\n')
}

// readValue retains exact decoded keys; encoding/json alone accepts duplicates.
// All numeric application values are forbidden in this initial schema.
func readValue(d *json.Decoder, depth int) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t == json.Delim('{') || t == json.Delim('[') {
		depth++
		if depth > 12 {
			return nil, errors.New("depth")
		}
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			s, ok := k.(string)
			if !ok {
				return nil, errors.New("key")
			}
			if _, exists := m[s]; exists {
				return nil, errors.New("duplicate")
			}
			v, err := readValue(d, depth)
			if err != nil {
				return nil, err
			}
			m[s] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("object")
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			if len(a) == 64 {
				return nil, errors.New("array")
			}
			v, err := readValue(d, depth)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("array")
		}
		return a, nil
	default:
		if _, number := t.(json.Number); number {
			return nil, errors.New("number")
		}
		return t, nil
	}
}

// Reject lone surrogate escapes before encoding/json can replace them with
// U+FFFD. Legitimate pairs, escaped backslashes and literal U+FFFD remain valid.
func scalarEscapes(b []byte) bool {
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

// canonical accepts only validated internal string/object/array/bool/null trees.
// Go string ordering is unsigned UTF-8 byte order. No Unicode normalization or
// HTML escaping is applied, and every control uses exactly six lowercase bytes.
func canonical(v any) []byte {
	var b bytes.Buffer
	var emit func(any)
	emit = func(v any) {
		switch x := v.(type) {
		case nil:
			b.WriteString("null")
		case bool:
			b.WriteString(strconv.FormatBool(x))
		case string:
			b.WriteByte('"')
			for _, r := range x {
				switch {
				case r == '"' || r == '\\':
					b.WriteByte('\\')
					b.WriteRune(r)
				case r < 0x20:
					const hex = "0123456789abcdef"
					b.WriteString(`\u00`)
					b.WriteByte(hex[r>>4])
					b.WriteByte(hex[r&15])
				default:
					b.WriteRune(r)
				}
			}
			b.WriteByte('"')
		case []any:
			b.WriteByte('[')
			for i, e := range x {
				if i > 0 {
					b.WriteByte(',')
				}
				emit(e)
			}
			b.WriteByte(']')
		case map[string]any:
			names := make([]string, 0, len(x))
			for k := range x {
				names = append(names, k)
			}
			sort.Strings(names)
			b.WriteByte('{')
			for i, k := range names {
				if i > 0 {
					b.WriteByte(',')
				}
				emit(k)
				b.WriteByte(':')
				emit(x[k])
			}
			b.WriteByte('}')
		default:
			panic("invalid internal canonical value")
		}
	}
	emit(v)
	return b.Bytes()
}
