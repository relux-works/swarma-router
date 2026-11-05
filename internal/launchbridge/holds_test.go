package launchbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Explicit [] is the positive authority control. Missing and null holds are
// unavailable observations and must cause no Create through the assembled path.
func TestExplicitHolds(t *testing.T) {
	for _, form := range []string{"empty", "missing", "null"} {
		t.Run(form, func(t *testing.T) {
			p, i, _, r, now := fixture(t)
			var envelope, body map[string]any
			if err := json.Unmarshal(r.event, &envelope); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(envelope["body"].(string)), &body); err != nil {
				t.Fatal(err)
			}
			if form == "missing" {
				delete(body, "holds")
			}
			if form == "null" {
				body["holds"] = nil
			}
			envelope["body"] = string(encode(t, body))
			r.event = encode(t, envelope)
			b := &backend{}
			router, err := Assemble(p, CLIEvents{r, "/workspace"}, b, &owner{}, nil, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result := router.Start(context.Background(), i)
			if form == "empty" {
				if result.Status != "unknown" || b.creates != 1 {
					t.Fatalf("valid holds: %+v creates=%d", result, b.creates)
				}
			} else if result.Status != "refused" || result.Reason != "admission_invalid" || b.creates != 0 {
				t.Fatalf("unavailable holds %s: %+v creates=%d", form, result, b.creates)
			}
		})
	}
}
