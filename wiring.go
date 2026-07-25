package agentflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstaterecording "github.com/aijustin/agentflow-go/internal/adapter/runstate/recording"
	schemamigrations "github.com/aijustin/agentflow-go/migrations/postgres"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// --- Wiring Validation ---

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

func autoMemoryNames(scenario core.Scenario) map[string]bool {
	autoMemory := make(map[string]bool)
	for name, ref := range scenario.Memories {
		if ref.Type == "in_memory" {
			autoMemory[name] = true
		}
	}
	return autoMemory
}

func buildWiringOptions(scenario core.Scenario, opts ...Option) (options, map[string]bool, error) {
	cfg := defaultOptions()
	autoMemory := autoMemoryNames(scenario)
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
	// A job queue shared across workers plus a shared run-state repository is
	// a multi-node deployment; without run-lease coordination a crashed worker
	// leaves its runs in Running forever and redelivered run jobs dead-letter
	// on ErrRunInProgress. Warn loudly but do not fail: single-node and
	// in-memory setups are legitimate without a lease.
	if cfg.jobQueue != nil && cfg.runLocker == nil && cfg.runs != nil && !isInMemoryRunState(cfg.runs) && cfg.logger != nil {
		cfg.logger.Warn(context.Background(), "agentflow: wiring: job queue and shared run-state repository configured without WithRunLease: a crashed worker leaves runs stuck in Running and redelivered run jobs dead-letter; configure WithRunLease (and consider WithRunReaper) for multi-node deployments")
	}
	// A PostgreSQL run-state repository must sit on a schema new enough for
	// the columns the code writes (fence_token, added in migration 0004).
	// Failing here turns a runtime "column does not exist" into a boot-time
	// error that names the fix.
	if err := checkRunStateSchemaVersion(cfg.runs); err != nil {
		return err
	}
	return nil
}

// schemaVersionChecker is implemented by run-state repositories that can
// report the applied schema migration version (the PostgreSQL adapter).
type schemaVersionChecker interface {
	SchemaVersion(ctx context.Context) (int, error)
}

func checkRunStateSchemaVersion(repo runstate.Repository) error {
	checker, ok := unwrapRunState(repo).(schemaVersionChecker)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := checker.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("agentflow: wiring: could not verify run-state schema version: %w", err)
	}
	if version < schemamigrations.RequiredVersion {
		return fmt.Errorf("agentflow: wiring: run-state schema version %d is older than required version %d: apply the PostgreSQL migrations first (migrations/postgres, e.g. examples/deploy/init/apply-migrations.sh)", version, schemamigrations.RequiredVersion)
	}
	return nil
}

// unwrapRunState peels the checkpoint-history recording wrapper so capability
// checks (in-memory detection, schema version) reach the real repository.
func unwrapRunState(repo runstate.Repository) runstate.Repository {
	if recording, ok := repo.(*runstaterecording.Repository); ok {
		return recording.Inner
	}
	return repo
}

// isInMemoryRunState reports whether repo is the built-in in-process
// run-state repository (optionally wrapped by checkpoint-history recording),
// in which case a missing run lease is not a multi-node hazard.
func isInMemoryRunState(repo runstate.Repository) bool {
	switch repo := repo.(type) {
	case *runstateinmem.Repository:
		return true
	case *runstaterecording.Repository:
		return isInMemoryRunState(repo.Inner)
	default:
		return false
	}
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
		tools:                  make(map[string]core.ToolExecutor),
		memory:                 make(map[string]memory.Repository),
		tierMemory:             make(map[string]tier.Manager),
		tierStores:             make(map[string]tier.Store),
		cognitive:              make(map[string]memory.CognitiveMemory),
		tokenWriter:            discardWriter{},
		toolResolverCacheLimit: defaultToolResolverCacheLimit,
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
