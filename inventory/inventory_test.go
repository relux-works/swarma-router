package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type sourceFunc func(context.Context, io.Writer) error

func (f sourceFunc) Feed(ctx context.Context, w io.Writer) error { return f(ctx, w) }

func binding() Binding {
	return Binding{SourceVersions: []string{"fixture-source-1"}, OwnerBindingRef: "fixture-receipt", Member: "fixture-member", Project: "fixture-project", Workbench: "fixture-workbench", Execution: "fixture-execution", RunnerInstance: "fixture-runner"}
}

func document() map[string]any {
	b, err := os.ReadFile("testdata/unknown-valid.json")
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	return m
}

// These frozen fixtures name all fields independently of the implementation's
// allowlist. The source-compatible form includes stored descriptor/partial unknown
// semantics confirmed by the source; neither fixture is actual-host evidence.
func TestIndependentContractFixtures(t *testing.T) {
	for _, name := range []string{"unknown-valid.json", "source-compatible.json"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		got := read(b, binding())
		if got.Class != "usable" || len(got.Projection.Facts) != 21 {
			t.Fatal(name, got)
		}
		if name == "source-compatible.json" {
			p := got.Projection
			if p.Facts["current_turn"].Known || p.Facts["current_turn"].Reason != "not-observed" || p.Facts["observed_release"].Value != "0.164.0" || p.Facts["observed_release"].SourceObservedAt != nil || p.Facts["effective_effort"].Known {
				t.Fatal("source declaration/unknown semantics changed", p)
			}
		}
	}
}

func known(v any, provenance string) map[string]any {
	return map[string]any{"known": true, "value": v, "provenance": provenance, "source_observed_at": nil, "source_expires_at": nil}
}

func frame(m map[string]any) []byte { b, _ := json.Marshal(m); return append(b, '\n') }

func read(b []byte, expected Binding) Result {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return Read(ctx, sourceFunc(func(_ context.Context, w io.Writer) error {
		_, err := w.Write(b)
		return err
	}), expected)
}

func refusal(t *testing.T, got Result, class, reason string) {
	t.Helper()
	if got.Class != class || got.Reason != reason || got.Projection != nil {
		t.Fatalf("expected %s/%s without data, got %+v", class, reason, got)
	}
}

// Positive controls cover every source scalar family and each current activity/
// fence label, while preserving configured/last-confirmed and unknown facts.
func TestSupportedSourceForms(t *testing.T) {
	m := document()
	f := m["facts"].(map[string]any)
	for name, rule := range facts {
		if rule.kind == "unknown" {
			continue
		}
		var value any = "fixture-value"
		switch rule.kind {
		case "uint64":
			value = "18446744073709551615"
		case "bool":
			value = false
		}
		f[name] = known(value, rule.provenance)
	}
	for _, label := range []string{"unknown", "idle", "busy", "draft", "approval", "security-stop"} {
		f["native_activity"] = known(label, "source_state")
		f["input_state"] = known(label, "source_state")
		for _, fence := range []string{"open", "held", "unknown"} {
			f["fence"] = known(fence, "source_state")
			got := read(frame(m), binding())
			if got.Class != "usable" || got.Projection == nil || got.Reason != "" {
				t.Fatalf("supported %s/%s: %+v", label, fence, got)
			}
			p := got.Projection
			if p.TrustMode != "local_single_user_unverified" || p.Coverage != "owned_execution_partial" || len(p.Facts) != 21 ||
				p.Facts["effective_effort"].Known || p.Facts["readiness"].Known || p.Facts["messaging_binding"].Known ||
				p.Facts["native_model"].Provenance != "thread_start_confirmed" || p.Facts["configured_model"].Provenance != "configured" ||
				p.Facts["native_model"].SourceObservedAt != nil || p.Facts["observed_release"].SourceObservedAt != nil {
				t.Fatal("source meanings promoted or facts lost", p)
			}
		}
	}
}

// An unused unknown does not reject valid narrower DATA; no tested result asserts
// readiness, absence, executable attestation, task acceptance or completion.
func TestUnknownAndTimestampForms(t *testing.T) {
	for _, reason := range []string{"not-exposed", "not-observed", "not-bound", "unsupported", "redacted"} {
		for _, times := range [][2]any{{nil, nil}, {"2026-11-02T12:00:00Z", nil}, {nil, "2026-11-02T12:01:00Z"}, {"2026-11-02T12:00:00.123456789Z", "2026-11-02T12:01:00Z"}} {
			m := document()
			f := m["facts"].(map[string]any)
			f["profile"] = map[string]any{"known": false, "reason": reason}
			k := known("model-1", "configured")
			k["source_observed_at"], k["source_expires_at"] = times[0], times[1]
			f["configured_model"] = k
			got := read(frame(m), binding())
			if got.Class != "usable" || got.Projection.Facts["profile"].Reason != reason {
				t.Fatal(reason, times, got)
			}
		}
	}
}

// Each exact closed envelope key is required and case-sensitive; each mapping
// substitution has its specific conflict and never returns any partial facts.
func TestEnvelopeAndBindingRefusals(t *testing.T) {
	if got := read(frame(document()), binding()); got.Class != "usable" {
		t.Fatal("positive control", got)
	}
	for _, key := range envelopeKeys {
		for _, variant := range []string{"missing", "alias", "wrong-type", "extra"} {
			t.Run(key+"/"+variant, func(t *testing.T) {
				m := document()
				switch variant {
				case "missing":
					delete(m, key)
				case "alias":
					m[strings.ToUpper(key)] = m[key]
					delete(m, key)
				case "wrong-type":
					m[key] = 1
				case "extra":
					m["extra_"+key] = "private-sentinel"
				}
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			})
		}
	}
	for _, key := range []string{"owner_binding_ref", "member", "project", "workbench", "execution", "runner_instance"} {
		m := document()
		m[key] = "substitute"
		reason := "owner-binding-mismatch"
		if key == "execution" || key == "runner_instance" {
			reason = "execution-mismatch"
		}
		refusal(t, read(frame(m), binding()), "conflict", reason)
	}
	for _, key := range []string{"contract", "source_version"} {
		m := document()
		m[key] = "unsupported-version"
		refusal(t, read(frame(m), binding()), "unavailable", "unsupported-contract")
	}
	for _, key := range []string{"trust_mode", "coverage"} {
		m := document()
		m[key] = "verified-or-fleet"
		refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
	}
}

// Every named fact has an adjacent valid form and rejects missing keys, malformed
// unions, wrong primitive/provenance and unknown reasons without a snapshot.
func TestFactAdmissionTable(t *testing.T) {
	for name, rule := range facts {
		t.Run(name, func(t *testing.T) {
			m := document()
			f := m["facts"].(map[string]any)
			if read(frame(m), binding()).Class != "usable" {
				t.Fatal("unknown positive control")
			}
			delete(f, name)
			refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			for _, bad := range []any{nil, []any{}, true, "raw-state", map[string]any{"known": "false", "reason": "not-exposed"}, map[string]any{"known": false, "reason": "new-reason"}, map[string]any{"known": false, "reason": "not-exposed", "value": "hidden"}, map[string]any{"Known": false, "reason": "not-exposed"}} {
				f[name] = bad
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			}
			var v any = "fixture-value"
			if rule.kind == "uint64" {
				v = "0"
			}
			if rule.kind == "bool" {
				v = true
			}
			k := known(v, rule.provenance)
			f[name] = k
			if rule.kind == "unknown" {
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
				f[name] = map[string]any{"known": false, "reason": "redacted"}
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
				return
			}
			// Native confirmation additionally needs the same observed native thread.
			if name == "native_model" || name == "native_vendor" {
				f["native_thread"] = known("fixture-thread", "source_state")
			}
			if got := read(frame(m), binding()); got.Class != "usable" {
				t.Fatal("known positive control", got)
			}
			for _, key := range []string{"known", "value", "provenance", "source_observed_at", "source_expires_at"} {
				c := known(v, rule.provenance)
				delete(c, key)
				f[name] = c
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			}
			for _, provenance := range []string{"made-up", "declared", "owner_receipt", "source_state", "configured", "thread_start_confirmed"} {
				if provenance == rule.provenance {
					continue
				}
				f[name] = known(v, provenance)
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			}
			for _, wrong := range []any{nil, map[string]any{"raw": "private-state"}, []any{"raw"}, json.Number("1")} {
				f[name] = known(wrong, rule.provenance)
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			}
			if rule.kind == "bool" {
				f[name] = known("false", rule.provenance)
				refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
			}
		})
	}
}

// Concurrent calls keep their validated DATA independent; the race run exercises
// these actual overlapping parser calls, not only sequential fixtures.
func TestConcurrentCalls(t *testing.T) {
	b := frame(document())
	errs := make(chan string, 16)
	for i := 0; i < 16; i++ {
		go func() {
			got := read(b, binding())
			if got.Class != "usable" || got.Projection.Facts["readiness"].Known {
				errs <- "invalid independent result"
				return
			}
			got.Projection.Facts["readiness"] = Fact{Known: true, Value: "local mutation"}
			errs <- ""
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-errs; err != "" {
			t.Fatal(err)
		}
	}
}

// Exact integer/string/time boundaries accept recorded legitimate forms and
// reject neighboring malformed claims; expiry alone is not refreshed by parsing.
func TestValueBoundaries(t *testing.T) {
	for _, good := range []string{"0", "1", "18446744073709551615"} {
		m := document()
		m["facts"].(map[string]any)["controller_epoch"] = known(good, "source_state")
		if read(frame(m), binding()).Class != "usable" {
			t.Fatal(good)
		}
	}
	for _, bad := range []any{"", "00", "+1", "-1", "1.0", "1e0", " 1", "18446744073709551616", 0, true} {
		m := document()
		m["facts"].(map[string]any)["controller_epoch"] = known(bad, "source_state")
		refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
	}
	for _, tc := range []struct {
		value  string
		usable bool
	}{{strings.Repeat("a", 512), true}, {strings.Repeat("é", 256), true}, {"literal�-😀", true}, {strings.Repeat("é", 257), false}, {"", false}, {"x\x00y", false}, {"x\u0085y", false}} {
		m := document()
		m["facts"].(map[string]any)["configured_model"] = known(tc.value, "configured")
		got := read(frame(m), binding())
		if tc.usable {
			if got.Class != "usable" || got.Projection.Facts["configured_model"].Value != tc.value {
				t.Fatal(tc, got)
			}
		} else {
			refusal(t, got, "unavailable", "frame-invalid")
		}
	}
	for _, bad := range []any{"2026-11-02T12:00:00+00:00", "2026-11-02t12:00:00Z", "2026-11-02T12:00:00.1234567890Z", "2026-02-30T12:00:00Z", "2026-11-02T12:00:00,1Z", 1, true} {
		m := document()
		f := known("fixture", "configured")
		f["source_observed_at"] = bad
		m["facts"].(map[string]any)["configured_model"] = f
		refusal(t, read(frame(m), binding()), "unavailable", "frame-invalid")
	}
	for _, times := range [][2]any{{"2026-11-02T13:00:01Z", nil}, {"2026-11-02T12:00:00Z", "2026-11-02T12:00:00Z"}, {"2026-11-02T12:00:01Z", "2026-11-02T12:00:00Z"}} {
		m := document()
		k := known("fixture", "configured")
		k["source_observed_at"], k["source_expires_at"] = times[0], times[1]
		m["facts"].(map[string]any)["configured_model"] = k
		refusal(t, read(frame(m), binding()), "conflict", "source-facts-conflict")
	}
}

// Duplicate and escaped-alias duplicates, invalid encodings and framing failures
// return a specific closed refusal, not a parsed subset or raw input diagnostic.
func TestFrameRefusals(t *testing.T) {
	b := frame(document())
	for name, bad := range map[string][]byte{
		"duplicate-envelope": bytes.Replace(b, []byte(`"member":`), []byte(`"member":"discarded","member":`), 1),
		"escaped-duplicate":  bytes.Replace(b, []byte(`"member":`), []byte(`"\u006dember":"discarded","member":`), 1),
		"duplicate-fact":     bytes.Replace(b, []byte(`"known":false`), []byte(`"known":true,"known":false`), 1),
		"invalid-utf8":       bytes.Replace(b, []byte("fixture-member"), []byte{'x', 0xff}, 1),
		"bom":                append([]byte{0xef, 0xbb, 0xbf}, b...),
		"no-lf":              b[:len(b)-1], "double-lf": append(append([]byte{}, b...), '\n'),
		"crlf":                append(append([]byte{}, b[:len(b)-1]...), '\r', '\n'),
		"trailing-space":      append(append([]byte{}, b[:len(b)-1]...), ' ', '\n'),
		"second-document":     append(append([]byte{}, b...), []byte("{}\n")...),
		"truncated":           append(append([]byte{}, b[:len(b)-5]...), '\n'),
		"lone-high-surrogate": bytes.Replace(b, []byte("fixture-member"), []byte(`\ud800`), 1),
		"lone-low-surrogate":  bytes.Replace(b, []byte("fixture-member"), []byte(`\udc00`), 1),
		"array":               []byte("[]\n"), "scalar": []byte("true\n"),
		"too-deep": []byte(strings.Repeat("{\"a\":", 9) + "0" + strings.Repeat("}", 9) + "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			got := read(bad, binding())
			refusal(t, got, "unavailable", "frame-invalid")
			if strings.Contains(fmt.Sprintf("%+v", got), "discarded") {
				t.Fatal("source bytes leaked")
			}
		})
	}
	// Paired UTF-16 escapes remain a supported Unicode scalar, not a blanket ban.
	m := document()
	m["facts"].(map[string]any)["configured_model"] = known("😀", "configured")
	escaped := bytes.Replace(frame(m), []byte("😀"), []byte(`\ud83d\ude00`), 1)
	if got := read(escaped, binding()); got.Class != "usable" || got.Projection.Facts["configured_model"].Value != "😀" {
		t.Fatal("valid paired escape refused", got)
	}
}

// The producer must complete normally before DATA is published. A full valid
// frame with nonzero/timeout/cancellation is not partially successful.
func TestProducerCompletionAndBounds(t *testing.T) {
	b := frame(document())
	for _, want := range []struct {
		err    error
		reason string
	}{{errors.New("private-source-stderr"), "source-unavailable"}, {context.DeadlineExceeded, "deadline-exceeded"}, {context.Canceled, "source-unavailable"}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		calls := 0
		got := Read(ctx, sourceFunc(func(_ context.Context, w io.Writer) error { calls++; _, _ = w.Write(b); return want.err }), binding())
		cancel()
		refusal(t, got, "unavailable", want.reason)
		if calls != 1 || strings.Contains(fmt.Sprintf("%+v", got), "private-source-stderr") {
			t.Fatal("retry or diagnostics leak")
		}
	}
	max := bytes.Replace(b, []byte("{"), []byte("{"+strings.Repeat(" ", MaxFrame-len(b))), 1)
	if len(max) != MaxFrame || read(max, binding()).Class != "usable" {
		t.Fatal("exact frame bound refused")
	}
	tooLarge := bytes.Replace(max, []byte("{"), []byte("{ "), 1)
	refusal(t, read(tooLarge, binding()), "unavailable", "frame-too-large")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	got := Read(ctx, sourceFunc(func(_ context.Context, w io.Writer) error {
		_, _ = w.Write(tooLarge) // Even a producer ignoring the sink error cannot publish.
		return nil
	}), binding())
	cancel()
	refusal(t, got, "unavailable", "frame-too-large")
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	got = Read(ctx, sourceFunc(func(_ context.Context, w io.Writer) error { _, _ = w.Write(b); cancel(); return nil }), binding())
	refusal(t, got, "unavailable", "source-unavailable")
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	got = Read(ctx, sourceFunc(func(_ context.Context, w io.Writer) error {
		cancel()
		if n, err := w.Write(b); n != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled sink wrote data", n, err)
		}
		return nil
	}), binding())
	refusal(t, got, "unavailable", "source-unavailable")
	noDeadlineCalls := 0
	refusal(t, Read(context.Background(), sourceFunc(func(context.Context, io.Writer) error { noDeadlineCalls++; return nil }), binding()), "unavailable", "source-unavailable")
	if noDeadlineCalls != 0 {
		t.Fatal("producer called without finite deadline")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	refusal(t, Read(ctx, sourceFunc(func(context.Context, io.Writer) error { t.Fatal("expired producer called"); return nil }), binding()), "unavailable", "deadline-exceeded")
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	got = Read(ctx, sourceFunc(func(ctx context.Context, _ io.Writer) error { <-ctx.Done(); return ctx.Err() }), binding())
	cancel()
	refusal(t, got, "unavailable", "deadline-exceeded")
}

// Configured/native conflicts cannot be hidden by source priority; a positive
// native confirmation needs this execution's actual observed thread reference.
func TestSourceFactConflicts(t *testing.T) {
	for _, field := range []string{"model", "vendor"} {
		m := document()
		f := m["facts"].(map[string]any)
		f["configured_"+field] = known("one", "configured")
		f["native_"+field] = known("two", "thread_start_confirmed")
		f["native_thread"] = known("thread", "source_state")
		refusal(t, read(frame(m), binding()), "conflict", "source-facts-conflict")
		f["native_"+field] = known("one", "thread_start_confirmed")
		if read(frame(m), binding()).Class != "usable" {
			t.Fatal("matched confirmation refused")
		}
		f["native_thread"] = map[string]any{"known": false, "reason": "not-observed"}
		refusal(t, read(frame(m), binding()), "conflict", "source-facts-conflict")
	}
}

// Invalid trusted configuration/cancelled admission never calls the source;
// independent successful calls return separate data with no shared mutable facts.
func TestConfigurationAndCallIsolation(t *testing.T) {
	calls := 0
	src := sourceFunc(func(_ context.Context, w io.Writer) error { calls++; _, err := w.Write(frame(document())); return err })
	for _, b := range []Binding{{}, {SourceVersions: []string{"fixture-source-1"}}, {OwnerBindingRef: "receipt\x00", SourceVersions: []string{"fixture-source-1"}}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		refusal(t, Read(ctx, src, b), "unavailable", "source-unavailable")
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	refusal(t, Read(ctx, src, binding()), "unavailable", "source-unavailable")
	if calls != 0 {
		t.Fatal("source called before admission", calls)
	}
	a, b := read(frame(document()), binding()), read(frame(document()), binding())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("identical data differs")
	}
	a.Projection.Facts["readiness"] = Fact{Known: true, Value: "caller-mutated"}
	if b.Projection.Facts["readiness"].Known {
		t.Fatal("shared facts across reads")
	}
}
