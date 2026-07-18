package contextwindow

// messageGroup is an atomic trim unit: an assistant tool-call turn and its
// matching tool results stay together so the model never sees orphan pairs.
type messageGroup struct {
	Messages []Message
	Tokens   int
}

func groupMessagesForToolPairSafety(messages []Message) []messageGroup {
	groups := make([]messageGroup, 0, len(messages))
	for i := 0; i < len(messages); {
		msg := messages[i]
		if msg.Role == RoleAssistant && len(msg.ToolCallIDs) > 0 {
			pending := make(map[string]struct{}, len(msg.ToolCallIDs))
			for _, id := range msg.ToolCallIDs {
				if id != "" {
					pending[id] = struct{}{}
				}
			}
			groupMsgs := []Message{msg}
			tokens := EstimateTokens(msg.Content)
			j := i + 1
			for j < len(messages) && messages[j].Role == RoleTool && len(pending) > 0 {
				id := messages[j].ToolCallID
				if _, ok := pending[id]; !ok {
					break
				}
				groupMsgs = append(groupMsgs, messages[j])
				tokens += EstimateTokens(messages[j].Content)
				delete(pending, id)
				j++
			}
			groups = append(groups, messageGroup{Messages: groupMsgs, Tokens: tokens})
			i = j
			continue
		}
		groups = append(groups, messageGroup{
			Messages: []Message{msg},
			Tokens:   EstimateTokens(msg.Content),
		})
		i++
	}
	return groups
}

func flattenGroups(groups []messageGroup) []Message {
	total := 0
	for _, g := range groups {
		total += len(g.Messages)
	}
	out := make([]Message, 0, total)
	for _, g := range groups {
		out = append(out, g.Messages...)
	}
	return out
}
