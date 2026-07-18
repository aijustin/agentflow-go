package contextwindow

type roleDropStats struct {
	Total     int
	User      int
	Assistant int
	Tool      int
	System    int
}

func (d roleDropStats) applyTo(stats *Stats) {
	stats.DroppedMessages = d.Total
	stats.DroppedUserMessages = d.User
	stats.DroppedAssistantMessages = d.Assistant
	stats.DroppedToolMessages = d.Tool
	stats.ContextIncomplete = d.User > 0
	if d.Total > 0 {
		stats.NeedsReminder = true
	}
}

func trimToBudget(messages []Message, budget int, pinUser bool) ([]Message, roleDropStats) {
	if budget <= 0 {
		return nil, countAllDropped(messages)
	}
	groups := groupMessagesForToolPairSafety(messages)
	if pinUser {
		return keepRecentGroupsPinUser(groups, budget, messages)
	}
	keptGroups, _ := keepRecentGroups(groups, budget)
	kept := flattenGroups(keptGroups)
	return kept, droppedStatsByRole(messages, kept)
}

func keepRecentGroups(groups []messageGroup, budget int) ([]messageGroup, int) {
	if budget <= 0 {
		return nil, len(groups)
	}
	out := make([]messageGroup, 0, len(groups))
	used := 0
	for i := len(groups) - 1; i >= 0; i-- {
		cost := groups[i].Tokens
		if used+cost > budget {
			continue
		}
		used += cost
		out = append([]messageGroup{groups[i]}, out...)
	}
	return out, len(groups) - len(out)
}

func keepRecentGroupsPinUser(groups []messageGroup, budget int, original []Message) ([]Message, roleDropStats) {
	if budget <= 0 {
		return nil, countAllDropped(original)
	}

	userGroups := make([]int, 0)
	nonUserGroups := make([]int, 0)
	for i, g := range groups {
		if groupHasUser(g) {
			userGroups = append(userGroups, i)
			continue
		}
		nonUserGroups = append(nonUserGroups, i)
	}

	keptIdx := make(map[int]struct{})
	userUsed := 0
	for i := len(userGroups) - 1; i >= 0; i-- {
		idx := userGroups[i]
		cost := groups[idx].Tokens
		if userUsed+cost > budget {
			continue
		}
		userUsed += cost
		keptIdx[idx] = struct{}{}
	}

	remaining := budget - userUsed
	nonUserUsed := 0
	for i := len(nonUserGroups) - 1; i >= 0; i-- {
		idx := nonUserGroups[i]
		cost := groups[idx].Tokens
		if nonUserUsed+cost > remaining {
			continue
		}
		nonUserUsed += cost
		keptIdx[idx] = struct{}{}
	}

	keptGroups := make([]messageGroup, 0, len(keptIdx))
	for i := range groups {
		if _, ok := keptIdx[i]; ok {
			keptGroups = append(keptGroups, groups[i])
		}
	}
	kept := flattenGroups(keptGroups)
	return kept, droppedStatsByRole(original, kept)
}

func groupHasUser(g messageGroup) bool {
	for _, msg := range g.Messages {
		if msg.Role == RoleUser {
			return true
		}
	}
	return false
}

func keepRecentPinUser(messages []Message, budget int) ([]Message, roleDropStats) {
	return keepRecentGroupsPinUser(groupMessagesForToolPairSafety(messages), budget, messages)
}

func droppedStatsByRole(original, kept []Message) roleDropStats {
	stats := roleDropStats{}
	ki := 0
	for _, msg := range original {
		if ki < len(kept) && messagesEquivalent(msg, kept[ki]) {
			ki++
			continue
		}
		stats.Total++
		switch msg.Role {
		case RoleUser:
			stats.User++
		case RoleAssistant:
			stats.Assistant++
		case RoleTool:
			stats.Tool++
		case RoleSystem:
			stats.System++
		}
	}
	if stats.Total > 0 {
		// NeedsReminder is applied via applyTo on Stats.
	}
	return stats
}

func countAllDropped(messages []Message) roleDropStats {
	stats := roleDropStats{Total: len(messages)}
	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			stats.User++
		case RoleAssistant:
			stats.Assistant++
		case RoleTool:
			stats.Tool++
		case RoleSystem:
			stats.System++
		}
	}
	return stats
}

func messagesEquivalent(a, b Message) bool {
	if a.Role != b.Role || a.Content != b.Content || a.Name != b.Name || a.ToolCallID != b.ToolCallID {
		return false
	}
	if len(a.ToolCallIDs) != len(b.ToolCallIDs) {
		return false
	}
	for i := range a.ToolCallIDs {
		if a.ToolCallIDs[i] != b.ToolCallIDs[i] {
			return false
		}
	}
	return true
}
