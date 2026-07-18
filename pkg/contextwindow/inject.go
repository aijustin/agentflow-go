package contextwindow

// InsertPosition controls where reinjected context sits after compaction.
type InsertPosition string

const (
	// InsertBeforeLastUserMessage places content above the last user message
	// (and below any trailing summary/system blocks that precede it). Matches
	// Codex InitialContextInjection::BeforeLastUserMessage so the model sees
	// reinjected world/plan context before the latest user turn.
	InsertBeforeLastUserMessage InsertPosition = "before_last_user_message"
	// InsertAppend appends at the end (legacy compact_reminder behavior).
	InsertAppend InsertPosition = "append"
)

// InsertMessage inserts msg into messages according to position.
// For BeforeLastUserMessage: finds the last RoleUser and inserts immediately
// before it; if none, appends.
func InsertMessage(messages []Message, msg Message, position InsertPosition) []Message {
	if position == "" || position == InsertAppend {
		out := make([]Message, 0, len(messages)+1)
		out = append(out, messages...)
		out = append(out, msg)
		return out
	}
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		out := make([]Message, 0, len(messages)+1)
		out = append(out, messages...)
		out = append(out, msg)
		return out
	}
	out := make([]Message, 0, len(messages)+1)
	out = append(out, messages[:lastUser]...)
	out = append(out, msg)
	out = append(out, messages[lastUser:]...)
	return out
}
