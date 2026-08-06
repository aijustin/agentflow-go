package runtime_test

import (
	"context"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
)

// advertiseCaptureGateway records the advertised tool names of every turn and
// answers immediately, so tests can assert which tools the model was shown.
type advertiseCaptureGateway struct {
	turns [][]string
}

func (advertiseCaptureGateway) Supports(string, llm.Capability) bool { return true }

func (g *advertiseCaptureGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *advertiseCaptureGateway) ChatWithTools(_ context.Context, _ string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	names := make([]string, len(req.Tools))
	for i, spec := range req.Tools {
		names[i] = spec.Name
	}
	g.turns = append(g.turns, names)
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	}, nil
}

func deferralScenario(agentTools []string, tools map[string]core.Tool) core.Scenario {
	return core.Scenario{
		Name: "deferral",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: agentTools},
		},
		Tools: tools,
	}
}

func runDeferralScenario(t *testing.T, scenario core.Scenario, catalog toolcatalog.Catalog) []string {
	t.Helper()
	gateway := &advertiseCaptureGateway{}
	engine, err := runtime.NewEngine(scenario, runtime.Dependencies{
		LLM:           gateway,
		Runs:          runstateinmem.NewRepository(),
		Tools:         stubToolRegistry{"docs.search": execTool{}, "docs.gated": execTool{}, "echo": execTool{}},
		ToolCatalog:   catalog,
		DeferredTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), runtime.RunRequest{RunID: "run-deferral", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(gateway.turns) == 0 {
		t.Fatal("gateway saw no turns")
	}
	return gateway.turns[0]
}

func containsTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// A configured DeferralPolicy fully advertises a small catalog (below
// MinTools, default 8): the meta-tool round-trip is not worth the savings.
func TestEngineDeferralPolicySmallCatalogAdvertisesAll(t *testing.T) {
	catalog := toolcatalog.NewSnapshotWithDeferral("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: "Search docs"},
	}, toolcatalog.DeferralPolicy{})
	scenario := deferralScenario(
		[]string{"docs.search"},
		map[string]core.Tool{"docs.search": {Name: "docs.search", Type: "mcp.tool"}},
	)
	first := runDeferralScenario(t, scenario, catalog)
	if !containsTool(first, "docs.search") {
		t.Fatalf("small catalog should advertise every tool, got %v", first)
	}
}

// Once the catalog reaches MinTools, deferral applies as before.
func TestEngineDeferralPolicyLargeCatalogStillDefers(t *testing.T) {
	catalog := toolcatalog.NewSnapshotWithDeferral("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: "Search docs"},
		{Name: "docs.gated", Description: "Gated"},
	}, toolcatalog.DeferralPolicy{MinTools: 2})
	scenario := deferralScenario(
		[]string{"docs.search", "docs.gated"},
		map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool"},
			"docs.gated":  {Name: "docs.gated", Type: "mcp.tool"},
		},
	)
	first := runDeferralScenario(t, scenario, catalog)
	if containsTool(first, "docs.search") {
		t.Fatalf("catalog at MinTools must stay deferred, got %v", first)
	}
}

// A small catalog whose schemas exceed MaxOverheadTokens stays deferred.
func TestEngineDeferralPolicyOverheadBudgetKeepsDeferral(t *testing.T) {
	bigDescription := make([]byte, 400)
	for i := range bigDescription {
		bigDescription[i] = 'x'
	}
	catalog := toolcatalog.NewSnapshotWithDeferral("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: string(bigDescription)},
	}, toolcatalog.DeferralPolicy{MaxOverheadTokens: 10})
	scenario := deferralScenario(
		[]string{"docs.search"},
		map[string]core.Tool{"docs.search": {Name: "docs.search", Type: "mcp.tool"}},
	)
	first := runDeferralScenario(t, scenario, catalog)
	if containsTool(first, "docs.search") {
		t.Fatalf("schema overhead above budget must keep deferral, got %v", first)
	}
}

// Approval-gated tools are never deferred, even under legacy unconditional
// deferral: the model must see the approval requirement with the schema.
func TestEngineApprovalGatedToolNeverDeferred(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: "Search docs"},
		{Name: "docs.gated", Description: "Gated"},
	})
	scenario := deferralScenario(
		[]string{"docs.search", "docs.gated"},
		map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool"},
			"docs.gated":  {Name: "docs.gated", Type: "mcp.tool", Approval: core.ApprovalAlways},
		},
	)
	first := runDeferralScenario(t, scenario, catalog)
	if !containsTool(first, "docs.gated") {
		t.Fatalf("approval-gated tool must never be deferred, got %v", first)
	}
	if containsTool(first, "docs.search") {
		t.Fatalf("plain catalog tool must stay deferred, got %v", first)
	}
}

// The catalog entry's own Approval field also forces advertisement when the
// scenario declaration carries none.
func TestEngineCatalogEntryApprovalNeverDeferred(t *testing.T) {
	catalog := toolcatalog.NewSnapshot("v1", time.Hour, []toolcatalog.Entry{
		{Name: "docs.search", Description: "Search docs", Approval: core.ApprovalRisky},
	})
	scenario := deferralScenario(
		[]string{"docs.search"},
		map[string]core.Tool{"docs.search": {Name: "docs.search", Type: "mcp.tool"}},
	)
	first := runDeferralScenario(t, scenario, catalog)
	if !containsTool(first, "docs.search") {
		t.Fatalf("catalog-entry approval policy must force advertisement, got %v", first)
	}
}
