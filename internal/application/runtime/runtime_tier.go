package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

func (e *Engine) tierManager(agent core.Agent) (tier.Manager, tier.Settings, bool) {
	if agent.Memory == "" || e.tierMemory == nil {
		return nil, tier.Settings{}, false
	}
	manager, ok := e.tierMemory[agent.Memory]
	if !ok || manager == nil {
		return nil, tier.Settings{}, false
	}
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok {
		return nil, tier.Settings{}, false
	}
	settings, enabled := tier.SettingsFromCore(ref.Tiers)
	if !enabled {
		return nil, tier.Settings{}, false
	}
	return manager, settings, true
}

func (e *Engine) tierRecallBudget(agent core.Agent, settings tier.Settings) tier.RecallBudget {
	budget := settings.Budget()
	if profile, ok := e.scenario.LLMs[agent.LLM]; ok {
		limit := profile.Context.Normalize().MemoryRecallLimit
		if limit > 0 && budget.Total > limit {
			budget.Total = limit
			budget = budget.Normalize()
		}
	}
	return budget
}

func (e *Engine) scopedMemoryNamespace(ctx context.Context, runID string, agent core.Agent) (memory.Namespace, bool) {
	ns, ok := e.memoryNamespace(runID, agent)
	if !ok {
		return memory.Namespace{}, false
	}
	if principal, ok := identity.PrincipalFromContext(ctx); ok {
		ns = memory.TenantScopedNamespace(ns, principal.Scope.TenantID)
	}
	return ns, true
}

func (e *Engine) ReconcileTierMemory(ctx context.Context, runID, memoryName, agentName string) error {
	agent, ok := e.scenario.Agents[agentName]
	if !ok {
		return fmt.Errorf("runtime: unknown agent %q", agentName)
	}
	if agent.Memory != memoryName {
		return fmt.Errorf("runtime: agent %q is not bound to memory %q", agentName, memoryName)
	}
	manager, _, ok := e.tierManager(agent)
	if !ok {
		return fmt.Errorf("runtime: memory %q is not tier-enabled", memoryName)
	}
	ns, ok := e.scopedMemoryNamespace(ctx, runID, agent)
	if !ok {
		return fmt.Errorf("runtime: memory namespace unavailable for agent %q", agentName)
	}
	ctx, span := e.startSpan(ctx, observability.SpanMemoryTierMigrate,
		observability.Attribute{Key: "memory", Value: memoryName},
		observability.Attribute{Key: "agent", Value: agentName},
	)
	defer span.End()
	_, err := manager.Reconcile(tier.WithMigrationRunID(ctx, runID), ns, time.Now().UTC())
	return err
}

func (e *Engine) enqueueTierReconcile(ctx context.Context, runID string, agent core.Agent) {
	if e.enqueueMemoryReconcile == nil || agent.Memory == "" {
		return
	}
	payload := asyncpkg.MemoryReconcilePayload{
		MemoryName: agent.Memory,
		Agent:      agent.Name,
		RunID:      runID,
	}
	if principal, ok := identity.PrincipalFromContext(ctx); ok {
		payload.Principal = principal
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		e.logWarn(ctx, "runtime: marshal tier reconcile payload failed", "run_id", runID, "agent", agent.Name, "memory", agent.Memory, "error", err)
		return
	}
	// A failure here just means the async reconcile pass for this write
	// won't be scheduled; it must not fail the write itself. Log it so a
	// stuck queue or broken enqueuer is visible instead of silently
	// leaving tier memory unreconciled.
	if err := e.enqueueMemoryReconcile(ctx, asyncpkg.Job{
		ID:          generateRunID(),
		Type:        asyncpkg.MemoryReconcileJobType,
		RunID:       runID,
		Payload:     raw,
		MaxAttempts: 3,
	}); err != nil {
		e.logWarn(ctx, "runtime: enqueue tier reconcile job failed", "run_id", runID, "agent", agent.Name, "memory", agent.Memory, "error", err)
	}
}
