package orchestration

import (
	"context"
	"encoding/json"
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
	// Loop body nodes are excluded from the dependency graph (they only run
	// through their owning loop node), so downstreamNodeIDs never reaches
	// them directly. If a loop node being rerun is in remove, its body
	// nodes' cached outputs must be truncated too: the loop's own resume
	// idempotency check (see runLoopNode) treats an existing body output as
	// "already ran" for the iteration it is about to resume, and a stale
	// output from before the rerun would make it wrongly skip that body
	// node instead of re-running it.
	bodyIDs := make(map[string]bool)
	for _, node := range workflow.Nodes {
		if node.Kind != core.NodeLoop || !remove[node.ID] || len(node.Input) == 0 {
			continue
		}
		var spec loopSpec
		if err := json.Unmarshal(node.Input, &spec); err != nil {
			continue
		}
		for _, bodyID := range spec.Body {
			bodyIDs[bodyID] = true
		}
	}
	for stepID := range outputs {
		matched := false
		for nodeID := range remove {
			if stepID == nodeID || strings.HasPrefix(stepID, nodeID+".") || strings.HasPrefix(stepID, nodeID+subgraphStepDelimiter) {
				delete(outputs, stepID)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for bodyID := range bodyIDs {
			if stepID == bodyID || strings.HasPrefix(stepID, bodyID+".") || strings.HasPrefix(stepID, bodyID+subgraphStepDelimiter) {
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
		if node, ok := workflowNodeByID(scenario, snapshot.CurrentNodeID); ok && snapshot.PendingGate == nil {
			if node.Kind == core.NodeHumanGate || node.Interrupt {
				done[snapshot.CurrentNodeID] = true
			}
		}
	}
	return done
}

// RestoreSnapshotAndRun replaces the current run snapshot with a historical
// revision and reruns the workflow from that restored state forward.
func (r *WorkflowRunner) RestoreSnapshotAndRun(ctx context.Context, scenario core.Scenario, runID string, restored runstate.RunSnapshot) error {
	if scenario.Orchestration.Workflow == nil {
		return fmt.Errorf("orchestration: workflow is required")
	}
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for workflow restore")
	}

	current, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return err
	}
	switch current.Status {
	case runstate.RunStatusRunning, runstate.RunStatusPaused, runstate.RunStatusCompleted, runstate.RunStatusFailed:
	default:
		return fmt.Errorf("orchestration: workflow restore requires running, paused, completed, or failed snapshot, got %s", current.Status)
	}

	// alreadyDoneFromSnapshot treats a nil PendingGate as "this gate was
	// already resolved". Restoring always transitions the persisted
	// snapshot to Running with PendingGate cleared (a restored run cannot
	// stay paused on a gate token minted against the old snapshot version),
	// but that must not be conflated with "the gate was actually approved":
	// keep the *restored* PendingGate for the done-computation below so a
	// snapshot that was genuinely captured mid-approval re-executes (and
	// re-pauses on) that gate instead of silently skipping it.
	restoredPendingGate := restored.PendingGate

	snapshot := restored
	snapshot.RunID = runID
	snapshot.Version = current.Version
	snapshot.Status = runstate.RunStatusRunning
	snapshot.CurrentNodeID = restored.CurrentNodeID
	snapshot.PendingGate = nil
	if snapshot.StepOutputs == nil {
		snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	delete(snapshot.StepOutputs, "final")
	saveCtx := runstate.ContextWithStatusTransitionOverride(ctx)
	if err := r.runs.Save(saveCtx, &snapshot, current.Version); err != nil {
		return err
	}

	doneSnapshot := snapshot
	doneSnapshot.PendingGate = restoredPendingGate

	ctx, cancel := workflowTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	return r.run(ctx, scenario, runID, alreadyDoneFromSnapshot(scenario, doneSnapshot))
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
	if node.Interrupt {
		return fmt.Errorf("orchestration: cannot resume from interrupt node %q; use gate resume instead", nodeID)
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
	saveCtx := runstate.ContextWithStatusTransitionOverride(ctx)
	if err := r.runs.Save(saveCtx, &snapshot, snapshot.Version); err != nil {
		return err
	}

	ctx, cancel := workflowTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	return r.run(ctx, scenario, runID, alreadyDoneFromSnapshot(scenario, snapshot))
}
