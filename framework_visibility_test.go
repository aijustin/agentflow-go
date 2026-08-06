package agentflow_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// captureGateway scripts tool-loop responses and records every request the
// runtime hands to the provider side.
type captureGateway struct {
	mu       sync.Mutex
	requests []llm.ToolCallRequest
	script   func(call int) llm.ToolCallResponse
}

func (g *captureGateway) Supports(string, llm.Capability) bool { return true }

func (g *captureGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *captureGateway) ChatWithTools(_ context.Context, _ string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	call := len(g.requests)
	g.mu.Unlock()
	return g.script(call), nil
}

func (g *captureGateway) request(call int) llm.ToolCallRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests[call-1]
}

type padEchoTool struct {
	name string
	pad  string
}

func (t padEchoTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	out, _ := json.Marshal(map[string]string{"text": t.pad})
	return core.ToolResult{Tool: t.name, Output: out}, nil
}

func dualVisibilityScenario(pinOff *bool) core.Scenario {
	return core.Scenario{
		Name: "dual-visibility",
		LLMs: map[string]core.LLMProfileRef{
			"default": {
				Provider: "mock",
				Model:    "test",
				Context: contextwindow.Policy{
					Strategy:        contextwindow.StrategySlidingWindow,
					MaxInputTokens:  40,
					PinUserMessages: pinOff,
				},
			},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo", "gated"}},
		},
		Tools: map[string]core.Tool{
			"echo": {
				Name:        "echo",
				Type:        "builtin.echo",
				Approval:    core.ApprovalNever,
				InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			},
			"gated": {
				Name:        "gated",
				Type:        "builtin.gated",
				Approval:    core.ApprovalAlways,
				InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
}

// TestFrameworkDualVisibilityMessagesProjection runs the autonomous tool loop
// with a tiny context window and asserts on the messages handed to the
// provider side: with the option on, trimmed history stays in the sequence
// carrying visibility=user marks (user-side projections keep the full
// transcript; llm.AgentVisibleMessages is the model projection); with the
// option off (default), trimming physically drops history as before.
func TestFrameworkDualVisibilityMessagesProjection(t *testing.T) {
	pinOff := false
	pad := strings.Repeat("x", 56) // ~19 estimated tokens; window keeps the newest tool pair
	tests := []struct {
		name           string
		dualVisibility bool
		wantMessages   int
		wantMarked     int
	}{
		{name: "enabled retains full sequence with marks", dualVisibility: true, wantMessages: 4, wantMarked: 1},
		{name: "default physically drops", dualVisibility: false, wantMessages: 3, wantMarked: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &captureGateway{
				script: func(call int) llm.ToolCallResponse {
					if call == 1 {
						return llm.ToolCallResponse{
							ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "checking echo " + pad}},
							ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{"text":"hi"}`)}},
						}
					}
					return llm.ToolCallResponse{
						ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
					}
				},
			}
			fw, err := agentflow.New(
				dualVisibilityScenario(&pinOff),
				agentflow.WithLLMGateway(gateway),
				agentflow.WithToolExecutor("echo", padEchoTool{name: "echo", pad: "ok"}),
				agentflow.WithToolExecutor("gated", padEchoTool{name: "gated", pad: "ok"}),
				agentflow.WithDualVisibilityMessages(tt.dualVisibility),
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := fw.Run(context.Background(), agentflow.RunRequest{
				RunID:  "run-visibility-" + tt.name,
				Agent:  "assistant",
				Prompt: "u " + pad,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != runstate.RunStatusCompleted || result.Output != "done" {
				t.Fatalf("unexpected result: %+v", result)
			}
			second := gateway.request(2)
			if len(second.Messages) != tt.wantMessages {
				t.Fatalf("provider-side request has %d messages, want %d: %+v", len(second.Messages), tt.wantMessages, second.Messages)
			}
			marked := 0
			for _, msg := range second.Messages {
				if !llm.IsAgentVisible(msg) {
					marked++
				}
			}
			if marked != tt.wantMarked {
				t.Fatalf("request has %d marked messages, want %d", marked, tt.wantMarked)
			}
			if tt.dualVisibility {
				if second.Messages[1].Role != llm.RoleUser || llm.IsAgentVisible(second.Messages[1]) {
					t.Fatalf("expected the trimmed user message to be marked: %+v", second.Messages[1])
				}
				if projection := llm.AgentVisibleMessages(second.Messages); len(projection) != tt.wantMessages-tt.wantMarked {
					t.Fatalf("model projection has %d messages: %+v", len(projection), projection)
				}
			}
		})
	}
}

// TestFrameworkDualVisibilityMessagesSurvivePauseResume: marks backfilled
// onto the run history are serialized with checkpoint_messages at a
// tool-approval pause and are still there after resume, so the projection
// survives process boundaries without any migration.
func TestFrameworkDualVisibilityMessagesSurvivePauseResume(t *testing.T) {
	pinOff := false
	pad := strings.Repeat("x", 56)
	gateway := &captureGateway{
		script: func(call int) llm.ToolCallResponse {
			switch call {
			case 1:
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "checking echo " + pad}},
					ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{"text":"hi"}`)}},
				}
			case 2:
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "need approval"}},
					ToolCalls:    []llm.ToolCall{{ID: "c2", Name: "gated", Input: json.RawMessage(`{"text":"go"}`)}},
				}
			default:
				return llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"}},
				}
			}
		},
	}
	fw, err := agentflow.New(
		dualVisibilityScenario(&pinOff),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", padEchoTool{name: "echo", pad: "ok"}),
		agentflow.WithToolExecutor("gated", padEchoTool{name: "gated", pad: "ok"}),
		agentflow.WithDualVisibilityMessages(true),
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := fw.Run(ctx, agentflow.RunRequest{
		RunID:  "run-visibility-hitl",
		Agent:  "assistant",
		Prompt: "u " + pad,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused run, got %+v", result)
	}
	snapshot, err := fw.RunStateRepository().Load(ctx, "run-visibility-hitl")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := string(snapshot.Variables["checkpoint_messages"])
	if !strings.Contains(checkpoint, `"visibility":"user"`) {
		t.Fatalf("checkpoint messages must retain the visibility mark, got %s", checkpoint)
	}
	continued, err := fw.ResumeAndContinue(ctx, result.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if continued.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after resume, got %+v", continued)
	}
	// The resumed request is built from the checkpointed history: the mark
	// made before the pause is still attached to the old user message.
	third := gateway.request(3)
	var markedUser bool
	for _, msg := range third.Messages {
		if msg.Role == llm.RoleUser && strings.HasPrefix(msg.Content, "u ") && !llm.IsAgentVisible(msg) {
			markedUser = true
		}
	}
	if !markedUser {
		t.Fatalf("resumed request must keep the checkpointed visibility mark: %+v", third.Messages)
	}
}
