package builder

import (
	"strings"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
)

// ValidateCatalogEntry validates a catalog stack with the scenario validator.
func ValidateCatalogEntry(entry CatalogEntry) error {
	scenario := entry.Build()
	return configyaml.Validate(scenario)
}

// FindCatalogEntry resolves a catalog id.
func FindCatalogEntry(entries []CatalogEntry, target string) (CatalogEntry, bool) {
	target = strings.TrimSpace(target)
	for _, entry := range entries {
		if entry.ID == target {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}
