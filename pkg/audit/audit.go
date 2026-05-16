package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type EventType string

const (
	EventRunSubmitted EventType = "run.submitted"
	EventRunCancelled EventType = "run.cancelled"
	EventHITLDecided  EventType = "hitl.decided"
	EventToolInvoked  EventType = "tool.invoked"
	EventPolicyDenied EventType = "policy.denied"
)

type Event struct {
	ID        string             `json:"id,omitempty"`
	Type      EventType          `json:"type"`
	Timestamp time.Time          `json:"timestamp"`
	Principal identity.Principal `json:"principal"`
	Action    security.Action    `json:"action,omitempty"`
	Resource  security.Resource  `json:"resource"`
	RunID     string             `json:"run_id,omitempty"`
	Outcome   string             `json:"outcome,omitempty"`
	Reason    string             `json:"reason,omitempty"`
	Metadata  map[string]string  `json:"metadata,omitempty"`
	Payload   json.RawMessage    `json:"payload,omitempty"`
}

func (event Event) WithDefaults(now time.Time) Event {
	if event.Timestamp.IsZero() {
		event.Timestamp = now.UTC()
	}
	return event
}

type Sink interface {
	Record(ctx context.Context, event Event) error
}

type SinkFunc func(ctx context.Context, event Event) error

func (fn SinkFunc) Record(ctx context.Context, event Event) error {
	return fn(ctx, event)
}

func NoopSink() Sink {
	return SinkFunc(func(ctx context.Context, event Event) error { return ctx.Err() })
}

func CloneEvent(event Event) Event {
	if event.Principal.Roles != nil {
		roles := make([]identity.Role, len(event.Principal.Roles))
		copy(roles, event.Principal.Roles)
		event.Principal.Roles = roles
	}
	if event.Principal.Metadata != nil {
		metadata := make(map[string]string, len(event.Principal.Metadata))
		for key, value := range event.Principal.Metadata {
			metadata[key] = value
		}
		event.Principal.Metadata = metadata
	}
	if event.Resource.Metadata != nil {
		metadata := make(map[string]string, len(event.Resource.Metadata))
		for key, value := range event.Resource.Metadata {
			metadata[key] = value
		}
		event.Resource.Metadata = metadata
	}
	if event.Metadata != nil {
		metadata := make(map[string]string, len(event.Metadata))
		for key, value := range event.Metadata {
			metadata[key] = value
		}
		event.Metadata = metadata
	}
	if event.Payload != nil {
		payload := make([]byte, len(event.Payload))
		copy(payload, event.Payload)
		event.Payload = payload
	}
	return event
}
