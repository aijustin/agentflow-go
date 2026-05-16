package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
)

func TestSinkRecordsAndBoundsEvents(t *testing.T) {
	sink := NewSink(1)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	sink.now = func() time.Time { return now }
	if err := sink.Record(context.Background(), audit.Event{ID: "one", Type: audit.EventRunSubmitted}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Record(context.Background(), audit.Event{ID: "two", Type: audit.EventPolicyDenied}); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) != 1 || events[0].ID != "two" || !events[0].Timestamp.Equal(now) {
		t.Fatalf("unexpected events: %+v", events)
	}
}
