package agentflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// Close releases resources registered through WithCloser or WithDatabase.
func (f *Framework) Close(ctx context.Context) error {
	if f == nil {
		return nil
	}
	var errs []error
	for i := len(f.closers) - 1; i >= 0; i-- {
		if err := f.closers[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
