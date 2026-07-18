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
	// ToolCallIDs lists tool_call ids issued by an assistant turn. Used for
	// tool-pair-safe trimming (assistant + matching tool results stay atomic).
	ToolCallIDs []string          `json:"tool_call_ids,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Stats struct {
	Strategy                 Strategy `json:"strategy"`
	BeforeTokens             int      `json:"before_tokens"`
	AfterTokens              int      `json:"after_tokens"`
	MaxInputTokens           int      `json:"max_input_tokens"`
	DroppedMessages          int      `json:"dropped_messages"`
	DroppedUserMessages      int      `json:"dropped_user_messages,omitempty"`
	DroppedAssistantMessages int      `json:"dropped_assistant_messages,omitempty"`
	DroppedToolMessages      int      `json:"dropped_tool_messages,omitempty"`
	ContextIncomplete        bool     `json:"context_incomplete,omitempty"`
	Summarized               bool     `json:"summarized"`
	SummaryTokens            int      `json:"summary_tokens,omitempty"`
	PolicySource             string   `json:"policy_source,omitempty"`
	FallbackApplied          bool     `json:"fallback_applied,omitempty"`
	StaleDroppedToolTurns    int      `json:"stale_dropped_tool_turns,omitempty"`
	DenialOccupiedSlots      int      `json:"denial_occupied_slots,omitempty"`
	StaleExcludedTurns       int      `json:"stale_excluded_turns,omitempty"`
	// NeedsReminder is true when compaction dropped or summarized history and
	// hosts should re-inject active plan/TODO state.
	NeedsReminder bool `json:"needs_reminder,omitempty"`
}

type Result struct {
	Messages []Message `json:"messages"`
	Stats    Stats     `json:"stats"`
}

type Manager struct {
	policy       Policy
	summarizer   Summarizer
	policySource string
	fallback8192 bool
}

type Summarizer func(messages []Message, budget int) string

func New(policy Policy) *Manager {
	detailed := policy.NormalizeDetailed()
	return &Manager{policy: detailed.Policy, policySource: detailed.PolicySource, fallback8192: detailed.Fallback8192}
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
		Strategy:        m.policy.Strategy,
		BeforeTokens:    before,
		MaxInputTokens:  m.policy.MaxInputTokens,
		PolicySource:    m.policySource,
		FallbackApplied: m.fallback8192,
	}
	if m.policy.Compression.Enabled && m.policy.MaxInputTokens > 0 {
		trigger := int(float64(m.policy.MaxInputTokens) * m.policy.Compression.TriggerRatio)
		if before > trigger {
			messages = compressToolMessages(messages, m.policy.ToolResultMaxTokens)
			before = CountMessages(messages)
			stats.BeforeTokens = before
		}
	}
	if before <= m.policy.MaxInputTokens {
		stats.AfterTokens = before
		return Result{Messages: cloneMessages(messages), Stats: stats}
	}
	if m.policy.Strategy == StrategyNone {
		// StrategyNone means "don't proactively manage the window", not
		// "ignore the configured budget". Sending an over-budget request
		// as-is either gets rejected by the provider for exceeding its
		// real context length or silently balloons cost/latency, so once
		// MaxInputTokens is actually exceeded, fall back to a sliding
		// window trim rather than letting the context grow unbounded.
		protected, candidates := splitProtected(messages, m.policy.SystemPromptProtection)
		kept, dropped := m.trimCandidates(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := append(cloneMessages(protected), kept...)
		dropped.applyTo(&stats)
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	}

	protected, candidates := splitProtected(messages, m.policy.SystemPromptProtection)
	switch m.policy.Strategy {
	case StrategySlidingWindow:
		kept, dropped := m.trimCandidates(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := append(cloneMessages(protected), kept...)
		dropped.applyTo(&stats)
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	case StrategySlidingWindowWithSummary:
		summary, remaining, dropped := m.summarizeAndKeep(candidates, m.policy.MaxInputTokens-CountMessages(protected), m.policy.SummaryTokens)
		out := cloneMessages(protected)
		if summary.Content != "" {
			out = append(out, summary)
			stats.Summarized = true
			stats.NeedsReminder = true
			stats.SummaryTokens = EstimateTokens(summary.Content)
		}
		out = append(out, remaining...)
		dropped.applyTo(&stats)
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	case StrategyFullReplace:
		budget := m.policy.MaxInputTokens - CountMessages(protected)
		summaryBudget := m.policy.SummaryTokens
		recentBudget := budget - summaryBudget
		if recentBudget < budget/3 {
			recentBudget = budget / 3
		}
		summary, remaining, dropped := m.summarizeAndKeep(candidates, budget, summaryBudget)
		// Prefer a tighter recent tail than sliding_window_with_summary.
		if CountMessages(remaining) > recentBudget {
			var extra roleDropStats
			remaining, extra = m.trimCandidates(remaining, recentBudget)
			dropped.Total += extra.Total
			dropped.User += extra.User
			dropped.Assistant += extra.Assistant
			dropped.Tool += extra.Tool
			dropped.System += extra.System
		}
		out := cloneMessages(protected)
		if summary.Content != "" {
			out = append(out, summary)
			stats.Summarized = true
			stats.NeedsReminder = true
			stats.SummaryTokens = EstimateTokens(summary.Content)
		}
		out = append(out, remaining...)
		dropped.applyTo(&stats)
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	default:
		kept, dropped := m.trimCandidates(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := append(cloneMessages(protected), kept...)
		dropped.applyTo(&stats)
		stats.AfterTokens = CountMessages(out)
		return Result{Messages: out, Stats: stats}
	}
}

func (m *Manager) trimCandidates(candidates []Message, budget int) ([]Message, roleDropStats) {
	return trimToBudget(candidates, budget, m.policy.PinUserMessagesEnabled())
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

func (m *Manager) summarizeAndKeep(messages []Message, budget, summaryBudget int) (Message, []Message, roleDropStats) {
	if budget <= 0 {
		return Message{}, nil, countAllDropped(messages)
	}
	recentBudget := budget - summaryBudget
	if recentBudget < budget/2 {
		recentBudget = budget / 2
	}
	remaining, dropped := m.trimCandidates(messages, recentBudget)
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
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		limit := roleBudgetLimit(budgets, msg.Role)
		if limit <= 0 {
			out = append([]Message{msg}, out...)
			continue
		}
		cost := EstimateTokens(msg.Content)
		if usage[msg.Role]+cost > limit {
			continue
		}
		usage[msg.Role] += cost
		out = append([]Message{msg}, out...)
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
	for i, msg := range messages {
		out[i] = msg
		if len(msg.ToolCallIDs) > 0 {
			out[i].ToolCallIDs = append([]string(nil), msg.ToolCallIDs...)
		}
		if len(msg.Metadata) > 0 {
			meta := make(map[string]string, len(msg.Metadata))
			for k, v := range msg.Metadata {
				meta[k] = v
			}
			out[i].Metadata = meta
		}
	}
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
		truncated, meta := ApplyToolOutputTransform(msg.Name, []byte(msg.Content), maxToolTokens, nil)
		out[i].Content = string(truncated)
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]string{}
		}
		out[i].Metadata["context_window"] = "compressed"
		if meta.Strategy != "" {
			out[i].Metadata["truncate_strategy"] = meta.Strategy
		}
	}
	return out
}
