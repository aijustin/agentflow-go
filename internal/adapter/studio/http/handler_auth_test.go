package studiohttp

import (
	"bytes"
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type runSaverStub struct{ calls int }

func (s *runSaverStub) RunStudioGraph(context.Context, any, any) (any, error) {
	s.calls++
	return map[string]string{"status": "ok"}, nil
}

func (s *runSaverStub) SaveStudioGraph(context.Context, any) (any, error) {
	s.calls++
	return map[string]string{"status": "ok"}, nil
}

// TestMutatingEndpointsDefaultDeny: studio run/save must not execute without
// a Policy or the explicit insecure opt-out; pure-transform endpoints stay
// open.
func TestMutatingEndpointsDefaultDeny(t *testing.T) {
	stub := &runSaverStub{}
	handler := NewHandler(HandlerConfig{Run: stub, Save: stub})
	for _, path := range []string{"/v1/studio/run", "/v1/studio/save"} {
		req := httptest.NewRequest(nethttp.MethodPost, path, bytes.NewBufferString(`{"graph":{}}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != nethttp.StatusForbidden {
			t.Fatalf("%s: expected 403, got %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "auth_required") {
			t.Fatalf("%s: expected auth_required code, got %s", path, rec.Body.String())
		}
	}
	if stub.calls != 0 {
		t.Fatalf("denied requests must not execute, got %d calls", stub.calls)
	}
}

// TestMutatingEndpointsInsecureOptOut keeps the old open behavior behind the
// explicit opt-out.
func TestMutatingEndpointsInsecureOptOut(t *testing.T) {
	stub := &runSaverStub{}
	handler := NewHandler(HandlerConfig{Run: stub, InsecureAllowNoAuth: true})
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/studio/run", bytes.NewBufferString(`{"graph":{}}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 with insecure opt-out, got %d: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("expected one execution, got %d", stub.calls)
	}
}
