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
	checkpointKindVar       = "checkpoint_kind"
	checkpointPromptVar     = "checkpoint_prompt"
	checkpointAgentVar      = "checkpoint_agent"
	checkpointContextVar    = "checkpoint_context"
	checkpointResumedVar    = "checkpoint_resumed"
	checkpointToolCallVar   = "checkpoint_tool_call"
	checkpointMessagesVar   = "checkpoint_messages"
	checkpointToolCountsVar = "checkpoint_tool_counts"
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
	req := RunRequest{
		RunID:   snapshot.RunID,
		Agent:   variableString(snapshot.Variables, checkpointAgentVar),
		Prompt:  variableString(snapshot.Variables, checkpointPromptVar),
		Context: snapshot.Variables[checkpointContextVar],
	}
	if err := e.markCheckpointResumed(ctx, &snapshot); err != nil {
		return RunResult{}, err
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
	var toolCall llm.ToolCall
	if raw := snapshot.Variables[checkpointToolCallVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &toolCall); err != nil {
			return RunResult{}, fmt.Errorf("runtime: decode checkpoint tool call: %w", err)
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
		_ = json.Unmarshal(raw, &toolCounts)
	}
	if err := e.markCheckpointResumed(ctx, &snapshot); err != nil {
		return RunResult{}, err
	}
	profile := e.scenario.LLMs[agent.LLM]
	caller, ok := e.llm.(llm.ToolCaller)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
		return RunResult{}, fmt.Errorf("runtime: llm profile %q does not support tool calling", agent.LLM)
	}
	output, err := e.continueToolLoopFrom(ctx, snapshot.RunID, agent, profile, messages, toolCall, toolCounts, caller)
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

func (e *Engine) continueToolLoopFrom(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, messages []llm.Message, pending llm.ToolCall, toolCounts map[string]int, caller llm.ToolCaller) (string, error) {
	if pending.Name != "" {
		result, err := e.executeApprovedTool(ctx, runID, agent, pending, toolCounts)
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
			Name:       pending.Name,
			ToolCallID: pending.ID,
		})
	}
	maxSteps := firstPositive(agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 8)
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
	return e.answerWithToolsFrom(ctx, runID, agent, profile, baseReq, caller, toolSpecs, messages, toolCounts, maxSteps)
}

func (e *Engine) executeApprovedTool(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, toolCounts map[string]int) (core.ToolResult, error) {
	toolCounts[call.Name]++
	result, err := e.executeToolAfterApproval(ctx, runID, agent, call)
	if err != nil {
		return core.ToolResult{}, err
	}
	if err := e.saveStepOutput(ctx, runID, "tool."+call.ID, result); err != nil && result.Error == "" {
		result.Error = "persist tool output: " + err.Error()
	}
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "error": result.Error})
	_ = e.writeMemory(ctx, runID, agent, []memoryMessage{{
		Role:       string(llm.RoleTool),
		Content:    string(mustMarshal(result)),
		Tool:       call.Name,
		ToolCallID: call.ID,
	}})
	if e.scenario.Orchestration.Planning.Enabled && e.scenario.Orchestration.Planning.Execute {
		_ = e.markPlanStepDone(ctx, runID, call.Name)
	}
	return result, nil
}

func (e *Engine) executeToolAfterApproval(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall) (core.ToolResult, error) {
	if subAgentName, ok := e.delegateTarget(agent, call.Name); ok {
		return e.dispatchSubAgent(ctx, runID, agent, subAgentName, call), nil
	}
	tool, ok := e.scenario.Tools[call.Name]
	if !ok {
		return core.ToolResult{Tool: call.Name, Error: "tool is not declared in scenario"}, nil
	}
	if tool.Name == "" {
		tool.Name = call.Name
	}
	if e.tools == nil {
		return core.ToolResult{Tool: call.Name, Error: "tool executor registry is not configured"}, nil
	}
	executor, ok, err := e.tools.ResolveTool(ctx, tool)
	if err != nil {
		return core.ToolResult{Tool: call.Name, Error: "resolve tool executor: " + err.Error()}, nil
	}
	if !ok {
		return core.ToolResult{Tool: call.Name, Error: "tool executor is not registered"}, nil
	}
	return e.executeToolWithRetry(ctx, runID, agent, call, executor)
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

func (e *Engine) markCheckpointResumed(ctx context.Context, snapshot *runstate.RunSnapshot) error {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	snapshot.Variables[checkpointResumedVar] = json.RawMessage(`true`)
	return e.runs.Save(ctx, snapshot, snapshot.Version)
}

func (e *Engine) saveCheckpointVariables(ctx context.Context, snapshot *runstate.RunSnapshot, values map[string]json.RawMessage) error {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	for key, value := range values {
		snapshot.Variables[key] = value
	}
	return e.runs.Save(ctx, snapshot, snapshot.Version)
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

func errorsAsRunPaused(err error, target *RunPausedError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

func (e *Engine) maybePauseToolCall(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, messages []llm.Message, toolCounts map[string]int) (*RunPausedError, error) {
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
	toolCallRaw, err := json.Marshal(call)
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
		checkpointKindVar:       json.RawMessage(`"tool_approval"`),
		checkpointAgentVar:      json.RawMessage(fmt.Sprintf("%q", agent.Name)),
		checkpointToolCallVar:   toolCallRaw,
		checkpointMessagesVar:   messagesRaw,
		checkpointToolCountsVar: countsRaw,
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
