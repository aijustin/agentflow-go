package runstate

import (
	"context"
	"encoding/json"
	"fmt"
)

// HydrateStepContext loads inline and blob-backed step outputs into a JSON
// object shaped as {"steps":{"<node_id>":...}} suitable for hybrid run context.
func HydrateStepContext(ctx context.Context, blobs BlobStore, outputs map[string]StepOutputRef) (json.RawMessage, error) {
	if len(outputs) == 0 {
		return nil, nil
	}
	steps := make(map[string]json.RawMessage, len(outputs))
	for nodeID, ref := range outputs {
		raw, err := LoadStepOutput(ctx, blobs, ref)
		if err != nil {
			return nil, fmt.Errorf("runstate: hydrate step %q: %w", nodeID, err)
		}
		steps[nodeID] = raw
	}
	payload, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		return nil, fmt.Errorf("runstate: marshal hydrated context: %w", err)
	}
	return payload, nil
}

// LoadStepOutput resolves a step output reference from inline or blob storage.
func LoadStepOutput(ctx context.Context, blobs BlobStore, ref StepOutputRef) (json.RawMessage, error) {
	if ref.Blob != nil {
		if blobs == nil {
			return nil, fmt.Errorf("runstate: blob store is required for externalized step output")
		}
		raw, err := blobs.Get(ctx, *ref.Blob)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	if len(ref.Inline) == 0 {
		return json.RawMessage(`null`), nil
	}
	return ref.Inline, nil
}
