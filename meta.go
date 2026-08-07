package agentflow

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/schemas"
)

// --- Version ---

// Version is the library release version exposed to embedders.
const Version = "0.5.1"

// SchemaVersion is the JSON Schema draft used by ScenarioJSONSchema.
const SchemaVersion = "2020-12"

// --- Schema ---

// ScenarioJSONSchema returns a copy of the AgentFlow scenario JSON Schema.
func ScenarioJSONSchema() []byte {
	return schemas.ScenarioJSONSchema()
}

// --- JSON Helpers ---

// quoteJSONString encodes s as a JSON string value without fmt.Sprintf.
// json.Marshal (not strconv.Quote/%q) is required: Go literals escape control
// characters as \xNN, which is invalid JSON.
func quoteJSONString(s string) json.RawMessage {
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return raw
}

// quoteJSONErrorPayload builds the {"error":...} fallback event payload via
// json.Marshal; the former fmt.Sprintf(`{"error":%q}`, ...) produced invalid
// JSON for error strings containing control characters.
func quoteJSONErrorPayload(err error) json.RawMessage {
	raw, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
	if marshalErr != nil {
		return json.RawMessage(`{"error":"marshal failed"}`)
	}
	return raw
}
