package contextwindow

type Strategy string

const (
	StrategyNone                     Strategy = "none"
	StrategySlidingWindow            Strategy = "sliding_window"
	StrategySlidingWindowWithSummary Strategy = "sliding_window_with_summary"
	// StrategyFullReplace summarizes everything outside a recent tail into one
	// system summary, then keeps the tool-pair-safe recent tail.
	StrategyFullReplace Strategy = "full_replace"
)

type CompressionPolicy struct {
	Enabled      bool    `json:"enabled" yaml:"enabled"`
	TriggerRatio float64 `json:"trigger_ratio,omitempty" yaml:"trigger_ratio,omitempty"`
}

type RoleBudgets struct {
	System    int `json:"system,omitempty" yaml:"system,omitempty"`
	User      int `json:"user,omitempty" yaml:"user,omitempty"`
	Assistant int `json:"assistant,omitempty" yaml:"assistant,omitempty"`
	Tool      int `json:"tool,omitempty" yaml:"tool,omitempty"`
}

// ToolResultClass classifies tool messages for stale-window accounting.
type ToolResultClass string

const (
	ToolResultClassSuccess ToolResultClass = "success"
	ToolResultClassDenied  ToolResultClass = "denied"
	ToolResultClassEmpty   ToolResultClass = "empty"
)

type Policy struct {
	Strategy             Strategy `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	ContextWindowTokens  int      `json:"context_window_tokens,omitempty" yaml:"context_window_tokens,omitempty"`
	MaxInputTokens       int      `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`
	ReservedOutputTokens int      `json:"reserved_output_tokens,omitempty" yaml:"reserved_output_tokens,omitempty"`
	SummaryTokens        int      `json:"summary_tokens,omitempty" yaml:"summary_tokens,omitempty"`
	ToolResultMaxTokens  int      `json:"tool_result_max_tokens,omitempty" yaml:"tool_result_max_tokens,omitempty"`
	// ToolOutputMaxBytes caps tool message bytes written to session memory.
	// Zero disables the write-side byte cap (LLM-side ToolResultMaxTokens still applies).
	ToolOutputMaxBytes     int               `json:"tool_output_max_bytes,omitempty" yaml:"tool_output_max_bytes,omitempty"`
	MemoryRecallLimit      int               `json:"memory_recall_limit,omitempty" yaml:"memory_recall_limit,omitempty"`
	SystemPromptProtection bool              `json:"system_prompt_protection,omitempty" yaml:"system_prompt_protection,omitempty"`
	Compression            CompressionPolicy `json:"compression,omitempty" yaml:"compression,omitempty"`
	RoleBudgets            RoleBudgets       `json:"role_budgets,omitempty" yaml:"role_budgets,omitempty"`
	SummaryMode            string            `json:"summary_mode,omitempty" yaml:"summary_mode,omitempty"`
	ToolSchemaPruning      bool              `json:"tool_schema_pruning,omitempty" yaml:"tool_schema_pruning,omitempty"`
	StaleToolTurns         int               `json:"stale_tool_turns,omitempty" yaml:"stale_tool_turns,omitempty"`
	// ExcludeFromStaleWindow lists tool result classes that do not consume a
	// StaleToolTurns slot. When nil, denied and empty are excluded by default.
	ExcludeFromStaleWindow []ToolResultClass `json:"exclude_from_stale_window,omitempty" yaml:"exclude_from_stale_window,omitempty"`
	PinUserMessages        *bool             `json:"pin_user_messages,omitempty" yaml:"pin_user_messages,omitempty"`
	// MemoryStoreLimit caps how many messages are retained in the persistent
	// flat memory store. When exceeded, the oldest messages are dropped on
	// write (keeping the most recent MemoryStoreLimit). Zero (the default)
	// disables the write-side cap, preserving unbounded append behavior.
	MemoryStoreLimit int `json:"memory_store_limit,omitempty" yaml:"memory_store_limit,omitempty"`
	// StripAssistantPatterns is an optional list of substrings; when non-empty,
	// matching assistant messages may be stripped on memory write by integrators
	// that opt in. The framework does not strip by default.
	StripAssistantPatterns []string `json:"strip_assistant_patterns,omitempty" yaml:"strip_assistant_patterns,omitempty"`
	// InjectCompactReminder asks the runtime to reinject a system reminder with
	// active plan/TODO state after compaction drops or summarizes history.
	// Placement is before the last user message (see InsertBeforeLastUserMessage).
	InjectCompactReminder bool `json:"inject_compact_reminder,omitempty" yaml:"inject_compact_reminder,omitempty"`
}

// ExcludeFromStaleWindowOrDefault returns configured exclusions, or denied+empty.
func (p Policy) ExcludeFromStaleWindowOrDefault() []ToolResultClass {
	if p.ExcludeFromStaleWindow == nil {
		return []ToolResultClass{ToolResultClassDenied, ToolResultClassEmpty}
	}
	return p.ExcludeFromStaleWindow
}

// PinUserMessagesEnabled reports whether user messages should be retained
// during context-window truncation. When nil, user pinning defaults to true.
func (p Policy) PinUserMessagesEnabled() bool {
	if p.PinUserMessages == nil {
		return true
	}
	return *p.PinUserMessages
}

// NormalizeResult carries a normalized policy plus how MaxInputTokens was derived.
type NormalizeResult struct {
	Policy       Policy
	PolicySource string // profile | context_policy | default_8192
	Fallback8192 bool
}

func (p Policy) Normalize() Policy {
	return p.NormalizeDetailed().Policy
}

func (p Policy) NormalizeDetailed() NormalizeResult {
	source := "context_policy"
	fallback := false
	if p.Strategy == "" {
		p.Strategy = StrategyNone
	}
	if p.ContextWindowTokens > 0 && p.MaxInputTokens == 0 {
		p.MaxInputTokens = p.ContextWindowTokens - p.ReservedOutputTokens
		source = "profile"
	}
	if p.MaxInputTokens > 0 && p.ContextWindowTokens == 0 && source == "context_policy" {
		source = "context_policy"
	}
	if p.MaxInputTokens <= 0 {
		p.MaxInputTokens = 8192
		source = "default_8192"
		fallback = true
	}
	if p.ReservedOutputTokens <= 0 {
		p.ReservedOutputTokens = 1024
	}
	if p.SummaryTokens <= 0 {
		p.SummaryTokens = 512
	}
	if p.Compression.TriggerRatio <= 0 {
		p.Compression.TriggerRatio = 0.85
	}
	return NormalizeResult{Policy: p, PolicySource: source, Fallback8192: fallback}
}
