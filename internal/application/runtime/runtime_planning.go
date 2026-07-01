package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func planAllowedTools(ctx context.Context, e *Engine, runID string, agent core.Agent) map[string]struct{} {
	if !e.scenario.Orchestration.Planning.Enabled || !e.scenario.Orchestration.Planning.Execute {
		return nil
	}
	profile := e.scenario.LLMs[agent.LLM]
	if !profile.Context.ToolSchemaPruning {
		return nil
	}
	// Restrict the exposed schema to just the tool named by the next
	// pending plan step (mirrors planningToolHint's own read of the
	// persisted plan). Without this, pruning always fell back to "every
	// tool the agent has" - identical to no pruning - because it never
	// looked at plan progress at all. If the plan cannot be read or the
	// next step names no specific tool, do not prune: allowing the full
	// set is always safe, whereas guessing wrong would block a legitimate
	// call.
	nextTool := nextPlannedToolName(ctx, e, runID)
	if nextTool == "" {
		return nil
	}
	allowed := map[string]struct{}{nextTool: {}}
	// Sub-agent delegation is exposed to the LLM as a synthetic tool spec
	// (see toolSpecs), not as an entry in agent.Tools, so it must be added
	// here explicitly. Otherwise schema pruning would strip every
	// delegate-to-sub-agent tool whenever it's enabled, silently taking
	// away the agent's ability to delegate while planning is active.
	for _, name := range agent.SubAgents {
		allowed[delegateToolName(name)] = struct{}{}
	}
	return allowed
}

// nextPlannedToolName returns the tool name of the first pending step in
// the persisted plan, or "" if there is no persisted plan, it cannot be
// read, or that step does not name a specific tool.
func nextPlannedToolName(ctx context.Context, e *Engine, runID string) string {
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
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
		return step.Tool
	}
	return ""
}

func (e *Engine) planningToolHint(ctx context.Context, runID string) string {
	if !e.scenario.Orchestration.Planning.Enabled || !e.scenario.Orchestration.Planning.Execute {
		return ""
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
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

// planningComplete reports whether every step of the persisted plan is done.
// A load or decode failure is surfaced as an error rather than silently
// treated as "complete": doing the latter would suppress replanning (or
// incorrectly stop the loop) for a run whose plan state simply could not be
// read, masking the real underlying problem behind a generic max-steps
// error.
func (e *Engine) planningComplete(ctx context.Context, runID string) (bool, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return false, err
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok || len(ref.Inline) == 0 {
		return true, nil
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		return false, fmt.Errorf("runtime: decode plan execution state for run %q: %w", runID, err)
	}
	for _, step := range state.Steps {
		if step.Status == "pending" {
			return false, nil
		}
	}
	return true, nil
}

func (e *Engine) maybeReplan(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req RunRequest, messages []llm.Message) ([]llm.Message, error) {
	planning := e.scenario.Orchestration.Planning
	if !planning.Enabled || !planning.Execute || !planning.ReplanOnFailure {
		return messages, nil
	}
	complete, err := e.planningComplete(ctx, runID)
	if err != nil {
		return messages, err
	}
	if complete {
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
