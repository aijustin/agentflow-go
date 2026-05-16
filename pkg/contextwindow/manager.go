package contextwindow

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role              `json:"role"`
	Content    string            `json:"content,omitempty"`
	Name       string            `json:"name,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Stats struct {
	Strategy        Strategy `json:"strategy"`
	BeforeTokens    int      `json:"before_tokens"`
	AfterTokens     int      `json:"after_tokens"`
	MaxInputTokens  int      `json:"max_input_tokens"`
	DroppedMessages int      `json:"dropped_messages"`
	Summarized      bool     `json:"summarized"`
	SummaryTokens   int      `json:"summary_tokens,omitempty"`
}

type Result struct {
	Messages []Message `json:"messages"`
	Stats    Stats     `json:"stats"`
}

type Manager struct {
	policy Policy
}

func New(policy Policy) *Manager {
	normalized := policy.Normalize()
	return &Manager{policy: normalized}
}

func (m *Manager) Prepare(messages []Message) Result {
	before := CountMessages(messages)
	stats := Stats{
		Strategy:       m.policy.Strategy,
		BeforeTokens:   before,
		MaxInputTokens: m.policy.MaxInputTokens,
	}
	if m.policy.Strategy == StrategyNone || before <= m.policy.MaxInputTokens {
		stats.AfterTokens = before
		return Result{Messages: cloneMessages(messages), Stats: stats}
	}

	protected, candidates := splitProtected(messages, m.policy.SystemPromptProtection)
	switch m.policy.Strategy {
	case StrategySlidingWindow:
		kept, dropped := keepRecent(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := append(cloneMessages(protected), kept...)
		stats.DroppedMessages = dropped
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	case StrategySlidingWindowWithSummary:
		summary, remaining, dropped := summarizeAndKeep(candidates, m.policy.MaxInputTokens-CountMessages(protected), m.policy.SummaryTokens)
		out := cloneMessages(protected)
		if summary.Content != "" {
			out = append(out, summary)
			stats.Summarized = true
			stats.SummaryTokens = EstimateTokens(summary.Content)
		}
		out = append(out, remaining...)
		stats.DroppedMessages = dropped
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	default:
		kept, dropped := keepRecent(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := append(cloneMessages(protected), kept...)
		stats.DroppedMessages = dropped
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	}
}

func CountMessages(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg.Content)
	}
	return total
}

func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	words := len(strings.Fields(text))
	estimate := runes / 3
	if words > estimate {
		estimate = words
	}
	if estimate == 0 {
		estimate = 1
	}
	return estimate
}

func splitProtected(messages []Message, protectSystem bool) ([]Message, []Message) {
	if !protectSystem {
		return nil, cloneMessages(messages)
	}
	protected := make([]Message, 0)
	candidates := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			protected = append(protected, msg)
		} else {
			candidates = append(candidates, msg)
		}
	}
	return protected, candidates
}

func keepRecent(messages []Message, budget int) ([]Message, int) {
	if budget <= 0 {
		return nil, len(messages)
	}
	out := make([]Message, 0, len(messages))
	used := 0
	for i := len(messages) - 1; i >= 0; i-- {
		cost := EstimateTokens(messages[i].Content)
		if used+cost > budget {
			continue
		}
		used += cost
		out = append([]Message{messages[i]}, out...)
	}
	return out, len(messages) - len(out)
}

func summarizeAndKeep(messages []Message, budget, summaryBudget int) (Message, []Message, int) {
	if budget <= 0 {
		return Message{}, nil, len(messages)
	}
	recentBudget := budget - summaryBudget
	if recentBudget < budget/2 {
		recentBudget = budget / 2
	}
	remaining, dropped := keepRecent(messages, recentBudget)
	droppedMessages := messages[:max(0, len(messages)-len(remaining))]
	summaryText := buildSummary(droppedMessages, summaryBudget)
	if summaryText == "" {
		return Message{}, remaining, dropped
	}
	summary := Message{
		Role:    RoleSystem,
		Content: summaryText,
		Metadata: map[string]string{
			"context_window": "summary",
		},
	}
	return summary, remaining, dropped
}

func buildSummary(messages []Message, budget int) string {
	if len(messages) == 0 || budget <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Earlier context summary:\n")
	used := EstimateTokens(b.String())
	for _, msg := range messages {
		line := fmt.Sprintf("- %s: %s\n", msg.Role, compact(msg.Content, 240))
		cost := EstimateTokens(line)
		if used+cost > budget {
			break
		}
		b.WriteString(line)
		used += cost
	}
	return strings.TrimSpace(b.String())
}

func compact(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	return out
}
