package http

import (
	"bytes"
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type denyCountingPurger struct{ calls int }

func (p *denyCountingPurger) PurgeRuns(context.Context, runstate.ListFilter) (int, error) {
	p.calls++
	return 0, nil
}

func (p *denyCountingPurger) PurgeExpired(context.Context, time.Duration) (int, error) {
	p.calls++
	return 0, nil
}

func (p *denyCountingPurger) PurgeWithPolicy(context.Context, RetentionPolicy) (int, error) {
	p.calls++
	return 0, nil
}

func (p *denyCountingPurger) PurgeOrphanBlobs(context.Context) (int, error) {
	p.calls++
	return 0, nil
}

// TestPurgeEndpointsDefaultDeny: destructive purge endpoints must not run
// without a Policy or the explicit insecure opt-out.
func TestPurgeEndpointsDefaultDeny(t *testing.T) {
	purger := &denyCountingPurger{}
	handler, err := NewHandler(HandlerConfig{Purger: purger})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/admin/retention/purge-runs", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth_required") {
		t.Fatalf("expected auth_required code, got %s", rec.Body.String())
	}
	if purger.calls != 0 {
		t.Fatalf("denied purge must not execute, got %d calls", purger.calls)
	}
}

// TestPurgeEndpointsInsecureOptOut keeps the old open behavior behind the
// explicit opt-out.
func TestPurgeEndpointsInsecureOptOut(t *testing.T) {
	purger := &denyCountingPurger{}
	handler, err := NewHandler(HandlerConfig{Purger: purger, InsecureAllowNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/admin/retention/purge-runs", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 with insecure opt-out, got %d: %s", rec.Code, rec.Body.String())
	}
	if purger.calls != 1 {
		t.Fatalf("expected one purge, got %d", purger.calls)
	}
}
