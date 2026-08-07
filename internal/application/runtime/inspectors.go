package runtime

import (
	"context"
	"strings"

	"github.com/aijustin/agentflow-go/internal/toolinvoke"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/toolinspect"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// This file expresses the decision gates of dispatchToolWithOptions as a
// toolinspect.Chain. The chain order, the ToolDenied event payloads (reason /
// kind), the audit records, and the tracker reserve/commit/release semantics
// are identical to the historical inline gates. The catalog meta-tool
// short-circuit, the sampling freeze check, and the delegation branch stay in
// dispatchToolWithOptions: they are dispatch routing, not verdicts.
//
// Gate order (must not change):
//  1. agent whitelist
//  2. scenario declaration (resolves req.Tool)
//  3. input schema validation
//  4. approval cache (orchestrator)
//  5. approval denial without a gate
//  6. executor registry configured
//  7. security authorization
//  8. executor resolution (resolves req.Executor)
//  9. execution budget: doom-loop / rate cap (reserves req.Reservation)
// 10. governance policy

// toolInspectorChain assembles the full chain for one dispatch: host
// prepended inspectors, the built-in gates, then host appended inspectors.
// It is rebuilt per call because the budget and governance gates close over
// the per-run tracker; inspectors themselves are stateless.
func (e *Engine) toolInspectorChain(runID string, tracker *toolCallTracker, approved bool) toolinspect.Chain {
	builtins := e.builtinToolInspectors(runID, tracker, approved)
	all := make([]toolinspect.Inspector, 0, len(e.tooling.toolInspectorPrepend)+len(builtins)+len(e.tooling.toolInspectorAppend))
	all = append(all, e.tooling.toolInspectorPrepend...)
	all = append(all, builtins...)
	all = append(all, e.tooling.toolInspectorAppend...)
	return toolinspect.NewChain(all...)
}

func (e *Engine) builtinToolInspectors(runID string, tracker *toolCallTracker, approved bool) []toolinspect.Inspector {
	return []toolinspect.Inspector{
		toolinspect.InspectorFunc{InspectorName: "agent_whitelist", Fn: e.inspectAgentWhitelist},
		toolinspect.InspectorFunc{InspectorName: "scenario_declaration", Fn: e.inspectScenarioDeclaration},
		toolinspect.InspectorFunc{InspectorName: "input_schema", Fn: e.inspectInputSchema},
		toolinspect.InspectorFunc{InspectorName: "approval_cache", Fn: func(ctx context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
			return e.inspectApprovalCache(ctx, runID, approved, req)
		}},
		toolinspect.InspectorFunc{InspectorName: "approval_gate", Fn: e.inspectApprovalGate},
		toolinspect.InspectorFunc{InspectorName: "executor_registry", Fn: e.inspectExecutorRegistry},
		toolinspect.InspectorFunc{InspectorName: "security", Fn: func(ctx context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
			return e.inspectSecurity(ctx, runID, req)
		}},
		toolinspect.InspectorFunc{InspectorName: "executor_resolve", Fn: e.inspectExecutorResolve},
		toolinspect.InspectorFunc{InspectorName: "execution_budget", Fn: func(ctx context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
			return e.inspectExecutionBudget(tracker, req)
		}},
		toolinspect.InspectorFunc{InspectorName: "governance", Fn: func(ctx context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
			return e.inspectGovernance(ctx, runID, req)
		}},
	}
}

func (e *Engine) inspectAgentWhitelist(_ context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
	if !agentAllowsTool(req.Agent, req.Call.Name) && !e.isFrameworkMetaTool(req.Call.Name) {
		return toolinspect.Deny("", "tool is not in agent whitelist"), nil
	}
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectScenarioDeclaration(_ context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
	tool, ok := e.scenario.Tools[req.Call.Name]
	if !ok {
		return toolinspect.Deny("", "tool is not declared in scenario"), nil
	}
	if tool.Name == "" {
		tool.Name = req.Call.Name
	}
	req.Tool = tool
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectInputSchema(_ context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
	validateToolInput := e.scenario.Runtime.ValidateToolInput || !e.scenario.Runtime.DisableToolInputValidation
	if err := toolinvoke.ValidateInput(validateToolInput, req.Tool, req.Call.Input); err != nil {
		errText := err.Error()
		if diag := e.toolArgsRepairFor(req.RunID, req.Call.ID); diag != "" {
			// The arguments reaching validation were repaired (malformed JSON
			// collapsed to {}), so a bare schema error would send the model
			// chasing the wrong fix. Append the original parse error to make
			// the format problem explicit.
			errText = errText + " (note: " + diag + ")"
		}
		return toolinspect.Deny("", errText), nil
	}
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectApprovalCache(ctx context.Context, runID string, approved bool, req *toolinspect.Request) (toolinspect.Finding, error) {
	if approved || e.tooling.orchestrator == nil {
		return toolinspect.AllowFinding, nil
	}
	decision, err := e.tooling.orchestrator.DecideApproval(ctx, toolorch.ApprovalRequest{
		RunID:         runID,
		Tool:          req.Call.Name,
		Input:         req.Call.Input,
		PauseRequired: false,
	})
	if err != nil {
		return toolinspect.Finding{}, err
	}
	if decision == toolorch.DecisionDeny {
		return toolinspect.Finding{
			Verdict:          toolinspect.VerdictDeny,
			Kind:             "approval_cache",
			Reason:           "tool approval denied (cached)",
			NoteApprovalDeny: true,
		}, nil
	}
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectApprovalGate(ctx context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
	if reason := toolinvoke.DenialWithoutGate(req.Tool, e.coord.gate != nil, req.Approved); reason != "" {
		return toolinspect.Finding{
			Verdict:          toolinspect.VerdictDeny,
			Reason:           reason,
			NoteApprovalDeny: true,
		}, nil
	}
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectExecutorRegistry(_ context.Context, _ *toolinspect.Request) (toolinspect.Finding, error) {
	if e.tooling.tools == nil {
		return toolinspect.Deny("", "tool executor registry is not configured"), nil
	}
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectSecurity(ctx context.Context, runID string, req *toolinspect.Request) (toolinspect.Finding, error) {
	if err := e.authorizeTool(ctx, runID, toolResource(req.Agent, req.Call, &req.Tool)); err != nil {
		return toolinspect.Finding{
			Verdict:     toolinspect.VerdictDeny,
			Reason:      "tool invocation unauthorized",
			EventReason: err.Error(),
		}, nil
	}
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectExecutorResolve(ctx context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
	executor, ok, err := e.tooling.tools.ResolveTool(ctx, req.Tool)
	if err != nil {
		return toolinspect.Deny("", "resolve tool executor: "+err.Error()), nil
	}
	if !ok {
		return toolinspect.Deny("", "tool executor is not registered"), nil
	}
	req.Executor = executor
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectExecutionBudget(tracker *toolCallTracker, req *toolinspect.Request) (toolinspect.Finding, error) {
	reservation, counts, denial := tracker.reserveToolCall(
		req.Call.Name,
		req.Call.Input,
		e.scenario.Runtime.DoomLoopLimit,
		req.Tool.RateCap,
	)
	if denial != "" {
		kind := "rate_cap"
		if strings.HasPrefix(denial, "doom-loop") {
			kind = "doom_loop"
		}
		return toolinspect.Deny(kind, denial), nil
	}
	req.Reservation = &reservation
	req.CallCount = counts.byName
	req.SameInputCalls = counts.bySameInput
	req.TotalCalls = counts.total
	return toolinspect.AllowFinding, nil
}

func (e *Engine) inspectGovernance(ctx context.Context, runID string, req *toolinspect.Request) (toolinspect.Finding, error) {
	// req.CallCount/SameInputCalls/TotalCalls come from the budget inspector's
	// reservation-time view, which already includes in-flight sibling calls,
	// so governance budgets hold under parallel batches. A tracker re-read
	// here would observe committed counts only and reopen the race.
	if err := e.authorizeGovernanceTool(ctx, runID, req.Agent, req.Tool, req.Call, req.CallCount, req.SameInputCalls, req.TotalCalls); err != nil {
		if req.Reservation != nil {
			req.Reservation.CommitAttempt()
		}
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: toolResource(req.Agent, req.Call, &req.Tool), RunID: runID, Outcome: "denied", Reason: err.Error()})
		return toolinspect.Finding{
			Verdict:     toolinspect.VerdictDeny,
			Reason:      governanceBlockError(err),
			EventReason: err.Error(),
		}, nil
	}
	return toolinspect.AllowFinding, nil
}

// emitToolDenied publishes the ToolDenied event for an inspector finding.
// The "kind" key is omitted when the finding carries no classification,
// preserving the payload shape of the historical inline gates.
func (e *Engine) emitToolDenied(ctx context.Context, runID, agentName, toolName string, finding toolinspect.Finding) {
	payload := map[string]any{
		"agent":  agentName,
		"tool":   toolName,
		"reason": finding.EventReasonOrDefault(),
	}
	if finding.Kind != "" {
		payload["kind"] = finding.Kind
	}
	e.emitJSON(ctx, core.EventToolDenied, runID, payload)
}
