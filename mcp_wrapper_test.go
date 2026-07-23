package agentflow_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/adapters"
)

func TestNewMCPToolExecutorRequiresClient(t *testing.T) {
	if _, err := adapters.NewMCPToolExecutor(nil, "search"); err == nil {
		t.Fatal("expected client required error")
	}
}

func TestNewMCPStdioClientRequiresCommand(t *testing.T) {
	if _, err := adapters.NewMCPStdioClient(context.Background(), adapters.MCPStdioClientConfig{}); err == nil {
		t.Fatal("expected command required error")
	}
}
