package eventrouter

import "encoding/json"

// Event is an external trigger delivered through webhooks, queues, or CLI.
type Event struct {
	Type    string          `json:"type"`
	RunID   string          `json:"run_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// RunRequest is the resolved run input produced from an event.
type RunRequest struct {
	RunID   string          `json:"run_id,omitempty"`
	Agent   string          `json:"agent,omitempty"`
	Prompt  string          `json:"prompt,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
}
