package agentflow_test

import (
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
)

func TestTicketToolWrappers(t *testing.T) {
	store := agentflow.NewMemoryTicketStore(map[string]agentflow.Ticket{
		"T-1": {ID: "T-1", Title: "issue"},
	})
	if store == nil {
		t.Fatal("expected ticket store")
	}
	executor, err := agentflow.NewTicketToolExecutor(agentflow.TicketToolConfig{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if executor == nil {
		t.Fatal("expected ticket executor")
	}
}
