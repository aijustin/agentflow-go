package httpx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
)

func TestStudioFrameworkDelegatesToFramework(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-delegate",
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
	fw, err := agentflow.New(scenario, agentflow.WithCheckpointHistory(adapters.NewInMemoryCheckpointHistory()))
	if err != nil {
		t.Fatal(err)
	}
	savePath := filepath.Join(t.TempDir(), "scenario.yaml")
	adapter := &studioFramework{framework: fw, savePath: savePath}
	runID := "studio-delegate-run"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ListRunSteps(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := adapter.ListRunCheckpoints(context.Background(), runID, 10)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(checkpoints)
	var listed struct {
		Checkpoints []struct {
			Version int64 `json:"version"`
		} `json:"checkpoints"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Checkpoints) == 0 {
		t.Fatal("expected checkpoint history")
	}
	version := listed.Checkpoints[0].Version
	if _, err := adapter.GetRunCheckpoint(context.Background(), runID, version); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResumeFromStep(context.Background(), runID, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResumeFromCheckpoint(context.Background(), runID, version); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResumeRunHITL(context.Background(), runID, core.DecisionApprove, nil, false); err == nil {
		t.Fatal("expected HITL resume error without pending gate")
	}
	exported, ok := adapter.ExportScenarioGraph().(graph.ScenarioGraph)
	if !ok {
		t.Fatal("expected exported scenario graph")
	}
	if _, err := adapter.ValidateStudioGraph(context.Background(), exported); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.GenerateStudioBuilderCode(context.Background(), exported); err != nil {
		t.Fatal(err)
	}
	yamlResult, err := adapter.GenerateStudioScenarioYAML(context.Background(), exported)
	if err != nil {
		t.Fatal(err)
	}
	gen, ok := yamlResult.(agentflow.CodegenResult)
	if !ok || gen.Code == "" {
		t.Fatalf("unexpected yaml result: %#v", yamlResult)
	}
	if _, err := adapter.ImportStudioScenarioYAML(context.Background(), []byte(gen.Code), exported); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.RunStudioGraph(context.Background(), exported, map[string]any{"run_id": "studio-graph-run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CompareRuns(context.Background(), runID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ListRunThread(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ForkRun(context.Background(), runID, version); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.SaveStudioGraph(context.Background(), exported); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(savePath); err != nil {
		t.Fatalf("expected saved graph file: %v", err)
	}
}

func TestStudioFrameworkSaveRequiresPath(t *testing.T) {
	fw, err := agentflow.New(core.Scenario{
		Name: "empty",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &studioFramework{framework: fw}
	if _, err := adapter.SaveStudioGraph(context.Background(), map[string]any{
		"name":     "empty",
		"workflow": map[string]any{"nodes": []any{}, "edges": []any{}},
	}); err == nil {
		t.Fatal("expected save path error")
	}
}

func TestDecodeStudioGraphAndRunRequest(t *testing.T) {
	graph, err := decodeStudioGraph(map[string]any{
		"name":     "g",
		"workflow": map[string]any{"nodes": []any{}, "edges": []any{}},
	})
	if err != nil || graph.Name != "g" || graph.Workflow == nil {
		t.Fatalf("graph=%+v err=%v", graph, err)
	}
	req, err := decodeStudioRunRequest(map[string]any{"run_id": "r1", "prompt": "hi"})
	if err != nil || req.RunID != "r1" || req.Prompt != "hi" {
		t.Fatalf("req=%+v err=%v", req, err)
	}
	if _, err := decodeStudioGraph(make(chan int)); err == nil {
		t.Fatal("expected encode error for unsupported graph type")
	}
}
