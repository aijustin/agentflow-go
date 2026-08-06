package llm

import "github.com/aijustin/agentflow-go/pkg/contextwindow"

// Dual-visibility projection for Message. The metadata key and values are
// defined in pkg/contextwindow (which writes the marks during
// mark-instead-of-drop trimming); the helpers here are the read/write side
// for llm.Message consumers such as provider gateways.
const (
	// MetadataKeyVisibility is the Message.Metadata key carrying the
	// dual-visibility projection mark.
	MetadataKeyVisibility = contextwindow.MetadataKeyVisibility
	// VisibilityBoth is the zero value: the message is visible to both the
	// model and the user.
	VisibilityBoth = contextwindow.VisibilityBoth
	// VisibilityAgentOnly marks a message hidden from the user projection.
	VisibilityAgentOnly = contextwindow.VisibilityAgentOnly
	// VisibilityUserOnly marks a message hidden from the model projection:
	// provider gateways filter it out, while events, memory, and checkpoints
	// keep the full sequence.
	VisibilityUserOnly = contextwindow.VisibilityUserOnly
)

// MarkUserVisibleOnly tags msg so the model never sees it while user-facing
// projections (events, memory, checkpoints) keep it.
func MarkUserVisibleOnly(msg *Message) {
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[MetadataKeyVisibility] = VisibilityUserOnly
}

// MarkAgentVisibleOnly tags msg so user-facing projections may hide it while
// the model keeps seeing it.
func MarkAgentVisibleOnly(msg *Message) {
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[MetadataKeyVisibility] = VisibilityAgentOnly
}

// IsAgentVisible reports whether msg belongs to the model-visible projection.
func IsAgentVisible(msg Message) bool {
	return msg.Metadata[MetadataKeyVisibility] != VisibilityUserOnly
}

// IsUserVisible reports whether msg belongs to the user-visible projection.
func IsUserVisible(msg Message) bool {
	return msg.Metadata[MetadataKeyVisibility] != VisibilityAgentOnly
}

// AgentVisibleMessages returns the model-visible projection of messages:
// every message except those marked VisibilityUserOnly, order preserved.
// When nothing is marked it returns the input slice unchanged.
func AgentVisibleMessages(messages []Message) []Message {
	filtered := false
	for _, msg := range messages {
		if !IsAgentVisible(msg) {
			filtered = true
			break
		}
	}
	if !filtered {
		return messages
	}
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if IsAgentVisible(msg) {
			out = append(out, msg)
		}
	}
	return out
}

// UserVisibleMessages returns the user-visible projection of messages:
// every message except those marked VisibilityAgentOnly, order preserved.
// When nothing is marked it returns the input slice unchanged.
func UserVisibleMessages(messages []Message) []Message {
	filtered := false
	for _, msg := range messages {
		if !IsUserVisible(msg) {
			filtered = true
			break
		}
	}
	if !filtered {
		return messages
	}
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if IsUserVisible(msg) {
			out = append(out, msg)
		}
	}
	return out
}
