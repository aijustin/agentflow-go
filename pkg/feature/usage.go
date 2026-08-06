package feature

import (
	"context"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// UsageAccounting is an optional built-in feature that accumulates
// provider-reported token usage across the steps of a run via the loop-hook
// extension point. It demonstrates (and validates) the feature model; wire it
// with agentflow.WithFeatures(feature.NewUsageAccounting(...)).
//
// The runtime already accumulates per-run usage internally for context
// management; this feature exists for hosts that want the totals pushed to
// their own accounting sink after every step.
type UsageAccounting struct {
	// Report, when set, receives the running totals after every step.
	Report func(ctx context.Context, total llm.TokenUsage)

	mu    sync.Mutex
	total llm.TokenUsage
	steps int
}

// NewUsageAccounting builds the feature; report may be nil.
func NewUsageAccounting(report func(ctx context.Context, total llm.TokenUsage)) *UsageAccounting {
	return &UsageAccounting{Report: report}
}

// Name implements Feature.
func (u *UsageAccounting) Name() string { return "usage_accounting" }

// LoopHooks implements LoopHookContributor.
func (u *UsageAccounting) LoopHooks() LoopHooks {
	return LoopHooks{OnStepFinish: func(ctx context.Context, info StepInfo) {
		u.mu.Lock()
		u.total.InputTokens += info.Usage.InputTokens
		u.total.OutputTokens += info.Usage.OutputTokens
		u.total.ReasoningTokens += info.Usage.ReasoningTokens
		u.total.TotalTokens += info.Usage.TotalTokens
		u.steps++
		total := u.total
		u.mu.Unlock()
		if u.Report != nil {
			u.Report(ctx, total)
		}
	}}
}

// Total returns the accumulated token usage across all observed steps.
func (u *UsageAccounting) Total() llm.TokenUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.total
}

// Steps returns how many loop steps contributed to the totals.
func (u *UsageAccounting) Steps() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.steps
}
