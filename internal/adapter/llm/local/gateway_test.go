package local_test

import (
	"net/http"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/llm/local"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestNewGatewayConstructsOpenAICompatibleClient(t *testing.T) {
	gw := local.NewGateway([]llm.Profile{{Name: "local", Model: "llama3"}}, http.DefaultClient)
	if gw == nil {
		t.Fatal("expected gateway")
	}
	if !gw.Supports("local", llm.CapChat) {
		t.Fatal("expected chat support")
	}
}
