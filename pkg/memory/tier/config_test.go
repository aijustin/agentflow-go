package tier

import (
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestSettingsPolicyBudgetWeights(t *testing.T) {
	settings := Settings{
		HotCapacity:   5,
		WarmCapacity:  10,
		ColdCapacity:  20,
		HotTTL:        time.Hour,
		WarmTTL:       24 * time.Hour,
		PromoteAccess: 3,
		DemoteIdle:    time.Minute,
		RecallBudget:  RecallBudget{Total: 8, Hot: 3, Warm: 3, Cold: 2},
		RecallWeights: RecallWeights{Semantic: 0.5, Recency: 0.3, Importance: 0.2},
	}
	policy := settings.Policy()
	if policy.HotCapacity != 5 || policy.WarmCapacity != 10 || policy.ColdCapacity != 20 {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	if policy.HotTTL != time.Hour || policy.PromoteAccess != 3 {
		t.Fatalf("unexpected policy timing: %+v", policy)
	}
	budget := settings.Budget()
	if budget.Total != 8 || budget.Hot != 3 {
		t.Fatalf("unexpected budget: %+v", budget)
	}
	weights := settings.Weights()
	if weights.Semantic <= 0 || weights.Recency <= 0 || weights.Importance <= 0 {
		t.Fatalf("expected normalized weights, got %+v", weights)
	}
}

func TestSettingsDefaultBudgetWhenEmpty(t *testing.T) {
	budget := Settings{}.Budget()
	if budget.Total != 20 {
		t.Fatalf("expected default total 20, got %+v", budget)
	}
}

func TestRecallWeightsMemoryWeightsNormalize(t *testing.T) {
	got := RecallWeights{Semantic: 2, Recency: 2, Importance: 2}.memoryWeights()
	want := memory.RecallWeights{Semantic: 2, Recency: 2, Importance: 2}.Normalize()
	if got != want {
		t.Fatalf("memoryWeights = %+v, want %+v", got, want)
	}
}
