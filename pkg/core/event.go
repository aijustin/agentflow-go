package core

import (
	"context"
	"encoding/json"
	"time"
)

type EventType string

const (
	EventRunStarted       EventType = "RunStarted"
	EventRunCompleted     EventType = "RunCompleted"
	EventRunFailed        EventType = "RunFailed"
	EventRunPaused        EventType = "RunPaused"
	EventRunResumed       EventType = "RunResumed"
	EventStepStarted      EventType = "StepStarted"
	EventStepCompleted    EventType = "StepCompleted"
	EventStepFailed       EventType = "StepFailed"
	EventSubgraphStarted  EventType = "SubgraphStarted"
	EventSubgraphCompleted EventType = "SubgraphCompleted"
	EventToolCalled       EventType = "ToolCalled"
	EventToolReturned     EventType = "ToolReturned"
	EventToolDenied       EventType = "ToolDenied"
	EventLLMCalled        EventType = "LLMCalled"
	EventLLMReturned      EventType = "LLMReturned"
	EventLLMTokenUsage    EventType = "LLMTokenUsage"
	EventHumanGateOpened  EventType = "HumanGateOpened"
	EventHumanGateDecided EventType = "HumanGateDecided"
	EventHumanGateExpired EventType = "HumanGateExpired"
	EventMemoryRead       EventType = "MemoryRead"
	EventMemoryWrite      EventType = "MemoryWrite"
	EventMemoryPromoted   EventType = "MemoryPromoted"
	EventMemoryDemoted    EventType = "MemoryDemoted"
	EventMemoryEvicted    EventType = "MemoryEvicted"
	EventContextPrepared  EventType = "ContextPrepared"
)

type Event struct {
	Type         EventType       `json:"type"`
	RunID        string          `json:"run_id"`
	ScenarioName string          `json:"scenario_name,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
	TraceID      string          `json:"trace_id,omitempty"`
	SpanID       string          `json:"span_id,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

type EventSink interface {
	Emit(ctx context.Context, event Event) error
}

type EventSinkFunc func(ctx context.Context, event Event) error

func (f EventSinkFunc) Emit(ctx context.Context, event Event) error {
	return f(ctx, event)
}
