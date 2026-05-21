package otel

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestNewStdoutTracerProvider(t *testing.T) {
	provider, err := NewStdoutTracerProvider(context.Background(), TracerProviderConfig{
		ServiceName:    "agentflow-test",
		ServiceVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = provider.Shutdown(context.Background())
	}()
	tracer := TracerFromProvider(provider, "test")
	if tracer == nil {
		t.Fatal("expected tracer")
	}
	_, span := tracer.Start(context.Background(), observability.SpanRun)
	span.End()
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
}
