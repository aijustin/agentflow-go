package stdout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestSinkEmitWritesJSONAndTimestamp(t *testing.T) {
	var out bytes.Buffer
	sink := NewSink(&out)

	if err := sink.Emit(context.Background(), core.Event{Type: core.EventRunStarted, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}

	var got core.Event
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != core.EventRunStarted || got.RunID != "run-1" {
		t.Fatalf("unexpected event: %+v", got)
	}
	if got.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be populated")
	}
}

func TestSinkEmitHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewSink(&bytes.Buffer{}).Emit(ctx, core.Event{Type: core.EventRunStarted})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
