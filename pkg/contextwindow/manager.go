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
	policy     Policy
	summarizer Summarizer
}

type Summarizer func(messages []Message, budget int) string

func New(policy Policy) *Manager {
	normalized := policy.Normalize()
	return &Manager{policy: normalized}
}

func NewWithSummarizer(policy Policy, summarizer Summarizer) *Manager {
	manager := New(policy)
	manager.summarizer = summarizer
	return manager
}

func (m *Manager) Prepare(messages []Message) Result {
	messages = cloneMessages(messages)
	messages = applyRoleBudgets(messages, m.policy.RoleBudgets)
	before := CountMessages(messages)
	stats := Stats{
		Strategy:       m.policy.Strategy,
		BeforeTokens:   before,
		MaxInputTokens: m.policy.MaxInputTokens,
	}
	if m.policy.Compression.Enabled && m.policy.MaxInputTokens > 0 {
		trigger := int(float64(m.policy.MaxInputTokens) * m.policy.Compression.TriggerRatio)
		if before > trigger {
			messages = compressToolMessages(messages, m.policy.ToolResultMaxTokens)
			before = CountMessages(messages)
			stats.BeforeTokens = before
		}
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
		summary, remaining, dropped := m.summarizeAndKeep(candidates, m.policy.MaxInputTokens-CountMessages(protected), m.policy.SummaryTokens)
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

func (m *Manager) summarizeAndKeep(messages []Message, budget, summaryBudget int) (Message, []Message, int) {
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
	if m.summarizer != nil && (m.policy.SummaryMode == "llm" || m.policy.SummaryMode == "custom") {
		summaryText = strings.TrimSpace(m.summarizer(droppedMessages, summaryBudget))
	}
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

func applyRoleBudgets(messages []Message, budgets RoleBudgets) []Message {
	if budgets == (RoleBudgets{}) {
		return messages
	}
	usage := map[Role]int{}
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		limit := roleBudgetLimit(budgets, msg.Role)
		if limit <= 0 {
			out = append(out, msg)
			continue
		}
		cost := EstimateTokens(msg.Content)
		if usage[msg.Role]+cost > limit {
			continue
		}
		usage[msg.Role] += cost
		out = append(out, msg)
	}
	return out
}

func roleBudgetLimit(budgets RoleBudgets, role Role) int {
	switch role {
	case RoleSystem:
		return budgets.System
	case RoleUser:
		return budgets.User
	case RoleAssistant:
		return budgets.Assistant
	case RoleTool:
		return budgets.Tool
	default:
		return 0
	}
}

func summarizeAndKeep(messages []Message, budget, summaryBudget int) (Message, []Message, int) {
	return New(Policy{}).summarizeAndKeep(messages, budget, summaryBudget)
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

func compressToolMessages(messages []Message, maxToolTokens int) []Message {
	if maxToolTokens <= 0 {
		maxToolTokens = 256
	}
	out := make([]Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if msg.Role != RoleTool {
			continue
		}
		if EstimateTokens(msg.Content) <= maxToolTokens {
			continue
		}
		out[i].Content = compact(msg.Content, maxToolTokens*3)
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]string{}
		}
		out[i].Metadata["context_window"] = "compressed"
	}
	return out
}
