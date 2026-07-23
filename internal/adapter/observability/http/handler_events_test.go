package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestHandlerEventsAndRunsFilters(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store, TraceExploreURL: "https://trace.example"})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-a", ScenarioName: "demo", Timestamp: ts}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunCompleted, RunID: "run-a", ScenarioName: "demo", Timestamp: ts.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-b", ScenarioName: "demo", Timestamp: ts}); err != nil {
		t.Fatal(err)
	}

	ui := httptest.NewRecorder()
	handler.ServeHTTP(ui, httptest.NewRequest(http.MethodGet, "/api/ui-config", nil))
	if ui.Code != http.StatusOK {
		t.Fatalf("ui-config code=%d", ui.Code)
	}

	runs := httptest.NewRecorder()
	handler.ServeHTTP(runs, httptest.NewRequest(http.MethodGet, "/api/runs?limit=1&offset=0", nil))
	if runs.Code != http.StatusOK {
		t.Fatalf("runs code=%d body=%s", runs.Code, runs.Body.String())
	}

	events := httptest.NewRecorder()
	handler.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/api/runs/run-a/events?after_sequence=1&limit=10", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("events code=%d body=%s", events.Code, events.Body.String())
	}
	var eventsBody struct {
		Events []obspkg.EventRecord `json:"events"`
	}
	if err := json.Unmarshal(events.Body.Bytes(), &eventsBody); err != nil {
		t.Fatal(err)
	}
	if len(eventsBody.Events) != 1 || eventsBody.Events[0].Event.Type != core.EventRunCompleted {
		t.Fatalf("unexpected events: %+v", eventsBody.Events)
	}
}

func TestHandlerEventsPresetProductUI(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	for _, event := range []core.Event{
		{Type: core.EventRunStarted, RunID: "run-preset", Timestamp: ts},
		{Type: core.EventMemoryRead, RunID: "run-preset", Timestamp: ts.Add(time.Second)},
		{Type: core.EventToolCalled, RunID: "run-preset", Timestamp: ts.Add(2 * time.Second)},
	} {
		if _, err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	product := httptest.NewRecorder()
	handler.ServeHTTP(product, httptest.NewRequest(http.MethodGet, "/api/runs/run-preset/events?preset=product_ui", nil))
	if product.Code != http.StatusOK {
		t.Fatalf("product_ui code=%d body=%s", product.Code, product.Body.String())
	}
	var productBody struct {
		Events []obspkg.EventRecord   `json:"events"`
		Preset core.EventFilterPreset `json:"preset"`
	}
	if err := json.Unmarshal(product.Body.Bytes(), &productBody); err != nil {
		t.Fatal(err)
	}
	if productBody.Preset != core.EventFilterProductUI {
		t.Fatalf("preset=%q", productBody.Preset)
	}
	if len(productBody.Events) != 2 {
		t.Fatalf("expected 2 product events, got %d", len(productBody.Events))
	}

	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/runs/run-preset/events?preset=nope", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown preset, got %d", bad.Code)
	}
}

func TestHandlerRunsFilterByStatus(t *testing.T) {
	ctx := context.Background()
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC()
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-done", ScenarioName: "demo", Timestamp: ts}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunCompleted, RunID: "run-done", ScenarioName: "demo", Timestamp: ts.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-open", ScenarioName: "demo", Timestamp: ts}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs?status=completed&limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("runs code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Runs []obspkg.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Runs) != 1 || body.Runs[0].RunID != "run-done" {
		t.Fatalf("unexpected filtered runs: %+v", body.Runs)
	}
}

func TestHandlerRunsRejectsNonGet(t *testing.T) {
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: obsinmem.NewStore()})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandlerStreamEndpointReturnsEventStream(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/run-1/stream", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 stream, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("unexpected content type %q", rec.Header().Get("Content-Type"))
	}
}

func TestHandlerEventsMethodNotAllowed(t *testing.T) {
	store := obsinmem.NewStore()
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/run-1/events", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
