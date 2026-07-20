package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func (e *Engine) answer(ctx context.Context, req RunRequest) (string, error) {
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return "", err
	}
	return e.answerForAgent(ctx, req, agent)
}

func (e *Engine) answerForAgent(ctx context.Context, req RunRequest, agent core.Agent) (string, error) {
	if e.llm == nil {
		return "", fmt.Errorf("runtime: llm gateway is required; wire WithLLMGateway or use WithRequireLLM at construction")
	}
	ctx, cancel := e.withTimeout(ctx, agent.Policy.Timeout)
	defer cancel()
	e.emitSkillApplied(ctx, req.RunID, agent)
	profile, err := e.llmProfile(agent.LLM)
	if err != nil {
		return "", err
	}
	history, err := e.readMemory(ctx, req.RunID, agent, req.Prompt)
	if err != nil {
		return "", err
	}
	messages, stats := e.prepareContext(ctx, agent, profile, req, history)
	if e.planningEnabled() {
		var err error
		messages, err = e.injectAutonomousPlan(ctx, req.RunID, agent, profile, req, messages)
		if err != nil {
			return "", err
		}
	}
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	if len(agent.Tools)+len(agent.SubAgents) > 0 {
		caller, ok := e.llm.(llm.ToolCaller)
		if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
			// Silently ignoring the configured tools and falling back to a
			// plain chat call would make the agent behave as if it had no
			// tools at all, with no indication of why - fail loudly so the
			// mismatch between agent config and LLM profile capability is
			// caught immediately instead of manifesting as "the agent
			// never calls any tools".
			return "", fmt.Errorf("runtime: agent %q has tools/sub-agents configured but llm profile %q does not support tool calling", agent.Name, agent.LLM)
		}
		return e.answerWithTools(ctx, req.RunID, agent, profile, baseReq, caller, req.Prompt, nil)
	}
	e.emitContextPrepared(ctx, req.RunID, stats)
	resp, err := e.chatWithRetry(ctx, req.RunID, agent, profile, baseReq)
	if err != nil {
		return "", err
	}
	if resp.Usage.TotalTokens > 0 {
		e.emitJSON(ctx, core.EventLLMTokenUsage, req.RunID, resp.Usage)
	}
	if strings.TrimSpace(resp.Message.Content) == "" && resp.FinishReason == "length" {
		return "", fmt.Errorf("runtime: llm response was empty after reaching max tokens; increase max_output_tokens or disable reasoning output for profile %q", agent.LLM)
	}
	if err := e.writeMemory(ctx, req.RunID, agent, []memoryMessage{
		runTurnMemoryMessage(string(llm.RoleUser), req.Prompt),
		runTurnMemoryMessage(string(llm.RoleAssistant), resp.Message.Content),
	}); err != nil {
		return "", err
	}
	return resp.Message.Content, nil
}

// RunAgent executes one configured agent inside an existing run. It reuses the
// runtime LLM, memory, tool, governance, and observability paths without
// creating or completing a root RunSnapshot.
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

func (e *Engine) structuredAnswer(ctx context.Context, req RunRequest) (json.RawMessage, error) {
	if e.llm == nil {
		return nil, fmt.Errorf("runtime: structured output requires llm gateway")
	}
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return nil, err
	}
	e.emitSkillApplied(ctx, req.RunID, agent)
	if len(agent.Policy.OutputSchema) == 0 {
		return nil, fmt.Errorf("runtime: agent %q output_schema is required for structured output", agent.Name)
	}
	if !json.Valid(agent.Policy.OutputSchema) {
		return nil, fmt.Errorf("runtime: agent %q output_schema is invalid JSON", agent.Name)
	}
	outputter, ok := e.llm.(llm.StructuredOutputter)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapStructuredOutput) {
		return nil, fmt.Errorf("runtime: llm profile %q does not support structured output", agent.LLM)
	}
	ctx, cancel := e.withTimeout(ctx, agent.Policy.Timeout)
	defer cancel()
	profile, err := e.llmProfile(agent.LLM)
	if err != nil {
		return nil, err
	}
	history, err := e.readMemory(ctx, req.RunID, agent, req.Prompt)
	if err != nil {
		return nil, err
	}
	messages, stats := e.prepareContext(ctx, agent, profile, req, history)
	e.emitContextPrepared(ctx, req.RunID, stats)
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	raw, err := e.structuredWithRetry(ctx, req.RunID, agent, profile, agent.Policy.OutputSchema, baseReq, outputter)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("runtime: structured output was not valid JSON")
	}
	if err := e.writeMemory(ctx, req.RunID, agent, []memoryMessage{
		runTurnMemoryMessage(string(llm.RoleUser), req.Prompt),
		memoryMessageFromLLMWithProvenance(llm.Message{Role: llm.RoleAssistant, Content: string(raw)}, memory.ProvenanceRunTurn),
	}); err != nil {
		return nil, err
	}
	return raw, nil
}

func (e *Engine) streamAnswer(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, core.Agent, context.CancelFunc, error) {
	if e.llm == nil {
		return nil, core.Agent{}, nil, fmt.Errorf("runtime: streaming requires llm gateway")
	}
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return nil, core.Agent{}, nil, err
	}
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = e.withTimeout(ctx, agent.Policy.Timeout)
	} else {
		cancel = func() {}
	}
	e.emitSkillApplied(ctx, req.RunID, agent)
	profile, err := e.llmProfile(agent.LLM)
	if err != nil {
		cancel()
		return nil, core.Agent{}, nil, err
	}
	history, err := e.readMemory(ctx, req.RunID, agent, req.Prompt)
	if err != nil {
		cancel()
		return nil, core.Agent{}, nil, err
	}
	messages, stats := e.prepareContext(ctx, agent, profile, req, history)
	if e.planningEnabled() {
		messages, err = e.injectAutonomousPlan(ctx, req.RunID, agent, profile, req, messages)
		if err != nil {
			cancel()
			return nil, core.Agent{}, nil, err
		}
	}
	e.emitContextPrepared(ctx, req.RunID, stats)
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	if len(agent.Tools)+len(agent.SubAgents) > 0 {
		caller, ok := e.llm.(llm.ToolCaller)
		if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
			cancel()
			return nil, core.Agent{}, nil, fmt.Errorf("runtime: llm profile %q does not support tool calling", agent.LLM)
		}
		// Buffer tool/call progress separately from the terminal Done chunk so a
		// slow Stream consumer still observes incremental tool events.
		ch := make(chan llm.ChatChunk, 16)
		go func() {
			defer close(ch)
			defer func() {
				if r := recover(); r != nil {
					select {
					case ch <- llm.ChatChunk{Done: true, Error: fmt.Sprintf("runtime: panic recovered: %v", r)}:
					case <-ctx.Done():
					default:
					}
				}
			}()
			contentEmitted := false
			emit := func(chunk llm.ChatChunk) {
				if chunk.IsAnswerContent() && chunk.Content != "" {
					contentEmitted = true
				}
				select {
				case ch <- chunk:
				case <-ctx.Done():
				}
			}
			output, err := e.answerWithTools(ctx, req.RunID, agent, profile, baseReq, caller, req.Prompt, emit)
			if err != nil {
				var paused RunPausedError
				if errorsAsRunPaused(err, &paused) {
					select {
					case ch <- llm.ChatChunk{Done: true, Paused: true, PauseToken: paused.Token, PauseKind: paused.Kind}:
					case <-ctx.Done():
					}
					return
				}
				select {
				case ch <- llm.ChatChunk{Done: true, Error: err.Error()}:
				case <-ctx.Done():
				}
				return
			}
			// When the tool loop already streamed answer deltas, only signal Done.
			done := llm.ChatChunk{Done: true}
			if !contentEmitted && output != "" {
				done.Content = output
			}
			select {
			case ch <- done:
			case <-ctx.Done():
			}
		}()
		return ch, agent, cancel, nil
	}
	streamer, ok := e.llm.(llm.Streamer)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapStream) {
		cancel()
		return nil, core.Agent{}, nil, fmt.Errorf("runtime: llm profile %q does not support streaming", agent.LLM)
	}
	e.emitJSON(ctx, core.EventLLMCalled, req.RunID, llmCalledPayload(map[string]any{"profile": agent.LLM, "stream": true}, baseReq.Messages))
	ch, err := streamer.StreamChat(ctx, agent.LLM, baseReq)
	if err != nil {
		cancel()
		return nil, core.Agent{}, nil, err
	}
	return ch, agent, cancel, nil
}

// maxReplanAttempts caps how many times the autonomous tool loop may replan
// after exhausting its step budget, so an incomplete plan cannot drive
// unbounded recursion and runaway cost.
const maxReplanAttempts = 3

// streamChunkSink receives incremental Stream progress (tool_call / tool_result /
// tool_denied) while the governed tool loop runs. Nil means non-streaming callers.
type streamChunkSink func(llm.ChatChunk)

func emitStreamChunk(emit streamChunkSink, chunk llm.ChatChunk) {
	if emit == nil {
		return
	}
	emit(chunk)
}

func (e *Engine) answerWithTools(
	ctx context.Context,
	runID string,
	agent core.Agent,
	profile core.LLMProfileRef,
	req llm.ChatRequest,
	caller llm.ToolCaller,
	prompt string,
	emit streamChunkSink,
) (string, error) {
	maxSteps := firstPositive(agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 8)
	toolSpecs := e.toolSpecs(ctx, runID, agent)
	messages := append([]llm.Message(nil), req.Messages...)
	tracker := newToolCallTracker()
	return e.answerWithToolsFrom(
		ctx, runID, agent, profile, req, caller, toolSpecs, messages, tracker,
		maxSteps, prompt, 0, 0, false, 0, emit,
	)
}

func (e *Engine) answerWithToolsFrom(
	ctx context.Context,
	runID string,
	agent core.Agent,
	profile core.LLMProfileRef,
	req llm.ChatRequest,
	caller llm.ToolCaller,
	toolSpecs []llm.ToolSpec,
	messages []llm.Message,
	tracker *toolCallTracker,
	maxSteps int,
	prompt string,
	replanAttempts int,
	stepsConsumedBase int,
	userPromptPersisted bool,
	completionRecoveryAttempts int,
	emit streamChunkSink,
) (string, error) {
	if hint := e.planningToolHint(ctx, runID); hint != "" {
		messages = appendPlanningHint(messages, hint)
	}
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var drainErr error
		policy := e.drainPolicy()
		if policy.Allow(interjection.DrainBeforeSample, false) && !policy.DeferUntilPostCompact {
			messages, drainErr = e.drainInterjectionsIfAllowed(ctx, runID, agent, messages, interjection.DrainBeforeSample, false)
			if drainErr != nil {
				return "", drainErr
			}
		}
		var staleStats staleEvictionStats
		if profile.Context.StaleToolTurns > 0 {
			messages, staleStats = evictStaleToolMessagesWithPolicy(
				messages,
				profile.Context.StaleToolTurns,
				profile.Context.ExcludeFromStaleWindowOrDefault(),
			)
		}
		// Recompute on every turn (not just once before the loop) so
		// plan-driven schema pruning reflects progress made by the tool
		// calls dispatched in prior iterations of this same loop, not just
		// the plan state as of the very first turn.
		toolSpecs := e.toolSpecs(ctx, runID, agent)
		stepCtx := toolorch.FreezeSamplingStepContext(toolSpecs)
		stepRunCtx := contextWithSamplingStep(ctx, stepCtx)
		prepared, stats := e.prepareMessages(ctx, runID, agent, messages, profile)
		if policy.DeferUntilPostCompact {
			if policy.Allow(interjection.DrainPostCompact, stats.NeedsReminder) {
				messages, drainErr = e.drainInterjectionsIfAllowed(ctx, runID, agent, messages, interjection.DrainPostCompact, stats.NeedsReminder)
				if drainErr != nil {
					return "", drainErr
				}
				prepared, stats = e.prepareMessages(ctx, runID, agent, messages, profile)
			} else if policy.BeforeSample {
				messages, drainErr = e.drainInterjectionsIfAllowed(ctx, runID, agent, messages, interjection.DrainBeforeSample, false)
				if drainErr != nil {
					return "", drainErr
				}
				prepared, stats = e.prepareMessages(ctx, runID, agent, messages, profile)
			}
		}
		stats.StaleDroppedToolTurns = staleStats.DroppedToolTurns
		stats.DenialOccupiedSlots = staleStats.DenialOccupiedSlots
		stats.StaleExcludedTurns = staleStats.ExcludedTurns
		stats.CompactedToolDenials = staleStats.CompactedDenials
		e.emitContextPrepared(ctx, runID, stats)
		toolReq := llm.ToolCallRequest{
			ChatRequest: llm.ChatRequest{
				Messages:        prepared,
				Temperature:     req.Temperature,
				TopP:            req.TopP,
				MaxTokens:       req.MaxTokens,
				Thinking:        req.Thinking,
				ReasoningEffort: req.ReasoningEffort,
				ExtraBody:       req.ExtraBody,
			},
			Tools: toolSpecs,
		}
		resp, err := e.chatWithToolsWithRetry(stepRunCtx, runID, agent, profile, toolReq, caller, step+1, emit)
		if err != nil {
			return "", err
		}
		if resp.Usage.TotalTokens > 0 {
			e.emitJSON(ctx, core.EventLLMTokenUsage, runID, resp.Usage)
		}
		assistant := resp.Message
		assistant.Role = llm.RoleAssistant
		assistant.ToolCalls = resp.ToolCalls
		messages = append(messages, assistant)
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Message.Content) == "" && resp.FinishReason == "length" {
				return "", fmt.Errorf("runtime: llm response was empty after reaching max tokens; increase max_output_tokens or disable reasoning output for profile %q", agent.LLM)
			}
			continued, cont, enforceErr := e.enforceCompletionRequirement(
				ctx, runID, agent, messages, tracker, &completionRecoveryAttempts,
			)
			if enforceErr != nil {
				return "", enforceErr
			}
			if cont {
				messages = continued
				continue
			}
			if e.turnStopHook != nil {
				decision, hookErr := e.turnStopHook(ctx, core.TurnStopInfo{
					RunID:  runID,
					Agent:  agent.Name,
					Answer: resp.Message.Content,
				})
				if hookErr != nil {
					return "", hookErr
				}
				if decision.Continue {
					promptText := strings.TrimSpace(decision.ContinuationPrompt)
					if promptText == "" {
						promptText = "Continue."
					}
					messages = append(messages, llm.Message{Role: llm.RoleUser, Content: promptText})
					e.emitJSON(ctx, core.EventTurnStopContinued, runID, map[string]any{
						"agent": agent.Name,
					})
					continue
				}
			}
			mem := make([]memoryMessage, 0, 2)
			if !userPromptPersisted && strings.TrimSpace(prompt) != "" {
				mem = append(mem, runTurnMemoryMessage(string(llm.RoleUser), prompt))
			}
			mem = append(mem, memoryMessageFromLLM(assistant))
			if err := e.writeMemory(ctx, runID, agent, mem); err != nil {
				return "", err
			}
			return resp.Message.Content, nil
		}
		var dispatchErr error
		messages, userPromptPersisted, dispatchErr = e.dispatchToolCalls(
			stepRunCtx, runID, agent, profile, assistant, resp.ToolCalls, messages, tracker,
			prompt, true, stepsConsumedBase+step+1, replanAttempts, userPromptPersisted, emit,
		)
		if dispatchErr != nil {
			return "", dispatchErr
		}
		messages, drainErr = e.drainInterjectionsIfAllowed(ctx, runID, agent, messages, interjection.DrainAfterToolBatch, false)
		if drainErr != nil {
			return "", drainErr
		}
	}
	return e.replanOrFail(
		ctx, runID, agent, profile, req, caller, toolSpecs, messages, tracker,
		maxSteps, prompt, replanAttempts, stepsConsumedBase, userPromptPersisted, completionRecoveryAttempts, emit,
	)
}

// replanOrFail is called once the tool loop has exhausted its step budget,
// either by running maxSteps local steps or - on the resume-after-approval
// path - by having already consumed maxSteps steps in total before control
// returned to the loop. replanAttempts must be the cumulative count of
// replans across the whole run, including any that happened before a pause
// and resume, so pausing cannot reset the maxReplanAttempts budget and drive
// unbounded replanning across checkpoints.
func (e *Engine) replanOrFail(
	ctx context.Context,
	runID string,
	agent core.Agent,
	profile core.LLMProfileRef,
	req llm.ChatRequest,
	caller llm.ToolCaller,
	toolSpecs []llm.ToolSpec,
	messages []llm.Message,
	tracker *toolCallTracker,
	maxSteps int,
	prompt string,
	replanAttempts int,
	stepsConsumedBase int,
	userPromptPersisted bool,
	completionRecoveryAttempts int,
	emit streamChunkSink,
) (string, error) {
	if replanAttempts < maxReplanAttempts {
		complete, err := e.planningComplete(ctx, runID)
		if err != nil {
			return "", err
		}
		if !complete {
			replanned, err := e.maybeReplan(ctx, runID, agent, profile, RunRequest{RunID: runID, Agent: agent.Name, Prompt: prompt}, messages)
			if err != nil {
				return "", err
			}
			if len(replanned) > len(messages) {
				return e.answerWithToolsFrom(
					ctx, runID, agent, profile, req, caller, toolSpecs, replanned, tracker,
					maxSteps, prompt, replanAttempts+1, stepsConsumedBase+maxSteps, userPromptPersisted, completionRecoveryAttempts, emit,
				)
			}
		}
	}
	return "", fmt.Errorf("runtime: autonomous tool loop exceeded max_steps=%d", stepsConsumedBase+maxSteps)
}

// dispatchToolCalls executes an assistant turn's tool calls. Before each
// executable prefix it checks whether human approval is required; if so it
// persists the remaining calls and returns a pause. Approved prefixes run
// concurrently (bounded by max_parallel, default = batch size) with same-path
// serialization. Tool result messages are appended in model order. When
// persistTurnMemory is true and every call in the batch completes, the
// assistant turn and tool results are written to memory together so a mid-turn
// pause never leaves partial assistant/tool_call pairings in memory.
func (e *Engine) dispatchToolCalls(
	ctx context.Context,
	runID string,
	agent core.Agent,
	profile core.LLMProfileRef,
	turnAssistant llm.Message,
	calls []llm.ToolCall,
	messages []llm.Message,
	tracker *toolCallTracker,
	prompt string,
	persistTurnMemory bool,
	stepsConsumed int,
	replanAttempts int,
	userPromptPersisted bool,
	emit streamChunkSink,
) ([]llm.Message, bool, error) {
	toolMem := make([]memoryMessage, 0, len(calls))
	for index := 0; index < len(calls); {
		toolCall := calls[index]
		emitStreamChunk(emit, llm.ChatChunk{
			Kind:       llm.ChunkKindToolCall,
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			ToolInput:  toolCall.Input,
		})
		if paused, err := e.maybePauseToolCall(ctx, runID, agent, calls[index:], messages, tracker, prompt, stepsConsumed, replanAttempts); err != nil {
			return messages, userPromptPersisted, err
		} else if paused != nil {
			return messages, userPromptPersisted, *paused
		}
		end, err := e.growExecutableToolPrefix(ctx, runID, calls, index)
		if err != nil {
			return messages, userPromptPersisted, err
		}
		for j := index + 1; j < end; j++ {
			emitStreamChunk(emit, llm.ChatChunk{
				Kind:       llm.ChunkKindToolCall,
				ToolCallID: calls[j].ID,
				ToolName:   calls[j].Name,
				ToolInput:  calls[j].Input,
			})
		}
		items, err := e.executeToolBatch(ctx, runID, agent, profile, calls[index:end], tracker, emit)
		if err != nil {
			return messages, userPromptPersisted, err
		}
		for _, item := range items {
			toolMem = append(toolMem, item.toolMsg)
			messages = append(messages, item.message)
		}
		e.markPlanStepsForBatch(ctx, runID, items)
		index = end
	}
	if persistTurnMemory {
		if !userPromptPersisted && strings.TrimSpace(prompt) != "" {
			if err := e.writeMemory(ctx, runID, agent, []memoryMessage{runTurnMemoryMessage(string(llm.RoleUser), prompt)}); err != nil {
				return messages, userPromptPersisted, err
			}
			userPromptPersisted = true
		}
		if err := e.persistToolTurnMemory(ctx, runID, agent, turnAssistant, toolMem); err != nil {
			return messages, userPromptPersisted, err
		}
	}
	return messages, userPromptPersisted, nil
}

func (e *Engine) chatWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ChatRequest) (llm.ChatResponse, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return llm.ChatResponse{}, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, llmCalledPayload(map[string]any{
			"profile": agent.LLM,
			"tools":   false,
			"attempt": attempt,
		}, req.Messages))
		resp, err := safecall.Invoke("runtime: llm chat", func() (llm.ChatResponse, error) {
			return e.llm.Chat(callCtx, agent.LLM, req)
		})
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, llmReturnedPayload(map[string]any{
				"profile":       agent.LLM,
				"finish_reason": resp.FinishReason,
				"attempt":       attempt,
			}, resp.Message.Content))
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return llm.ChatResponse{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return llm.ChatResponse{}, err
		}
	}
	return llm.ChatResponse{}, lastErr
}

func (e *Engine) chatWithToolsWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ToolCallRequest, caller llm.ToolCaller, step int, emit streamChunkSink) (llm.ToolCallResponse, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return llm.ToolCallResponse{}, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, llmCalledPayload(map[string]any{
			"profile": agent.LLM,
			"tools":   true,
			"step":    step,
			"attempt": attempt,
			"stream":  emit != nil,
		}, req.Messages))
		var resp llm.ToolCallResponse
		var err error
		if emit != nil {
			if streamer, ok := e.llm.(llm.ToolCallStreamer); ok && e.llm.Supports(agent.LLM, llm.CapStream) {
				resp, err = e.collectStreamChatWithTools(callCtx, streamer, agent.LLM, req, emit)
			} else {
				resp, err = safecall.Invoke("runtime: llm chat with tools", func() (llm.ToolCallResponse, error) {
					return caller.ChatWithTools(callCtx, agent.LLM, req)
				})
			}
		} else {
			resp, err = safecall.Invoke("runtime: llm chat with tools", func() (llm.ToolCallResponse, error) {
				return caller.ChatWithTools(callCtx, agent.LLM, req)
			})
		}
		cancel()
		if err == nil {
			payload := map[string]any{
				"profile":       agent.LLM,
				"finish_reason": resp.FinishReason,
				"tool_calls":    len(resp.ToolCalls),
				"step":          step,
				"attempt":       attempt,
			}
			if names := toolCallNames(resp.ToolCalls); len(names) > 0 {
				payload["tool_names"] = names
			}
			e.emitJSON(ctx, core.EventLLMReturned, runID, llmReturnedPayload(payload, resp.Message.Content))
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return llm.ToolCallResponse{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return llm.ToolCallResponse{}, err
		}
	}
	return llm.ToolCallResponse{}, lastErr
}

func (e *Engine) collectStreamChatWithTools(
	ctx context.Context,
	streamer llm.ToolCallStreamer,
	profile string,
	req llm.ToolCallRequest,
	emit streamChunkSink,
) (llm.ToolCallResponse, error) {
	ch, err := streamer.StreamChatWithTools(ctx, profile, req)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	var content strings.Builder
	var toolCalls []llm.ToolCall
	var usage llm.TokenUsage
	finishReason := "stop"
	for chunk := range ch {
		if chunk.Error != "" {
			return llm.ToolCallResponse{}, fmt.Errorf("%s", chunk.Error)
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.InputTokens > 0 || chunk.Usage.OutputTokens > 0 {
			usage = chunk.Usage
		}
		switch chunk.Kind {
		case llm.ChunkKindToolCall:
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:    chunk.ToolCallID,
				Name:  chunk.ToolName,
				Input: chunk.ToolInput,
			})
			finishReason = "tool_calls"
		default:
			if chunk.IsAnswerContent() && chunk.Content != "" {
				content.WriteString(chunk.Content)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolCallResponse{}, err
	}
	message := llm.Message{
		Role:    llm.RoleAssistant,
		Content: content.String(),
	}
	if len(toolCalls) > 0 {
		message.ToolCalls = append([]llm.ToolCall(nil), toolCalls...)
		message.Content = ""
	} else if message.Content != "" {
		// A tool-capable provider may send prose before it declares native
		// tool_calls. Commit answer content only after the turn closes and is
		// known to be a terminal prose turn.
		emitStreamChunk(emit, llm.ChatChunk{Content: message.Content})
	}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{
			Message:      message,
			Usage:        usage,
			FinishReason: finishReason,
		},
		ToolCalls: toolCalls,
	}, nil
}

func (e *Engine) structuredWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, schema json.RawMessage, req llm.ChatRequest, outputter llm.StructuredOutputter) (json.RawMessage, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, llmCalledPayload(map[string]any{
			"profile":    agent.LLM,
			"structured": true,
			"attempt":    attempt,
		}, req.Messages))
		raw, err := safecall.Invoke("runtime: llm structured chat", func() (json.RawMessage, error) {
			return outputter.StructuredChat(callCtx, agent.LLM, schema, req)
		})
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, llmReturnedPayload(map[string]any{
				"profile":    agent.LLM,
				"structured": true,
				"attempt":    attempt,
			}, string(raw)))
			return raw, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return nil, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// maxLLMCalledMessageChars caps each message content in LLMCalled payloads so
// EventStore / diagnostic drawers stay usable without unbounded tool dumps.
const maxLLMCalledMessageChars = 8000

// llmCalledPayload attaches the messages actually sent to the model so Debug
// drawers can show LLM 入参 (messages / prompt) instead of metadata-only cards.
func llmCalledPayload(base map[string]any, messages []llm.Message) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	if len(messages) == 0 {
		return base
	}
	obs := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		entry := map[string]any{"role": string(m.Role)}
		if c := strings.TrimSpace(m.Content); c != "" {
			entry["content"] = truncateObservabilityText(c, maxLLMCalledMessageChars)
		}
		if id := strings.TrimSpace(m.ToolCallID); id != "" {
			entry["tool_call_id"] = id
		}
		if name := strings.TrimSpace(m.Name); name != "" {
			entry["name"] = name
		}
		if names := toolCallNames(m.ToolCalls); len(names) > 0 {
			entry["tool_names"] = names
		}
		obs = append(obs, entry)
	}
	base["messages"] = obs
	base["message_count"] = len(obs)
	if prompt := lastUserMessageContent(messages); prompt != "" {
		base["prompt"] = truncateObservabilityText(prompt, maxLLMCalledMessageChars)
	}
	return base
}

// llmReturnedPayload attaches assistant text for Debug/EventStore consumers.
// Older emitters omitted text; ProductUI and diagnostic drawers need it.
func llmReturnedPayload(base map[string]any, text string) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		base["text"] = trimmed
	}
	return base
}

func lastUserMessageContent(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleUser {
			continue
		}
		if c := strings.TrimSpace(messages[i].Content); c != "" {
			return c
		}
	}
	return ""
}

func truncateObservabilityText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	// Prefer rune-safe cut when the limit lands mid-codepoint.
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + "…"
}

func toolCallNames(calls []llm.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	for _, tc := range calls {
		if name := strings.TrimSpace(tc.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
