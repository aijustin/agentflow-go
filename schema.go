package agentflow

import "github.com/aijustin/agentflow-go/schemas"

// ScenarioJSONSchema returns a copy of the AgentFlow scenario JSON Schema.
func ScenarioJSONSchema() []byte {
	return schemas.ScenarioJSONSchema()
}
