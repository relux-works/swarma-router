package workbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"
	"unicode/utf8"

	"relux.works/swarma-router/launch"
)

const wireTime = "2006-01-02T15:04:05.000Z"

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func object(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		panic("invalid internal JSON")
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		panic("invalid internal JSON")
	}
	return out
}
func same(a, b any) bool { return bytes.Equal(canonical(object(a)), canonical(object(b))) }
func validRevision(r RevisionRef) bool {
	return ref(r.Source) && ref(r.Object) && ref(r.Revision) && digest(r.Digest)
}
func validReason(s string) bool {
	for _, r := range reasons {
		if s == r {
			return true
		}
	}
	return false
}
func timestamp(s string) (time.Time, bool) {
	t, err := time.Parse(wireTime, s)
	return t, err == nil && t.Format(wireTime) == s
}

func validReferences(refs []RevisionRef) bool {
	if refs == nil || len(refs) > 16 {
		return false
	}
	last := ""
	for i, r := range refs {
		if !validRevision(r) {
			return false
		}
		s := string(canonical(object(r)))
		if i > 0 && s <= last {
			return false
		}
		last = s
	}
	return true
}
func normalizeReferences(refs []RevisionRef) ([]RevisionRef, bool) {
	out := append([]RevisionRef{}, refs...)
	sort.Slice(out, func(i, j int) bool { return string(canonical(object(out[i]))) < string(canonical(object(out[j]))) })
	return out, validReferences(out)
}

func validSelected(s *SelectedBinding, req PrepareRequest) bool {
	if s == nil || !ref(s.Member) || s.Configuration.Member != s.Member || !validRevision(s.Proposal) {
		return false
	}
	cardFound := false
	for _, c := range req.Cards {
		if c.Member == s.Member && c.Card == s.Card {
			cardFound = true
		}
	}
	if !cardFound {
		return false
	}
	for _, cfg := range req.Constraints.Configurations {
		if cfg == s.Configuration {
			return true
		}
	}
	return false
}
func validLower(l *LowerMapping, s *SelectedBinding) bool {
	if l == nil || s == nil {
		return false
	}
	i := l.Intent
	for _, v := range []string{i.Contract, i.CorrelationID, i.Project, i.TaskID, i.Member, i.Manager, i.Plugin, i.PluginConfig, i.Runtime, i.Profile, i.ProfileLock, i.Model, i.Effort, i.Workspace, i.TmuxSession, i.Mode, i.NativeSession} {
		if !utf8.ValidString(v) {
			return false
		}
	}
	return ref(l.Binding.Plugin) && ref(l.Binding.OperationRef) && digest(l.Binding.IntentDigest) && validRevision(l.Mapping) && i.Contract == launch.Contract && i.Plugin == l.Binding.Plugin && i.Digest() == l.Binding.IntentDigest && i.ProfileLock == s.Configuration.ProfileLock && i.Runtime == s.Configuration.Runtime
}
func validExecution(e map[string]string) bool {
	m := map[string]any{}
	for k, v := range e {
		m[k] = v
	}
	var names []string
	switch e["kind"] {
	case "a2a":
		names = []string{"kind", "session", "thread", "generation"}
	case "host":
		names = []string{"kind", "execution", "runner_instance", "native_thread", "controller_epoch", "messaging_session", "messaging_thread", "messaging_generation"}
	default:
		return false
	}
	if !keys(m, names...) {
		return false
	}
	for _, s := range e {
		if !ref(s) {
			return false
		}
	}
	return true
}
func validReady(r *ReadyBinding, s *SelectedBinding) bool {
	if r == nil || s == nil || r.Selected != *s || !ref(r.ExecutionOwner) || !validExecution(r.Execution) {
		return false
	}
	for _, x := range []RevisionRef{r.ManagerCustody, r.TaskState, r.RuntimeEvidence, r.MessagingRoute, r.LaneReply} {
		if !validRevision(x) {
			return false
		}
	}
	_, ok := timestamp(r.ValidUntil)
	return ok
}
func observationShape(o *Observation, r operationRecord, b RetainedRequest) bool {
	if o == nil || r.Selected == nil || o.Key != b.Key() || o.RequestDigest != b.Digest() || o.Selected != *r.Selected || !same(o.Lower, lowerBinding(r)) || !validReferences(o.Evidence) || !validReason(o.Reason) {
		return false
	}
	switch o.Status {
	case "ready":
		if o.Reason != "ok" || !validReady(o.Ready, r.Selected) {
			return false
		}
	case "pending", "refused", "unavailable", "unknown":
		if o.Reason == "ok" || o.Ready != nil {
			return false
		}
	default:
		return false
	}
	if o.Status == "unknown" && o.CheckedAt == "" && o.ValidUntil == "" {
		return true
	}
	checked, a := timestamp(o.CheckedAt)
	expiry, c := timestamp(o.ValidUntil)
	if !a || !c || !expiry.After(checked) {
		return false
	}
	if o.Ready != nil {
		t, _ := timestamp(o.Ready.ValidUntil)
		if t.After(expiry) {
			return false
		}
		if r.KnownExecution != nil && !same(r.KnownExecution, o.Ready.Execution) {
			return false
		}
	}
	return true
}

func observationShapeUnordered(o Observation, r operationRecord, b RetainedRequest) bool {
	// The port may supply a semantic set in any order; reject duplicates and bad
	// original strings before sorting/copying, then use the ordinary full shape.
	refs, ok := normalizeReferences(o.Evidence)
	if !ok {
		return false
	}
	o.Evidence = refs
	return observationShape(&o, r, b)
}
func observationFresh(o Observation, now time.Time) bool {
	if o.Status == "unknown" && o.CheckedAt == "" && o.ValidUntil == "" {
		return true
	}
	checked, a := timestamp(o.CheckedAt)
	expiry, b := timestamp(o.ValidUntil)
	if !a || !b || checked.After(now) || !expiry.After(now) {
		return false
	}
	if o.Ready != nil {
		t, _ := timestamp(o.Ready.ValidUntil)
		return t.After(now)
	}
	return true
}
func lowerBinding(r operationRecord) *LowerBinding {
	if r.Lower == nil {
		return nil
	}
	b := r.Lower.Binding
	return &b
}

func recordShape(r operationRecord, b RetainedRequest) bool {
	p, _ := b.Prepare()
	if r.Contract != OperationContract {
		return false
	}
	if r.Sources != nil && (!digest(r.Sources.Digest) || len(r.Sources.Data) == 0 || hash(r.Sources.Data) != r.Sources.Digest || !validReferences(r.Sources.References)) {
		return false
	}
	if r.Proposal != nil && !validRevision(*r.Proposal) {
		return false
	}
	if r.Selected != nil && (!validSelected(r.Selected, p) || r.Proposal == nil || r.Selected.Proposal != *r.Proposal || r.Sources == nil || (r.Action != "reuse" && r.Action != "start")) {
		return false
	}
	if r.Selected == nil && (r.Action != "" || r.Lower != nil || r.PossibleEffect || r.KnownExecution != nil || r.LastObservation != nil) {
		return false
	}
	if r.Lower != nil && (r.Action != "start" || !validLower(r.Lower, r.Selected)) {
		return false
	}
	if r.PossibleEffect && (r.Lower == nil || r.TerminalNoEffect) {
		return false
	}
	if r.KnownExecution != nil && !validExecution(r.KnownExecution) {
		return false
	}
	if r.LastObservation != nil && !observationShape(r.LastObservation, r, b) {
		return false
	}
	if r.Receipt != nil {
		x := r.Receipt
		if x.Kind != "receipt" || x.OperationRef != b.OperationRef() || x.DurableRevision != b.DurableRevision() || x.Historical || !same(x.Selected, r.Selected) || !same(x.Lower, lowerBinding(r)) || !validReferences(x.Evidence) || !validReason(x.Reason) {
			return false
		}
		if x.Status == "ready" {
			if x.Reason != "ok" || !validReady(x.Ready, r.Selected) || !same(r.KnownExecution, x.Ready.Execution) {
				return false
			}
		} else if x.Ready != nil || x.Reason == "ok" {
			return false
		}
		if r.LastObservation != nil {
			o := r.LastObservation
			if x.Status != o.Status || x.Reason != o.Reason || !same(x.Ready, o.Ready) || !same(x.Evidence, o.Evidence) {
				return false
			}
		} else if x.Status == "ready" || x.Status == "pending" {
			return false
		}
	}
	switch r.Phase {
	case "resolving":
		return r.Sources == nil && r.Proposal == nil && r.Selected == nil && r.Receipt == nil && !r.TerminalNoEffect
	case "selected":
		return r.Selected != nil && r.Lower == nil && !r.PossibleEffect && r.Receipt == nil && !r.TerminalNoEffect
	case "preparing":
		return r.Selected != nil && r.Action == "start" && r.Receipt == nil && !r.TerminalNoEffect
	case "observing":
		return r.Selected != nil && (r.Action == "reuse" || r.Lower != nil) && (r.Receipt == nil || r.Receipt.Status == "pending") && !r.TerminalNoEffect
	case "ready":
		return r.Selected != nil && r.Receipt != nil && r.Receipt.Status == "ready" && !r.TerminalNoEffect
	case "deferred":
		return r.Receipt != nil && r.Receipt.Status == r.Phase && r.TerminalNoEffect
	case "refused", "unavailable", "unknown":
		return r.Receipt != nil && r.Receipt.Status == r.Phase
	default:
		return false
	}
}

func encodeOperation(next operationRecord, binding RetainedRequest) (operationRecord, []byte, error) {
	b, err := binding.Encode()
	if err != nil {
		return operationRecord{}, nil, err
	}
	built := clone(next)
	built.Request = bytes.Clone(b[:len(b)-1])
	if built.Receipt != nil {
		built.Receipt.OperationRef = binding.OperationRef()
		built.Receipt.DurableRevision = binding.DurableRevision()
	}
	if !recordShape(built, binding) {
		return operationRecord{}, nil, errors.New("store_invalid")
	}
	b = append(canonical(object(built)), '\n')
	if len(b) > MaxRecordBytes {
		return operationRecord{}, nil, errors.New("output_budget")
	}
	if _, _, ok := decodeOperation(b, binding.Key()); !ok {
		return operationRecord{}, nil, errors.New("store_invalid")
	}
	return built, b, nil
}

func decodeOperation(raw []byte, key OperationKey) (operationRecord, RetainedRequest, bool) {
	bad := func() (operationRecord, RetainedRequest, bool) { return operationRecord{}, RetainedRequest{}, false }
	if len(raw) < 3 || len(raw) > MaxRecordBytes || raw[len(raw)-1] != '\n' || raw[len(raw)-2] != '}' || !utf8.Valid(raw) || !scalarEscapes(raw) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw[:len(raw)-1]))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return bad()
	}
	if _, err = d.Token(); err != io.EOF {
		return bad()
	}
	var r operationRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil || !bytes.Equal(canonical(v), canonical(object(r))) {
		return bad()
	}
	b, err := RestoreRetained(key, append(bytes.Clone(r.Request), '\n'))
	if err != nil || !recordShape(r, b) {
		return bad()
	}
	return r, b, true
}

func immutableTransition(old operationRecord, next operationRecord) bool {
	if old.Contract == "" {
		return next.Phase == "resolving"
	}
	for _, pair := range [][2]any{{old.Sources, next.Sources}, {old.Proposal, next.Proposal}, {old.Selected, next.Selected}, {old.Lower, next.Lower}, {old.KnownExecution, next.KnownExecution}} {
		// A nil first value can become known exactly once; retained non-nil data
		// never changes. Typed nils are normalized through JSON for this check.
		if object(pair[0]) != nil && !same(pair[0], pair[1]) {
			return false
		}
	}
	if old.Action != "" && old.Action != next.Action || old.PossibleEffect && !next.PossibleEffect || old.TerminalNoEffect && !next.TerminalNoEffect {
		return false
	}
	if old.TerminalNoEffect {
		return old.Phase == next.Phase
	}
	allowed := map[string][]string{"resolving": {"selected", "deferred", "refused", "unavailable", "unknown"}, "selected": {"preparing", "observing"}, "preparing": {"preparing", "observing", "unknown", "refused"}, "observing": {"observing", "ready", "refused", "unavailable", "unknown"}, "ready": {"observing", "ready", "unknown", "refused", "unavailable"}, "unknown": {"observing", "unknown", "ready", "refused", "unavailable"}, "refused": {"observing", "unknown", "ready", "refused", "unavailable"}, "unavailable": {"observing", "unknown", "ready", "refused", "unavailable"}}
	for _, s := range allowed[old.Phase] {
		if next.Phase == s {
			return true
		}
	}
	return false
}
