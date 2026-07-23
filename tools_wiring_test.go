package agentflow_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/adapters"
)

func TestTicketToolWrappers(t *testing.T) {
	store := adapters.NewMemoryTicketStore(map[string]adapters.Ticket{
		"T-1": {ID: "T-1", Title: "issue"},
	})
	if store == nil {
		t.Fatal("expected ticket store")
	}
	executor, err := adapters.NewTicketToolExecutor(adapters.TicketToolConfig{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if executor == nil {
		t.Fatal("expected ticket executor")
	}
}
