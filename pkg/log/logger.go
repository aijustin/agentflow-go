package log

import "context"

// Logger is a minimal structured-logging interface for runtime warning and
// error paths. Implementations can wrap slog, zap, or other backends.
type Logger interface {
	Warn(ctx context.Context, msg string, keysAndValues ...any)
	Error(ctx context.Context, msg string, keysAndValues ...any)
}
