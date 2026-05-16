package async

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

const RunJobType = "run"

type RunPayload struct {
	RunID       string             `json:"run_id,omitempty"`
	ScenarioID  string             `json:"scenario_id,omitempty"`
	Scenario    json.RawMessage    `json:"scenario,omitempty"`
	Agent       string             `json:"agent,omitempty"`
	Prompt      string             `json:"prompt,omitempty"`
	Context     json.RawMessage    `json:"context,omitempty"`
	MaxAttempts int                `json:"max_attempts,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
	Principal   identity.Principal `json:"principal,omitempty"`
}
