package http

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestHandlerServesDashboardRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	hub := obspkg.NewEventHub()
	handler, err := NewHandler(Config{Store: store, Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "sales", Timestamp: time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventLLMReturned, RunID: "run-1", ScenarioName: "sales", Timestamp: time.Date(2026, 5, 17, 10, 0, 1, 0, time.UTC), Payload: json.RawMessage(`{"output":"done"}`)}); err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "AgentFlow Observability") {
		t.Fatalf("dashboard page was not served: code=%d body=%q", page.Code, page.Body.String())
	}

	runs := httptest.NewRecorder()
	handler.ServeHTTP(runs, httptest.NewRequest(http.MethodGet, "/api/runs?limit=5", nil))
	if runs.Code != http.StatusOK {
		t.Fatalf("list runs code=%d body=%s", runs.Code, runs.Body.String())
	}
	var runsBody struct {
		Runs []obspkg.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(runs.Body.Bytes(), &runsBody); err != nil {
		t.Fatal(err)
	}
	if len(runsBody.Runs) != 1 || runsBody.Runs[0].RunID != "run-1" || runsBody.Runs[0].EventCount != 2 {
		t.Fatalf("unexpected runs response: %+v", runsBody.Runs)
	}

	events := httptest.NewRecorder()
	handler.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events?after_sequence=1", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("list events code=%d body=%s", events.Code, events.Body.String())
	}
	var eventsBody struct {
		Events []obspkg.EventRecord `json:"events"`
	}
	if err := json.Unmarshal(events.Body.Bytes(), &eventsBody); err != nil {
		t.Fatal(err)
	}
	if len(eventsBody.Events) != 1 || eventsBody.Events[0].Event.Type != core.EventLLMReturned {
		t.Fatalf("unexpected events response: %+v", eventsBody.Events)
	}
}

func TestHandlerStreamsRuntimeEvents(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	hub := obspkg.NewEventHub()
	handler, err := NewHandler(Config{Store: store, Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Append(ctx, core.Event{Type: core.EventToolCalled, RunID: "run-1", ScenarioName: "sales", Timestamp: time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{"tool":"search"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PublishEvent(ctx, record); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	requestCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL+"/api/runs/run-1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected stream content type %q", resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && requestCtx.Err() != nil {
			t.Fatal("timed out waiting for ToolCalled stream event")
		}
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if strings.Contains(line, "ToolCalled") {
			cancel()
			return
		}
		if err == io.EOF {
			t.Fatal("stream closed before ToolCalled event")
		}
	}
}

func TestNewHandlerValidatesConfig(t *testing.T) {
	if _, err := NewHandler(Config{}); err == nil {
		t.Fatal("expected nil store error")
	}
}
