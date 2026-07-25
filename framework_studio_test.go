package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkStudioParts(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-parts",
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Description: "repeat"},
		},
		Skills: map[string]core.Skill{
			"brief": {Name: "brief", Kind: core.SkillKindPrompt, Description: "short"},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", Description: "helper", Tools: []string{"echo"}},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
			Workflows: map[string]core.Workflow{
				"prep": {Nodes: []core.WorkflowNode{{ID: "p", Kind: core.NodeTransform}}},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	parts := fw.StudioParts()
	if len(parts.Agents) != 1 || parts.Agents[0].Name != "assistant" {
		t.Fatalf("unexpected agents: %+v", parts.Agents)
	}
	if len(parts.Tools) != 1 || parts.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", parts.Tools)
	}
	if len(parts.Skills) != 1 || parts.Skills[0].Name != "brief" {
		t.Fatalf("unexpected skills: %+v", parts.Skills)
	}
	if len(parts.Subgraphs) != 1 || parts.Subgraphs[0].Name != "prep" {
		t.Fatalf("unexpected subgraphs: %+v", parts.Subgraphs)
	}
	viaStudio := fw.Studio().Parts()
	if len(viaStudio.Agents) != 1 || viaStudio.Agents[0].Name != "assistant" {
		t.Fatalf("Studio.Parts mismatch: %+v", viaStudio)
	}
}

func TestFrameworkValidateStudioGraph(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-validate",
		Agents: map[string]core.Agent{
			"reviewer": {Name: "reviewer"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{ID: "b", Kind: string(core.NodeAgent), Ref: "reviewer"})
	edited.Workflow.Edges = append(edited.Workflow.Edges, graph.GraphEdge{From: "a", To: "b"})
	result, err := fw.ValidateStudioGraph(context.Background(), edited)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected valid graph, got %+v", result)
	}
}

func TestFrameworkRunStudioGraphSupportsAutonomousScenario(t *testing.T) {
	fw, err := agentflow.New(testAutonomousScenario(), agentflow.WithLLMGateway(fakeGateway{content: "studio autonomous"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStudioGraph(context.Background(), fw.ExportScenarioGraph(), agentflow.RunRequest{
		RunID:  "run-studio-autonomous",
		Agent:  "assistant",
		Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "studio autonomous" {
		t.Fatalf("unexpected autonomous Studio result: %+v", result)
	}
}

func TestFrameworkSaveStudioGraphWithScenarioPersistsNewAgent(t *testing.T) {
	base := core.Scenario{
		Name: "studio-scenario-save",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"existing": {Name: "existing", LLM: "default", Instructions: "existing"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "seed", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)}},
			},
		},
	}
	fw, err := agentflow.New(base, agentflow.WithLLMGateway(fakeGateway{content: "ok"}))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := graph.DeepCopyScenario(base)
	if err != nil {
		t.Fatal(err)
	}
	draft.Agents["writer"] = core.Agent{Name: "writer", LLM: "default", Instructions: "write"}
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{ID: "writer", Kind: string(core.NodeAgent), Ref: "writer"})
	edited.Workflow.Edges = append(edited.Workflow.Edges, graph.GraphEdge{From: "seed", To: "writer"})
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	result, err := fw.SaveStudioGraphWithScenario(context.Background(), edited, &draft, path)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScenarioName != base.Name {
		t.Fatalf("unexpected save result: %+v", result)
	}
	parts := fw.StudioParts()
	found := false
	for _, part := range parts.Agents {
		if part.Name == "writer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved live scenario omitted composed agent: %+v", parts.Agents)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "writer") {
		t.Fatalf("saved YAML omitted composed agent:\n%s", raw)
	}
}

func TestFrameworkScenarioDraftParticipatesInValidateAndExport(t *testing.T) {
	base := core.Scenario{
		Name: "studio-scenario-preview",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"existing": {Name: "existing", LLM: "default", Instructions: "existing"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "seed", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)}},
			},
		},
	}
	fw, err := agentflow.New(base)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := graph.DeepCopyScenario(base)
	if err != nil {
		t.Fatal(err)
	}
	draft.Agents["writer"] = core.Agent{Name: "writer", LLM: "default", Instructions: "write"}
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{ID: "writer", Kind: string(core.NodeAgent), Ref: "writer"})
	edited.Workflow.Edges = append(edited.Workflow.Edges, graph.GraphEdge{From: "seed", To: "writer"})

	validated, err := fw.ValidateStudioGraphWithScenario(context.Background(), edited, &draft)
	if err != nil {
		t.Fatal(err)
	}
	if !validated.Valid {
		t.Fatalf("scenario draft should validate before save: %+v", validated)
	}
	yamlResult, err := fw.GenerateStudioScenarioYAMLWithScenario(context.Background(), edited, &draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlResult.Code, "writer") {
		t.Fatalf("scenario draft missing from YAML export:\n%s", yamlResult.Code)
	}
	goResult, err := fw.GenerateStudioBuilderCodeWithScenario(context.Background(), edited, &draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goResult.Code, "writer") {
		t.Fatalf("scenario draft missing from Go export:\n%s", goResult.Code)
	}

	draft.Agents["existing"] = core.Agent{Name: "existing", LLM: "default", Instructions: "tampered"}
	rejected, err := fw.ValidateStudioGraphWithScenario(context.Background(), edited, &draft)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Valid || rejected.ErrorCode == "" {
		t.Fatalf("expected tampered existing agent to be rejected: %+v", rejected)
	}
}

func TestFrameworkForkAndCompareRuns(t *testing.T) {
	scenario := core.Scenario{
		Name: "fork-compare",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
					{ID: "b", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"y":2}}`)},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithCheckpointHistory(adapters.NewInMemoryCheckpointHistory()))
	if err != nil {
		t.Fatal(err)
	}
	parentID := "run-parent"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: parentID}); err != nil {
		t.Fatal(err)
	}
	fork, err := fw.ForkRun(context.Background(), parentID, 0)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := fw.ListRunThread(context.Background(), parentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) < 2 {
		t.Fatalf("expected parent and fork in thread, got %+v", thread)
	}
	compare, err := fw.CompareRuns(context.Background(), parentID, fork.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(compare.SharedSteps) == 0 {
		t.Fatalf("expected shared steps, got %+v", compare)
	}
}

func TestFrameworkListRunThreadFiltersTenant(t *testing.T) {
	fw, err := agentflow.New(core.Scenario{
		Name:   "thread-tenant",
		Agents: map[string]core.Agent{"noop": {Name: "noop"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	for _, snapshot := range []runstate.RunSnapshot{
		{RunID: "run-a", ThreadID: "shared-thread", TenantID: "tenant-a", ScenarioName: "thread-tenant", Status: runstate.RunStatusCompleted},
		{RunID: "run-b", ThreadID: "shared-thread", TenantID: "tenant-b", ScenarioName: "thread-tenant", Status: runstate.RunStatusCompleted},
	} {
		copy := snapshot
		if err := repo.Save(context.Background(), &copy, 0); err != nil {
			t.Fatal(err)
		}
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "viewer-a", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	thread, err := fw.ListRunThread(ctx, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) != 1 || thread[0].RunID != "run-a" {
		t.Fatalf("cross-tenant thread data leaked: %+v", thread)
	}
}

func TestFrameworkGenerateStudioBuilderCode(t *testing.T) {
	scenario := core.Scenario{
		Name: "codegen",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.GenerateStudioBuilderCode(context.Background(), fw.ExportScenarioGraph())
	if err != nil {
		t.Fatal(err)
	}
	if result.Language != "go" || result.Code == "" {
		t.Fatalf("unexpected codegen: %+v", result)
	}
}

func TestFrameworkGenerateStudioScenarioYAML(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-yaml",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.GenerateStudioScenarioYAML(context.Background(), fw.ExportScenarioGraph())
	if err != nil {
		t.Fatal(err)
	}
	if result.Language != "yaml" || result.Code == "" || !strings.Contains(result.Code, "scenario:") || !strings.Contains(result.Code, "studio-yaml") {
		t.Fatalf("unexpected yaml export: %+v", result)
	}
}

func TestFrameworkSaveStudioGraph(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-save",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{ID: "b", Kind: string(core.NodeTransform), Input: json.RawMessage(`{"set":{"y":2}}`)})
	result, err := fw.SaveStudioGraph(context.Background(), edited, path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != path || result.ScenarioName != "studio-save" {
		t.Fatalf("unexpected save result: %+v", result)
	}
	yamlBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := fw.ImportStudioScenarioYAML(context.Background(), yamlBytes, graph.ScenarioGraph{})
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Graph.Workflow.Nodes) != 2 {
		t.Fatalf("expected saved workflow with 2 nodes, got %+v", imported.Graph.Workflow.Nodes)
	}
}

func TestFrameworkRunStudioGraph(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-run",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithRunStateRepository(adapters.NewInMemoryRunStateRepository()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStudioGraph(context.Background(), fw.ExportScenarioGraph(), agentflow.RunRequest{RunID: "studio-run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != "studio-run-1" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected studio run result: %+v", result)
	}
}

func TestFrameworkValidateStudioGraphErrors(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-invalid",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{ID: "missing", Kind: string(core.NodeAgent), Ref: "unknown-agent"})
	result, err := fw.ValidateStudioGraph(context.Background(), edited)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatalf("expected invalid graph, got %+v", result)
	}
	if result.Error == "" || result.ErrorCode == "" {
		t.Fatalf("expected structured validation error, got %+v", result)
	}
}

func TestFrameworkRunStudioGraphInvalidGraph(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-run-invalid",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{ID: "bad", Kind: string(core.NodeAgent), Ref: "missing"})
	if _, err := fw.RunStudioGraph(context.Background(), edited, agentflow.RunRequest{RunID: "studio-bad"}); err == nil {
		t.Fatal("expected error for invalid studio graph")
	}
}

func TestFrameworkCompareRunsRequiresRunIDs(t *testing.T) {
	fw, err := agentflow.New(core.Scenario{
		Name:   "compare",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.CompareRuns(context.Background(), "", "run-b"); err == nil {
		t.Fatal("expected missing run id error")
	}
}

func TestFrameworkRunStudioGraphHybridWithWorkflow(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-hybrid",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "studio hybrid"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStudioGraph(context.Background(), fw.ExportScenarioGraph(), agentflow.RunRequest{
		RunID: "studio-hybrid-1", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "studio hybrid" {
		t.Fatalf("unexpected hybrid studio run: %+v", result)
	}
}

func TestFrameworkRunStudioGraphHybridAutonomousOnly(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-hybrid-auto",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode:     core.OrchestrationHybrid,
			Workflow: nil,
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "auto only"}))
	if err != nil {
		t.Fatal(err)
	}
	edited := fw.ExportScenarioGraph()
	edited.Workflow = nil
	result, err := fw.RunStudioGraph(context.Background(), edited, agentflow.RunRequest{
		RunID: "studio-auto-1", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected autonomous studio run: %+v", result)
	}
}

func TestFrameworkRunStudioGraphWorkflowFailure(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-run-fail",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Tools: map[string]core.Tool{
			"boom": {Name: "boom", Type: "builtin.boom", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "fail", Kind: core.NodeTool, Ref: "boom", Input: json.RawMessage(`{}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithToolExecutor("boom", errorTool{err: errors.New("boom")}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.RunStudioGraph(context.Background(), fw.ExportScenarioGraph(), agentflow.RunRequest{RunID: "studio-fail"}); err == nil {
		t.Fatal("expected workflow failure")
	}
}

func TestFrameworkImportStudioScenarioYAMLInvalid(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-import",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.ImportStudioScenarioYAML(context.Background(), []byte(":\n\tbad yaml"), graph.ScenarioGraph{}); err == nil {
		t.Fatal("expected yaml parse error")
	}
	if _, err := fw.ImportStudioScenarioYAML(context.Background(), []byte("scenario:\n  name: no-agents\n  orchestration:\n    mode: autonomous"), graph.ScenarioGraph{}); err == nil {
		t.Fatal("expected scenario validation error")
	}
}

type errorTool struct {
	err error
}

func (e errorTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, e.err
}
