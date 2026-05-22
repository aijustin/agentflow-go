package builder

import (
	"fmt"
	"path/filepath"
	"strings"

	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
)

// ValidateCatalogEntry validates a catalog stack and checks coarse alignment
// with the paired examples/*.yaml file under examplesRoot.
func ValidateCatalogEntry(entry CatalogEntry, examplesRoot string) error {
	scenario := entry.Build()
	if err := configyaml.Validate(scenario); err != nil {
		return err
	}
	yamlPath := filepath.Join(examplesRoot, entry.YAML)
	yamlScenario, err := configyaml.LoadFile(yamlPath)
	if err != nil {
		return err
	}
	if scenario.Orchestration.Mode != yamlScenario.Orchestration.Mode {
		return fmt.Errorf("mode got=%q want=%q", scenario.Orchestration.Mode, yamlScenario.Orchestration.Mode)
	}
	if len(scenario.Agents) != len(yamlScenario.Agents) {
		return fmt.Errorf("agents got=%d want=%d", len(scenario.Agents), len(yamlScenario.Agents))
	}
	return nil
}

// FindCatalogEntry resolves a catalog id, yaml path, or basename.
func FindCatalogEntry(entries []CatalogEntry, target string) (CatalogEntry, bool) {
	target = strings.TrimSpace(target)
	base := filepath.Base(target)
	for _, entry := range entries {
		if entry.ID == target || entry.YAML == target || entry.YAML == base {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}
