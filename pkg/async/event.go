package async

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/eventrouter"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

const EventJobType = "event"

type EventPayload struct {
	Type        string             `json:"type"`
	RunID       string             `json:"run_id,omitempty"`
	Payload     json.RawMessage    `json:"payload,omitempty"`
	MaxAttempts int                `json:"max_attempts,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
	Principal   identity.Principal `json:"principal,omitempty"`
}

func (payload EventPayload) Event() eventrouter.Event {
	return eventrouter.Event{
		Type:    payload.Type,
		RunID:   payload.RunID,
		Payload: payload.Payload,
	}
}
