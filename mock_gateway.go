package agentflow

import (
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
)

// NewMockLLMGateway creates a fallback mock gateway for tests and examples.
func NewMockLLMGateway(scenario core.Scenario) llm.Gateway {
	gateway := llmmock.NewFallbackGateway()
	for name, ref := range scenario.LLMs {
		if ref.Provider != "mock" {
			continue
		}
		gateway.SetCapabilities(name, llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput, llm.CapStream, llm.CapEmbed)
	}
	return gateway
}
