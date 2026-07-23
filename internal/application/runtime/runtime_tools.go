package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/internal/toolinvoke"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

type toolDispatchOptions struct {
	approved bool
	// skipPersist defers the step-output save to the caller: the parallel
	// tool batch persists every item in a single saveStepOutputs after the
	// parallel section, instead of N goroutines racing optimistic-CAS writes
	// on the same run snapshot. Audit and EventToolReturned still fire here.
	skipPersist bool
}

func (e *Engine) dispatchApprovedTool(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, tracker *toolCallTracker) (core.ToolResult, error) {
	return e.dispatchToolWithOptions(ctx, runID, agent, call, tracker, toolDispatchOptions{approved: true})
}

func (e *Engine) dispatchToolWithOptions(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, tracker *toolCallTracker, options toolDispatchOptions) (core.ToolResult, error) {
	tracker = tracker.ensure()
	if step, ok := samplingStepFromContext(ctx); ok && step.Frozen() && !step.Allows(call.Name) {
		result := core.ToolResult{Tool: call.Name, Error: "tool was not advertised in this sampling step"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{
			"agent":  agent.Name,
			"tool":   call.Name,
			"reason": result.Error,
			"kind":   "step_context",
		})
		return result, nil
	}
	if limit := e.scenario.Runtime.DoomLoopLimit; limit > 0 {
		// sameInputCount is prior attempts; +1 is the attempt about to run.
		if tracker.sameInputCount(call.Name, call.Input)+1 >= limit {
			result := core.ToolResult{Tool: call.Name, Error: formatDoomLoopError(call.Name, limit)}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{
				"agent":  agent.Name,
				"tool":   call.Name,
				"reason": result.Error,
				"kind":   "doom_loop",
			})
			return result, nil
		}
	}
	if subAgentName, ok := e.delegateTarget(agent, call.Name); ok {
		if delegationDepthFromContext(ctx) >= maxDelegationDepth {
			result := core.ToolResult{Tool: call.Name, Error: fmt.Sprintf("delegation depth limit (%d) exceeded", maxDelegationDepth)}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
			return result, nil
		}
		resource := toolResource(agent, call, nil)
		if err := e.authorizeTool(ctx, runID, resource); err != nil {
			result := core.ToolResult{Tool: call.Name, Error: "tool invocation unauthorized"}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
			return result, nil
		}
		delegateTool := core.Tool{Name: call.Name, SideEffect: core.SideEffectRead}
		if err := e.authorizeGovernanceTool(ctx, runID, agent, delegateTool, call, tracker); err != nil {
			tracker.recordAttempt(call.Name, call.Input)
			result := core.ToolResult{Tool: call.Name, Error: governanceBlockError(err)}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
			return result, nil
		}
		tracker.recordAttempt(call.Name, call.Input)
		return e.dispatchSubAgent(ctx, runID, agent, subAgentName, call, options.skipPersist)
	}
	if !agentAllowsTool(agent, call.Name) {
		result := core.ToolResult{Tool: call.Name, Error: "tool is not in agent whitelist"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	tool, ok := e.scenario.Tools[call.Name]
	if !ok {
		result := core.ToolResult{Tool: call.Name, Error: "tool is not declared in scenario"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	if tool.Name == "" {
		tool.Name = call.Name
	}
	if err := toolinvoke.ValidateInput(e.scenario.Runtime.ValidateToolInput, tool, call.Input); err != nil {
		result := core.ToolResult{Tool: call.Name, Error: err.Error()}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	if !options.approved && e.orchestrator != nil {
		decision, orchErr := e.orchestrator.DecideApproval(ctx, toolorch.ApprovalRequest{
			RunID:         runID,
			Tool:          call.Name,
			Input:         call.Input,
			PauseRequired: false,
		})
		if orchErr != nil {
			return core.ToolResult{}, orchErr
		}
		if decision == toolorch.DecisionDeny {
			result := core.ToolResult{Tool: call.Name, Error: "tool approval denied (cached)"}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error, "kind": "approval_cache"})
			if err := e.noteApprovalDeny(ctx, runID, call.Name); err != nil {
				return core.ToolResult{}, err
			}
			return result, nil
		}
	}
	if reason := toolinvoke.DenialWithoutGate(tool, e.gate != nil, options.approved || TrustModeFromContext(ctx) == TrustModeFullTrust); reason != "" {
		result := core.ToolResult{Tool: call.Name, Error: reason}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": reason})
		if err := e.noteApprovalDeny(ctx, runID, call.Name); err != nil {
			return core.ToolResult{}, err
		}
		return result, nil
	}
	if tool.RateCap > 0 && tracker.nameCount(call.Name) >= tool.RateCap {
		result := core.ToolResult{Tool: call.Name, Error: fmt.Sprintf("tool rate cap exceeded: %d call(s) per run", tool.RateCap)}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	if e.tools == nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool executor registry is not configured"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	resource := toolResource(agent, call, &tool)
	if err := e.authorizeTool(ctx, runID, resource); err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool invocation unauthorized"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
		return result, nil
	}
	if err := e.authorizeGovernanceTool(ctx, runID, agent, tool, call, tracker); err != nil {
		tracker.recordAttempt(call.Name, call.Input)
		result := core.ToolResult{Tool: call.Name, Error: governanceBlockError(err)}
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: err.Error()})
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
		return result, nil
	}
	executor, ok, err := e.tools.ResolveTool(ctx, tool)
	if err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "resolve tool executor: " + err.Error()}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	if !ok {
		result := core.ToolResult{Tool: call.Name, Error: "tool executor is not registered"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	result, err := e.executeToolWithRetry(ctx, runID, agent, tool, call, executor)
	if err != nil {
		// A context cancellation/deadline is a runtime-level condition, not
		// a tool failure: surfacing it as a ToolResult.Error would let the
		// tool loop keep calling the LLM after the run should have already
		// stopped, wasting tokens on a call that can never complete. Let it
		// propagate so the caller (and ultimately Run/RunHybrid) can
		// classify it as a cancellation or a timeout failure instead.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return core.ToolResult{}, err
		}
		result = core.ToolResult{Tool: call.Name, Error: err.Error()}
	}
	if e.orchestrator != nil {
		_ = e.orchestrator.AfterAttempt(ctx, runID, call.Name, call.Input, toolorch.AttemptResult{})
	}
	tracker.recordAttempt(call.Name, call.Input)
	if result.Error == "" {
		tracker.recordSuccess(call.Name)
		if e.denyBreaker != nil {
			e.denyBreaker.RecordAllow(runID)
		}
	}
	if !options.skipPersist {
		if err := e.saveStepOutput(ctx, runID, "tool."+call.ID, result); err != nil {
			persistErr := err.Error()
			if result.Error == "" {
				result.Error = "persist tool output: " + persistErr
			} else {
				e.logWarn(ctx, "runtime: failed to persist tool output after tool error", "run_id", runID, "tool", call.Name, "error", err)
			}
			e.recordAudit(ctx, audit.Event{Type: audit.EventToolInvoked, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: toolOutcome(result)})
			e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "error": result.Error, "persist_error": persistErr})
			return result, nil
		}
	}
	// On the skipPersist (batch) path this audit+event pair carries no
	// persistence semantics; the batch persists every item in one
	// saveStepOutputs after the parallel section.
	e.recordAudit(ctx, audit.Event{Type: audit.EventToolInvoked, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: toolOutcome(result)})
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "error": result.Error})
	return result, nil
}

// maxDelegationDepth bounds how many levels deep agent-to-agent delegation
// may nest (A delegates to B, B delegates to C, ...), so a delegation cycle
// (A delegates to B, B delegates back to A) fails fast with a clear tool
// error instead of recursing until the call stack or the run budget is
// exhausted.
const maxDelegationDepth = 8

type delegationDepthKey struct{}

func withDelegationDepth(ctx context.Context) context.Context {
	return context.WithValue(ctx, delegationDepthKey{}, delegationDepthFromContext(ctx)+1)
}

func delegationDepthFromContext(ctx context.Context) int {
	depth, _ := ctx.Value(delegationDepthKey{}).(int)
	return depth
}

func (e *Engine) authorizeGovernanceTool(ctx context.Context, runID string, agent core.Agent, tool core.Tool, call llm.ToolCall, tracker *toolCallTracker) error {
	if e.toolGov == nil {
		return nil
	}
	tracker = tracker.ensure()
	return e.toolGov.AuthorizeTool(ctx, governance.ToolInvocation{
		RunID:          runID,
		Agent:          agent.Name,
		Tool:           call.Name,
		SideEffect:     tool.SideEffect,
		Input:          call.Input,
		CallCount:      tracker.nameCount(call.Name),
		SameInputCalls: tracker.sameInputCount(call.Name, call.Input),
		TotalCalls:     tracker.totalSuccesses(),
		Metadata:       cloneStringMap(tool.Metadata),
	})
}

func governanceBlockError(err error) string {
	msg := err.Error()
	const deniedPrefix = "governance: denied: "
	if strings.HasPrefix(msg, deniedPrefix) {
		return "tool invocation blocked by governance: " + strings.TrimPrefix(msg, deniedPrefix)
	}
	return "tool invocation blocked by governance: " + msg
}

func (e *Engine) authorizeTool(ctx context.Context, runID string, resource security.Resource) error {
	if e.policy == nil {
		return nil
	}
	principal, err := identity.RequirePrincipal(ctx)
	if err != nil {
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: identity.Principal{}, Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: security.ErrUnauthenticated.Error()})
		return security.ErrUnauthenticated
	}
	if err := e.policy.Authorize(ctx, principal, security.ActionToolInvoke, resource); err != nil {
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principal, Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: err.Error()})
		return err
	}
	return nil
}

func (e *Engine) recordAudit(ctx context.Context, event audit.Event) {
	if e.audit == nil {
		return
	}
	if err := e.audit.Record(ctx, event.WithDefaults(time.Now().UTC())); err != nil {
		e.logWarn(ctx, "runtime: audit record failed", "event_type", event.Type, "run_id", event.RunID, "error", err)
	}
}

func principalFromContext(ctx context.Context) identity.Principal {
	principal, _ := identity.PrincipalFromContext(ctx)
	return principal
}

func toolResource(agent core.Agent, call llm.ToolCall, tool *core.Tool) security.Resource {
	if tool == nil {
		return security.Resource{Type: "tool", ID: call.Name, Metadata: map[string]string{"agent": agent.Name}}
	}
	return toolinvoke.SecurityResource(call.Name, *tool, map[string]string{"agent": agent.Name})
}

func toolOutcome(result core.ToolResult) string {
	if result.Error != "" {
		return "error"
	}
	return "success"
}

func (e *Engine) dispatchSubAgent(ctx context.Context, runID string, parent core.Agent, subAgentName string, call llm.ToolCall, skipPersist bool) (core.ToolResult, error) {
	var input struct {
		Prompt  string          `json:"prompt"`
		Context json.RawMessage `json:"context"`
	}
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &input); err != nil {
			result := core.ToolResult{Tool: call.Name, Error: "invalid delegation input: " + err.Error()}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "reason": result.Error})
			return result, nil
		}
	}
	if strings.TrimSpace(input.Prompt) == "" {
		result := core.ToolResult{Tool: call.Name, Error: "delegation prompt is required"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "reason": result.Error})
		return result, nil
	}
	e.emitJSON(ctx, core.EventToolCalled, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "sub_agent": subAgentName, "tool_call_id": call.ID})
	output, err := e.answer(withDelegationDepth(ctx), RunRequest{RunID: runID, Agent: subAgentName, Prompt: input.Prompt, Context: input.Context})
	result := core.ToolResult{Tool: call.Name}
	if err != nil {
		var paused RunPausedError
		if errors.As(err, &paused) {
			// A delegated sub-agent shares the parent's run snapshot, so a
			// pause inside the delegation would persist checkpoint state
			// (checkpoint_agent/messages/tool_calls) for the sub-agent and
			// overwrite whatever the parent's own tool loop needs to
			// resume, then complete the whole run with only the
			// sub-agent's answer once approved - silently discarding the
			// parent's in-flight turn. maybePauseToolCall refuses to pause
			// for delegated calls (see delegationDepthFromContext) so this
			// branch is defensive; still fail the delegation cleanly
			// rather than letting a pause escape as if the top-level run
			// itself had paused.
			result.Error = "delegated sub-agent requested human approval, which is not supported inside a delegation call"
		} else {
			result.Error = err.Error()
		}
	} else {
		raw, marshalErr := json.Marshal(core.AgentOutput{RunID: runID, Text: output})
		if marshalErr != nil {
			result.Error = marshalErr.Error()
		} else {
			result.Output = raw
		}
	}
	if !skipPersist {
		if err := e.saveStepOutput(ctx, runID, "agent."+subAgentName+"."+call.ID, result); err != nil && result.Error == "" {
			result.Error = "persist delegated output: " + err.Error()
		}
	}
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "sub_agent": subAgentName, "tool_call_id": call.ID, "error": result.Error})
	return result, nil
}

func (e *Engine) executeToolWithRetry(ctx context.Context, runID string, agent core.Agent, tool core.Tool, call llm.ToolCall, executor core.ToolExecutor) (core.ToolResult, error) {
	attempts := e.maxAttempts(agent)
	// Align with workflow nodeAutoRetrySafe: write/external/dangerous tools
	// are never auto-retried. A failed attempt may already have committed
	// its side effect; re-running would duplicate it.
	if !toolinvoke.AutoRetrySafe(tool) {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, err
		}
		e.emitJSON(ctx, core.EventToolCalled, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "attempt": attempt})
		start := time.Now()
		toolCtx, toolSpan := e.startSpan(ctx, observability.SpanToolCall,
			observability.Attribute{Key: "run_id", Value: runID},
			observability.Attribute{Key: "agent", Value: agent.Name},
			observability.Attribute{Key: "tool", Value: call.Name},
			observability.Attribute{Key: "scenario_name", Value: e.scenario.Name},
		)
		// A per-tool timeout (tool.Timeout, default 0 = disabled) bounds a
		// single execution attempt so a slow tool cannot consume the whole
		// run budget. withTimeout is a no-op when the timeout is zero.
		execCtx, cancelTimeout := e.withTimeout(toolCtx, tool.Timeout)
		toolCall := core.ToolCall{RunID: runID, Agent: agent.Name, Tool: call.Name, ToolCallID: call.ID, Input: call.Input}
		result, err := safecall.Invoke("runtime: tool execute", func() (core.ToolResult, error) {
			return e.invokeToolExecutor(execCtx, call, executor, toolCall)
		})
		cancelTimeout()
		if err == nil {
			toolSpan.End()
			e.recorder.ObserveHistogram(ctx, observability.MetricToolDurationSeconds, time.Since(start).Seconds(),
				observability.Attribute{Key: "tool", Value: call.Name},
				observability.Attribute{Key: "scenario", Value: e.scenario.Name})
			return result, nil
		}
		toolSpan.RecordError(err)
		toolSpan.End()
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return core.ToolResult{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return core.ToolResult{}, err
		}
	}
	return core.ToolResult{}, lastErr
}

type toolProgressSinkKey struct{}

func withToolProgressSink(ctx context.Context, emit streamChunkSink) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, toolProgressSinkKey{}, emit)
}

func toolProgressSinkFromContext(ctx context.Context) streamChunkSink {
	emit, _ := ctx.Value(toolProgressSinkKey{}).(streamChunkSink)
	return emit
}

func (e *Engine) invokeToolExecutor(ctx context.Context, call llm.ToolCall, executor core.ToolExecutor, toolCall core.ToolCall) (core.ToolResult, error) {
	streamer, ok := executor.(core.ToolStreamer)
	if !ok {
		return executor.Execute(ctx, toolCall)
	}
	events, err := streamer.ExecuteStream(ctx, toolCall)
	if err != nil {
		return core.ToolResult{}, err
	}
	if events == nil {
		return executor.Execute(ctx, toolCall)
	}
	emit := toolProgressSinkFromContext(ctx)
	var terminal *core.ToolResult
	var terminalErr string
	for event := range events {
		if !event.Terminal {
			if len(event.Progress) > 0 {
				emitStreamChunk(emit, llm.ChatChunk{
					Kind:         llm.ChunkKindToolProgress,
					ToolCallID:   call.ID,
					ToolName:     call.Name,
					ToolProgress: event.Progress,
				})
			}
			continue
		}
		if event.Error != "" {
			terminalErr = event.Error
		}
		terminal = event.Result
	}
	if terminalErr != "" {
		return core.ToolResult{}, fmt.Errorf("%s", terminalErr)
	}
	if terminal == nil {
		return core.ToolResult{}, fmt.Errorf("runtime: tool stream ended without a terminal result")
	}
	return *terminal, nil
}

func agentAllowsTool(agent core.Agent, tool string) bool {
	for _, allowed := range agent.Tools {
		if allowed == tool {
			return true
		}
	}
	return false
}
