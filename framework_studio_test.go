package agentflow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

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
	fw, err := agentflow.New(scenario, agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()))
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
	fw, err := agentflow.New(scenario, agentflow.WithRunStateRepository(agentflow.NewInMemoryRunStateRepository()))
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
