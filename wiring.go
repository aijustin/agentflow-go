package agentflow

import (
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

// WiringOptions controls ValidateWiring and optional New-time checks.
type WiringOptions struct {
	RequireLLM                 bool
	AllowMockProviderWithoutGW bool
}

// WithRequireLLM makes New fail when no LLM gateway is wired.
func WithRequireLLM() Option {
	return func(o *options) error {
		o.requireLLM = true
		return nil
	}
}

// ValidateWiring checks that a scenario's declared dependencies are covered by
// the provided options before constructing a Framework.
func ValidateWiring(scenario core.Scenario, opts ...Option) error {
	cfg, autoMemory, err := buildWiringOptions(scenario, opts...)
	if err != nil {
		return err
	}
	return validateWiring(scenario, cfg, autoMemory, defaultWiringOptions())
}

// ValidateWiringWithOptions validates wiring using explicit wiring rules.
func ValidateWiringWithOptions(scenario core.Scenario, wiring WiringOptions, opts ...Option) error {
	cfg, autoMemory, err := buildWiringOptions(scenario, opts...)
	if err != nil {
		return err
	}
	return validateWiring(scenario, cfg, autoMemory, wiring)
}

func defaultWiringOptions() WiringOptions {
	return WiringOptions{AllowMockProviderWithoutGW: true}
}

func buildWiringOptions(scenario core.Scenario, opts ...Option) (options, map[string]bool, error) {
	cfg := defaultOptions()
	autoMemory := make(map[string]bool)
	for name, ref := range scenario.Memories {
		if ref.Type == "in_memory" {
			autoMemory[name] = true
		}
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return options{}, nil, err
		}
	}
	return cfg, autoMemory, nil
}

func validateWiring(scenario core.Scenario, cfg options, autoMemory map[string]bool, rules WiringOptions) error {
	if rules.RequireLLM || scenarioNeedsLLM(scenario, rules) {
		if cfg.llm == nil {
			return fmt.Errorf("agentflow: wiring: LLM gateway is required but not configured")
		}
	}
	for name, tool := range scenario.Tools {
		if cfg.resolver != nil {
			continue
		}
		if _, ok := cfg.tools[name]; ok {
			continue
		}
		if isDevelopmentBuiltinTool(tool.Type) {
			continue
		}
		if strings.TrimSpace(tool.Type) == "" {
			return fmt.Errorf("agentflow: wiring: tool %q is missing type", name)
		}
		return fmt.Errorf("agentflow: wiring: tool %q (%s) has no executor or resolver", name, tool.Type)
	}
	for name, ref := range scenario.Memories {
		if ref.Tiers != nil && ref.Tiers.Enabled {
			continue
		}
		if ref.Type == "in_memory" || autoMemory[name] {
			continue
		}
		if _, ok := cfg.memory[name]; !ok {
			return fmt.Errorf("agentflow: wiring: memory %q (%s) has no repository", name, ref.Type)
		}
	}
	if scenario.Orchestration.HumanInLoop.Enabled {
		if cfg.gate == nil && len(cfg.tokenSecret) == 0 {
			return fmt.Errorf("agentflow: wiring: human-in-the-loop is enabled but no HumanGate or HITL token secret is configured")
		}
	}
	return nil
}

func scenarioNeedsLLM(scenario core.Scenario, rules WiringOptions) bool {
	if len(scenario.LLMs) == 0 {
		return false
	}
	if rules.AllowMockProviderWithoutGW {
		allMock := true
		for _, ref := range scenario.LLMs {
			if strings.TrimSpace(ref.Provider) != "mock" {
				allMock = false
				break
			}
		}
		if allMock {
			return false
		}
	}
	for _, agent := range scenario.Agents {
		if strings.TrimSpace(agent.LLM) != "" {
			return true
		}
	}
	return len(scenario.LLMs) > 0
}

func isDevelopmentBuiltinTool(toolType string) bool {
	switch strings.TrimSpace(toolType) {
	case "builtin.echo", "builtin.repo_search", "builtin.git", "builtin.ticket":
		return true
	default:
		return false
	}
}

func defaultOptions() options {
	return options{
		tools:       make(map[string]core.ToolExecutor),
		memory:      make(map[string]memory.Repository),
		tierMemory:  make(map[string]tier.Manager),
		tierStores:  make(map[string]tier.Store),
		cognitive:   make(map[string]memory.CognitiveMemory),
		tokenWriter: discardWriter{},
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
