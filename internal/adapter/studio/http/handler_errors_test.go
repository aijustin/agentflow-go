package studiohttp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	studiohttp "github.com/aijustin/agentflow-go/internal/adapter/studio/http"
)

func TestHandlerStudioValidationErrors(t *testing.T) {
	handler := studiohttp.NewHandler(studiohttp.HandlerConfig{InsecureAllowNoAuth: true,
		Validate:   studioStub{},
		Codegen:    studioStub{},
		YAML:       studioStub{},
		ImportYAML: studioStub{},
		Save:       studioStub{},
		Run:        studioStub{},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/validate", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("validate expected 400, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/import-yaml", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("import expected 400 for missing yaml, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/studio/run", strings.NewReader(`{"prompt":"hello"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("run expected 400 for missing graph, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerStudioNotConfiguredPerRoute(t *testing.T) {
	handler := studiohttp.NewHandler(studiohttp.HandlerConfig{InsecureAllowNoAuth: true})
	cases := []string{
		"/v1/studio/validate",
		"/v1/studio/codegen",
		"/v1/studio/yaml",
		"/v1/studio/import-yaml",
		"/v1/studio/save",
	}
	for _, path := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"demo"}`)))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s expected 501, got %d", path, rec.Code)
		}
	}
}
