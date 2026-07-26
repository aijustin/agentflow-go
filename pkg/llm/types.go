package llm

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
)

type Capability int

const (
	CapChat Capability = iota
	CapToolCall
	CapStructuredOutput
	CapStream
	CapEmbed
)

func (c Capability) String() string {
	switch c {
	case CapChat:
		return "chat"
	case CapToolCall:
		return "tool_call"
	case CapStructuredOutput:
		return "structured_output"
	case CapStream:
		return "stream"
	case CapEmbed:
		return "embed"
	default:
		return "unknown"
	}
}

type Profile struct {
	Name                string               `json:"name"`
	Provider            string               `json:"provider"`
	Model               string               `json:"model"`
	Endpoint            string               `json:"endpoint,omitempty"`
	APIKeyEnv           string               `json:"api_key_env,omitempty"`
	ContextWindowTokens int                  `json:"context_window_tokens,omitempty"`
	MaxOutputTokens     int                  `json:"max_output_tokens,omitempty"`
	Temperature         *float32             `json:"temperature,omitempty"`
	TopP                *float32             `json:"top_p,omitempty"`
	Timeout             time.Duration        `json:"timeout,omitempty"`
	Thinking            ThinkingConfig       `json:"thinking,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	PromptCache         PromptCacheConfig    `json:"prompt_cache,omitempty"`
	Context             contextwindow.Policy `json:"context,omitempty"`
	ExtraBody           map[string]any       `json:"extra_body,omitempty"`
	Capabilities        []Capability         `json:"capabilities,omitempty"`
	Metadata            map[string]string    `json:"metadata,omitempty"`
}

type ThinkingConfig struct {
	Enabled      bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	BudgetTokens int  `json:"budget_tokens,omitempty" yaml:"budget_tokens,omitempty"`
}

// PromptCacheConfig asks providers that require explicit cache markers to
// cache the stable head of the prompt.
//
// It is off by default because it is not free in every shape of workload:
// providers bill a premium for writing the cache, so a profile that issues one
// short single-turn call and never reuses the prefix pays more. It pays for
// itself as soon as the prefix is re-sent, which is every iteration of a tool
// loop, and that is the runtime's primary path.
//
// Providers that cache automatically by matching a stable prefix (the
// OpenAI-compatible family) ignore this setting; for them what matters is that
// the prefix does not churn between turns.
type PromptCacheConfig struct {
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role             Role              `json:"role"`
	Content          string            `json:"content,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	Name             string            `json:"name,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type ChatRequest struct {
	Messages        []Message         `json:"messages"`
	Temperature     *float32          `json:"temperature,omitempty"`
	TopP            *float32          `json:"top_p,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Thinking        ThinkingConfig    `json:"thinking,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	ExtraBody       map[string]any    `json:"extra_body,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type ChatResponse struct {
	Message      Message         `json:"message"`
	Usage        TokenUsage      `json:"usage"`
	FinishReason string          `json:"finish_reason,omitempty"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

// ChunkKind discriminates Stream progress events. Empty Kind is treated as
// answer content for backward compatibility with provider token streams.
type ChunkKind string

const (
	ChunkKindContent      ChunkKind = ""
	ChunkKindToolCall     ChunkKind = "tool_call"
	ChunkKindToolResult   ChunkKind = "tool_result"
	ChunkKindToolDenied   ChunkKind = "tool_denied"
	ChunkKindToolProgress ChunkKind = "tool_progress"
)

type ChatChunk struct {
	Kind    ChunkKind `json:"kind,omitempty"`
	Content string    `json:"content,omitempty"`
	Done    bool      `json:"done,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Err carries the structured provider error behind Error when one exists,
	// so retry classification (e.g. APIError.Retryable) survives the streaming
	// path instead of being flattened into an opaque string. It is process-local
	// only and never serialized.
	Err          error           `json:"-"`
	Usage        TokenUsage      `json:"usage,omitempty"`
	Paused       bool            `json:"paused,omitempty"`
	PauseToken   string          `json:"pause_token,omitempty"`
	PauseKind    string          `json:"pause_kind,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput   json.RawMessage `json:"tool_output,omitempty"`
	ToolError    string          `json:"tool_error,omitempty"`
	ToolProgress json.RawMessage `json:"tool_progress,omitempty"`
}

// IsAnswerContent reports whether this chunk contributes to the final answer
// text aggregated by Engine.Stream. Tool progress/result events must not.
func (c ChatChunk) IsAnswerContent() bool {
	return c.Kind == ChunkKindContent
}

// TokenUsage reports one call's token accounting.
//
// Providers disagree on whether cached prompt tokens are part of the input
// count: OpenAI reports cached_tokens as a subset of prompt_tokens, while
// Anthropic reports cache reads and writes *alongside* input_tokens. Adapters
// normalize to one meaning so a cache hit rate is comparable across providers:
// InputTokens is always the full prompt, and CachedInputTokens is the part of
// it the provider served from cache.
type TokenUsage struct {
	// InputTokens is the whole prompt, including any part served from cache.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	// CachedInputTokens is the subset of InputTokens read from the provider's
	// prompt cache and billed at a discount. CachedInputTokens/InputTokens is
	// the cache hit rate, the highest-leverage cost number an agent runtime
	// controls: an uncached prefix is re-billed on every turn of a tool loop.
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	// CacheWriteTokens is the subset of InputTokens written into the cache by
	// this call. Providers bill writes at a premium over ordinary input, so a
	// workload that only ever writes and never reads is paying for nothing.
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// UncachedInputTokens is the part of the prompt billed at full input price.
func (u TokenUsage) UncachedInputTokens() int {
	if u.CachedInputTokens >= u.InputTokens {
		return 0
	}
	return u.InputTokens - u.CachedInputTokens
}

// CacheHitRate is the fraction of the prompt served from cache, in [0,1]. It
// reports 0 when the prompt size is unknown.
func (u TokenUsage) CacheHitRate() float64 {
	if u.InputTokens <= 0 {
		return 0
	}
	rate := float64(u.CachedInputTokens) / float64(u.InputTokens)
	if rate > 1 {
		return 1
	}
	return rate
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type ToolCallRequest struct {
	ChatRequest
	Tools []ToolSpec `json:"tools,omitempty"`
}

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

type ToolCallResponse struct {
	ChatResponse
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Chatter interface {
	Chat(ctx context.Context, profile string, req ChatRequest) (ChatResponse, error)
}

type Streamer interface {
	StreamChat(ctx context.Context, profile string, req ChatRequest) (<-chan ChatChunk, error)
}

type ToolCaller interface {
	ChatWithTools(ctx context.Context, profile string, req ToolCallRequest) (ToolCallResponse, error)
}

// ToolCallStreamer streams a tool-enabled chat turn. Content can precede
// ChunkKindToolCall frames, so consumers must classify the completed turn
// before committing content as a final answer.
type ToolCallStreamer interface {
	StreamChatWithTools(ctx context.Context, profile string, req ToolCallRequest) (<-chan ChatChunk, error)
}

type StructuredOutputter interface {
	StructuredChat(ctx context.Context, profile string, schema json.RawMessage, req ChatRequest) (json.RawMessage, error)
}

type Embedder interface {
	Embed(ctx context.Context, profile string, input []string) ([][]float32, error)
}

type Gateway interface {
	Supports(profile string, cap Capability) bool
	Chatter
}
