package recommend

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	Invocation struct{ At string }
	InputFiles map[string]map[string]any `json:"input_files"`
	Cases      []struct {
		Name     string
		Changes  map[string]any
		Remove   []string
		Expected json.RawMessage
	}
}

func fixtures(t *testing.T) fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func instant(t *testing.T) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, fixtures(t).Invocation.At)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func inputs(t *testing.T, edit func(map[string]any)) RawInputs {
	t.Helper()
	docs := map[string]any{}
	for name, object := range fixtures(t).InputFiles {
		docs[strings.TrimSuffix(name, ".json")] = object
	}
	if edit != nil {
		edit(docs)
	}
	encode := func(key string) []byte {
		b, err := json.Marshal(docs[key])
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return RawInputs{encode("task"), encode("member"), encode("state"), encode("policy")}
}

// setFixture applies the contract's published path notation; it does not
// compute expectations or call the engine's policy helpers.
func setFixture(t *testing.T, docs map[string]any, path string, value any, remove bool) {
	t.Helper()
	parts := strings.Split(path, ".")
	var node any = docs
	for i, part := range parts {
		object := node.(map[string]any)
		if i == len(parts)-1 {
			if remove {
				delete(object, part)
			} else {
				object[part] = value
			}
			return
		}
		if j := strings.IndexByte(part, '['); j >= 0 {
			values := object[part[:j]].([]any)
			selector := strings.TrimSuffix(part[j+1:], "]")
			if n, err := strconv.Atoi(selector); err == nil {
				node = values[n]
			} else {
				found := false
				for _, v := range values {
					if v.(map[string]any)["scope"] == selector {
						node, found = v, true
						break
					}
				}
				if !found {
					t.Fatalf("fixture path not found: %s", path)
				}
			}
		} else {
			node = object[part]
		}
	}
}

func changed(t *testing.T, changes map[string]any) RawInputs {
	t.Helper()
	return inputs(t, func(docs map[string]any) {
		for path, value := range changes {
			setFixture(t, docs, path, value, false)
		}
	})
}

func requireFailure(t *testing.T, r Result, status, code string) {
	t.Helper()
	if r.Status != status || r.Code != code {
		t.Fatalf("got %+v; want %s/%s", r, status, code)
	}
	if r.Action != "" || r.Selected != "" || r.Runtime != "" || r.Model != "" || r.Effort != "" || r.ContextReset != nil || r.ExecutionAuthority {
		t.Fatalf("non-recommendation leaked a target/authority: %+v", r)
	}
}

// Each published r2 case has its complete JSON result checked against the
// independently agreed expected object, including omitted non-result fields.
func TestAcceptedContract(t *testing.T) {
	for _, tc := range fixtures(t).Cases {
		t.Run(tc.Name, func(t *testing.T) {
			raw := inputs(t, func(docs map[string]any) {
				for path, value := range tc.Changes {
					setFixture(t, docs, path, value, false)
				}
				for _, path := range tc.Remove {
					setFixture(t, docs, path, nil, true)
				}
			})
			got := Recommend(raw, instant(t), nil)
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(tc.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("got %s\nwant %s", encoded, tc.Expected)
			}
		})
	}
}

// Closed decoding rejects ambiguous/unknown shapes, values and types before
// calling a classifier; the unchanged base is the paired positive control.
func TestStrictRawDecoding(t *testing.T) {
	base := inputs(t, nil)
	if r := Recommend(base, instant(t), nil); r.Status != "recommended" {
		t.Fatal(r)
	}
	cases := []struct {
		name string
		edit func(*RawInputs)
	}{
		{"duplicate-root", func(r *RawInputs) {
			r.Task = bytes.Replace(r.Task, []byte(`"class":"research"`), []byte(`"class":"code","class":"research"`), 1)
		}},
		{"duplicate-escaped-key", func(r *RawInputs) {
			r.Task = bytes.Replace(r.Task, []byte(`"class":"research"`), []byte(`"cl\u0061ss":"code","class":"research"`), 1)
		}},
		{"duplicate-nested", func(r *RawInputs) {
			r.Member = bytes.Replace(r.Member, []byte(`"effort":"high"`), []byte(`"effort":"low","effort":"high"`), 1)
		}},
		{"case-alias", func(r *RawInputs) { r.Task = bytes.Replace(r.Task, []byte(`"class"`), []byte(`"Class"`), 1) }},
		{"unknown-field", func(r *RawInputs) { r.State = append([]byte(`{"discover":true,`), r.State[1:]...) }},
		{"trailing-object", func(r *RawInputs) { r.Policy = append(r.Policy, []byte(` {}`)...) }},
		{"trailing-garbage", func(r *RawInputs) { r.Task = append(r.Task, 'x') }},
		{"root-array", func(r *RawInputs) { r.Task = []byte(`[]`) }},
		{"root-null", func(r *RawInputs) { r.Task = []byte(`null`) }},
		{"null-bool", func(r *RawInputs) {
			r.State = bytes.Replace(r.State, []byte(`"session_live":false`), []byte(`"session_live":null`), 1)
		}},
		{"null-int", func(r *RawInputs) {
			r.State = bytes.Replace(r.State, []byte(`"context_used_bps":1000`), []byte(`"context_used_bps":null`), 1)
		}},
		{"null-array", func(r *RawInputs) { *r = changed(t, map[string]any{"policy.catalog": nil}) }},
		{"fractional-bps", func(r *RawInputs) { *r = changed(t, map[string]any{"state.context_used_bps": 1000.5}) }},
		{"exponent-bps", func(r *RawInputs) {
			r.State = bytes.Replace(r.State, []byte(`"context_used_bps":1000`), []byte(`"context_used_bps":1e3`), 1)
		}},
		{"string-bps", func(r *RawInputs) { *r = changed(t, map[string]any{"state.context_used_bps": "1000"}) }},
		{"negative-bps", func(r *RawInputs) { *r = changed(t, map[string]any{"state.context_used_bps": -1}) }},
		{"over-bps", func(r *RawInputs) { *r = changed(t, map[string]any{"state.provider_quotas[pool-a].used_bps": 10001}) }},
		{"zero-cost", func(r *RawInputs) { *r = changed(t, map[string]any{"policy.catalog[0].relative_cost": 0}) }},
		{"zero-rank", func(r *RawInputs) { *r = changed(t, map[string]any{"policy.catalog[0].capability_rank": 0}) }},
		{"zero-estimate", func(r *RawInputs) { *r = changed(t, map[string]any{"task.estimate_minutes": 0}) }},
		{"unknown-version", func(r *RawInputs) { *r = changed(t, map[string]any{"task.contract": "router-recommend/2"}) }},
		{"unknown-class", func(r *RawInputs) { *r = changed(t, map[string]any{"task.class": "inferred"}) }},
		{"unknown-review", func(r *RawInputs) { *r = changed(t, map[string]any{"task.review_policy": "guess"}) }},
		{"invalid-utf8", func(r *RawInputs) { r.Task = append([]byte{0xff}, r.Task...) }},
		{"unpaired-surrogate", func(r *RawInputs) { r.Task = bytes.Replace(r.Task, []byte(`"EXAMPLE-001"`), []byte(`"\ud800"`), 1) }},
		{"oversize", func(r *RawInputs) { r.Task = append(bytes.Repeat([]byte(" "), maxInputBytes), r.Task...) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := base
			tc.edit(&raw)
			calls := 0
			r := Recommend(raw, instant(t), classifierFunc(func(c Classification) Result { calls++; return RuleTable{}.Classify(c) }))
			requireFailure(t, r, "refused", "invalid_input")
			if calls != 0 {
				t.Fatal("classifier called before raw validation")
			}
		})
	}
}

// Missing values are distinguished from false/zero; no inferred state or clock
// is substituted. Every required field's removal has a valid base control.
func TestMissingEvidence(t *testing.T) {
	docs := inputs(t, nil)
	for _, document := range []string{"task", "member", "state", "policy"} {
		var paths []string
		var walk func(string, any)
		walk = func(path string, value any) {
			if object, ok := value.(map[string]any); ok {
				for k, v := range object {
					paths = append(paths, path+"."+k)
					walk(path+"."+k, v)
				}
			}
		}
		var object map[string]any
		raw := map[string][]byte{"task": docs.Task, "member": docs.Member, "state": docs.State, "policy": docs.Policy}[document]
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		walk(document, object)
		for _, path := range paths {
			t.Run(path, func(t *testing.T) {
				raw := inputs(t, func(d map[string]any) { setFixture(t, d, path, nil, true) })
				r := Recommend(raw, instant(t), nil)
				requireFailure(t, r, "insufficient_evidence", "missing_input")
				if !contains(r.Missing, path) {
					t.Fatalf("missing path not named: %+v", r)
				}
			})
		}
	}
	for _, document := range []string{"task", "member", "state", "policy", "at"} {
		t.Run("absent-"+document, func(t *testing.T) {
			raw := docs
			at := instant(t)
			switch document {
			case "task":
				raw.Task = nil
			case "member":
				raw.Member = nil
			case "state":
				raw.State = nil
			case "policy":
				raw.Policy = nil
			case "at":
				at = time.Time{}
			}
			requireFailure(t, Recommend(raw, at, nil), "insufficient_evidence", "missing_input")
		})
	}
	valid := changed(t, map[string]any{"state.context_used_bps": 0, "state.session_live": false, "state.provider_quotas[pool-a].used_bps": 0})
	if r := Recommend(valid, instant(t), nil); r.Status != "recommended" {
		t.Fatal(r)
	}
}

// Evidence boundary controls cover equality and a one-nanosecond valid neighbor.
// Only relevant pools block on freshness; malformed dates are refused.
func TestEvidenceFreshness(t *testing.T) {
	at := instant(t)
	for _, path := range []string{"state.expires_at", "state.provider_quotas[pool-a].expires_at", "state.provider_quotas[pool-a].reset_at", "state.provider_quotas[pool-b].expires_at", "state.provider_quotas[pool-b].reset_at"} {
		t.Run(path, func(t *testing.T) {
			for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
				raw := changed(t, map[string]any{path: at.Add(delta).Format(time.RFC3339Nano)})
				r := Recommend(raw, at, nil)
				if delta > 0 {
					if r.Status != "recommended" {
						t.Fatal(r)
					}
				} else {
					requireFailure(t, r, "insufficient_evidence", "missing_input")
				}
			}
		})
	}
	for _, path := range []string{"state.observed_at", "state.provider_quotas[pool-a].observed_at", "state.provider_quotas[pool-b].observed_at"} {
		t.Run(path, func(t *testing.T) {
			future := changed(t, map[string]any{path: at.Add(time.Nanosecond).Format(time.RFC3339Nano)})
			requireFailure(t, Recommend(future, at, nil), "insufficient_evidence", "missing_input")
			now := changed(t, map[string]any{path: at.Format(time.RFC3339Nano)})
			if r := Recommend(now, at, nil); r.Status != "recommended" {
				t.Fatal(r)
			}
			bad := changed(t, map[string]any{path: "yesterday"})
			requireFailure(t, Recommend(bad, at, nil), "refused", "invalid_evidence")
		})
	}
	missingPool := changed(t, map[string]any{"state.provider_quotas": []any{}})
	requireFailure(t, Recommend(missingPool, at, nil), "insufficient_evidence", "missing_input")
	missingThreshold := changed(t, map[string]any{"policy.quota_thresholds": []any{}})
	requireFailure(t, Recommend(missingThreshold, at, nil), "insufficient_evidence", "missing_input")
}

type classifierFunc func(Classification) Result

func (f classifierFunc) Classify(c Classification) Result { return f(c) }

// The whole classifier result is checked, including every target field and
// error/authority field. A conforming fake proves the seam does not reject all.
func TestClassifierPostvalidation(t *testing.T) {
	base := inputs(t, nil)
	at := instant(t)
	valid := classifierFunc(func(c Classification) Result { return RuleTable{}.Classify(c) })
	if r := Recommend(base, at, valid); r.Status != "recommended" {
		t.Fatal(r)
	}
	cases := []struct {
		name string
		edit func(*Result)
	}{
		{"contract", func(r *Result) { r.Contract = "other/1" }},
		{"status", func(r *Result) { r.Status = "defer" }},
		{"task", func(r *Result) { r.TaskID = "OTHER" }},
		{"authority", func(r *Result) { r.ExecutionAuthority = true }},
		{"action", func(r *Result) { r.Action = "launch" }},
		{"selected", func(r *Result) { r.Selected = "large-high" }},
		{"runtime", func(r *Result) { r.Runtime = "other" }},
		{"model", func(r *Result) { r.Model = "model-large" }},
		{"effort", func(r *Result) { r.Effort = "max" }},
		{"reset", func(r *Result) { v := true; r.ContextReset = &v }},
		{"missing-reset", func(r *Result) { r.ContextReset = nil }},
		{"code", func(r *Result) { r.Code = "error" }},
		{"missing", func(r *Result) { r.Missing = []string{"state"} }},
		{"blank-rule", func(r *Result) { r.RuleID = "" }},
		{"blank-reason", func(r *Result) { r.Reason = "" }},
		{"multiline", func(r *Result) { r.Reason = "first\nsecond" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Recommend(base, at, classifierFunc(func(c Classification) Result { r := RuleTable{}.Classify(c); tc.edit(&r); return r }))
			requireFailure(t, r, "refused", "classifier_invalid")
		})
	}
	// A fake can choose another eligible tuple, but cannot claim a keep for it.
	r := Recommend(base, at, classifierFunc(func(c Classification) Result {
		return selected(c, c.Eligible[0], "custom-rule", "Use the explicit eligible alternative.")
	}))
	if r.Selected != "small-low" || r.Action != "switch" || r.ContextReset == nil || !*r.ContextReset {
		t.Fatal(r)
	}
}

// Admission/quota exclusions survive high context and hostile classifier
// snapshots. Hard refusals/defer do not invoke the classifier at all.
func TestHardGatesCannotBeOverridden(t *testing.T) {
	at := instant(t)
	for _, changes := range []map[string]any{
		{"member.current.model": "model-large", "state.session_live": true, "state.context_used_bps": 10000},
		{"state.provider_quotas[pool-a].used_bps": 8001, "state.session_live": true, "state.context_used_bps": 10000},
	} {
		raw := changed(t, changes)
		r := Recommend(raw, at, classifierFunc(func(c Classification) Result { return selected(c, c.Current, c.RuleID, c.Reason) }))
		requireFailure(t, r, "refused", "classifier_invalid")
		if r := Recommend(raw, at, nil); r.Status != "recommended" || r.Action != "switch" {
			t.Fatal(r)
		}
	}
	base := inputs(t, nil)
	r := Recommend(base, at, classifierFunc(func(c Classification) Result {
		c.Eligible[0].ID = "forged"
		c.Eligible[0].Model = "model-large"
		c.PreferredTupleIDs[0] = "forged"
		return selected(c, c.Eligible[0], "custom", "Attempt to mutate the admitted snapshot.")
	}))
	requireFailure(t, r, "refused", "classifier_invalid")
	for _, raw := range []RawInputs{
		changed(t, map[string]any{"member.current.runtime": "unknown"}),
		changed(t, map[string]any{"state.provider_quotas[pool-a].used_bps": 8001, "state.provider_quotas[pool-b].used_bps": 8001}),
		{Task: base.Task, Member: base.Member, Policy: base.Policy},
	} {
		calls := 0
		r := Recommend(raw, at, classifierFunc(func(c Classification) Result { calls++; return RuleTable{}.Classify(c) }))
		if calls != 0 || r.Status == "recommended" {
			t.Fatalf("hard gate invoked classifier: calls=%d result=%+v", calls, r)
		}
	}
	retained := changed(t, map[string]any{"state.session_live": true, "state.context_used_bps": 9000})
	r = Recommend(retained, at, classifierFunc(func(c Classification) Result { return selected(c, c.Eligible[0], "custom", "Discard high context.") }))
	requireFailure(t, r, "refused", "classifier_invalid")
}
