package otel

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

type Tracer struct{}

func NewTracer() Tracer {
	return Tracer{}
}

func (Tracer) Start(ctx context.Context, name observability.SpanName, attrs ...observability.Attribute) (context.Context, observability.Span) {
	traceID := randomID(16)
	spanID := randomID(8)
	ctx = observability.WithTrace(ctx, traceID, spanID)
	return ctx, span{name: string(name), attrs: attrs, started: time.Now().UTC()}
}

type span struct {
	name    string
	attrs   []observability.Attribute
	started time.Time
}

func (s span) RecordError(error) {}

func (s span) SetAttributes(attrs ...observability.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}

func (s span) End() {}

func randomID(size int) string {
	buf := make([]byte, size)
	if _, err := cryptorand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
