package llm

import (
	"errors"
	"fmt"
	"testing"
)

func TestAPIErrorStringAndRetryable(t *testing.T) {
	withBody := APIError{Provider: "openai", Status: "429 Too Many Requests", StatusCode: 429, Body: "rate limited"}
	if withBody.Error() == "" || withBody.Error() == withBody.Status {
		t.Fatalf("unexpected error string: %q", withBody.Error())
	}
	if !withBody.Retryable() {
		t.Fatal("429 should be retryable")
	}
	withoutBody := APIError{Provider: "anthropic", Status: "400 Bad Request", StatusCode: 400}
	if withoutBody.Retryable() {
		t.Fatal("400 should not be retryable")
	}
	if withoutBody.Error() == "" {
		t.Fatal("expected error string")
	}
	serverErr := APIError{Provider: "local", Status: "503 Service Unavailable", StatusCode: 503}
	if !serverErr.Retryable() {
		t.Fatal("5xx should be retryable")
	}
}

func TestAPIErrorIsContextLengthExceeded(t *testing.T) {
	cases := []struct {
		name string
		err  APIError
		want bool
	}{
		{name: "openai code", err: APIError{Provider: "openai", StatusCode: 400, Code: "context_length_exceeded"}, want: true},
		{name: "code in body", err: APIError{Provider: "openai", StatusCode: 400, Body: `{"error":{"code":"context_length_exceeded"}}`}, want: true},
		{name: "maximum context length body", err: APIError{Provider: "openai", StatusCode: 400, Body: "This model's maximum context length is 8192 tokens"}, want: true},
		{name: "anthropic prompt too long", err: APIError{Provider: "anthropic", StatusCode: 400, Code: "invalid_request_error", Body: "prompt is too long: 213000 tokens > 200000 maximum"}, want: true},
		{name: "rate limit is not context", err: APIError{Provider: "openai", StatusCode: 429, Code: "rate_limit_exceeded", Body: "rate limited"}, want: false},
		{name: "plain 400 is not context", err: APIError{Provider: "openai", StatusCode: 400, Body: "invalid schema"}, want: false},
		{name: "server error is not context", err: APIError{Provider: "openai", StatusCode: 500}, want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.IsContextLengthExceeded(); got != tc.want {
				t.Fatalf("IsContextLengthExceeded()=%v want %v (err=%v)", got, tc.want, tc.err)
			}
			wrapped := fmt.Errorf("gateway call: %w", tc.err)
			if got := IsContextLengthExceeded(wrapped); got != tc.want {
				t.Fatalf("IsContextLengthExceeded(wrapped)=%v want %v", got, tc.want)
			}
		})
	}
	if IsContextLengthExceeded(errors.New("context_length_exceeded")) {
		t.Fatal("plain errors without an APIError in the chain must not classify")
	}
	if IsContextLengthExceeded(nil) {
		t.Fatal("nil error must not classify")
	}
}
