package agentflow

import (
	"context"
	"encoding/json"
	"testing"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestMemoryReconcileJobHandler(t *testing.T) {
	scenario := core.Scenario{
		Name: "tier-reconcile",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Memories: map[string]core.MemoryRef{
			"session": {
				Type:  "custom",
				Scope: string(memory.ScopeSession),
				Tiers: &core.MemoryTierSettings{Enabled: true, HotCapacity: 1},
			},
		},
		Agents: map[string]core.Agent{
			"assistant": {LLM: "default", Memory: "session"},
		},
	}
	fw, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewFrameworkJobHandler(FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(asyncpkg.MemoryReconcilePayload{
		MemoryName: "session",
		Agent:      "assistant",
		RunID:      "run-reconcile",
		Principal: identity.Principal{
			ID:    "svc",
			Type:  identity.PrincipalService,
			Roles: []identity.Role{identity.RoleService},
			Scope: identity.Scope{TenantID: "tenant-a"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleJob(context.Background(), asyncpkg.Job{
		ID:          "job-1",
		Type:        asyncpkg.MemoryReconcileJobType,
		RunID:       "run-reconcile",
		Payload:     payload,
		MaxAttempts: 1,
	}); err != nil {
		t.Fatal(err)
	}
}
