package toolorch_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func TestMemoryApprovalStore(t *testing.T) {
	store := toolorch.NewMemoryApprovalStore()
	key := toolorch.Key("echo", json.RawMessage(`{"msg":"hi"}`))
	if _, ok := store.Get("run-1", key); ok {
		t.Fatal("expected miss")
	}
	store.Put("run-1", key, toolorch.DecisionAllow)
	got, ok := store.Get("run-1", key)
	if !ok || got != toolorch.DecisionAllow {
		t.Fatalf("got %v ok=%v", got, ok)
	}
	store.Put("run-1", key, toolorch.DecisionPause) // ignored
	got, ok = store.Get("run-1", key)
	if !ok || got != toolorch.DecisionAllow {
		t.Fatalf("pause must not overwrite, got %v", got)
	}
	store.Clear("run-1")
	if _, ok := store.Get("run-1", key); ok {
		t.Fatal("expected cleared")
	}
}

func TestStoreOrchestratorCachedDeny(t *testing.T) {
	store := toolorch.NewMemoryApprovalStore()
	orch := toolorch.NewStoreOrchestrator(store)
	input := json.RawMessage(`{"x":1}`)
	toolorch.RememberDeny(store, "r1", "echo", input)
	d, err := orch.DecideApproval(t.Context(), toolorch.ApprovalRequest{
		RunID: "r1", Tool: "echo", Input: input, PauseRequired: true,
	})
	if err != nil || d != toolorch.DecisionDeny {
		t.Fatalf("got %v err=%v", d, err)
	}
}
