package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func (e *Engine) dispatchTool(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, callCounts map[string]int) core.ToolResult {
	if subAgentName, ok := e.delegateTarget(agent, call.Name); ok {
		resource := toolResource(agent, call, nil)
		if err := e.authorizeTool(ctx, runID, resource); err != nil {
			result := core.ToolResult{Tool: call.Name, Error: "tool invocation unauthorized"}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
			return result
		}
		return e.dispatchSubAgent(ctx, runID, agent, subAgentName, call)
	}
	if !agentAllowsTool(agent, call.Name) {
		result := core.ToolResult{Tool: call.Name, Error: "tool is not in agent whitelist"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	tool, ok := e.scenario.Tools[call.Name]
	if !ok {
		result := core.ToolResult{Tool: call.Name, Error: "tool is not declared in scenario"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	if tool.Name == "" {
		tool.Name = call.Name
	}
	if reason := approvalDenialReason(tool); reason != "" {
		result := core.ToolResult{Tool: call.Name, Error: reason}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": reason})
		return result
	}
	if tool.RateCap > 0 && callCounts[call.Name] >= tool.RateCap {
		result := core.ToolResult{Tool: call.Name, Error: fmt.Sprintf("tool rate cap exceeded: %d call(s) per run", tool.RateCap)}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	if e.tools == nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool executor registry is not configured"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	resource := toolResource(agent, call, &tool)
	if err := e.authorizeTool(ctx, runID, resource); err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool invocation unauthorized"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
		return result
	}
	if err := e.authorizeGovernanceTool(ctx, runID, agent, tool, call, callCounts); err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool invocation blocked by governance"}
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: err.Error()})
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
		return result
	}
	executor, ok, err := e.tools.ResolveTool(ctx, tool)
	if err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "resolve tool executor: " + err.Error()}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	if !ok {
		result := core.ToolResult{Tool: call.Name, Error: "tool executor is not registered"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	callCounts[call.Name]++
	result, err := e.executeToolWithRetry(ctx, runID, agent, call, executor)
	if err != nil {
		result = core.ToolResult{Tool: call.Name, Error: err.Error()}
	}
	if err := e.saveStepOutput(ctx, runID, "tool."+call.ID, result); err != nil && result.Error == "" {
		result.Error = "persist tool output: " + err.Error()
	}
	e.recordAudit(ctx, audit.Event{Type: audit.EventToolInvoked, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: toolOutcome(result)})
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "error": result.Error})
	_ = e.writeMemory(ctx, runID, agent, []memoryMessage{{
		Role:       string(llm.RoleTool),
		Content:    string(mustMarshal(result)),
		Tool:       call.Name,
		ToolCallID: call.ID,
	}})
	return result
}

func (e *Engine) authorizeGovernanceTool(ctx context.Context, runID string, agent core.Agent, tool core.Tool, call llm.ToolCall, callCounts map[string]int) error {
	if e.toolGov == nil {
		return nil
	}
	return e.toolGov.AuthorizeTool(ctx, governance.ToolInvocation{
		RunID:      runID,
		Agent:      agent.Name,
		Tool:       call.Name,
		SideEffect: tool.SideEffect,
		Input:      call.Input,
		CallCount:  callCounts[call.Name],
		TotalCalls: totalToolCalls(callCounts),
		Metadata:   cloneStringMap(tool.Metadata),
	})
}

func totalToolCalls(callCounts map[string]int) int {
	total := 0
	for _, count := range callCounts {
		total += count
	}
	return total
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
	_ = e.audit.Record(ctx, event.WithDefaults(time.Now().UTC()))
}

func principalFromContext(ctx context.Context) identity.Principal {
	principal, _ := identity.PrincipalFromContext(ctx)
	return principal
}

func toolResource(agent core.Agent, call llm.ToolCall, tool *core.Tool) security.Resource {
	resource := security.Resource{Type: "tool", ID: call.Name, Metadata: map[string]string{"agent": agent.Name}}
	if tool != nil {
		resource.Metadata["tool_type"] = tool.Type
		resource.Metadata["side_effect"] = string(tool.SideEffect)
	}
	return resource
}

func toolOutcome(result core.ToolResult) string {
	if result.Error != "" {
		return "error"
	}
	return "success"
}

func (e *Engine) dispatchSubAgent(ctx context.Context, runID string, parent core.Agent, subAgentName string, call llm.ToolCall) core.ToolResult {
	var input struct {
		Prompt  string          `json:"prompt"`
		Context json.RawMessage `json:"context"`
	}
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &input); err != nil {
			result := core.ToolResult{Tool: call.Name, Error: "invalid delegation input: " + err.Error()}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "reason": result.Error})
			return result
		}
	}
	if strings.TrimSpace(input.Prompt) == "" {
		result := core.ToolResult{Tool: call.Name, Error: "delegation prompt is required"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	e.emitJSON(ctx, core.EventToolCalled, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "sub_agent": subAgentName, "tool_call_id": call.ID})
	output, err := e.answer(ctx, RunRequest{RunID: runID, Agent: subAgentName, Prompt: input.Prompt, Context: input.Context})
	result := core.ToolResult{Tool: call.Name}
	if err != nil {
		result.Error = err.Error()
	} else {
		raw, marshalErr := json.Marshal(core.AgentOutput{RunID: runID, Text: output})
		if marshalErr != nil {
			result.Error = marshalErr.Error()
		} else {
			result.Output = raw
		}
	}
	if err := e.saveStepOutput(ctx, runID, "agent."+subAgentName+"."+call.ID, result); err != nil && result.Error == "" {
		result.Error = "persist delegated output: " + err.Error()
	}
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "sub_agent": subAgentName, "tool_call_id": call.ID, "error": result.Error})
	return result
}

func (e *Engine) executeToolWithRetry(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, executor core.ToolExecutor) (core.ToolResult, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, err
		}
		e.emitJSON(ctx, core.EventToolCalled, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "attempt": attempt})
		start := time.Now()
		result, err := executor.Execute(ctx, core.ToolCall{RunID: runID, Agent: agent.Name, Tool: call.Name, Input: call.Input})
		if err == nil {
			e.recorder.ObserveHistogram(ctx, observability.MetricToolDurationSeconds, time.Since(start).Seconds(),
				observability.Attribute{Key: "tool", Value: call.Name},
				observability.Attribute{Key: "scenario", Value: e.scenario.Name})
			return result, nil
		}
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

func approvalDenialReason(tool core.Tool) string {
	switch tool.Approval {
	case "", core.ApprovalNever:
		return ""
	case core.ApprovalAlways:
		return "tool requires approval"
	case core.ApprovalRisky:
		switch tool.SideEffect {
		case core.SideEffectWrite, core.SideEffectExternal, core.SideEffectDangerous:
			return "risky tool requires approval"
		default:
			return ""
		}
	default:
		return fmt.Sprintf("unsupported approval policy %q", tool.Approval)
	}
}

func agentAllowsTool(agent core.Agent, tool string) bool {
	for _, allowed := range agent.Tools {
		if allowed == tool {
			return true
		}
	}
	return false
}
