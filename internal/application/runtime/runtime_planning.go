package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
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
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
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
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
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
	if len(messages) > 0 && messages[0].Role == llm.RoleSystem && messages[0].Content == hint {
		return messages
	}
	out := make([]llm.Message, 0, len(messages)+1)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: hint})
	out = append(out, messages...)
	return out
}

func (e *Engine) injectAutonomousPlan(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req RunRequest, messages []llm.Message) ([]llm.Message, error) {
	plannerAgent := agent
	if planner := e.scenario.Orchestration.Planning.Agent; planner != "" {
		resolved, err := e.resolveAgent(planner)
		if err != nil {
			return nil, err
		}
		plannerAgent = resolved
		var profileErr error
		profile, profileErr = e.llmProfile(plannerAgent.LLM)
		if profileErr != nil {
			return nil, profileErr
		}
	}
	maxSteps := firstPositive(e.scenario.Orchestration.Planning.MaxSteps, agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 5)
	planReq := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: fmt.Sprintf("Create a concise execution plan with at most %d steps. Return JSON with a steps array; each step has goal and optional tool.", maxSteps)},
			{Role: llm.RoleUser, Content: plannerUserContent(req)},
		},
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	var rawPlan []byte
	if outputter, ok := e.llm.(llm.StructuredOutputter); ok && e.llm.Supports(plannerAgent.LLM, llm.CapStructuredOutput) {
		raw, err := e.structuredWithRetry(ctx, runID, plannerAgent, profile, autonomousPlanSchema, planReq, outputter)
		if err != nil {
			return nil, err
		}
		rawPlan = raw
	} else {
		resp, err := e.chatWithRetry(ctx, runID, plannerAgent, profile, planReq)
		if err != nil {
			return nil, err
		}
		rawPlan = []byte(resp.Message.Content)
	}
	planText := formatAutonomousPlan(rawPlan, maxSteps)
	if e.scenario.Orchestration.Planning.Execute {
		if err := e.persistPlan(ctx, runID, rawPlan, maxSteps); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(planText) == "" {
		return messages, nil
	}
	// A replan injects a fresh plan message; drop any earlier plan system
	// message from history first so the model never sees two (possibly
	// conflicting) "Autonomous execution plan" messages at once.
	filtered := stripPriorPlanSystemMessages(messages)
	planned := make([]llm.Message, 0, len(filtered)+1)
	planned = append(planned, llm.Message{Role: llm.RoleSystem, Content: planSystemMessagePrefix + planText})
	planned = append(planned, filtered...)
	e.emitJSON(ctx, core.EventContextPrepared, runID, map[string]any{"planning": true, "steps": strings.Count(planText, "\n") + 1})
	return planned, nil
}

const planSystemMessagePrefix = "Autonomous execution plan:\n"

func stripPriorPlanSystemMessages(messages []llm.Message) []llm.Message {
	filtered := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llm.RoleSystem && strings.HasPrefix(msg.Content, planSystemMessagePrefix) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func (e *Engine) planningEnabled() bool {
	planning := e.scenario.Orchestration.Planning
	if !planning.Enabled {
		return false
	}
	if e.scenario.Orchestration.Mode != core.OrchestrationHybrid {
		return true
	}
	if e.scenario.Orchestration.Workflow == nil {
		return true
	}
	return planning.AfterWorkflow
}

func plannerUserContent(req RunRequest) string {
	prompt := strings.TrimSpace(req.Prompt)
	if len(req.Context) == 0 {
		return prompt
	}
	if prompt == "" {
		return "Workflow context:\n" + string(req.Context)
	}
	return prompt + "\n\nWorkflow context:\n" + string(req.Context)
}

func formatAutonomousPlan(raw []byte, maxSteps int) string {
	var plan autonomousPlan
	if err := json.Unmarshal(raw, &plan); err != nil || len(plan.Steps) == 0 {
		return strings.TrimSpace(string(raw))
	}
	limit := len(plan.Steps)
	if maxSteps > 0 && limit > maxSteps {
		limit = maxSteps
	}
	var b strings.Builder
	for index := 0; index < limit; index++ {
		step := plan.Steps[index]
		goal := strings.TrimSpace(step.Goal)
		if goal == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(index + 1))
		b.WriteString(". ")
		b.WriteString(goal)
		if step.Tool != "" {
			b.WriteString(" (tool: ")
			b.WriteString(step.Tool)
			b.WriteByte(')')
		}
	}
	return b.String()
}

var autonomousPlanSchema = json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","items":{"type":"object","properties":{"goal":{"type":"string"},"tool":{"type":"string"}},"required":["goal"]}}},"required":["steps"]}`)

type autonomousPlan struct {
	Steps []autonomousPlanStep `json:"steps"`
}

type autonomousPlanStep struct {
	Goal string `json:"goal"`
	Tool string `json:"tool,omitempty"`
}
