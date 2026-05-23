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
}

type AgentPolicy struct {
	MaxSteps         int             `json:"max_steps,omitempty"`
	Timeout          time.Duration   `json:"timeout,omitempty"`
	RetryLimit       int             `json:"retry_limit,omitempty"`
	OutputSchema     json.RawMessage `json:"output_schema,omitempty"`
	HumanCheckpoints []string        `json:"human_checkpoints,omitempty"`
}

type Tool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Type         string            `json:"type"`
	InputSchema  json.RawMessage   `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage   `json:"output_schema,omitempty"`
	SideEffect   SideEffectLevel   `json:"side_effect,omitempty"`
	Approval     ApprovalPolicy    `json:"approval,omitempty"`
	LLM          string            `json:"llm,omitempty"`
	RateCap      int               `json:"rate_cap,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
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

type Skill struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Version          string            `json:"version,omitempty"`
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
	Enabled       bool                    `json:"enabled,omitempty"`
	HotCapacity   int                     `json:"hot_capacity,omitempty"`
	WarmCapacity  int                     `json:"warm_capacity,omitempty"`
	ColdCapacity  int                     `json:"cold_capacity,omitempty"`
	HotTTL        string                  `json:"hot_ttl,omitempty"`
	WarmTTL       string                  `json:"warm_ttl,omitempty"`
	PromoteAccess int                     `json:"promote_access,omitempty"`
	DemoteIdle    string                  `json:"demote_idle,omitempty"`
	RecallBudget  MemoryTierRecallBudget  `json:"recall_budget,omitempty"`
	RecallWeights MemoryTierRecallWeights `json:"recall_weights,omitempty"`
	ColdSummary   *MemoryTierColdSummarySettings `json:"cold_summary,omitempty"`
}

type MemoryTierColdSummarySettings struct {
	Enabled         bool  `json:"enabled,omitempty"`
	MinBytes        int64 `json:"min_bytes,omitempty"`
	MaxSummaryChars int   `json:"max_summary_chars,omitempty"`
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
	Mode        OrchestrationMode `json:"mode"`
	Workflow    *Workflow         `json:"workflow,omitempty"`
	Workflows   map[string]Workflow `json:"workflows,omitempty"`
	MaxParallel int               `json:"max_parallel,omitempty"`
	HumanInLoop HumanInLoopPolicy `json:"human_in_loop"`
	Planning    PlanningPolicy    `json:"planning,omitempty"`
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
	Timeout             time.Duration     `json:"timeout,omitempty"`
	MaxSteps            int               `json:"max_steps,omitempty"`
	MaxRetries          int               `json:"max_retries,omitempty"`
	MaxParallel         int               `json:"max_parallel,omitempty"`
	StepOutputThreshold int64             `json:"step_output_threshold,omitempty"`
	Secrets             map[string]string `json:"secrets,omitempty"`
}

type ToolCall struct {
	RunID    string            `json:"run_id"`
	Agent    string            `json:"agent,omitempty"`
	Tool     string            `json:"tool"`
	Input    json.RawMessage   `json:"input,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type ToolResult struct {
	Tool   string          `json:"tool"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type ToolExecutor interface {
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
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
