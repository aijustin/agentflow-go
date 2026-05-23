package tier

import (
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

// Settings configures tiered memory for a scenario memory reference.
type Settings struct {
	Enabled       bool          `json:"enabled,omitempty" yaml:"enabled"`
	HotCapacity   int           `json:"hot_capacity,omitempty" yaml:"hot_capacity"`
	WarmCapacity  int           `json:"warm_capacity,omitempty" yaml:"warm_capacity"`
	ColdCapacity  int           `json:"cold_capacity,omitempty" yaml:"cold_capacity"`
	HotTTL        time.Duration `json:"hot_ttl,omitempty" yaml:"hot_ttl"`
	WarmTTL       time.Duration `json:"warm_ttl,omitempty" yaml:"warm_ttl"`
	PromoteAccess int           `json:"promote_access,omitempty" yaml:"promote_access"`
	DemoteIdle    time.Duration `json:"demote_idle,omitempty" yaml:"demote_idle"`
	RecallBudget  RecallBudget  `json:"recall_budget,omitempty" yaml:"recall_budget"`
	RecallWeights RecallWeights `json:"recall_weights,omitempty" yaml:"recall_weights"`
	ColdSummary   ColdSummarySettings `json:"cold_summary,omitempty" yaml:"cold_summary"`
}

// Policy returns the effective tier policy from settings, overlaying defaults.
func (s Settings) Policy() Policy {
	p := DefaultPolicy()
	if s.HotCapacity > 0 {
		p.HotCapacity = s.HotCapacity
	}
	if s.WarmCapacity > 0 {
		p.WarmCapacity = s.WarmCapacity
	}
	if s.ColdCapacity > 0 {
		p.ColdCapacity = s.ColdCapacity
	}
	if s.HotTTL > 0 {
		p.HotTTL = s.HotTTL
	}
	if s.WarmTTL > 0 {
		p.WarmTTL = s.WarmTTL
	}
	if s.PromoteAccess > 0 {
		p.PromoteAccess = s.PromoteAccess
	}
	if s.DemoteIdle > 0 {
		p.DemoteIdle = s.DemoteIdle
	}
	return p
}

// Budget returns the normalized recall budget for tier recall.
func (s Settings) Budget() RecallBudget {
	if s.RecallBudget.Total == 0 && s.RecallBudget.Hot == 0 && s.RecallBudget.Warm == 0 && s.RecallBudget.Cold == 0 {
		return RecallBudget{Total: 20}.Normalize()
	}
	return s.RecallBudget.Normalize()
}

// Weights returns normalized RankMemories weights.
func (s Settings) Weights() memory.RecallWeights {
	return s.RecallWeights.memoryWeights()
}
