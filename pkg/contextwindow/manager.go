package contextwindow

import (
	"fmt"
	"strings"
	"unicode"
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
	Role       Role   `json:"role"`
	Content    string `json:"content,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
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
	CompactedToolDenials     int      `json:"compacted_tool_denials,omitempty"`
	// MarkedMessages counts messages retained with a visibility=user mark
	// instead of being physically dropped (Manager mark-instead-of-drop mode).
	// It equals DroppedMessages in that mode: the messages are dropped from
	// the model-visible projection but kept for user-side projections.
	MarkedMessages int `json:"marked_messages,omitempty"`
	// NeedsReminder is true when compaction dropped or summarized history and
	// hosts should re-inject active plan/TODO state.
	NeedsReminder bool `json:"needs_reminder,omitempty"`
}

type Result struct {
	Messages []Message `json:"messages"`
	Stats    Stats     `json:"stats"`
}

type Manager struct {
	policy            Policy
	summarizer        Summarizer
	policySource      string
	fallback8192      bool
	markInsteadOfDrop bool
}

type Summarizer func(messages []Message, budget int) string

// ManagerOption customizes a Manager at construction.
type ManagerOption func(*Manager)

// WithMarkInsteadOfDrop makes trimming strategies retain trimmed messages in
// the returned sequence with a visibility=user metadata mark instead of
// physically dropping them. Summary messages stay visible to both audiences.
// The default (false) keeps the historic drop behavior byte-for-byte.
func WithMarkInsteadOfDrop(enabled bool) ManagerOption {
	return func(m *Manager) {
		m.markInsteadOfDrop = enabled
	}
}

func New(policy Policy, opts ...ManagerOption) *Manager {
	detailed := policy.NormalizeDetailed()
	manager := &Manager{policy: detailed.Policy, policySource: detailed.PolicySource, fallback8192: detailed.Fallback8192}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	return manager
}

func NewWithSummarizer(policy Policy, summarizer Summarizer, opts ...ManagerOption) *Manager {
	manager := New(policy, opts...)
	manager.summarizer = summarizer
	return manager
}

// PrepareOptions carries optional advisory inputs to Prepare.
type PrepareOptions struct {
	// KnownInputTokens, when > 0, is the provider-reported token count of the
	// previous LLM call (input + output). It takes precedence over the
	// EstimateTokens heuristic for the trigger/over-budget decisions, because
	// the heuristic can badly undercount tool-heavy or multilingual
	// transcripts and let a request sail past the provider's real window.
	// Trimming budgets still use the heuristic: it is the only per-message
	// breakdown available.
	KnownInputTokens int
}

func (m *Manager) Prepare(messages []Message) Result {
	return m.PrepareWithOptions(messages, PrepareOptions{})
}

func (m *Manager) PrepareWithOptions(messages []Message, opts PrepareOptions) Result {
	messages = cloneMessages(messages)
	if m.policy.ObservationMaskAfterTurns > 0 {
		messages = MaskObservations(messages, m.policy.ObservationMaskAfterTurns, m.policy.ExcludeToolNamesFromStaleWindow...)
	}
	messages = applyRoleBudgets(messages, m.policy.RoleBudgets)
	before := CountMessages(messages)
	// measured is what trigger decisions compare against the budget: the
	// provider-reported count when known, else the heuristic estimate.
	measured := before
	if opts.KnownInputTokens > 0 {
		measured = opts.KnownInputTokens
	}
	stats := Stats{
		Strategy:        m.policy.Strategy,
		BeforeTokens:    before,
		MaxInputTokens:  m.policy.MaxInputTokens,
		PolicySource:    m.policySource,
		FallbackApplied: m.fallback8192,
	}
	if m.policy.Compression.Enabled && m.policy.MaxInputTokens > 0 {
		trigger := int(float64(m.policy.MaxInputTokens) * m.policy.Compression.TriggerRatio)
		if measured > trigger {
			messages = compressToolMessages(messages, m.policy.ToolResultMaxTokens)
			before = CountMessages(messages)
			stats.BeforeTokens = before
			// Compression changed the messages, so the stale provider count no
			// longer reflects them; fall back to the fresh heuristic estimate.
			measured = before
		}
	}
	if measured <= m.policy.MaxInputTokens {
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
		out := m.assembleWindow(protected, Message{}, false, candidates, kept, dropped, &stats)
		return Result{Messages: out, Stats: stats}
	}

	protected, candidates := splitProtected(messages, m.policy.SystemPromptProtection)
	switch m.policy.Strategy {
	case StrategySlidingWindow:
		kept, dropped := m.trimCandidates(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := m.assembleWindow(protected, Message{}, false, candidates, kept, dropped, &stats)
		return Result{Messages: out, Stats: stats}
	case StrategySlidingWindowWithSummary:
		summary, remaining, dropped := m.summarizeAndKeep(candidates, m.policy.MaxInputTokens-CountMessages(protected), m.policy.SummaryTokens)
		out := m.assembleWindow(protected, summary, summary.Content != "", candidates, remaining, dropped, &stats)
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
		out := m.assembleWindow(protected, summary, summary.Content != "", candidates, remaining, dropped, &stats)
		return Result{Messages: out, Stats: stats}
	default:
		kept, dropped := m.trimCandidates(candidates, m.policy.MaxInputTokens-CountMessages(protected))
		out := m.assembleWindow(protected, Message{}, false, candidates, kept, dropped, &stats)
		return Result{Messages: out, Stats: stats}
	}
}

// assembleWindow builds the result sequence for a trimmed window. With the
// default drop behavior it is exactly protected + [summary] + kept. With
// mark-instead-of-drop enabled, dropped candidates stay in the sequence in
// their original positions carrying a visibility=user mark, so user-side
// projections (events, memory, checkpoints) keep the full transcript while
// provider gateways project the marked messages out. Dropped counts are
// recorded on stats either way; in mark mode MarkedMessages mirrors the
// total and AfterTokens reflects only the model-visible projection.
func (m *Manager) assembleWindow(protected []Message, summary Message, hasSummary bool, candidates, kept []Message, dropped roleDropStats, stats *Stats) []Message {
	out := cloneMessages(protected)
	visibleTokens := CountMessages(protected)
	if hasSummary {
		out = append(out, summary)
		stats.Summarized = true
		stats.NeedsReminder = true
		stats.SummaryTokens = EstimateTokens(summary.Content)
		visibleTokens += EstimateTokens(summary.Content)
	}
	if m.markInsteadOfDrop {
		out = append(out, markDroppedCandidates(candidates, kept)...)
		dropped.applyTo(stats)
		stats.MarkedMessages = dropped.Total
		stats.AfterTokens = visibleTokens + CountMessages(kept)
		return out
	}
	out = append(out, kept...)
	dropped.applyTo(stats)
	stats.AfterTokens = CountMessages(out)
	return out
}

// markDroppedCandidates returns the full candidates sequence with every
// message absent from kept tagged visibility=user instead of removed. kept
// alignment mirrors the two-pointer walk of droppedStatsByRole. candidates
// must be owned by the Manager (Prepare clones on entry), because the marks
// mutate message Metadata in place.
func markDroppedCandidates(candidates, kept []Message) []Message {
	out := make([]Message, 0, len(candidates))
	ki := 0
	for _, msg := range candidates {
		if ki < len(kept) && messagesEquivalent(msg, kept[ki]) {
			ki++
			out = append(out, msg)
			continue
		}
		if msg.Metadata == nil {
			msg.Metadata = map[string]string{}
		}
		msg.Metadata[MetadataKeyVisibility] = VisibilityUserOnly
		out = append(out, msg)
	}
	return out
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
	// runes/3 alone systematically underestimates CJK text: modern tokenizers
	// sit near one token per CJK character, so a Chinese-heavy conversation
	// could silently overflow the window. Count CJK runes closer to parity
	// and keep the cheaper ASCII heuristic for Latin text.
	var ascii, cjk, other int
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			ascii++
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			cjk++
		default:
			other++
		}
	}
	estimate := ascii/3 + cjk + other/3
	words := len(strings.Fields(text))
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
