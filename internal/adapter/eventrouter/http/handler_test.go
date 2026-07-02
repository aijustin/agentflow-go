package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	eventrouterhttp "github.com/aijustin/agentflow-go/internal/adapter/eventrouter/http"
	"github.com/aijustin/agentflow-go/pkg/eventrouter"
)

type stubRunner struct {
	last eventrouter.Event
}

func (s *stubRunner) HandleEvent(_ *http.Request, event eventrouter.Event) (any, error) {
	s.last = event
	return map[string]string{"status": "ok"}, nil
}

func TestHandlerRejectsNonPost(t *testing.T) {
	runner := &stubRunner{}
	handler, err := eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{Framework: runner})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandlerDispatchesEvent(t *testing.T) {
	runner := &stubRunner{}
	handler, err := eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{Framework: runner})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"type":"ticket.created","payload":{"id":"1"}}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if runner.last.Type != "ticket.created" {
		t.Fatalf("unexpected event: %+v", runner.last)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandlerRejectsInvalidJSON(t *testing.T) {
	runner := &stubRunner{}
	handler, err := eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{Framework: runner})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBufferString(`not-json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestNewHandlerRequiresFramework(t *testing.T) {
	if _, err := eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{}); err == nil {
		t.Fatal("expected nil framework error")
	}
	_ = context.Background()
}
