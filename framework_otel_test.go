package agentflow_test

import (
	"context"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
)

func TestFrameworkRunWithOpenTelemetryTracer(t *testing.T) {
	ctx := context.Background()
	provider, err := agentflow.NewOpenTelemetryStdoutTracerProvider(ctx, agentflow.OpenTelemetryTracerProviderConfig{
		ServiceName:    "agentflow-test",
		ServiceVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = provider.Shutdown(ctx)
	}()

	tracer := agentflow.OpenTelemetryTracerFromProvider(provider, "test")
	fw, err := agentflow.New(testAutonomousScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithTracer(tracer),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(ctx, agentflow.RunRequest{RunID: "run-otel", Agent: "assistant", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "ok" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
}
