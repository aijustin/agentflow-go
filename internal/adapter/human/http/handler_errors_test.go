package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// TestResumeErrorMapping pins the HTTP status and error_code contract for
// resume failures so clients can branch on machine-readable codes instead of
// message text.
func TestResumeErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "expired token", err: runstate.ErrTokenExpired, wantStatus: http.StatusGone, wantCode: "token_expired"},
		{name: "invalid token", err: runstate.ErrInvalidToken, wantStatus: http.StatusUnauthorized, wantCode: "invalid_token"},
		{name: "superseded token", err: runstate.ErrTokenSuperseded, wantStatus: http.StatusConflict, wantCode: "token_superseded"},
		{name: "resume in progress", err: runstate.ErrResumeInProgress, wantStatus: http.StatusConflict, wantCode: "resume_in_progress"},
		{name: "run in progress", err: appexec.ErrRunInProgress, wantStatus: http.StatusConflict, wantCode: "run_in_progress"},
		{name: "wrapped sentinel", err: fmt.Errorf("agentflow: run %q: %w", "run-1", runstate.ErrResumeInProgress), wantStatus: http.StatusConflict, wantCode: "resume_in_progress"},
		{name: "unknown error", err: errors.New("db exploded"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := &fakeGate{err: tt.err}
			req := httptest.NewRequest(http.MethodPost, "/resume", bytes.NewBufferString(`{"token":"t","decision":"approve"}`))
			rec := httptest.NewRecorder()
			NewHandler(HandlerConfig{Gate: gate}).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var body struct {
				Error     string `json:"error"`
				ErrorCode string `json:"error_code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("error body is not JSON: %v", err)
			}
			if body.ErrorCode != tt.wantCode {
				t.Fatalf("got error_code %q, want %q", body.ErrorCode, tt.wantCode)
			}
			if body.Error == "" {
				t.Fatal("expected human-readable error message in body")
			}
		})
	}
}

// TestContinueErrorMapping exercises the same mapping through the
// ResumeAndContinue path.
func TestContinueErrorMapping(t *testing.T) {
	continuer := &fakeContinuer{err: runstate.ErrResumeInProgress}
	req := httptest.NewRequest(http.MethodPost, "/resume", bytes.NewBufferString(`{"token":"t","decision":"approve","continue":true}`))
	rec := httptest.NewRecorder()
	NewHandler(HandlerConfig{Continuer: continuer}).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorCode != "resume_in_progress" {
		t.Fatalf("got error_code %q", body.ErrorCode)
	}
}
