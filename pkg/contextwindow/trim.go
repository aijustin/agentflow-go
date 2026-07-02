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
}

func trimToBudget(messages []Message, budget int, pinUser bool) ([]Message, roleDropStats) {
	if budget <= 0 {
		return nil, countAllDropped(messages)
	}
	if pinUser {
		return keepRecentPinUser(messages, budget)
	}
	kept, _ := keepRecent(messages, budget)
	return kept, droppedStatsByRole(messages, kept)
}

func keepRecentPinUser(messages []Message, budget int) ([]Message, roleDropStats) {
	if budget <= 0 {
		return nil, countAllDropped(messages)
	}

	userIdx := make([]int, 0)
	nonUserIdx := make([]int, 0)
	for i, msg := range messages {
		if msg.Role == RoleUser {
			userIdx = append(userIdx, i)
			continue
		}
		nonUserIdx = append(nonUserIdx, i)
	}

	keptIdx := make(map[int]struct{})
	userUsed := 0
	for i := len(userIdx) - 1; i >= 0; i-- {
		idx := userIdx[i]
		cost := EstimateTokens(messages[idx].Content)
		if userUsed+cost > budget {
			continue
		}
		userUsed += cost
		keptIdx[idx] = struct{}{}
	}

	remaining := budget - userUsed
	nonUserUsed := 0
	for i := len(nonUserIdx) - 1; i >= 0; i-- {
		idx := nonUserIdx[i]
		cost := EstimateTokens(messages[idx].Content)
		if nonUserUsed+cost > remaining {
			continue
		}
		nonUserUsed += cost
		keptIdx[idx] = struct{}{}
	}

	out := make([]Message, 0, len(keptIdx))
	stats := roleDropStats{}
	for i, msg := range messages {
		if _, ok := keptIdx[i]; ok {
			out = append(out, msg)
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
	return out, stats
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
	return a.Role == b.Role &&
		a.Content == b.Content &&
		a.Name == b.Name &&
		a.ToolCallID == b.ToolCallID
}
