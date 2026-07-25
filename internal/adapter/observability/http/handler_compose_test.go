package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	obsinmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
)

func composeTestHandler(t *testing.T, insecure bool) *Handler {
	t.Helper()
	handler, err := NewHandler(Config{
		InsecureAllowNoAuth: insecure,
		Store:               obsinmem.NewStore(),
		Compose:             studioStub{value: map[string]any{"valid": true}},
		Parts:               studioStub{value: map[string]any{"agents": []any{map[string]any{"name": "assistant"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHandlerStudioCompose(t *testing.T) {
	handler := composeTestHandler(t, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/studio/compose", strings.NewReader(`{"prompt":"build a pipeline","mode":"catalog"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("compose code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStudioComposeRequiresPrompt(t *testing.T) {
	handler := composeTestHandler(t, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/studio/compose", strings.NewReader(`{"mode":"catalog"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStudioComposeGuardsMutating(t *testing.T) {
	handler := composeTestHandler(t, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/studio/compose", strings.NewReader(`{"prompt":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 auth_required, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStudioParts(t *testing.T) {
	handler := composeTestHandler(t, true)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/studio/parts", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "assistant") {
		t.Fatalf("parts code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStudioComposeNotConfigured(t *testing.T) {
	handler, err := NewHandler(Config{InsecureAllowNoAuth: true, Store: obsinmem.NewStore()})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/studio/compose", strings.NewReader(`{"prompt":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d body=%s", rec.Code, rec.Body.String())
	}

	partsRec := httptest.NewRecorder()
	handler.ServeHTTP(partsRec, httptest.NewRequest(http.MethodGet, "/api/studio/parts", nil))
	if partsRec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d body=%s", partsRec.Code, partsRec.Body.String())
	}
}

func (s studioStub) ComposeStudioGraph(_ context.Context, _ any) (any, error) {
	return s.value, nil
}

func (s studioStub) ListStudioParts() any {
	return s.value
}
