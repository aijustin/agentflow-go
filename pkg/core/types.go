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
	Tools         map[string]Tool          `json:"tools,omitempty"`
	Skills        map[string]Skill         `json:"skills,omitempty"`
	Agents        map[string]Agent         `json:"agents,omitempty"`
	Orchestration Orchestration            `json:"orchestration"`
	Runtime       RuntimePolicy            `json:"runtime"`
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
	Type      string            `json:"type"`
	Scope     string            `json:"scope"`
	Namespace string            `json:"namespace,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
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
	MaxParallel int               `json:"max_parallel,omitempty"`
	HumanInLoop HumanInLoopPolicy `json:"human_in_loop"`
	Planning    PlanningPolicy    `json:"planning,omitempty"`
}

type PlanningPolicy struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Agent    string `json:"agent,omitempty"`
	MaxSteps int    `json:"max_steps,omitempty"`
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
