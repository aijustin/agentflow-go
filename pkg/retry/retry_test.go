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
