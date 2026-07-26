package httpclient

import (
	"net/http"
	"testing"
)

// A streaming completion delivers tokens for as long as the caller allows, so
// an overall Client.Timeout would cut it off mid-response.
func TestNewLeavesClientTimeoutUnset(t *testing.T) {
	client := New()
	if client.Timeout != 0 {
		t.Fatalf("expected no overall client timeout, got %v", client.Timeout)
	}
}

func TestNewTransportBoundsStalledPeers(t *testing.T) {
	transport := NewTransport()
	if transport.ResponseHeaderTimeout != ResponseHeaderTimeout {
		t.Fatalf("expected response header timeout %v, got %v", ResponseHeaderTimeout, transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != TLSHandshakeTimeout {
		t.Fatalf("expected TLS handshake timeout %v, got %v", TLSHandshakeTimeout, transport.TLSHandshakeTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected a dialer with a connect timeout")
	}
	if transport.IdleConnTimeout != IdleConnTimeout {
		t.Fatalf("expected idle conn timeout %v, got %v", IdleConnTimeout, transport.IdleConnTimeout)
	}
}

// net/http's default of 2 idle connections per host throttles a gateway that
// fans out concurrent tool-augmented calls to one provider.
func TestNewTransportRaisesIdleConnsPerHost(t *testing.T) {
	transport := NewTransport()
	if transport.MaxIdleConnsPerHost != MaxIdleConnsPerHost {
		t.Fatalf("expected %d idle conns per host, got %d", MaxIdleConnsPerHost, transport.MaxIdleConnsPerHost)
	}
	if transport.MaxIdleConnsPerHost <= http.DefaultMaxIdleConnsPerHost {
		t.Fatalf("expected more than the net/http default of %d", http.DefaultMaxIdleConnsPerHost)
	}
}

func TestNewReturnsIndependentClients(t *testing.T) {
	first := New()
	second := New()
	if first == second {
		t.Fatal("expected independent clients so callers cannot mutate a shared default")
	}
	if first.Transport == second.Transport {
		t.Fatal("expected independent transports")
	}
	if first.Transport == http.DefaultTransport {
		t.Fatal("expected a dedicated transport rather than http.DefaultTransport")
	}
}
