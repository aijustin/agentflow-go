package contextwindow

import (
	"fmt"
	"strings"
)

const maskedToolResultPrefix = "[masked tool result:"

// MaskObservations replaces tool-role messages older than afterTurns success-
// bearing assistant turns (counting from the latest message backward) with a
// short placeholder.
//
// Denial/empty-only assistant turns do not advance the mask clock, so a
// governance denial cannot push a prior successful business result out of the
// unmasked window. Additionally, for each tool name the latest successful
// observation is always kept unmasked (unless CompactContext later drops it).
func MaskObservations(messages []Message, afterTurns int) []Message {
	if afterTurns <= 0 || len(messages) == 0 {
		return messages
	}
	out := cloneMessages(messages)
	batchHasSuccess := assistantBatchHasSuccess(out)
	wouldMask := make([]bool, len(out))
	assistantsSeenFromEnd := 0
	for i := len(out) - 1; i >= 0; i-- {
		switch out[i].Role {
		case RoleAssistant:
			if batchHasSuccess[i] {
				assistantsSeenFromEnd++
			}
		case RoleTool:
			if assistantsSeenFromEnd >= afterTurns {
				wouldMask[i] = true
			}
		}
	}

	// Keep the latest successful observation per tool name even if turn-distance
	// would otherwise mask it. Prefer a still-unmasked newer success of the same
	// name (no keep entry needed); only lift the mask when that latest success
	// itself sits outside the turn window.
	keep := map[int]struct{}{}
	latestSuccess := map[string]int{}
	for i, msg := range out {
		if msg.Role != RoleTool {
			continue
		}
		if ClassifyToolResult(msg) != ToolResultClassSuccess {
			continue
		}
		latestSuccess[toolObservationName(msg)] = i
	}
	for _, idx := range latestSuccess {
		if wouldMask[idx] {
			keep[idx] = struct{}{}
		}
	}

	for i := range out {
		if out[i].Role != RoleTool || !wouldMask[i] {
			continue
		}
		if _, ok := keep[i]; ok {
			continue
		}
		origLen := len(out[i].Content)
		out[i].Content = fmt.Sprintf("[masked tool result: %d bytes]", origLen)
	}
	return out
}

// assistantBatchHasSuccess reports whether each assistant message is followed
// by at least one successful tool result before the next non-tool message that
// ends the batch (another assistant/user/system).
func assistantBatchHasSuccess(messages []Message) []bool {
	out := make([]bool, len(messages))
	for i, msg := range messages {
		if msg.Role != RoleAssistant {
			continue
		}
		for j := i + 1; j < len(messages); j++ {
			switch messages[j].Role {
			case RoleTool:
				if ClassifyToolResult(messages[j]) == ToolResultClassSuccess {
					out[i] = true
				}
			default:
				goto nextAssistant
			}
		}
	nextAssistant:
	}
	return out
}

func toolObservationName(msg Message) string {
	if name := strings.TrimSpace(msg.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(msg.ToolCallID); id != "" {
		return "call:" + id
	}
	return "_unnamed"
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
