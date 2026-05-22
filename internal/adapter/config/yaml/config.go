package yaml

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type Document struct {
	Scenario Scenario `yaml:"scenario"`
}

type Scenario struct {
	Name          string                `yaml:"name"`
	Description   string                `yaml:"description"`
	LLMs          map[string]LLMProfile `yaml:"llms"`
	Memories      map[string]Memory     `yaml:"memories"`
	Knowledge     Knowledge             `yaml:"knowledge"`
	MCP           MCP                   `yaml:"mcp"`
	Tools         map[string]Tool       `yaml:"tools"`
	Skills        map[string]Skill      `yaml:"skills"`
	Agents        map[string]Agent      `yaml:"agents"`
	Triggers      []Trigger             `yaml:"triggers"`
	Orchestration Orchestration         `yaml:"orchestration"`
	Runtime       Runtime               `yaml:"runtime"`
}

type LLMProfile struct {
	Provider            string               `yaml:"provider"`
	Model               string               `yaml:"model"`
	Endpoint            string               `yaml:"endpoint"`
	APIKeyEnv           string               `yaml:"api_key_env"`
	ContextWindowTokens int                  `yaml:"context_window_tokens"`
	MaxOutputTokens     int                  `yaml:"max_output_tokens"`
	Temperature         *float32             `yaml:"temperature"`
	TopP                *float32             `yaml:"top_p"`
	Timeout             time.Duration        `yaml:"timeout"`
	Thinking            llm.ThinkingConfig   `yaml:"thinking"`
	ReasoningEffort     string               `yaml:"reasoning_effort"`
	Context             contextwindow.Policy `yaml:"context"`
	ExtraBody           map[string]any       `yaml:"extra_body"`
	Capabilities        []string             `yaml:"capabilities"`
	Metadata            map[string]string    `yaml:"metadata"`
}

type Memory struct {
	Type      string            `yaml:"type"`
	Scope     string            `yaml:"scope"`
	Namespace string            `yaml:"namespace"`
	Metadata  map[string]string `yaml:"metadata"`
	Tiers     *MemoryTiers      `yaml:"tiers"`
}

type MemoryTiers struct {
	Enabled       bool                `yaml:"enabled"`
	HotCapacity   int                 `yaml:"hot_capacity"`
	WarmCapacity  int                 `yaml:"warm_capacity"`
	ColdCapacity  int                 `yaml:"cold_capacity"`
	HotTTL        time.Duration       `yaml:"hot_ttl"`
	WarmTTL       time.Duration       `yaml:"warm_ttl"`
	PromoteAccess int                 `yaml:"promote_access"`
	DemoteIdle    time.Duration       `yaml:"demote_idle"`
	RecallBudget  MemoryRecallBudget  `yaml:"recall_budget"`
	RecallWeights MemoryRecallWeights `yaml:"recall_weights"`
}

type MemoryRecallWeights struct {
	Semantic   float64 `yaml:"semantic"`
	Recency    float64 `yaml:"recency"`
	Importance float64 `yaml:"importance"`
}

type MemoryRecallBudget struct {
	Total int `yaml:"total"`
	Hot   int `yaml:"hot"`
	Warm  int `yaml:"warm"`
	Cold  int `yaml:"cold"`
}

type Tool struct {
	Type         string            `yaml:"type"`
	Description  string            `yaml:"description"`
	InputSchema  map[string]any    `yaml:"input_schema"`
	OutputSchema map[string]any    `yaml:"output_schema"`
	SideEffect   string            `yaml:"side_effect"`
	Approval     string            `yaml:"approval"`
	LLM          string            `yaml:"llm"`
	RateCap      int               `yaml:"rate_cap"`
	Metadata     map[string]string `yaml:"metadata"`
}

type Skill struct {
	Source           string            `yaml:"source"`
	Description      string            `yaml:"description"`
	Version          string            `yaml:"version"`
	CompatibleAgents []string          `yaml:"compatible_agents"`
	PromptFragments  []PromptFragment  `yaml:"prompt_fragments"`
	AgentPolicy      AgentPolicy       `yaml:"agent_policy"`
	ToolPolicies     []SkillToolPolicy `yaml:"tool_policies"`
	Workflow         *Workflow         `yaml:"workflow"`
	Metadata         map[string]string `yaml:"metadata"`
}

type PromptFragment struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

type SkillToolPolicy struct {
	Tool       string `yaml:"tool"`
	Approval   string `yaml:"approval"`
	SideEffect string `yaml:"side_effect"`
	RateCap    int    `yaml:"rate_cap"`
}

type AgentPolicy struct {
	MaxSteps         int            `yaml:"max_steps"`
	Timeout          time.Duration  `yaml:"timeout"`
	RetryLimit       int            `yaml:"retry_limit"`
	OutputSchema     map[string]any `yaml:"output_schema"`
	HumanCheckpoints []string       `yaml:"human_checkpoints"`
}

type Agent struct {
	Description      string            `yaml:"description"`
	Role             string            `yaml:"role"`
	Instructions     string            `yaml:"instructions"`
	LLM              string            `yaml:"llm"`
	Memory           string            `yaml:"memory"`
	Tools            []string          `yaml:"tools"`
	Skills           []string          `yaml:"skills"`
	SubAgents        []string          `yaml:"sub_agents"`
	MaxSteps         int               `yaml:"max_steps"`
	Timeout          time.Duration     `yaml:"timeout"`
	RetryLimit       int               `yaml:"retry_limit"`
	OutputSchema     map[string]any    `yaml:"output_schema"`
	HumanCheckpoints []string          `yaml:"human_checkpoints"`
	Metadata         map[string]string `yaml:"metadata"`
}

type Orchestration struct {
	Mode        string            `yaml:"mode"`
	Workflow    *Workflow         `yaml:"workflow"`
	MaxParallel int               `yaml:"max_parallel"`
	HumanInLoop HumanInLoopPolicy `yaml:"human_in_loop"`
	Planning    PlanningPolicy    `yaml:"planning"`
}

type PlanningPolicy struct {
	Enabled         bool   `yaml:"enabled"`
	Agent           string `yaml:"agent"`
	MaxSteps        int    `yaml:"max_steps"`
	Execute         bool   `yaml:"execute"`
	ReplanOnFailure bool   `yaml:"replan_on_failure"`
}

type Knowledge struct {
	Collections []KnowledgeCollection `yaml:"collections"`
}

type KnowledgeCollection struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Namespace    string   `yaml:"namespace"`
	Tool         string   `yaml:"tool"`
	EmbedProfile string   `yaml:"embed_profile"`
	SearchMode   string   `yaml:"search_mode"`
	Agents       []string `yaml:"agents"`
	TenantScoped bool     `yaml:"tenant_scoped"`
}

type MCP struct {
	Servers []MCPServer `yaml:"servers"`
}

type MCPServer struct {
	Name       string            `yaml:"name"`
	Transport  string            `yaml:"transport"`
	Command    []string          `yaml:"command"`
	URL        string            `yaml:"url"`
	ToolPrefix string            `yaml:"tool_prefix"`
	Metadata   map[string]string `yaml:"metadata"`
}

type Trigger struct {
	Event         string `yaml:"event"`
	Agent         string `yaml:"agent"`
	PromptPath    string `yaml:"prompt_path"`
	ContextPath   string `yaml:"context_path"`
	RunIDPath     string `yaml:"run_id_path"`
	DefaultPrompt string `yaml:"default_prompt"`
}

type HumanInLoopPolicy struct {
	Enabled     bool     `yaml:"enabled"`
	Checkpoints []string `yaml:"checkpoints"`
}

type Workflow struct {
	Nodes []WorkflowNode `yaml:"nodes"`
	Edges []WorkflowEdge `yaml:"edges"`
}

type WorkflowNode struct {
	ID        string         `yaml:"id"`
	Kind      string         `yaml:"kind"`
	Ref       string         `yaml:"ref"`
	Input     map[string]any `yaml:"input"`
	DependsOn []string       `yaml:"depends_on"`
	Condition string         `yaml:"condition"`
	Retry     RetryPolicy    `yaml:"retry"`
}

type WorkflowEdge struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Condition string `yaml:"condition"`
}

type RetryPolicy struct {
	MaxAttempts int `yaml:"max_attempts"`
}

type Runtime struct {
	Timeout             time.Duration     `yaml:"timeout"`
	MaxSteps            int               `yaml:"max_steps"`
	MaxRetries          int               `yaml:"max_retries"`
	MaxParallel         int               `yaml:"max_parallel"`
	StepOutputThreshold int64             `yaml:"step_output_threshold"`
	Secrets             map[string]string `yaml:"secrets"`
}

func LoadFile(path string) (core.Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Scenario{}, err
	}
	return Load(data)
}

func Load(data []byte) (core.Scenario, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return core.Scenario{}, err
	}
	scenario, err := doc.ToCore()
	if err != nil {
		return core.Scenario{}, err
	}
	if err := Validate(scenario); err != nil {
		return core.Scenario{}, err
	}
	return scenario, nil
}

func (d Document) ToCore() (core.Scenario, error) {
	s := core.Scenario{
		Name:        d.Scenario.Name,
		Description: d.Scenario.Description,
		LLMs:        make(map[string]core.LLMProfileRef, len(d.Scenario.LLMs)),
		Memories:    make(map[string]core.MemoryRef, len(d.Scenario.Memories)),
		Tools:       make(map[string]core.Tool, len(d.Scenario.Tools)),
		Skills:      make(map[string]core.Skill, len(d.Scenario.Skills)),
		Agents:      make(map[string]core.Agent, len(d.Scenario.Agents)),
		Orchestration: core.Orchestration{
			Mode:        core.OrchestrationMode(d.Scenario.Orchestration.Mode),
			MaxParallel: d.Scenario.Orchestration.MaxParallel,
			HumanInLoop: core.HumanInLoopPolicy{
				Enabled:     d.Scenario.Orchestration.HumanInLoop.Enabled,
				Checkpoints: d.Scenario.Orchestration.HumanInLoop.Checkpoints,
			},
			Planning: core.PlanningPolicy{
				Enabled:         d.Scenario.Orchestration.Planning.Enabled,
				Agent:           d.Scenario.Orchestration.Planning.Agent,
				MaxSteps:        d.Scenario.Orchestration.Planning.MaxSteps,
				Execute:         d.Scenario.Orchestration.Planning.Execute,
				ReplanOnFailure: d.Scenario.Orchestration.Planning.ReplanOnFailure,
			},
		},
		Runtime: core.RuntimePolicy{
			Timeout:             d.Scenario.Runtime.Timeout,
			MaxSteps:            d.Scenario.Runtime.MaxSteps,
			MaxRetries:          d.Scenario.Runtime.MaxRetries,
			MaxParallel:         d.Scenario.Runtime.MaxParallel,
			StepOutputThreshold: d.Scenario.Runtime.StepOutputThreshold,
			Secrets:             d.Scenario.Runtime.Secrets,
		},
	}
	for name, profile := range d.Scenario.LLMs {
		s.LLMs[name] = core.LLMProfileRef{
			Provider:            profile.Provider,
			Model:               profile.Model,
			Endpoint:            profile.Endpoint,
			APIKeyEnv:           profile.APIKeyEnv,
			ContextWindowTokens: profile.ContextWindowTokens,
			MaxOutputTokens:     profile.MaxOutputTokens,
			Temperature:         profile.Temperature,
			TopP:                profile.TopP,
			Timeout:             profile.Timeout,
			Thinking:            profile.Thinking,
			ReasoningEffort:     profile.ReasoningEffort,
			Context:             profile.Context,
			ExtraBody:           profile.ExtraBody,
			Capabilities:        profile.Capabilities,
			Metadata:            profile.Metadata,
		}
	}
	for name, mem := range d.Scenario.Memories {
		ref := core.MemoryRef{
			Type:      mem.Type,
			Scope:     mem.Scope,
			Namespace: mem.Namespace,
			Metadata:  mem.Metadata,
		}
		if mem.Tiers != nil {
			ref.Tiers = &core.MemoryTierSettings{
				Enabled:       mem.Tiers.Enabled,
				HotCapacity:   mem.Tiers.HotCapacity,
				WarmCapacity:  mem.Tiers.WarmCapacity,
				ColdCapacity:  mem.Tiers.ColdCapacity,
				PromoteAccess: mem.Tiers.PromoteAccess,
				RecallBudget: core.MemoryTierRecallBudget{
					Total: mem.Tiers.RecallBudget.Total,
					Hot:   mem.Tiers.RecallBudget.Hot,
					Warm:  mem.Tiers.RecallBudget.Warm,
					Cold:  mem.Tiers.RecallBudget.Cold,
				},
				RecallWeights: core.MemoryTierRecallWeights{
					Semantic:   mem.Tiers.RecallWeights.Semantic,
					Recency:    mem.Tiers.RecallWeights.Recency,
					Importance: mem.Tiers.RecallWeights.Importance,
				},
			}
			if mem.Tiers.HotTTL > 0 {
				ref.Tiers.HotTTL = mem.Tiers.HotTTL.String()
			}
			if mem.Tiers.WarmTTL > 0 {
				ref.Tiers.WarmTTL = mem.Tiers.WarmTTL.String()
			}
			if mem.Tiers.DemoteIdle > 0 {
				ref.Tiers.DemoteIdle = mem.Tiers.DemoteIdle.String()
			}
		}
		s.Memories[name] = ref
	}
	for _, collection := range d.Scenario.Knowledge.Collections {
		s.Knowledge.Collections = append(s.Knowledge.Collections, core.KnowledgeCollection{
			Name:         collection.Name,
			Description:  collection.Description,
			Namespace:    collection.Namespace,
			Tool:         collection.Tool,
			EmbedProfile: collection.EmbedProfile,
			SearchMode:   collection.SearchMode,
			Agents:       collection.Agents,
			TenantScoped: collection.TenantScoped,
		})
	}
	for _, server := range d.Scenario.MCP.Servers {
		s.MCP.Servers = append(s.MCP.Servers, core.MCPServer{
			Name:       server.Name,
			Transport:  server.Transport,
			Command:    server.Command,
			URL:        server.URL,
			ToolPrefix: server.ToolPrefix,
			Metadata:   server.Metadata,
		})
	}
	for name, tool := range d.Scenario.Tools {
		inputSchema, err := marshalRaw(tool.InputSchema)
		if err != nil {
			return core.Scenario{}, fmt.Errorf("tool %q input_schema: %w", name, err)
		}
		outputSchema, err := marshalRaw(tool.OutputSchema)
		if err != nil {
			return core.Scenario{}, fmt.Errorf("tool %q output_schema: %w", name, err)
		}
		s.Tools[name] = core.Tool{
			Name:         name,
			Type:         tool.Type,
			Description:  tool.Description,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
			SideEffect:   core.SideEffectLevel(tool.SideEffect),
			Approval:     core.ApprovalPolicy(tool.Approval),
			LLM:          tool.LLM,
			RateCap:      tool.RateCap,
			Metadata:     tool.Metadata,
		}
	}
	for name, skill := range d.Scenario.Skills {
		workflow, err := skill.workflowToCore()
		if err != nil {
			return core.Scenario{}, fmt.Errorf("skill %q workflow: %w", name, err)
		}
		agentPolicy, err := skill.agentPolicyToCore()
		if err != nil {
			return core.Scenario{}, fmt.Errorf("skill %q agent_policy: %w", name, err)
		}
		s.Skills[name] = core.Skill{
			Name:             name,
			Description:      skill.Description,
			Version:          skill.Version,
			CompatibleAgents: skill.CompatibleAgents,
			PromptFragments:  skill.promptFragmentsToCore(),
			AgentPolicy:      agentPolicy,
			ToolPolicies:     skill.toolPoliciesToCore(),
			Workflow:         workflow,
			Metadata:         skillMetadata(skill.Source, skill.Metadata),
		}
	}
	for name, agent := range d.Scenario.Agents {
		outputSchema, err := marshalRaw(agent.OutputSchema)
		if err != nil {
			return core.Scenario{}, fmt.Errorf("agent %q output_schema: %w", name, err)
		}
		s.Agents[name] = core.Agent{
			Name:         name,
			Description:  agent.Description,
			Role:         agent.Role,
			Instructions: agent.Instructions,
			LLM:          agent.LLM,
			Memory:       agent.Memory,
			Tools:        agent.Tools,
			Skills:       agent.Skills,
			SubAgents:    agent.SubAgents,
			Policy:       core.AgentPolicy{MaxSteps: agent.MaxSteps, Timeout: agent.Timeout, RetryLimit: agent.RetryLimit, OutputSchema: outputSchema, HumanCheckpoints: agent.HumanCheckpoints},
			Metadata:     agent.Metadata,
		}
	}
	for _, trigger := range d.Scenario.Triggers {
		s.Triggers = append(s.Triggers, core.Trigger{
			Event:         trigger.Event,
			Agent:         trigger.Agent,
			PromptPath:    trigger.PromptPath,
			ContextPath:   trigger.ContextPath,
			RunIDPath:     trigger.RunIDPath,
			DefaultPrompt: trigger.DefaultPrompt,
		})
	}
	if d.Scenario.Orchestration.Workflow != nil {
		workflow, err := d.Scenario.Orchestration.Workflow.toCore()
		if err != nil {
			return core.Scenario{}, err
		}
		s.Orchestration.Workflow = &workflow
	}
	return s, nil
}

func (s Skill) workflowToCore() (*core.Workflow, error) {
	if s.Workflow == nil {
		return nil, nil
	}
	workflow, err := s.Workflow.toCore()
	if err != nil {
		return nil, err
	}
	return &workflow, nil
}

func (s Skill) promptFragmentsToCore() []core.PromptFragment {
	if len(s.PromptFragments) == 0 {
		return nil
	}
	out := make([]core.PromptFragment, 0, len(s.PromptFragments))
	for _, fragment := range s.PromptFragments {
		out = append(out, core.PromptFragment{Name: fragment.Name, Content: fragment.Content})
	}
	return out
}

func (s Skill) toolPoliciesToCore() []core.SkillToolPolicy {
	if len(s.ToolPolicies) == 0 {
		return nil
	}
	out := make([]core.SkillToolPolicy, 0, len(s.ToolPolicies))
	for _, policy := range s.ToolPolicies {
		out = append(out, core.SkillToolPolicy{
			Tool:       policy.Tool,
			Approval:   core.ApprovalPolicy(policy.Approval),
			SideEffect: core.SideEffectLevel(policy.SideEffect),
			RateCap:    policy.RateCap,
		})
	}
	return out
}

func (s Skill) agentPolicyToCore() (core.AgentPolicy, error) {
	outputSchema, err := marshalRaw(s.AgentPolicy.OutputSchema)
	if err != nil {
		return core.AgentPolicy{}, err
	}
	return core.AgentPolicy{
		MaxSteps:         s.AgentPolicy.MaxSteps,
		Timeout:          s.AgentPolicy.Timeout,
		RetryLimit:       s.AgentPolicy.RetryLimit,
		OutputSchema:     outputSchema,
		HumanCheckpoints: s.AgentPolicy.HumanCheckpoints,
	}, nil
}

func skillMetadata(source string, metadata map[string]string) map[string]string {
	if len(metadata) == 0 && source == "" {
		return nil
	}
	out := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		out[key] = value
	}
	if source != "" {
		out["source"] = source
	}
	return out
}

func (w Workflow) toCore() (core.Workflow, error) {
	out := core.Workflow{
		Nodes: make([]core.WorkflowNode, 0, len(w.Nodes)),
		Edges: make([]core.WorkflowEdge, 0, len(w.Edges)),
	}
	for _, node := range w.Nodes {
		input, err := marshalRaw(node.Input)
		if err != nil {
			return core.Workflow{}, fmt.Errorf("workflow node %q input: %w", node.ID, err)
		}
		out.Nodes = append(out.Nodes, core.WorkflowNode{
			ID:        node.ID,
			Kind:      core.WorkflowNodeKind(node.Kind),
			Ref:       node.Ref,
			Input:     input,
			DependsOn: node.DependsOn,
			Condition: node.Condition,
			Retry:     core.RetryPolicy{MaxAttempts: node.Retry.MaxAttempts},
		})
	}
	for _, edge := range w.Edges {
		out.Edges = append(out.Edges, core.WorkflowEdge{From: edge.From, To: edge.To, Condition: edge.Condition})
	}
	return out, nil
}

func marshalRaw(v map[string]any) ([]byte, error) {
	if len(v) == 0 {
		return nil, nil
	}
	return json.Marshal(v)
}
