package agentflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
)

type studioFramework struct {
	framework *Framework
	savePath  string
}

func (adapter *studioFramework) ListRunSteps(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunSteps(ctx, runID)
}

func (adapter *studioFramework) ResumeRunHITL(ctx context.Context, runID string, decision core.Decision, amendment json.RawMessage, continueExecution bool) (any, error) {
	return adapter.framework.ResumeRunByID(ctx, runID, decision, amendment, continueExecution)
}

func (adapter *studioFramework) ResumeFromStep(ctx context.Context, runID, nodeID string) (any, error) {
	return adapter.framework.ResumeFromStep(ctx, runID, nodeID)
}

func (adapter *studioFramework) ListRunCheckpoints(ctx context.Context, runID string, limit int) (any, error) {
	return adapter.framework.ListRunCheckpoints(ctx, runID, limit)
}

func (adapter *studioFramework) GetRunCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.GetRunCheckpoint(ctx, runID, version)
}

func (adapter *studioFramework) ResumeFromCheckpoint(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.ResumeFromCheckpoint(ctx, runID, version)
}

func (adapter *studioFramework) ExportScenarioGraph() any {
	return adapter.framework.ExportScenarioGraph()
}

func (adapter *studioFramework) ValidateStudioGraph(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.ValidateStudioGraph(ctx, graph)
}

func (adapter *studioFramework) GenerateStudioBuilderCode(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.GenerateStudioBuilderCode(ctx, graph)
}

func (adapter *studioFramework) GenerateStudioScenarioYAML(ctx context.Context, edited any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.GenerateStudioScenarioYAML(ctx, graph)
}

func (adapter *studioFramework) ImportStudioScenarioYAML(ctx context.Context, yamlData []byte, layout any) (any, error) {
	var layoutGraph graph.ScenarioGraph
	if layout != nil {
		var err error
		layoutGraph, err = decodeStudioGraph(layout)
		if err != nil {
			return nil, err
		}
	}
	return adapter.framework.ImportStudioScenarioYAML(ctx, yamlData, layoutGraph)
}

func (adapter *studioFramework) RunStudioGraph(ctx context.Context, edited any, req any) (any, error) {
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	runReq, err := decodeStudioRunRequest(req)
	if err != nil {
		return nil, err
	}
	return adapter.framework.RunStudioGraph(ctx, graph, runReq)
}

func decodeStudioRunRequest(value any) (RunRequest, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return RunRequest{}, fmt.Errorf("studio run request: encode: %w", err)
	}
	var req RunRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return RunRequest{}, fmt.Errorf("studio run request: decode: %w", err)
	}
	return req, nil
}

func (adapter *studioFramework) CompareRuns(ctx context.Context, runA, runB string) (any, error) {
	return adapter.framework.CompareRuns(ctx, runA, runB)
}

func (adapter *studioFramework) ListRunThread(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunThread(ctx, runID)
}

func (adapter *studioFramework) ForkRun(ctx context.Context, runID string, version int64) (any, error) {
	return adapter.framework.ForkRun(ctx, runID, version)
}

func (adapter *studioFramework) SaveStudioGraph(ctx context.Context, edited any) (any, error) {
	if adapter.savePath == "" {
		return nil, fmt.Errorf("studio save path is not configured")
	}
	graph, err := decodeStudioGraph(edited)
	if err != nil {
		return nil, err
	}
	return adapter.framework.SaveStudioGraph(ctx, graph, adapter.savePath)
}

func decodeStudioGraph(value any) (graph.ScenarioGraph, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return graph.ScenarioGraph{}, fmt.Errorf("studio graph: encode: %w", err)
	}
	var out graph.ScenarioGraph
	if err := json.Unmarshal(raw, &out); err != nil {
		return graph.ScenarioGraph{}, fmt.Errorf("studio graph: decode: %w", err)
	}
	return out, nil
}
