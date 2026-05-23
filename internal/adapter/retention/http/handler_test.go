package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	retentionhttp "github.com/aijustin/agentflow-go/internal/adapter/retention/http"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type stubPurger struct {
	runsRemoved int
	blobsRemoved int
	lastPolicy retentionhttp.RetentionPolicy
}

func (s *stubPurger) PurgeRuns(context.Context, runstate.ListFilter) (int, error) {
	return s.runsRemoved, nil
}

func (s *stubPurger) PurgeExpired(context.Context, time.Duration) (int, error) {
	return s.runsRemoved, nil
}

func (s *stubPurger) PurgeWithPolicy(_ context.Context, policy retentionhttp.RetentionPolicy) (int, error) {
	s.lastPolicy = policy
	return s.runsRemoved, nil
}

func (s *stubPurger) PurgeOrphanBlobs(context.Context) (int, error) {
	return s.blobsRemoved, nil
}

func TestHandlerPurgePolicy(t *testing.T) {
	purger := &stubPurger{runsRemoved: 3}
	handler, err := retentionhttp.NewHandler(retentionhttp.HandlerConfig{Purger: purger})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"max_age":"1h","limit":10}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/retention/purge-policy", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if purger.lastPolicy.MaxAge != time.Hour || purger.lastPolicy.Limit != 10 {
		t.Fatalf("unexpected policy: %+v", purger.lastPolicy)
	}
	var resp struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Removed != 3 {
		t.Fatalf("expected removed=3, got %d", resp.Removed)
	}
}
