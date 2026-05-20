package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/core"
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
	if len(spec.Refs) == 0 && len(spec.Tools) == 0 {
		return fmt.Errorf("orchestration: parallel_group node %q requires refs or tools", node.ID)
	}
	collectErrors := strings.EqualFold(spec.OnError, "collect_errors")
	memberCount := len(spec.Refs) + len(spec.Tools)
	var wg sync.WaitGroup
	errs := make(chan error, memberCount)
	outputs := make(map[string]any, memberCount)
	var mu sync.Mutex
	var collected []string

	runMember := func(memberKey string, run func() error, decode func() (any, error)) {
		defer wg.Done()
		if err := run(); err != nil {
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
	}

	for _, ref := range spec.Refs {
		wg.Add(1)
		go func(agentName string) {
			childID := node.ID + ".agent." + agentName
			child := core.WorkflowNode{ID: childID, Kind: core.NodeAgent, Ref: agentName}
			runMember(agentName, func() error {
				return r.runAgentNode(ctx, scenario, child, runID)
			}, func() (any, error) {
				raw, ok, err := r.stepOutputRaw(ctx, runID, childID)
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
		}(ref)
	}
	for _, ref := range spec.Tools {
		wg.Add(1)
		go func(toolName string) {
			childID := node.ID + ".tool." + toolName
			child := core.WorkflowNode{ID: childID, Kind: core.NodeTool, Ref: toolName}
			runMember("tool:"+toolName, func() error {
				return r.runToolNode(ctx, scenario, child, runID)
			}, func() (any, error) {
				raw, ok, err := r.stepOutputRaw(ctx, runID, childID)
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
		}(ref)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	result := map[string]any{"members": outputs}
	if len(collected) > 0 {
		result["errors"] = collected
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, result)
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
	actualIterations := 0
	completed := true
	for iteration := 1; iteration <= maxIterations; iteration++ {
		if spec.Until != "" {
			ok, err := r.conditionEnabled(ctx, runID, spec.Until)
			if err != nil {
				return err
			}
			if ok {
				break
			}
		}
		actualIterations = iteration
		var lastErr error
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
			completed = false
			return fmt.Errorf("orchestration: loop node %q failed on iteration %d: %w", node.ID, iteration, lastErr)
		}
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]any{
		"iterations": actualIterations,
		"completed":  completed,
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
