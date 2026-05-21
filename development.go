package agentflow

import (
	"path/filepath"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
)

// DevelopmentConfig controls local development wiring for scenarios loaded from YAML.
type DevelopmentConfig = DemoConfig

// DevelopmentOptions registers mock LLM gateways and built-in development tool
// executors declared in a scenario. Use for tests and local prototyping; wire
// real executors in production via explicit options or ProductionOptions.
func DevelopmentOptions(scenario core.Scenario, config DevelopmentConfig) ([]Option, error) {
	return DemoOptions(scenario, config)
}

// NewMockLLMGateway creates a fallback mock gateway suitable for local development.
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

func developmentStateOptions(stateDir string) ([]Option, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, nil
	}
	repo, err := NewFileRunStateRepository(filepath.Join(stateDir, "runs"))
	if err != nil {
		return nil, err
	}
	blobs, err := NewFileBlobStore(filepath.Join(stateDir, "blobs"))
	if err != nil {
		return nil, err
	}
	return []Option{WithRunStateRepository(repo), WithBlobStore(blobs)}, nil
}
