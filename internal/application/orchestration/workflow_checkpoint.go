package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// conversationMemoryWatermarksVar mirrors the runtime engine's snapshot
// variable key (see runtime.conversationMemoryWatermarksVar); the two literals
// must stay in sync.
const conversationMemoryWatermarksVar = "conversation_memory_watermarks"

type conversationWatermarkEntry struct {
	Agent string `json:"agent"`
	Len   int    `json:"len"`
}

// rewindConversationMemory truncates each affected agent's run-scoped
// conversation memory back to the earliest watermark recorded for any node
// being discarded, so a re-run does not see turns from the rewound (future)
// portion of the run. It is a no-op when no rewinder is wired or no watermarks
// were recorded.
func (r *WorkflowRunner) rewindConversationMemory(ctx context.Context, runID string, variables map[string]json.RawMessage, removed map[string]bool) error {
	if r.memory == nil || len(variables) == 0 || len(removed) == 0 {
		return nil
	}
	raw := variables[conversationMemoryWatermarksVar]
	if len(raw) == 0 {
		return nil
	}
	var marks map[string]conversationWatermarkEntry
	if err := json.Unmarshal(raw, &marks); err != nil {
		return nil
	}
	keep := make(map[string]int)
	for storedNodeID, entry := range marks {
		if !watermarkNodeDiscarded(removed, storedNodeID) {
			continue
		}
		if existing, ok := keep[entry.Agent]; !ok || entry.Len < existing {
			keep[entry.Agent] = entry.Len
		}
	}
	for agentName, k := range keep {
		if err := r.memory.RewindConversationMemory(ctx, runID, agentName, k); err != nil {
			return err
		}
	}
	return nil
}

func watermarkNodeDiscarded(removed map[string]bool, storedNodeID string) bool {
	for nodeID := range removed {
		if nodeOrDescendantMatches(storedNodeID, nodeID) {
			return true
		}
	}
	return false
}

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

// nodeOrDescendantMatches reports whether stepID names nodeID itself or a
// step nested under it (subgraph child, loop body, etc.), using the same
// storage-id prefixing convention as storageNodeID.
func nodeOrDescendantMatches(stepID, nodeID string) bool {
	return stepID == nodeID || strings.HasPrefix(stepID, nodeID+".") || strings.HasPrefix(stepID, nodeID+subgraphStepDelimiter)
}

func truncateStepOutputsForRerun(outputs map[string]runstate.StepOutputRef, workflow core.Workflow, fromNodeID string) {
	if len(outputs) == 0 {
		return
	}
	remove, bodyIDs := rerunRemovalSets(workflow, fromNodeID)
	for stepID := range outputs {
		matched := false
		for nodeID := range remove {
			if nodeOrDescendantMatches(stepID, nodeID) {
				delete(outputs, stepID)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for bodyID := range bodyIDs {
			if nodeOrDescendantMatches(stepID, bodyID) {
				delete(outputs, stepID)
				break
			}
		}
	}
}

// rerunRemovalSets computes the node IDs whose cached state must be
// discarded when rerunning a workflow from fromNodeID: every node
// transitively downstream of it, plus (since loop body nodes are excluded
// from the dependency graph and only run through their owning loop node)
// the body node IDs of any loop node being rerun.
func rerunRemovalSets(workflow core.Workflow, fromNodeID string) (remove map[string]bool, bodyIDs map[string]bool) {
	remove = downstreamNodeIDs(workflow, fromNodeID)
	bodyIDs = make(map[string]bool)
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
	return remove, bodyIDs
}

// clearLoopProgressForRerun deletes any loop_progress:<nodeID> variable for
// a loop node being rerun (or nested inside a subgraph being rerun). Without
// this, ResumeFromStep only truncates StepOutputs and runLoopNode would see
// the loop's stale iteration count from before the rerun, causing it to skip
// iterations it has not actually re-executed yet or wrongly report the loop
// as already completed.
func clearLoopProgressForRerun(variables map[string]json.RawMessage, workflow core.Workflow, fromNodeID string) {
	if len(variables) == 0 {
		return
	}
	remove, _ := rerunRemovalSets(workflow, fromNodeID)
	const progressPrefix = "loop_progress:"
	for key := range variables {
		storedNodeID, ok := strings.CutPrefix(key, progressPrefix)
		if !ok {
			continue
		}
		for nodeID := range remove {
			if nodeOrDescendantMatches(storedNodeID, nodeID) {
				delete(variables, key)
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

	// Rewind conversation memory for every node that ran after the restore
	// point (present in the current snapshot but not the restored one) so the
	// re-run does not inherit those discarded turns.
	removed := make(map[string]bool)
	for stepID := range current.StepOutputs {
		if _, ok := restored.StepOutputs[stepID]; !ok {
			removed[stepID] = true
		}
	}
	if err := r.rewindConversationMemory(ctx, runID, current.Variables, removed); err != nil {
		return err
	}

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
	bodyOnly, err := loopBodyNodeIDs(*scenario.Orchestration.Workflow)
	if err != nil {
		return err
	}
	if bodyOnly[nodeID] {
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
	clearLoopProgressForRerun(snapshot.Variables, *scenario.Orchestration.Workflow, nodeID)
	remove, bodyIDs := rerunRemovalSets(*scenario.Orchestration.Workflow, nodeID)
	removed := make(map[string]bool, len(remove)+len(bodyIDs))
	for id := range remove {
		removed[id] = true
	}
	for id := range bodyIDs {
		removed[id] = true
	}
	if err := r.rewindConversationMemory(ctx, runID, snapshot.Variables, removed); err != nil {
		return err
	}
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
