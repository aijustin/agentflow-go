package orchestration

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeRewinder struct {
	calls map[string]int
}

func (f *fakeRewinder) RewindConversationMemory(_ context.Context, _ string, agentName string, keep int) error {
	f.calls[agentName] = keep
	return nil
}

// M2: rewinding keeps, per agent, the earliest (min) watermark among the
// discarded nodes, and leaves agents with no discarded node untouched.
func TestRewindConversationMemoryComputesMinWatermarkPerAgent(t *testing.T) {
	rewinder := &fakeRewinder{calls: map[string]int{}}
	r := &WorkflowRunner{memory: rewinder}

	marks := map[string]conversationWatermarkEntry{
		"a": {Agent: "writer", Len: 2},
		"b": {Agent: "writer", Len: 5},
		"c": {Agent: "other", Len: 3},
	}
	raw, err := json.Marshal(marks)
	if err != nil {
		t.Fatal(err)
	}
	variables := map[string]json.RawMessage{conversationMemoryWatermarksVar: raw}
	removed := map[string]bool{"b": true, "c": true}

	if err := r.rewindConversationMemory(context.Background(), "run-1", variables, removed); err != nil {
		t.Fatal(err)
	}
	if rewinder.calls["writer"] != 5 {
		t.Fatalf("expected writer kept at discarded watermark 5, got %d", rewinder.calls["writer"])
	}
	if rewinder.calls["other"] != 3 {
		t.Fatalf("expected other kept at 3, got %d", rewinder.calls["other"])
	}
}

func TestRewindConversationMemoryNoWatermarksIsNoOp(t *testing.T) {
	rewinder := &fakeRewinder{calls: map[string]int{}}
	r := &WorkflowRunner{memory: rewinder}
	if err := r.rewindConversationMemory(context.Background(), "run-1", nil, map[string]bool{"x": true}); err != nil {
		t.Fatal(err)
	}
	if len(rewinder.calls) != 0 {
		t.Fatalf("expected no rewind calls, got %+v", rewinder.calls)
	}
}
