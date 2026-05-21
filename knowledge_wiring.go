package agentflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// KnowledgeRegistry wires scenario knowledge collections to retriever executors.
type KnowledgeRegistry struct {
	Embedder llm.Embedder
	Store    knowledge.VectorStore
	Reranker knowledge.Reranker
}

// KnowledgeWiringOptions returns Framework options that bind scenario knowledge collections.
func KnowledgeWiringOptions(scenario core.Scenario, registry KnowledgeRegistry) ([]Option, error) {
	if registry.Embedder == nil || registry.Store == nil {
		return nil, fmt.Errorf("agentflow: knowledge registry requires embedder and store")
	}
	var opts []Option
	for _, collection := range scenario.Knowledge.Collections {
		toolName := strings.TrimSpace(collection.Tool)
		if toolName == "" {
			toolName = "knowledge." + collection.Name
		}
		if _, exists := scenario.Tools[toolName]; !exists {
			return nil, fmt.Errorf("agentflow: knowledge collection %q requires tool %q in scenario.tools", collection.Name, toolName)
		}
		profile := strings.TrimSpace(collection.EmbedProfile)
		if profile == "" {
			profile = firstEmbedProfile(scenario.LLMs)
		}
		if profile == "" {
			return nil, fmt.Errorf("agentflow: knowledge collection %q requires embed_profile", collection.Name)
		}
		mode := knowledge.SearchModeVector
		if strings.EqualFold(collection.SearchMode, "hybrid") {
			mode = knowledge.SearchModeHybrid
		}
		exec, err := NewRetrieverTool(RetrieverToolConfig{
			Embedder:     registry.Embedder,
			Store:        registry.Store,
			Profile:      profile,
			Namespace:    collection.Namespace,
			SearchMode:   mode,
			Reranker:     registry.Reranker,
			DefaultLimit: 5,
		})
		if err != nil {
			return nil, err
		}
		opts = append(opts, WithToolExecutor(toolName, &tenantScopedRetriever{
			inner:        exec,
			tenantScoped: collection.TenantScoped,
			namespace:    collection.Namespace,
		}))
	}
	return opts, nil
}

type tenantScopedRetriever struct {
	inner        core.ToolExecutor
	tenantScoped bool
	namespace    string
}

func (t *tenantScopedRetriever) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.tenantScoped {
		if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
			var payload map[string]any
			if len(call.Input) > 0 {
				_ = json.Unmarshal(call.Input, &payload)
			}
			if payload == nil {
				payload = map[string]any{}
			}
			if _, ok := payload["namespace"]; !ok {
				payload["namespace"] = tenantKnowledgeNamespace(t.namespace, principal.Scope.TenantID)
				raw, err := json.Marshal(payload)
				if err != nil {
					return core.ToolResult{}, err
				}
				call.Input = raw
			}
		}
	}
	return t.inner.Execute(ctx, call)
}

func tenantKnowledgeNamespace(base, tenantID string) string {
	base = strings.TrimSpace(base)
	tenantID = strings.TrimSpace(tenantID)
	if base == "" {
		return tenantID
	}
	if tenantID == "" {
		return base
	}
	return tenantID + "/" + base
}

func firstEmbedProfile(profiles map[string]core.LLMProfileRef) string {
	for name, profile := range profiles {
		for _, cap := range profile.Capabilities {
			if cap == "embed" {
				return name
			}
		}
	}
	for name := range profiles {
		return name
	}
	return ""
}
