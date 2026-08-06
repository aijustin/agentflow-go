package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type ApprovalPolicy string

const (
	ApprovalNever  ApprovalPolicy = "never"
	ApprovalRisky  ApprovalPolicy = "risky"
	ApprovalAlways ApprovalPolicy = "always"
	// ApprovalPause pauses the run at a human gate instead of denying the tool call.
	ApprovalPause ApprovalPolicy = "pause"
)

type SideEffectLevel string

const (
	SideEffectNone      SideEffectLevel = "none"
	SideEffectRead      SideEffectLevel = "read"
	SideEffectWrite     SideEffectLevel = "write"
	SideEffectExternal  SideEffectLevel = "external"
	SideEffectDangerous SideEffectLevel = "dangerous"
)

type Agent struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Role         string            `json:"role,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	LLM          string            `json:"llm,omitempty"`
	Memory       string            `json:"memory,omitempty"`
	Tools        []string          `json:"tools,omitempty"`
	Skills       []string          `json:"skills,omitempty"`
	SubAgents    []string          `json:"sub_agents,omitempty"`
	Policy       AgentPolicy       `json:"policy"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	// CompletionRequirement, when set, requires a successful call to Tool
	// before the autonomous tool loop may finish with a final answer.
	CompletionRequirement *CompletionRequirement `json:"completion_requirement,omitempty"`
}

// CompletionRequirement declares that the agent must call a specific tool
// before ending its turn (orchestrated worker pattern from grok-build).
type CompletionRequirement struct {
	Tool     string              `json:"tool"`
	Reminder string              `json:"reminder"`
	Recovery *CompletionRecovery `json:"recovery,omitempty"`
}

// CompletionRecovery controls reminder retries with exponential backoff when
// the required completion tool was not called.
type CompletionRecovery struct {
	MaxRetries  int   `json:"max_retries"`
	BaseDelayMS int64 `json:"base_delay_ms"`
	MaxDelayMS  int64 `json:"max_delay_ms"`
}

type AgentPolicy struct {
	MaxSteps         int             `json:"max_steps,omitempty"`
	Timeout          time.Duration   `json:"timeout,omitempty"`
	RetryLimit       int             `json:"retry_limit,omitempty"`
	OutputSchema     json.RawMessage `json:"output_schema,omitempty"`
	HumanCheckpoints []string        `json:"human_checkpoints,omitempty"`
}

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Type         string          `json:"type"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	SideEffect   SideEffectLevel `json:"side_effect,omitempty"`
	Approval     ApprovalPolicy  `json:"approval,omitempty"`
	LLM          string          `json:"llm,omitempty"`
	RateCap      int             `json:"rate_cap,omitempty"`
	// Timeout bounds a single tool execution attempt. Zero (the default)
	// disables the per-tool timeout, so the call is only bounded by the
	// run/agent context deadline, preserving the previous behavior.
	Timeout  time.Duration     `json:"timeout,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type PromptFragment struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content"`
}

type SkillToolPolicy struct {
	Tool       string          `json:"tool"`
	Approval   ApprovalPolicy  `json:"approval,omitempty"`
	SideEffect SideEffectLevel `json:"side_effect,omitempty"`
	RateCap    int             `json:"rate_cap,omitempty"`
}

// SkillKind distinguishes prompt-fragment skills from script skills.
type SkillKind string

const (
	SkillKindPrompt SkillKind = "prompt"
	SkillKindScript SkillKind = "script"
)

type Skill struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Version          string            `json:"version,omitempty"`
	Kind             SkillKind         `json:"kind,omitempty"`
	CompatibleAgents []string          `json:"compatible_agents,omitempty"`
	PromptFragments  []PromptFragment  `json:"prompt_fragments,omitempty"`
	AgentPolicy      AgentPolicy       `json:"agent_policy,omitempty"`
	ToolPolicies     []SkillToolPolicy `json:"tool_policies,omitempty"`
	Workflow         *Workflow         `json:"workflow,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type Scenario struct {
	Name          string                   `json:"name"`
	Description   string                   `json:"description,omitempty"`
	LLMs          map[string]LLMProfileRef `json:"llms,omitempty"`
	Memories      map[string]MemoryRef     `json:"memories,omitempty"`
	Knowledge     KnowledgeConfig          `json:"knowledge,omitempty"`
	MCP           MCPConfig                `json:"mcp,omitempty"`
	Tools         map[string]Tool          `json:"tools,omitempty"`
	Skills        map[string]Skill         `json:"skills,omitempty"`
	Agents        map[string]Agent         `json:"agents,omitempty"`
	Triggers      []Trigger                `json:"triggers,omitempty"`
	Orchestration Orchestration            `json:"orchestration"`
	Runtime       RuntimePolicy            `json:"runtime"`
}

// KnowledgeConfig groups knowledge collections for scenario-level RAG binding.
type KnowledgeConfig struct {
	Collections []KnowledgeCollection `json:"collections,omitempty"`
}

// Trigger maps an external event type to a run request template.
type Trigger struct {
	Event         string `json:"event"`
	Agent         string `json:"agent,omitempty"`
	PromptPath    string `json:"prompt_path,omitempty"`
	ContextPath   string `json:"context_path,omitempty"`
	RunIDPath     string `json:"run_id_path,omitempty"`
	DefaultPrompt string `json:"default_prompt,omitempty"`
}

type LLMProfileRef struct {
	Provider            string               `json:"provider"`
	Model               string               `json:"model"`
	Endpoint            string               `json:"endpoint,omitempty"`
	APIKeyEnv           string               `json:"api_key_env,omitempty"`
	ContextWindowTokens int                  `json:"context_window_tokens,omitempty"`
	MaxOutputTokens     int                  `json:"max_output_tokens,omitempty"`
	Temperature         *float32             `json:"temperature,omitempty"`
	TopP                *float32             `json:"top_p,omitempty"`
	Timeout             time.Duration        `json:"timeout,omitempty"`
	Thinking            llm.ThinkingConfig   `json:"thinking,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Context             contextwindow.Policy `json:"context,omitempty"`
	ExtraBody           map[string]any       `json:"extra_body,omitempty"`
	Capabilities        []string             `json:"capabilities,omitempty"`
	Metadata            map[string]string    `json:"metadata,omitempty"`
}

type MemoryRef struct {
	Type      string              `json:"type"`
	Scope     string              `json:"scope"`
	Namespace string              `json:"namespace,omitempty"`
	Metadata  map[string]string   `json:"metadata,omitempty"`
	Tiers     *MemoryTierSettings `json:"tiers,omitempty"`
}

// MemoryTierSettings configures hot/warm/cold tiered recall for a memory reference.
type MemoryTierSettings struct {
	Enabled       bool                           `json:"enabled,omitempty"`
	HotCapacity   int                            `json:"hot_capacity,omitempty"`
	WarmCapacity  int                            `json:"warm_capacity,omitempty"`
	ColdCapacity  int                            `json:"cold_capacity,omitempty"`
	HotTTL        string                         `json:"hot_ttl,omitempty"`
	WarmTTL       string                         `json:"warm_ttl,omitempty"`
	PromoteAccess int                            `json:"promote_access,omitempty"`
	DemoteIdle    string                         `json:"demote_idle,omitempty"`
	RecallBudget  MemoryTierRecallBudget         `json:"recall_budget,omitempty"`
	RecallWeights MemoryTierRecallWeights        `json:"recall_weights,omitempty"`
	ColdSummary   *MemoryTierColdSummarySettings `json:"cold_summary,omitempty"`
}

type MemoryTierColdSummarySettings struct {
	Enabled         bool   `json:"enabled,omitempty"`
	MinBytes        int64  `json:"min_bytes,omitempty"`
	MaxSummaryChars int    `json:"max_summary_chars,omitempty"`
	SummaryProfile  string `json:"summary_profile,omitempty"`
}

type MemoryTierRecallWeights struct {
	Semantic   float64 `json:"semantic,omitempty"`
	Recency    float64 `json:"recency,omitempty"`
	Importance float64 `json:"importance,omitempty"`
}

type MemoryTierRecallBudget struct {
	Total int `json:"total,omitempty"`
	Hot   int `json:"hot,omitempty"`
	Warm  int `json:"warm,omitempty"`
	Cold  int `json:"cold,omitempty"`
}

type OrchestrationMode string

const (
	OrchestrationAutonomous    OrchestrationMode = "autonomous"
	OrchestrationFixedWorkflow OrchestrationMode = "fixed_workflow"
	OrchestrationHybrid        OrchestrationMode = "hybrid"
)

type Orchestration struct {
	Mode        OrchestrationMode   `json:"mode"`
	Workflow    *Workflow           `json:"workflow,omitempty"`
	Workflows   map[string]Workflow `json:"workflows,omitempty"`
	MaxParallel int                 `json:"max_parallel,omitempty"`
	HumanInLoop HumanInLoopPolicy   `json:"human_in_loop"`
	Planning    PlanningPolicy      `json:"planning,omitempty"`
}

type PlanningPolicy struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Agent    string `json:"agent,omitempty"`
	MaxSteps int    `json:"max_steps,omitempty"`
	// Execute tracks plan step completion in run state during the tool loop.
	Execute bool `json:"execute,omitempty"`
	// Replan retries planning when execute mode stalls before max steps.
	ReplanOnFailure bool `json:"replan_on_failure,omitempty"`
	// AfterWorkflow enables planning during hybrid phase-2 after workflow outputs
	// are hydrated into run context.
	AfterWorkflow bool `json:"after_workflow,omitempty"`
}

type HumanInLoopPolicy struct {
	Enabled     bool     `json:"enabled"`
	Checkpoints []string `json:"checkpoints,omitempty"`
}

type RuntimePolicy struct {
	Timeout             time.Duration `json:"timeout,omitempty"`
	MaxSteps            int           `json:"max_steps,omitempty"`
	MaxRetries          int           `json:"max_retries,omitempty"`
	MaxParallel         int           `json:"max_parallel,omitempty"`
	StepOutputThreshold int64         `json:"step_output_threshold,omitempty"`
	// ValidateToolInput is retained for source/configuration compatibility.
	// Tool input validation is now enabled by default.
	ValidateToolInput bool `json:"validate_tool_input,omitempty"`
	// DisableToolInputValidation explicitly restores advisory-only schemas.
	// Production scenarios should leave this false.
	DisableToolInputValidation bool `json:"disable_tool_input_validation,omitempty"`
	// DoomLoopLimit, when > 0, denies a tool call that repeats the same
	// canonical input this many times within one autonomous run (including
	// the current attempt). Zero disables the check.
	DoomLoopLimit int `json:"doom_loop_limit,omitempty"`
	// HITLDenyLimit, when > 0, fails the run after this many consecutive
	// approval denials (soft deny or cached deny). Orthogonal to DoomLoopLimit.
	HITLDenyLimit int               `json:"hitl_deny_limit,omitempty"`
	Secrets       map[string]string `json:"secrets,omitempty"`
	// DetachedCancellationPollInterval controls how often a detached
	// stream's cancellation watcher reloads the run snapshot. Zero or
	// negative falls back to the runtime default (250ms).
	DetachedCancellationPollInterval time.Duration `json:"detached_cancellation_poll_interval,omitempty"`
}

type ToolCall struct {
	RunID string `json:"run_id"`
	Agent string `json:"agent,omitempty"`
	Tool  string `json:"tool"`
	// ToolCallID is the LLM-issued identifier for this call (e.g.
	// llm.ToolCall.ID), when the call originated from a tool-calling LLM
	// turn. Executors that must correlate calls by tool_call_id (rather
	// than by name alone) can use this field.
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Input      json.RawMessage   `json:"input,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type ToolResult struct {
	Tool   string          `json:"tool"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type ToolExecutor interface {
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}

// ToolStreamEvent is one item in a tool progress stream.
// Shape: zero or more Progress-only events, then exactly one Terminal
// (Result set) or a failed Terminal (Error set). Matching grok-build's
// Progress* → Terminal invariant.
type ToolStreamEvent struct {
	Progress json.RawMessage `json:"progress,omitempty"`
	Result   *ToolResult     `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

// ToolStreamer is an optional ToolExecutor extension that emits progress
// before the terminal result. Runtimes that do not care about progress
// continue to call Execute only.
type ToolStreamer interface {
	ToolExecutor
	ExecuteStream(ctx context.Context, call ToolCall) (<-chan ToolStreamEvent, error)
}

// ToolResolver resolves a declared tool manifest to an executor at call time.
// Resolvers are useful for heavy or tenant-scoped tools whose clients should
// not be initialized during scenario loading.
type ToolResolver interface {
	ResolveTool(ctx context.Context, tool Tool) (ToolExecutor, error)
}

type ToolResolverFunc func(ctx context.Context, tool Tool) (ToolExecutor, error)

func (fn ToolResolverFunc) ResolveTool(ctx context.Context, tool Tool) (ToolExecutor, error) {
	return fn(ctx, tool)
}

type AgentInput struct {
	RunID   string          `json:"run_id"`
	Prompt  string          `json:"prompt,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
}

type AgentOutput struct {
	RunID  string          `json:"run_id"`
	Text   string          `json:"text,omitempty"`
	Raw    json.RawMessage `json:"raw,omitempty"`
	Events []Event         `json:"events,omitempty"`
}

type AgentRunner interface {
	Run(ctx context.Context, input AgentInput) (AgentOutput, error)
}

type CheckpointState struct {
	RunID   string          `json:"run_id"`
	Version int64           `json:"version"`
	NodeID  string          `json:"node_id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type HumanGate interface {
	Pause(ctx context.Context, state CheckpointState) (token string, err error)
	Resume(ctx context.Context, token string, decision Decision, amendment json.RawMessage) error
}

// ToolApprovalEvaluator allows hosts to require human approval for tool calls
// beyond static scenario Tool.Approval policies (for example MCP invoke proxies
// that delegate to remote tools discovered at runtime).
type ToolApprovalEvaluator interface {
	PauseRequired(ctx context.Context, runID string, tool Tool, call llm.ToolCall) (bool, error)
}

// TurnStopInfo is passed to TurnStopHook after the model produces a final
// answer (no tool calls) and CompletionRequirement is satisfied.
type TurnStopInfo struct {
	RunID  string
	Agent  string
	Answer string
}

// TurnStopDecision lets a host veto turn completion (Codex stop-hooks style).
// When Continue is true and ContinuationPrompt is non-empty, the runtime
// appends the prompt as a user message and samples again.
type TurnStopDecision struct {
	Continue           bool
	ContinuationPrompt string
}

// TurnStopHook is an optional host callback after a candidate final answer.
type TurnStopHook func(ctx context.Context, info TurnStopInfo) (TurnStopDecision, error)

// NamedToolApprovalEvaluator is an optional extension that exposes a stable
// evaluator name for RunPaused observability (AF-REQ-04).
type NamedToolApprovalEvaluator interface {
	ToolApprovalEvaluator
	Name() string
}

// PauseTokenDecoder is an optional HumanGate extension for deployments that
// do not use HMAC-signed pause tokens. ResumeAndContinue uses it to resolve
// the run ID before calling Resume.
type PauseTokenDecoder interface {
	RunIDFromPauseToken(token string) (string, error)
}
