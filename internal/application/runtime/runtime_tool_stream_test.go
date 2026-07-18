package runtime

import (
	"context"
	"encoding/json"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type progressEchoTool struct{}

func (progressEchoTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

func (progressEchoTool) ExecuteStream(ctx context.Context, call core.ToolCall) (<-chan core.ToolStreamEvent, error) {
	ch := make(chan core.ToolStreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- core.ToolStreamEvent{Progress: json.RawMessage(`{"stage":"start"}`)}
		ch <- core.ToolStreamEvent{Progress: json.RawMessage(`{"stage":"mid"}`)}
		result := core.ToolResult{Tool: call.Tool, Output: call.Input}
		ch <- core.ToolStreamEvent{Terminal: true, Result: &result}
	}()
	return ch, nil
}

func TestToolStreamerEmitsProgressChunks(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStream)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"hi"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": progressEchoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(context.Background(), RunRequest{RunID: "run-stream-tool", Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var progress int
	var gotResult bool
	for chunk := range ch {
		if chunk.Kind == llm.ChunkKindToolProgress {
			progress++
		}
		if chunk.Kind == llm.ChunkKindToolResult {
			gotResult = true
		}
		if chunk.Error != "" {
			t.Fatalf("stream error: %s", chunk.Error)
		}
	}
	if progress < 2 {
		t.Fatalf("expected tool progress chunks, got %d", progress)
	}
	if !gotResult {
		t.Fatal("expected tool_result chunk")
	}
}
