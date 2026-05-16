// Package schemas embeds machine-readable configuration schemas for AgentFlow.
package schemas

import _ "embed"

// scenarioJSONSchema is the JSON Schema document for scenario YAML files.
//
//go:embed agentflow.scenario.schema.json
var scenarioJSONSchema []byte

// ScenarioJSONSchema returns a copy of the AgentFlow scenario JSON Schema.
func ScenarioJSONSchema() []byte {
	return append([]byte(nil), scenarioJSONSchema...)
}
