package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const (
	checkpointKindVar            = "checkpoint_kind"
	checkpointPromptVar          = "checkpoint_prompt"
	checkpointAgentVar           = "checkpoint_agent"
	checkpointContextVar         = "checkpoint_context"
	checkpointResumedVar         = "checkpoint_resumed"
	checkpointToolCallsVar       = "checkpoint_tool_calls"
	checkpointMessagesVar        = "checkpoint_messages"
	checkpointToolCountsVar      = "checkpoint_tool_counts"
	checkpointOutputModeVar      = "checkpoint_output_mode"
	checkpointStepsConsumedVar   = "checkpoint_steps_consumed"
	humanAmendmentVar            = "human_amendment"
)

// RunPausedError indicates the run paused and requires human approval before continuing.
type RunPausedError struct {
	RunID string
	Token string
	Kind  string
}

func (e RunPausedError) Error() string {
	return fmt.Sprintf("runtime: run %q paused (%s)", e.RunID, e.Kind)
}

// ContinueAfterCheckpoint resumes execution for a run that was approved at a checkpoint.
func (e *Engine) ContinueAfterCheckpoint(ctx context.Context, runID string) (RunResult, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return RunResult{}, fmt.Errorf("runtime: continue requires running snapshot, got %s", snapshot.Status)
	}
	kind := variableString(snapshot.Variables, checkpointKindVar)
	switch kind {
	case "before_final_answer":
		return e.continueBeforeFinalAnswer(ctx, snapshot)
	case "tool_approval":
		return e.continueToolApproval(ctx, snapshot)
	default:
		return RunResult{}, fmt.Errorf("runtime: unknown checkpoint kind %q", kind)
	}
}

func (e *Engine) continueBeforeFinalAnswer(ctx context.Context, snapshot runstate.RunSnapshot) (RunResult, error) {
	prompt := applyHumanAmendment(snapshot.Variables, variableString(snapshot.Variables, checkpointPromptVar))
	req := RunRequest{
		RunID:   snapshot.RunID,
		Agent:   variableString(snapshot.Variables, checkpointAgentVar),
		Prompt:  prompt,
		Context: snapshot.Variables[checkpointContextVar],
	}
	if err := e.markCheckpointResumed(ctx, &snapshot); err != nil {
		return RunResult{}, err
	}
	if variableString(snapshot.Variables, checkpointOutputModeVar) == "structured" {
		raw, err := e.structuredAnswer(ctx, req)
		if err != nil {
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		return e.completeStructuredRun(ctx, req.RunID, raw)
	}
	output, err := e.answer(ctx, req)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	return e.completeRun(ctx, req.RunID, output)
}

func (e *Engine) continueToolApproval(ctx context.Context, snapshot runstate.RunSnapshot) (RunResult, error) {
	agentName := variableString(snapshot.Variables, checkpointAgentVar)
	agent, err := e.resolveAgent(agentName)
	if err != nil {
		return RunResult{}, err
	}
	var pending []llm.ToolCall
	if raw := snapshot.Variables[checkpointToolCallsVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &pending); err != nil {
			return RunResult{}, fmt.Errorf("runtime: decode checkpoint tool calls: %w", err)
		}
	}
	var messages []llm.Message
	if raw := snapshot.Variables[checkpointMessagesVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &messages); err != nil {
			return RunResult{}, fmt.Errorf("runtime: decode checkpoint messages: %w", err)
		}
	}
	toolCounts := map[string]int{}
	if raw := snapshot.Variables[checkpointToolCountsVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &toolCounts); err != nil {
			return RunResult{}, fmt.Errorf("runtime: decode checkpoint tool counts: %w", err)
		}
	}
	prompt := applyHumanAmendment(snapshot.Variables, variableString(snapshot.Variables, checkpointPromptVar))
	messages = injectHumanAmendmentMessages(messages, snapshot.Variables)
	if err := e.markCheckpointResumed(ctx, &snapshot); err != nil {
		return RunResult{}, err
	}
	profile := e.scenario.LLMs[agent.LLM]
	caller, ok := e.llm.(llm.ToolCaller)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
		return RunResult{}, fmt.Errorf("runtime: llm profile %q does not support tool calling", agent.LLM)
	}
	stepsConsumed := checkpointStepsConsumed(snapshot.Variables)
	output, err := e.continueToolLoopFrom(ctx, snapshot.RunID, agent, profile, messages, pending, toolCounts, caller, prompt, stepsConsumed)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		e.markRunFailed(ctx, snapshot.RunID, err)
		return RunResult{}, err
	}
	return e.completeRun(ctx, snapshot.RunID, output)
}

func (e *Engine) continueToolLoopFrom(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, messages []llm.Message, pending []llm.ToolCall, toolCounts map[string]int, caller llm.ToolCaller, prompt string, stepsConsumed int) (string, error) {
	if len(pending) > 0 {
		turnStart := lastAssistantWithToolCallsIndex(messages)
		// pending[0] is the tool call the human just approved; execute it
		// directly. The remaining calls from the same assistant turn still go
		// through the normal approval/dispatch path and may pause again.
		approved := pending[0]
		result, err := e.dispatchApprovedTool(ctx, runID, agent, approved, toolCounts)
		if err != nil {
			return "", err
		}
		contextResult := compactToolResultForContext(result, profile.Context.ToolResultMaxTokens)
		raw, err := json.Marshal(contextResult)
		if err != nil {
			return "", err
		}
		messages = append(messages, llm.Message{
			Role:       llm.RoleTool,
			Content:    string(raw),
			Name:       approved.Name,
			ToolCallID: approved.ID,
		})
		if e.scenario.Orchestration.Planning.Enabled && e.scenario.Orchestration.Planning.Execute && result.Error == "" {
			_ = e.markPlanStepDone(ctx, runID, approved.Name)
		}
		messages, err = e.dispatchToolCalls(ctx, runID, agent, profile, llm.Message{}, pending[1:], messages, toolCounts, prompt, false, stepsConsumed)
		if err != nil {
			return "", err
		}
		if turnStart >= 0 {
			if err := e.persistToolTurnFromStepOutputs(ctx, runID, agent, messages[turnStart]); err != nil {
				return "", err
			}
		}
	}
	maxSteps := firstPositive(agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 8)
	remainingSteps := maxSteps - stepsConsumed
	if remainingSteps <= 0 {
		return "", fmt.Errorf("runtime: autonomous tool loop exceeded max_steps=%d", maxSteps)
	}
	toolSpecs := e.toolSpecs(agent)
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	return e.answerWithToolsFrom(ctx, runID, agent, profile, baseReq, caller, toolSpecs, messages, toolCounts, remainingSteps, prompt, 0, stepsConsumed)
}

func (e *Engine) completeRun(ctx context.Context, runID, output string) (RunResult, error) {
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRaw := []byte(fmt.Sprintf(`{"text":%q}`, output))
	finalRef, err := e.stepOutputRef(ctx, runID, "final", finalRaw)
	if err != nil {
		e.markRunFailed(ctx, runID, err)
		return RunResult{}, err
	}
	if loaded.StepOutputs == nil {
		loaded.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	e.emit(ctx, core.EventRunCompleted, runID, finalRef.Inline)
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

func applyHumanAmendment(vars map[string]json.RawMessage, prompt string) string {
	if vars == nil {
		return prompt
	}
	raw, ok := vars[humanAmendmentVar]
	if !ok || len(raw) == 0 {
		return prompt
	}
	amendment := decodeAmendmentText(raw)
	if amendment == "" {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return amendment
	}
	return prompt + "\n\nHuman feedback: " + amendment
}

func injectHumanAmendmentMessages(messages []llm.Message, vars map[string]json.RawMessage) []llm.Message {
	if vars == nil {
		return messages
	}
	raw, ok := vars[humanAmendmentVar]
	if !ok || len(raw) == 0 {
		return messages
	}
	amendment := decodeAmendmentText(raw)
	if amendment == "" {
		return messages
	}
	out := append([]llm.Message(nil), messages...)
	out = append(out, llm.Message{Role: llm.RoleUser, Content: "Human feedback: " + amendment})
	return out
}

func decodeAmendmentText(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return strings.TrimSpace(value)
}

func clearHumanAmendment(snapshot *runstate.RunSnapshot) {
	if snapshot == nil || snapshot.Variables == nil {
		return
	}
	delete(snapshot.Variables, humanAmendmentVar)
}

func checkpointStepsConsumed(vars map[string]json.RawMessage) int {
	if vars == nil {
		return 0
	}
	raw, ok := vars[checkpointStepsConsumedVar]
	if !ok || len(raw) == 0 {
		return 0
	}
	var consumed int
	if err := json.Unmarshal(raw, &consumed); err != nil {
		return 0
	}
	return consumed
}

func variableString(vars map[string]json.RawMessage, key string) string {
	if vars == nil {
		return ""
	}
	raw, ok := vars[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return value
}

func (e *Engine) isCheckpointResumed(snapshot runstate.RunSnapshot) bool {
	raw, ok := snapshot.Variables[checkpointResumedVar]
	if !ok || len(raw) == 0 {
		return false
	}
	var resumed bool
	if err := json.Unmarshal(raw, &resumed); err != nil {
		return false
	}
	return resumed
}

func errorsAsRunPaused(err error, target *RunPausedError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

func (e *Engine) maybePauseToolCall(ctx context.Context, runID string, agent core.Agent, pending []llm.ToolCall, messages []llm.Message, toolCounts map[string]int, prompt string, stepsConsumed int) (*RunPausedError, error) {
	if len(pending) == 0 {
		return nil, nil
	}
	call := pending[0]
	tool, ok := e.scenario.Tools[call.Name]
	if !ok || !approvalPauseRequired(tool) {
		return nil, nil
	}
	if e.gate == nil {
		return nil, nil
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return nil, err
	}
	// Persist every still-pending call from this assistant turn (the one
	// awaiting approval plus any that follow it) so resume executes all of
	// them and never leaves orphaned tool_call IDs without a tool response.
	toolCallsRaw, err := json.Marshal(pending)
	if err != nil {
		return nil, err
	}
	messagesRaw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	countsRaw, err := json.Marshal(toolCounts)
	if err != nil {
		return nil, err
	}
	vars := map[string]json.RawMessage{
		checkpointKindVar:            json.RawMessage(`"tool_approval"`),
		checkpointAgentVar:           json.RawMessage(fmt.Sprintf("%q", agent.Name)),
		checkpointPromptVar:          json.RawMessage(fmt.Sprintf("%q", prompt)),
		checkpointToolCallsVar:       toolCallsRaw,
		checkpointMessagesVar:        messagesRaw,
		checkpointToolCountsVar:      countsRaw,
		checkpointStepsConsumedVar:     json.RawMessage(fmt.Sprintf("%d", stepsConsumed)),
	}
	if err := e.saveCheckpointVariables(ctx, &snapshot, vars); err != nil {
		return nil, err
	}
	snapshot, err = runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"tool":        call.Name,
		"tool_call":   call.ID,
		"agent":       agent.Name,
		"side_effect": tool.SideEffect,
	})
	if err != nil {
		return nil, err
	}
	token, err := e.gate.Pause(ctx, core.CheckpointState{
		RunID:   runID,
		Version: snapshot.Version,
		NodeID:  "tool_approval",
		Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	e.emitJSON(ctx, core.EventRunPaused, runID, payload)
	paused := RunPausedError{RunID: runID, Token: token, Kind: "tool_approval"}
	return &paused, nil
}
