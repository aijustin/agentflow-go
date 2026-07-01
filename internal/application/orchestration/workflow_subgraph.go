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
	// prefix is local to this subgraph invocation and is what withStepPrefix
	// composes with any prefix already on ctx (e.g. this subgraph is itself
	// nested inside another subgraph or a loop). fullPrefix is the absolute,
	// fully-composed prefix that child step-output ids actually carry in the
	// snapshot, and must be used whenever matching against snapshot.StepOutputs
	// or snapshot.CurrentNodeID directly.
	prefix := subgraphStepPrefix(node.ID)
	fullPrefix := storageNodeID(ctx, node.ID) + subgraphStepDelimiter
	r.emitJSON(ctx, core.EventSubgraphStarted, scenario.Name, runID, map[string]any{
		"node_id":      node.ID,
		"subgraph_ref": node.Ref,
	})
	subScenario := scenario
	subScenario.Orchestration = core.Orchestration{
		Mode:        core.OrchestrationFixedWorkflow,
		Workflow:    &sub,
		Workflows:   scenario.Orchestration.Workflows,
		MaxParallel: scenario.Orchestration.MaxParallel,
		HumanInLoop: scenario.Orchestration.HumanInLoop,
		Planning:    scenario.Orchestration.Planning,
	}
	subCtx := withStepPrefix(ctx, prefix)
	alreadyDone := make(map[string]bool)
	if r.runs != nil {
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return err
		}
		for id := range snapshot.StepOutputs {
			if strings.HasPrefix(id, fullPrefix) {
				alreadyDone[bareNodeID(id, fullPrefix)] = true
			}
		}
		// human_gate/interrupt nodes never write a step output, so the loop
		// above alone can never mark them done. If CurrentNodeID (stored
		// with its full storage-scoped id when the gate paused) points at a
		// gate inside this subgraph and the gate has since been resolved
		// (PendingGate cleared), mark it done too, otherwise resuming would
		// re-run the subgraph from its first node and re-pause on a gate
		// that was already approved.
		if snapshot.CurrentNodeID != "" && snapshot.PendingGate == nil && strings.HasPrefix(snapshot.CurrentNodeID, fullPrefix) {
			bareID := bareNodeID(snapshot.CurrentNodeID, fullPrefix)
			if gateNode, ok := nodeByID(sub, bareID); ok && (gateNode.Kind == core.NodeHumanGate || gateNode.Interrupt) {
				alreadyDone[bareID] = true
			}
		}
	}
	if err := r.run(subCtx, subScenario, runID, alreadyDone); err != nil {
		return err
	}
	produced := make(map[string]json.RawMessage)
	if r.runs != nil {
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return err
		}
		// Aggregate every child output currently in the snapshot, not just
		// ones produced by this call: a resume after a pause must still
		// surface child outputs that were produced before the pause, or
		// downstream nodes reading steps.<subgraph>.steps.<child> would see
		// them go missing.
		for id, ref := range snapshot.StepOutputs {
			if !strings.HasPrefix(id, fullPrefix) {
				continue
			}
			bareID := bareNodeID(id, fullPrefix)
			if ref.Inline != nil {
				produced[bareID] = ref.Inline
				continue
			}
			if ref.Blob != nil && r.blobs != nil {
				// Hydrate externalized child output so the aggregated subgraph
				// result carries the real value, not an opaque marker that
				// downstream nodes cannot read. The parent saveStepOutput
				// re-thresholds and re-externalizes the aggregate if it is large.
				raw, err := r.blobs.Get(ctx, *ref.Blob)
				if err != nil {
					return fmt.Errorf("orchestration: subgraph %q hydrate step %q: %w", node.ID, bareID, err)
				}
				produced[bareID] = raw
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
