package tier

import (
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestSettingsFromCoreDisabledOrNil(t *testing.T) {
	if _, ok := SettingsFromCore(nil); ok {
		t.Fatal("nil settings should be disabled")
	}
	if _, ok := SettingsFromCore(&core.MemoryTierSettings{Enabled: false}); ok {
		t.Fatal("disabled settings should return false")
	}
}

func TestSettingsFromCoreMapsFields(t *testing.T) {
	settings, ok := SettingsFromCore(&core.MemoryTierSettings{
		Enabled:       true,
		HotCapacity:   10,
		WarmCapacity:  20,
		ColdCapacity:  30,
		PromoteAccess: 3,
		HotTTL:        "1h",
		WarmTTL:       "24h",
		DemoteIdle:    "10m",
		RecallBudget:  core.MemoryTierRecallBudget{Total: 8, Hot: 4, Warm: 2, Cold: 2},
		RecallWeights: core.MemoryTierRecallWeights{Semantic: 0.5, Recency: 0.3, Importance: 0.2},
		ColdSummary: &core.MemoryTierColdSummarySettings{
			Enabled: true, MinBytes: 100, MaxSummaryChars: 500, SummaryProfile: "summary",
		},
	})
	if !ok {
		t.Fatal("expected enabled settings")
	}
	if settings.HotCapacity != 10 || settings.WarmCapacity != 20 || settings.ColdCapacity != 30 {
		t.Fatalf("unexpected capacities: %+v", settings)
	}
	if settings.HotTTL != time.Hour || settings.WarmTTL != 24*time.Hour || settings.DemoteIdle != 10*time.Minute {
		t.Fatalf("unexpected TTL: hot=%v warm=%v demote=%v", settings.HotTTL, settings.WarmTTL, settings.DemoteIdle)
	}
	if !settings.ColdSummary.Enabled || settings.ColdSummary.SummaryProfile != "summary" {
		t.Fatalf("unexpected cold summary: %+v", settings.ColdSummary)
	}
}
