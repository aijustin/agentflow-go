package local

import (
	"net/http"

	"github.com/aijustin/agentflow-go/internal/adapter/llm/openai"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// Gateway targets OpenAI-compatible local model endpoints such as Ollama,
// LM Studio, vLLM, or llama.cpp servers.
type Gateway = openai.Gateway

func NewGateway(profiles []llm.Profile, client *http.Client) *Gateway {
	return openai.NewGateway(profiles, client)
}
