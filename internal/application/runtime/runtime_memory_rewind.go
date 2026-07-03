package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// conversationMemoryWatermarksVar stores, per workflow node (by storage id),
// the run-scoped conversation memory length recorded just before the node's
// agent ran. Workflow time-travel (ResumeFromStep / ResumeFromCheckpoint) uses
// it to rewind conversation memory in step with the rewound step outputs so a
// re-run does not see turns from the discarded (future) portion of the run.
//
// The orchestration package references the same literal key; the two must
// stay in sync (mirroring the existing checkpoint_kind duplication).
const conversationMemoryWatermarksVar = "conversation_memory_watermarks"

type conversationWatermark struct {
	Agent string `json:"agent"`
	Len   int    `json:"len"`
}

// conversationMemoryLen returns the number of stored messages for a run-scoped
// (conversation) flat memory and whether that memory is rewindable. Session
// and long-term memory are cross-run and must never be rewound; tier-backed
// memory is not rewound here (a known limitation) since it has no flat message
// array to truncate.
func (e *Engine) conversationMemoryLen(ctx context.Context, runID string, agent core.Agent) (int, bool) {
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok || memory.Scope(ref.Scope) != memory.ScopeConversation {
		return 0, false
	}
	if _, _, ok := e.tierManager(agent); ok {
		return 0, false
	}
	repo, ns, ok := e.memoryRepository(runID, agent)
	if !ok {
		return 0, false
	}
	raw, err := repo.Get(ctx, ns, "messages")
	if err != nil {
		return 0, false
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return 0, false
	}
	return len(stored), true
}

// recordConversationWatermark stamps the current run-scoped conversation memory
// length for the workflow node in context (if any) so a later rewind can
// truncate memory back to this point. It is a no-op outside a workflow node or
// when the agent's memory is not run-scoped conversation memory.
func (e *Engine) recordConversationWatermark(ctx context.Context, runID string, agent core.Agent) error {
	nodeID := core.WorkflowNodeFromContext(ctx)
	if nodeID == "" {
		return nil
	}
	length, ok := e.conversationMemoryLen(ctx, runID, agent)
	if !ok {
		return nil
	}
	return e.saveSnapshotWithRetry(ctx, runID, func(loaded *runstate.RunSnapshot) error {
		if loaded.Variables == nil {
			loaded.Variables = make(map[string]json.RawMessage)
		}
		marks := map[string]conversationWatermark{}
		if raw := loaded.Variables[conversationMemoryWatermarksVar]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &marks)
		}
		marks[nodeID] = conversationWatermark{Agent: agent.Name, Len: length}
		out, err := json.Marshal(marks)
		if err != nil {
			return err
		}
		loaded.Variables[conversationMemoryWatermarksVar] = out
		return nil
	})
}

// RewindConversationMemory truncates a run-scoped conversation memory to the
// first keep messages, dropping any later turns. It is used by workflow
// time-travel to align conversation memory with rewound step outputs. It is a
// no-op for non-conversation or tier-backed memory.
func (e *Engine) RewindConversationMemory(ctx context.Context, runID, agentName string, keep int) error {
	if keep < 0 {
		keep = 0
	}
	agent, err := e.resolveAgent(agentName)
	if err != nil {
		return nil
	}
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok || memory.Scope(ref.Scope) != memory.ScopeConversation {
		return nil
	}
	if _, _, ok := e.tierManager(agent); ok {
		return nil
	}
	repo, ns, ok := e.memoryRepository(runID, agent)
	if !ok {
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
	if len(stored) <= keep {
		return nil
	}
	out, err := json.Marshal(stored[:keep])
	if err != nil {
		return err
	}
	return repo.Set(ctx, ns, "messages", out)
}
