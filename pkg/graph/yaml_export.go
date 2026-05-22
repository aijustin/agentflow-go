package graph

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// GenerateScenarioYAML renders a scenario document for edited Studio graphs.
func GenerateScenarioYAML(scenario core.Scenario) (string, error) {
	data, err := yaml.Marshal(scenario)
	if err != nil {
		return "", fmt.Errorf("graph: marshal scenario yaml: %w", err)
	}
	return string(data), nil
}
