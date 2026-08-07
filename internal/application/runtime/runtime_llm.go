package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/feature"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/observability"
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
		return "", fmt.Errorf("%w; wire WithLLMGateway or use WithRequireLLM at construction", ErrLLMGatewayRequired)
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
		return e.answerWithTools(ctx, req.RunID, agent, profile, baseReq, e.wrapToolCaller(caller), req.Prompt, nil)
	}
	e.emitContextPrepared(ctx, req.RunID, stats)
	resp, err := e.chatWithRetry(ctx, req.RunID, agent, profile, baseReq)
	if err != nil {
		return "", err
	}
	if emitUsage := normalizeEmittedUsage(resp.Usage); emitUsage != nil {
		e.emitJSON(ctx, core.EventLLMTokenUsage, req.RunID, *emitUsage)
		e.recordLLMUsage(ctx, req.RunID, agent.LLM, emitUsage)
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

func (e *Engine) structuredAnswer(ctx context.Context, req RunRequest) (json.RawMessage, error) {
	if e.llm == nil {
		return nil, fmt.Errorf("%w (structured output)", ErrLLMGatewayRequired)
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
			emit := func(chunk llm.ChatChunk) {
				select {
				case ch <- chunk:
				case <-ctx.Done():
				}
			}
			output, err := e.answerWithTools(ctx, req.RunID, agent, profile, baseReq, e.wrapToolCaller(caller), req.Prompt, emit)
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
				case ch <- llm.ChatChunk{Done: true, Error: err.Error(), Err: err}:
				case <-ctx.Done():
				}
				return
			}
			// Always attach authoritative terminal prose on Done so Engine.Stream
			// can persist StepOutputs["final"] without tool-turn preambles.
			// When deltas were already streamed, Stream strips Done.Content
			// before fanout to avoid a duplicate bulk frame.
			select {
			case ch <- llm.ChatChunk{Done: true, Content: output}:
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
	e.emitJSON(ctx, core.EventLLMCalled, req.RunID, llmCalledPayload(e.obs.llmPayloadCapture, map[string]any{"profile": agent.LLM, "stream": true}, baseReq.Messages))
	streamStart := time.Now()
	streamCtx, llmSpan := e.startLLMCallSpan(ctx, req.RunID, agent, profile,
		observability.Attribute{Key: "stream", Value: "true"})
	ch, err := streamer.StreamChat(streamCtx, agent.LLM, baseReq)
	if err != nil {
		e.finishLLMCall(ctx, llmSpan, agent.LLM, streamStart, err)
		cancel()
		return nil, core.Agent{}, nil, err
	}
	return e.wrapLLMStream(ctx, req.RunID, ch, llmSpan, agent.LLM, streamStart), agent, cancel, nil
}

// maxReplanAttempts caps how many times the autonomous tool loop may replan
// after exhausting its step budget, so an incomplete plan cannot drive
// unbounded recursion and runaway cost.
const maxReplanAttempts = 3

// maxEmptyTurnRetries bounds how many times a single step re-samples after
// the provider returns a turn with neither content nor tool calls (and no
// length cutoff). Accepting such a turn would silently end the run with an
// empty answer; retrying absorbs transient provider glitches, and exhausting
// the budget surfaces a diagnostic error instead.
const maxEmptyTurnRetries = 3

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
		messages = e.applySelfCompactIfPending(ctx, runID, profile, messages)
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
				profile.Context.ExcludeToolNamesFromStaleWindow,
			)
		}
		// Recompute on every turn (not just once before the loop) so
		// plan-driven schema pruning reflects progress made by the tool
		// calls dispatched in prior iterations of this same loop, not just
		// the plan state as of the very first turn.
		toolSpecs = e.toolSpecs(ctx, runID, agent)
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
		// A turn with no content and no tool calls (and no length cutoff) is
		// a protocol violation, not an answer: accepting it would silently
		// end the run with an empty final output. Re-sample the same step a
		// bounded number of times; the empty assistant message is never
		// appended to the conversation, so it cannot poison later turns.
		emptyRetries := 0
		for isEmptyLLMTurn(resp) && emptyRetries < maxEmptyTurnRetries {
			emptyRetries++
			e.logWarn(ctx, "runtime: llm returned an empty turn; re-sampling",
				"run_id", runID, "agent", agent.Name, "step", step+1, "attempt", emptyRetries)
			resp, err = e.chatWithToolsWithRetry(stepRunCtx, runID, agent, profile, toolReq, caller, step+1, emit)
			if err != nil {
				return "", err
			}
		}
		if isEmptyLLMTurn(resp) {
			return "", fmt.Errorf("runtime: llm profile %q returned an empty response (no content, no tool calls, finish_reason=%q) on step %d after %d retries", agent.LLM, resp.FinishReason, step+1, emptyRetries)
		}
		if emitUsage := normalizeEmittedUsage(resp.Usage); emitUsage != nil {
			e.emitJSON(ctx, core.EventLLMTokenUsage, runID, *emitUsage)
			e.recordLLMUsage(ctx, runID, agent.LLM, emitUsage)
		}
		// Token-budget circuit breaker: the tracker totals accumulate across
		// pause/resume (checkpoint_usage), so a resumed run cannot spend its
		// budget twice. Providers that report no usage never trip this.
		if budget := e.scenario.Runtime.MaxTotalTokens; budget > 0 {
			if used := e.usageTrackerFor(runID).totalTokens(); used > budget {
				return "", fmt.Errorf("%w: run %s consumed %d total tokens (budget %d)", ErrTokenBudgetExceeded, runID, used, budget)
			}
		}
		assistant := resp.Message
		assistant.Role = llm.RoleAssistant
		logicalStep := stepsConsumedBase + step + 1
		assistant.ToolCalls = ensureToolCallIDs(runID, logicalStep, llm.NormalizeToolCallInputs(resp.ToolCalls))
		for index, call := range assistant.ToolCalls {
			diag, repaired := toolArgsRepairDiagnostic(resp.ToolCalls[index].Input)
			if !repaired {
				continue
			}
			// The repair collapsed malformed arguments to {}. Make that
			// visible instead of silent: the message metadata travels with
			// checkpoints/memory, the event feeds diagnostic drawers, and the
			// recorded diagnostic is appended to a later ValidateInput
			// failure so the model learns this was a format problem.
			if assistant.Metadata == nil {
				assistant.Metadata = map[string]string{}
			}
			assistant.Metadata["tool_args_normalized"] = "true"
			e.emitJSON(ctx, core.EventToolArgsNormalized, runID, map[string]any{
				"agent":     agent.Name,
				"tool":      call.Name,
				"tool_call": call.ID,
				"step":      logicalStep,
				"reason":    diag,
			})
			e.recordToolArgsRepair(runID, call.ID, diag)
		}
		resp.ToolCalls = assistant.ToolCalls
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
			if e.hooks.turnStopHook != nil {
				decision, hookErr := e.hooks.turnStopHook(ctx, core.TurnStopInfo{
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
			e.runStepFinishHooks(ctx, feature.StepInfo{RunID: runID, Agent: agent.Name, Step: logicalStep, Usage: resp.Usage})
			return resp.Message.Content, nil
		}
		var dispatchErr error
		messages, userPromptPersisted, dispatchErr = e.dispatchToolCalls(
			stepRunCtx, runID, agent, profile, assistant, resp.ToolCalls, messages, tracker,
			prompt, true, logicalStep, replanAttempts, userPromptPersisted, emit,
		)
		if dispatchErr != nil {
			return "", dispatchErr
		}
		messages, drainErr = e.drainInterjectionsIfAllowed(ctx, runID, agent, messages, interjection.DrainAfterToolBatch, false)
		if drainErr != nil {
			return "", drainErr
		}
		// Iteration boundary: the LLM response and every tool call it
		// requested are fully processed, so the conversation is consistent
		// (no orphaned tool_call IDs). Persist it so a crash before the next
		// boundary lets RetryFailedRun resume from here instead of from
		// scratch. A persistence failure fails the run - the already-saved
		// iterations keep it resumable.
		if e.autonomousIterationPersistenceEnabled(ctx) {
			if err := e.persistAutonomousIteration(ctx, runID, logicalStep, messages); err != nil {
				return "", err
			}
		}
		// Feature extension points for the completed step: loop hooks observe
		// the persisted state; stop conditions may halt the run with an
		// attributable termination reason before the next LLM call.
		stepInfo := feature.StepInfo{RunID: runID, Agent: agent.Name, Step: logicalStep, ToolCalls: len(resp.ToolCalls), Usage: resp.Usage}
		e.runStepFinishHooks(ctx, stepInfo)
		if err := e.evaluateStopConditions(ctx, stepInfo); err != nil {
			return "", err
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
	// Keep the historic message verbatim; the sentinel wrapper is what lets
	// terminal handling classify this as max_steps_exceeded via errors.Is.
	return "", fmt.Errorf("%w=%d", ErrMaxStepsExceeded, stepsConsumedBase+maxSteps)
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
