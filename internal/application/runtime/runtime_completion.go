package runtime

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func completionRequirementSatisfied(req *core.CompletionRequirement, tracker *toolCallTracker) bool {
	if req == nil || strings.TrimSpace(req.Tool) == "" {
		return true
	}
	return tracker.ensure().nameCount(strings.TrimSpace(req.Tool)) > 0
}

func completionMaxRetries(req *core.CompletionRequirement) int {
	if req == nil {
		return 0
	}
	if req.Recovery == nil {
		// One reminder retry when recovery is omitted: enforce the contract
		// without an unbounded loop.
		return 1
	}
	if req.Recovery.MaxRetries <= 0 {
		return 0
	}
	return req.Recovery.MaxRetries
}

func completionBackoff(req *core.CompletionRequirement, attempt int) time.Duration {
	if req == nil || req.Recovery == nil {
		return 0
	}
	base := req.Recovery.BaseDelayMS
	maxDelay := req.Recovery.MaxDelayMS
	if base <= 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := float64(base) * math.Pow(2, float64(attempt-1))
	if maxDelay > 0 && delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}
	return time.Duration(delay) * time.Millisecond
}

func completionReminderMessage(req *core.CompletionRequirement) llm.Message {
	reminder := strings.TrimSpace(req.Reminder)
	if reminder == "" {
		reminder = fmt.Sprintf("You stopped without calling `%s`. Continue and call it when done.", req.Tool)
	}
	return llm.Message{
		Role:    llm.RoleUser,
		Content: reminder,
		Metadata: map[string]string{
			"completion_requirement": "reminder",
			"required_tool":          strings.TrimSpace(req.Tool),
		},
	}
}

// enforceCompletionRequirement runs when the model returns a final answer with
// no tool calls. If the required tool has not succeeded, it injects a reminder
// (with optional backoff) and reports whether the loop should continue.
func (e *Engine) enforceCompletionRequirement(
	ctx context.Context,
	runID string,
	agent core.Agent,
	messages []llm.Message,
	tracker *toolCallTracker,
	recoveryAttempts *int,
) (continuedMessages []llm.Message, cont bool, err error) {
	req := agent.CompletionRequirement
	if req == nil || strings.TrimSpace(req.Tool) == "" {
		return messages, false, nil
	}
	if completionRequirementSatisfied(req, tracker) {
		return messages, false, nil
	}
	maxRetries := completionMaxRetries(req)
	*recoveryAttempts++
	attempt := *recoveryAttempts
	if attempt > maxRetries {
		e.emitJSON(ctx, core.EventCompletionRequirementFail, runID, map[string]any{
			"agent":    agent.Name,
			"tool":     strings.TrimSpace(req.Tool),
			"attempts": attempt,
		})
		return messages, false, fmt.Errorf(
			"runtime: completion requirement not satisfied: tool %q was not called after %d recovery attempt(s)",
			strings.TrimSpace(req.Tool),
			attempt-1,
		)
	}
	delay := completionBackoff(req, attempt)
	e.emitJSON(ctx, core.EventCompletionRecovery, runID, map[string]any{
		"agent":       agent.Name,
		"tool":        strings.TrimSpace(req.Tool),
		"attempt":     attempt,
		"max_retries": maxRetries,
		"delay_ms":    delay.Milliseconds(),
	})
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return messages, false, ctx.Err()
		case <-timer.C:
		}
	}
	messages = append(messages, completionReminderMessage(req))
	return messages, true, nil
}
