package llm

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCodeContextLengthExceeded is the provider error code signalling that the
// request exceeded the model's context window. OpenAI-compatible providers
// emit it verbatim; other providers only signal the condition through the
// error body, which IsContextLengthExceeded matches as a fallback.
const ErrCodeContextLengthExceeded = "context_length_exceeded"

type APIError struct {
	Provider   string
	StatusCode int
	Status     string
	Body       string
	// Code is the provider's machine-readable error code (e.g.
	// "context_length_exceeded", "rate_limit_error") when the response
	// carried one. It lets upstream recovery logic classify failures without
	// re-parsing Body.
	Code string
}

func (err APIError) Error() string {
	if err.Body != "" {
		return fmt.Sprintf("%s: unexpected status %s: %s", err.Provider, err.Status, err.Body)
	}
	return fmt.Sprintf("%s: unexpected status %s", err.Provider, err.Status)
}

func (err APIError) Retryable() bool {
	switch err.StatusCode {
	case 408, 409, 425, 429:
		return true
	case 400, 401, 403, 404, 422:
		return false
	default:
		return err.StatusCode >= 500
	}
}

// IsContextLengthExceeded reports whether the error is a provider context
// window overflow. Such failures are not retryable as-is, but they are
// recoverable: compacting the conversation and retrying can succeed, which
// the runtime does (once per run) before failing the run.
func (err APIError) IsContextLengthExceeded() bool {
	if err.Code == ErrCodeContextLengthExceeded {
		return true
	}
	body := strings.ToLower(err.Body)
	return strings.Contains(body, ErrCodeContextLengthExceeded) ||
		strings.Contains(body, "maximum context length") ||
		// Anthropic signals context overflow as a 400 invalid_request_error
		// whose message starts with "prompt is too long".
		strings.Contains(body, "prompt is too long")
}

// IsContextLengthExceeded unwraps err and reports whether any APIError in the
// chain is a provider context window overflow.
func IsContextLengthExceeded(err error) bool {
	var apiErr APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsContextLengthExceeded()
	}
	return false
}
