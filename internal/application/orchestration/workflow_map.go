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
	Kind  core.WorkflowNodeKind `json:"kind"`
	Ref   string                `json:"ref,omitempty"`
	Input json.RawMessage       `json:"input,omitempty"`
}

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

	for index, item := range items {
		wg.Add(1)
		go func(index int, item any) {
			defer wg.Done()
			childInput, err := buildMapBranchInput(spec.Branch, item, itemField)
			if err != nil {
				if collectErrors {
					mu.Lock()
					collected = append(collected, err.Error())
					mu.Unlock()
					errs <- nil
					return
				}
				cancel()
				errs <- fmt.Errorf("orchestration: map node %q branch input: %w", node.ID, err)
				return
			}
			childID := fmt.Sprintf("%s.%s.%s.%d", node.ID, spec.Branch.Kind, spec.Branch.Ref, index)
			if spec.Branch.Kind == core.NodeTransform {
				childID = fmt.Sprintf("%s.transform.%d", node.ID, index)
			}
			if spec.Branch.Kind == core.NodeSubgraph {
				childID = fmt.Sprintf("%s.subgraph.%s.%d", node.ID, spec.Branch.Ref, index)
			}
			child := core.WorkflowNode{
				ID:    childID,
				Kind:  spec.Branch.Kind,
				Ref:   spec.Branch.Ref,
				Input: childInput,
			}
			memberKey := fmt.Sprintf("%d", index)

			if err := groupCtx.Err(); err != nil {
				return
			}
			if err := r.runNodeWithRetry(groupCtx, scenario, child, runID); err != nil {
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
				cancel()
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
				cancel()
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
				cancel()
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
				cancel()
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
