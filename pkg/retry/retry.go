// Package retry provides the retry classification and backoff shared by the
// autonomous runtime and the workflow orchestrator, so both apply the same
// semantics: only errors that explicitly classify themselves as retryable
// are re-attempted, with exponential backoff and jitter between attempts.
package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Retryable reports whether err is worth retrying. The caller's context must
// still be live, and the error (or any error in its chain) must classify
// itself as retryable via a `Retryable() bool` method (e.g. llm.APIError).
// Unclassified errors are treated as permanent: blindly re-running them
// would just repeat the failure - or worse, repeat a side effect.
func Retryable(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var classified interface{ Retryable() bool }
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	return false
}

// Backoff sleeps before the given 1-based retry attempt with exponential
// backoff (100ms base, 2s cap) and ±25% jitter, aborting early with the
// context error if ctx is done.
func Backoff(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond
	for range max(0, attempt-1) {
		delay *= 2
		if delay >= 2*time.Second {
			delay = 2 * time.Second
			break
		}
	}
	// Add ±25% jitter to prevent thundering-herd on concurrent retries.
	jitter := time.Duration(rand.N(int64(delay/2))) - delay/4
	delay += jitter
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
