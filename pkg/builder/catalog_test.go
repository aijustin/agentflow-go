package builder_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
)

func TestCoreCatalogValidates(t *testing.T) {
	for _, entry := range builder.CoreCatalog() {
		t.Run(entry.ID, func(t *testing.T) {
			if err := builder.ValidateCatalogEntry(entry); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCoreCatalogCount(t *testing.T) {
	if len(builder.CoreCatalog()) != 9 {
		t.Fatalf("core catalog entries=%d want=9", len(builder.CoreCatalog()))
	}
}

func TestExampleCatalogCount(t *testing.T) {
	if len(builder.ExampleCatalog()) != 19 {
		t.Fatalf("catalog entries=%d want=19", len(builder.ExampleCatalog()))
	}
	if len(builder.LegacyCatalog()) != 10 {
		t.Fatalf("legacy catalog entries=%d want=10", len(builder.LegacyCatalog()))
	}
}

func TestExampleCatalogIDsUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for _, entry := range builder.ExampleCatalog() {
		if _, ok := seen[entry.ID]; ok {
			t.Fatalf("duplicate catalog id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
}
