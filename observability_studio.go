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

func (adapter *studioFramework) ExportScenarioGraph() any {
	return adapter.framework.ExportScenarioGraph()
}
