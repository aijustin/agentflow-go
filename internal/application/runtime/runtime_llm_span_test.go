package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
	obsprometheus "github.com/aijustin/agentflow-go/pkg/observability/prometheus"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type recordedSpan struct {
	mu     sync.Mutex
	name   observability.SpanName
	attrs  map[string]string
	errors []error
	ended  bool
}

func (s *recordedSpan) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, err)
}

func (s *recordedSpan) SetAttributes(attrs ...observability.Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, attr := range attrs {
		s.attrs[attr.Key] = attr.Value
	}
}

func (s *recordedSpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
}

type recordingTracer struct {
	mu    sync.Mutex
	spans []*recordedSpan
}

func (t *recordingTracer) Start(ctx context.Context, name observability.SpanName, attrs ...observability.Attribute) (context.Context, observability.Span) {
	span := &recordedSpan{name: name, attrs: map[string]string{}}
	for _, attr := range attrs {
		span.attrs[attr.Key] = attr.Value
	}
	t.mu.Lock()
	t.spans = append(t.spans, span)
	t.mu.Unlock()
	return ctx, span
}

func (t *recordingTracer) spansNamed(name observability.SpanName) []*recordedSpan {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*recordedSpan
	for _, span := range t.spans {
		if span.name == name {
			out = append(out, span)
		}
	}
	return out
}

func scrapeMetrics(t *testing.T, recorder *obsprometheus.Recorder) string {
	t.Helper()
	rw := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(rw, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(rw.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestEngineLLMCallSpanAndMetricsOnSuccess(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{
		Message: llm.Message{Content: "llm answer"},
		Usage:   llm.TokenUsage{InputTokens: 3, OutputTokens: 2},
	})
	tracer := &recordingTracer{}
	recorder := obsprometheus.NewRecorder()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway, Tracer: tracer, Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-span", Agent: "assistant", Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	spans := tracer.spansNamed(observability.SpanLLMCall)
	if len(spans) != 1 {
		t.Fatalf("expected exactly one llm call span, got %d", len(spans))
	}
	span := spans[0]
	span.mu.Lock()
	defer span.mu.Unlock()
	if !span.ended {
		t.Fatal("llm call span was not ended")
	}
	if len(span.errors) != 0 {
		t.Fatalf("unexpected span errors: %v", span.errors)
	}
	for key, want := range map[string]string{
		"run_id":        "run-span",
		"agent":         "assistant",
		"profile":       "default",
		"model":         "test",
		"scenario_name": "scenario",
		"attempt":       "1",
	} {
		if got := span.attrs[key]; got != want {
			t.Fatalf("span attr %s=%q want %q", key, got, want)
		}
	}
	metrics := scrapeMetrics(t, recorder)
	for _, want := range []string{
		`agentflow_llm_tokens_total{kind="prompt",profile="default"} 3`,
		`agentflow_llm_tokens_total{kind="completion",profile="default"} 2`,
		`agentflow_llm_duration_seconds_count{profile="default"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics)
		}
	}
	if strings.Contains(metrics, "agentflow_llm_errors_total") {
		t.Fatalf("successful call must not record llm errors:\n%s", metrics)
	}
}

func TestEngineLLMCallSpanFailureRecordsError(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway() // no queued response: Chat fails with ErrNoResponse
	tracer := &recordingTracer{}
	recorder := obsprometheus.NewRecorder()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway, Tracer: tracer, Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-span-fail", Agent: "assistant", Prompt: "hello"}); err == nil {
		t.Fatal("expected run failure")
	}
	spans := tracer.spansNamed(observability.SpanLLMCall)
	if len(spans) != 1 {
		t.Fatalf("expected exactly one llm call span, got %d", len(spans))
	}
	span := spans[0]
	span.mu.Lock()
	defer span.mu.Unlock()
	if !span.ended {
		t.Fatal("llm call span was not ended")
	}
	if len(span.errors) != 1 || !errors.Is(span.errors[0], llmmock.ErrNoResponse) {
		t.Fatalf("expected ErrNoResponse recorded on span, got %v", span.errors)
	}
	metrics := scrapeMetrics(t, recorder)
	for _, want := range []string{
		`agentflow_llm_errors_total{profile="default"} 1`,
		`agentflow_llm_duration_seconds_count{profile="default"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics)
		}
	}
	// The run span must also carry the failure (Run's failRun path).
	runSpans := tracer.spansNamed(observability.SpanRun)
	if len(runSpans) != 1 {
		t.Fatalf("expected one run span, got %d", len(runSpans))
	}
	runSpans[0].mu.Lock()
	defer runSpans[0].mu.Unlock()
	if len(runSpans[0].errors) != 1 {
		t.Fatalf("expected failure recorded on run span, got %v", runSpans[0].errors)
	}
}

func TestEngineStreamLLMSpanClosesWithStream(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapStream)
	gateway.QueueChat("default", llm.ChatResponse{
		Message: llm.Message{Content: "streamed answer"},
		Usage:   llm.TokenUsage{InputTokens: 4, OutputTokens: 6},
	})
	tracer := &recordingTracer{}
	recorder := obsprometheus.NewRecorder()
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway, Tracer: tracer, Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(context.Background(), RunRequest{RunID: "run-stream-span", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	spans := tracer.spansNamed(observability.SpanLLMCall)
	if len(spans) != 1 {
		t.Fatalf("expected exactly one llm call span, got %d", len(spans))
	}
	span := spans[0]
	span.mu.Lock()
	defer span.mu.Unlock()
	if !span.ended {
		t.Fatal("stream llm span was not ended after the stream terminated")
	}
	if len(span.errors) != 0 {
		t.Fatalf("unexpected span errors: %v", span.errors)
	}
	if got := span.attrs["stream"]; got != "true" {
		t.Fatalf("span attr stream=%q want true", got)
	}
	metrics := scrapeMetrics(t, recorder)
	for _, want := range []string{
		`agentflow_llm_tokens_total{kind="prompt",profile="default"} 4`,
		`agentflow_llm_tokens_total{kind="completion",profile="default"} 6`,
		`agentflow_llm_duration_seconds_count{profile="default"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics)
		}
	}
}

type heldOpenTerminalGateway struct {
	source <-chan llm.ChatChunk
}

func (g heldOpenTerminalGateway) Supports(_ string, capability llm.Capability) bool {
	return capability == llm.CapChat || capability == llm.CapStream
}

func (heldOpenTerminalGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("chat should not be called")
}

func (g heldOpenTerminalGateway) StreamChat(context.Context, string, llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return g.source, nil
}

func TestEngineStreamLLMSpanEndsBeforeTerminalIsVisible(t *testing.T) {
	providerStream := make(chan llm.ChatChunk, 1)
	providerStream <- llm.ChatChunk{
		Content: "streamed answer",
		Done:    true,
		Usage:   llm.TokenUsage{InputTokens: 4, OutputTokens: 6},
	}
	defer close(providerStream)

	tracer := &recordingTracer{}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    heldOpenTerminalGateway{source: providerStream},
		Tracer: tracer,
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := engine.Stream(context.Background(), RunRequest{
		RunID:  "run-stream-span-ordering",
		Agent:  "assistant",
		Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal, ok := <-ch
	if !ok || !terminal.Done {
		t.Fatalf("expected terminal chunk, got %+v, open=%t", terminal, ok)
	}
	spans := tracer.spansNamed(observability.SpanLLMCall)
	if len(spans) != 1 {
		t.Fatalf("expected exactly one llm call span, got %d", len(spans))
	}
	span := spans[0]
	span.mu.Lock()
	ended := span.ended
	span.mu.Unlock()
	if !ended {
		t.Fatal("llm span must end before the terminal chunk becomes visible")
	}
	for range ch {
	}
}

func TestEngineRunHybridFailureRecordsSpanError(t *testing.T) {
	saveRunning := func(t *testing.T, repo *runstateinmem.Repository, runID string) {
		t.Helper()
		if err := repo.Save(context.Background(), &runstate.RunSnapshot{
			RunID: runID, ScenarioName: "scenario", Status: runstate.RunStatusRunning,
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	newEngine := func(t *testing.T, repo *runstateinmem.Repository, tracer *recordingTracer, gateway *llmmock.Gateway) *Engine {
		t.Helper()
		engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway, Tracer: tracer})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	cases := []struct {
		name  string
		runID string
		agent string
		queue bool
	}{
		{name: "resolve agent fails", runID: "run-hybrid-noagent", agent: "missing"},
		{name: "answer fails", runID: "run-hybrid-llmfail", agent: "assistant"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := runstateinmem.NewRepository()
			tracer := &recordingTracer{}
			engine := newEngine(t, repo, tracer, llmmock.NewGateway())
			saveRunning(t, repo, tt.runID)
			if _, err := engine.RunHybrid(context.Background(), RunRequest{RunID: tt.runID, Agent: tt.agent, Prompt: "go"}); err == nil {
				t.Fatal("expected hybrid failure")
			}
			runSpans := tracer.spansNamed(observability.SpanRun)
			if len(runSpans) != 1 {
				t.Fatalf("expected one run span, got %d", len(runSpans))
			}
			span := runSpans[0]
			span.mu.Lock()
			defer span.mu.Unlock()
			if !span.ended {
				t.Fatal("run span was not ended")
			}
			if len(span.errors) != 1 {
				t.Fatalf("expected failure recorded on run span, got %v", span.errors)
			}
			if got := span.attrs["hybrid"]; got != "true" {
				t.Fatalf("span attr hybrid=%q want true", got)
			}
		})
	}
}

func TestEngineToolErrorCounterOnFailure(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Content: "call tool"}},
		ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "done"}})
	recorder := obsprometheus.NewRecorder()
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:     repo,
		LLM:      gateway,
		Tools:    mapToolRegistry{"echo": brokenTool{}},
		Recorder: recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = engine.Run(context.Background(), RunRequest{RunID: "run-tool-err", Agent: "assistant", Prompt: "go"})
	metrics := scrapeMetrics(t, recorder)
	if !strings.Contains(metrics, `agentflow_tool_errors_total{scenario="scenario",tool="echo"}`) {
		t.Fatalf("metrics missing tool errors counter:\n%s", metrics)
	}
}

func TestEngineLLMCalledEventRedactsPromptByDefault(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "ok"}})
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, LLM: gateway, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-redact-default", Agent: "assistant", Prompt: "my topsecret prompt"}); err != nil {
		t.Fatal(err)
	}
	payload := llmCalledEventPayload(t, events)
	if strings.Contains(string(payload), "topsecret") {
		t.Fatalf("default LLMCalled payload must not contain prompt plaintext: %s", payload)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["message_count"].(float64) < 1 {
		t.Fatalf("message_count missing: %v", decoded)
	}
	if _, ok := decoded["messages_hash"].(string); !ok {
		t.Fatalf("messages_hash missing: %v", decoded)
	}
}

func TestEngineLLMCalledEventCaptureOptInStillRedacts(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "ok"}})
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs:              repo,
		LLM:               gateway,
		Events:            events,
		LLMPayloadCapture: true,
		OutputRedactor:    substringRedactor{find: "topsecret", replace: "[REDACTED]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-redact-optin", Agent: "assistant", Prompt: "my topsecret prompt"}); err != nil {
		t.Fatal(err)
	}
	payload := llmCalledEventPayload(t, events)
	if strings.Contains(string(payload), "topsecret") {
		t.Fatalf("opt-in payload must still pass through the redactor: %s", payload)
	}
	if !strings.Contains(string(payload), "[REDACTED]") {
		t.Fatalf("opt-in payload must carry redacted prompt plaintext: %s", payload)
	}
}

func llmCalledEventPayload(t *testing.T, events *captureEvents) json.RawMessage {
	t.Helper()
	for _, event := range events.events {
		if event.Type == core.EventLLMCalled {
			return event.Payload
		}
	}
	t.Fatal("no LLMCalled event captured")
	return nil
}
