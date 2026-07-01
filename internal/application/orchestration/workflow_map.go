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

type mapSpec struct {
	ItemsPath string    `json:"items_path"`
	Branch    mapBranch `json:"branch"`
	OnError   string    `json:"on_error,omitempty"`
	ItemField string    `json:"item_field,omitempty"`
}

type mapBranch struct {
	Kind      core.WorkflowNodeKind `json:"kind"`
	Ref       string                `json:"ref,omitempty"`
	Input     json.RawMessage       `json:"input,omitempty"`
	Condition string                `json:"condition,omitempty"`
	Retry     core.RetryPolicy      `json:"retry"`
}

// defaultMapConcurrency bounds how many map branches run concurrently when the
// scenario does not set an explicit max_parallel. It prevents a large runtime
// items array from fanning out into an unbounded number of goroutines and
// concurrent LLM/tool calls, while preserving parallel-by-default behavior.
const defaultMapConcurrency = 16

func (r *WorkflowRunner) runMapNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	var spec mapSpec
	if len(node.Input) == 0 {
		return fmt.Errorf("orchestration: map node %q input is required", node.ID)
	}
	if err := json.Unmarshal(node.Input, &spec); err != nil {
		return fmt.Errorf("orchestration: map node %q decode input: %w", node.ID, err)
	}
	if strings.TrimSpace(spec.ItemsPath) == "" {
		return fmt.Errorf("orchestration: map node %q requires items_path", node.ID)
	}
	if spec.Branch.Kind == "" {
		return fmt.Errorf("orchestration: map node %q requires branch.kind", node.ID)
	}
	switch spec.Branch.Kind {
	case core.NodeAgent, core.NodeTool, core.NodeTransform, core.NodeSubgraph:
	default:
		return fmt.Errorf("orchestration: map node %q branch kind %q is unsupported", node.ID, spec.Branch.Kind)
	}
	if spec.Branch.Kind != core.NodeTransform && spec.Branch.Ref == "" {
		return fmt.Errorf("orchestration: map node %q branch ref is required", node.ID)
	}

	rawItems, ok, err := r.resolveWorkflowPath(ctx, runID, spec.ItemsPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("orchestration: map node %q items_path %q not found", node.ID, spec.ItemsPath)
	}
	items, err := coerceAnySlice(rawItems)
	if err != nil {
		return fmt.Errorf("orchestration: map node %q items_path %q: %w", node.ID, spec.ItemsPath, err)
	}

	itemField := spec.ItemField
	if itemField == "" {
		itemField = "item"
	}
	collectErrors := strings.EqualFold(spec.OnError, "collect_errors")
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, len(items))
	outputs := make(map[string]any, len(items))
	var mu sync.Mutex
	var collected []string

	limit := firstPositive(scenario.Orchestration.MaxParallel, scenario.Runtime.MaxParallel)
	if limit <= 0 {
		limit = defaultMapConcurrency
	}
	if limit > len(items) {
		limit = len(items)
	}
	sem := make(chan struct{}, limit)

scheduleLoop:
	for index, item := range items {
		childID := fmt.Sprintf("%s.%s.%s.%d", node.ID, spec.Branch.Kind, spec.Branch.Ref, index)
		if spec.Branch.Kind == core.NodeTransform {
			childID = fmt.Sprintf("%s.transform.%d", node.ID, index)
		}
		if spec.Branch.Kind == core.NodeSubgraph {
			childID = fmt.Sprintf("%s.subgraph.%s.%d", node.ID, spec.Branch.Ref, index)
		}
		memberKey := fmt.Sprintf("%d", index)
		if raw, ok, err := r.stepOutputRaw(ctx, runID, childID); err != nil {
			return err
		} else if ok {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("orchestration: map node %q decode cached output for index %d: %w", node.ID, index, err)
			}
			mu.Lock()
			outputs[memberKey] = value
			mu.Unlock()
			continue
		}
		// Acquire a slot before launching so the number of live branch
		// goroutines never exceeds limit. Acquiring also stops scheduling new
		// work once the group is cancelled (e.g. a pause or hard failure).
		select {
		case sem <- struct{}{}:
		case <-groupCtx.Done():
			break scheduleLoop
		}
		wg.Add(1)
		go func(index int, item any) {
			defer wg.Done()
			defer func() { <-sem }()
			childInput, err := buildMapBranchInput(spec.Branch, item, itemField)
			if err != nil {
				if collectErrors {
					mu.Lock()
					collected = append(collected, err.Error())
					mu.Unlock()
					errs <- nil
					return
				}
				// Do not cancel: an item not yet scheduled may still
				// pause, and pause must always take precedence over a
				// plain error regardless of scheduling order. Cancelling
				// here would race the scheduling loop's semaphore-acquire
				// select and could stop it from ever scheduling that
				// item, silently dropping its pause.
				errs <- fmt.Errorf("orchestration: map node %q branch input: %w", node.ID, err)
				return
			}
			child := core.WorkflowNode{
				ID:        childID,
				Kind:      spec.Branch.Kind,
				Ref:       spec.Branch.Ref,
				Input:     childInput,
				Condition: spec.Branch.Condition,
				Retry:     spec.Branch.Retry,
			}

			if err := groupCtx.Err(); err != nil {
				return
			}
			branchCtx := withSkipCurrentNode(groupCtx)
			if err := r.runNodeWithRetry(branchCtx, scenario, child, runID); err != nil {
				// A pause must always halt the whole map, even under
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
				errs <- err
				return
			}
			raw, ok, err := r.stepOutputRaw(ctx, runID, childID)
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
			if !ok {
				err = fmt.Errorf("orchestration: map node %q missing output for branch index %d", node.ID, index)
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
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				if collectErrors {
					mu.Lock()
					collected = append(collected, err.Error())
					mu.Unlock()
					errs <- nil
					return
				}
				errs <- fmt.Errorf("orchestration: map node %q decode output for index %d: %w", node.ID, index, err)
				return
			}
			mu.Lock()
			outputs[memberKey] = value
			mu.Unlock()
			errs <- nil
		}(index, item)
	}

	wg.Wait()
	close(errs)
	// If the caller's context was cancelled or timed out, treat that as a
	// failure even if no branch happened to report it: a branch that exits
	// early on groupCtx cancellation never sends to errs, which would
	// otherwise let a partially-completed map look like a success.
	if err := ctx.Err(); err != nil {
		return err
	}
	// Prefer a pause error over any other so HITL halts propagate correctly,
	// regardless of the order branches finished in.
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

func buildMapBranchInput(branch mapBranch, item any, itemField string) (json.RawMessage, error) {
	if branch.Kind == core.NodeTransform {
		var spec transformSpec
		if len(branch.Input) > 0 {
			if err := json.Unmarshal(branch.Input, &spec); err != nil {
				return nil, fmt.Errorf("decode branch input: %w", err)
			}
		}
		if spec.Set == nil {
			spec.Set = map[string]any{}
		}
		spec.Set[itemField] = item
		return json.Marshal(spec)
	}
	base := map[string]any{}
	if len(branch.Input) > 0 {
		if err := json.Unmarshal(branch.Input, &base); err != nil {
			return nil, fmt.Errorf("decode branch input: %w", err)
		}
	}
	base[itemField] = item
	return json.Marshal(base)
}

func coerceAnySlice(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case []string:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must resolve to array, got %T", value)
	}
}
