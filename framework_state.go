package agentflow

import (
	"fmt"
	"maps"

	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

// currentScenario returns a snapshot of the live scenario under read lock.
func (f *Framework) currentScenario() core.Scenario {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.scenario
}

// currentEngine returns the live engine pointer under read lock. Callers may
// use the returned pointer after unlock; in-flight work keeps the old engine
// alive when SaveStudioGraph swaps in a replacement.
func (f *Framework) currentEngine() *appexec.Engine {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.engine
}

func wireTierMemory(scenario core.Scenario, cfg *options) error {
	for name, ref := range scenario.Memories {
		if _, exists := cfg.memory[name]; exists {
			continue
		}
		if ref.Tiers != nil && ref.Tiers.Enabled {
			continue
		}
		if ref.Type == "in_memory" {
			cfg.memory[name] = memoryinmem.NewRepository()
		}
	}
	for name, ref := range scenario.Memories {
		if ref.Tiers == nil || !ref.Tiers.Enabled {
			continue
		}
		if _, exists := cfg.tierMemory[name]; exists {
			continue
		}
		store := cfg.tierStores[name]
		if store == nil {
			store = tierinmem.NewStore()
		}
		settings, ok := tier.SettingsFromCore(ref.Tiers)
		if !ok {
			return fmt.Errorf("agentflow: memory %q has invalid tier settings", name)
		}
		policy := settings.Policy()
		if override, ok := cfg.tierStorePolicies[name]; ok {
			policy = override
		}
		coldSummary := tierColdSummaryBackend(settings.ColdSummary, cfg.tierColdIndexers[name], tierColdSummarizer(cfg, name, settings.ColdSummary))
		manager := tier.NewManagerWithWeights(store, policy, tierMigrationObserver(scenario, cfg.recorder, cfg.events), settings.Weights(), coldSummary)
		cognitive := cfg.cognitive[name]
		if cognitive == nil {
			if cfg.cognitive == nil {
				cfg.cognitive = make(map[string]memory.CognitiveMemory)
			}
			cognitive = memoryinmem.NewCognitiveRepository()
			cfg.cognitive[name] = cognitive
		}
		cfg.tierMemory[name] = tier.NewDualWriteManager(manager, cognitive)
	}
	return nil
}

func (f *Framework) engineDependencies(transforms map[string]contextwindow.ToolOutputTransform, drain interjection.DrainPolicy) appexec.Dependencies {
	return appexec.Dependencies{
		LLM:                    f.llm,
		Runs:                   f.runs,
		Blobs:                  f.blobs,
		Events:                 f.events,
		HumanGate:              f.gate,
		ToolApprovalEvaluator:  f.approvalEvaluator,
		Tools:                  f.tools,
		Memory:                 f.memory,
		TierMemory:             f.tierMemory,
		Cognitive:              f.cognitive,
		Policy:                 f.policy,
		Audit:                  f.audit,
		ToolPolicy:             f.toolGov,
		OutputRedactor:         f.redactor,
		LLMPayloadCapture:      f.llmPayloadCapture,
		Recorder:               f.recorder,
		Tracer:                 f.tracer,
		Logger:                 f.logger,
		EnqueueMemoryReconcile: f.enqueueMemoryReconcile,
		ToolOutputTransforms:   transforms,
		InterjectDrain:         drain,
		ToolOrchestrator:       f.toolOrchestrator,
		ApprovalStore:          f.approvalStore,
		TurnStopHook:           f.turnStopHook,
	}
}

// rebuildLiveEngine wires tier memory for scenario and constructs a replacement engine.
// Caller must hold f.mu for write when swapping the result onto Framework.
func (f *Framework) rebuildLiveEngine(scenario core.Scenario) (*appexec.Engine, error) {
	cfg := &options{
		memory:              f.memory,
		tierMemory:          f.tierMemory,
		tierStores:          f.tierStores,
		tierStorePolicies:   f.tierStorePolicies,
		tierColdIndexers:    f.tierColdIndexers,
		tierColdSummarizers: f.tierColdSummarizers,
		cognitive:           f.cognitive,
		recorder:            f.recorder,
		events:              f.events,
		llm:                 f.llm,
	}
	if cfg.memory == nil {
		cfg.memory = make(map[string]memory.Repository)
	}
	if cfg.tierMemory == nil {
		cfg.tierMemory = make(map[string]tier.Manager)
	}
	if cfg.cognitive == nil {
		cfg.cognitive = make(map[string]memory.CognitiveMemory)
	}
	if err := wireTierMemory(scenario, cfg); err != nil {
		return nil, err
	}
	f.memory = cfg.memory
	f.tierMemory = cfg.tierMemory
	f.cognitive = cfg.cognitive

	var transforms map[string]contextwindow.ToolOutputTransform
	var drain interjection.DrainPolicy
	if f.engine != nil {
		transforms, drain = f.engine.LateConfig()
	}
	return appexec.NewEngine(scenario, f.engineDependencies(transforms, drain))
}

// buildEphemeralEngine constructs an engine for an arbitrary scenario without
// mutating Framework state (rebuildLiveEngine swaps the live memory wiring).
// Memory maps are cloned before tier wiring so ephemeral runs cannot leak
// repositories into the live maps. When tools is non-nil it replaces the
// default tool registry (used by graph composition to inject per-call tools).
func (f *Framework) buildEphemeralEngine(scenario core.Scenario, tools appexec.ToolRegistry) (*appexec.Engine, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	cfg := &options{
		memory:              maps.Clone(f.memory),
		tierMemory:          maps.Clone(f.tierMemory),
		tierStores:          f.tierStores,
		tierStorePolicies:   f.tierStorePolicies,
		tierColdIndexers:    f.tierColdIndexers,
		tierColdSummarizers: f.tierColdSummarizers,
		cognitive:           maps.Clone(f.cognitive),
		recorder:            f.recorder,
		events:              f.events,
		llm:                 f.llm,
	}
	if cfg.memory == nil {
		cfg.memory = make(map[string]memory.Repository)
	}
	if cfg.tierMemory == nil {
		cfg.tierMemory = make(map[string]tier.Manager)
	}
	if cfg.cognitive == nil {
		cfg.cognitive = make(map[string]memory.CognitiveMemory)
	}
	if err := wireTierMemory(scenario, cfg); err != nil {
		return nil, err
	}
	var transforms map[string]contextwindow.ToolOutputTransform
	var drain interjection.DrainPolicy
	if f.engine != nil {
		transforms, drain = f.engine.LateConfig()
	}
	deps := f.engineDependencies(transforms, drain)
	deps.Memory = cfg.memory
	deps.TierMemory = cfg.tierMemory
	deps.Cognitive = cfg.cognitive
	if tools != nil {
		deps.Tools = tools
	}
	return appexec.NewEngine(scenario, deps)
}
