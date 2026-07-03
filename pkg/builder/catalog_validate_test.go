package builder_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestFindCatalogEntry(t *testing.T) {
	entries := []builder.CatalogEntry{
		{ID: "echo", Build: func() core.Scenario { return builder.MinimalAutonomous("assistant") }},
		{ID: "review", Build: func() core.Scenario { return builder.New("review").Autonomous().Scenario() }},
	}
	got, ok := builder.FindCatalogEntry(entries, "echo")
	if !ok || got.ID != "echo" {
		t.Fatalf("unexpected entry: ok=%v id=%q", ok, got.ID)
	}
	if _, ok := builder.FindCatalogEntry(entries, "missing"); ok {
		t.Fatal("expected missing entry")
	}
}

func TestValidateCatalogEntryRejectsInvalidScenario(t *testing.T) {
	entry := builder.CatalogEntry{
		ID: "invalid",
		Build: func() core.Scenario {
			return core.Scenario{Name: "invalid"}
		},
	}
	if err := builder.ValidateCatalogEntry(entry); err == nil {
		t.Fatal("expected validation error for scenario without agents")
	}
}
