package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func (r *WorkflowRunner) runSubgraphNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	sub, ok := scenario.Orchestration.Workflows[node.Ref]
	if !ok {
		return fmt.Errorf("orchestration: subgraph node %q references unknown workflow %q", node.ID, node.Ref)
	}
	prefix := subgraphStepPrefix(node.ID)
	r.emitJSON(ctx, core.EventSubgraphStarted, scenario.Name, runID, map[string]any{
		"node_id":      node.ID,
		"subgraph_ref": node.Ref,
	})
	var before map[string]struct{}
	if r.runs != nil {
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return err
		}
		before = make(map[string]struct{}, len(snapshot.StepOutputs))
		for id := range snapshot.StepOutputs {
			before[id] = struct{}{}
		}
	}
	subScenario := scenario
	subScenario.Orchestration = core.Orchestration{
		Mode:        core.OrchestrationFixedWorkflow,
		Workflow:    &sub,
		Workflows:   scenario.Orchestration.Workflows,
		MaxParallel: scenario.Orchestration.MaxParallel,
	}
	subCtx := withStepPrefix(ctx, prefix)
	if err := r.run(subCtx, subScenario, runID, nil); err != nil {
		return err
	}
	produced := make(map[string]json.RawMessage)
	if r.runs != nil {
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return err
		}
		for id, ref := range snapshot.StepOutputs {
			if before != nil {
				if _, existed := before[id]; existed {
					continue
				}
			}
			if !strings.HasPrefix(id, prefix) {
				continue
			}
			bareID := bareNodeID(id, prefix)
			if ref.Inline != nil {
				produced[bareID] = ref.Inline
				continue
			}
			produced[bareID] = json.RawMessage(`{"external":true}`)
		}
	}
	r.emitJSON(ctx, core.EventSubgraphCompleted, scenario.Name, runID, map[string]any{
		"node_id":      node.ID,
		"subgraph_ref": node.Ref,
		"step_count":   len(produced),
	})
	return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]any{
		"subgraph": node.Ref,
		"steps":    produced,
	})
}
