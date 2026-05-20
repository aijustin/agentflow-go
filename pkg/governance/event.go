package governance

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// RedactEventPayload applies the configured output redactor to event payloads
// before they are emitted to sinks.
func RedactEventPayload(ctx context.Context, redactor OutputRedactor, runID string, typ core.EventType, payload json.RawMessage) json.RawMessage {
	if redactor == nil || len(payload) == 0 {
		return payload
	}
	redacted, err := redactor.RedactOutput(ctx, OutputRedaction{
		RunID:  runID,
		StepID: "event",
		Kind:   "event." + string(typ),
		Data:   payload,
	})
	if err != nil || len(redacted) == 0 {
		return payload
	}
	return redacted
}
