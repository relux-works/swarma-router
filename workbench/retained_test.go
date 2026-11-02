package workbench

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func retainedControl(t *testing.T) (Request, RetainedRequest, []byte) {
	t.Helper()
	r := mustParse(t, []byte(goldenPrepare+"\n"), local)
	stored, err := RetainRequest(r, "operation-a", "revision-9")
	if err != nil {
		t.Fatal(err)
	}
	b, err := stored.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return r, stored, b
}

// Exact envelope bytes and complete request revalidation preserve all original
// bindings. Returned values are copied; this does not prove disk durability.
func TestRetainedRoundTrip(t *testing.T) {
	r, original, b := retainedControl(t)
	want := `{"contract":"router-workbench-request-record/0.1.0","durable_revision":"revision-9","key":{"caller":"caller","correlation_id":"example","project":"project"},"operation_ref":"operation-a","prepare_request":` + goldenPrepare + `,"request_digest":"a43077d11f2854cb0289649dc710951577b4774401ca3d9028fde3b6fd5341ce"}` + "\n"
	if string(b) != want {
		t.Fatalf("unexpected retained envelope: %s", b)
	}
	restored, err := RestoreRetained(r.Key(), b)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Key() != r.Key() || restored.Digest() != r.Digest() || restored.OperationRef() != "operation-a" || restored.DurableRevision() != "revision-9" || !restored.AtRevision("revision-9") || restored.AtRevision("revision-10") || restored.AtRevision("") {
		t.Fatal("changed retained binding")
	}
	if err := restored.Match(r); err != nil {
		t.Fatal(err)
	}
	b[0] = 'x'
	restored.CanonicalPrepare()[0] = 'x'
	p, _ := restored.Prepare()
	p.Cards[0].Member = "changed"
	after, err := restored.Encode()
	if err != nil || string(after) != want {
		t.Fatal("mutable retained alias")
	}
	if !bytes.Equal(original.CanonicalPrepare(), restored.CanonicalPrepare()) {
		t.Fatal("request changed")
	}
}

// Canonical controls expand relative to valid short input escapes. A retained
// canonical request may exceed 64KiB while its original wire frame fits; the
// retained record has its own 256KiB limit and must not reject that valid form.
func TestRetainedCanonicalExpansion(t *testing.T) {
	m := base(t)
	configs := []any{}
	for i := 0; i < 16; i++ {
		c := cfg(base(t))
		c["placement"] = strings.Repeat("p", i+1)
		for _, kind := range []string{"model", "profile"} {
			for _, field := range []string{"source", "object", "revision"} {
				c[kind].(map[string]any)[field] = strings.Repeat("\n", 256)
			}
		}
		configs = append(configs, c)
	}
	m["constraints"].(map[string]any)["configurations"] = configs
	input := frame(t, m)
	if len(input) > MaxRequestBytes {
		t.Fatal("invalid witness setup")
	}
	r := mustParse(t, input, local)
	if len(r.CanonicalPrepare()) <= MaxRequestBytes {
		t.Fatal("control expansion not exercised")
	}
	stored, err := RetainRequest(r, "operation-a", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := stored.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxRecordBytes {
		t.Fatal("record bound exceeded")
	}
	restored, err := RestoreRetained(r.Key(), b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.CanonicalPrepare(), r.CanonicalPrepare()) {
		t.Fatal("expanded request lost")
	}
}

// Prepare and Status share the same original tuple/digest. Altered original
// task/model/objective bindings conflict; a wrong lookup tuple is invalid store
// data, never a replacement operation. No source or lifecycle ports exist here.
func TestRetainedMatch(t *testing.T) {
	r, stored, _ := retainedControl(t)
	status := map[string]any{"contract": Contract, "operation": "status", "caller": "caller", "project": "project", "correlation_id": "example", "expected_request_digest": r.Digest()}
	s := mustParse(t, frame(t, status), local)
	if err := stored.Match(s); err != nil {
		t.Fatal(err)
	}
	if _, err := RetainRequest(s, "operation-a", "revision-1"); err == nil || err.Error() != "store_invalid" {
		t.Fatal("Status initialized a binding")
	}
	for _, mutate := range []func(map[string]any){func(m map[string]any) { m["task"].(map[string]any)["revision"] = "r2" }, func(m map[string]any) { cfg(m)["model"].(map[string]any)["object"] = "other" }, func(m map[string]any) { m["constraints"].(map[string]any)["objective"] = "other" }} {
		m := base(t)
		mutate(m)
		changed := mustParse(t, frame(t, m), local)
		if err := stored.Match(changed); err == nil || err.Error() != "idempotency_conflict" {
			t.Fatal("changed request did not conflict")
		}
	}
	for _, key := range []string{"caller", "project", "correlation_id"} {
		m := base(t)
		m[key] = "other"
		changed := mustParse(t, frame(t, m), LocalContext{m["caller"].(string), m["project"].(string)})
		if err := stored.Match(changed); err == nil || err.Error() != "store_invalid" {
			t.Fatal("wrong tuple accepted")
		}
	}
	if err := stored.Match(Request{}); err == nil {
		t.Fatal("zero request accepted")
	}
}

// Corrupt, incomplete, forged-digest, key-mismatched or ambiguous retained data
// returns only store_invalid and no partial binding. A valid neighbor is always
// restored first; failure does not authorize journal reset or another operation.
func TestRetainedRejections(t *testing.T) {
	r, _, good := retainedControl(t)
	if _, err := RestoreRetained(r.Key(), good); err != nil {
		t.Fatal(err)
	}
	limitFrame := append(bytes.Repeat([]byte(" "), MaxRecordBytes-len(good)), good...)
	if _, err := RestoreRetained(r.Key(), limitFrame); err != nil {
		t.Fatal("exact record frame limit rejected:", err)
	}
	bad := [][]byte{nil, []byte("{}\n"), []byte("[]\n"), good[:len(good)-1], append(bytes.Clone(good), '\n'), []byte(strings.TrimSuffix(string(good), "\n") + "\r\n"), append([]byte{0xef, 0xbb, 0xbf}, good...), []byte(strings.Replace(string(good), `"operation_ref":"operation-a"`, `"operation_ref":"operation-a","\u006fperation_ref":"operation-a"`, 1)), []byte(strings.Replace(string(good), `"revision":"r"`, `"revision":"r","revision":"r"`, 1)), []byte(strings.Replace(string(good), `"operation-a"`, `"\ud800"`, 1)), []byte(strings.Replace(string(good), RetainedRequestContract, "queued-task-handoff/0.1.0", 1))}
	for _, field := range []string{"contract", "key", "request_digest", "prepare_request", "operation_ref", "durable_revision"} {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, field)
		bad = append(bad, frame(t, m))
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		m[field] = nil
		bad = append(bad, frame(t, m))
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["extra"] = true }, func(m map[string]any) { m["request_digest"] = strings.Repeat("2", 64) },
		func(m map[string]any) { m["key"].(map[string]any)["extra"] = "x" }, func(m map[string]any) { m["key"].(map[string]any)["correlation_id"] = "other" },
		func(m map[string]any) {
			m["prepare_request"].(map[string]any)["task"].(map[string]any)["revision"] = "other"
		},
		func(m map[string]any) { m["prepare_request"].(map[string]any)["operation"] = "status" },
		func(m map[string]any) { m["operation_ref"] = "" }, func(m map[string]any) { m["durable_revision"] = strings.Repeat("x", 257) },
	} {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		mutate(m)
		bad = append(bad, frame(t, m))
	}
	invalidUTF8 := bytes.Clone(good)
	invalidUTF8[20] = 0xff
	bad = append(bad, invalidUTF8)
	bad = append(bad, []byte(strings.Repeat(" ", MaxRecordBytes)+string(good)))
	for i, b := range bad {
		got, err := RestoreRetained(r.Key(), b)
		if err == nil || err.Error() != "store_invalid" {
			t.Fatalf("bad retained case%d: %v", i, err)
		}
		if got.Key() != (OperationKey{}) || got.Digest() != "" || got.OperationRef() != "" || got.DurableRevision() != "" || got.CanonicalPrepare() != nil {
			t.Fatal("partial restored binding")
		}
	}
	if _, err := RestoreRetained(OperationKey{}, good); err == nil {
		t.Fatal("missing lookup tuple accepted")
	}
	if _, err := (RetainedRequest{}).Encode(); err == nil {
		t.Fatal("zero retained value encoded")
	}
	if err := (RetainedRequest{}).Match(r); err == nil {
		t.Fatal("zero retained value matched")
	}
	for _, args := range [][2]string{{"", "revision"}, {"operation", ""}} {
		if _, err := RetainRequest(r, args[0], args[1]); err == nil {
			t.Fatal("invalid store references accepted")
		}
	}
}
