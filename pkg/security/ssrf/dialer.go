package ssrf

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Guard enforces the address policy at connection time.
//
// Checking a URL before handing it to an http.Client is inherently racy: the
// client performs its own DNS resolution, so a hostname that resolved to a
// public address during the check can resolve to 169.254.169.254 microseconds
// later (DNS rebinding). Following redirects has the same problem one hop out.
// Guard closes both holes by validating the concrete address the kernel is
// about to connect to, which every request and every redirect hop must pass
// through.
//
// The zero value permits loopback, matching IsBlockedIP, so local development
// and httptest servers keep working. Deployments that do not want agents
// reaching services on the pod itself should set BlockLoopback.
type Guard struct {
	BlockLoopback bool

	// DialTimeout bounds establishing the TCP connection. Zero uses
	// DefaultDialTimeout.
	DialTimeout time.Duration

	// KeepAlive configures the TCP keep-alive interval. Zero uses
	// DefaultKeepAlive.
	KeepAlive time.Duration
}

const (
	DefaultDialTimeout = 10 * time.Second
	DefaultKeepAlive   = 30 * time.Second
)

// CheckIP reports whether the guard permits connecting to ip.
func (g Guard) CheckIP(ip net.IP) error {
	if ip == nil {
		return ErrBlocked{}
	}
	if g.BlockLoopback && ip.IsLoopback() {
		return ErrBlocked{IP: ip}
	}
	if IsBlockedIP(ip) {
		return ErrBlocked{IP: ip}
	}
	return nil
}

// Control implements the net.Dialer Control hook. address is always a
// resolved "ip:port" literal at this point, never a hostname.
func (g Guard) Control(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return fmt.Errorf("ssrf: network %q is not permitted", network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf: invalid dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf: unresolved dial address %q", address)
	}
	return g.CheckIP(ip)
}

// Dialer returns a net.Dialer that refuses to connect to blocked addresses.
func (g Guard) Dialer() *net.Dialer {
	dialTimeout := g.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	keepAlive := g.KeepAlive
	if keepAlive <= 0 {
		keepAlive = DefaultKeepAlive
	}
	return &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive, Control: g.Control}
}

// ProtectTransport returns a copy of base whose dialer enforces the guard.
// A nil base starts from a clone of http.DefaultTransport.
func (g Guard) ProtectTransport(base *http.Transport) *http.Transport {
	var transport *http.Transport
	if base != nil {
		transport = base.Clone()
	} else if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{}
	}
	dialer := g.Dialer()
	transport.DialContext = dialer.DialContext
	// A pre-set DialTLSContext would bypass DialContext entirely, taking the
	// guard offline for https. Clearing it keeps TLS on the guarded dialer.
	transport.DialTLSContext = nil
	return transport
}

// ProtectClient returns a copy of client whose transport enforces the guard.
//
// It fails closed: a client carrying a custom http.RoundTripper cannot be
// guarded, because there is no way to reach the dialer underneath it, and
// silently returning an unguarded client would defeat the purpose.
func (g Guard) ProtectClient(client *http.Client) (*http.Client, error) {
	if client == nil {
		return &http.Client{Transport: g.ProtectTransport(nil)}, nil
	}
	guarded := *client
	switch transport := client.Transport.(type) {
	case nil:
		guarded.Transport = g.ProtectTransport(nil)
	case *http.Transport:
		guarded.Transport = g.ProtectTransport(transport)
	default:
		return nil, fmt.Errorf(
			"ssrf: cannot guard client transport of type %T: the address policy is enforced by the dialer, "+
				"which is unreachable behind a custom http.RoundTripper. Supply an *http.Transport, or keep the "+
				"wrapper and build it on Guard.ProtectTransport(nil) so its inner transport stays guarded",
			client.Transport)
	}
	return &guarded, nil
}
