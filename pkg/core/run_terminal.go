package core

import "encoding/json"

// RunTerminalPayload is the structured lifecycle payload for terminal run
// events (RunCompleted / RunFailed / RunCancelled). Output holds the prior
// raw final-answer bytes when present.
type RunTerminalPayload struct {
	Status      string          `json:"status"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	EpisodeID   string          `json:"episode_id,omitempty"`
	TriggerKind string          `json:"trigger_kind,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
}

// LifecycleCorrelationPayload is written into non-terminal lifecycle event
// payloads so Episode/Session identifiers are stably available on the wire.
type LifecycleCorrelationPayload struct {
	EpisodeID   string `json:"episode_id,omitempty"`
	TriggerKind string `json:"trigger_kind,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}
