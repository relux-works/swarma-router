package workbench

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// This manually ordered vector specifies every field's canonical position. It
// is independent of the implementation's object builder and sorting routines.
const goldenPrepare = `{"caller":"caller","cards":[{"card":{"digest":"1111111111111111111111111111111111111111111111111111111111111111","object":"x","revision":"r","source":"s"},"member":"member"}],"constraints":{"configurations":[{"effort":"high","member":"member","model":{"digest":"1111111111111111111111111111111111111111111111111111111111111111","object":"x","revision":"r","source":"s"},"placement":"host","profile":{"digest":"1111111111111111111111111111111111111111111111111111111111111111","object":"x","revision":"r","source":"s"},"profile_lock":"1111111111111111111111111111111111111111111111111111111111111111","runtime":"codex_cli","vendor":"openai"}],"objective":"retain-context","policy":{"digest":"1111111111111111111111111111111111111111111111111111111111111111","object":"x","revision":"r","source":"s"}},"contract":"swarma-router/0.2.0-draft.2","correlation_id":"example","operation":"prepare_workbench","project":"project","task":{"digest":"1111111111111111111111111111111111111111111111111111111111111111","object":"x","revision":"r","source":"s"}}`

const goldenRecovery = `{"caller":"caller","contract":"swarma-router/0.2.0-draft.2","correlation_id":"example","operation":"prepare_workbench","project":"project","request_digest":"DIGEST","result":{"historical":false,"key":{"caller":"caller","correlation_id":"example","project":"project"},"kind":"recovery","last_durable_revision":null,"lower_launch":null,"operation_ref":null,"reason":"unsupported_configuration","request_digest":"DIGEST","status":"unknown"}}`

var local = LocalContext{"caller", "project"}

func base(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(goldenPrepare), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func frame(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func mustParse(t *testing.T, b []byte, ctx LocalContext) Request {
	t.Helper()
	r, err := Parse(b, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func reject(t *testing.T, b []byte, ctx LocalContext, reason string) {
	t.Helper()
	r, err := Parse(b, ctx)
	if err == nil || err.Error() != reason {
		t.Fatalf("wanted %s, got %v", reason, err)
	}
	if r.Digest() != "" || r.Operation() != "" || r.Key() != (OperationKey{}) || r.CanonicalPrepare() != nil || r.RecoveryTemplate() != nil {
		t.Fatal("rejection leaked a partial admitted request")
	}
	if _, ok := r.Prepare(); ok {
		t.Fatal("rejection exposed prepare")
	}
}

func cfg(m map[string]any) map[string]any {
	return m["constraints"].(map[string]any)["configurations"].([]any)[0].(map[string]any)
}

// Proves complete golden bytes/digest, exact initial recovery shape, and typed
// copy isolation; no operation engine or IO is exercised by this package.
func TestGoldenPrepare(t *testing.T) {
	r := mustParse(t, []byte(goldenPrepare+"\n"), local)
	if string(r.CanonicalPrepare()) != goldenPrepare {
		t.Fatalf("canonical mismatch: %s", r.CanonicalPrepare())
	}
	const expectedDigest = "a43077d11f2854cb0289649dc710951577b4774401ca3d9028fde3b6fd5341ce"
	if r.Digest() != expectedDigest {
		t.Fatalf("digest %s", r.Digest())
	}
	if string(r.RecoveryTemplate()) != strings.ReplaceAll(goldenRecovery, "DIGEST", expectedDigest)+"\n" {
		t.Fatalf("recovery mismatch: %s", r.RecoveryTemplate())
	}
	p, ok := r.Prepare()
	if !ok || p.Task.Object != "x" || p.Constraints.Configurations[0].Effort != "high" || r.Key() != (OperationKey{"caller", "project", "example"}) {
		t.Fatal("wrong typed request")
	}
	p.Cards[0].Member = "changed"
	p.Constraints.Configurations[0].Model.Revision = "changed"
	r.CanonicalPrepare()[0] = 'x'
	r.RecoveryTemplate()[0] = 'x'
	p, _ = r.Prepare()
	if p.Cards[0].Member != "member" || p.Constraints.Configurations[0].Model.Revision != "r" || r.CanonicalPrepare()[0] != '{' || r.RecoveryTemplate()[0] != '{' {
		t.Fatal("mutable result alias")
	}
}

// Equivalent whitespace/key order/escape spelling are positive controls. Every
// application-field change below must alter the full prepare digest.
func TestEquivalentSpellings(t *testing.T) {
	want := mustParse(t, []byte(goldenPrepare+"\n"), local).Digest()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(goldenPrepare), "", " \t"); err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{frame(t, base(t)), append(pretty.Bytes(), '\n'), []byte(strings.ReplaceAll(goldenPrepare, `"caller"`, `"\u0063aller"`) + "\n")} {
		if got := mustParse(t, b, local).Digest(); got != want {
			t.Fatal("equivalent spelling changed identity")
		}
	}
}

func TestAllApplicationFieldsBindDigest(t *testing.T) {
	want := mustParse(t, []byte(goldenPrepare+"\n"), local).Digest()
	type change struct {
		name   string
		mutate func(map[string]any)
	}
	changes := []change{
		{"caller", func(m map[string]any) { m["caller"] = "other" }},
		{"project", func(m map[string]any) { m["project"] = "other" }},
		{"correlation", func(m map[string]any) { m["correlation_id"] = "other" }},
		{"objective", func(m map[string]any) { m["constraints"].(map[string]any)["objective"] = "other" }},
		{"member", func(m map[string]any) {
			m["cards"].([]any)[0].(map[string]any)["member"] = "other"
			cfg(m)["member"] = "other"
		}},
	}
	for _, key := range []string{"runtime", "vendor", "effort", "placement", "profile_lock"} {
		changes = append(changes, change{"config-" + key, func(m map[string]any) {
			value := "other"
			if key == "profile_lock" {
				value = strings.Repeat("2", 64)
			}
			cfg(m)[key] = value
		}})
	}
	for _, target := range []string{"task", "card", "policy", "model", "profile"} {
		for _, field := range []string{"source", "object", "revision", "digest"} {
			changes = append(changes, change{target + "-" + field, func(m map[string]any) {
				var ref map[string]any
				switch target {
				case "task":
					ref = m["task"].(map[string]any)
				case "card":
					ref = m["cards"].([]any)[0].(map[string]any)["card"].(map[string]any)
				case "policy":
					ref = m["constraints"].(map[string]any)["policy"].(map[string]any)
				default:
					ref = cfg(m)[target].(map[string]any)
				}
				value := "other"
				if field == "digest" {
					value = strings.Repeat("2", 64)
				}
				ref[field] = value
			}})
		}
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			m := base(t)
			tc.mutate(m)
			ctx := LocalContext{m["caller"].(string), m["project"].(string)}
			if mustParse(t, frame(t, m), ctx).Digest() == want {
				t.Fatal("field omitted from identity")
			}
		})
	}
}

// Set order is not rank; same-member different tuples remain valid, while both
// semantic duplicate kinds and undeclared members are rejected.
func TestSemanticSets(t *testing.T) {
	m := base(t)
	card := base(t)["cards"].([]any)[0].(map[string]any)
	card["member"] = "another"
	m["cards"] = append(m["cards"].([]any), card)
	second := cfg(base(t))
	second["placement"] = "elsewhere"
	constraints := m["constraints"].(map[string]any)
	constraints["configurations"] = append(constraints["configurations"].([]any), second)
	want := mustParse(t, frame(t, m), local).Digest()
	cards := m["cards"].([]any)
	cards[0], cards[1] = cards[1], cards[0]
	configs := constraints["configurations"].([]any)
	configs[0], configs[1] = configs[1], configs[0]
	if mustParse(t, frame(t, m), local).Digest() != want {
		t.Fatal("set permutation changed identity")
	}
	m = base(t)
	card = base(t)["cards"].([]any)[0].(map[string]any)
	card["card"].(map[string]any)["revision"] = "other"
	m["cards"] = append(m["cards"].([]any), card)
	reject(t, frame(t, m), local, "invalid_request")
	m = base(t)
	constraints = m["constraints"].(map[string]any)
	constraints["configurations"] = append(constraints["configurations"].([]any), cfg(base(t)))
	reject(t, frame(t, m), local, "invalid_request")
	m = base(t)
	cfg(m)["member"] = "undeclared"
	reject(t, frame(t, m), local, "invalid_request")
}

// The literal expected string checks all 32 controls, slash/quote/backslash,
// non-ASCII, non-normalization and HTML/line-separator escaping conventions.
func TestCanonicalStrings(t *testing.T) {
	const controls = `"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000a\u000b\u000c\u000d\u000e\u000f\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f"`
	var all strings.Builder
	for i := 0; i < 32; i++ {
		all.WriteByte(byte(i))
	}
	if string(canonical(all.String())) != controls {
		t.Fatal("control canonical spelling")
	}
	for _, s := range []string{"é", "e\u0301", "😀", "�", "<>&", "\u2028\u2029"} {
		if string(canonical(s)) != `"`+s+`"` {
			t.Fatalf("unexpected escaping: %q", s)
		}
		m := base(t)
		m["constraints"].(map[string]any)["objective"] = s
		mustParse(t, frame(t, m), local)
	}
	if string(canonical("\"\\/")) != `"\"\\/"` {
		t.Fatal("quote/backslash/slash spelling")
	}
	if string(canonical(map[string]any{"é": "e", "z": "z", "😀": "s"})) != `{"z":"z","é":"e","😀":"s"}` {
		t.Fatal("UTF-8 key order")
	}
	plain := strings.Replace(goldenPrepare, "retain-context", "😀", 1)
	escaped := strings.Replace(goldenPrepare, "retain-context", `\ud83d\ude00`, 1)
	if mustParse(t, []byte(plain+"\n"), local).Digest() != mustParse(t, []byte(escaped+"\n"), local).Digest() {
		t.Fatal("paired escape inequivalence")
	}
	plain = strings.Replace(goldenPrepare, "retain-context", `\\ud800`, 1)
	mustParse(t, []byte(plain+"\n"), local) // Literal backslash text, not a surrogate.
	a := base(t)
	a["constraints"].(map[string]any)["objective"] = "é"
	b := base(t)
	b["constraints"].(map[string]any)["objective"] = "e\u0301"
	if mustParse(t, frame(t, a), local).Digest() == mustParse(t, frame(t, b), local).Digest() {
		t.Fatal("unexpected Unicode normalization")
	}
}

// Each forbidden frame has the golden valid neighbor. No errors expose raw
// input, partial parsed data or alternative result frames.
func TestFrameRejections(t *testing.T) {
	mustParse(t, []byte(goldenPrepare+"\n"), local)
	invalid := []string{
		goldenPrepare, goldenPrepare + "\r\n", goldenPrepare + "\n\n", "\ufeff" + goldenPrepare + "\n",
		goldenPrepare + " {}\n", goldenPrepare + " null\n", goldenPrepare[:len(goldenPrepare)-1] + "\n",
		"[]\n", "null\n", "{}\n", "\n", "",
		strings.Replace(goldenPrepare, `"caller":"caller"`, `"caller":"caller","\u0063aller":"caller"`, 1) + "\n",
		strings.Replace(goldenPrepare, `"revision":"r"`, `"revision":"r","revision":"r"`, 1) + "\n",
		strings.Replace(goldenPrepare, `"effort":"high"`, `"effort":"high","effort":"high"`, 1) + "\n",
	}
	for _, v := range []string{`\ud800`, `\udc00`, `\ud800x`, `\ud800\u0041`, `\uQQQQ`, `\u12`, `\q`} {
		invalid = append(invalid, strings.Replace(goldenPrepare, "retain-context", v, 1)+"\n")
	}
	for i, b := range invalid {
		t.Run(string(rune('A'+i)), func(t *testing.T) { reject(t, []byte(b), local, "invalid_request") })
	}
	b := []byte(goldenPrepare + "\n")
	b[20] = 0xff
	reject(t, b, local, "invalid_request")
	reject(t, []byte(strings.Repeat(" ", MaxRequestBytes)+goldenPrepare+"\n"), local, "invalid_request")
}

// Every currently recognized object is closed with required fields; case
// aliases, nulls, numbers, unknown fields and no-effort values cannot enter.
func TestExactSchema(t *testing.T) {
	paths := []func(map[string]any) map[string]any{
		func(m map[string]any) map[string]any { return m },
		func(m map[string]any) map[string]any { return m["task"].(map[string]any) },
		func(m map[string]any) map[string]any { return m["cards"].([]any)[0].(map[string]any) },
		func(m map[string]any) map[string]any {
			return m["cards"].([]any)[0].(map[string]any)["card"].(map[string]any)
		},
		func(m map[string]any) map[string]any { return m["constraints"].(map[string]any) },
		func(m map[string]any) map[string]any {
			return m["constraints"].(map[string]any)["policy"].(map[string]any)
		},
		cfg,
		func(m map[string]any) map[string]any { return cfg(m)["model"].(map[string]any) },
		func(m map[string]any) map[string]any { return cfg(m)["profile"].(map[string]any) },
	}
	for _, path := range paths {
		m := base(t)
		path(m)["extra"] = true
		reject(t, frame(t, m), local, "invalid_request")
		for key := range path(base(t)) {
			m = base(t)
			target := path(m)
			delete(target, key)
			reject(t, frame(t, m), local, "invalid_request")
			for _, value := range []any{nil, true, 1, -0.5, []any{}} {
				m = base(t)
				path(m)[key] = value
				reject(t, frame(t, m), local, "invalid_request")
			}
			m = base(t)
			target = path(m)
			target[strings.ToUpper(key)] = target[key]
			delete(target, key)
			reject(t, frame(t, m), local, "invalid_request")
		}
	}
	for _, value := range []any{"", strings.Repeat("x", 257), strings.Repeat("é", 129)} {
		m := base(t)
		cfg(m)["effort"] = value
		reject(t, frame(t, m), local, "invalid_request")
	}
	m := base(t)
	cfg(m)["effort"] = strings.Repeat("é", 128)
	mustParse(t, frame(t, m), local)
	for _, value := range []string{strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("1", 63), strings.Repeat("1", 65)} {
		m := base(t)
		cfg(m)["profile_lock"] = value
		reject(t, frame(t, m), local, "invalid_request")
	}
	for _, n := range []string{"0", "-0", "01", "1.0", "1e0", "9007199254740992"} {
		reject(t, []byte(strings.Replace(goldenPrepare, `"high"`, n, 1)+"\n"), local, "invalid_request")
	}
	reject(t, []byte(strings.Replace(goldenPrepare, Contract, "future", 1)+"\n"), local, "unsupported_version")
	for _, op := range []string{"Prepare_Workbench", "start", "retry", "change"} {
		reject(t, []byte(strings.Replace(goldenPrepare, "prepare_workbench", op, 1)+"\n"), local, "invalid_request")
	}
}

// Exact limits are tested at admission and one step beyond. The lower-level
// scanner depth control is necessary because the closed schema itself is shallow.
func TestBounds(t *testing.T) {
	for _, count := range []int{0, 1, 64, 65} {
		m := base(t)
		cards := []any{}
		for i := 0; i < count; i++ {
			c := base(t)["cards"].([]any)[0].(map[string]any)
			if i > 0 {
				c["member"] = strings.Repeat("m", i)
			}
			cards = append(cards, c)
		}
		m["cards"] = cards
		if count == 1 || count == 64 {
			mustParse(t, frame(t, m), local)
		} else {
			reject(t, frame(t, m), local, "invalid_request")
		}
	}
	for _, count := range []int{0, 1, 16, 17} {
		m := base(t)
		configs := []any{}
		for i := 0; i < count; i++ {
			c := cfg(base(t))
			c["placement"] = strings.Repeat("p", i+1)
			configs = append(configs, c)
		}
		m["constraints"].(map[string]any)["configurations"] = configs
		if count == 1 || count == 16 {
			mustParse(t, frame(t, m), local)
		} else {
			reject(t, frame(t, m), local, "invalid_request")
		}
	}
	for _, depth := range []int{12, 13} {
		b := strings.Repeat("[", depth) + `"x"` + strings.Repeat("]", depth)
		d := json.NewDecoder(strings.NewReader(b))
		d.UseNumber()
		_, err := readValue(d, 0)
		if (err == nil) != (depth == 12) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
	padding := MaxRequestBytes - len(goldenPrepare) - 1
	mustParse(t, []byte(strings.Repeat(" ", padding)+goldenPrepare+"\n"), local)
	reject(t, []byte(strings.Repeat(" ", padding+1)+goldenPrepare+"\n"), local, "invalid_request")
}

// Trusted selectors are supplied separately; raw request selectors do not
// authenticate anything. Status uses the same tuple and original digest only.
func TestContextAndStatus(t *testing.T) {
	reject(t, []byte(goldenPrepare+"\n"), LocalContext{"other", "project"}, "caller_mismatch")
	reject(t, []byte(goldenPrepare+"\n"), LocalContext{"caller", "other"}, "project_mismatch")
	reject(t, []byte(goldenPrepare+"\n"), LocalContext{}, "authority_unknown")
	prepare := mustParse(t, []byte(goldenPrepare+"\n"), local)
	m := map[string]any{"contract": Contract, "operation": "status", "correlation_id": "example", "caller": "caller", "project": "project", "expected_request_digest": prepare.Digest()}
	r := mustParse(t, frame(t, m), local)
	if r.Key() != prepare.Key() || r.Digest() != prepare.Digest() || r.Operation() != "status" || r.CanonicalPrepare() != nil {
		t.Fatal("status invented identity")
	}
	if _, ok := r.Prepare(); ok {
		t.Fatal("status carries prepare")
	}
	for _, key := range []string{"task", "cards", "constraints", "configuration", "retry"} {
		m[key] = true
		reject(t, frame(t, m), local, "invalid_request")
		delete(m, key)
	}
	delete(m, "expected_request_digest")
	reject(t, frame(t, m), local, "invalid_request")
}

// Repeated opaque Ref echoes include their JSON expansion. Long ordinary UTF-8
// remains legitimate; aggregate control-heavy echoes can exceed recovery budget.
func TestRecoveryBudget(t *testing.T) {
	m := base(t)
	ctx := LocalContext{strings.Repeat("é", 128), strings.Repeat("x", 256)}
	m["caller"] = ctx.Caller
	m["project"] = ctx.Project
	m["correlation_id"] = strings.Repeat("y", 256)
	r := mustParse(t, frame(t, m), ctx)
	if len(r.RecoveryTemplate()) > MaxRecoveryBytes {
		t.Fatal("ordinary refs overflowed")
	}
	ctx = LocalContext{strings.Repeat("\x00", 256), strings.Repeat("\x01", 256)}
	m["caller"] = ctx.Caller
	m["project"] = ctx.Project
	m["correlation_id"] = strings.Repeat("\n", 256)
	reject(t, frame(t, m), ctx, "output_budget")
	// Status has an even-sized baseline, allowing exactly 4096 bytes. Derive
	// the boundary from the independent literal golden envelope: each
	// replaced ASCII key byte is echoed twice, each NUL adds five extra bytes.
	baseline := len(strings.ReplaceAll(goldenRecovery, "DIGEST", strings.Repeat("1", 64))) + 1 - len("prepare_workbench") + len("status")
	found := false
	for ascii := 1; ascii <= 256 && !found; ascii++ {
		for controls := 0; controls <= 256; controls++ {
			want := baseline + 2*(ascii-7) + 2*(controls*6-6) + 2*(256-7)
			if want != MaxRecoveryBytes {
				continue
			}
			ctx := LocalContext{strings.Repeat("\x00", controls), strings.Repeat("x", 256)}
			if controls == 0 {
				continue
			}
			m := map[string]any{"contract": Contract, "operation": "status", "expected_request_digest": strings.Repeat("1", 64)}
			m["caller"] = ctx.Caller
			m["project"] = ctx.Project
			m["correlation_id"] = strings.Repeat("z", ascii)
			r := mustParse(t, frame(t, m), ctx)
			if len(r.RecoveryTemplate()) != want {
				t.Fatalf("boundary length %d != %d", len(r.RecoveryTemplate()), want)
			}
			m["correlation_id"] = strings.Repeat("z", ascii+1)
			reject(t, frame(t, m), ctx, "output_budget")
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no exact boundary constructed, baseline=%d", baseline)
	}
}
