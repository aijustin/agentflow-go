package agentflow

import (
	"context"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/eventrouter"
)

// EventRouter maps external events to run requests for a scenario.
type EventRouter = eventrouter.Router

// IncomingEvent is an external trigger delivered through webhooks or CLI.
type IncomingEvent = eventrouter.Event

// NewEventRouter creates a router from scenario trigger definitions.
func NewEventRouter(scenario core.Scenario) *EventRouter {
	return eventrouter.NewRouter(scenario)
}

// HandleEvent resolves an incoming event and executes the scenario. The
// event type is carried into the run as its trigger kind
// ("event:<type>") so lifecycle events and metrics attribute the run to the
// external trigger that started it.
func (f *Framework) HandleEvent(ctx context.Context, event IncomingEvent) (RunResult, error) {
	router := eventrouter.NewRouter(f.currentScenario())
	req, err := router.Resolve(event)
	if err != nil {
		return RunResult{}, err
	}
	return f.Run(ctx, RunRequest{
		RunID:       req.RunID,
		Agent:       req.Agent,
		Prompt:      req.Prompt,
		Context:     req.Context,
		TriggerKind: eventTriggerKind(event.Type),
	})
}

// eventTriggerKind derives the run trigger kind from the external event type
// so webhook/CLI-triggered runs are distinguishable from direct user runs.
func eventTriggerKind(eventType string) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return ""
	}
	return "event:" + eventType
}

// ResolveEvent resolves an incoming event without executing it.
func (f *Framework) ResolveEvent(event IncomingEvent) (RunRequest, error) {
	resolved, err := eventrouter.NewRouter(f.currentScenario()).Resolve(event)
	if err != nil {
		return RunRequest{}, err
	}
	return RunRequest{
		RunID:       resolved.RunID,
		Agent:       resolved.Agent,
		Prompt:      resolved.Prompt,
		Context:     resolved.Context,
		TriggerKind: eventTriggerKind(event.Type),
	}, nil
}
