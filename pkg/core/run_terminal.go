package core

import "encoding/json"

// RunUsage is token usage attached to terminal lifecycle payloads (AF-REQ-03).
type RunUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// Termination reasons recorded on terminal run payloads, so operators can
// attribute why a run ended without re-parsing free-form error text.
const (
	TerminationReasonCompleted        = "completed"
	TerminationReasonMaxStepsExceeded = "max_steps_exceeded"
	TerminationReasonTimeout          = "timeout"
	TerminationReasonCancelled        = "cancelled"
	TerminationReasonLeaseLost        = "lease_lost"
	TerminationReasonLLMError         = "llm_error"
	TerminationReasonError            = "error"
)

// RunTerminalPayload is the structured lifecycle payload for terminal run
// events (RunCompleted / RunFailed / RunCancelled). Output holds the prior
// raw final-answer bytes when present.
type RunTerminalPayload struct {
	Status                string          `json:"status"`
	OutcomeKind           string          `json:"outcome_kind,omitempty"`
	FinalText             string          `json:"final_text,omitempty"`
	StructuredOutput      json.RawMessage `json:"structured_output,omitempty"`
	StructuredOutputError string          `json:"structured_output_error,omitempty"`
	Output                json.RawMessage `json:"output,omitempty"`
	Error                 string          `json:"error,omitempty"`
	TerminationReason     string          `json:"termination_reason,omitempty"`
	Usage                 *RunUsage       `json:"usage,omitempty"`
	EpisodeID             string          `json:"episode_id,omitempty"`
	TriggerKind           string          `json:"trigger_kind,omitempty"`
	SessionID             string          `json:"session_id,omitempty"`
}

// LifecycleCorrelationPayload is written into non-terminal lifecycle event
// payloads so Episode/Session identifiers are stably available on the wire.
type LifecycleCorrelationPayload struct {
	EpisodeID   string `json:"episode_id,omitempty"`
	TriggerKind string `json:"trigger_kind,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}
