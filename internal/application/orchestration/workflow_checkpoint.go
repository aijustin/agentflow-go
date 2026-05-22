package orchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// downstreamNodeIDs returns the start node and every workflow node that
// transitively depends on it via edges or depends_on.
func downstreamNodeIDs(workflow core.Workflow, start string) map[string]bool {
	deps := dependencies(workflow)
	dependents := make(map[string]map[string]bool, len(workflow.Nodes))
	for nodeID, nodeDeps := range deps {
		for dep := range nodeDeps {
			if dependents[dep] == nil {
				dependents[dep] = make(map[string]bool)
			}
			dependents[dep][nodeID] = true
		}
	}
	out := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for child := range dependents[current] {
			if out[child] {
				continue
			}
			out[child] = true
			queue = append(queue, child)
		}
	}
	return out
}

func truncateStepOutputsForRerun(outputs map[string]runstate.StepOutputRef, workflow core.Workflow, fromNodeID string) {
	if len(outputs) == 0 {
		return
	}
	remove := downstreamNodeIDs(workflow, fromNodeID)
	for stepID := range outputs {
		for nodeID := range remove {
			if stepID == nodeID || strings.HasPrefix(stepID, nodeID+".") {
				delete(outputs, stepID)
				break
			}
		}
	}
}

func alreadyDoneFromSnapshot(scenario core.Scenario, snapshot runstate.RunSnapshot) map[string]bool {
	done := make(map[string]bool, len(snapshot.StepOutputs)+1)
	for nodeID := range snapshot.StepOutputs {
		done[nodeID] = true
	}
	if snapshot.CurrentNodeID != "" {
		if node, ok := workflowNodeByID(scenario, snapshot.CurrentNodeID); ok && node.Kind == core.NodeHumanGate && snapshot.PendingGate == nil {
			done[snapshot.CurrentNodeID] = true
		}
	}
	return done
}

// ResumeFromStep truncates outputs for the node and its downstream steps, then
// reruns the workflow from that node forward.
func (r *WorkflowRunner) ResumeFromStep(ctx context.Context, scenario core.Scenario, runID, nodeID string) error {
	if scenario.Orchestration.Workflow == nil {
		return fmt.Errorf("orchestration: workflow is required")
	}
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for workflow resume")
	}
	node, ok := workflowNodeByID(scenario, nodeID)
	if !ok {
		return fmt.Errorf("orchestration: workflow node %q not found", nodeID)
	}
	if loopBodyNodeIDs(*scenario.Orchestration.Workflow)[nodeID] {
		return fmt.Errorf("orchestration: cannot resume from loop body node %q", nodeID)
	}
	if node.Kind == core.NodeHumanGate {
		return fmt.Errorf("orchestration: cannot resume from human_gate node %q; use gate resume instead", nodeID)
	}

	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return err
	}
	switch snapshot.Status {
	case runstate.RunStatusRunning, runstate.RunStatusPaused, runstate.RunStatusCompleted, runstate.RunStatusFailed:
	default:
		return fmt.Errorf("orchestration: workflow resume from step requires running, paused, completed, or failed snapshot, got %s", snapshot.Status)
	}

	if snapshot.StepOutputs == nil {
		snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	truncateStepOutputsForRerun(snapshot.StepOutputs, *scenario.Orchestration.Workflow, nodeID)
	snapshot.Status = runstate.RunStatusRunning
	snapshot.CurrentNodeID = ""
	snapshot.PendingGate = nil
	delete(snapshot.StepOutputs, "final")
	if err := r.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
		return err
	}

	ctx, cancel := workflowTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	return r.run(ctx, scenario, runID, alreadyDoneFromSnapshot(scenario, snapshot))
}
