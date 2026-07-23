package eventrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// Classified resolution errors so HTTP adapters can distinguish a malformed
// event (400) from an unroutable one (404) without string matching.
var (
	ErrEventTypeRequired = errors.New("eventrouter: event type is required")
	ErrNoTrigger         = errors.New("eventrouter: no trigger configured for event")
)

// Router maps external events to run requests using scenario trigger definitions.
type Router struct {
	scenario core.Scenario
}

func NewRouter(scenario core.Scenario) *Router {
	return &Router{scenario: scenario}
}

func (r *Router) Resolve(event Event) (RunRequest, error) {
	eventType := strings.TrimSpace(event.Type)
	if eventType == "" {
		return RunRequest{}, ErrEventTypeRequired
	}
	trigger, ok := r.matchTrigger(eventType)
	if !ok {
		return RunRequest{}, fmt.Errorf("%w %q", ErrNoTrigger, eventType)
	}
	if trigger.Agent != "" {
		if _, ok := r.scenario.Agents[trigger.Agent]; !ok {
			return RunRequest{}, fmt.Errorf("eventrouter: trigger agent %q is not declared", trigger.Agent)
		}
	}
	runID := strings.TrimSpace(event.RunID)
	if runID == "" && trigger.RunIDPath != "" {
		value, ok, err := stringAtPath(event.Payload, trigger.RunIDPath)
		if err != nil {
			return RunRequest{}, err
		}
		if ok {
			runID = value
		}
	}
	if runID == "" {
		runID = generateRunID()
	}
	prompt := strings.TrimSpace(trigger.DefaultPrompt)
	if trigger.PromptPath != "" {
		value, ok, err := stringAtPath(event.Payload, trigger.PromptPath)
		if err != nil {
			return RunRequest{}, err
		}
		if ok {
			prompt = value
		}
	}
	if prompt == "" {
		prompt = defaultPromptFromPayload(event.Payload)
	}
	var context json.RawMessage
	switch {
	case trigger.ContextPath != "":
		value, ok, err := rawAtPath(event.Payload, trigger.ContextPath)
		if err != nil {
			return RunRequest{}, err
		}
		if ok {
			context = value
		}
	case len(event.Payload) > 0:
		context = append(json.RawMessage(nil), event.Payload...)
	}
	return RunRequest{
		RunID:   runID,
		Agent:   trigger.Agent,
		Prompt:  prompt,
		Context: context,
	}, nil
}

func (r *Router) matchTrigger(eventType string) (core.Trigger, bool) {
	for _, trigger := range r.scenario.Triggers {
		if strings.TrimSpace(trigger.Event) == eventType {
			return trigger, true
		}
	}
	return core.Trigger{}, false
}

func defaultPromptFromPayload(payload json.RawMessage) string {
	for _, path := range []string{"prompt", "message", "summary", "title", "body.summary", "body.message"} {
		value, ok, err := stringAtPath(payload, path)
		if err == nil && ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// generateRunID delegates to the canonical 128-bit generator in runstate so
// the event router, framework facade, engine, and async adapter share one
// implementation instead of carrying private 64-bit copies.
func generateRunID() string {
	return runstate.GenerateRunID()
}
