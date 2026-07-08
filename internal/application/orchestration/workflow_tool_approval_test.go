package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	signer, err := runstate.NewTokenSigner([]byte("secret"))
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
	signer, err := runstate.NewTokenSigner([]byte("secret"))
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

func TestWorkflowRunnerValidatesToolInputWhenEnabled(t *testing.T) {
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
		Runtime: core.RuntimePolicy{ValidateToolInput: true},
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
