package runstate_test

import (
	"context"
	"encoding/json"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestHydrateStepContextLoadsInlineAndBlobOutputs(t *testing.T) {
	blobs := blobinmem.NewStore()
	blobRef, err := blobs.Put(context.Background(), json.RawMessage(`{"blob":true}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := runstate.HydrateStepContext(context.Background(), blobs, map[string]runstate.StepOutputRef{
		"inline": {Inline: json.RawMessage(`{"inline":true}`)},
		"blob":   {Blob: &blobRef},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Steps map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload.Steps["inline"]) != `{"inline":true}` {
		t.Fatalf("unexpected inline step: %s", payload.Steps["inline"])
	}
	if string(payload.Steps["blob"]) != `{"blob":true}` {
		t.Fatalf("unexpected blob step: %s", payload.Steps["blob"])
	}
}

func TestHydrateStepContextEmpty(t *testing.T) {
	raw, err := runstate.HydrateStepContext(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Fatalf("expected nil context, got %s", raw)
	}
}
