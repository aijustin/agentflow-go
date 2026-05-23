package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type memoryMessage struct {
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Time       time.Time `json:"time"`
}

func (e *Engine) readMemory(ctx context.Context, runID string, agent core.Agent, query string) ([]llm.Message, error) {
	if err := e.authorizeMemory(ctx, runID, agent, security.ActionMemoryRead); err != nil {
		return nil, err
	}
	if manager, settings, ok := e.tierManager(agent); ok {
		return e.readTierMemory(ctx, runID, agent, manager, settings, query)
	}
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
	if err := e.authorizeMemory(ctx, runID, agent, security.ActionMemoryWrite); err != nil {
		return err
	}
	if manager, _, ok := e.tierManager(agent); ok {
		return e.writeTierMemory(ctx, runID, agent, manager, messages)
	}
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
		if err := e.rememberCognitive(ctx, runID, agent, msg); err != nil {
			return err
		}
	}
	e.emitJSON(ctx, core.EventMemoryWrite, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages)})
	return nil
}

func (e *Engine) readTierMemory(ctx context.Context, runID string, agent core.Agent, manager tier.Manager, settings tier.Settings, query string) ([]llm.Message, error) {
	ctx, span := e.startSpan(ctx, observability.SpanMemoryTierRecall,
		observability.Attribute{Key: "memory", Value: agent.Memory},
		observability.Attribute{Key: "agent", Value: agent.Name},
	)
	defer span.End()
	start := time.Now()

	ns, ok := e.scopedMemoryNamespace(ctx, runID, agent)
	if !ok {
		return nil, nil
	}
	records, err := manager.Recall(tier.WithMigrationRunID(ctx, runID), ns, query, e.tierRecallBudget(agent, settings))
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(records))
	for _, record := range records {
		msg, err := tierRecordToMessage(record)
		if err != nil {
			return nil, err
		}
		switch llm.Role(msg.Role) {
		case llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
			messages = append(messages, llm.Message{
				Role:       llm.Role(msg.Role),
				Content:    msg.Content,
				Name:       msg.Tool,
				ToolCallID: msg.ToolCallID,
				Metadata: map[string]string{
					"memory": agent.Memory,
					"tier":   string(record.Tier),
				},
			})
		}
	}
	e.emitJSON(ctx, core.EventMemoryRead, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages), "tiered": true})
	e.recorder.ObserveHistogram(ctx, observability.MetricMemoryRecallLatencySeconds, time.Since(start).Seconds(),
		observability.Attribute{Key: "memory", Value: agent.Memory},
	)
	return messages, nil
}

func (e *Engine) writeTierMemory(ctx context.Context, runID string, agent core.Agent, manager tier.Manager, messages []memoryMessage) error {
	if len(messages) == 0 {
		return nil
	}
	ns, ok := e.scopedMemoryNamespace(ctx, runID, agent)
	if !ok {
		return nil
	}
	for _, msg := range messages {
		msg.Time = time.Now().UTC()
		content, err := e.redactMemoryMessage(ctx, runID, msg)
		if err != nil {
			return err
		}
		msg.Content = content
		record, err := messageToTierRecord(msg, ns)
		if err != nil {
			return err
		}
		if err := manager.Remember(tier.WithMigrationRunID(ctx, runID), ns, record); err != nil {
			return err
		}
	}
	e.emitJSON(ctx, core.EventMemoryWrite, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages), "tiered": true})
	e.enqueueTierReconcile(ctx, runID, agent)
	return nil
}

func messageToTierRecord(msg memoryMessage, ns memory.Namespace) (tier.Record, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return tier.Record{}, err
	}
	id, err := newTierRecordID()
	if err != nil {
		return tier.Record{}, err
	}
	return tier.Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID:         id,
			Content:    string(raw),
			Scope:      string(ns.Scope),
			Categories: []string{msg.Role},
			Importance: memory.ImportanceForRole(msg.Role),
			CreatedAt:  msg.Time,
			Metadata: map[string]string{
				"role":       msg.Role,
				"kind":       "message",
				"searchable": msg.Content,
			},
		},
		Tier:         tier.LevelHot,
		LastAccessAt: msg.Time,
	}, nil
}

func (e *Engine) rememberCognitive(ctx context.Context, runID string, agent core.Agent, msg memoryMessage) error {
	repo, ok := e.cognitive[agent.Memory]
	if !ok || repo == nil || strings.TrimSpace(msg.Content) == "" {
		return nil
	}
	ns, ok := e.scopedMemoryNamespace(ctx, runID, agent)
	if !ok {
		return nil
	}
	id, err := newTierRecordID()
	if err != nil {
		return err
	}
	return repo.Remember(ctx, ns, memory.CognitiveRecord{
		ID:         id,
		Content:    msg.Content,
		Scope:      string(ns.Scope),
		Categories: []string{msg.Role},
		Importance: memory.ImportanceForRole(msg.Role),
		CreatedAt:  msg.Time,
		Metadata:   map[string]string{"role": msg.Role, "kind": "message"},
	})
}

func tierRecordToMessage(record tier.Record) (memoryMessage, error) {
	var msg memoryMessage
	if err := json.Unmarshal([]byte(record.Content), &msg); err != nil {
		return memoryMessage{}, fmt.Errorf("runtime: tier record %q is invalid: %w", record.ID, err)
	}
	return msg, nil
}

func newTierRecordID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("runtime: generate tier record id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func (e *Engine) memoryNamespace(runID string, agent core.Agent) (memory.Namespace, bool) {
	if agent.Memory == "" {
		return memory.Namespace{}, false
	}
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok {
		return memory.Namespace{}, false
	}
	scope := memory.Scope(ref.Scope)
	ns := memory.Namespace{Agent: agent.Name, Scope: scope}
	switch scope {
	case memory.ScopeConversation, memory.ScopeAudit:
		ns.RunID = runID
	case memory.ScopeSession:
		sessionID := firstNonEmpty(ref.Namespace, e.scenario.Name)
		ns.SessionID = sessionID + ":" + agent.Name
	default:
		ns.SessionID = firstNonEmpty(ref.Namespace, e.scenario.Name)
	}
	return ns, true
}

func (e *Engine) memoryRepository(runID string, agent core.Agent) (memory.Repository, memory.Namespace, bool) {
	if agent.Memory == "" || e.memory == nil {
		return nil, memory.Namespace{}, false
	}
	repo, ok := e.memory[agent.Memory]
	if !ok || repo == nil {
		return nil, memory.Namespace{}, false
	}
	ns, ok := e.memoryNamespace(runID, agent)
	if !ok {
		return nil, memory.Namespace{}, false
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

func (e *Engine) authorizeMemory(ctx context.Context, runID string, agent core.Agent, action security.Action) error {
	if e.policy == nil || agent.Memory == "" {
		return nil
	}
	principal, err := identity.RequirePrincipal(ctx)
	if err != nil {
		resource := security.Resource{Type: "memory", ID: agent.Memory, Metadata: map[string]string{"agent": agent.Name}}
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: identity.Principal{}, Action: action, Resource: resource, RunID: runID, Outcome: "denied", Reason: security.ErrUnauthenticated.Error()})
		return security.ErrUnauthenticated
	}
	resource := security.Resource{
		Type:     "memory",
		ID:       agent.Memory,
		TenantID: principal.Scope.TenantID,
		Metadata: map[string]string{"agent": agent.Name},
	}
	if err := e.policy.Authorize(ctx, principal, action, resource); err != nil {
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principal, Action: action, Resource: resource, RunID: runID, Outcome: "denied", Reason: err.Error()})
		return err
	}
	return nil
}
