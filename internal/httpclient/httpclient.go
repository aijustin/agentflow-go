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

// New returns a client tuned for streaming-capable provider calls.
//
// Client.Timeout is deliberately left unset. It bounds the whole exchange
// including reading the response body, so any finite value would truncate a
// long completion or a large object transfer mid-flight. The failure it is
// usually reached for — a peer that connects and then stalls — is covered by
// the transport timeouts above, and total duration belongs to the caller's
// context.
func New() *http.Client {
	return &http.Client{Transport: NewTransport()}
}

// NewTransport returns the transport used by New.
func NewTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   MaxIdleConnsPerHost,
		IdleConnTimeout:       IdleConnTimeout,
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		ResponseHeaderTimeout: ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
