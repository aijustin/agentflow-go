package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// pauseOnCompleteRepository simulates another writer pausing the run in the
// window between the resumed turn producing its answer and the completion
// save landing. The interception mirrors the real race: the concurrent write
// bumps the version, so the completion's compare-and-set fails, reloads, and
// discovers the run is no longer Running.
type pauseOnCompleteRepository struct {
	runstate.Repository
	once      sync.Once
	pauseVars map[string]json.RawMessage
}

func (r *pauseOnCompleteRepository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if snapshot.Status == runstate.RunStatusCompleted {
		r.once.Do(func() {
			current, err := r.Load(ctx, snapshot.RunID)
			if err != nil {
				return
			}
			current.Status = runstate.RunStatusPaused
			if current.Variables == nil {
				current.Variables = map[string]json.RawMessage{}
			}
			for key, value := range r.pauseVars {
				current.Variables[key] = value
			}
			_ = r.Repository.Save(ctx, &current, current.Version)
		})
	}
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

// When a resumed run loses the completion race to a concurrent pause, the
// checkpoint variables belong to the new pause. Clearing them anyway leaves
// that pause unresumable: its messages, pending tool calls and kind are gone.
func TestContinueToolApprovalKeepsCheckpointOfConcurrentPauseWinner(t *testing.T) {
	newPauseVars := map[string]json.RawMessage{
		checkpointKindVar:       json.RawMessage(`"tool_approval"`),
		checkpointMessagesVar:   json.RawMessage(`[{"role":"user","content":"second turn"}]`),
		checkpointToolCallsVar:  json.RawMessage(`[{"id":"call-2","name":"echo","input":{}}]`),
		checkpointAgentVar:      json.RawMessage(`"assistant"`),
		checkpointToolCountsVar: json.RawMessage(`{"by_name":{},"by_same_input":{}}`),
	}
	repo := &pauseOnCompleteRepository{Repository: runstateinmem.NewRepository(), pauseVars: newPauseVars}

	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})

	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:        "run-pause-race",
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			checkpointKindVar:       json.RawMessage(`"tool_approval"`),
			checkpointAgentVar:      json.RawMessage(`"assistant"`),
			checkpointMessagesVar:   json.RawMessage(`[{"role":"user","content":"first turn"}]`),
			checkpointToolCallsVar:  json.RawMessage(`[]`),
			checkpointToolCountsVar: json.RawMessage(`{"by_name":{},"by_same_input":{}}`),
		},
	}, 0); err != nil {
		t.Fatal(err)
	}

	result, err := engine.ContinueAfterCheckpoint(ctx, "run-pause-race")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected the concurrent pause to win, got status %s", result.Status)
	}

	snapshot, err := repo.Load(ctx, "run-pause-race")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("expected the run to stay Paused, got %s", snapshot.Status)
	}
	for key := range newPauseVars {
		if _, ok := snapshot.Variables[key]; !ok {
			t.Fatalf("checkpoint variable %q belonging to the new pause was cleared, leaving it unresumable", key)
		}
	}
}

// cancelOnCompleteRepository is the Cancelled counterpart of
// pauseOnCompleteRepository.
type cancelOnCompleteRepository struct {
	runstate.Repository
	once sync.Once
}

func (r *cancelOnCompleteRepository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if snapshot.Status == runstate.RunStatusCompleted {
		r.once.Do(func() {
			current, err := r.Load(ctx, snapshot.RunID)
			if err != nil {
				return
			}
			current.Status = runstate.RunStatusCancelled
			_ = r.Repository.Save(ctx, &current, current.Version)
		})
	}
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

// A Cancelled winner has no claim on the checkpoint, so the consumed
// checkpoint must still be dropped rather than left looking resumable.
func TestContinueToolApprovalClearsCheckpointWhenCancelWinsCompletion(t *testing.T) {
	repo := &cancelOnCompleteRepository{Repository: runstateinmem.NewRepository()}

	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})

	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:        "run-cancel-race",
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			checkpointKindVar:       json.RawMessage(`"tool_approval"`),
			checkpointAgentVar:      json.RawMessage(`"assistant"`),
			checkpointMessagesVar:   json.RawMessage(`[{"role":"user","content":"first turn"}]`),
			checkpointToolCallsVar:  json.RawMessage(`[]`),
			checkpointToolCountsVar: json.RawMessage(`{"by_name":{},"by_same_input":{}}`),
		},
	}, 0); err != nil {
		t.Fatal(err)
	}

	result, err := engine.ContinueAfterCheckpoint(ctx, "run-cancel-race")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected the concurrent cancel to win, got status %s", result.Status)
	}

	snapshot, err := repo.Load(ctx, "run-cancel-race")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{checkpointKindVar, checkpointMessagesVar, checkpointToolCallsVar, checkpointToolCountsVar} {
		if _, ok := snapshot.Variables[key]; ok {
			t.Fatalf("expected checkpoint variable %q to be cleared after a cancelled winner", key)
		}
	}
}
