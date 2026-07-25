package agentflow

import (
	"encoding/json"
	"strconv"

	"github.com/aijustin/agentflow-go/schemas"
)

// --- Version ---

// Version is the library release version exposed to embedders.
const Version = "0.5.0"

// SchemaVersion is the JSON Schema draft used by ScenarioJSONSchema.
const SchemaVersion = "2020-12"

// --- Schema ---

// ScenarioJSONSchema returns a copy of the AgentFlow scenario JSON Schema.
func ScenarioJSONSchema() []byte {
	return schemas.ScenarioJSONSchema()
}

// --- JSON Helpers ---

// quoteJSONString encodes s as a JSON string value without fmt.Sprintf.
func quoteJSONString(s string) json.RawMessage {
	return json.RawMessage(strconv.Quote(s))
}
