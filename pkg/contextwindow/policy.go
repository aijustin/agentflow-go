package contextwindow

type Strategy string

const (
	StrategyNone                     Strategy = "none"
	StrategySlidingWindow            Strategy = "sliding_window"
	StrategySlidingWindowWithSummary Strategy = "sliding_window_with_summary"
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

type Policy struct {
	Strategy               Strategy          `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	ContextWindowTokens    int               `json:"context_window_tokens,omitempty" yaml:"context_window_tokens,omitempty"`
	MaxInputTokens         int               `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`
	ReservedOutputTokens   int               `json:"reserved_output_tokens,omitempty" yaml:"reserved_output_tokens,omitempty"`
	SummaryTokens          int               `json:"summary_tokens,omitempty" yaml:"summary_tokens,omitempty"`
	ToolResultMaxTokens    int               `json:"tool_result_max_tokens,omitempty" yaml:"tool_result_max_tokens,omitempty"`
	MemoryRecallLimit      int               `json:"memory_recall_limit,omitempty" yaml:"memory_recall_limit,omitempty"`
	SystemPromptProtection bool              `json:"system_prompt_protection,omitempty" yaml:"system_prompt_protection,omitempty"`
	Compression            CompressionPolicy `json:"compression,omitempty" yaml:"compression,omitempty"`
	RoleBudgets            RoleBudgets       `json:"role_budgets,omitempty" yaml:"role_budgets,omitempty"`
	SummaryMode            string            `json:"summary_mode,omitempty" yaml:"summary_mode,omitempty"`
	ToolSchemaPruning      bool              `json:"tool_schema_pruning,omitempty" yaml:"tool_schema_pruning,omitempty"`
	StaleToolTurns         int               `json:"stale_tool_turns,omitempty" yaml:"stale_tool_turns,omitempty"`
}

func (p Policy) Normalize() Policy {
	if p.Strategy == "" {
		p.Strategy = StrategyNone
	}
	if p.ContextWindowTokens > 0 && p.MaxInputTokens == 0 {
		p.MaxInputTokens = p.ContextWindowTokens - p.ReservedOutputTokens
	}
	if p.MaxInputTokens <= 0 {
		p.MaxInputTokens = 8192
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
	return p
}
