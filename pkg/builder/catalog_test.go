package builder_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
)

func TestExampleCatalogMatchesYAML(t *testing.T) {
	for _, entry := range builder.ExampleCatalog() {
		t.Run(entry.ID, func(t *testing.T) {
			if err := builder.ValidateCatalogEntry(entry, "../../examples"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExampleCatalogCount(t *testing.T) {
	if len(builder.ExampleCatalog()) != 18 {
		t.Fatalf("catalog entries=%d want=18", len(builder.ExampleCatalog()))
	}
}
