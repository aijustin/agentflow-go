package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
)

type nodeInputSpec struct {
	CopyFrom   map[string]string `json:"copy_from"`
	PromptFrom string            `json:"prompt_from"`
}

func (r *WorkflowRunner) resolveNodeInput(ctx context.Context, runID string, raw json.RawMessage, secrets map[string]string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	raw, err := resolveRuntimeSecrets(raw, secrets)
	if err != nil {
		return nil, err
	}
	var spec nodeInputSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return raw, nil
	}
	if len(spec.CopyFrom) == 0 && spec.PromptFrom == "" {
		return raw, nil
	}
	var base map[string]any
	if err := json.Unmarshal(raw, &base); err != nil {
		return raw, nil
	}
	delete(base, "copy_from")
	delete(base, "prompt_from")
	for field, path := range spec.CopyFrom {
		value, ok, err := r.resolveWorkflowPath(ctx, runID, path)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("orchestration: copy_from path %q not found for field %q", path, field)
		}
		base[field] = value
	}
	if spec.PromptFrom != "" {
		value, ok, err := r.resolveWorkflowPath(ctx, runID, spec.PromptFrom)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("orchestration: prompt_from path %q not found", spec.PromptFrom)
		}
		base["prompt"] = value
	}
	out, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("orchestration: marshal resolved node input: %w", err)
	}
	return out, nil
}
