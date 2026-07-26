package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowHeaderServer withholds response headers for delay, the way a provider
// does while a non-streaming completion is still generating.
func slowHeaderServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// A response-header deadline cannot tell "the model is still generating" apart
// from "the peer stalled": on a non-streaming completion no headers arrive
// until the answer is finished. Bounding it would cap how long a completion may
// take, which is the caller's decision (llm.Profile.Timeout), not the
// transport's.
func TestNewLongResponseDoesNotBoundTimeToFirstByte(t *testing.T) {
	transport := NewLongResponseTransport()
	if transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("expected no response header timeout, got %v", transport.ResponseHeaderTimeout)
	}

	server := slowHeaderServer(t, 250*time.Millisecond)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("expected a slow first byte to be tolerated, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
}

// The caller keeps control: a context deadline still cuts the call off.
func TestNewLongResponseStillHonorsContextDeadline(t *testing.T) {
	server := slowHeaderServer(t, 2*time.Second)
	client := &http.Client{Transport: NewLongResponseTransport()}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected the context deadline to cut the request off")
	}
}

// Connection establishment is still bounded: those deadlines cannot be
// confused with generation time.
func TestNewLongResponseStillBoundsConnectionSetup(t *testing.T) {
	transport := NewLongResponseTransport()
	if transport.TLSHandshakeTimeout != TLSHandshakeTimeout {
		t.Fatalf("expected TLS handshake timeout %v, got %v", TLSHandshakeTimeout, transport.TLSHandshakeTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected a dialer with a connect timeout")
	}
}

// The request/response variant keeps the header deadline, where a peer that
// goes quiet really is stalled.
func TestNewBoundsTimeToFirstByte(t *testing.T) {
	transport := NewTransport()
	transport.ResponseHeaderTimeout = 100 * time.Millisecond

	server := slowHeaderServer(t, 2*time.Second)
	client := &http.Client{Transport: transport}
	_, err := client.Get(server.URL)
	if err == nil {
		t.Fatal("expected a stalled peer to be cut off")
	}
	if !strings.Contains(err.Error(), "timeout awaiting response headers") {
		t.Fatalf("expected a response-header timeout, got %v", err)
	}
}
