package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestHandlerServeHTTP(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		body       string
		gateErr    error
		wantStatus int
		wantCalls  int
	}{
		{name: "success", method: http.MethodPost, body: `{"token":"t","decision":"approve"}`, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "wrong method", method: http.MethodGet, wantStatus: http.StatusMethodNotAllowed},
		{name: "bad json", method: http.MethodPost, body: `{`, wantStatus: http.StatusBadRequest},
		{name: "invalid decision", method: http.MethodPost, body: `{"token":"t","decision":"skip"}`, wantStatus: http.StatusBadRequest},
		{name: "gate conflict", method: http.MethodPost, body: `{"token":"t","decision":"reject"}`, gateErr: errors.New("conflict"), wantStatus: http.StatusConflict, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := &fakeGate{err: tt.gateErr}
			req := httptest.NewRequest(tt.method, "/resume", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			NewHandler(HandlerConfig{Gate: gate}).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if gate.calls != tt.wantCalls {
				t.Fatalf("got calls %d, want %d", gate.calls, tt.wantCalls)
			}
		})
	}
}

type fakeGate struct {
	err      error
	calls    int
	decision core.Decision
}

func (g *fakeGate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	g.calls++
	g.decision = decision
	return g.err
}
