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
