package audit

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestCloneEventDefensivelyCopiesMutableFields(t *testing.T) {
	event := Event{
		Principal: identity.Principal{Roles: []identity.Role{identity.RoleService}, Metadata: map[string]string{"team": "platform"}},
		Metadata:  map[string]string{"request_id": "req-1"},
		Payload:   []byte(`{"secret":"redacted"}`),
	}
	clone := CloneEvent(event)
	clone.Principal.Roles[0] = identity.RoleAdmin
	clone.Principal.Metadata["team"] = "other"
	clone.Metadata["request_id"] = "req-2"
	clone.Payload[0] = '['
	if event.Principal.Roles[0] != identity.RoleService || event.Principal.Metadata["team"] != "platform" || event.Metadata["request_id"] != "req-1" || event.Payload[0] != '{' {
		t.Fatalf("clone mutation leaked into source: %+v", event)
	}
}

func TestNoopSinkRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NoopSink().Record(ctx, Event{}); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestEventWithDefaultsSetsTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	event := (Event{}).WithDefaults(now)
	if !event.Timestamp.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, event.Timestamp)
	}
}

func TestCloneEventCopiesResourceMetadata(t *testing.T) {
	event := Event{
		Resource: security.Resource{Metadata: map[string]string{"tenant": "t1"}},
	}
	clone := CloneEvent(event)
	clone.Resource.Metadata["tenant"] = "t2"
	if event.Resource.Metadata["tenant"] != "t1" {
		t.Fatal("clone should not alias resource metadata")
	}
}

func TestSinkFuncRecords(t *testing.T) {
	var recorded bool
	sink := SinkFunc(func(context.Context, Event) error {
		recorded = true
		return nil
	})
	if err := sink.Record(context.Background(), Event{Type: EventRunSubmitted}); err != nil || !recorded {
		t.Fatalf("expected sink func to record, recorded=%v", recorded)
	}
}
