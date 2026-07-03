package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retryableErr struct{ msg string }

func (e retryableErr) Error() string   { return e.msg }
func (retryableErr) Retryable() bool   { return true }

func TestRetryable(t *testing.T) {
	ctx := context.Background()
	if Retryable(ctx, nil) {
		t.Fatal("nil error is not retryable")
	}
	if Retryable(ctx, errors.New("permanent")) {
		t.Fatal("unclassified error is not retryable")
	}
	if !Retryable(ctx, retryableErr{msg: "transient"}) {
		t.Fatal("classified retryable error should retry")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if Retryable(cancelled, retryableErr{msg: "transient"}) {
		t.Fatal("cancelled context should not retry")
	}
}

func TestBackoffRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Backoff(ctx, 1); err == nil {
		t.Fatal("expected context error")
	}
}

func TestBackoffCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Backoff(ctx, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRetryableRejectsDeadlineExceeded(t *testing.T) {
	ctx := context.Background()
	if Retryable(ctx, context.DeadlineExceeded) {
		t.Fatal("deadline exceeded should not retry")
	}
}

type nonRetryableErr struct{}

func (nonRetryableErr) Error() string   { return "permanent classified" }
func (nonRetryableErr) Retryable() bool { return false }

func TestRetryableHonorsFalseClassification(t *testing.T) {
	if Retryable(context.Background(), nonRetryableErr{}) {
		t.Fatal("classified non-retryable error should not retry")
	}
}

func TestBackoffUsesExponentialDelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := Backoff(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("expected backoff delay for attempt 4, got %v", elapsed)
	}
}
