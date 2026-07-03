package core_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestEventSinkFuncEmit(t *testing.T) {
	var emitted core.Event
	sink := core.EventSinkFunc(func(_ context.Context, event core.Event) error {
		emitted = event
		return nil
	})
	event := core.Event{Type: core.EventRunStarted, RunID: "run-1"}
	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if emitted.RunID != "run-1" {
		t.Fatalf("unexpected event: %+v", emitted)
	}
}
