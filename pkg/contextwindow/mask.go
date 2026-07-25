package contextwindow

import (
	"fmt"
	"strings"
)

const maskedToolResultPrefix = "[masked tool result:"

// MaskObservations replaces tool-role messages older than afterTurns assistant
// turns (counting from the latest message backward) with a short placeholder.
func MaskObservations(messages []Message, afterTurns int) []Message {
	if afterTurns <= 0 || len(messages) == 0 {
		return messages
	}
	out := cloneMessages(messages)
	assistantsSeenFromEnd := 0
	for i := len(out) - 1; i >= 0; i-- {
		switch out[i].Role {
		case RoleAssistant:
			assistantsSeenFromEnd++
		case RoleTool:
			if assistantsSeenFromEnd >= afterTurns {
				origLen := len(out[i].Content)
				out[i].Content = fmt.Sprintf("[masked tool result: %d bytes]", origLen)
			}
		}
	}
	return out
}

// IsMaskedObservation reports whether a message content was replaced by
// MaskObservations.
func IsMaskedObservation(msg Message) bool {
	return msg.Role == RoleTool && strings.HasPrefix(msg.Content, maskedToolResultPrefix)
}

// CompactContext drops tool messages whose content was masked by
// MaskObservations, preserving all other messages in order.
func CompactContext(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if IsMaskedObservation(msg) {
			continue
		}
		out = append(out, msg)
	}
	return out
}
