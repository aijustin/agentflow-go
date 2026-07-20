package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// Interject queues a mid-turn user message for runID. Drain timing follows
// Engine.interjectDrain (Codex-style steer alignment).
func (e *Engine) Interject(runID, text string) error {
	if e == nil {
		return fmt.Errorf("runtime: engine is nil")
	}
	runID = strings.TrimSpace(runID)
	text = strings.TrimSpace(text)
	if runID == "" {
		return fmt.Errorf("runtime: interject requires run_id")
	}
	if text == "" {
		return fmt.Errorf("runtime: interject text is required")
	}
	if e.interjections == nil {
		e.interjections = interjection.NewBuffer()
	}
	e.interjections.Push(runID, text)
	return nil
}

// SetInterjectDrainPolicy overrides the drain policy (tests / late config).
// Safe for concurrent use with the tool loop.
func (e *Engine) SetInterjectDrainPolicy(policy interjection.DrainPolicy) {
	if e == nil {
		return
	}
	e.interjectDrain.Store(policy.Normalize())
}

func (e *Engine) drainInterjectionsIfAllowed(ctx context.Context, runID string, agent core.Agent, messages []llm.Message, phase interjection.DrainPhase, justCompacted bool) ([]llm.Message, error) {
	if e == nil {
		return messages, nil
	}
	policy := e.drainPolicy()
	if !policy.Allow(phase, justCompacted) {
		return messages, nil
	}
	return e.drainInterjectionsInto(ctx, runID, agent, messages)
}

// clearInterjections discards any buffered mid-turn messages for a terminal run.
func (e *Engine) clearInterjections(runID string) {
	if e == nil || e.interjections == nil {
		return
	}
	_ = e.interjections.Drain(runID)
}

func (e *Engine) drainInterjectionsInto(ctx context.Context, runID string, agent core.Agent, messages []llm.Message) ([]llm.Message, error) {
	if e == nil || e.interjections == nil {
		return messages, nil
	}
	pending := e.interjections.Drain(runID)
	if len(pending) == 0 {
		return messages, nil
	}
	for _, text := range pending {
		formatted := interjection.Format(text)
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: formatted,
			Metadata: map[string]string{
				"interjection": "true",
			},
		})
		if err := e.writeMemory(ctx, runID, agent, []memoryMessage{
			runTurnMemoryMessage(string(llm.RoleUser), formatted),
		}); err != nil {
			return messages, err
		}
	}
	e.emitJSON(ctx, core.EventInterjectionDrained, runID, map[string]any{
		"agent": agent.Name,
		"count": len(pending),
	})
	return messages, nil
}
