// Package feature defines the host feature contribution model: small,
// composable extensions that wire LLM middleware, tool inspectors, tool-loop
// hooks, and stop conditions into the runtime through one option
// (agentflow.WithFeatures) instead of a growing list of dedicated options.
//
// A Feature implements only the contribution interfaces it cares about; the
// runtime discovers them with type assertions. Collection is error-isolated:
// a feature that panics or misbehaves while contributing loses only that
// contribution, never the whole run.
package feature

import (
	"context"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/toolinspect"
)

// Feature is a named host extension. Implement any of the optional
// contributor interfaces below to extend the runtime.
type Feature interface {
	Name() string
}

// LLMMiddlewareContributor wraps the tool-calling LLM gateway used by the
// autonomous tool loop. Wrappers apply in feature order: the first feature's
// wrapper is the innermost.
type LLMMiddlewareContributor interface {
	Feature
	WrapLLMGateway(inner llm.ToolCaller) llm.ToolCaller
}

// ToolInspectorContributor contributes tool inspectors appended after the
// built-in dispatch gates (see pkg/toolinspect).
type ToolInspectorContributor interface {
	Feature
	ToolInspectors() []toolinspect.Inspector
}

// StepInfo describes one completed autonomous tool-loop step.
type StepInfo struct {
	RunID string
	Agent string
	// Step is the 1-based logical step (stable across pause/resume).
	Step int
	// ToolCalls is the number of tool calls the step's assistant turn
	// requested (0 on the final-answer step).
	ToolCalls int
	// Usage is the provider-reported token usage of the step's LLM call.
	Usage llm.TokenUsage
}

// LoopHooks are optional callbacks into the autonomous tool loop.
type LoopHooks struct {
	// OnStepFinish runs after each completed loop step, once the step's
	// conversation state has been persisted. Panics are recovered by the
	// runtime and logged, never fatal.
	OnStepFinish func(ctx context.Context, info StepInfo)
}

// LoopHookContributor contributes tool-loop hooks.
type LoopHookContributor interface {
	Feature
	LoopHooks() LoopHooks
}

// StopCondition decides after a tool-executing step whether the run must
// stop. A non-empty reason becomes the run's termination_reason (see
// core.TerminationReason*); an empty reason defaults to "error".
type StopCondition func(ctx context.Context, info StepInfo) (reason string, stop bool)

// StopConditionContributor contributes stop conditions evaluated after every
// tool-executing loop step.
type StopConditionContributor interface {
	Feature
	StopConditions() []StopCondition
}

// Contributions is everything collected from a feature set.
type Contributions struct {
	// LLMWrappers wrap the tool-calling gateway in feature order (first
	// feature innermost).
	LLMWrappers []func(llm.ToolCaller) llm.ToolCaller
	// Inspectors are appended after the built-in dispatch gates.
	Inspectors []toolinspect.Inspector
	// Hooks fire on every completed tool-loop step.
	Hooks []LoopHooks
	// StopConditions are evaluated after every tool-executing step, in order.
	StopConditions []StopCondition
}

// Empty reports whether no feature contributed anything.
func (c Contributions) Empty() bool {
	return len(c.LLMWrappers) == 0 && len(c.Inspectors) == 0 && len(c.Hooks) == 0 && len(c.StopConditions) == 0
}

// Collect gathers the contributions of every feature. Each contribution call
// is isolated: a panicking (or nil-returning) feature loses only that
// contribution and the failure is logged; remaining features collect
// normally.
func Collect(features []Feature, logger log.Logger) Contributions {
	var out Contributions
	for _, f := range features {
		if f == nil {
			continue
		}
		name := featureName(f)
		if contributor, ok := f.(LLMMiddlewareContributor); ok {
			out.LLMWrappers = append(out.LLMWrappers, contributor.WrapLLMGateway)
		}
		if contributor, ok := f.(ToolInspectorContributor); ok {
			if inspectors, ok := collectIsolated(logger, name, "tool_inspectors", contributor.ToolInspectors); ok {
				for _, inspector := range inspectors {
					if inspector != nil {
						out.Inspectors = append(out.Inspectors, inspector)
					}
				}
			}
		}
		if contributor, ok := f.(LoopHookContributor); ok {
			if hooks, ok := collectIsolated(logger, name, "loop_hooks", contributor.LoopHooks); ok {
				out.Hooks = append(out.Hooks, hooks)
			}
		}
		if contributor, ok := f.(StopConditionContributor); ok {
			if conditions, ok := collectIsolated(logger, name, "stop_conditions", contributor.StopConditions); ok {
				for _, condition := range conditions {
					if condition != nil {
						out.StopConditions = append(out.StopConditions, condition)
					}
				}
			}
		}
	}
	return out
}

// featureName resolves a feature name defensively: Name() itself may panic.
func featureName(f Feature) (name string) {
	defer func() {
		if r := recover(); r != nil {
			name = fmt.Sprintf("%T", f)
		}
	}()
	return f.Name()
}

// collectIsolated runs one contribution call, converting a panic into a
// dropped contribution plus a log line.
func collectIsolated[T any](logger log.Logger, feature, kind string, fn func() T) (result T, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			logWarn(logger, "feature: contribution dropped after panic", "feature", feature, "contribution", kind, "panic", fmt.Sprint(r))
			result = *new(T)
			ok = false
		}
	}()
	return fn(), true
}

func logWarn(logger log.Logger, msg string, keysAndValues ...any) {
	if logger != nil {
		logger.Warn(context.Background(), msg, keysAndValues...)
	}
}
