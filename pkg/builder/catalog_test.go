package builder_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
)

func TestExampleCatalogValidates(t *testing.T) {
	for _, entry := range builder.ExampleCatalog() {
		t.Run(entry.ID, func(t *testing.T) {
			if err := builder.ValidateCatalogEntry(entry); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExampleCatalogCount(t *testing.T) {
	if len(builder.ExampleCatalog()) != 19 {
		t.Fatalf("catalog entries=%d want=19", len(builder.ExampleCatalog()))
	}
}
