package yaml

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// Marshal renders a legacy scenario YAML document for the given core scenario.
func Marshal(scenario core.Scenario) ([]byte, error) {
	doc, err := DocumentFromCore(scenario)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("yaml: marshal scenario: %w", err)
	}
	return data, nil
}

// SaveFile writes a legacy scenario YAML document to path atomically.
func SaveFile(path string, scenario core.Scenario) error {
	data, err := Marshal(scenario)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".scenario-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// DocumentFromCore converts a core scenario into the legacy YAML document shape.
func DocumentFromCore(scenario core.Scenario) (Document, error) {
	doc := Document{Scenario: Scenario{
		Name:        scenario.Name,
		Description: scenario.Description,
		LLMs:        make(map[string]LLMProfile, len(scenario.LLMs)),
		Memories:    make(map[string]Memory, len(scenario.Memories)),
		Tools:       make(map[string]Tool, len(scenario.Tools)),
		Skills:      make(map[string]Skill, len(scenario.Skills)),
		Agents:      make(map[string]Agent, len(scenario.Agents)),
		Orchestration: Orchestration{
			Mode:        string(scenario.Orchestration.Mode),
			MaxParallel: scenario.Orchestration.MaxParallel,
			HumanInLoop: HumanInLoopPolicy{
				Enabled:     scenario.Orchestration.HumanInLoop.Enabled,
				Checkpoints: scenario.Orchestration.HumanInLoop.Checkpoints,
			},
			Planning: PlanningPolicy{
				Enabled:         scenario.Orchestration.Planning.Enabled,
				Agent:           scenario.Orchestration.Planning.Agent,
				MaxSteps:        scenario.Orchestration.Planning.MaxSteps,
				Execute:         scenario.Orchestration.Planning.Execute,
				ReplanOnFailure: scenario.Orchestration.Planning.ReplanOnFailure,
			},
		},
		Runtime: Runtime{
			Timeout:             scenario.Runtime.Timeout,
			MaxSteps:            scenario.Runtime.MaxSteps,
			MaxRetries:          scenario.Runtime.MaxRetries,
			MaxParallel:         scenario.Runtime.MaxParallel,
			StepOutputThreshold: scenario.Runtime.StepOutputThreshold,
			Secrets:             scenario.Runtime.Secrets,
		},
	}}
	for name, profile := range scenario.LLMs {
		doc.Scenario.LLMs[name] = LLMProfile{
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
	for name, mem := range scenario.Memories {
		doc.Scenario.Memories[name] = memoryFromCore(mem)
	}
	for _, collection := range scenario.Knowledge.Collections {
		doc.Scenario.Knowledge.Collections = append(doc.Scenario.Knowledge.Collections, KnowledgeCollection{
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
	for _, server := range scenario.MCP.Servers {
		doc.Scenario.MCP.Servers = append(doc.Scenario.MCP.Servers, MCPServer{
			Name:       server.Name,
			Transport:  server.Transport,
			Command:    server.Command,
			URL:        server.URL,
			ToolPrefix: server.ToolPrefix,
			Metadata:   server.Metadata,
		})
	}
	for name, tool := range scenario.Tools {
		inputSchema, err := unmarshalRaw(tool.InputSchema)
		if err != nil {
			return Document{}, fmt.Errorf("tool %q input_schema: %w", name, err)
		}
		outputSchema, err := unmarshalRaw(tool.OutputSchema)
		if err != nil {
			return Document{}, fmt.Errorf("tool %q output_schema: %w", name, err)
		}
		doc.Scenario.Tools[name] = Tool{
			Type:         tool.Type,
			Description:  tool.Description,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
			SideEffect:   string(tool.SideEffect),
			Approval:     string(tool.Approval),
			LLM:          tool.LLM,
			RateCap:      tool.RateCap,
			Metadata:     tool.Metadata,
		}
	}
	for name, skill := range scenario.Skills {
		exported, err := skillFromCore(name, skill)
		if err != nil {
			return Document{}, err
		}
		doc.Scenario.Skills[name] = exported
	}
	for name, agent := range scenario.Agents {
		outputSchema, err := unmarshalRaw(agent.Policy.OutputSchema)
		if err != nil {
			return Document{}, fmt.Errorf("agent %q output_schema: %w", name, err)
		}
		doc.Scenario.Agents[name] = Agent{
			Description:      agent.Description,
			Role:             agent.Role,
			Instructions:     agent.Instructions,
			LLM:              agent.LLM,
			Memory:           agent.Memory,
			Tools:            agent.Tools,
			Skills:           agent.Skills,
			SubAgents:        agent.SubAgents,
			MaxSteps:         agent.Policy.MaxSteps,
			Timeout:          agent.Policy.Timeout,
			RetryLimit:       agent.Policy.RetryLimit,
			OutputSchema:     outputSchema,
			HumanCheckpoints: agent.Policy.HumanCheckpoints,
			Metadata:         agent.Metadata,
		}
	}
	for _, trigger := range scenario.Triggers {
		doc.Scenario.Triggers = append(doc.Scenario.Triggers, Trigger{
			Event:         trigger.Event,
			Agent:         trigger.Agent,
			PromptPath:    trigger.PromptPath,
			ContextPath:   trigger.ContextPath,
			RunIDPath:     trigger.RunIDPath,
			DefaultPrompt: trigger.DefaultPrompt,
		})
	}
	if scenario.Orchestration.Workflow != nil {
		workflow, err := workflowFromCore(*scenario.Orchestration.Workflow)
		if err != nil {
			return Document{}, err
		}
		doc.Scenario.Orchestration.Workflow = &workflow
	}
	if len(scenario.Orchestration.Workflows) > 0 {
		doc.Scenario.Orchestration.Workflows = make(map[string]Workflow, len(scenario.Orchestration.Workflows))
		for name, wf := range scenario.Orchestration.Workflows {
			workflow, err := workflowFromCore(wf)
			if err != nil {
				return Document{}, fmt.Errorf("orchestration.workflows.%s: %w", name, err)
			}
			doc.Scenario.Orchestration.Workflows[name] = workflow
		}
	}
	return doc, nil
}

func memoryFromCore(mem core.MemoryRef) Memory {
	out := Memory{
		Type:      mem.Type,
		Scope:     mem.Scope,
		Namespace: mem.Namespace,
		Metadata:  mem.Metadata,
	}
	if mem.Tiers != nil {
		out.Tiers = &MemoryTiers{
			Enabled:       mem.Tiers.Enabled,
			HotCapacity:   mem.Tiers.HotCapacity,
			WarmCapacity:  mem.Tiers.WarmCapacity,
			ColdCapacity:  mem.Tiers.ColdCapacity,
			PromoteAccess: mem.Tiers.PromoteAccess,
			RecallBudget: MemoryRecallBudget{
				Total: mem.Tiers.RecallBudget.Total,
				Hot:   mem.Tiers.RecallBudget.Hot,
				Warm:  mem.Tiers.RecallBudget.Warm,
				Cold:  mem.Tiers.RecallBudget.Cold,
			},
			RecallWeights: MemoryRecallWeights{
				Semantic:   mem.Tiers.RecallWeights.Semantic,
				Recency:    mem.Tiers.RecallWeights.Recency,
				Importance: mem.Tiers.RecallWeights.Importance,
			},
		}
		if mem.Tiers.HotTTL != "" {
			out.Tiers.HotTTL = parseDuration(mem.Tiers.HotTTL)
		}
		if mem.Tiers.WarmTTL != "" {
			out.Tiers.WarmTTL = parseDuration(mem.Tiers.WarmTTL)
		}
		if mem.Tiers.DemoteIdle != "" {
			out.Tiers.DemoteIdle = parseDuration(mem.Tiers.DemoteIdle)
		}
	}
	return out
}

func skillFromCore(name string, skill core.Skill) (Skill, error) {
	var workflow *Workflow
	if skill.Workflow != nil {
		wf, err := workflowFromCore(*skill.Workflow)
		if err != nil {
			return Skill{}, fmt.Errorf("skill %q workflow: %w", name, err)
		}
		workflow = &wf
	}
	outputSchema, err := unmarshalRaw(skill.AgentPolicy.OutputSchema)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q agent_policy output_schema: %w", name, err)
	}
	source := ""
	if skill.Metadata != nil {
		source = skill.Metadata["source"]
	}
	metadata := make(map[string]string, len(skill.Metadata))
	for key, value := range skill.Metadata {
		if key == "source" {
			continue
		}
		metadata[key] = value
	}
	toolPolicies := make([]SkillToolPolicy, 0, len(skill.ToolPolicies))
	for _, policy := range skill.ToolPolicies {
		toolPolicies = append(toolPolicies, SkillToolPolicy{
			Tool:       policy.Tool,
			Approval:   string(policy.Approval),
			SideEffect: string(policy.SideEffect),
			RateCap:    policy.RateCap,
		})
	}
	fragments := make([]PromptFragment, 0, len(skill.PromptFragments))
	for _, fragment := range skill.PromptFragments {
		fragments = append(fragments, PromptFragment{Name: fragment.Name, Content: fragment.Content})
	}
	return Skill{
		Source:           source,
		Description:      skill.Description,
		Version:          skill.Version,
		CompatibleAgents: skill.CompatibleAgents,
		PromptFragments:  fragments,
		AgentPolicy: AgentPolicy{
			MaxSteps:         skill.AgentPolicy.MaxSteps,
			Timeout:          skill.AgentPolicy.Timeout,
			RetryLimit:       skill.AgentPolicy.RetryLimit,
			OutputSchema:     outputSchema,
			HumanCheckpoints: skill.AgentPolicy.HumanCheckpoints,
		},
		ToolPolicies: toolPolicies,
		Workflow:     workflow,
		Metadata:     metadata,
	}, nil
}

func workflowFromCore(wf core.Workflow) (Workflow, error) {
	out := Workflow{
		Nodes: make([]WorkflowNode, 0, len(wf.Nodes)),
		Edges: make([]WorkflowEdge, 0, len(wf.Edges)),
	}
	for _, node := range wf.Nodes {
		input, err := unmarshalRaw(node.Input)
		if err != nil {
			return Workflow{}, fmt.Errorf("workflow node %q input: %w", node.ID, err)
		}
		out.Nodes = append(out.Nodes, WorkflowNode{
			ID:        node.ID,
			Kind:      string(node.Kind),
			Ref:       node.Ref,
			Input:     input,
			DependsOn: node.DependsOn,
			Condition: node.Condition,
			Retry:     RetryPolicy{MaxAttempts: node.Retry.MaxAttempts},
		})
	}
	for _, edge := range wf.Edges {
		out.Edges = append(out.Edges, WorkflowEdge{From: edge.From, To: edge.To, Condition: edge.Condition})
	}
	return out, nil
}

func unmarshalRaw(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseDuration(raw string) time.Duration {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}
