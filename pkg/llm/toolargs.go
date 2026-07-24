package llm

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NormalizeToolArguments returns tool-call arguments that are always valid JSON.
//
// Providers often deliver arguments as a JSON string (possibly empty) or as a
// raw object. Streaming models may also truncate mid-object. Empty or
// whitespace-only input becomes {}, matching the OpenAI empty-arguments
// convention. Malformed/truncated payloads also become {}: json.RawMessage must
// be valid JSON or json.Marshal fails with
// "error calling MarshalJSON for type json.RawMessage: unexpected end of JSON input",
// which aborts HITL checkpoint persistence before the tool can validate input.
func NormalizeToolArguments(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		if strings.TrimSpace(encoded) == "" {
			return json.RawMessage(`{}`)
		}
		raw = json.RawMessage(encoded)
	}
	if !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

// NormalizeToolCallInputs rewrites every ToolCall.Input with NormalizeToolArguments.
func NormalizeToolCallInputs(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		out[i].Input = NormalizeToolArguments(out[i].Input)
	}
	return out
}

// NormalizeMessageToolInputs returns a shallow copy of messages whose assistant
// tool-call inputs are normalized for safe JSON marshaling (checkpoints, iteration
// snapshots). Messages without tool calls are reused as-is.
func NormalizeMessageToolInputs(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		if len(out[i].ToolCalls) == 0 {
			continue
		}
		out[i].ToolCalls = NormalizeToolCallInputs(out[i].ToolCalls)
	}
	return out
}
