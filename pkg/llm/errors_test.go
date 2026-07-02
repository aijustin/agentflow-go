package llm

import "testing"

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
