package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

type preambleToolStreamGateway struct {
	turn int
}

func (*preambleToolStreamGateway) Supports(_ string, capability llm.Capability) bool {
	return capability == llm.CapChat || capability == llm.CapToolCall || capability == llm.CapStream
}

func (*preambleToolStreamGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("unexpected Chat call")
}

func (*preambleToolStreamGateway) ChatWithTools(
	context.Context,
	string,
	llm.ToolCallRequest,
) (llm.ToolCallResponse, error) {
	return llm.ToolCallResponse{}, errors.New("unexpected ChatWithTools call")
}

func (g *preambleToolStreamGateway) StreamChatWithTools(
	_ context.Context,
	_ string,
	_ llm.ToolCallRequest,
) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk, 4)
	switch g.turn {
	case 0:
		ch <- llm.ChatChunk{Content: "我先查询。"}
		ch <- llm.ChatChunk{
			Kind:       llm.ChunkKindToolCall,
			ToolCallID: "call-1",
			ToolName:   "echo",
			ToolInput:  json.RawMessage(`{"query":"hi"}`),
		}
		ch <- llm.ChatChunk{Done: true}
	case 1:
		ch <- llm.ChatChunk{Content: "最终"}
		ch <- llm.ChatChunk{Content: "答案"}
		ch <- llm.ChatChunk{Done: true}
	default:
		close(ch)
		return nil, errors.New("unexpected stream turn")
	}
	g.turn++
	close(ch)
	return ch, nil
}

func TestEngineStreamDiscardsToolTurnPreamble(t *testing.T) {
	gateway := &preambleToolStreamGateway{}
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": okEchoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(
		context.Background(),
		RunRequest{RunID: "run-stream-preamble", Agent: "assistant", Prompt: "go"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var answer strings.Builder
	var contentFrames int
	for chunk := range ch {
		if chunk.Error != "" {
			t.Fatalf("stream error: %s", chunk.Error)
		}
		if chunk.IsAnswerContent() && chunk.Content != "" {
			contentFrames++
			answer.WriteString(chunk.Content)
		}
	}
	// Live stream may include tool-turn preamble; authoritative final must not.
	if got := answer.String(); !strings.Contains(got, "最终答案") {
		t.Fatalf("expected terminal answer in stream, got %q", got)
	}
	if contentFrames < 2 {
		t.Fatalf("expected incremental content frames, got %d (%q)", contentFrames, answer.String())
	}
	loaded, err := repo.Load(context.Background(), "run-stream-preamble")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(loaded.StepOutputs["final"].Inline); got != `{"text":"最终答案"}` {
		t.Fatalf("authoritative final must exclude tool-turn preamble, got %q", got)
	}
}
