// Package httpclient supplies the default outbound HTTP client for adapters
// that talk to long-lived remote services (LLM providers, object storage).
package httpclient

import (
	"net"
	"net/http"
	"time"
)

const (
	// DialTimeout bounds establishing the TCP connection.
	DialTimeout = 10 * time.Second
	// TLSHandshakeTimeout bounds the TLS handshake.
	TLSHandshakeTimeout = 10 * time.Second
	// ResponseHeaderTimeout bounds how long a peer may accept the request and
	// then say nothing. It does not bound the response body, so a streaming
	// completion can keep delivering tokens for as long as the caller's
	// context allows.
	ResponseHeaderTimeout = 2 * time.Minute
	// IdleConnTimeout closes pooled connections that go unused.
	IdleConnTimeout = 90 * time.Second
	// MaxIdleConnsPerHost keeps connections warm for the handful of hosts a
	// gateway talks to, instead of net/http's default of 2.
	MaxIdleConnsPerHost = 32
)

// New returns a client for request/response services that are expected to
// start answering promptly, such as object storage.
//
// Client.Timeout is deliberately left unset. It bounds the whole exchange
// including reading the response body, so any finite value would truncate a
// large transfer mid-flight. The failure it is usually reached for — a peer
// that connects and then stalls — is covered by the transport timeouts above,
// and total duration belongs to the caller's context.
func New() *http.Client {
	return &http.Client{Transport: NewTransport()}
}

// NewTransport returns the transport used by New.
func NewTransport() *http.Transport {
	transport := baseTransport()
	transport.ResponseHeaderTimeout = ResponseHeaderTimeout
	return transport
}

// NewLongResponse returns a client for services whose time to first byte is
// legitimately unbounded.
//
// An LLM answering a non-streaming request sends no response headers until the
// completion is finished, so a response-header deadline cannot tell a model
// that is still generating apart from a peer that has stalled. Bounding it
// would silently cap how long a completion may take, which is the caller's
// decision (llm.Profile.Timeout, or the context it passes) rather than the
// transport's. Connection establishment is still bounded, since those
// deadlines cannot be confused with generation time.
func NewLongResponse() *http.Client {
	return &http.Client{Transport: NewLongResponseTransport()}
}

// NewLongResponseTransport returns the transport used by NewLongResponse.
func NewLongResponseTransport() *http.Transport {
	return baseTransport()
}

func baseTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   MaxIdleConnsPerHost,
		IdleConnTimeout:       IdleConnTimeout,
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
