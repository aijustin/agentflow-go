package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func planAllowedTools(e *Engine, agent core.Agent) map[string]struct{} {
	if !e.scenario.Orchestration.Planning.Enabled || !e.scenario.Orchestration.Planning.Execute {
		return nil
	}
	profile := e.scenario.LLMs[agent.LLM]
	if !profile.Context.ToolSchemaPruning {
		return nil
	}
	allowed := make(map[string]struct{})
	for _, name := range agent.Tools {
		allowed[name] = struct{}{}
	}
	return allowed
}

func (e *Engine) planningToolHint(ctx context.Context, runID string) string {
	if !e.scenario.Orchestration.Planning.Enabled || !e.scenario.Orchestration.Planning.Execute {
		return ""
	}
	snapshot, err := e.runs.Load(ctx, runID)
	if err != nil {
		return ""
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok || len(ref.Inline) == 0 {
		return ""
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		return ""
	}
	for _, step := range state.Steps {
		if step.Status != "pending" {
			continue
		}
		if step.Tool != "" {
			return fmt.Sprintf("Next planned step prefers tool %q: %s", step.Tool, step.Goal)
		}
		return "Next planned step: " + step.Goal
	}
	return ""
}

func (e *Engine) planningComplete(ctx context.Context, runID string) bool {
	snapshot, err := e.runs.Load(ctx, runID)
	if err != nil {
		return true
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok || len(ref.Inline) == 0 {
		return true
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		return true
	}
	for _, step := range state.Steps {
		if step.Status == "pending" {
			return false
		}
	}
	return true
}

func (e *Engine) maybeReplan(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req RunRequest, messages []llm.Message) ([]llm.Message, error) {
	planning := e.scenario.Orchestration.Planning
	if !planning.Enabled || !planning.Execute || !planning.ReplanOnFailure || e.planningComplete(ctx, runID) {
		return messages, nil
	}
	replanned, err := e.injectAutonomousPlan(ctx, runID, agent, profile, req, messages)
	if err != nil {
		return messages, err
	}
	return replanned, nil
}

func appendPlanningHint(messages []llm.Message, hint string) []llm.Message {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return messages
	}
	out := make([]llm.Message, 0, len(messages)+1)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: hint})
	out = append(out, messages...)
	return out
}
