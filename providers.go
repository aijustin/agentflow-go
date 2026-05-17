package agentflow

import (
	"net/http"

	"github.com/aijustin/agentflow-go/internal/adapter/llm/anthropic"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/local"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/openai"
	llmrouter "github.com/aijustin/agentflow-go/internal/adapter/llm/router"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type OpenAICompatibleProvider interface {
	llm.Gateway
	llm.Embedder
}

type LLMProviderRouter interface {
	llm.Gateway
	llm.Embedder
}

// NewOpenAICompatibleGateway creates a gateway for OpenAI-compatible chat APIs.
func NewOpenAICompatibleGateway(profiles []llm.Profile, client *http.Client) llm.Gateway {
	return openai.NewGateway(profiles, client)
}

// NewOpenAICompatibleProvider creates a gateway/embedder for OpenAI-compatible APIs.
func NewOpenAICompatibleProvider(profiles []llm.Profile, client *http.Client) OpenAICompatibleProvider {
	return openai.NewGateway(profiles, client)
}

// NewOpenAICompatibleEmbedder creates an embedder for OpenAI-compatible embedding APIs.
func NewOpenAICompatibleEmbedder(profiles []llm.Profile, client *http.Client) llm.Embedder {
	return openai.NewGateway(profiles, client)
}

// NewLocalGateway creates a gateway for local OpenAI-compatible model servers.
func NewLocalGateway(profiles []llm.Profile, client *http.Client) llm.Gateway {
	return local.NewGateway(profiles, client)
}

// NewAnthropicGateway creates a gateway for Anthropic Messages APIs.
func NewAnthropicGateway(profiles []llm.Profile, client *http.Client) llm.Gateway {
	return anthropic.NewGateway(profiles, client)
}

// NewLLMRouter routes profile names to provider-specific gateways.
func NewLLMRouter(routes map[string]llm.Gateway) llm.Gateway {
	return llmrouter.New(routes)
}

// NewLLMProviderRouter routes chat/tool/structured/streaming and embedding
// calls by profile name when the selected route supports the requested capability.
func NewLLMProviderRouter(routes map[string]llm.Gateway) LLMProviderRouter {
	return llmrouter.New(routes)
}
