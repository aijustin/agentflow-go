package agentflow

import (
	"context"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func (f *Framework) hydrateRunRequest(ctx context.Context, req RunRequest, snapshot runstate.RunSnapshot) (RunRequest, error) {
	if req.Context != nil || len(snapshot.StepOutputs) == 0 {
		return req, nil
	}
	raw, err := runstate.HydrateStepContext(ctx, f.blobs, snapshot.StepOutputs)
	if err != nil {
		return req, fmt.Errorf("agentflow: hydrate workflow context: %w", err)
	}
	req.Context = raw
	return req, nil
}

func completedHybridResult(snapshot runstate.RunSnapshot) (RunResult, bool) {
	if snapshot.Status != runstate.RunStatusCompleted {
		return RunResult{}, false
	}
	return RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusCompleted, Output: "hybrid run already completed"}, true
}
