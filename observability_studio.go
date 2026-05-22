package agentflow

import (
	"context"
)

type studioFramework struct {
	framework *Framework
}

func (adapter *studioFramework) ListRunSteps(ctx context.Context, runID string) (any, error) {
	return adapter.framework.ListRunSteps(ctx, runID)
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
