package studio

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorPayloadFromCodedError(t *testing.T) {
	payload := ErrorPayloadFrom(ErrSavePathMissing)
	if payload.Code != "studio.save_path_missing" {
		t.Fatalf("unexpected code: %+v", payload)
	}
}

func TestErrorPayloadFromGraphDuplicateNode(t *testing.T) {
	payload := ErrorPayloadFrom(fmt.Errorf(`graph: duplicate workflow node "review"`))
	if payload.Code != "graph.duplicate_node" || payload.Params["id"] != "review" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestWrapGraphError(t *testing.T) {
	wrapped := WrapGraphError(errors.New(`graph: workflow node id is required`))
	var coded *CodedError
	if !errors.As(wrapped, &coded) || coded.Code != "graph.invalid" {
		t.Fatalf("expected coded graph error, got %v", wrapped)
	}
}
