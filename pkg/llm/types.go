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
	Context             contextwindow.Policy `json:"context,omitempty"`
	ExtraBody           map[string]any       `json:"extra_body,omitempty"`
	Capabilities        []Capability         `json:"capabilities,omitempty"`
	Metadata            map[string]string    `json:"metadata,omitempty"`
}

type ThinkingConfig struct {
	Enabled      bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	BudgetTokens int  `json:"budget_tokens,omitempty" yaml:"budget_tokens,omitempty"`
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

type ChatChunk struct {
	Content string     `json:"content,omitempty"`
	Done    bool       `json:"done,omitempty"`
	Error   string     `json:"error,omitempty"`
	Usage   TokenUsage `json:"usage,omitempty"`
}

type TokenUsage struct {
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	TotalTokens     int `json:"total_tokens,omitempty"`
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
