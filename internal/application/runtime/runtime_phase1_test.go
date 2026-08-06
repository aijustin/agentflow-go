package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// terminalPayload returns the decoded RunTerminalPayload of the first event
// of typ, failing the test when the event was never emitted.
func (c *captureEvents) terminalPayload(t *testing.T, typ core.EventType) core.RunTerminalPayload {
	t.Helper()
	for _, event := range c.events {
		if event.Type != typ {
			continue
		}
		var payload core.RunTerminalPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v (raw=%s)", typ, err, event.Payload)
		}
		return payload
	}
	t.Fatalf("expected %s event, got %v", typ, c.types())
	return core.RunTerminalPayload{}
}

func TestTerminationReasonForError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "max steps sentinel", err: fmt.Errorf("%w=9", ErrMaxStepsExceeded), want: core.TerminationReasonMaxStepsExceeded},
		{name: "deadline", err: context.DeadlineExceeded, want: core.TerminationReasonTimeout},
		{name: "stale fence", err: runstate.ErrStaleFence, want: core.TerminationReasonLeaseLost},
		{name: "lease lost", err: coordination.ErrRunLeaseLost, want: core.TerminationReasonLeaseLost},
		{name: "provider api error", err: llm.APIError{Provider: "openai", StatusCode: 500, Status: "500"}, want: core.TerminationReasonLLMError},
		{name: "wrapped api error", err: fmt.Errorf("call: %w", llm.APIError{Provider: "openai", StatusCode: 400, Status: "400"}), want: core.TerminationReasonLLMError},
		{name: "generic error", err: errors.New("boom"), want: core.TerminationReasonError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := terminationReasonForError(tc.err); got != tc.want {
				t.Fatalf("terminationReasonForError(%v)=%q want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestEngineMaxStepsFailureCarriesTerminationReason(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 1), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-maxsteps", Agent: "assistant", Prompt: "loop"})
	if !errors.Is(err, ErrMaxStepsExceeded) {
		t.Fatalf("expected ErrMaxStepsExceeded, got %v", err)
	}
	// The historic message text must survive the sentinel wrapping.
	if !strings.Contains(err.Error(), "max_steps=1") {
		t.Fatalf("error text changed: %v", err)
	}
	payload := events.terminalPayload(t, core.EventRunFailed)
	if payload.TerminationReason != core.TerminationReasonMaxStepsExceeded {
		t.Fatalf("TerminationReason=%q want %q", payload.TerminationReason, core.TerminationReasonMaxStepsExceeded)
	}
}

func TestEngineCancellationCarriesTerminationReason(t *testing.T) {
	repo := runstateinmem.NewRepository()
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-cancel-reason", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.markRunFailedOrCancelled(ctx, "run-cancel-reason", context.Canceled)
	payload := events.terminalPayload(t, core.EventRunCancelled)
	if payload.Status != "cancelled" || payload.TerminationReason != core.TerminationReasonCancelled {
		t.Fatalf("unexpected cancelled payload: %+v", payload)
	}
}

func TestUsageTrackerCheckpointCompat(t *testing.T) {
	// Snapshots written before checkpoint_usage existed have no (or an empty)
	// payload: they must decode to a zero tracker, not an error.
	for _, raw := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("  ")} {
		tracker, err := decodeUsageTracker(raw)
		if err != nil {
			t.Fatalf("decode empty payload: %v", err)
		}
		if tracker.lastCallTokens() != 0 || tracker.contextRecoveryAttempts() != 0 {
			t.Fatalf("expected zero tracker, got %+v", tracker)
		}
	}
	tracker := newUsageTracker()
	tracker.record(llm.TokenUsage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120})
	tracker.record(llm.TokenUsage{InputTokens: 200, OutputTokens: 30, TotalTokens: 230})
	tracker.markContextRecovery()
	raw, err := json.Marshal(tracker)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeUsageTracker(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.InputTokens != 300 || decoded.OutputTokens != 50 || decoded.TotalTokens != 350 {
		t.Fatalf("totals not preserved: %+v", decoded)
	}
	// The last-call predictor is the most recent call, not the accumulation.
	if got := decoded.lastCallTokens(); got != 230 {
		t.Fatalf("lastCallTokens=%d want 230", got)
	}
	if got := decoded.contextRecoveryAttempts(); got != 1 {
		t.Fatalf("contextRecoveryAttempts=%d want 1", got)
	}
	if _, err := decodeUsageTracker(json.RawMessage(`{`)); err == nil {
		t.Fatal("corrupt payload must fail decode")
	}
}

// knownUsageScenario builds a tool-loop scenario whose context policy
// compresses tool outputs once the trigger ratio (0.5 of 1000 tokens) is
// crossed. The echo output (~340 heuristic tokens) stays under the trigger,
// so only a provider-reported usage above 500 can fire compression.
func knownUsageScenario() core.Scenario {
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	profile := scenario.LLMs["default"]
	profile.Context = contextwindow.Policy{
		Strategy:       contextwindow.StrategySlidingWindow,
		MaxInputTokens: 1000,
		Compression:    contextwindow.CompressionPolicy{Enabled: true, TriggerRatio: 0.5},
	}
	scenario.LLMs["default"] = profile
	return scenario
}

func TestEngineKnownUsageDrivesCompression(t *testing.T) {
	cases := []struct {
		name           string
		usage          llm.TokenUsage
		wantCompressed bool
	}{
		{name: "heuristic-sized usage does not compress", usage: llm.TokenUsage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}, wantCompressed: false},
		{name: "large real usage triggers compression", usage: llm.TokenUsage{InputTokens: 800, OutputTokens: 100, TotalTokens: 900}, wantCompressed: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gateway := llmmock.NewGateway()
			gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
			gateway.QueueToolCall("default", llm.ToolCallResponse{
				ChatResponse: llm.ChatResponse{Usage: tc.usage},
				ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
			})
			gateway.QueueToolCall("default", llm.ToolCallResponse{
				ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
			})
			engine, err := NewEngine(knownUsageScenario(), Dependencies{
				Runs:   runstateinmem.NewRepository(),
				LLM:    gateway,
				Tools:  mapToolRegistry{"echo": largeOutputTool{}},
				Events: &captureEvents{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-usage", Agent: "assistant", Prompt: "go"}); err != nil {
				t.Fatal(err)
			}
			requests := gateway.ToolRequests("default")
			if len(requests) != 2 {
				t.Fatalf("expected 2 llm calls, got %d", len(requests))
			}
			compressed := false
			for _, msg := range requests[1].Messages {
				if msg.Metadata["context_window"] == "compressed" {
					compressed = true
				}
			}
			if compressed != tc.wantCompressed {
				t.Fatalf("compressed=%v want %v", compressed, tc.wantCompressed)
			}
		})
	}
}

// scriptedToolGateway plays a fixed script of tool-call outcomes (error or
// response), recording every request for inspection.
type scriptedToolGateway struct {
	calls    int
	script   []scriptedToolTurn
	requests []llm.ToolCallRequest
}

type scriptedToolTurn struct {
	err  error
	resp llm.ToolCallResponse
}

func (g *scriptedToolGateway) Supports(_ string, cap llm.Capability) bool {
	return cap == llm.CapChat || cap == llm.CapToolCall
}

func (g *scriptedToolGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("unexpected plain chat call")
}

func (g *scriptedToolGateway) ChatWithTools(_ context.Context, _ string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	g.calls++
	g.requests = append(g.requests, req)
	if g.calls > len(g.script) {
		return llm.ToolCallResponse{}, errors.New("script exhausted")
	}
	turn := g.script[g.calls-1]
	return turn.resp, turn.err
}

func contextOverflowError() error {
	return llm.APIError{
		Provider:   "openai",
		StatusCode: 400,
		Status:     "400",
		Code:       llm.ErrCodeContextLengthExceeded,
		Body:       "This model's maximum context length is 8192 tokens",
	}
}

func contextRecoveryEvents(events *captureEvents) int {
	count := 0
	for _, event := range events.events {
		if event.Type != core.EventContextPrepared {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload["context_recovery"] == true {
			count++
		}
	}
	return count
}

func TestEngineContextLengthRecovery(t *testing.T) {
	finalAnswer := llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "recovered answer"}},
	}
	cases := []struct {
		name          string
		script        []scriptedToolTurn
		wantOutput    string
		wantErr       bool
		wantCalls     int
		wantRecovery  int
		wantReason    string
		wantReasonSet bool
	}{
		{
			name: "overflow once then succeed",
			script: []scriptedToolTurn{
				{err: contextOverflowError()},
				{resp: finalAnswer},
			},
			wantOutput:   "recovered answer",
			wantCalls:    2,
			wantRecovery: 1,
		},
		{
			name: "second overflow fails the run",
			script: []scriptedToolTurn{
				{err: contextOverflowError()},
				{err: contextOverflowError()},
			},
			wantErr:       true,
			wantCalls:     2,
			wantRecovery:  1,
			wantReason:    core.TerminationReasonLLMError,
			wantReasonSet: true,
		},
		{
			name: "non-context 400 does not recover",
			script: []scriptedToolTurn{
				{err: llm.APIError{Provider: "openai", StatusCode: 400, Status: "400", Body: "invalid schema"}},
			},
			wantErr:       true,
			wantCalls:     1,
			wantRecovery:  0,
			wantReason:    core.TerminationReasonLLMError,
			wantReasonSet: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gateway := &scriptedToolGateway{script: tc.script}
			events := &captureEvents{}
			engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
				Runs:   runstateinmem.NewRepository(),
				LLM:    gateway,
				Tools:  mapToolRegistry{"echo": echoTool{}},
				Events: events,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.Run(context.Background(), RunRequest{RunID: "run-cle", Agent: "assistant", Prompt: "go"})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected run to fail")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if result.Output != tc.wantOutput {
					t.Fatalf("output=%q want %q", result.Output, tc.wantOutput)
				}
			}
			if gateway.calls != tc.wantCalls {
				t.Fatalf("llm calls=%d want %d", gateway.calls, tc.wantCalls)
			}
			if got := contextRecoveryEvents(events); got != tc.wantRecovery {
				t.Fatalf("recovery events=%d want %d", got, tc.wantRecovery)
			}
			if tc.wantReasonSet {
				payload := events.terminalPayload(t, core.EventRunFailed)
				if payload.TerminationReason != tc.wantReason {
					t.Fatalf("TerminationReason=%q want %q", payload.TerminationReason, tc.wantReason)
				}
			}
		})
	}
}

func TestEngineEmptyTurnRetries(t *testing.T) {
	cases := []struct {
		name       string
		queued     int // number of empty turns queued before a real answer; -1 = never answer
		wantOutput string
		wantErr    string
		wantCalls  int
	}{
		{name: "empty turns retried then answer", queued: 2, wantOutput: "finally", wantCalls: 3},
		{name: "empty turns exhaust retries", queued: -1, wantErr: "empty response", wantCalls: 1 + maxEmptyTurnRetries},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gateway := llmmock.NewGateway()
			gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
			empties := tc.queued
			if empties < 0 {
				empties = 1 + maxEmptyTurnRetries
			}
			for i := 0; i < empties; i++ {
				// No content, no tool calls, finish_reason "stop": a contract
				// violation the loop must not accept as a final answer.
				gateway.QueueToolCall("default", llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{FinishReason: "stop"},
				})
			}
			if tc.queued >= 0 {
				gateway.QueueToolCall("default", llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: tc.wantOutput}},
				})
			}
			engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
				Runs:   runstateinmem.NewRepository(),
				LLM:    gateway,
				Tools:  mapToolRegistry{"echo": echoTool{}},
				Events: &captureEvents{},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.Run(context.Background(), RunRequest{RunID: "run-empty", Agent: "assistant", Prompt: "go"})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected %q error, got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if result.Output != tc.wantOutput {
					t.Fatalf("output=%q want %q", result.Output, tc.wantOutput)
				}
			}
			if got := len(gateway.ToolRequests("default")); got != tc.wantCalls {
				t.Fatalf("llm calls=%d want %d", got, tc.wantCalls)
			}
		})
	}
}

func TestToolArgsRepairDiagnostic(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{name: "valid object is not a repair", raw: json.RawMessage(`{"query":"x"}`), want: false},
		{name: "empty input is convention not repair", raw: json.RawMessage(`  `), want: false},
		{name: "truncated object is a repair", raw: json.RawMessage(`{"query":`), want: true},
		{name: "plain prose is a repair", raw: json.RawMessage(`not json at all`), want: true},
		{name: "string-encoded valid object unwraps cleanly", raw: json.RawMessage(`"{\"query\":\"x\"}"`), want: false},
		{name: "string-encoded malformed payload is a repair", raw: json.RawMessage(`"{\"query\":"`), want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			diag, repaired := toolArgsRepairDiagnostic(tc.raw)
			if repaired != tc.want {
				t.Fatalf("repaired=%v want %v", repaired, tc.want)
			}
			if repaired && !strings.Contains(diag, "not valid JSON") {
				t.Fatalf("diagnostic should carry the parse problem, got %q", diag)
			}
		})
	}
}

func TestEngineToolArgsNormalizationIsVisible(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "understood"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	tool := scenario.Tools["echo"]
	tool.InputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)
	scenario.Tools["echo"] = tool
	events := &captureEvents{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-args", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if !events.has(core.EventToolArgsNormalized) {
		t.Fatalf("expected ToolArgsNormalized event, got %v", events.types())
	}
	requests := gateway.ToolRequests("default")
	if len(requests) != 2 {
		t.Fatalf("expected 2 llm calls, got %d", len(requests))
	}
	var sawMarker, sawToolFeedback bool
	for _, msg := range requests[1].Messages {
		if msg.Metadata["tool_args_normalized"] == "true" {
			sawMarker = true
		}
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "not valid JSON") && strings.Contains(msg.Content, "invalid tool input") {
			sawToolFeedback = true
		}
	}
	if !sawMarker {
		t.Fatal("assistant message should carry the tool_args_normalized marker")
	}
	if !sawToolFeedback {
		t.Fatal("tool validation feedback should include the original parse error")
	}
}
