package eventrouter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
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
		return RunRequest{}, fmt.Errorf("eventrouter: event type is required")
	}
	trigger, ok := r.matchTrigger(eventType)
	if !ok {
		return RunRequest{}, fmt.Errorf("eventrouter: no trigger configured for event %q", eventType)
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

func generateRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
