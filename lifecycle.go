package agentflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WithCloser registers a function invoked by Framework.Close in LIFO order.
func WithCloser(fn func(context.Context) error) Option {
	return func(o *options) error {
		if fn == nil {
			return fmt.Errorf("agentflow: closer is nil")
		}
		o.closers = append(o.closers, fn)
		return nil
	}
}

// WithDatabase registers a database handle for automatic close on Framework.Close.
func WithDatabase(db *sql.DB) Option {
	return WithCloser(func(context.Context) error {
		if db == nil {
			return nil
		}
		return db.Close()
	})
}

// Close releases resources registered through WithCloser or WithDatabase and
// detaches every AttachRunStream subscriber of the stream hub. The event
// emission queue is drained first (bounded wait) so queued events reach the
// sink before any closer tears down the resources the sink depends on.
func (f *Framework) Close(ctx context.Context) error {
	if f == nil {
		return nil
	}
	f.streamHub.close()
	if emitter := f.currentEmitter(); emitter != nil {
		emitter.Close()
	}
	var errs []error
	for i := len(f.closers) - 1; i >= 0; i-- {
		closerCtx := ctx
		var cancel context.CancelFunc
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			closerCtx, cancel = context.WithTimeout(ctx, closerTimeout)
		}
		err := f.closers[i](closerCtx)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

const closerTimeout = 30 * time.Second
