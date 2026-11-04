package localworkbench

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"
)

// Recommend acquires one bounded snapshot and returns data only. The trusted
// reader owns source verification/currentness; this planner never creates it.
func (p LocalPlanner) Recommend(ctx context.Context, r LocalRequest) (LocalRecommendation, error) {
	return p.recommend(ctx, r, nil)
}

// Revalidate refreshes only the retained target. A source cannot broaden this
// read or substitute a newly eligible target. It still performs no effect.
func (p LocalPlanner) Revalidate(ctx context.Context, r LocalRequest, s LocalSelection) (LocalRecommendation, error) {
	return p.recommend(ctx, r, &s)
}

// Bytes encodes the inert result with canonical UTC-millisecond times. It grants
// no source trust or effect authority; callers must not treat bytes as a receipt.
func (r LocalRecommendation) Bytes() ([]byte, error) {
	if r.Contract != Contract || r.ExecutionAuthority || !goStrings(reflect.ValueOf(r), 0) || !instant(r.AsOf) || !hashString(r.RequestDigest) || !contains([]string{"reuse", "start", "defer", "insufficient_evidence", "refuse"}, r.Action) {
		return nil, failure("invalid_recommendation")
	}
	positive := r.Action == "reuse" || r.Action == "start"
	if positive != (r.Selected != nil && r.PreparedWorkbench != nil) || !positive && (r.Selected != nil || r.PreparedWorkbench != nil) {
		return nil, failure("invalid_recommendation")
	}
	b := encode(r)
	if len(b) == 0 || len(b) > MaxSnapshotBytes {
		return nil, failure("output_budget")
	}
	return b, nil
}
func bounded(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	_, ok := ctx.Deadline()
	return ok
}
func (p LocalPlanner) recommend(ctx context.Context, r LocalRequest, only *LocalSelection) (LocalRecommendation, error) {
	bad := func(code string) (LocalRecommendation, error) { return LocalRecommendation{}, failure(code) }
	if !bounded(ctx) || p.Reader == nil || p.Clock == nil || !mxid(p.Local.Caller) || !ref(p.Local.Project) || !ref(p.Room) {
		return bad("source_unavailable")
	}
	if r.Caller != p.Local.Caller || r.Project != p.Local.Project {
		return bad("authority_denied")
	}
	r, err := normalize(r)
	if err != nil {
		return LocalRecommendation{}, err
	}
	rd := digest(encode(r))
	beforeRead := p.Clock()
	if !instant(beforeRead) {
		return bad("source_unavailable")
	}
	if only != nil {
		if only.RequestDigest != rd || !goStrings(reflect.ValueOf(only), 0) || !validBinding(r, only.Target, beforeRead) {
			return bad("source_conflict")
		}
		s := clone(*only)
		only = &s
	}
	// Give the reader independent copies; keep even a buggy mutation from changing
	// the request or retained selection used for validation below.
	readOnly := clone(only)
	in, err := p.Reader.Read(ctx, clone(r), readOnly)
	if !bounded(ctx) {
		return bad("source_unavailable")
	}
	if err != nil {
		return bad("source_unavailable")
	}
	if len(in.Targets) > 16 {
		return bad("source_conflict")
	}
	for _, target := range in.Targets {
		if len(target.Checks) != 16 {
			return bad("source_conflict")
		}
	}
	if !goStrings(reflect.ValueOf(in), 0) {
		return bad("source_conflict")
	}
	now := p.Clock()
	if !instant(now) {
		return bad("source_unavailable")
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > MaxSnapshotBytes {
		return bad("output_budget")
	}
	in = clone(in)
	if in.Contract != Contract || in.RequestDigest != rd || !contains([]string{"partial", "complete"}, in.Scope) || len(in.Targets) > 16 {
		return bad("source_conflict")
	}
	out := LocalRecommendation{Contract: Contract, RequestDigest: rd, AsOf: now, Scope: in.Scope, Action: "refuse", Reason: "no_eligible_candidate", Evidence: []GateRecord{}}
	type option struct {
		action  string
		binding Binding
		room    string
	}
	var options []option
	incomplete := len(in.Targets) == 0
	live := false
	seen := map[string]bool{}
	seats := map[string]Binding{}
	identitySeats := map[string]string{}
	for _, target := range in.Targets {
		b := target.Binding
		td := TargetDigest(b)
		if !validBinding(r, b, now) || seen[td] {
			return bad("source_conflict")
		}
		seen[td] = true
		if prior, ok := seats[b.Member]; ok {
			if prior.MXID != b.MXID || prior.Seat != b.Seat || prior.CardSHA256 != b.CardSHA256 || prior.State != "stopped" || b.State != "stopped" {
				return bad("source_conflict")
			}
		}
		seats[b.Member] = b
		if prior, ok := identitySeats[b.MXID]; ok && prior != b.Member {
			return bad("source_conflict")
		}
		identitySeats[b.MXID] = b.Member
		if only != nil && (len(in.Targets) != 1 || td != TargetDigest(only.Target)) {
			return bad("source_conflict")
		}
		action := ""
		if b.State == "ready" {
			action = "reuse"
		}
		if b.State == "stopped" {
			action = "start"
		}
		checks := map[string]bool{}
		denied, missing := false, false
		room := ""
		sort.Slice(target.Checks, func(i, j int) bool { return target.Checks[i].Gate < target.Checks[j].Gate })
		for _, record := range target.Checks {
			g := record.Gate
			if !contains(append(append(append([]string{}, common...), reuse...), start...), g) || checks[g] || record.RequestDigest != rd || record.TargetDigest != td {
				return bad("source_conflict")
			}
			checks[g] = true
			relevant := contains(gates(action), g)
			if !relevant {
				if !shape(record.Evidence, "kind", "reason") || record.Evidence.Kind != "unknown" || record.Evidence.Reason != "not_required" {
					return bad("source_conflict")
				}
			} else {
				status, _, err := check(record.Evidence, g, r, b, rd, p.Room, now)
				if err != nil {
					return bad("source_conflict")
				}
				if status == "unknown" {
					missing = true
				}
				if status == "deny" {
					denied = true
				}
				if g == "messaging_admission" && status == "positive" {
					room = record.Evidence.Projection.Membership.Room
				}
			}
			out.Evidence = append(out.Evidence, record)
		}
		if denied {
			continue
		}
		if missing || b.State == "unknown" || b.State == "conflict" {
			incomplete = true
			continue
		}
		if b.State == "running" || b.State == "starting" {
			live = true
			continue
		}
		options = append(options, option{action, b, room})
	}
	sort.Slice(out.Evidence, func(i, j int) bool {
		a, b := out.Evidence[i], out.Evidence[j]
		if a.TargetDigest != b.TargetDigest {
			return a.TargetDigest < b.TargetDigest
		}
		return a.Gate < b.Gate
	})
	if len(options) == 0 {
		if incomplete {
			out.Action, out.Reason = "insufficient_evidence", "required_evidence_unavailable"
		} else if live {
			out.Action, out.Reason = "defer", "change_unavailable"
		}
	} else {
		sort.Slice(options, func(i, j int) bool {
			a, b := options[i], options[j]
			if a.action != b.action {
				return a.action == "reuse"
			}
			return string(encode(a.binding)) < string(encode(b.binding))
		})
		chosen := options[0]
		out.Action, out.Reason, out.Selected = chosen.action, "eligible", &chosen.binding
		// The digest covers the recommendation with no workbench; the draft then
		// refers to it without a circular hash or an execution/receipt claim.
		recommendationDigest := digest(encode(out))
		out.PreparedWorkbench = &PreparedWorkbench{r.Caller, r.Project, r.TaskID, r.CorrelationID, chosen.binding.Member, chosen.binding.MXID, chosen.binding.Configuration, recommendationDigest, DeliveryDraft{chosen.room, chosen.binding.MXID, r.TaskID + " request:" + rd + " target:" + TargetDigest(chosen.binding) + "\n" + r.Objective}}
	}
	if !bounded(ctx) {
		return bad("source_unavailable")
	}
	if len(encode(out)) > MaxSnapshotBytes {
		return bad("output_budget")
	}
	return out, nil
}
func validBinding(r LocalRequest, b Binding, now time.Time) bool {
	if !ref(b.Member) || !mxid(b.MXID) || !ref(b.Seat) || !hashString(b.CardSHA256) || !config(b.Configuration) || b.Configuration.Member != b.Member || b.Configuration.MXID != b.MXID {
		return false
	}
	cardFound, configurationFound := false, false
	for _, c := range r.Cards {
		if c.Member == b.Member && c.MXID == b.MXID && c.SHA256 == b.CardSHA256 {
			cardFound = true
		}
	}
	for _, c := range r.Configurations {
		if c == b.Configuration {
			configurationFound = true
		}
	}
	if !cardFound || !configurationFound {
		return false
	}
	switch b.State {
	case "stopped":
		return b.Execution == nil
	case "ready", "running", "starting":
		return execution(b.Execution, now)
	case "unknown", "conflict":
		return b.Execution == nil || execution(b.Execution, now)
	}
	return false
}
func shape(v any, keys ...string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(encode(v), &m) != nil || len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
func check(e Evidence, g string, r LocalRequest, b Binding, rd, room string, now time.Time) (string, string, error) {
	bad := func() (string, string, error) { return "", "", failure("source_conflict") }
	switch e.Kind {
	case "unknown":
		if !shape(e, "kind", "reason") || !contains([]string{"missing", "unavailable", "partial", "stale", "expired", "future", "binding_mismatch", "source_time_unknown", "predicate_unproven"}, e.Reason) {
			return bad()
		}
		return "unknown", e.Reason, nil
	case "caller-asserted":
		if !assertionGate(g) || !shape(e, "kind", "asserted_by", "statement", "recorded_at") || e.AssertedBy != r.Caller || !text(e.Statement, 4096, true) || e.RecordedAt == nil || !instant(*e.RecordedAt) || e.RecordedAt.After(now) {
			return bad()
		}
		return "positive", "", nil
	case "observed":
		if !shape(e, "kind", "source", "projection") || e.Source == nil || e.Projection == nil {
			return bad()
		}
		state, reason, err := source(*e.Source, g, now)
		if err != nil {
			return bad()
		}
		ok, err := predicate(*e.Projection, g, r, b, rd, room, now)
		if err != nil {
			return bad()
		}
		if state != "positive" {
			return state, reason, nil
		}
		if !ok {
			return bad()
		}
		return "positive", "", nil
	case "deny":
		if !shape(e, "kind", "reason", "basis") || e.Basis == nil || !contains([]string{"explicit_denial", "predicate_false", "source_conflict", "unsupported_configuration"}, e.Reason) {
			return bad()
		}
		basis := *e.Basis
		if basis.Kind == "caller-asserted" {
			state, _, err := check(basis, g, r, b, rd, room, now)
			if err != nil || state != "positive" || e.Reason != "explicit_denial" {
				return bad()
			}
			return "deny", "", nil
		}
		if basis.Kind != "observed" || !shape(basis, "kind", "source", "projection") || basis.Source == nil || basis.Projection == nil {
			return bad()
		}
		state, reason, err := source(*basis.Source, g, now)
		if err != nil {
			return bad()
		}
		ok, err := predicate(*basis.Projection, g, r, b, rd, room, now)
		if err != nil {
			return bad()
		}
		if state != "positive" {
			return state, reason, nil
		}
		if e.Reason == "predicate_false" && ok {
			return bad()
		}
		return "deny", "", nil
	}
	return bad()
}

func source(s Source, g string, now time.Time) (string, string, error) {
	allowed := map[string][]string{
		"caller_authority": {"a2a"}, "task_constraints": {"task_board", "a2a"}, "manager_custody": {"a2a"}, "board_assignment": {"task_board", "a2a"}, "preparation_authority": {"a2a"},
		"card_member": {"pulse_self", "pulse_discovery"}, "profile_runtime": {"curator_catalog"}, "quality": {"curator_catalog"}, "provider_limits": {"host", "curator_catalog", "a2a"}, "messaging_admission": {"a2a"}, "current_configuration": {"pulse_environment"}, "exact_generation": {"a2a", "host"}, "ready_response": {"a2a"}, "occupancy": {"host", "a2a"}, "complete_owned_absence": {"a2a", "host"}, "launch_capability": {"curator_catalog"},
	}
	if !contains(allowed[g], s.Kind) || !ref(s.Locator) || !hashString(s.ContentSHA256) || !instant(s.ReadAt) {
		return "", "", failure("source_conflict")
	}
	if s.ObservedAt != nil && !instant(*s.ObservedAt) {
		return "", "", failure("source_conflict")
	}
	if s.ExpiresAt != nil && (!instant(*s.ExpiresAt) || s.ObservedAt != nil && !s.ObservedAt.Before(*s.ExpiresAt)) {
		return "", "", failure("source_conflict")
	}
	if s.ReadAt.After(now) || s.ObservedAt != nil && s.ObservedAt.After(s.ReadAt) {
		return "unknown", "future", nil
	}
	if s.ExpiresAt != nil && !now.Before(*s.ExpiresAt) {
		return "unknown", "expired", nil
	}
	// A source's admitted acquisition/currentness contract must also be checked
	// by the reader. No default TTL or collection-derived expiry is invented.
	if contains([]string{"provider_limits", "messaging_admission", "current_configuration", "exact_generation", "ready_response", "occupancy", "complete_owned_absence"}, g) && s.ObservedAt == nil {
		return "unknown", "source_time_unknown", nil
	}
	return "positive", "", nil
}
func predicate(p Projection, g string, r LocalRequest, b Binding, rd, room string, now time.Time) (bool, error) {
	bad := func() (bool, error) { return false, failure("source_conflict") }
	if p.Subject != (Subject{b.Member, b.MXID, b.CardSHA256}) {
		return bad()
	}
	configuration := func() bool { return p.Configuration != nil && *p.Configuration == b.Configuration }
	exec := func() bool {
		return execution(p.Execution, now) && string(encode(p.Execution)) == string(encode(b.Execution))
	}
	if assertionGate(g) {
		if !shape(p, "subject", "configuration", "task_id", "statement") || !configuration() || p.TaskID == nil || *p.TaskID != r.TaskID || p.Statement == nil || !text(*p.Statement, 4096, true) {
			return bad()
		}
		return true, nil
	}
	switch g {
	case "card_member":
		if !shape(p, "subject", "declaration") || p.Declaration == nil || !ref(p.Declaration.Role) || p.Declaration.Project != nil && !ref(*p.Declaration.Project) {
			return bad()
		}
		return p.Declaration.Project == nil || *p.Declaration.Project == r.Project, nil
	case "profile_runtime":
		if !shape(p, "subject", "configuration") || !configuration() {
			return bad()
		}
		return true, nil
	case "quality":
		if !shape(p, "subject", "configuration", "qualification") || !configuration() || p.Qualification == nil || !ref(p.Qualification.Record) || !hashString(p.Qualification.SHA256) {
			return bad()
		}
		return p.Qualification.Qualified, nil
	case "provider_limits":
		if !shape(p, "subject", "configuration", "quota") || !configuration() || p.Quota == nil || p.Quota.Needed == 0 || !ref(p.Quota.Unit) || !ref(p.Quota.Scope) {
			return bad()
		}
		return p.Quota.Complete && p.Quota.Available >= p.Quota.Needed, nil
	case "messaging_admission":
		if !shape(p, "subject", "membership") || p.Membership == nil || p.Membership.MXID != b.MXID || p.Membership.Room != room {
			return bad()
		}
		return p.Membership.Joined && p.Membership.Complete, nil
	case "current_configuration":
		if !shape(p, "subject", "configuration", "live") || !configuration() || p.Live == nil || p.Live.Configuration != b.Configuration || string(encode(&p.Live.Execution)) != string(encode(b.Execution)) {
			return bad()
		}
		state, _, err := source(p.Live.Source, "exact_generation", now)
		if err != nil {
			return bad()
		}
		return state == "positive", nil
	case "exact_generation":
		if !shape(p, "subject", "execution") || !exec() {
			return bad()
		}
		return true, nil
	case "ready_response":
		if !shape(p, "subject", "execution", "response") || !exec() || p.Response == nil || p.Response.Sender != b.MXID || p.Response.To != r.Caller || p.Response.TaskID != r.TaskID || p.Response.RequestDigest != rd {
			return bad()
		}
		return p.Response.Accepted, nil
	case "occupancy":
		if !shape(p, "subject", "execution", "available") || !exec() || p.Available == nil {
			return bad()
		}
		return *p.Available, nil
	case "complete_owned_absence":
		if !shape(p, "subject", "absence") || p.Absence == nil || p.Absence.Seat != b.Seat || p.Absence.MXID != b.MXID || !ref(p.Absence.Scope) {
			return bad()
		}
		return p.Absence.Absent && p.Absence.Complete, nil
	case "launch_capability":
		if !shape(p, "subject", "capability") || p.Capability == nil || !ref(p.Capability.Route) || p.Capability.Configuration != b.Configuration {
			return bad()
		}
		return p.Capability.Qualified, nil
	}
	return bad()
}
