package recommend

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// Unknown/ambiguous owner data refuses even when an unrelated observation is
// absent. Catalog order, runtime facts, costs and role admission are never guessed.
func TestInvalidPolicyAndUnknownConfiguration(t *testing.T) {
	cases := []struct {
		name, code string
		changes    map[string]any
	}{
		{"unknown-runtime", "unknown_configuration", map[string]any{"member.current.runtime": "unlisted"}},
		{"unknown-model", "unknown_configuration", map[string]any{"member.current.model": "unlisted"}},
		{"unknown-effort", "unknown_configuration", map[string]any{"member.current.effort": "unlisted"}},
		{"unknown-role", "unknown_role", map[string]any{"member.role": "unlisted"}},
		{"unknown-role-reference", "invalid_policy", map[string]any{"policy.rules[0].role": "unlisted"}},
		{"unknown-preference", "invalid_policy", map[string]any{"policy.rules[0].preferred_tuple_ids": []any{"unlisted"}}},
		{"unknown-admission", "invalid_policy", map[string]any{"policy.roles[0].allowed_tuple_ids": []any{"unlisted"}}},
		{"duplicate-preference", "invalid_policy", map[string]any{"policy.rules[0].preferred_tuple_ids": []any{"small-low", "small-low"}}},
		{"duplicate-admission", "invalid_policy", map[string]any{"policy.roles[0].allowed_tuple_ids": []any{"small-low", "small-low"}}},
		{"duplicate-id", "invalid_policy", map[string]any{"policy.catalog[0].id": "standard-high"}},
		{"ambiguous-configuration", "invalid_policy", map[string]any{"policy.catalog[0].model": "model-standard", "policy.catalog[0].effort": "high"}},
		{"unknown-threshold-scope", "invalid_policy", map[string]any{"policy.quota_thresholds[0].scope": "unlisted"}},
		{"duplicate-threshold-scope", "invalid_policy", map[string]any{"policy.quota_thresholds[0].scope": "pool-b"}},
		{"unknown-billing", "invalid_policy", map[string]any{"policy.catalog[0].billing": "automatic"}},
		{"inverted-estimate-range", "invalid_policy", map[string]any{"policy.rules[0].estimate_min_minutes": 121}},
		{"empty-catalog", "invalid_policy", map[string]any{"policy.catalog": []any{}}},
		{"empty-roles", "invalid_policy", map[string]any{"policy.roles": []any{}}},
		{"empty-allowlist", "invalid_policy", map[string]any{"policy.roles[0].allowed_tuple_ids": []any{}}},
	}
	if r := Recommend(inputs(t, nil), instant(t), nil); r.Status != "recommended" {
		t.Fatal(r)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, missingState := range []bool{false, true} {
				raw := changed(t, tc.changes)
				if missingState {
					raw.State = nil
				}
				requireFailure(t, Recommend(raw, instant(t), nil), "refused", tc.code)
			}
		})
	}
	for _, scope := range []string{"pool-a", "unknown"} {
		raw := inputs(t, func(d map[string]any) {
			s := d["state"].(map[string]any)
			qs := s["provider_quotas"].([]any)
			q := cloneObject(qs[0].(map[string]any))
			q["scope"] = scope
			s["provider_quotas"] = append(qs, q)
		})
		requireFailure(t, Recommend(raw, instant(t), nil), "refused", "invalid_evidence")
	}
}

// A switch that changes only effort still resets context; a stopped keep is
// configuration only. Unadmitted current state is corrected before retention.
func TestEffortOnlyAndAdmission(t *testing.T) {
	raw := inputs(t, func(d map[string]any) {
		p := d["policy"].(map[string]any)
		catalog := p["catalog"].([]any)
		low := cloneObject(catalog[1].(map[string]any))
		low["id"], low["effort"] = "standard-low", "low"
		p["catalog"] = append(catalog, low)
		p["roles"].([]any)[0].(map[string]any)["allowed_tuple_ids"] = []any{"standard-high", "standard-low"}
		p["rules"].([]any)[0].(map[string]any)["preferred_tuple_ids"] = []any{"standard-low", "standard-high"}
	})
	r := Recommend(raw, instant(t), nil)
	if r.Status != "recommended" || r.Action != "switch" || r.Model != "model-standard" || r.Effort != "low" || r.ContextReset == nil || !*r.ContextReset {
		t.Fatal(r)
	}
	stopped := Recommend(inputs(t, nil), instant(t), nil)
	if stopped.Action != "keep" || stopped.ExecutionAuthority || stopped.ContextReset == nil || *stopped.ContextReset {
		t.Fatal(stopped)
	}
	unadmitted := changed(t, map[string]any{
		"policy.roles[0].allowed_tuple_ids": []any{"small-low"},
		"state.session_live":                true, "state.context_used_bps": 10000,
	})
	r = Recommend(unadmitted, instant(t), nil)
	if r.Selected != "small-low" || r.Action != "switch" || r.RuleID != "R02" {
		t.Fatal(r)
	}
	noCeilingOption := changed(t, map[string]any{"policy.roles[0].allowed_tuple_ids": []any{"large-high"}})
	requireFailure(t, Recommend(noCeilingOption, instant(t), nil), "defer", "no_admitted_option")
}

// Quota correction never chooses equal/higher cost or paid/credits options.
// Clear included cheaper options are positive controls; equality remains clear.
func TestQuotaCostAndBilling(t *testing.T) {
	for _, cost := range []int{1, 2, 3} {
		raw := changed(t, map[string]any{"state.provider_quotas[pool-a].used_bps": 8001, "policy.catalog[0].relative_cost": cost})
		r := Recommend(raw, instant(t), nil)
		if cost == 1 {
			if r.Selected != "small-low" || r.Action != "switch" {
				t.Fatal(r)
			}
		} else {
			requireFailure(t, r, "defer", "quota_exhausted")
		}
	}
	for _, billing := range []string{"included", "paid", "credits"} {
		raw := changed(t, map[string]any{
			"state.provider_quotas[pool-a].used_bps": 8001,
			"policy.catalog[0].billing":              billing,
		})
		r := Recommend(raw, instant(t), nil)
		if billing == "included" {
			if r.Selected != "small-low" {
				t.Fatal(r)
			}
		} else {
			requireFailure(t, r, "defer", "quota_exhausted")
		}
		// The same non-included tuple is also excluded from ordinary preference.
		raw = changed(t, map[string]any{"policy.catalog[0].billing": billing, "policy.rules[0].preferred_tuple_ids": []any{"small-low", "standard-high"}})
		r = Recommend(raw, instant(t), nil)
		if billing != "included" && r.Selected != "standard-high" {
			t.Fatal(r)
		}
	}
	for _, currentBilling := range []string{"paid", "credits"} {
		raw := changed(t, map[string]any{"policy.catalog[1].billing": currentBilling})
		r := Recommend(raw, instant(t), nil)
		if r.Selected != "small-low" || r.Action != "switch" {
			t.Fatal(r)
		}
	}
}

// Missing/stale relevant pools prevent a decision; missing/stale scopes that
// cannot pass admission or the forced-cheaper gate do not create a new barrier.
func TestRelevantQuotaScopes(t *testing.T) {
	makeInput := func(rank int, cost int, observed bool) RawInputs {
		return inputs(t, func(d map[string]any) {
			p := d["policy"].(map[string]any)
			catalog := p["catalog"].([]any)
			other := cloneObject(catalog[2].(map[string]any))
			other["id"], other["model"], other["quota_scope"], other["capability_rank"], other["relative_cost"] = "other", "other", "pool-other", rank, cost
			p["catalog"] = append(catalog, other)
			p["roles"].([]any)[0].(map[string]any)["allowed_tuple_ids"] = []any{"small-low", "standard-high", "other"}
			p["quota_thresholds"] = append(p["quota_thresholds"].([]any), map[string]any{"scope": "pool-other", "used_bps": 8000})
			s := d["state"].(map[string]any)
			s["provider_quotas"].([]any)[0].(map[string]any)["used_bps"] = 8001
			if observed {
				q := cloneObject(s["provider_quotas"].([]any)[0].(map[string]any))
				q["scope"], q["used_bps"], q["expires_at"] = "pool-other", 0, instant(t).Format(time.RFC3339)
				s["provider_quotas"] = append(s["provider_quotas"].([]any), q)
			}
		})
	}
	for _, observed := range []bool{false, true} {
		for _, tc := range []struct {
			rank, cost int
			relevant   bool
		}{{3, 1, false}, {2, 3, false}, {2, 1, true}} {
			r := Recommend(makeInput(tc.rank, tc.cost, observed), instant(t), nil)
			if tc.relevant {
				requireFailure(t, r, "insufficient_evidence", "missing_input")
			} else if r.Selected != "small-low" {
				t.Fatal(r)
			}
		}
	}
}

// Explicit matching uses file order and inclusive estimate bounds. No match
// yields insufficient evidence instead of an inferred task policy or quality floor.
func TestExplicitRuleOrderAndBounds(t *testing.T) {
	for _, estimate := range []int{1, 120, 121} {
		r := Recommend(changed(t, map[string]any{"task.estimate_minutes": estimate}), instant(t), nil)
		if estimate <= 120 {
			if r.Status != "recommended" {
				t.Fatal(r)
			}
		} else {
			requireFailure(t, r, "insufficient_evidence", "missing_input")
		}
	}
	for _, changes := range []map[string]any{{"task.project": "other"}, {"task.class": "code"}, {"task.review_policy": "required"}, {"policy.rules": []any{}}} {
		requireFailure(t, Recommend(changed(t, changes), instant(t), nil), "insufficient_evidence", "missing_input")
	}
	raw := inputs(t, func(d map[string]any) {
		p := d["policy"].(map[string]any)
		rules := p["rules"].([]any)
		second := cloneObject(rules[0].(map[string]any))
		second["id"] = "second"
		second["preferred_tuple_ids"] = []any{"small-low"}
		p["rules"] = append(rules, second)
	})
	for i := 0; i < 20; i++ {
		if r := Recommend(raw, instant(t), nil); r.Selected != "standard-high" {
			t.Fatal(r)
		}
	}
	// A preference excluded by admission does not authorize that tuple.
	raw = changed(t, map[string]any{"policy.rules[0].preferred_tuple_ids": []any{"large-high"}})
	if r := Recommend(raw, instant(t), nil); r.Selected != "standard-high" {
		t.Fatal(r)
	}
}

// Returned reset storage and detached classifier inputs do not alias caller
// bytes or the storage kept by a classifier after its synchronous return.
func TestSnapshotAndResultIsolation(t *testing.T) {
	raw := inputs(t, nil)
	before := inputs(t, nil)
	var retained Result
	r := Recommend(raw, instant(t), classifierFunc(func(c Classification) Result { retained = RuleTable{}.Classify(c); return retained }))
	*retained.ContextReset = true
	if r.ContextReset == nil || *r.ContextReset {
		t.Fatal("classifier reset pointer aliases result")
	}
	if !reflect.DeepEqual(raw, before) {
		t.Fatal("raw input mutated")
	}
}

// Exit mapping is exhaustive for contract outcomes; unknown statuses fail closed.
func TestResultExits(t *testing.T) {
	for status, code := range map[string]int{"recommended": 0, "refused": 1, "insufficient_evidence": 2, "defer": 3, "unknown": 1} {
		if got := (Result{Status: status}).ExitCode(); got != code {
			t.Fatalf("%s got%d want%d", status, got, code)
		}
	}
}

func cloneObject(object map[string]any) map[string]any {
	b, err := json.Marshal(object)
	if err != nil {
		panic(err)
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		panic(err)
	}
	return result
}
