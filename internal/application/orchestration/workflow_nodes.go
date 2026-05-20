package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type parallelGroupSpec struct {
	Refs []string `json:"refs"`
}

type loopSpec struct {
	Body          []string `json:"body"`
	MaxIterations int      `json:"max_iterations,omitempty"`
	Until         string   `json:"until,omitempty"`
}

func loopBodyNodeIDs(workflow core.Workflow) map[string]bool {
	ids := make(map[string]bool)
	for _, node := range workflow.Nodes {
		if node.Kind != core.NodeLoop || len(node.Input) == 0 {
			continue
		}
		var spec loopSpec
		if err := json.Unmarshal(node.Input, &spec); err != nil {
			continue
		}
		for _, bodyID := range spec.Body {
			ids[bodyID] = true
		}
	}
	return ids
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
	if len(spec.Refs) == 0 {
		return fmt.Errorf("orchestration: parallel_group node %q requires refs", node.ID)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(spec.Refs))
	outputs := make(map[string]any, len(spec.Refs))
	var mu sync.Mutex
	for _, ref := range spec.Refs {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			childID := node.ID + "." + agentName
			child := core.WorkflowNode{ID: childID, Kind: core.NodeAgent, Ref: agentName}
			if err := r.runAgentNode(ctx, scenario, child, runID); err != nil {
				errs <- err
				return
			}
			raw, ok, err := r.stepOutputRaw(ctx, runID, childID)
			if err != nil {
				errs <- err
				return
			}
			if !ok {
				errs <- fmt.Errorf("orchestration: parallel_group node %q missing output for agent %q", node.ID, agentName)
				return
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				errs <- fmt.Errorf("orchestration: parallel_group node %q decode output for agent %q: %w", node.ID, agentName, err)
				return
			}
			mu.Lock()
			outputs[agentName] = value
			mu.Unlock()
		}(ref)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]any{"members": outputs})
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
	var lastErr error
	for iteration := 1; iteration <= maxIterations; iteration++ {
		for _, bodyID := range spec.Body {
			bodyNode, ok := nodes[bodyID]
			if !ok {
				return fmt.Errorf("orchestration: loop node %q references unknown body node %q", node.ID, bodyID)
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
		if spec.Until == "" {
			break
		}
		ok, err := r.conditionEnabled(ctx, runID, spec.Until)
		if err != nil {
			return err
		}
		if ok {
			break
		}
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]any{
		"iterations": maxIterations,
		"completed":  true,
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
