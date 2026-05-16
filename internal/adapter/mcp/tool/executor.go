package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

type Executor struct {
	client  mcp.Client
	mcpTool string
}

func NewExecutor(client mcp.Client, mcpTool string) (*Executor, error) {
	if client == nil {
		return nil, fmt.Errorf("mcp tool: client is nil")
	}
	mcpTool = strings.TrimSpace(mcpTool)
	if mcpTool == "" {
		return nil, fmt.Errorf("mcp tool: tool name is required")
	}
	return &Executor{client: client, mcpTool: mcpTool}, nil
}

func (executor *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	args := call.Input
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	result, err := executor.client.CallTool(ctx, mcp.CallToolRequest{Name: executor.mcpTool, Arguments: args})
	if err != nil {
		return core.ToolResult{}, err
	}
	output, err := json.Marshal(result)
	if err != nil {
		return core.ToolResult{}, err
	}
	toolResult := core.ToolResult{Tool: call.Tool, Output: output}
	if result.IsError {
		toolResult.Error = resultText(result)
	}
	return toolResult, nil
}

func resultText(result mcp.CallToolResult) string {
	for _, content := range result.Content {
		if strings.TrimSpace(content.Text) != "" {
			return content.Text
		}
	}
	return "mcp tool returned an error"
}
