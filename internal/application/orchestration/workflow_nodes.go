package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type parallelGroupSpec struct {
	Refs    []string `json:"refs"`
	Tools   []string `json:"tools"`
	OnError string   `json:"on_error,omitempty"`
}

type loopSpec struct {
	Body          []string `json:"body"`
	MaxIterations int      `json:"max_iterations,omitempty"`
	Until         string   `json:"until,omitempty"`
}

func loopBodyNodeIDs(workflow core.Workflow) (map[string]bool, error) {
	ids := make(map[string]bool)
	for _, node := range workflow.Nodes {
		if node.Kind != core.NodeLoop || len(node.Input) == 0 {
			continue
		}
		var spec loopSpec
		if err := json.Unmarshal(node.Input, &spec); err != nil {
			return nil, fmt.Errorf("orchestration: loop node %q decode input: %w", node.ID, err)
		}
		for _, bodyID := range spec.Body {
			ids[bodyID] = true
		}
	}
	return ids, nil
}

func (r *WorkflowRunner) runParallelGroupNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	var spec parallelGroupSpec
	if len(node.Input) > 0 {
		if err := json.Unmarshal(node.Input, &spec); err != nil {
			return fmt.Errorf("orchestration: parallel_group node %q decode input: %w", node.ID, err)
		}
	}
	if len(spec.Refs) == 0 && node.Ref != "" {
		spec.Refs = []string{node.Ref}
	}
	if len(spec.Refs) == 0 && len(spec.Tools) == 0 {
		return fmt.Errorf("orchestration: parallel_group node %q requires refs or tools", node.ID)
	}
	collectErrors := strings.EqualFold(spec.OnError, "collect_errors")
	memberCount := len(spec.Refs) + len(spec.Tools)
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, memberCount)
	outputs := make(map[string]any, memberCount)
	var mu sync.Mutex
	var collected []string

	// Bound concurrent member execution the same way map nodes do, so a
	// parallel_group with a large refs/tools list cannot fan out into an
	// unbounded number of concurrent LLM/tool calls.
	limit := firstPositive(scenario.Orchestration.MaxParallel, scenario.Runtime.MaxParallel)
	if limit <= 0 {
		limit = defaultMapConcurrency
	}
	if limit > memberCount {
		limit = memberCount
	}
	sem := make(chan struct{}, limit)

	runMember := func(memberKey string, run func(context.Context) error, decode func() (any, error)) {
		defer wg.Done()
		if err := groupCtx.Err(); err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-groupCtx.Done():
			return
		}
		r.emitJSON(ctx, core.EventStepStarted, scenario.Name, runID, map[string]any{
			"node_id":         memberKey,
			"parallel_group":  node.ID,
			"parallel_member": true,
		})
		if err := run(groupCtx); err != nil {
			// A pause must always halt the whole group, even under
			// collect_errors: it is not a failure to be aggregated.
			var paused WorkflowPausedError
			if errors.As(err, &paused) {
				cancel()
				errs <- err
				return
			}
			if collectErrors {
				mu.Lock()
				collected = append(collected, err.Error())
				mu.Unlock()
				errs <- nil
				return
			}
			// Do not cancel the group here: a sibling that hasn't
			// acquired its semaphore slot yet may still pause, and pause
			// must always take precedence over a plain error regardless
			// of scheduling order (see the pause-preference drain
			// below). Cancelling would race the semaphore-acquire select
			// of a not-yet-started sibling and could make it exit
			// without ever running, silently dropping its pause.
			errs <- err
			return
		}
		value, err := decode()
		if err != nil {
			if collectErrors {
				mu.Lock()
				collected = append(collected, err.Error())
				mu.Unlock()
				errs <- nil
				return
			}
			errs <- err
			return
		}
		mu.Lock()
		outputs[memberKey] = value
		mu.Unlock()
		r.emitJSON(ctx, core.EventStepCompleted, scenario.Name, runID, map[string]any{
			"node_id":         memberKey,
			"parallel_group":  node.ID,
			"parallel_member": true,
		})
	}

	for index, ref := range spec.Refs {
		wg.Add(1)
		go func(index int, agentName string) {
			childID := fmt.Sprintf("%s.agent.%s.%d", node.ID, agentName, index)
			memberKey := "agent:" + agentName
			if index > 0 {
				memberKey = fmt.Sprintf("%s:%d", memberKey, index)
			}
			if raw, ok, err := r.stepOutputRaw(groupCtx, runID, childID); err == nil && ok {
				var value any
				if err := json.Unmarshal(raw, &value); err == nil {
					mu.Lock()
					outputs[memberKey] = value
					mu.Unlock()
					wg.Done()
					return
				}
			}
			child := core.WorkflowNode{ID: childID, Kind: core.NodeAgent, Ref: agentName}
			runMember(memberKey, func(runCtx context.Context) error {
				return r.runAgentNode(withSkipCurrentNode(runCtx), scenario, child, runID)
			}, func() (any, error) {
				raw, ok, err := r.stepOutputRaw(groupCtx, runID, childID)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("orchestration: parallel_group node %q missing output for agent %q", node.ID, agentName)
				}
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, fmt.Errorf("orchestration: parallel_group node %q decode output for agent %q: %w", node.ID, agentName, err)
				}
				return value, nil
			})
		}(index, ref)
	}
	for index, ref := range spec.Tools {
		wg.Add(1)
		go func(index int, toolName string) {
			childID := fmt.Sprintf("%s.tool.%s.%d", node.ID, toolName, index)
			memberKey := "tool:" + toolName
			if index > 0 {
				memberKey = fmt.Sprintf("%s:%d", memberKey, index)
			}
			if raw, ok, err := r.stepOutputRaw(groupCtx, runID, childID); err == nil && ok {
				var value any
				if err := json.Unmarshal(raw, &value); err == nil {
					mu.Lock()
					outputs[memberKey] = value
					mu.Unlock()
					wg.Done()
					return
				}
			}
			child := core.WorkflowNode{ID: childID, Kind: core.NodeTool, Ref: toolName}
			runMember(memberKey, func(runCtx context.Context) error {
				return r.runToolNode(withSkipCurrentNode(runCtx), scenario, child, runID)
			}, func() (any, error) {
				raw, ok, err := r.stepOutputRaw(groupCtx, runID, childID)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("orchestration: parallel_group node %q missing output for tool %q", node.ID, toolName)
				}
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, fmt.Errorf("orchestration: parallel_group node %q decode output for tool %q: %w", node.ID, toolName, err)
				}
				return value, nil
			})
		}(index, ref)
	}
	wg.Wait()
	close(errs)
	// If the caller's context was cancelled or timed out, treat that as a
	// failure even if no member happened to report it: a member that exits
	// early on ctx cancellation (see the groupCtx.Err() check above) never
	// sends to errs, which would otherwise let a partially-completed group
	// look like a success.
	if err := ctx.Err(); err != nil {
		return err
	}
	var firstErr error
	for err := range errs {
		if err == nil {
			continue
		}
		var paused WorkflowPausedError
		if errors.As(err, &paused) {
			return err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return firstErr
	}
	result := map[string]any{"members": outputs}
	if len(collected) > 0 {
		result["errors"] = collected
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, result)
}

func loopProgressVariable(storedNodeID string) string {
	return "loop_progress:" + storedNodeID
}

type loopProgress struct {
	Iterations int  `json:"iterations"`
	Completed  bool `json:"completed"`
}

// loadLoopProgress reads how many iterations of nodeID already completed on
// a prior attempt, so a resumed run continues instead of restarting at
// iteration 1. Progress is stored in run Variables (not StepOutputs) so it
// never causes the node to look "done" to the generic dependency scheduler,
// which treats any StepOutputs entry as a finished node.
func (r *WorkflowRunner) loadLoopProgress(ctx context.Context, runID, storedNodeID string) (loopProgress, error) {
	if r.runs == nil {
		return loopProgress{}, nil
	}
	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return loopProgress{}, err
	}
	raw, ok := snapshot.Variables[loopProgressVariable(storedNodeID)]
	if !ok || len(raw) == 0 {
		return loopProgress{}, nil
	}
	var progress loopProgress
	if err := json.Unmarshal(raw, &progress); err != nil {
		return loopProgress{}, nil
	}
	return progress, nil
}

func (r *WorkflowRunner) saveLoopProgress(ctx context.Context, runID, storedNodeID string, progress loopProgress) error {
	raw, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	return r.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables[loopProgressVariable(storedNodeID)] = raw
		return nil
	})
}

func (r *WorkflowRunner) runLoopNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if len(node.Input) == 0 {
		return fmt.Errorf("orchestration: loop node %q input is required", node.ID)
	}
	var spec loopSpec
	if err := json.Unmarshal(node.Input, &spec); err != nil {
		return fmt.Errorf("orchestration: loop node %q decode input: %w", node.ID, err)
	}
	if len(spec.Body) == 0 {
		return fmt.Errorf("orchestration: loop node %q requires body node ids", node.ID)
	}
	nodes := workflowNodesByID(scenario)
	maxIterations := firstPositive(spec.MaxIterations, 3)
	storedNodeID := storageNodeID(ctx, node.ID)

	progress, err := r.loadLoopProgress(ctx, runID, storedNodeID)
	if err != nil {
		return err
	}
	if progress.Completed {
		// A prior attempt already finished this loop; a resume must not
		// re-run completed iterations or their side effects.
		return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]any{
			"iterations": progress.Iterations,
			"completed":  true,
		})
	}

	actualIterations := progress.Iterations
	untilMet := spec.Until == ""
	// resumeIteration is the only iteration that could have partially run
	// before a pause or a hard-failure retry of this loop node; every later
	// iteration in this invocation starts completely fresh. Body node
	// outputs intentionally keep their plain (unprefixed) node id across
	// iterations - other nodes and `until` conditions read the body's
	// *latest* output via that id - so the done-check below is scoped to
	// resumeIteration only, otherwise a body output left over from an
	// earlier iteration would make every later iteration look already done.
	resumeIteration := progress.Iterations + 1
	for iteration := resumeIteration; iteration <= maxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if spec.Until != "" {
			ok, err := r.conditionEnabled(ctx, runID, spec.Until)
			if err != nil {
				return err
			}
			if ok {
				untilMet = true
				break
			}
		}
		var lastErr error
		for _, bodyID := range spec.Body {
			bodyNode, ok := nodes[bodyID]
			if !ok {
				return fmt.Errorf("orchestration: loop node %q references unknown body node %q", node.ID, bodyID)
			}
			if iteration == resumeIteration {
				// Skip body nodes that already produced output (or whose
				// gate was already approved) during a prior partial attempt
				// at this exact iteration, so resuming never re-executes
				// (and re-triggers the side effects of) completed work.
				if done, err := r.nodeAlreadyDone(ctx, runID, bodyNode); err != nil {
					return err
				} else if done {
					continue
				}
			}
			if err := r.runNodeWithRetry(ctx, scenario, bodyNode, runID); err != nil {
				var paused WorkflowPausedError
				if errors.As(err, &paused) {
					return err
				}
				lastErr = err
				break
			}
		}
		if lastErr != nil {
			return fmt.Errorf("orchestration: loop node %q failed on iteration %d: %w", node.ID, iteration, lastErr)
		}
		actualIterations = iteration
		if err := r.saveLoopProgress(ctx, runID, storedNodeID, loopProgress{Iterations: actualIterations, Completed: false}); err != nil {
			return err
		}
	}
	if err := r.saveLoopProgress(ctx, runID, storedNodeID, loopProgress{Iterations: actualIterations, Completed: untilMet}); err != nil {
		return err
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]any{
		"iterations": actualIterations,
		"completed":  untilMet,
	})
}

func workflowNodesByID(scenario core.Scenario) map[string]core.WorkflowNode {
	nodes := make(map[string]core.WorkflowNode)
	if scenario.Orchestration.Workflow == nil {
		return nodes
	}
	for _, node := range scenario.Orchestration.Workflow.Nodes {
		nodes[node.ID] = node
	}
	return nodes
}
