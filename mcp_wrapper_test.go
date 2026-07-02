package agentflow_test

import (
	"context"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
)

func TestNewMCPToolExecutorRequiresClient(t *testing.T) {
	if _, err := agentflow.NewMCPToolExecutor(nil, "search"); err == nil {
		t.Fatal("expected client required error")
	}
}

func TestNewMCPStdioClientRequiresCommand(t *testing.T) {
	if _, err := agentflow.NewMCPStdioClient(context.Background(), agentflow.MCPStdioClientConfig{}); err == nil {
		t.Fatal("expected command required error")
	}
}
