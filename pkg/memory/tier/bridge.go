package tier

import (
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// SettingsFromCore converts a scenario memory tier reference into tier settings.
func SettingsFromCore(ref *core.MemoryTierSettings) (Settings, bool) {
	if ref == nil || !ref.Enabled {
		return Settings{}, false
	}
	settings := Settings{
		Enabled:       true,
		HotCapacity:   ref.HotCapacity,
		WarmCapacity:  ref.WarmCapacity,
		ColdCapacity:  ref.ColdCapacity,
		PromoteAccess: ref.PromoteAccess,
		RecallBudget: RecallBudget{
			Total: ref.RecallBudget.Total,
			Hot:   ref.RecallBudget.Hot,
			Warm:  ref.RecallBudget.Warm,
			Cold:  ref.RecallBudget.Cold,
		},
		RecallWeights: RecallWeights{
			Semantic:   ref.RecallWeights.Semantic,
			Recency:    ref.RecallWeights.Recency,
			Importance: ref.RecallWeights.Importance,
		},
	}
	if ref.HotTTL != "" {
		if d, err := time.ParseDuration(ref.HotTTL); err == nil {
			settings.HotTTL = d
		}
	}
	if ref.WarmTTL != "" {
		if d, err := time.ParseDuration(ref.WarmTTL); err == nil {
			settings.WarmTTL = d
		}
	}
	if ref.DemoteIdle != "" {
		if d, err := time.ParseDuration(ref.DemoteIdle); err == nil {
			settings.DemoteIdle = d
		}
	}
	return settings, true
}
