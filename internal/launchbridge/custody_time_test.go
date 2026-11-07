package launchbridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Both timestamps are in the reader's past. Only their relative ordering may
// refuse custody; the same-second control preserves CLI timestamp precision.
// All source and backend observations use the existing inert test doubles.
func TestCustodyEventTimeOrdering(t *testing.T) {
	for _, tc := range []struct {
		name    string
		offset  time.Duration
		refused bool
	}{
		{"later-second", time.Second, true},
		{"before-event", -time.Second, false},
		{"at-event", 0, false},
		{"same-second", 999 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, i, c, events, now := fixture(t)
			at := now.Add(-2 * time.Second)
			checked := at.Add(tc.offset)
			c.CheckedAt = checked.Format(time.RFC3339Nano)
			if !at.Before(now) || !checked.Before(now) {
				t.Fatal("fixture must keep event and custody before reader time")
			}
			if err := c.validate(i, p, now); err != nil {
				t.Fatalf("standalone custody must remain valid: %v", err)
			}
			setEvent(t, events, p, c, at)
			var envelope map[string]any
			// Render event time to whole seconds, as permitted by the CLI contract.
			if err := json.Unmarshal(events.event, &envelope); err != nil {
				t.Fatal(err)
			}
			envelope["at"] = at.Format(time.RFC3339)
			events.event = encode(t, envelope)
			got, err := (CLIEvents{events, "/workspace"}).read(context.Background(), p.Event, now)
			if tc.refused {
				if err != Refusal("custody_time_invalid") || !reflect.DeepEqual(got, Custody{}) {
					t.Errorf("later-second custody: got %+v, %v; want empty custody and custody_time_invalid", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got, c) {
				t.Errorf("valid event-time boundary: got %+v, %v; want exact custody", got, err)
			}
			wantCalls := [][]string{
				{"/workspace", "a2a", "room", "members", p.Event.RoomID, "--membership", "join", "--json"},
				{"/workspace", "a2a", "read", p.Event.RoomID, "--event", p.Event.EventID, "--json"},
			}
			if !reflect.DeepEqual(events.calls, wantCalls) {
				t.Fatalf("unexpected source reads: %v", events.calls)
			}

			b, o := &backend{}, &owner{}
			router, err := Assemble(p, CLIEvents{events, "/workspace"}, b, o, nil, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result := router.Start(context.Background(), i)
			files, err := filepath.Glob(filepath.Join(p.JournalDir, "launch-*.json"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.refused {
				if result.Status != "refused" || result.Reason != "admission_invalid" || o.calls != 0 || b.creates != 0 || len(files) != 0 {
					t.Errorf("later-second custody reached launch: result=%+v owner=%d creates=%d receipts=%v", result, o.calls, b.creates, files)
				}
			} else if result.Status != "unknown" || o.calls != 1 || b.creates != 1 || len(files) != 1 {
				t.Errorf("valid boundary did not reach one inert launch attempt: result=%+v owner=%d creates=%d receipts=%v", result, o.calls, b.creates, files)
			}
		})
	}
}
