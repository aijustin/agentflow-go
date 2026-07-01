package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestEngineStreamReportsCompleteStreamRunError(t *testing.T) {
	repo := &failingSaveRepository{Repository: runstateinmem.NewRepository(), failRunID: "run-stream-fail"}
	gateway := &streamOnceGateway{content: "hello"}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs: repo,
		LLM:  gateway,
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(context.Background(), RunRequest{RunID: "run-stream-fail", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var terminal llm.ChatChunk
	for chunk := range ch {
		if chunk.Done || chunk.Error != "" {
			terminal = chunk
		}
	}
	if terminal.Error == "" {
		t.Fatal("expected terminal error chunk after completeStreamRun failure")
	}
	if !strings.Contains(terminal.Error, "save failed") {
		t.Fatalf("unexpected terminal error: %q", terminal.Error)
	}
}

type streamOnceGateway struct {
	content string
}

func (g *streamOnceGateway) Supports(_ string, cap llm.Capability) bool {
	return cap == llm.CapStream || cap == llm.CapChat
}

func (g *streamOnceGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Content: g.content}}, nil
}

func (g *streamOnceGateway) StreamChat(context.Context, string, llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Content: g.content, Done: true}
	close(ch)
	return ch, nil
}

// truncatedStreamGateway emits partial content and then closes the stream
// without ever sending a Done chunk, simulating a provider connection that
// was cut off mid-answer.
type truncatedStreamGateway struct{}

func (truncatedStreamGateway) Supports(_ string, cap llm.Capability) bool {
	return cap == llm.CapStream || cap == llm.CapChat
}

func (truncatedStreamGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Content: "unused"}}, nil
}

func (truncatedStreamGateway) StreamChat(context.Context, string, llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Content: "partial answer"}
	close(ch)
	return ch, nil
}

func TestEngineStreamTruncatedSourceFailsRun(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: truncatedStreamGateway{}})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(context.Background(), RunRequest{RunID: "run-stream-truncated", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var terminal llm.ChatChunk
	for chunk := range ch {
		if chunk.Done {
			terminal = chunk
		}
	}
	if terminal.Error == "" || !strings.Contains(terminal.Error, "without a done chunk") {
		t.Fatalf("expected truncated-stream terminal error, got %+v", terminal)
	}
	snapshot, err := repo.Load(context.Background(), "run-stream-truncated")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("truncated stream must fail the run, got %s", snapshot.Status)
	}
}

func TestEngineResolveAgentRequiresExplicitNameWithMultipleAgents(t *testing.T) {
	scenario := baseScenario(false)
	scenario.Agents["reviewer"] = scenario.Agents["assistant"]
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "hi"}})
	engine, err := NewEngine(scenario, Dependencies{Runs: runstateinmem.NewRepository(), LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-ambiguous-agent", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "multiple agents configured") {
		t.Fatalf("expected explicit-agent error for multi-agent scenario, got %v", err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-explicit-agent", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("explicit agent must still run, got %+v", result)
	}
}

type failingSaveRepository struct {
	runstate.Repository
	failRunID string
}

func (r *failingSaveRepository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if snapshot.RunID == r.failRunID && snapshot.Status == runstate.RunStatusCompleted {
		return errors.New("save failed")
	}
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

type staleOnceRepository struct {
	runstate.Repository
	triggered bool
}

func (r *staleOnceRepository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if !r.triggered && snapshot.Status == runstate.RunStatusCompleted {
		r.triggered = true
		return runstate.ErrStaleSnapshot
	}
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

func TestEngineRunRetriesCompletionSaveOnStaleSnapshot(t *testing.T) {
	repo := &staleOnceRepository{Repository: runstateinmem.NewRepository()}
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "final"}})
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-stale-complete", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatalf("expected completion save to survive one stale snapshot, got %v", err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "final" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !repo.triggered {
		t.Fatal("expected the stale snapshot to have been injected")
	}
	snapshot, err := repo.Load(context.Background(), "run-stale-complete")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected persisted status completed, got %s", snapshot.Status)
	}
}

func TestEngineRunMarksFailedWhenCompletionSaveFails(t *testing.T) {
	repo := &failingSaveRepository{Repository: runstateinmem.NewRepository(), failRunID: "run-complete-fail"}
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "final"}})
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-complete-fail", Agent: "assistant", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "save failed") {
		t.Fatalf("expected completion save error, got %v", err)
	}
	snapshot, err := repo.Load(context.Background(), "run-complete-fail")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("run must not linger in %s after completion save failure; want failed", snapshot.Status)
	}
	if got := variableString(snapshot.Variables, runErrorMessageVar); !strings.Contains(got, "save failed") {
		t.Fatalf("expected persisted failure reason, got %q", got)
	}
}

func TestEngineBeginRunStoresResumeMetadata(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "hello"}})
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{
		RunID:  "run-resume-meta",
		Agent:  "assistant",
		Prompt: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(context.Background(), "run-resume-meta")
	if err != nil {
		t.Fatal(err)
	}
	if got := variableString(snapshot.Variables, resumePromptVar); got != "hello" {
		t.Fatalf("expected resume_prompt, got %q", got)
	}
	if got := variableString(snapshot.Variables, resumeAgentVar); got != "assistant" {
		t.Fatalf("expected resume_agent, got %q", got)
	}
}
