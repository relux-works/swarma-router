package recommend

import (
	"fmt"
	"reflect"
	"testing"
)

// Shipped explanations distinguish admission fallback from preference and do
// not attribute an empty admission set to quota when every pool is clear.
func TestShippedExplanationFidelity(t *testing.T) {
	cases := []struct {
		name, rule, reason, status, selected string
		changes                              map[string]any
	}{
		{"preferred-current", "R06", "Current configuration remains preferred and within policy.", "recommended", "standard-high", nil},
		{"fallback-current", "R06", "Current configuration remains admitted; no preferred configuration is eligible.", "recommended", "standard-high", map[string]any{"policy.rules[0].preferred_tuple_ids": []any{"large-high"}}},
		{"allowlist-correction", "R02", "Current configuration is not admitted for this role; select an admitted configuration.", "recommended", "small-low", map[string]any{"policy.roles[0].allowed_tuple_ids": []any{"small-low"}}},
		{"paid-correction", "R02", "Current configuration is not admitted for this role; select an admitted configuration.", "recommended", "small-low", map[string]any{"policy.catalog[1].billing": "paid"}},
		{"credits-correction", "R02", "Current configuration is not admitted for this role; select an admitted configuration.", "recommended", "small-low", map[string]any{"policy.catalog[1].billing": "credits"}},
		{"ceiling-correction", "R02", "Current model exceeds the role ceiling; select a lower admitted model.", "recommended", "standard-high", map[string]any{"member.current.model": "model-large"}},
		{"empty-admission-clear-quota", "R02", "No included configuration satisfies role admission and quota gates.", "defer", "", map[string]any{"policy.roles[0].allowed_tuple_ids": []any{"large-high"}}},
		{"paid-and-quota", "R02+R03", "Current configuration is not admitted for this role; select a cheaper admitted configuration with clear quota.", "recommended", "small-low", map[string]any{"policy.catalog[1].billing": "paid", "state.provider_quotas[pool-a].used_bps": 8001}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Recommend(changed(t, tc.changes), instant(t), nil)
			if r.RuleID != tc.rule || r.Reason != tc.reason || r.Status != tc.status || r.Selected != tc.selected {
				t.Fatalf("got %+v; want %s/%s/%s: %s", r, tc.status, tc.selected, tc.rule, tc.reason)
			}
			if tc.status == "defer" {
				requireFailure(t, r, "defer", "no_admitted_option")
			}
		})
	}
}

// Reserved rules cannot forge retention, quota/admission correction or default
// preference explanations. Conforming defaults and a custom eligible switch
// remain accepted through the public Recommend seam.
func TestClassifierExplanationPostvalidation(t *testing.T) {
	cases := []struct {
		name    string
		changes map[string]any
		forge   func(Classification) Result
	}{
		{"retention-switch-stopped", nil, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R04", "Preserve a live permitted configuration with high context use.")
		}},
		{"retention-switch-below-threshold", map[string]any{"state.session_live": true, "state.context_used_bps": 8999}, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R04", "Preserve a live permitted configuration with high context use.")
		}},
		{"retention-keep-without-retention", nil, func(c Classification) Result {
			return selected(c, c.Current, "R04", "Preserve a live permitted configuration with high context use.")
		}},
		{"quota-switch-clear", nil, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R03", "Current quota exceeds its threshold; select a cheaper admitted model.")
		}},
		{"quota-switch-at-threshold", map[string]any{"state.provider_quotas[pool-a].used_bps": 8000}, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R03", "Current quota exceeds its threshold; select a cheaper admitted model.")
		}},
		{"admission-switch-admitted", nil, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R02", "Current model exceeds the role ceiling; select a lower admitted model.")
		}},
		{"combined-without-forcing", nil, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R02+R03", "Select a lower admitted model with lower cost and clear quota.")
		}},
		{"refusal-rule-on-recommendation", nil, func(c Classification) Result {
			return selected(c, c.Current, "R00", "Input does not match the closed contract schema.")
		}},
		{"missing-rule-on-recommendation", nil, func(c Classification) Result {
			return selected(c, c.Current, "R01", "Required input evidence is missing.")
		}},
		{"nonpreferred-switch", nil, func(c Classification) Result {
			return selected(c, c.Eligible[0], "R05", "Select the first eligible preferred configuration.")
		}},
		{"false-preferred-keep", map[string]any{"policy.rules[0].preferred_tuple_ids": []any{"large-high"}}, func(c Classification) Result {
			return selected(c, c.Current, "R06", "Current configuration remains preferred and within policy.")
		}},
		{"false-retention-reason", map[string]any{"state.session_live": true, "state.context_used_bps": 9000}, func(c Classification) Result {
			return selected(c, c.Current, "R04", "Current quota exceeds its threshold.")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := changed(t, tc.changes)
			want := Recommend(raw, instant(t), nil)
			valid := Recommend(raw, instant(t), classifierFunc(func(c Classification) Result { return RuleTable{}.Classify(c) }))
			if want.Status != "recommended" || !reflect.DeepEqual(valid, want) {
				t.Fatalf("conforming control: got %+v want %+v", valid, want)
			}
			requireFailure(t, Recommend(raw, instant(t), classifierFunc(tc.forge)), "refused", "classifier_invalid")
		})
	}
	// These neighboring actual conditions legitimately produce the reserved rule.
	for _, changes := range []map[string]any{
		{"state.session_live": true, "state.context_used_bps": 9000},
		{"state.provider_quotas[pool-a].used_bps": 8001},
		{"member.current.model": "model-large"},
		{"member.current.model": "model-large", "state.provider_quotas[pool-a].used_bps": 8001},
		{"policy.rules[0].preferred_tuple_ids": []any{"small-low", "standard-high"}},
	} {
		raw := changed(t, changes)
		want := Recommend(raw, instant(t), nil)
		got := Recommend(raw, instant(t), classifierFunc(func(c Classification) Result { return RuleTable{}.Classify(c) }))
		if want.Status != "recommended" || !reflect.DeepEqual(got, want) {
			t.Fatalf("actual-condition control: got %+v want %+v", got, want)
		}
	}
	r := Recommend(inputs(t, nil), instant(t), classifierFunc(func(c Classification) Result {
		return selected(c, c.Eligible[0], "custom-rule", "Use the explicit eligible alternative.")
	}))
	if r.Status != "recommended" || r.RuleID != "custom-rule" || r.Selected != "small-low" || r.Action != "switch" || r.ContextReset == nil || !*r.ContextReset || r.ExecutionAuthority {
		t.Fatalf("custom positive control: %+v", r)
	}
}

// Quota correction requires strictly lower cost even when a clear alternative
// is admitted. Context retention and a custom classifier cannot bypass that gate;
// the existing all-exhausted and empty-admission explanations remain intact.
func TestQuotaDeferExplanationCostNeighbors(t *testing.T) {
	for _, cost := range []int{1, 2, 3} {
		for _, context := range []int{8999, 9000} {
			for _, used := range []int{1000, 8000, 8001} {
				t.Run(fmt.Sprintf("cost%d/context%d/alternative%d", cost, context, used), func(t *testing.T) {
					raw := changed(t, map[string]any{
						"state.provider_quotas[pool-a].used_bps": 8001,
						"state.provider_quotas[pool-b].used_bps": used,
						"policy.catalog[0].relative_cost":        cost,
						"state.session_live":                     true, "state.context_used_bps": context,
					})
					got := Recommend(raw, instant(t), nil)
					called := false
					custom := Recommend(raw, instant(t), classifierFunc(func(c Classification) Result {
						called = true
						return selected(c, c.Eligible[0], c.RuleID, c.Reason)
					}))
					if !reflect.DeepEqual(got, custom) {
						t.Fatalf("custom changed gated result: %+v versus %+v", got, custom)
					}
					if cost == 1 && used <= 8000 {
						if !called || got.Status != "recommended" || got.RuleID != "R03" || got.Selected != "small-low" || got.Action != "switch" || got.ContextReset == nil || !*got.ContextReset || got.ExecutionAuthority || got.ExitCode() != 0 {
							t.Fatalf("cheaper clear control: %+v, classifier called=%v", got, called)
						}
						return
					}
					requireFailure(t, got, "defer", "quota_exhausted")
					want := "No admitted included option is quota-clear."
					if cost >= 2 {
						want = "No strictly cheaper admitted included option is quota-clear."
					}
					if called || got.RuleID != "R03" || got.Reason != want || got.ExitCode() != 3 {
						t.Fatalf("got %+v, classifier called=%v; want reason %q", got, called, want)
					}
				})
			}
		}
	}
	// At exactly the current quota threshold, equal/higher cost does not force
	// defer; live context at the retention threshold still keeps the current tuple.
	for _, cost := range []int{1, 2, 3} {
		for _, context := range []int{8999, 9000} {
			raw := changed(t, map[string]any{"policy.catalog[0].relative_cost": cost,
				"state.provider_quotas[pool-a].used_bps": 8000,
				"state.session_live":                     true, "state.context_used_bps": context})
			got := Recommend(raw, instant(t), nil)
			wantRule := "R06"
			if context == 9000 {
				wantRule = "R04"
			}
			if got.Status != "recommended" || got.RuleID != wantRule || got.Selected != "standard-high" || got.Action != "keep" || got.ContextReset == nil || *got.ContextReset || got.ExecutionAuthority {
				t.Fatalf("threshold control cost%d/context%d: %+v", cost, context, got)
			}
		}
	}
	// Empty role admission remains distinct even with exhausted current quota.
	for _, used := range []int{3000, 8001} {
		got := Recommend(changed(t, map[string]any{"policy.roles[0].allowed_tuple_ids": []any{"large-high"}, "state.provider_quotas[pool-a].used_bps": used}), instant(t), nil)
		code, rule, reason := "no_admitted_option", "R02", "No included configuration satisfies role admission and quota gates."
		if used == 8001 {
			code, rule, reason = "quota_exhausted", "R03", "No admitted included option is quota-clear."
		}
		requireFailure(t, got, "defer", code)
		if got.RuleID != rule || got.Reason != reason {
			t.Fatalf("admission-empty control: %+v", got)
		}
	}
}

// Missing or stale evidence for an equal/higher-cost alternative remains
// irrelevant to quota correction; the explanation claims only the gated set.
// The cheaper neighbor still requires that evidence before recommending.
func TestQuotaDeferExplanationIrrelevantEvidence(t *testing.T) {
	for _, cost := range []int{1, 2, 3} {
		for _, stale := range []bool{false, true} {
			raw := inputs(t, func(d map[string]any) {
				p := d["policy"].(map[string]any)
				p["catalog"].([]any)[0].(map[string]any)["relative_cost"] = cost
				s := d["state"].(map[string]any)
				qs := s["provider_quotas"].([]any)
				qs[0].(map[string]any)["used_bps"] = 8001
				if stale {
					qs[1].(map[string]any)["expires_at"] = instant(t).Format("2006-01-02T15:04:05Z07:00")
				} else {
					s["provider_quotas"] = qs[:1]
				}
			})
			got := Recommend(raw, instant(t), nil)
			if cost == 1 {
				requireFailure(t, got, "insufficient_evidence", "missing_input")
				continue
			}
			requireFailure(t, got, "defer", "quota_exhausted")
			if got.Reason != "No strictly cheaper admitted included option is quota-clear." {
				t.Fatalf("cost%d/stale%v: %+v", cost, stale, got)
			}
		}
	}
}
