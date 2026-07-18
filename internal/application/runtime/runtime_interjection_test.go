package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type blockingEchoTool struct {
	started chan struct{}
	release chan struct{}
}

func (t blockingEchoTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	close(t.started)
	select {
	case <-t.release:
	case <-ctx.Done():
		return core.ToolResult{}, ctx.Err()
	}
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

func TestInterjectDrainsBeforeNextLLMCall(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{"query":"a"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": blockingEchoTool{started: started, release: release}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg     sync.WaitGroup
		result RunResult
		runErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, runErr = engine.Run(context.Background(), RunRequest{RunID: "run-interject", Agent: "assistant", Prompt: "go"})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not start")
	}
	if err := engine.Interject("run-interject", "change course"); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if result.Output != "done" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if events.count(core.EventInterjectionDrained) < 1 {
		t.Fatalf("expected InterjectionDrained, got %+v", events.types())
	}
	// Second LLM request should include the interjection text.
	reqs := gateway.ToolRequests("default")
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 tool-call LLM rounds, got %d", len(reqs))
	}
	found := false
	for _, msg := range reqs[1].Messages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "change course") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("interjection not present in second LLM request: %+v", reqs[1].Messages)
	}
}
