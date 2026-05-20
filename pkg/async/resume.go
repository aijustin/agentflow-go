package async

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

const ResumeContinueJobType = "resume.continue"

type ResumeContinuePayload struct {
	Token       string             `json:"token"`
	Decision    core.Decision      `json:"decision"`
	Amendment   json.RawMessage    `json:"amendment,omitempty"`
	RunID       string             `json:"run_id,omitempty"`
	MaxAttempts int                `json:"max_attempts,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
	Principal   identity.Principal `json:"principal,omitempty"`
}
