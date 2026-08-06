package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolinspect"
)

// toolDeniedPayloads decodes every ToolDenied event payload.
func toolDeniedPayloads(events *captureEvents) []map[string]any {
	var out []map[string]any
	for _, event := range events.events {
		if event.Type != core.EventToolDenied {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		out = append(out, payload)
	}
	return out
}

func newInspectorEngine(t *testing.T, gateway llm.Gateway, events core.EventSink, deps ...func(*Dependencies)) *Engine {
	t.Helper()
	options := Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": execOK{}},
		Events: events,
	}
	for _, apply := range deps {
		apply(&options)
	}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), options)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

type execOK struct{}

func (execOK) Execute(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: call.Tool, Output: json.RawMessage(`{"ok":true}`)}, nil
}

func queueEchoThenAnswer(gateway *llmmock.Gateway) {
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
}

// A prepended host inspector runs before the built-in gates and its denial
// carries the host kind through the ToolDenied event; the tool never executes.
func TestEngineToolInspectorPrependDeny(t *testing.T) {
	gateway := llmmock.NewGateway()
	queueEchoThenAnswer(gateway)
	events := &captureEvents{}
	inspector := toolinspect.InspectorFunc{
		InspectorName: "host_policy",
		Fn: func(_ context.Context, req *toolinspect.Request) (toolinspect.Finding, error) {
			if req.Call.Name == "echo" {
				return toolinspect.Deny("host_policy", "denied by host policy"), nil
			}
			return toolinspect.AllowFinding, nil
		},
	}
	engine := newInspectorEngine(t, gateway, events, func(d *Dependencies) {
		d.ToolInspectorPrepend = []toolinspect.Inspector{inspector}
	})
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-insp", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	denials := toolDeniedPayloads(events)
	if len(denials) != 1 {
		t.Fatalf("expected one ToolDenied event, got %v", events.types())
	}
	if denials[0]["kind"] != "host_policy" || denials[0]["reason"] != "denied by host policy" {
		t.Fatalf("unexpected denial payload: %v", denials[0])
	}
	if events.count(core.EventToolReturned) != 0 {
		t.Fatal("denied tool must not execute")
	}
}

// An appended host inspector runs after every built-in gate: a call the
// built-in schema gate rejects never reaches it, while an allowed call does.
func TestEngineToolInspectorAppendRunsAfterBuiltinGates(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	// echo's input schema is {"type":"object"}; the array input fails the
	// built-in schema gate before any appended inspector runs.
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`["x"]`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-2", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	events := &captureEvents{}
	seen := 0
	inspector := toolinspect.InspectorFunc{
		InspectorName: "audit_trail",
		Fn: func(_ context.Context, _ *toolinspect.Request) (toolinspect.Finding, error) {
			seen++
			return toolinspect.AllowFinding, nil
		},
	}
	engine := newInspectorEngine(t, gateway, events, func(d *Dependencies) {
		d.ToolInspectorAppend = []toolinspect.Inspector{inspector}
	})
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-insp-append", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("appended inspector ran %d times, want exactly 1 (the schema-valid call)", seen)
	}
	denials := toolDeniedPayloads(events)
	if len(denials) != 1 || !strings.HasPrefix(denials[0]["reason"].(string), "invalid tool input") {
		t.Fatalf("unexpected denial payloads: %v", denials)
	}
	if _, hasKind := denials[0]["kind"]; hasKind {
		t.Fatalf("schema denial must not carry a kind: %v", denials[0])
	}
}

// A RequireApproval verdict inside the dispatch chain settles as an
// approval-accounted soft denial (kind "approval").
func TestEngineToolInspectorRequireApprovalSoftDenies(t *testing.T) {
	gateway := llmmock.NewGateway()
	queueEchoThenAnswer(gateway)
	events := &captureEvents{}
	inspector := toolinspect.InspectorFunc{
		InspectorName: "approval_requester",
		Fn: func(_ context.Context, _ *toolinspect.Request) (toolinspect.Finding, error) {
			return toolinspect.RequireApproval(""), nil
		},
	}
	engine := newInspectorEngine(t, gateway, events, func(d *Dependencies) {
		d.ToolInspectorPrepend = []toolinspect.Inspector{inspector}
	})
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-insp-approval", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	denials := toolDeniedPayloads(events)
	if len(denials) != 1 {
		t.Fatalf("expected one ToolDenied event, got %v", events.types())
	}
	if denials[0]["kind"] != "approval" || denials[0]["reason"] != "tool requires approval" {
		t.Fatalf("unexpected denial payload: %v", denials[0])
	}
}

// An appended inspector's denial lands after the budget gate reserved the
// call; the settled attempt must still count toward the doom-loop budget,
// exactly like a governance denial.
func TestEngineToolInspectorDenyAfterReservationCountsAttempt(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	for range 2 {
		gateway.QueueToolCall("default", llm.ToolCallResponse{
			ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
		})
	}
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	events := &captureEvents{}
	inspector := toolinspect.InspectorFunc{
		InspectorName: "flaky_policy",
		Fn: func(_ context.Context, _ *toolinspect.Request) (toolinspect.Finding, error) {
			return toolinspect.Deny("flaky_policy", "denied downstream of the budget gate"), nil
		},
	}
	engine := newInspectorEngine(t, gateway, events, func(d *Dependencies) {
		d.ToolInspectorAppend = []toolinspect.Inspector{inspector}
	})
	engine.scenario.Runtime.DoomLoopLimit = 2
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-insp-doom", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	denials := toolDeniedPayloads(events)
	if len(denials) != 2 {
		t.Fatalf("expected two ToolDenied events (custom + doom_loop), got %v", denials)
	}
	if denials[0]["kind"] != "flaky_policy" {
		t.Fatalf("first denial kind=%v want flaky_policy", denials[0]["kind"])
	}
	if denials[1]["kind"] != "doom_loop" || !strings.HasPrefix(denials[1]["reason"].(string), "doom-loop detected") {
		t.Fatalf("second denial should be the doom-loop budget gate, got %v", denials[1])
	}
}
