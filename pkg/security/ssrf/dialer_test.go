package ssrf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGuardControlRejectsLinkLocalMetadataAddress(t *testing.T) {
	guard := Guard{}
	err := guard.Control("tcp", "169.254.169.254:80", nil)
	if err == nil {
		t.Fatal("expected cloud metadata address to be blocked at dial time")
	}
	var blocked ErrBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("expected ErrBlocked, got %T: %v", err, err)
	}
}

func TestGuardControlRejectsPrivateRanges(t *testing.T) {
	for _, address := range []string{
		"10.1.2.3:443",
		"172.16.0.9:80",
		"192.168.1.1:80",
		"100.64.0.1:80",
		"[fc00::1]:80",
	} {
		if err := (Guard{}).Control("tcp", address, nil); err == nil {
			t.Fatalf("expected %s to be blocked", address)
		}
	}
}

func TestGuardControlAllowsPublicAddress(t *testing.T) {
	if err := (Guard{}).Control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("expected public address to be allowed, got %v", err)
	}
}

func TestGuardControlLoopbackFollowsPolicy(t *testing.T) {
	if err := (Guard{}).Control("tcp", "127.0.0.1:8080", nil); err != nil {
		t.Fatalf("expected loopback allowed by default, got %v", err)
	}
	if err := (Guard{BlockLoopback: true}).Control("tcp", "127.0.0.1:8080", nil); err == nil {
		t.Fatal("expected loopback blocked when BlockLoopback is set")
	}
}

func TestGuardControlRejectsNonTCPNetwork(t *testing.T) {
	if err := (Guard{}).Control("udp", "93.184.216.34:53", nil); err == nil {
		t.Fatal("expected non-TCP network to be rejected")
	}
}

func TestGuardControlRejectsUnresolvedAddress(t *testing.T) {
	if err := (Guard{}).Control("tcp", "example.com:80", nil); err == nil {
		t.Fatal("expected unresolved hostname to be rejected")
	}
}

// The guard must survive a redirect into a blocked range, which is the hole
// that a pre-flight URL check cannot close.
func TestProtectClientBlocksRedirectIntoBlockedRange(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer origin.Close()

	client, err := Guard{}.ProtectClient(&http.Client{})
	if err != nil {
		t.Fatalf("protect client: %v", err)
	}
	resp, err := client.Get(origin.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect to metadata endpoint to fail at dial time")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}
}

// A hostname is not a literal IP, so a pre-flight URL check waves it through.
// Only the dial-time guard sees the address it actually resolves to, which is
// what makes it resistant to a rebind between check and connect.
func TestProtectClientBlocksHostnameResolvingToBlockedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	target := "http://localhost:" + strconv.Itoa(port) + "/"

	// The pre-flight literal-IP check cannot see through the hostname.
	if err := CheckURLHost(target); err != nil {
		t.Fatalf("expected hostname to pass the literal-IP check, got %v", err)
	}

	client, err := Guard{BlockLoopback: true}.ProtectClient(nil)
	if err != nil {
		t.Fatalf("protect client: %v", err)
	}
	resp, err := client.Get(target)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected hostname resolving to loopback to be blocked at dial time")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}
}

func TestProtectClientFailsClosedOnOpaqueTransport(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}
	if _, err := (Guard{}).ProtectClient(client); err == nil {
		t.Fatal("expected an unguardable transport to be rejected")
	}
}

func TestProtectClientPreservesClientSettings(t *testing.T) {
	base := &http.Client{Timeout: 42}
	guarded, err := (Guard{}).ProtectClient(base)
	if err != nil {
		t.Fatalf("protect client: %v", err)
	}
	if guarded.Timeout != 42 {
		t.Fatalf("expected timeout preserved, got %v", guarded.Timeout)
	}
	if guarded == base {
		t.Fatal("expected a copy, not the caller's client")
	}
	if base.Transport != nil {
		t.Fatal("expected the caller's client to be left untouched")
	}
}

// DialTLSContext bypasses DialContext, so leaving it set would silently take
// the guard offline for https.
func TestProtectTransportClearsDialTLSContext(t *testing.T) {
	base := &http.Transport{}
	base.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) { return nil, nil }
	guarded := (Guard{}).ProtectTransport(base)
	if guarded.DialTLSContext != nil {
		t.Fatal("expected DialTLSContext to be cleared so TLS dials stay guarded")
	}
	if guarded.DialContext == nil {
		t.Fatal("expected guarded DialContext to be installed")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
