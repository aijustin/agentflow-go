package builder_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestToolBuilderOptions(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	scenario := builder.New("tool-options").
		Tool("http", builder.HTTPTool()).
		Type("http.client").
		Description("call remote api").
		Approval(core.ApprovalAlways).
		SideEffect(builder.SideEffectWrite).
		LLM("chat").
		RateCap(3).
		InputSchema(schema).
		OutputSchema(schema).
		Metadata("team", "platform").
		Done().
		Agent("assistant").Instructions("go").Done().
		Autonomous().
		Scenario()
	tool := scenario.Tools["http"]
	if tool.Type != "http.client" || tool.Description != "call remote api" {
		t.Fatalf("unexpected tool: %+v", tool)
	}
	if tool.LLM != "chat" || tool.RateCap != 3 || tool.Approval != core.ApprovalAlways {
		t.Fatalf("unexpected tool policy: %+v", tool)
	}
	if tool.Metadata["team"] != "platform" {
		t.Fatalf("metadata=%+v", tool.Metadata)
	}
}
