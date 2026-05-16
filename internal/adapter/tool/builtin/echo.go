package builtin

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type EchoTool struct{}

func NewEchoTool() EchoTool {
	return EchoTool{}
}

func (EchoTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	payload := call.Input
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return core.ToolResult{Tool: call.Tool, Output: payload}, nil
}
