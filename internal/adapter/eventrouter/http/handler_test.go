package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestSignatureVerifierRejectsBadSignature: with a verifier configured, a
// failing signature check rejects the request with 401 before decoding.
func TestSignatureVerifierRejectsBadSignature(t *testing.T) {
	calls := 0
	handler, err := eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{
		Framework: &stubRunner{},
		VerifySignature: func(r *http.Request, body []byte) error {
			calls++
			if r.Header.Get("X-Signature") != "valid" {
				return errors.New("bad signature")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := `{"type":"ticket.created","payload":{}}`

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(event)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_signature") {
		t.Fatalf("expected invalid_signature code, got %s", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(event))
	req.Header.Set("X-Signature", "valid")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid signature, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("expected two verifier calls, got %d", calls)
	}
}
