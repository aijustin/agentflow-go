// Package toolorch provides Codex-inspired tool orchestration helpers for
// autonomous runs: sampling step freeze, approval cache, and deny breakers.
// OS sandbox / escalate remain host responsibilities.
package toolorch

import (
	"slices"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// SamplingStepContext freezes the tool advertisement view for one sampling
// step. Tool dispatch must honor Allows so "tools the model saw" cannot drift
// from "tools the runtime will execute" within the same step.
type SamplingStepContext struct {
	AdvertisedTools []string
	allowed         map[string]struct{}
}

// FreezeSamplingStepContext captures the advertised tool names from specs.
func FreezeSamplingStepContext(specs []llm.ToolSpec) SamplingStepContext {
	names := make([]string, 0, len(specs))
	allowed := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		allowed[name] = struct{}{}
		names = append(names, name)
	}
	slices.Sort(names)
	return SamplingStepContext{AdvertisedTools: names, allowed: allowed}
}

// Allows reports whether tool was advertised in this sampling step.
func (s SamplingStepContext) Allows(tool string) bool {
	if len(s.allowed) == 0 {
		// Zero value means no freeze — callers treat as unrestricted.
		return true
	}
	_, ok := s.allowed[strings.TrimSpace(tool)]
	return ok
}

// Frozen reports whether a non-empty advertisement set was captured.
func (s SamplingStepContext) Frozen() bool {
	return len(s.allowed) > 0
}
