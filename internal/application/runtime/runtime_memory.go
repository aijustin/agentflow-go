package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type memoryMessage struct {
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Time       time.Time `json:"time"`
}

func (e *Engine) readMemory(ctx context.Context, runID string, agent core.Agent) ([]llm.Message, error) {
	repo, ns, ok := e.memoryRepository(runID, agent)
	if !ok {
		return nil, nil
	}
	raw, err := repo.Get(ctx, ns, "messages")
	if err != nil {
		if err == memory.ErrNotFound {
			e.emitJSON(ctx, core.EventMemoryRead, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": 0})
			return nil, nil
		}
		return nil, err
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("runtime: memory %q messages are invalid: %w", agent.Memory, err)
	}
	messages := make([]llm.Message, 0, len(stored))
	for _, msg := range stored {
		switch llm.Role(msg.Role) {
		case llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
			messages = append(messages, llm.Message{
				Role:       llm.Role(msg.Role),
				Content:    msg.Content,
				Name:       msg.Tool,
				ToolCallID: msg.ToolCallID,
				Metadata: map[string]string{
					"memory": agent.Memory,
				},
			})
		}
	}
	if profile, ok := e.scenario.LLMs[agent.LLM]; ok {
		limit := profile.Context.Normalize().MemoryRecallLimit
		if limit > 0 && len(messages) > limit {
			messages = messages[len(messages)-limit:]
		}
	}
	e.emitJSON(ctx, core.EventMemoryRead, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages)})
	return messages, nil
}

func (e *Engine) writeMemory(ctx context.Context, runID string, agent core.Agent, messages []memoryMessage) error {
	repo, ns, ok := e.memoryRepository(runID, agent)
	if !ok || len(messages) == 0 {
		return nil
	}
	for _, msg := range messages {
		msg.Time = time.Now().UTC()
		content, err := e.redactMemoryMessage(ctx, runID, msg)
		if err != nil {
			return err
		}
		msg.Content = content
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if err := repo.Append(ctx, ns, "messages", raw); err != nil {
			return err
		}
	}
	e.emitJSON(ctx, core.EventMemoryWrite, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages)})
	return nil
}

func (e *Engine) memoryRepository(runID string, agent core.Agent) (memory.Repository, memory.Namespace, bool) {
	if agent.Memory == "" || e.memory == nil {
		return nil, memory.Namespace{}, false
	}
	repo, ok := e.memory[agent.Memory]
	if !ok || repo == nil {
		return nil, memory.Namespace{}, false
	}
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok {
		return nil, memory.Namespace{}, false
	}
	scope := memory.Scope(ref.Scope)
	ns := memory.Namespace{Agent: agent.Name, Scope: scope}
	switch scope {
	case memory.ScopeConversation, memory.ScopeAudit:
		ns.RunID = runID
	default:
		ns.SessionID = firstNonEmpty(ref.Namespace, e.scenario.Name)
	}
	return repo, ns, true
}

func (e *Engine) redactMemoryMessage(ctx context.Context, runID string, msg memoryMessage) (string, error) {
	if e.redactor == nil {
		return msg.Content, nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return msg.Content, err
	}
	redacted, err := e.redactor.RedactOutput(ctx, governance.OutputRedaction{
		RunID:  runID,
		StepID: "memory",
		Kind:   "memory." + msg.Role,
		Data:   raw,
	})
	if err != nil {
		return "", err
	}
	var out memoryMessage
	if err := json.Unmarshal(redacted, &out); err != nil {
		return msg.Content, nil
	}
	return out.Content, nil
}
