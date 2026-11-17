package recommend

import (
	"reflect"
	"sort"
	"strings"
	"time"
)

// Recommend decodes original JSON, checks owner policy and dated evidence, and
// returns an inert result. A nil classifier uses RuleTable. The caller supplies
// an explicit evaluation instant; this function never reads the system clock.
func Recommend(raw RawInputs, at time.Time, classifier Classifier) Result {
	var t task
	var m member
	var s state
	var p policy
	documents := []struct {
		name string
		raw  []byte
		out  any
	}{
		{"task", raw.Task, &t}, {"member", raw.Member, &m},
		{"state", raw.State, &s}, {"policy", raw.Policy, &p},
	}
	var missing []string
	complete := make(map[string]bool)
	for _, doc := range documents {
		absent, err := decode(doc.raw, doc.out, doc.name)
		if err != nil {
			return failure("refused", "R00", "invalid_input", "Input does not match the closed contract schema.", "")
		}
		missing = append(missing, absent...)
		complete[doc.name] = len(absent) == 0
	}
	// Known invalid policy/configuration still refuses when unrelated state is
	// missing. Missing evidence must not hide a known unknown configuration.
	if complete["policy"] && !validPolicy(p) {
		return failure("refused", "R00", "invalid_policy", "Policy contains invalid, duplicate or unknown references.", t.ElementID)
	}
	var current Tuple
	var owner role
	if complete["policy"] && complete["member"] {
		var found bool
		for _, tuple := range p.Catalog {
			if sameConfiguration(tuple, m.Current) {
				current, found = tuple, true
			}
		}
		if !found {
			return failure("refused", "R00", "unknown_configuration", "Current configuration is not in the explicit catalog.", t.ElementID)
		}
		found = false
		for _, r := range p.Roles {
			if r.Role == m.Role {
				owner, found = r, true
			}
		}
		if !found {
			return failure("refused", "R00", "unknown_role", "Member role is not in the explicit policy.", t.ElementID)
		}
	}
	if at.IsZero() {
		missing = append(missing, "at")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		r := failure("insufficient_evidence", "R01", "missing_input", "Required input evidence is missing.", t.ElementID)
		if complete["task"] && complete["member"] && complete["policy"] && !complete["state"] && !at.IsZero() {
			r.Reason = "Required worker-state evidence is missing."
		}
		r.Missing = missing
		return r
	}
	return evaluate(t, m, s, p, owner, current, at, classifier)
}

func validPolicy(p policy) bool {
	if len(p.Catalog) == 0 || len(p.Roles) == 0 {
		return false
	}
	ids := map[string]bool{}
	configs := map[configuration]bool{}
	scopes := map[string]bool{}
	for _, c := range p.Catalog {
		key := configuration{c.Runtime, c.Model, c.Effort}
		if ids[c.ID] || configs[key] || (c.Billing != "included" && c.Billing != "paid" && c.Billing != "credits") {
			return false
		}
		ids[c.ID], configs[key], scopes[c.QuotaScope] = true, true, true
	}
	roles := map[string]bool{}
	for _, r := range p.Roles {
		if roles[r.Role] || len(r.AllowedTupleIDs) == 0 || !knownUnique(r.AllowedTupleIDs, ids) {
			return false
		}
		roles[r.Role] = true
	}
	thresholds := map[string]bool{}
	for _, q := range p.QuotaThresholds {
		if thresholds[q.Scope] || !scopes[q.Scope] {
			return false
		}
		thresholds[q.Scope] = true
	}
	rules := map[string]bool{}
	for _, r := range p.Rules {
		if rules[r.ID] || !roles[r.Role] || r.EstimateMaxMinutes < r.EstimateMinMinutes || len(r.PreferredTupleIDs) == 0 || !knownUnique(r.PreferredTupleIDs, ids) {
			return false
		}
		rules[r.ID] = true
	}
	return true
}

func knownUnique(values []string, known map[string]bool) bool {
	seen := map[string]bool{}
	for _, id := range values {
		if !known[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func evaluate(t task, m member, s state, p policy, owner role, current Tuple, at time.Time, classifier Classifier) Result {
	thresholds := map[string]int{}
	for _, threshold := range p.QuotaThresholds {
		thresholds[threshold.Scope] = threshold.UsedBPS
	}
	quotas := map[string]quota{}
	knownScopes := map[string]bool{}
	for _, c := range p.Catalog {
		knownScopes[c.QuotaScope] = true
	}
	for _, q := range s.ProviderQuotas {
		if _, exists := quotas[q.Scope]; exists || !knownScopes[q.Scope] || !validDates(q.ObservedAt, q.ExpiresAt, q.ResetAt) {
			return failure("refused", "R00", "invalid_evidence", "Quota evidence contains invalid dates, duplicate or unknown scopes.", t.ElementID)
		}
		quotas[q.Scope] = q
	}
	if !validDates(s.ObservedAt, s.ExpiresAt, "") {
		return failure("refused", "R00", "invalid_evidence", "Worker-state evidence contains invalid dates.", t.ElementID)
	}
	if !fresh(s.ObservedAt, s.ExpiresAt, "", at) {
		return evidence(t.ElementID, "state", "Worker-state evidence is stale or future-dated.")
	}
	admitted := func(c Tuple) bool {
		return c.Billing == "included" && c.CapabilityRank <= owner.CeilingRank && contains(owner.AllowedTupleIDs, c.ID)
	}
	checkScope := func(scope string) *Result {
		if _, ok := thresholds[scope]; !ok {
			r := evidence(t.ElementID, "policy.quota_thresholds", "A relevant quota threshold is missing.")
			return &r
		}
		q, ok := quotas[scope]
		if !ok {
			r := evidence(t.ElementID, "state.provider_quotas", "Relevant quota evidence is missing.")
			return &r
		}
		if !fresh(q.ObservedAt, q.ExpiresAt, q.ResetAt, at) {
			r := evidence(t.ElementID, "state.provider_quotas", "Relevant quota evidence is stale or future-dated.")
			return &r
		}
		return nil
	}
	if problem := checkScope(current.QuotaScope); problem != nil {
		return *problem
	}
	quotaExceeded := quotas[current.QuotaScope].UsedBPS > thresholds[current.QuotaScope]
	// Validate every scope that could change the decision, not just the first
	// preference. When quota forces a cheaper choice, higher-cost pools are
	// irrelevant; their missing/stale observations cannot block this task.
	relevant := []string{current.QuotaScope}
	for _, tuple := range p.Catalog {
		if admitted(tuple) && (!quotaExceeded || tuple.RelativeCost < current.RelativeCost) && !contains(relevant, tuple.QuotaScope) {
			if problem := checkScope(tuple.QuotaScope); problem != nil {
				return *problem
			}
			relevant = append(relevant, tuple.QuotaScope)
		}
	}
	var preferences []string
	for _, r := range p.Rules {
		if r.Project == t.Project && r.Role == m.Role && r.Class == t.Class && r.ReviewPolicy == t.ReviewPolicy && t.EstimateMinutes >= r.EstimateMinMinutes && t.EstimateMinutes <= r.EstimateMaxMinutes {
			preferences = append([]string(nil), r.PreferredTupleIDs...)
			break
		}
	}
	if len(preferences) == 0 {
		return evidence(t.ElementID, "policy.rules", "No explicit policy rule covers this task and member.")
	}
	var eligible []Tuple
	for _, c := range p.Catalog {
		if admitted(c) && quotas[c.QuotaScope].UsedBPS <= thresholds[c.QuotaScope] && (!quotaExceeded || c.RelativeCost < current.RelativeCost) {
			eligible = append(eligible, c)
		}
	}
	c := Classification{
		TaskID: t.ElementID, Current: current, Eligible: eligible, PreferredTupleIDs: preferences,
		SessionLive: s.SessionLive, ContextUsedBPS: s.ContextUsedBPS, ContextRetentionBPS: p.ContextRetentionBPS,
	}
	switch {
	case !admitted(current) && quotaExceeded:
		c.RuleID, c.Reason = "R02+R03", "Select a lower admitted model with lower cost and clear quota."
		if current.CapabilityRank <= owner.CeilingRank {
			c.Reason = "Current configuration is not admitted for this role; select a cheaper admitted configuration with clear quota."
		}
	case !admitted(current):
		c.RuleID, c.Reason = "R02", "Current configuration is not admitted for this role; select an admitted configuration."
		if current.CapabilityRank > owner.CeilingRank {
			c.Reason = "Current model exceeds the role ceiling; select a lower admitted model."
		}
	case quotaExceeded:
		c.RuleID, c.Reason = "R03", "Current quota exceeds its threshold; select a cheaper admitted model."
	}
	if len(eligible) == 0 {
		if quotaExceeded {
			reason := "No admitted included option is quota-clear."
			// Equal/higher-cost alternatives were excluded without requiring
			// their quota evidence. Describe the strictly cheaper set in that
			// case, rather than claiming all admitted alternatives lack quota.
			for _, tuple := range p.Catalog {
				if admitted(tuple) && tuple.ID != current.ID && tuple.RelativeCost >= current.RelativeCost {
					reason = "No strictly cheaper admitted included option is quota-clear."
					break
				}
			}
			return failure("defer", "R03", "quota_exhausted", reason, t.ElementID)
		}
		return failure("defer", "R02", "no_admitted_option", "No included configuration satisfies role admission and quota gates.", t.ElementID)
	}
	if classifier == nil {
		classifier = RuleTable{}
	}
	// The classifier cannot mutate the snapshot used by postvalidation.
	detached := c
	detached.Eligible = append([]Tuple(nil), c.Eligible...)
	detached.PreferredTupleIDs = append([]string(nil), c.PreferredTupleIDs...)
	result := classifier.Classify(detached)
	if !validClassification(c, result) {
		return failure("refused", "R00", "classifier_invalid", "Classifier result violates the validated recommendation contract.", t.ElementID)
	}
	// Also detach the output pointer/slice from an implementation's storage.
	reset := *result.ContextReset
	result.ContextReset = &reset
	return result
}

func validDates(observed, expires, reset string) bool {
	_, e1 := time.Parse(time.RFC3339Nano, observed)
	_, e2 := time.Parse(time.RFC3339Nano, expires)
	if e1 != nil || e2 != nil {
		return false
	}
	if reset != "" {
		_, err := time.Parse(time.RFC3339Nano, reset)
		return err == nil
	}
	return true
}

func fresh(observed, expires, reset string, at time.Time) bool {
	o, _ := time.Parse(time.RFC3339Nano, observed)
	e, _ := time.Parse(time.RFC3339Nano, expires)
	if o.After(at) || !e.After(at) {
		return false
	}
	if reset != "" {
		r, _ := time.Parse(time.RFC3339Nano, reset)
		return r.After(at)
	}
	return true
}

// Classify preserves live high-context state only after hard gates, otherwise
// uses the first eligible explicit preference, then current, then catalog order.
func (RuleTable) Classify(c Classification) Result {
	if c.RuleID == "" && retain(c) {
		return selected(c, c.Current, "R04", "Preserve a live permitted configuration with high context use.")
	}
	for _, id := range c.PreferredTupleIDs {
		for _, tuple := range c.Eligible {
			if tuple.ID == id {
				return tableResult(c, tuple)
			}
		}
	}
	for _, tuple := range c.Eligible {
		if tuple.ID == c.Current.ID {
			return tableResult(c, tuple)
		}
	}
	if len(c.Eligible) > 0 {
		return tableResult(c, c.Eligible[0])
	}
	return failure("refused", "R00", "classifier_invalid", "Classifier has no eligible options.", c.TaskID)
}

func tableResult(c Classification, tuple Tuple) Result {
	if c.RuleID != "" {
		return selected(c, tuple, c.RuleID, c.Reason)
	}
	if tuple.ID == c.Current.ID {
		if !contains(c.PreferredTupleIDs, tuple.ID) {
			return selected(c, tuple, "R06", "Current configuration remains admitted; no preferred configuration is eligible.")
		}
		return selected(c, tuple, "R06", "Current configuration remains preferred and within policy.")
	}
	return selected(c, tuple, "R05", "Select the first eligible preferred configuration.")
}

func selected(c Classification, tuple Tuple, ruleID, reason string) Result {
	reset := tuple.ID != c.Current.ID
	action := "keep"
	if reset {
		action = "switch"
	}
	return Result{
		Contract: Contract, Status: "recommended", TaskID: c.TaskID,
		RuleID: ruleID, Reason: reason, Action: action, Selected: tuple.ID,
		Runtime: tuple.Runtime, Model: tuple.Model, Effort: tuple.Effort, ContextReset: &reset,
	}
}

func retain(c Classification) bool {
	if !c.SessionLive || c.ContextUsedBPS < c.ContextRetentionBPS {
		return false
	}
	for _, tuple := range c.Eligible {
		if tuple == c.Current {
			return true
		}
	}
	return false
}

func validClassification(c Classification, r Result) bool {
	if r.Contract != Contract || r.Status != "recommended" || r.TaskID != c.TaskID || r.ExecutionAuthority || r.Code != "" || len(r.Missing) != 0 || r.ContextReset == nil {
		return false
	}
	if r.RuleID == "" || len(r.RuleID) > 256 || r.Reason == "" || len(r.Reason) > 512 || strings.TrimSpace(r.Reason) != r.Reason || strings.ContainsAny(r.RuleID+r.Reason, "\r\n\x00") {
		return false
	}
	if c.RuleID != "" && (r.RuleID != c.RuleID || r.Reason != c.Reason) {
		return false
	}
	if c.RuleID == "" && retain(c) && (r.Selected != c.Current.ID || r.RuleID != "R04") {
		return false
	}
	if c.RuleID == "" {
		switch r.RuleID {
		case "R00", "R01", "R02", "R03", "R02+R03":
			// Failure and forced-correction rules cannot describe an ordinary choice.
			return false
		case "R04", "R05", "R06":
			// Reserved default explanations assert the default decision, including
			// the converse retention condition and the exact reason. Custom rules
			// may choose another eligible tuple when retention does not apply.
			if !reflect.DeepEqual(r, (RuleTable{}).Classify(c)) {
				return false
			}
		}
	}
	for _, tuple := range c.Eligible {
		if r.Selected == tuple.ID {
			expected := selected(c, tuple, r.RuleID, r.Reason)
			// Full equality includes action, target, reset and non-result fields.
			return reflect.DeepEqual(r, expected)
		}
	}
	return false
}

func sameConfiguration(c Tuple, current configuration) bool {
	return c.Runtime == current.Runtime && c.Model == current.Model && c.Effort == current.Effort
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func failure(status, ruleID, code, reason, taskID string) Result {
	return Result{Contract: Contract, Status: status, TaskID: taskID, RuleID: ruleID, Code: code, Reason: reason}
}

func evidence(taskID, path, reason string) Result {
	r := failure("insufficient_evidence", "R01", "missing_input", reason, taskID)
	r.Missing = []string{path}
	return r
}
