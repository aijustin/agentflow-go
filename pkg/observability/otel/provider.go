package otel

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TracerProviderConfig configures a default OpenTelemetry TracerProvider for host applications.
type TracerProviderConfig struct {
	ServiceName    string
	ServiceVersion string
}

// NewStdoutTracerProvider creates a TracerProvider that exports spans to stdout.
func NewStdoutTracerProvider(ctx context.Context, cfg TracerProviderConfig) (*sdktrace.TracerProvider, error) {
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return nil, errors.New("otel: service name is required")
	}
	exporter, err := stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider, nil
}

// TracerFromProvider returns an agentflow tracer backed by the given provider.
func TracerFromProvider(provider *sdktrace.TracerProvider, instrumentationName string) *Tracer {
	if provider == nil {
		return nil
	}
	if strings.TrimSpace(instrumentationName) == "" {
		instrumentationName = "github.com/aijustin/agentflow-go"
	}
	return NewTracer(provider.Tracer(instrumentationName))
}
