package noop

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestSinkEmitNoop(t *testing.T) {
	sink := NewSink()
	if err := sink.Emit(context.Background(), core.Event{Type: core.EventRunStarted, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
}
