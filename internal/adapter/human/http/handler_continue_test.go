package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestHandlerContinueUsesContinuer(t *testing.T) {
	continuer := &fakeContinuer{result: map[string]string{"output": "done"}}
	req := httptest.NewRequest(http.MethodPost, "/resume", bytes.NewBufferString(`{"token":"t","decision":"approve","continue":true}`))
	rec := httptest.NewRecorder()
	NewHandler(HandlerConfig{Continuer: continuer}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if continuer.calls != 1 {
		t.Fatalf("expected continuer call, got %d", continuer.calls)
	}
}

type fakeContinuer struct {
	calls  int
	result any
}

func (f *fakeContinuer) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	return nil
}

func (f *fakeContinuer) ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (any, error) {
	f.calls++
	return f.result, nil
}
