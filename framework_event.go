package agentflow

import (
	"context"

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

// HandleEvent resolves an incoming event and executes the scenario.
func (f *Framework) HandleEvent(ctx context.Context, event IncomingEvent) (RunResult, error) {
	router := eventrouter.NewRouter(f.scenario)
	req, err := router.Resolve(event)
	if err != nil {
		return RunResult{}, err
	}
	return f.Run(ctx, RunRequest{
		RunID:   req.RunID,
		Agent:   req.Agent,
		Prompt:  req.Prompt,
		Context: req.Context,
	})
}

// ResolveEvent resolves an incoming event without executing it.
func (f *Framework) ResolveEvent(event IncomingEvent) (RunRequest, error) {
	resolved, err := eventrouter.NewRouter(f.scenario).Resolve(event)
	if err != nil {
		return RunRequest{}, err
	}
	return RunRequest{
		RunID:   resolved.RunID,
		Agent:   resolved.Agent,
		Prompt:  resolved.Prompt,
		Context: resolved.Context,
	}, nil
}
