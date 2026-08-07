package runtime

import (
	"context"
	"fmt"

	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/feature"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// wrapToolCaller applies the feature-contributed LLM middleware chain to the
// tool-calling gateway. A wrapper that panics or returns nil is skipped
// (logged) so one broken feature cannot take down the tool loop.
func (e *Engine) wrapToolCaller(caller llm.ToolCaller) llm.ToolCaller {
	for _, wrap := range e.hooks.llmToolCallerWrappers {
		wrapped, err := safecall.Invoke("runtime: feature llm middleware", func() (llm.ToolCaller, error) {
			return wrap(caller), nil
		})
		if err != nil {
			e.logWarn(context.Background(), "runtime: feature llm middleware failed; skipped", "error", err)
			continue
		}
		if wrapped == nil {
			e.logWarn(context.Background(), "runtime: feature llm middleware returned nil; skipped")
			continue
		}
		caller = wrapped
	}
	return caller
}

// runStepFinishHooks fires feature loop hooks after a completed tool-loop
// step. Hook panics are recovered and logged (safecall isolation), matching
// the feature collection error-isolation contract.
func (e *Engine) runStepFinishHooks(ctx context.Context, info feature.StepInfo) {
	for _, hooks := range e.hooks.loopHooks {
		if hooks.OnStepFinish == nil {
			continue
		}
		hook := hooks.OnStepFinish
		if err := safecall.Do("runtime: feature on_step_finish", func() error {
			hook(ctx, info)
			return nil
		}); err != nil {
			e.logWarn(ctx, "runtime: feature loop hook failed", "run_id", info.RunID, "agent", info.Agent, "step", info.Step, "error", err)
		}
	}
}

// featureStopError fails the run when a feature stop condition fires. The
// reason becomes the terminal payload's termination_reason (stage-1
// attribution) through terminationReasonForError.
type featureStopError struct {
	step   int
	reason string
}

func (e *featureStopError) Error() string {
	if e.reason == "" {
		return fmt.Sprintf("runtime: feature stop condition halted the tool loop at step %d", e.step)
	}
	return fmt.Sprintf("runtime: feature stop condition halted the tool loop at step %d: %s", e.step, e.reason)
}

// TerminationReason feeds terminationReasonForError.
func (e *featureStopError) TerminationReason() string {
	if e.reason == "" {
		return core.TerminationReasonError
	}
	return e.reason
}

// evaluateStopConditions runs feature stop conditions after a tool-executing
// step. A failing (panicking) condition is logged and skipped, like any other
// isolated feature contribution.
func (e *Engine) evaluateStopConditions(ctx context.Context, info feature.StepInfo) error {
	for _, condition := range e.hooks.stopConditions {
		if condition == nil {
			continue
		}
		outcome, err := safecall.Invoke("runtime: feature stop condition", func() (struct {
			reason string
			stop   bool
		}, error) {
			reason, stop := condition(ctx, info)
			return struct {
				reason string
				stop   bool
			}{reason: reason, stop: stop}, nil
		})
		if err != nil {
			e.logWarn(ctx, "runtime: feature stop condition failed; skipped", "run_id", info.RunID, "agent", info.Agent, "step", info.Step, "error", err)
			continue
		}
		if outcome.stop {
			return &featureStopError{step: info.Step, reason: outcome.reason}
		}
	}
	return nil
}
