package ticket

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/toolticket"
)

func TestExecutorGetAndUpdate(t *testing.T) {
	store := NewMemoryStore(map[string]toolticket.Ticket{
		"T-1": {ID: "T-1", Title: "Login issue", Status: "open"},
	})
	executor, err := NewExecutor(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), core.ToolCall{
		Tool:  "ticket",
		Input: json.RawMessage(`{"action":"get","id":"T-1"}`),
	})
	if err != nil || result.Error != "" {
		t.Fatalf("get failed: err=%v error=%s", err, result.Error)
	}
	result, err = executor.Execute(t.Context(), core.ToolCall{
		Tool:  "ticket",
		Input: json.RawMessage(`{"action":"update","id":"T-1","fields":{"status":"resolved"}}`),
	})
	if err != nil || result.Error != "" {
		t.Fatalf("update failed: err=%v error=%s", err, result.Error)
	}
	var ticket toolticket.Ticket
	if err := json.Unmarshal(result.Output, &ticket); err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "resolved" {
		t.Fatalf("expected resolved, got %+v", ticket)
	}
}

func TestExecutorCommentAndErrors(t *testing.T) {
	store := NewMemoryStore(map[string]toolticket.Ticket{
		"T-1": {ID: "T-1", Title: "Login issue", Status: "open"},
	})
	executor, err := NewExecutor(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), core.ToolCall{
		Tool:  "ticket",
		Input: json.RawMessage(`{"action":"comment","id":"T-1","author":"qa","body":"looks fixed"}`),
	})
	if err != nil || result.Error != "" {
		t.Fatalf("comment failed: err=%v error=%s", err, result.Error)
	}
	if _, err := NewExecutor(nil); err == nil {
		t.Fatal("expected nil store error")
	}
	result, err = executor.Execute(t.Context(), core.ToolCall{
		Tool:  "ticket",
		Input: json.RawMessage(`{"action":"archive","id":"T-1"}`),
	})
	if err == nil {
		t.Fatalf("expected unsupported action error, err=%v result=%+v", err, result)
	}
	result, err = executor.Execute(t.Context(), core.ToolCall{
		Tool:  "ticket",
		Input: json.RawMessage(`{"action":"get"}`),
	})
	if err == nil {
		t.Fatalf("expected missing id error, err=%v result=%+v", err, result)
	}
}
