package agentflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
)

// Compose tool names exposed to the composer agent. They operate on a
// per-call composeGraphBuilder; executors are injected into an ephemeral
// engine and never registered on the live framework.
const (
	composeToolListParts  = "compose_list_parts"
	composeToolAddAgent   = "compose_add_agent"
	composeToolAddSkill   = "compose_add_skill"
	composeToolAddNode    = "compose_add_node"
	composeToolRemoveNode = "compose_remove_node"
	composeToolSetInput   = "compose_set_input"
	composeToolConnect    = "compose_connect"
	composeToolDisconnect = "compose_disconnect"
	composeToolValidate   = "compose_validate"
	composeToolFinish     = "compose_finish"
)

// composeNodeKinds is the v1 whitelist of workflow node kinds the composer
// may generate. Input-heavy kinds (query_router, rag_grade, map, supervisor)
// are excluded: high failure rate for marginal v1 value.
var composeNodeKinds = map[string]bool{
	string(core.NodeAgent):         true,
	string(core.NodeTool):          true,
	string(core.NodeSkill):         true,
	string(core.NodeTransform):     true,
	string(core.NodeHumanGate):     true,
	string(core.NodeParallelGroup): true,
	string(core.NodeLoop):          true,
	string(core.NodeSubgraph):      true,
}

// composeGraphBuilder accumulates the draft a composer agent produces through
// the compose_* tools. It is not safe for concurrent use; a compose run is
// single-threaded by construction.
type composeGraphBuilder struct {
	mode       ComposeMode
	base       core.Scenario
	defaultLLM string

	agents     map[string]core.Agent
	skills     map[string]core.Skill
	nodes      map[string]graph.GraphNode
	order      []string
	edges      []graph.GraphEdge
	finish     bool
	finishMode string
}

func newComposeGraphBuilder(base core.Scenario, mode ComposeMode, defaultLLM string) *composeGraphBuilder {
	return &composeGraphBuilder{
		mode:       mode,
		base:       base,
		defaultLLM: defaultLLM,
		agents:     make(map[string]core.Agent),
		skills:     make(map[string]core.Skill),
		nodes:      make(map[string]graph.GraphNode),
	}
}

// finished reports whether the composer finalized a valid draft.
func (b *composeGraphBuilder) finished() bool { return b.finish }

// graphView renders the draft workflow in stable (insertion) order.
func (b *composeGraphBuilder) graphView() *graph.GraphView {
	view := &graph.GraphView{
		Nodes: make([]graph.GraphNode, 0, len(b.order)),
		Edges: append([]graph.GraphEdge(nil), b.edges...),
	}
	for _, id := range b.order {
		view.Nodes = append(view.Nodes, b.nodes[id])
	}
	return view
}

// patch renders the draft as a scenario patch (scenario mode).
func (b *composeGraphBuilder) patch() graph.ScenarioPatch {
	patch := graph.ScenarioPatch{
		Mode:     b.finishMode,
		Workflow: b.graphView(),
	}
	if len(b.agents) > 0 {
		patch.Agents = make(map[string]core.Agent, len(b.agents))
		for name, agent := range b.agents {
			patch.Agents[name] = agent
		}
	}
	if len(b.skills) > 0 {
		patch.Skills = make(map[string]core.Skill, len(b.skills))
		for name, skill := range b.skills {
			patch.Skills[name] = skill
		}
	}
	return patch
}

// scenarioGraph renders the draft as a catalog-mode graph edit.
func (b *composeGraphBuilder) scenarioGraph() graph.ScenarioGraph {
	return graph.ScenarioGraph{Mode: b.finishMode, Workflow: b.graphView()}
}

// appliedScenario merges the draft onto a deep copy of the base scenario.
func (b *composeGraphBuilder) appliedScenario() (core.Scenario, error) {
	if b.mode == ComposeModeScenario {
		return graph.ApplyScenarioPatch(b.base, b.patch())
	}
	base, err := graph.DeepCopyScenario(b.base)
	if err != nil {
		return core.Scenario{}, err
	}
	return graph.ApplyGraph(base, b.scenarioGraph())
}

// effectiveMode resolves the orchestration mode for the composed scenario:
// an explicit finish mode wins, otherwise the base mode is kept when it is
// runnable as a graph, otherwise a composed workflow implies fixed_workflow.
func (b *composeGraphBuilder) effectiveMode() core.OrchestrationMode {
	if b.finishMode != "" {
		return core.OrchestrationMode(b.finishMode)
	}
	switch b.base.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow, core.OrchestrationHybrid:
		return b.base.Orchestration.Mode
	default:
		return core.OrchestrationFixedWorkflow
	}
}

// validateDraft applies the draft onto the base and runs full scenario
// validation, returning a model-actionable error message on failure.
func (b *composeGraphBuilder) validateDraft() error {
	if len(b.order) == 0 {
		return fmt.Errorf("workflow has no nodes yet; add nodes with %s first", composeToolAddNode)
	}
	b.finishMode = string(b.effectiveMode())
	scenario, err := b.appliedScenario()
	if err != nil {
		return err
	}
	return ValidateScenario(scenario)
}

// --- tool implementations -------------------------------------------------

type composePart struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	New         bool   `json:"new,omitempty"`
}

func (b *composeGraphBuilder) listParts(input []byte) (any, error) {
	var args struct {
		Kind  string `json:"kind"`
		Query string `json:"query"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	match := func(name, description string) bool {
		if query == "" {
			return true
		}
		return strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(description), query)
	}
	want := func(kind string) bool { return args.Kind == "" || args.Kind == kind }
	out := map[string]any{}
	if want("agent") {
		parts := make([]composePart, 0, len(b.base.Agents)+len(b.agents))
		for name, agent := range b.base.Agents {
			if match(name, agent.Description) {
				parts = append(parts, composePart{Name: name, Description: agent.Description})
			}
		}
		for name, agent := range b.agents {
			if match(name, agent.Description) {
				parts = append(parts, composePart{Name: name, Description: agent.Description, New: true})
			}
		}
		sortParts(parts)
		out["agents"] = parts
	}
	if want("tool") {
		parts := make([]composePart, 0, len(b.base.Tools))
		for name, tool := range b.base.Tools {
			if match(name, tool.Description) {
				parts = append(parts, composePart{Name: name, Description: tool.Description})
			}
		}
		sortParts(parts)
		out["tools"] = parts
	}
	if want("skill") {
		parts := make([]composePart, 0, len(b.base.Skills)+len(b.skills))
		for name, skill := range b.base.Skills {
			if match(name, skill.Description) {
				parts = append(parts, composePart{Name: name, Description: skill.Description})
			}
		}
		for name, skill := range b.skills {
			if match(name, skill.Description) {
				parts = append(parts, composePart{Name: name, Description: skill.Description, New: true})
			}
		}
		sortParts(parts)
		out["skills"] = parts
	}
	if want("subgraph") {
		parts := make([]composePart, 0, len(b.base.Orchestration.Workflows))
		for name := range b.base.Orchestration.Workflows {
			if match(name, "") {
				parts = append(parts, composePart{Name: name})
			}
		}
		sortParts(parts)
		out["subgraphs"] = parts
	}
	return out, nil
}

func sortParts(parts []composePart) {
	sort.Slice(parts, func(i, j int) bool { return parts[i].Name < parts[j].Name })
}

func (b *composeGraphBuilder) addAgent(input []byte) (any, error) {
	if b.mode != ComposeModeScenario {
		return nil, fmt.Errorf("%s requires scenario mode; catalog mode may only reference existing agents", composeToolAddAgent)
	}
	var args struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Instructions string   `json:"instructions"`
		LLM          string   `json:"llm"`
		Tools        []string `json:"tools"`
		Skills       []string `json:"skills"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if _, exists := b.base.Agents[name]; exists {
		return nil, fmt.Errorf("agent %q already exists in the base scenario; overwriting existing parts is not allowed — pick a new name or reference it from a node", name)
	}
	if _, exists := b.agents[name]; exists {
		return nil, fmt.Errorf("agent %q already added in this draft", name)
	}
	if strings.TrimSpace(args.Instructions) == "" {
		return nil, fmt.Errorf("agent %q instructions are required", name)
	}
	llmName := strings.TrimSpace(args.LLM)
	if llmName == "" {
		llmName = b.defaultLLM
	}
	if _, ok := b.base.LLMs[llmName]; !ok {
		return nil, fmt.Errorf("agent %q references unknown llm %q; available: %s", name, llmName, sortedKeys(b.base.LLMs))
	}
	for _, tool := range args.Tools {
		if _, ok := b.base.Tools[tool]; !ok {
			return nil, fmt.Errorf("agent %q references unknown tool %q; available: %s", name, tool, sortedKeys(b.base.Tools))
		}
	}
	for _, skill := range args.Skills {
		if _, ok := b.base.Skills[skill]; ok {
			continue
		}
		if _, ok := b.skills[skill]; !ok {
			return nil, fmt.Errorf("agent %q references unknown skill %q; add it first with %s", name, skill, composeToolAddSkill)
		}
	}
	b.agents[name] = core.Agent{
		Name:         name,
		Description:  strings.TrimSpace(args.Description),
		Instructions: args.Instructions,
		LLM:          llmName,
		Tools:        args.Tools,
		Skills:       args.Skills,
	}
	return map[string]any{"added": name}, nil
}

func (b *composeGraphBuilder) addSkill(input []byte) (any, error) {
	if b.mode != ComposeModeScenario {
		return nil, fmt.Errorf("%s requires scenario mode; catalog mode may only reference existing skills", composeToolAddSkill)
	}
	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, fmt.Errorf("skill name is required")
	}
	if _, exists := b.base.Skills[name]; exists {
		return nil, fmt.Errorf("skill %q already exists in the base scenario; overwriting existing parts is not allowed", name)
	}
	if _, exists := b.skills[name]; exists {
		return nil, fmt.Errorf("skill %q already added in this draft", name)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return nil, fmt.Errorf("skill %q prompt is required", name)
	}
	b.skills[name] = core.Skill{
		Name:            name,
		Description:     strings.TrimSpace(args.Description),
		Kind:            core.SkillKindPrompt,
		PromptFragments: []core.PromptFragment{{Content: args.Prompt}},
	}
	return map[string]any{"added": name}, nil
}

func (b *composeGraphBuilder) addNode(input []byte) (any, error) {
	var args struct {
		ID        string          `json:"id"`
		Kind      string          `json:"kind"`
		Ref       string          `json:"ref"`
		Input     json.RawMessage `json:"input"`
		Condition string          `json:"condition"`
		DependsOn []string        `json:"depends_on"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return nil, fmt.Errorf("node id is required")
	}
	if _, exists := b.nodes[id]; exists {
		return nil, fmt.Errorf("node %q already exists; use %s to replace its input or %s first", id, composeToolSetInput, composeToolRemoveNode)
	}
	kind := strings.TrimSpace(args.Kind)
	if !composeNodeKinds[kind] {
		return nil, fmt.Errorf("node kind %q is not allowed; allowed kinds: %s", kind, sortedKeys(composeNodeKinds))
	}
	ref := strings.TrimSpace(args.Ref)
	if err := b.checkNodeRef(id, kind, ref); err != nil {
		return nil, err
	}
	for _, dep := range args.DependsOn {
		if _, ok := b.nodes[dep]; !ok {
			return nil, fmt.Errorf("node %q depends_on unknown node %q; add it first", id, dep)
		}
	}
	if len(args.Input) > 0 && !json.Valid(args.Input) {
		return nil, fmt.Errorf("node %q input is not valid JSON", id)
	}
	b.nodes[id] = graph.GraphNode{
		ID:        id,
		Kind:      kind,
		Ref:       ref,
		Input:     args.Input,
		Condition: strings.TrimSpace(args.Condition),
		DependsOn: args.DependsOn,
	}
	b.order = append(b.order, id)
	return map[string]any{"added": id}, nil
}

func (b *composeGraphBuilder) checkNodeRef(id, kind, ref string) error {
	require := func(what string, lookup func(string) bool, available string) error {
		if ref == "" {
			return fmt.Errorf("node %q kind %q requires a %s ref", id, kind, what)
		}
		if !lookup(ref) {
			return fmt.Errorf("node %q references unknown %s %q; available: %s", id, what, ref, available)
		}
		return nil
	}
	switch core.WorkflowNodeKind(kind) {
	case core.NodeAgent:
		return require("agent", func(name string) bool {
			if _, ok := b.base.Agents[name]; ok {
				return true
			}
			_, ok := b.agents[name]
			return ok
		}, b.availableAgents())
	case core.NodeTool:
		return require("tool", func(name string) bool {
			_, ok := b.base.Tools[name]
			return ok
		}, sortedKeys(b.base.Tools))
	case core.NodeSkill:
		return require("skill", func(name string) bool {
			if _, ok := b.base.Skills[name]; ok {
				return true
			}
			_, ok := b.skills[name]
			return ok
		}, b.availableSkills())
	case core.NodeSubgraph:
		return require("subgraph", func(name string) bool {
			_, ok := b.base.Orchestration.Workflows[name]
			return ok
		}, sortedKeys(b.base.Orchestration.Workflows))
	}
	return nil
}

func (b *composeGraphBuilder) availableAgents() string {
	names := make([]string, 0, len(b.base.Agents)+len(b.agents))
	for name := range b.base.Agents {
		names = append(names, name)
	}
	for name := range b.agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (b *composeGraphBuilder) availableSkills() string {
	names := make([]string, 0, len(b.base.Skills)+len(b.skills))
	for name := range b.base.Skills {
		names = append(names, name)
	}
	for name := range b.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (b *composeGraphBuilder) removeNode(input []byte) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(args.ID)
	if _, ok := b.nodes[id]; !ok {
		return nil, fmt.Errorf("node %q does not exist", id)
	}
	delete(b.nodes, id)
	for i, existing := range b.order {
		if existing == id {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
	kept := b.edges[:0]
	for _, edge := range b.edges {
		if edge.From != id && edge.To != id {
			kept = append(kept, edge)
		}
	}
	b.edges = kept
	for nodeID, node := range b.nodes {
		if len(node.DependsOn) == 0 {
			continue
		}
		deps := node.DependsOn[:0]
		for _, dep := range node.DependsOn {
			if dep != id {
				deps = append(deps, dep)
			}
		}
		node.DependsOn = deps
		b.nodes[nodeID] = node
	}
	return map[string]any{"removed": id}, nil
}

func (b *composeGraphBuilder) setInput(input []byte) (any, error) {
	var args struct {
		ID    string          `json:"id"`
		Input json.RawMessage `json:"input"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	node, ok := b.nodes[strings.TrimSpace(args.ID)]
	if !ok {
		return nil, fmt.Errorf("node %q does not exist", args.ID)
	}
	if len(args.Input) > 0 && !json.Valid(args.Input) {
		return nil, fmt.Errorf("input for node %q is not valid JSON", args.ID)
	}
	node.Input = args.Input
	b.nodes[node.ID] = node
	return map[string]any{"updated": node.ID}, nil
}

func (b *composeGraphBuilder) connect(input []byte) (any, error) {
	var args struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Condition string `json:"condition"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	from, to := strings.TrimSpace(args.From), strings.TrimSpace(args.To)
	if _, ok := b.nodes[from]; !ok {
		return nil, fmt.Errorf("edge source node %q does not exist", from)
	}
	if _, ok := b.nodes[to]; !ok {
		return nil, fmt.Errorf("edge target node %q does not exist", to)
	}
	for _, edge := range b.edges {
		if edge.From == from && edge.To == to {
			return nil, fmt.Errorf("edge %q -> %q already exists", from, to)
		}
	}
	if b.pathExists(to, from) {
		return nil, fmt.Errorf("edge %q -> %q would create a cycle", from, to)
	}
	b.edges = append(b.edges, graph.GraphEdge{From: from, To: to, Condition: strings.TrimSpace(args.Condition)})
	return map[string]any{"connected": from + " -> " + to}, nil
}

func (b *composeGraphBuilder) disconnect(input []byte) (any, error) {
	var args struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	kept := b.edges[:0]
	removed := false
	for _, edge := range b.edges {
		if edge.From == args.From && edge.To == args.To {
			removed = true
			continue
		}
		kept = append(kept, edge)
	}
	b.edges = kept
	if !removed {
		return nil, fmt.Errorf("edge %q -> %q does not exist", args.From, args.To)
	}
	return map[string]any{"disconnected": args.From + " -> " + args.To}, nil
}

// pathExists reports whether target is reachable from start over edges and
// depends_on links (both express execution order).
func (b *composeGraphBuilder) pathExists(start, target string) bool {
	adj := make(map[string][]string, len(b.nodes))
	for _, edge := range b.edges {
		adj[edge.From] = append(adj[edge.From], edge.To)
	}
	for _, node := range b.nodes {
		for _, dep := range node.DependsOn {
			adj[dep] = append(adj[dep], node.ID)
		}
	}
	stack := []string{start}
	visited := map[string]bool{}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == target {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		stack = append(stack, adj[current]...)
	}
	return false
}

func (b *composeGraphBuilder) validateDraftTool() (any, error) {
	if err := b.validateDraft(); err != nil {
		return nil, err
	}
	return map[string]any{
		"valid": true,
		"nodes": len(b.order),
		"edges": len(b.edges),
		"mode":  b.finishMode,
	}, nil
}

func (b *composeGraphBuilder) finishDraft(input []byte) (any, error) {
	var args struct {
		Mode string `json:"mode"`
	}
	if err := unmarshalComposeInput(input, &args); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(args.Mode) {
	case "":
	case string(core.OrchestrationFixedWorkflow), string(core.OrchestrationHybrid):
		b.finishMode = strings.TrimSpace(args.Mode)
	default:
		return nil, fmt.Errorf("mode %q is unsupported; composed graphs run as %q or %q", args.Mode, core.OrchestrationFixedWorkflow, core.OrchestrationHybrid)
	}
	if err := b.validateDraft(); err != nil {
		return nil, err
	}
	b.finish = true
	return map[string]any{"finished": true, "mode": b.finishMode}, nil
}

// --- tool wiring ------------------------------------------------------------

// toolExecutors returns the compose tool executors bound to this builder.
// Model-correctable failures (unknown refs, cycles, invalid drafts) are
// returned as ToolResult.Error so the engine feeds them back to the composer
// instead of aborting the run — the compile-feedback loop.
func (b *composeGraphBuilder) toolExecutors() map[string]core.ToolExecutor {
	bind := func(fn func([]byte) (any, error)) core.ToolExecutor {
		return composeToolFunc(func(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
			output, err := fn(call.Input)
			if err != nil {
				return core.ToolResult{Tool: call.Tool, Error: err.Error()}, nil
			}
			raw, err := json.Marshal(output)
			if err != nil {
				return core.ToolResult{Tool: call.Tool, Error: err.Error()}, nil
			}
			return core.ToolResult{Tool: call.Tool, Output: raw}, nil
		})
	}
	executors := map[string]core.ToolExecutor{
		composeToolListParts:  bind(b.listParts),
		composeToolAddNode:    bind(b.addNode),
		composeToolRemoveNode: bind(b.removeNode),
		composeToolSetInput:   bind(b.setInput),
		composeToolConnect:    bind(b.connect),
		composeToolDisconnect: bind(b.disconnect),
		composeToolValidate:   bind(func([]byte) (any, error) { return b.validateDraftTool() }),
		composeToolFinish:     bind(b.finishDraft),
	}
	if b.mode == ComposeModeScenario {
		executors[composeToolAddAgent] = bind(b.addAgent)
		executors[composeToolAddSkill] = bind(b.addSkill)
	}
	return executors
}

// toolManifests declares the compose tools for the temporary composer
// scenario. Declarations stay in sync with toolExecutors.
func composeToolManifests(mode ComposeMode) map[string]core.Tool {
	manifests := map[string]core.Tool{
		composeToolListParts: {
			Name:        composeToolListParts,
			Type:        "compose",
			Description: "List catalog parts (agents, tools, skills, subgraphs) available for composition, with optional kind filter and substring query.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["agent","tool","skill","subgraph"]},"query":{"type":"string"}}}`),
		},
		composeToolAddNode: {
			Name:        composeToolAddNode,
			Type:        "compose",
			Description: "Add a workflow node. kind must be one of agent, tool, skill, transform, human_gate, parallel_group, loop, subgraph. ref must name an existing (or newly added) part.",
			InputSchema: json.RawMessage(`{"type":"object","required":["id","kind"],"properties":{"id":{"type":"string"},"kind":{"type":"string"},"ref":{"type":"string"},"input":{"type":"object"},"condition":{"type":"string"},"depends_on":{"type":"array","items":{"type":"string"}}}}`),
		},
		composeToolRemoveNode: {
			Name:        composeToolRemoveNode,
			Type:        "compose",
			Description: "Remove a workflow node and its incident edges.",
			InputSchema: json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`),
		},
		composeToolSetInput: {
			Name:        composeToolSetInput,
			Type:        "compose",
			Description: "Replace a node's input JSON (e.g. transform set spec, loop body, parallel_group refs).",
			InputSchema: json.RawMessage(`{"type":"object","required":["id","input"],"properties":{"id":{"type":"string"},"input":{"type":"object"}}}`),
		},
		composeToolConnect: {
			Name:        composeToolConnect,
			Type:        "compose",
			Description: "Connect two nodes with a directed edge. Optional condition uses exists/missing/eq/ne over steps.<id>... paths. Cycles are rejected.",
			InputSchema: json.RawMessage(`{"type":"object","required":["from","to"],"properties":{"from":{"type":"string"},"to":{"type":"string"},"condition":{"type":"string"}}}`),
		},
		composeToolDisconnect: {
			Name:        composeToolDisconnect,
			Type:        "compose",
			Description: "Remove an edge between two nodes.",
			InputSchema: json.RawMessage(`{"type":"object","required":["from","to"],"properties":{"from":{"type":"string"},"to":{"type":"string"}}}`),
		},
		composeToolValidate: {
			Name:        composeToolValidate,
			Type:        "compose",
			Description: "Validate the current draft against the full scenario rules (DAG, refs, kind rules). Returns the validation error to fix, or a summary when valid.",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		composeToolFinish: {
			Name:        composeToolFinish,
			Type:        "compose",
			Description: "Finalize the composed graph. Runs full validation; on success the graph is handed back to the host. Optional mode: fixed_workflow or hybrid.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["fixed_workflow","hybrid"]}}}`),
		},
	}
	if mode == ComposeModeScenario {
		manifests[composeToolAddAgent] = core.Tool{
			Name:        composeToolAddAgent,
			Type:        "compose",
			Description: "Add a NEW agent (scenario mode). Name must not collide with existing agents. instructions are required; tools/skills must reference existing or newly added parts.",
			InputSchema: json.RawMessage(`{"type":"object","required":["name","instructions"],"properties":{"name":{"type":"string"},"description":{"type":"string"},"instructions":{"type":"string"},"llm":{"type":"string"},"tools":{"type":"array","items":{"type":"string"}},"skills":{"type":"array","items":{"type":"string"}}}}`),
		}
		manifests[composeToolAddSkill] = core.Tool{
			Name:        composeToolAddSkill,
			Type:        "compose",
			Description: "Add a NEW prompt skill (scenario mode). Name must not collide with existing skills.",
			InputSchema: json.RawMessage(`{"type":"object","required":["name","prompt"],"properties":{"name":{"type":"string"},"description":{"type":"string"},"prompt":{"type":"string"}}}`),
		}
	}
	return manifests
}

type composeToolFunc func(ctx context.Context, call core.ToolCall) (core.ToolResult, error)

func (fn composeToolFunc) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return fn(ctx, call)
}

func unmarshalComposeInput(input []byte, args any) error {
	if len(input) == 0 {
		return nil
	}
	if err := json.Unmarshal(input, args); err != nil {
		return fmt.Errorf("invalid tool input JSON: %w", err)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
