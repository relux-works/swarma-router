// Package inventory consumes the bounded, locally trusted owned-execution DATA
// projection. It does not authenticate a caller, observe readiness or launch work.
package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

const Contract = "router-host-inventory/0.1.0-draft.1"
const MaxFrame = 65536

// Source is an admitted owner-side producer, not a request-selected executable.
// Feed must honor ctx, propagate write failures, and return nil only after normal
// complete synchronous production (including a subprocess's successful Wait, if
// applicable). Feed must not retain the writer or write after returning.
// Production adapters are deliberately outside this DATA consumer.
type Source interface {
	Feed(context.Context, io.Writer) error
}

// Binding is trusted local owner configuration, never read from the frame.
type Binding struct {
	SourceVersions                              []string
	OwnerBindingRef, Member, Project, Workbench string
	Execution, RunnerInstance                   string
}

type Fact struct {
	Known                             bool
	Value                             any // Validated string or bool, never raw JSON.
	Provenance, Reason                string
	SourceObservedAt, SourceExpiresAt *time.Time
}

type Projection struct {
	SourceVersion, TrustMode, OwnerBindingRef string
	Member, Project, Workbench                string
	Execution, RunnerInstance, Coverage       string
	CollectedAt                               time.Time
	Facts                                     map[string]Fact
}

type Result struct {
	Class, Reason string
	Projection    *Projection // Always nil for unavailable/conflict.
}

func unavailable(reason string) Result { return Result{Class: "unavailable", Reason: reason} }

var errBound = errors.New("frame bound")

type boundedSink struct {
	ctx      context.Context
	data     []byte
	overflow bool
}

func (s *boundedSink) Write(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > MaxFrame-len(s.data) {
		s.overflow = true
		return 0, errBound
	}
	s.data = append(s.data, p...)
	return len(p), nil
}

// Read calls one trusted producer and publishes no data until it has completed
// normally, within an explicit deadline, and the entire frame has passed checks.
// There is no subprocess, file, private socket, retry or alternate source here.
func Read(ctx context.Context, source Source, expected Binding) Result {
	if ctx == nil || source == nil || !expected.valid() {
		return unavailable("source-unavailable")
	}
	if _, ok := ctx.Deadline(); !ok {
		return unavailable("source-unavailable")
	}
	if ctx.Err() != nil {
		return sourceFailure(ctx.Err())
	}
	sink := &boundedSink{ctx: ctx, data: make([]byte, 0, MaxFrame)}
	err := source.Feed(ctx, sink)
	if ctx.Err() != nil {
		return sourceFailure(ctx.Err())
	}
	if sink.overflow {
		return unavailable("frame-too-large")
	}
	if err != nil {
		return sourceFailure(err)
	}
	p, reason := parse(sink.data, expected)
	if reason != "" {
		class := "unavailable"
		if reason == "owner-binding-mismatch" || reason == "execution-mismatch" || reason == "source-facts-conflict" {
			class = "conflict"
		}
		return Result{Class: class, Reason: reason}
	}
	return Result{Class: "usable", Projection: p}
}

func sourceFailure(err error) Result {
	if errors.Is(err, context.DeadlineExceeded) {
		return unavailable("deadline-exceeded")
	}
	return unavailable("source-unavailable")
}

func (b Binding) valid() bool {
	for _, s := range []string{b.OwnerBindingRef, b.Member, b.Project, b.Workbench, b.Execution, b.RunnerInstance} {
		if !text(s, 512) {
			return false
		}
	}
	if len(b.SourceVersions) == 0 {
		return false
	}
	for _, s := range b.SourceVersions {
		if !text(s, 256) {
			return false
		}
	}
	return true
}

type spec struct{ kind, provenance string }

// The admission allowlist is the agreed contract's own field/type predicates.
var facts = map[string]spec{
	"controller_epoch": {"uint64", "source_state"}, "native_thread": {"string", "source_state"},
	"selected_adapter": {"string", "configured"}, "observed_release": {"string", "source_state"},
	"configured_vendor": {"string", "configured"}, "configured_model": {"string", "configured"},
	"native_vendor": {"string", "thread_start_confirmed"}, "native_model": {"string", "thread_start_confirmed"},
	"requested_effort": {"string", "configured"}, "effective_effort": {"unknown", ""},
	"profile": {"string", "owner_receipt"}, "profile_lock": {"string", "owner_receipt"}, "placement": {"string", "owner_receipt"},
	"native_activity": {"string", "source_state"}, "current_turn": {"string", "source_state"}, "input_state": {"string", "source_state"},
	"security_stopped": {"bool", "source_state"}, "stop_requested": {"bool", "source_state"}, "fence": {"string", "source_state"},
	"messaging_binding": {"unknown", ""}, "readiness": {"unknown", ""},
}

var envelopeKeys = []string{"contract", "source_version", "trust_mode", "owner_binding_ref", "member", "project", "workbench", "execution", "runner_instance", "collected_at", "coverage", "facts"}
var timestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)
var decimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)

func parse(frame []byte, expected Binding) (*Projection, string) {
	if len(frame) < 3 || len(frame) > MaxFrame || frame[len(frame)-1] != '\n' || frame[len(frame)-2] != '}' || !utf8.Valid(frame) || !scalarEscapes(frame) {
		return nil, "frame-invalid"
	}
	d := json.NewDecoder(bytes.NewReader(frame[:len(frame)-1]))
	d.UseNumber()
	v, err := value(d, 0)
	if err != nil {
		return nil, "frame-invalid"
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, "frame-invalid"
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, "frame-invalid"
	}
	contract, ok := m["contract"].(string)
	if !ok {
		return nil, "frame-invalid"
	}
	if contract != Contract {
		return nil, "unsupported-contract"
	}
	if !keys(m, envelopeKeys...) {
		return nil, "frame-invalid"
	}
	for _, key := range envelopeKeys[:11] {
		limit := 512
		if key == "source_version" {
			limit = 256
		}
		s, ok := m[key].(string)
		if !ok || !text(s, limit) {
			return nil, "frame-invalid"
		}
	}
	if m["trust_mode"] != "local_single_user_unverified" || m["coverage"] != "owned_execution_partial" {
		return nil, "frame-invalid"
	}
	collected, ok := utc(m["collected_at"])
	if !ok || collected == nil {
		return nil, "frame-invalid"
	}
	supported := false
	for _, version := range expected.SourceVersions {
		supported = supported || version == m["source_version"]
	}
	if !supported {
		return nil, "unsupported-contract"
	}
	fs, ok := m["facts"].(map[string]any)
	if !ok || len(fs) != len(facts) {
		return nil, "frame-invalid"
	}
	out := make(map[string]Fact, len(facts))
	for name, rule := range facts {
		f, valid, conflict := fact(fs[name], rule, *collected)
		if !valid {
			return nil, "frame-invalid"
		}
		if conflict {
			return nil, "source-facts-conflict"
		}
		out[name] = f
	}
	for _, pair := range [][2]string{{"configured_vendor", "native_vendor"}, {"configured_model", "native_model"}} {
		a, b := out[pair[0]], out[pair[1]]
		if a.Known && b.Known && a.Value != b.Value {
			return nil, "source-facts-conflict"
		}
	}
	if (out["native_vendor"].Known || out["native_model"].Known) && !out["native_thread"].Known {
		return nil, "source-facts-conflict"
	}
	if m["owner_binding_ref"] != expected.OwnerBindingRef || m["member"] != expected.Member || m["project"] != expected.Project || m["workbench"] != expected.Workbench {
		return nil, "owner-binding-mismatch"
	}
	if m["execution"] != expected.Execution || m["runner_instance"] != expected.RunnerInstance {
		return nil, "execution-mismatch"
	}
	return &Projection{SourceVersion: m["source_version"].(string), TrustMode: m["trust_mode"].(string), OwnerBindingRef: expected.OwnerBindingRef,
		Member: expected.Member, Project: expected.Project, Workbench: expected.Workbench, Execution: expected.Execution, RunnerInstance: expected.RunnerInstance,
		CollectedAt: *collected, Coverage: "owned_execution_partial", Facts: out}, ""
}

func fact(v any, rule spec, collected time.Time) (Fact, bool, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return Fact{}, false, false
	}
	known, ok := m["known"].(bool)
	if !ok {
		return Fact{}, false, false
	}
	if !known {
		if !keys(m, "known", "reason") {
			return Fact{}, false, false
		}
		reason, ok := m["reason"].(string)
		valid := ok && (reason == "not-exposed" || reason == "not-observed" || reason == "not-bound" || reason == "unsupported" || reason == "redacted")
		if rule.kind == "unknown" && reason != "not-exposed" {
			valid = false
		}
		return Fact{Reason: reason}, valid, false
	}
	if rule.kind == "unknown" || !keys(m, "known", "value", "provenance", "source_observed_at", "source_expires_at") || m["provenance"] != rule.provenance {
		return Fact{}, false, false
	}
	switch rule.kind {
	case "bool":
		if _, ok := m["value"].(bool); !ok {
			return Fact{}, false, false
		}
	case "string", "uint64":
		s, ok := m["value"].(string)
		if !ok || !text(s, 512) {
			return Fact{}, false, false
		}
		if rule.kind == "uint64" {
			if !decimal.MatchString(s) {
				return Fact{}, false, false
			}
			if _, err := strconv.ParseUint(s, 10, 64); err != nil {
				return Fact{}, false, false
			}
		}
	}
	observed, a := utc(m["source_observed_at"])
	expires, b := utc(m["source_expires_at"])
	if !a || !b {
		return Fact{}, false, false
	}
	conflict := observed != nil && (observed.After(collected) || expires != nil && !expires.After(*observed))
	return Fact{Known: true, Value: m["value"], Provenance: rule.provenance, SourceObservedAt: observed, SourceExpiresAt: expires}, true, conflict
}

func keys(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := m[name]; !ok {
			return false
		}
	}
	return true
}

func text(s string, limit int) bool {
	if len(s) == 0 || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func utc(v any) (*time.Time, bool) {
	if v == nil {
		return nil, true
	}
	s, ok := v.(string)
	if !ok || !timestamp.MatchString(s) {
		return nil, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return &t, err == nil
}

// Tokens preserve exact decoded keys, including escaped-key duplicates. The
// scanner has its own small bound before any schema map is accepted.
func value(d *json.Decoder, depth int) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t == json.Delim('{') || t == json.Delim('[') {
		depth++
		if depth > 8 {
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
			v, err := value(d, depth)
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
		for d.More() {
			if _, err := value(d, depth); err != nil {
				return nil, err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("array")
		}
		return []any{}, nil
	default:
		return t, nil
	}
}

// encoding/json replaces lone UTF-16 surrogate escapes. Reject that lossy form
// while preserving legitimate paired escapes and literal U+FFFD/Unicode values.
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
