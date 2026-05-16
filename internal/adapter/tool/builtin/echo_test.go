package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestEchoToolExecute(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "echoes provided input", input: []byte(`{"message":"hi"}`), want: `{"message":"hi"}`},
		{name: "defaults empty input", input: nil, want: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NewEchoTool().Execute(context.Background(), core.ToolCall{Tool: "echo", Input: tt.input})
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Output) != tt.want {
				t.Fatalf("got %s, want %s", result.Output, tt.want)
			}
		})
	}
}

func TestEchoToolHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewEchoTool().Execute(ctx, core.ToolCall{Tool: "echo"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
