package audit

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/identity"
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
