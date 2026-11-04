package localworkbench

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func bind(r LocalRequest, in *LocalOwnerInputs) {
	rd, err := RequestDigest(r)
	if err != nil {
		panic(err)
	}
	in.RequestDigest = rd
	for i := range in.Targets {
		target := &in.Targets[i]
		td := TargetDigest(target.Binding)
		for j := range target.Checks {
			g := &target.Checks[j]
			g.RequestDigest = rd
			g.TargetDigest = td
			if g.Evidence.Projection != nil && g.Evidence.Projection.Response != nil {
				g.Evidence.Projection.Response.RequestDigest = rd
			}
		}
	}
}
func alternative(action, member, mx string) (LocalRequest, LocalOwnerInputs) {
	r, in := fixture(action)
	old := in.Targets[0].Binding.MXID
	r.Cards[0].Member, r.Cards[0].MXID = member, mx
	r.Configurations[0].Member, r.Configurations[0].MXID = member, mx
	b := &in.Targets[0].Binding
	b.Member, b.MXID, b.Seat, b.Configuration = member, mx, member+"-seat", r.Configurations[0]
	for i := range in.Targets[0].Checks {
		e := &in.Targets[0].Checks[i].Evidence
		e.Statement = strings.ReplaceAll(e.Statement, old, mx)
		if p := e.Projection; p != nil {
			p.Subject = Subject{member, mx, b.CardSHA256}
			if p.Configuration != nil {
				p.Configuration = pointer(b.Configuration)
			}
			if p.Live != nil {
				p.Live.Configuration = b.Configuration
			}
			if p.Capability != nil {
				p.Capability.Configuration = b.Configuration
			}
			if p.Membership != nil {
				p.Membership.MXID = mx
			}
			if p.Response != nil {
				p.Response.Sender = mx
			}
			if p.Absence != nil {
				p.Absence.MXID = mx
				p.Absence.Seat = b.Seat
			}
		}
	}
	bind(r, &in)
	return r, in
}

// Pure request identity preserves opaque Ref bytes and full domains, canonicalizes
// sets, and rejects invalid/ambiguous or oversized requests before any read.
func TestRequestBoundaryAndSetIdentity(t *testing.T) {
	r, in := fixture("reuse")
	other, second := alternative("start", "member / *", "@worker:two.example")
	r.Cards = append(r.Cards, other.Cards...)
	r.Configurations = append(r.Configurations, other.Configurations...)
	in.Targets = append(in.Targets, second.Targets...)
	bind(r, &in)
	a, _ := RequestDigest(r)
	r.Cards[0], r.Cards[1] = r.Cards[1], r.Cards[0]
	r.Configurations[0], r.Configurations[1] = r.Configurations[1], r.Configurations[0]
	b, _ := RequestDigest(r)
	if a != b {
		t.Fatal("set permutation changed digest")
	}
	out, e := run(r, in)
	if e != nil || out.Action != "reuse" || out.Selected.MXID != "@worker:one.example" {
		t.Fatalf("eligible reuse not preferred %+v/%v", out, e)
	}
	for _, mutate := range []func(*LocalRequest){
		func(x *LocalRequest) { x.Caller = "manager" }, func(x *LocalRequest) { x.Project = "" }, func(x *LocalRequest) { x.Objective = strings.Repeat("a", 4097) },
		func(x *LocalRequest) { x.Cards = append(x.Cards, x.Cards[0]) }, func(x *LocalRequest) { x.Configurations = append(x.Configurations, x.Configurations[0]) },
		func(x *LocalRequest) { x.Cards[0].MXID = "worker" }, func(x *LocalRequest) { x.Configurations[0].Member = "missing" },
		func(x *LocalRequest) { x.Project = "hidden\u202eproject" }, func(x *LocalRequest) { x.Project = string([]byte{0xff}) },
	} {
		bad := clone(r)
		mutate(&bad)
		if _, err := RequestDigest(bad); err == nil || err.Error() != "invalid_request" {
			t.Fatalf("wrong invalid request result %v", err)
		}
	}
}

// Complete absence is required only for start. Unknown/partial/busy evidence
// cannot start a second session, while separate stopped alternatives stay valid.
func TestStartAbsenceAndLiveIdentity(t *testing.T) {
	r, in := fixture("start")
	for _, name := range []string{"missing", "partial", "stale", "unavailable"} {
		bad := clone(in)
		gate(&bad, "complete_owned_absence").Evidence = Evidence{Kind: "unknown", Reason: name}
		o, e := run(r, bad)
		if e != nil || o.Action != "insufficient_evidence" || o.PreparedWorkbench != nil {
			t.Fatalf("absence %s promoted %+v/%v", name, o, e)
		}
	}
	for _, g := range []string{"quality", "provider_limits", "complete_owned_absence", "launch_capability"} {
		bad := clone(in)
		basis := clone(gate(&bad, g).Evidence)
		switch g {
		case "quality":
			basis.Projection.Qualification.Qualified = false
		case "provider_limits":
			basis.Projection.Quota.Available = 0
		case "complete_owned_absence":
			basis.Projection.Absence.Absent = false
		case "launch_capability":
			basis.Projection.Capability.Qualified = false
		}
		gate(&bad, g).Evidence = Evidence{Kind: "deny", Reason: "predicate_false", Basis: &basis}
		o, e := run(r, bad)
		if e != nil || o.Action != "refuse" || o.PreparedWorkbench != nil {
			t.Fatalf("denial %s promoted %+v/%v", g, o, e)
		}
	}
	_, ready := fixture("reuse")
	bad := clone(in)
	bad.Targets = append(bad.Targets, ready.Targets...)
	if o, e := run(r, bad); e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
		t.Fatal("live+absent same seat accepted")
	}
	// A second configuration is not a second live identity. Both are inert start
	// alternatives for the same positively absent owned seat.
	b := clone(in.Targets[0])
	b.Binding.Configuration.Model = "another-model"
	r.Configurations = append(r.Configurations, b.Binding.Configuration)
	for i := range b.Checks {
		p := b.Checks[i].Evidence.Projection
		if p != nil {
			if p.Configuration != nil {
				p.Configuration = pointer(b.Binding.Configuration)
			}
			if p.Capability != nil {
				p.Capability.Configuration = b.Binding.Configuration
			}
		}
	}
	in.Targets = append(in.Targets, b)
	bind(r, &in)
	o, e := run(r, in)
	if e != nil || o.Action != "start" {
		t.Fatalf("valid stopped options rejected %+v/%v", o, e)
	}
}

// Refresh narrows the admitted read to the retained complete target; mutation of
// reader arguments or returning a different generation cannot replace it.
func TestRetainedRefreshAndCopyBoundary(t *testing.T) {
	r, in := fixture("reuse")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rd, _ := RequestDigest(r)
	s := LocalSelection{rd, clone(in.Targets[0].Binding)}
	calls := 0
	p := LocalPlanner{Local: LocalContext{r.Caller, r.Project}, Room: "!room:one.example", Clock: func() time.Time { return now }}
	p.Reader = readerFunc(func(_ context.Context, got LocalRequest, only *LocalSelection) (LocalOwnerInputs, error) {
		calls++
		if only == nil || TargetDigest(only.Target) != TargetDigest(s.Target) {
			t.Fatal("selection missing")
		}
		got.Cards[0].MXID = "@wrong:other.example"
		only.Target.Execution.Generation = "changed-reader-argument"
		return in, nil
	})
	o, e := p.Revalidate(ctx, r, s)
	if e != nil || o.Action != "reuse" || s.Target.Execution.Generation != "generation-1" || r.Cards[0].MXID != "@worker:one.example" || calls != 1 {
		t.Fatalf("copy boundary %+v/%v", o, e)
	}
	p.Reader = readerFunc(func(context.Context, LocalRequest, *LocalSelection) (LocalOwnerInputs, error) {
		bad := clone(in)
		bad.Targets[0].Binding.Execution.Generation = "replacement"
		bind(r, &bad)
		return bad, nil
	})
	o, e = p.Revalidate(ctx, r, s)
	if e == nil || e.Error() != "source_conflict" || o.PreparedWorkbench != nil {
		t.Fatal("replacement refresh accepted")
	}
}

// Unknown dynamic facts remain unknown, null source lifetimes stay null, and a
// caller statement survives exactly. A collector never supplies an expiry.
func TestNullLifetimeAndVerbatimStatement(t *testing.T) {
	r, in := fixture("reuse")
	statement := "Explicit admission for TASK-1 / @worker:one.example\nno promise of authenticated authority."
	gate(&in, "caller_authority").Evidence.Statement = statement
	for i := range in.Targets[0].Checks {
		e := &in.Targets[0].Checks[i].Evidence
		if e.Source != nil {
			e.Source.ExpiresAt = nil
		}
		if e.Projection != nil && e.Projection.Live != nil {
			e.Projection.Live.Source.ExpiresAt = nil
		}
	}
	o, e := run(r, in)
	if e != nil || o.Action != "reuse" {
		t.Fatalf("null expiry fabricated dependency %+v/%v", o, e)
	}
	for _, g := range o.Evidence {
		if g.Gate == "caller_authority" && g.Evidence.Statement != statement {
			t.Fatal("statement changed")
		}
		if g.Evidence.Source != nil && g.Evidence.Source.ExpiresAt != nil {
			t.Fatal("expiry invented")
		}
	}
	for _, scope := range []string{"partial", "complete"} {
		empty := clone(in)
		empty.Targets = nil
		empty.Scope = scope
		o, e := run(r, empty)
		if e != nil || o.Action != "insufficient_evidence" || o.PreparedWorkbench != nil {
			t.Fatal("empty response became complete absence")
		}
	}
}

// Internal snapshot decoding rejects structural ambiguity, unknown fields,
// omitted mandatory fields and lone surrogates. Legitimate escaped scalars and
// optional whitespace remain accepted; parsing itself is not source trust.
func TestSnapshotDecoder(t *testing.T) {
	_, in := fixture("reuse")
	raw := encode(in)
	if _, e := DecodeInputs(raw); e != nil {
		t.Fatal(e)
	}
	valid := bytes.Replace(raw, []byte(`"role":"developer"`), []byte(`"role":"dev\ud83d\ude00"`), 1)
	if _, e := DecodeInputs(append([]byte(" \n"), valid...)); e != nil {
		t.Fatal("valid scalar pair/whitespace rejected", e)
	}
	bad := [][]byte{
		[]byte(`null`), []byte(`[]`), append(append([]byte{}, raw...), []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"kind":"observed"`), []byte(`"kind":"observed","kind":"observed"`), 1),
		bytes.Replace(raw, []byte(`"kind":"observed"`), []byte(`"kind":"observed","k\u0069nd":"observed"`), 1),
		bytes.Replace(raw, []byte(`"role":"developer"`), []byte(`"role":"\ud800"`), 1),
		bytes.Replace(raw, []byte(`"read_at":"2026-11-04T05:30:00.000Z"`), []byte(`"read_at":"2026-11-04T05:30:00Z"`), 1),
		append([]byte(`{"extra":1,`), raw[1:]...),
		bytes.Replace(raw, []byte(`"joined":true,`), nil, 1),
		bytes.Replace(raw, []byte(`"joined":true`), []byte(`"joined":null`), 1),
		append([]byte{0xff}, raw...), bytes.Repeat([]byte(" "), MaxSnapshotBytes+1),
	}
	for i, b := range bad {
		if _, e := DecodeInputs(b); e == nil || e.Error() != "source_conflict" {
			t.Fatalf("invalid case%d accepted %v", i, e)
		}
	}
}

func forbiddenImports(raw []byte) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", raw, parser.ImportsOnly)
	if err != nil {
		return true
	}
	allow := map[string]bool{}
	for _, s := range []string{"context", "encoding/json", "sort", "time", "bytes", "crypto/sha256", "encoding/hex", "errors", "io", "reflect", "strconv", "strings", "unicode", "unicode/utf8"} {
		allow[s] = true
	}
	for _, im := range f.Imports {
		s, e := strconv.Unquote(im.Path.Value)
		if e != nil || !allow[s] {
			return true
		}
	}
	return false
}

// The production package has only admitted pure standard-library imports. This
// is a bounded structural no-effect witness, not protection against a malicious
// admitted reader. The control plant proves os/exec is actually detected.
func TestEffectImportBoundary(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		if forbiddenImports(raw) {
			t.Fatalf("effect-capable/unadmitted import: %s", p)
		}
		count++
	}
	if count == 0 {
		t.Fatal("no source checked")
	}
	if forbiddenImports([]byte("package fixture\nimport \"context\"\n")) {
		t.Fatal("pure positive rejected")
	}
	if !forbiddenImports([]byte("package fixture\nimport \"os/exec\"\n")) {
		t.Fatal("known effect import was not detected")
	}
}

// This small valid reuse witness is also the nearby positive control for the
// separate action-relevance guard-removal experiment.
func TestRecommendReusePositive(t *testing.T) {
	r, in := fixture("reuse")
	o, e := run(r, in)
	if e != nil || o.Action != "reuse" || o.PreparedWorkbench == nil || o.ExecutionAuthority {
		t.Fatalf("%+v/%v", o, e)
	}
}

// Encoding the snapshot preserves the canonical UTC-millisecond representation
// and the explicit false execution flag; it never emits a partial positive body.
func TestRecommendationBytes(t *testing.T) {
	r, in := fixture("reuse")
	o, e := run(r, in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := o.Bytes()
	if e != nil || !bytes.Contains(b, []byte(`"as_of":"2026-11-04T05:30:00.000Z"`)) || !bytes.Contains(b, []byte(`"execution_authority":false`)) {
		t.Fatalf("%s / %v", b, e)
	}
	o.ExecutionAuthority = true
	if b, e := o.Bytes(); e == nil || len(b) != 0 {
		t.Fatal("execution authority emitted")
	}
}

// Strict decoder uses numbers without floating-point rounding of quota values.
func TestSnapshotQuotaIntegerPrecision(t *testing.T) {
	_, in := fixture("reuse")
	gate(&in, "provider_limits").Evidence.Projection.Quota.Available = 1<<60 + 17
	raw := encode(in)
	decoded, e := DecodeInputs(raw)
	if e != nil {
		t.Fatal(e)
	}
	if gate(&decoded, "provider_limits").Evidence.Projection.Quota.Available != 1<<60+17 {
		t.Fatal("quota rounded")
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		t.Fatal("invalid canonical JSON")
	}
}
