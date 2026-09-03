package tesco

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestSlotsDecodeNumericGroup is the regression guard for a bug the live suite
// could not catch. `delivery.group` is a number; it was declared as a string,
// and every `tescoctl slots` call died at the decoding step.
//
// The live tests validate that a field exists on a type, which `group` always
// did — the drift was in the Go type, not the query. Only decoding a real
// response shape reveals it, so that is what this does.
func TestSlotsDecodeNumericGroup(t *testing.T) {
	const reply = `[{"data":{"delivery":[{` +
		`"id":"REVMSVZFUllfVkFO","start":"2026-09-03T12:00:00Z","end":"2026-09-03T13:00:00Z",` +
		`"charge":9,"status":"Available","group":1,` +
		`"price":{"beforeDiscount":9,"afterDiscount":9},"locationUuid":"loc-1"}]}}]`

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, reply)
	})

	slots, err := c.Slots(context.Background(), time.Now(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("want 1 slot, got %d", len(slots))
	}
	if slots[0].Group != 1 {
		t.Errorf("group = %d, want 1", slots[0].Group)
	}
	if slots[0].Status != "Available" || slots[0].ID == "" {
		t.Errorf("slot decoded wrong: %+v", slots[0])
	}
}
