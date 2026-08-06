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
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type memoryMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []llm.ToolCall    `json:"tool_calls,omitempty"`
	Tool       string            `json:"tool,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Time       time.Time         `json:"time"`
}

func withMemoryProvenance(msg memoryMessage, provenance string) memoryMessage {
	if provenance == "" {
		return msg
	}
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[memory.ProvenanceKey] = provenance
	return msg
}

func memoryMessageFromToolResult(call llm.ToolCall, result core.ToolResult) memoryMessage {
	msg := withMemoryProvenance(memoryMessage{
		Role:       string(llm.RoleTool),
		Content:    string(mustMarshal(result)),
		Tool:       call.Name,
		ToolCallID: call.ID,
	}, memory.ProvenanceToolLoop)
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata["tier"] = "tool_trace"
	msg.Metadata["tool_name"] = call.Name
	class := classifyToolResultMessage(llm.Message{Role: llm.RoleTool, Content: msg.Content, Name: call.Name, Metadata: msg.Metadata})
	msg.Metadata["tool_result_class"] = string(class)
	return msg
}

func runTurnMemoryMessage(role, content string) memoryMessage {
	msg := withMemoryProvenance(memoryMessage{Role: role, Content: content}, memory.ProvenanceRunTurn)
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata["tier"] = "conversation"
	return msg
}

func memoryMessageFromLLM(msg llm.Message) memoryMessage {
	return memoryMessageFromLLMWithProvenance(msg, memory.ProvenanceRunTurn)
}

func memoryMessageFromLLMWithProvenance(msg llm.Message, provenance string) memoryMessage {
	out := withMemoryProvenance(memoryMessage{
		Role:       string(msg.Role),
		Content:    msg.Content,
		ToolCalls:  append([]llm.ToolCall(nil), msg.ToolCalls...),
		Tool:       msg.Name,
		ToolCallID: msg.ToolCallID,
	}, provenance)
	if out.Metadata == nil {
		out.Metadata = map[string]string{}
	}
	if msg.Role == llm.RoleTool {
		out.Metadata["tier"] = "tool_trace"
		if msg.Name != "" {
			out.Metadata["tool_name"] = msg.Name
		}
	} else {
		out.Metadata["tier"] = "conversation"
	}
	return out
}

func lastAssistantWithToolCallsIndex(messages []llm.Message) int {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == llm.RoleAssistant && len(messages[index].ToolCalls) > 0 {
			return index
		}
	}
	return -1
}

func (e *Engine) persistToolTurnMemory(ctx context.Context, runID string, agent core.Agent, assistant llm.Message, tools []memoryMessage) error {
	if len(assistant.ToolCalls) == 0 && len(tools) == 0 {
		return nil
	}
	batch := make([]memoryMessage, 0, 1+len(tools))
	batch = append(batch, memoryMessageFromLLMWithProvenance(assistant, memory.ProvenanceToolLoop))
	batch = append(batch, tools...)
	return e.writeMemory(ctx, runID, agent, batch)
}

func (e *Engine) persistToolTurnFromStepOutputs(ctx context.Context, runID string, agent core.Agent, assistant llm.Message) error {
	if len(assistant.ToolCalls) == 0 {
		return nil
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return err
	}
	// Store the same compacted result the model sees (matching dispatchToolCalls),
	// keeping memory and LLM context consistent on resume. The full result
	// remains in StepOutputs. With ToolResultMaxTokens==0 (default) this is a no-op.
	profile, _ := e.llmProfile(agent.LLM)
	tools := make([]memoryMessage, 0, len(assistant.ToolCalls))
	for _, call := range assistant.ToolCalls {
		raw, ok, err := e.stepOutputBytes(ctx, snapshot, e.batchPersistKey(agent, call))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var result core.ToolResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("runtime: decode tool output %q: %w", call.ID, err)
		}
		compacted, _ := e.materializeToolResultForContext(call.Name, result, profile)
		tools = append(tools, memoryMessageFromToolResult(call, compacted))
	}
	if len(tools) == 0 {
		return nil
	}
	return e.persistToolTurnMemory(ctx, runID, agent, assistant, tools)
}

func (e *Engine) stepOutputBytes(ctx context.Context, snapshot runstate.RunSnapshot, key string) (json.RawMessage, bool, error) {
	if snapshot.StepOutputs == nil {
		return nil, false, nil
	}
	ref, ok := snapshot.StepOutputs[key]
	if !ok {
		return nil, false, nil
	}
	if len(ref.Inline) > 0 {
		return ref.Inline, true, nil
	}
	if ref.Blob == nil || e.blobs == nil {
		return nil, false, nil
	}
	raw, err := e.blobs.Get(ctx, *ref.Blob)
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (e *Engine) readMemory(ctx context.Context, runID string, agent core.Agent, query string) ([]llm.Message, error) {
	if err := e.authorizeMemory(ctx, runID, agent, security.ActionMemoryRead); err != nil {
		return nil, err
	}
	if manager, settings, ok := e.tierManager(agent); ok {
		return e.readTierMemory(ctx, runID, agent, manager, settings, query)
	}
	repo, ns, ok, err := e.memoryRepository(ctx, runID, agent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	raw, err := repo.Get(ctx, ns, "messages")
	if err != nil {
		if err == memory.ErrNotFound {
			e.emitJSON(ctx, core.EventMemoryRead, runID, memoryReadPayload(agent, nil, 0, 0, 0))
			return nil, nil
		}
		return nil, err
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("runtime: memory %q messages are invalid: %w", agent.Memory, err)
	}
	storedCount := len(stored)
	messages := make([]llm.Message, 0, len(stored))
	for _, msg := range stored {
		switch llm.Role(msg.Role) {
		case llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
			messages = append(messages, llm.Message{
				Role:       llm.Role(msg.Role),
				Content:    msg.Content,
				ToolCalls:  append([]llm.ToolCall(nil), msg.ToolCalls...),
				Name:       msg.Tool,
				ToolCallID: msg.ToolCallID,
				Metadata: map[string]string{
					"memory": agent.Memory,
				},
			})
		}
	}
	recallLimit := 0
	if profile, ok := e.scenario.LLMs[agent.LLM]; ok {
		recallLimit = profile.Context.Normalize().MemoryRecallLimit
		if recallLimit > 0 && len(messages) > recallLimit {
			messages = trimRecallMessages(messages, recallLimit)
		}
	}
	e.emitJSON(ctx, core.EventMemoryRead, runID, memoryReadPayload(agent, stored, storedCount, len(messages), recallLimit))
	return messages, nil
}

// trimRecallMessages keeps at most limit recent messages while preserving a
// valid assistant/tool_call pairing contract for the recalled slice.
func trimRecallMessages(messages []llm.Message, limit int) []llm.Message {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return enforceToolCallPairing(messages[len(messages)-limit:])
}

func (e *Engine) writeMemory(ctx context.Context, runID string, agent core.Agent, messages []memoryMessage) error {
	if err := e.authorizeMemory(ctx, runID, agent, security.ActionMemoryWrite); err != nil {
		return err
	}
	if manager, _, ok := e.tierManager(agent); ok {
		return e.writeTierMemory(ctx, runID, agent, manager, messages)
	}
	repo, ns, ok, err := e.memoryRepository(ctx, runID, agent)
	if err != nil {
		return err
	}
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
	e.emitJSON(ctx, core.EventMemoryWrite, runID, memoryWritePayload(agent, messages))
	if err := e.capStoredMemory(ctx, repo, ns, agent); err != nil {
		return err
	}
	return nil
}

// capStoredMemory enforces the optional write-side flat-memory cap
// (Context.MemoryStoreLimit). When the stored history exceeds the limit it is
// rewritten to the most recent limit messages. Zero (default) disables the cap
// so append behavior is unchanged.
func (e *Engine) capStoredMemory(ctx context.Context, repo memory.Repository, ns memory.Namespace, agent core.Agent) error {
	profile, ok := e.scenario.LLMs[agent.LLM]
	if !ok {
		return nil
	}
	limit := profile.Context.Normalize().MemoryStoreLimit
	if limit <= 0 {
		return nil
	}
	raw, err := repo.Get(ctx, ns, "messages")
	if err != nil {
		if err == memory.ErrNotFound {
			return nil
		}
		return err
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("runtime: memory %q messages are invalid: %w", agent.Memory, err)
	}
	if len(stored) <= limit {
		return nil
	}
	trimmed := trimStoredMessages(stored, limit)
	out, err := json.Marshal(trimmed)
	if err != nil {
		return err
	}
	return repo.Set(ctx, ns, "messages", out)
}

// trimStoredMessages keeps the most recent limit stored messages and drops any
// tool result at the trimmed boundary whose issuing assistant tool_call fell
// outside the retained window, matching the read-side pairing contract so the
// persisted history never starts with an orphaned tool result.
func trimStoredMessages(stored []memoryMessage, limit int) []memoryMessage {
	if limit <= 0 || len(stored) <= limit {
		return stored
	}
	window := stored[len(stored)-limit:]
	issued := make(map[string]struct{})
	for _, msg := range window {
		for _, call := range msg.ToolCalls {
			issued[call.ID] = struct{}{}
		}
	}
	out := make([]memoryMessage, 0, len(window))
	for _, msg := range window {
		if msg.Role == string(llm.RoleTool) && msg.ToolCallID != "" {
			if _, ok := issued[msg.ToolCallID]; !ok {
				continue
			}
		}
		out = append(out, msg)
	}
	return out
}

func memoryReadPayload(agent core.Agent, stored []memoryMessage, storedCount, recalledCount, recallLimit int) map[string]any {
	payload := map[string]any{
		"agent":                  agent.Name,
		"memory":                 agent.Memory,
		"stored_messages":        storedCount,
		"messages":               recalledCount,
		"messages_by_role":       summarizeMemoryRoles(stored),
		"messages_by_provenance": summarizeMemoryProvenance(stored),
	}
	if recallLimit > 0 && storedCount > recalledCount {
		payload["memory_recall_limit"] = recallLimit
	}
	return payload
}

func memoryWritePayload(agent core.Agent, messages []memoryMessage) map[string]any {
	payload := map[string]any{
		"agent":                  agent.Name,
		"memory":                 agent.Memory,
		"messages":               len(messages),
		"messages_by_provenance": summarizeMemoryProvenance(messages),
	}
	if len(messages) == 1 {
		msg := messages[0]
		payload["message_bytes"] = len(msg.Content)
		if msg.Tool != "" {
			payload["tool_name"] = msg.Tool
		}
		if msg.Metadata != nil {
			if tier := msg.Metadata["tier"]; tier != "" {
				payload["tier"] = tier
			}
			if transformed := msg.Metadata["transformed"]; transformed != "" {
				payload["transformed"] = transformed == "true"
			}
		}
	} else {
		totalBytes := 0
		tiers := map[string]int{}
		for _, msg := range messages {
			totalBytes += len(msg.Content)
			if msg.Metadata != nil {
				if tier := msg.Metadata["tier"]; tier != "" {
					tiers[tier]++
				}
			}
		}
		payload["message_bytes"] = totalBytes
		if len(tiers) > 0 {
			payload["tiers"] = tiers
		}
	}
	return payload
}

func summarizeMemoryRoles(messages []memoryMessage) map[string]int {
	counts := map[string]int{}
	for _, msg := range messages {
		counts[msg.Role]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func summarizeMemoryProvenance(messages []memoryMessage) map[string]int {
	counts := map[string]int{}
	for _, msg := range messages {
		provenance := memory.ProvenanceUntracked
		if msg.Metadata != nil {
			if value := strings.TrimSpace(msg.Metadata[memory.ProvenanceKey]); value != "" {
				provenance = value
			}
		}
		counts[provenance]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func (e *Engine) readTierMemory(ctx context.Context, runID string, agent core.Agent, manager tier.Manager, settings tier.Settings, query string) ([]llm.Message, error) {
	ctx, span := e.startSpan(ctx, observability.SpanMemoryTierRecall,
		observability.Attribute{Key: "memory", Value: agent.Memory},
		observability.Attribute{Key: "agent", Value: agent.Name},
	)
	defer span.End()
	start := time.Now()

	ns, ok, err := e.scopedMemoryNamespace(ctx, runID, agent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	records, err := manager.Recall(tier.WithMigrationRunID(ctx, runID), ns, query, e.tierRecallBudget(agent, settings))
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(records))
	stored := make([]memoryMessage, 0, len(records))
	for _, record := range records {
		msg, err := tierRecordToMessage(record)
		if err != nil {
			return nil, err
		}
		stored = append(stored, msg)
		switch llm.Role(msg.Role) {
		case llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
			messages = append(messages, llm.Message{
				Role:       llm.Role(msg.Role),
				Content:    msg.Content,
				ToolCalls:  append([]llm.ToolCall(nil), msg.ToolCalls...),
				Name:       msg.Tool,
				ToolCallID: msg.ToolCallID,
				Metadata: map[string]string{
					"memory": agent.Memory,
					"tier":   string(record.Tier),
				},
			})
		}
	}
	recallLimit := 0
	if profile, ok := e.scenario.LLMs[agent.LLM]; ok {
		recallLimit = profile.Context.Normalize().MemoryRecallLimit
		if recallLimit > 0 && len(messages) > recallLimit {
			messages = trimRecallMessages(messages, recallLimit)
		}
	}
	payload := memoryReadPayload(agent, stored, len(stored), len(messages), recallLimit)
	payload["tiered"] = true
	e.emitJSON(ctx, core.EventMemoryRead, runID, payload)
	e.recorder.ObserveHistogram(ctx, observability.MetricMemoryRecallLatencySeconds, time.Since(start).Seconds(),
		observability.Attribute{Key: "memory", Value: agent.Memory},
	)
	return messages, nil
}

func (e *Engine) writeTierMemory(ctx context.Context, runID string, agent core.Agent, manager tier.Manager, messages []memoryMessage) error {
	if len(messages) == 0 {
		return nil
	}
	ns, ok, err := e.scopedMemoryNamespace(ctx, runID, agent)
	if err != nil {
		return err
	}
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
	payload := memoryWritePayload(agent, messages)
	payload["tiered"] = true
	e.emitJSON(ctx, core.EventMemoryWrite, runID, payload)
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
	ns, ok, err := e.scopedMemoryNamespace(ctx, runID, agent)
	if err != nil {
		return err
	}
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

func (e *Engine) memoryNamespace(runID string, agent core.Agent) (memory.Namespace, bool, error) {
	if agent.Memory == "" {
		return memory.Namespace{}, false, nil
	}
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok {
		return memory.Namespace{}, false, nil
	}
	scope := memory.Scope(ref.Scope)
	ns := memory.Namespace{Agent: agent.Name, Scope: scope}
	switch scope {
	case memory.ScopeConversation, memory.ScopeAudit:
		ns.RunID = runID
	case memory.ScopeSession, memory.ScopeLongTerm:
		if strings.TrimSpace(ref.Namespace) == "" {
			return memory.Namespace{}, false, fmt.Errorf("runtime: memory %q scope %q requires an explicit namespace so session history is not shared across callers", agent.Memory, scope)
		}
		ns.SessionID = ref.Namespace + ":" + agent.Name
	default:
		ns.SessionID = firstNonEmpty(ref.Namespace, e.scenario.Name)
	}
	return ns, true, nil
}

func (e *Engine) memoryRepository(ctx context.Context, runID string, agent core.Agent) (memory.Repository, memory.Namespace, bool, error) {
	if agent.Memory == "" || e.memory == nil {
		return nil, memory.Namespace{}, false, nil
	}
	repo, ok := e.memory[agent.Memory]
	if !ok || repo == nil {
		return nil, memory.Namespace{}, false, nil
	}
	ns, ok, err := e.scopedMemoryNamespace(ctx, runID, agent)
	if err != nil || !ok {
		return nil, memory.Namespace{}, false, err
	}
	return repo, ns, true, nil
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
		return "", fmt.Errorf("runtime: decode redacted memory message: %w", err)
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
