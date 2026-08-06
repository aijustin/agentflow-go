package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
)

type loadedToolSet struct {
	mu    sync.Mutex
	names map[string]struct{}
}

func newLoadedToolSet() *loadedToolSet {
	return &loadedToolSet{names: make(map[string]struct{})}
}

func (s *loadedToolSet) add(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			s.names[name] = struct{}{}
		}
	}
}

func (s *loadedToolSet) contains(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.names[name]
	return ok
}

func (e *Engine) catalogEnabled() bool {
	return e.toolCatalog != nil
}

func (e *Engine) loadedToolsForRun(runID string) *loadedToolSet {
	set := newLoadedToolSet()
	actual, _ := e.loadedTools.LoadOrStore(runID, set)
	return actual.(*loadedToolSet)
}

func (e *Engine) isCatalogMetaTool(name string) bool {
	return name == toolcatalog.ToolSearchTools || name == toolcatalog.ToolLoadSchemas
}

func (e *Engine) isFrameworkMetaTool(name string) bool {
	return e.isCatalogMetaTool(name) || name == toolcatalog.ToolCompactContext
}

func (e *Engine) dispatchCatalogMetaTool(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall) (core.ToolResult, bool, error) {
	if result, handled, err := e.dispatchSelfCompactMetaTool(ctx, runID, agent, call); handled {
		return result, true, err
	}
	if !e.catalogEnabled() || !e.isCatalogMetaTool(call.Name) {
		return core.ToolResult{}, false, nil
	}
	switch call.Name {
	case toolcatalog.ToolSearchTools:
		var req struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(call.Input, &req); err != nil {
			return core.ToolResult{Tool: call.Name, Error: "invalid search_tools input: " + err.Error()}, true, nil
		}
		results := e.toolCatalog.Search(req.Query, req.Limit)
		payload, err := json.Marshal(map[string]any{
			"results": results,
			"version": e.toolCatalog.Version(),
			"ttl_ms":  e.toolCatalog.TTL().Milliseconds(),
		})
		if err != nil {
			return core.ToolResult{}, true, err
		}
		return core.ToolResult{Tool: call.Name, Output: payload}, true, nil
	case toolcatalog.ToolLoadSchemas:
		var req struct {
			Names []string `json:"names"`
		}
		if err := json.Unmarshal(call.Input, &req); err != nil {
			return core.ToolResult{Tool: call.Name, Error: "invalid load_tool_schemas input: " + err.Error()}, true, nil
		}
		entries, err := e.toolCatalog.Load(req.Names)
		if err != nil {
			return core.ToolResult{Tool: call.Name, Error: err.Error()}, true, nil
		}
		// Approval-gated tools loaded here still pass the regular dispatch-time
		// approval gates (approval cache, deny-without-gate, pause evaluation),
		// so no extra check is needed on this path; on the advertise side they
		// are never deferred at all (see shouldAdvertiseCatalogTool).
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name
		}
		e.loadedToolsForRun(runID).add(names...)
		payload, err := json.Marshal(map[string]any{"tools": entries})
		if err != nil {
			return core.ToolResult{}, true, err
		}
		return core.ToolResult{Tool: call.Name, Output: payload}, true, nil
	default:
		return core.ToolResult{}, false, fmt.Errorf("runtime: unknown catalog meta-tool %q", call.Name)
	}
}

func (e *Engine) isCatalogPinnedTool(agent core.Agent, name string) bool {
	if !agentAllowsTool(agent, name) {
		return false
	}
	if entries, err := e.toolCatalog.Load([]string{name}); err == nil && len(entries) == 1 {
		// Catalog membership is authoritative: Pin=false means deferred even
		// when the scenario declaration uses a builtin.* executor type (hosts
		// often register MCP tools as builtin.custom).
		return entries[0].Pin
	}
	tool, ok := e.scenario.Tools[name]
	if !ok {
		return false
	}
	return strings.HasPrefix(tool.Type, "builtin.")
}

func (e *Engine) catalogToolSpecs(ctx context.Context, runID string, agent core.Agent) []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(agent.Tools))
	for _, meta := range toolcatalog.MetaToolSpecs() {
		specs = append(specs, llm.ToolSpec{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
		})
	}
	specs = e.appendSelfCompactMetaToolSpecs(agent, specs)
	loaded := e.loadedToolsForRun(runID)
	deferral := e.catalogDeferralActive()
	for _, name := range agent.Tools {
		if !e.shouldAdvertiseCatalogTool(agent, name, loaded, deferral) {
			continue
		}
		spec, ok := e.catalogAdvertisedSpec(name)
		if !ok {
			continue
		}
		specs = append(specs, spec)
	}
	for _, name := range agent.SubAgents {
		sub, ok := e.scenario.Agents[name]
		if !ok {
			continue
		}
		description := sub.Description
		if description == "" {
			description = "Delegate a task to sub-agent " + name
		}
		specs = append(specs, llm.ToolSpec{
			Name:        delegateToolName(name),
			Description: description,
			Schema:      json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"context":{"type":"object"}},"required":["prompt"]}`),
		})
	}
	return specs
}

// catalogDeferralActive reports whether unpinned, unloaded catalog tools stay
// deferred for this engine. Without a configured DeferralPolicy the legacy
// behavior holds: deferral is unconditional once enabled. With a policy, small
// catalogs (below MinTools, default 8) are advertised in full unless their
// estimated schema overhead exceeds MaxOverheadTokens.
func (e *Engine) catalogDeferralActive() bool {
	if !e.deferredTools || e.toolCatalog == nil {
		return false
	}
	configured, ok := e.toolCatalog.(interface {
		DeferralConfig() (toolcatalog.DeferralPolicy, bool)
	})
	if !ok {
		return true
	}
	policy, ok := configured.DeferralConfig()
	if !ok {
		return true
	}
	sizer, ok := e.toolCatalog.(interface{ Size() int })
	if !ok {
		// Unknown catalog size: keep deferring (legacy behavior).
		return true
	}
	overhead := 0
	if estimator, ok := e.toolCatalog.(interface{ OverheadTokens() int }); ok {
		overhead = estimator.OverheadTokens()
	}
	return policy.ShouldDefer(sizer.Size(), overhead)
}

// approvalNeverDeferred reports whether a tool approval policy must always be
// advertised alongside its schema (cherry's defer:'never' equivalent): hiding
// an approval-gated tool behind load_tool_schemas would let the model invoke
// it without ever seeing that human approval is required.
func approvalNeverDeferred(policy core.ApprovalPolicy) bool {
	switch policy {
	case core.ApprovalRisky, core.ApprovalAlways, core.ApprovalPause:
		return true
	default:
		return false
	}
}

// catalogToolApproval resolves the effective approval policy for an agent
// tool: the scenario declaration wins, then the catalog entry.
func (e *Engine) catalogToolApproval(name string) core.ApprovalPolicy {
	if tool, ok := e.scenario.Tools[name]; ok && tool.Approval != "" {
		return tool.Approval
	}
	if entries, err := e.toolCatalog.Load([]string{name}); err == nil && len(entries) == 1 {
		return entries[0].Approval
	}
	return ""
}

func (e *Engine) shouldAdvertiseCatalogTool(agent core.Agent, name string, loaded *loadedToolSet, deferral bool) bool {
	if !deferral {
		return true
	}
	if e.isCatalogPinnedTool(agent, name) {
		return true
	}
	if approvalNeverDeferred(e.catalogToolApproval(name)) {
		return true
	}
	return loaded.contains(name)
}

func (e *Engine) catalogAdvertisedSpec(name string) (llm.ToolSpec, bool) {
	tool, ok := e.scenario.Tools[name]
	if !ok {
		if entries, err := e.toolCatalog.Load([]string{name}); err == nil && len(entries) == 1 {
			entry := entries[0]
			schema := entry.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			return llm.ToolSpec{Name: name, Description: entry.Description, Schema: schema}, true
		}
		return llm.ToolSpec{}, false
	}
	return llm.ToolSpec{Name: name, Description: tool.Description, Schema: tool.InputSchema}, true
}
