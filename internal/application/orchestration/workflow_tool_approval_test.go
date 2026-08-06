package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type staticTool struct{}

func (staticTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: "risky", Output: json.RawMessage(`{"ok":true}`)}, nil
}

// H1: with a gate configured, an approval=always tool node must pause for a
// human decision (aligning with the autonomous runtime) instead of failing.
func TestWorkflowRunnerAlwaysApprovalPausesWhenGateConfigured(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "wf-always",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalAlways},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-always", ScenarioName: "wf-always", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), scenario, "run-always")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) || paused.NodeID != "call" {
		t.Fatalf("expected always-approval tool to pause, got %v", err)
	}
}

func TestWorkflowRunnerFullTrustSkipsToolApprovalPause(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "wf-full-trust",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalAlways},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-full-trust", ScenarioName: "wf-full-trust", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	ctx := core.ContextWithTrustMode(context.Background(), core.TrustModeFullTrust)
	if err := runner.Run(ctx, scenario, "run-full-trust"); err != nil {
		t.Fatalf("full_trust must skip approval pause, got %v", err)
	}
	snapshot, err := repo.Load(context.Background(), "run-full-trust")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PendingGate != nil {
		t.Fatalf("full_trust must not open human gate, got %+v", snapshot.PendingGate)
	}
	if _, ok := snapshot.StepOutputs["call"]; !ok {
		t.Fatalf("expected tool node output under full_trust, got %+v", snapshot.StepOutputs)
	}
}

// H1: without a gate, an approval=always tool node is denied (not executed).
func TestWorkflowRunnerAlwaysApprovalDeniedWithoutGate(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-always-nogate",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalAlways},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-nogate", ScenarioName: "wf-always-nogate", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err := runner.Run(context.Background(), scenario, "run-nogate")
	var paused WorkflowPausedError
	if err == nil || errors.As(err, &paused) {
		t.Fatalf("expected denial error without gate, got %v", err)
	}
}

type resultErrorTool struct{}

func (resultErrorTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: "boom", Error: "tool failed internally"}, nil
}

// T2: a tool that reports failure via ToolResult.Error (nil Go error) must fail
// the workflow node instead of being persisted as a successful step output.
func TestWorkflowRunnerToolResultErrorFailsNode(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("boom", resultErrorTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-result-error",
		Tools: map[string]core.Tool{
			"boom": {Name: "boom", Type: "builtin.static"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "boom"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-result-error", ScenarioName: "wf-result-error", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), scenario, "run-result-error"); err == nil {
		t.Fatal("expected node failure from ToolResult.Error")
	}
	snapshot, err := repo.Load(context.Background(), "run-result-error")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["call"]; ok {
		t.Fatalf("failed tool must not persist step output, got %+v", snapshot.StepOutputs)
	}
}

func TestWorkflowRunnerPausesToolNodeApproval(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("test-secret-012345"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "wf-tool-approval",
		Tools: map[string]core.Tool{
			"risky": {
				Name:        "risky",
				Type:        "builtin.static",
				Approval:    core.ApprovalPause,
				SideEffect:  core.SideEffectWrite,
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "call", Kind: core.NodeTool, Ref: "risky", Input: json.RawMessage(`{"query":"hi"}`)},
				},
			},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-wf-tool", ScenarioName: "wf-tool-approval", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), scenario, "run-wf-tool")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) || paused.NodeID != "call" {
		t.Fatalf("expected tool pause, got %v", err)
	}
	snapshot, err := repo.Load(context.Background(), "run-wf-tool")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused snapshot, got %q", snapshot.Status)
	}
	if variableString(snapshot.Variables, workflowCheckpointKindVar) != workflowToolApprovalKind {
		t.Fatalf("expected workflow tool checkpoint, got %+v", snapshot.Variables)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	if err := runner.Resume(context.Background(), scenario, "run-wf-tool"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Load(context.Background(), "run-wf-tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["call"]; !ok {
		t.Fatalf("expected tool output saved: %+v", snapshot.StepOutputs)
	}
}

// F1: security.Policy.Authorize must deny workflow tool nodes the same way
// the autonomous dispatch path does.
func TestWorkflowRunnerAuthorizesToolViaSecurityPolicy(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	policy := security.PolicyFunc(func(context.Context, identity.Principal, security.Action, security.Resource) error {
		return security.ErrUnauthorized
	})
	runner := NewWorkflowRunner(reg, repo, nil, WithSecurityPolicy(policy))
	scenario := core.Scenario{
		Name: "wf-sec",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-sec", ScenarioName: "wf-sec", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{ID: "svc", Type: identity.PrincipalService, Roles: []identity.Role{identity.RoleService}}
	err := runner.Run(identity.WithPrincipal(context.Background(), principal), scenario, "run-sec")
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected security denial, got %v", err)
	}
}

func TestWorkflowRunnerEnforcesRateCap(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-rate",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalNever, RateCap: 1},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "first", Kind: core.NodeTool, Ref: "risky"},
				{ID: "second", Kind: core.NodeTool, Ref: "risky", DependsOn: []string{"first"}},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-rate", ScenarioName: "wf-rate", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err := runner.Run(context.Background(), scenario, "run-rate")
	if err == nil || !strings.Contains(err.Error(), "rate cap exceeded") {
		t.Fatalf("expected rate cap denial, got %v", err)
	}
}

// DEFECT_REPORT D1: the workflow tool-call counter lives in the run snapshot
// and is independent of the runtime's toolCallTracker. Two parallel sibling
// nodes sharing one RateCapped tool must not both pass the check: the
// reserve is one atomic compare-and-swap, so exactly one of them executes.
func TestWorkflowRunnerEnforcesRateCapAcrossParallelNodes(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	var executed atomic.Int32
	if err := reg.RegisterTool("risky", toolFunc(func(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
		executed.Add(1)
		select {
		case <-ctx.Done():
			return core.ToolResult{}, ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
		return core.ToolResult{Tool: call.Tool, Output: json.RawMessage(`{"ok":true}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-rate-race",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalNever, RateCap: 1},
		},
		Orchestration: core.Orchestration{
			Mode:        core.OrchestrationFixedWorkflow,
			MaxParallel: 2,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "first", Kind: core.NodeTool, Ref: "risky"},
				{ID: "second", Kind: core.NodeTool, Ref: "risky"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-rate-race", ScenarioName: "wf-rate-race", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err := runner.Run(context.Background(), scenario, "run-rate-race")
	if err == nil || !strings.Contains(err.Error(), "rate cap exceeded") {
		t.Fatalf("expected rate cap denial, got %v", err)
	}
	if got := executed.Load(); got != 1 {
		t.Fatalf("executor ran %d times, want exactly 1 (cap=1 must hold under parallelism)", got)
	}
}

// A reserved-then-failed attempt must return its budget: the counter keeps
// its "successful executions only" semantics, so a transient failure does
// not permanently consume the per-run cap.
func TestWorkflowRunnerRateCapReservationReleasedOnFailure(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	var calls atomic.Int32
	if err := reg.RegisterTool("risky", toolFunc(func(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
		if calls.Add(1) == 1 {
			return core.ToolResult{}, transientWorkflowError{message: "transient failure"}
		}
		return core.ToolResult{Tool: call.Tool, Output: json.RawMessage(`{"ok":true}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-rate-release",
		Tools: map[string]core.Tool{
			"risky": {Name: "risky", Type: "builtin.static", Approval: core.ApprovalNever, RateCap: 1},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky", Retry: core.RetryPolicy{MaxAttempts: 2}},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-rate-release", ScenarioName: "wf-rate-release", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	// Attempt one reserves, fails, and releases; attempt two must be
	// admitted again (cap=1 was not consumed by the failed attempt).
	if err := runner.Run(context.Background(), scenario, "run-rate-release"); err != nil {
		t.Fatalf("expected retry to succeed after release, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("executor ran %d times, want 2 (failed attempt must release the reservation)", got)
	}
}

func TestWorkflowRunnerValidatesToolInputByDefault(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-validate",
		Tools: map[string]core.Tool{
			"risky": {
				Name: "risky", Type: "builtin.static", Approval: core.ApprovalNever,
				InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"}}}`),
			},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky", Input: json.RawMessage(`{}`)},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-validate", ScenarioName: "wf-validate", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err := runner.Run(context.Background(), scenario, "run-validate")
	if err == nil || !strings.Contains(err.Error(), "invalid tool input") {
		t.Fatalf("expected input validation denial, got %v", err)
	}
}

func TestWorkflowRunnerCanExplicitlyDisableToolInputValidation(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-validation-opt-out",
		Tools: map[string]core.Tool{
			"risky": {
				Name: "risky", Type: "builtin.static", Approval: core.ApprovalNever,
				InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"}}}`),
			},
		},
		Runtime: core.RuntimePolicy{DisableToolInputValidation: true},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "risky", Input: json.RawMessage(`{}`)},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-validation-opt-out", ScenarioName: scenario.Name, Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), scenario, "run-validation-opt-out"); err != nil {
		t.Fatalf("explicit validation opt-out should execute workflow: %v", err)
	}
}

type hangTool struct{}

func (hangTool) Execute(ctx context.Context, _ core.ToolCall) (core.ToolResult, error) {
	<-ctx.Done()
	return core.ToolResult{}, ctx.Err()
}

func TestWorkflowRunnerAppliesPerToolTimeout(t *testing.T) {
	repo := runstateinmem.NewRepository()
	reg := registry.New()
	if err := reg.RegisterTool("hang", hangTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil)
	scenario := core.Scenario{
		Name: "wf-timeout",
		Tools: map[string]core.Tool{
			"hang": {Name: "hang", Type: "builtin.hang", Approval: core.ApprovalNever, Timeout: 20 * time.Millisecond},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "call", Kind: core.NodeTool, Ref: "hang"},
			}},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-timeout", ScenarioName: "wf-timeout", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err := runner.Run(context.Background(), scenario, "run-timeout")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded from per-tool timeout, got %v", err)
	}
}
