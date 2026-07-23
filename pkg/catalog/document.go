package catalog

import (
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type toolSpec struct {
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

type skillSpec struct {
	Description      string                `yaml:"description"`
	Version          string                `yaml:"version"`
	CompatibleAgents []string              `yaml:"compatible_agents"`
	PromptFragments  []promptFragmentSpec  `yaml:"prompt_fragments"`
	AgentPolicy      agentPolicySpec       `yaml:"agent_policy"`
	ToolPolicies     []skillToolPolicySpec `yaml:"tool_policies"`
	Workflow         *workflowSpec         `yaml:"workflow"`
	Metadata         map[string]string     `yaml:"metadata"`
}

type promptFragmentSpec struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

type skillToolPolicySpec struct {
	Tool       string `yaml:"tool"`
	Approval   string `yaml:"approval"`
	SideEffect string `yaml:"side_effect"`
	RateCap    int    `yaml:"rate_cap"`
}

type agentPolicySpec struct {
	MaxSteps         int            `yaml:"max_steps"`
	Timeout          string         `yaml:"timeout"`
	RetryLimit       int            `yaml:"retry_limit"`
	OutputSchema     map[string]any `yaml:"output_schema"`
	HumanCheckpoints []string       `yaml:"human_checkpoints"`
}

type workflowSpec struct {
	Nodes []workflowNodeSpec `yaml:"nodes"`
	Edges []workflowEdgeSpec `yaml:"edges"`
}

type workflowNodeSpec struct {
	ID        string          `yaml:"id"`
	Kind      string          `yaml:"kind"`
	Ref       string          `yaml:"ref"`
	Input     map[string]any  `yaml:"input"`
	DependsOn []string        `yaml:"depends_on"`
	Condition string          `yaml:"condition"`
	Interrupt bool            `yaml:"interrupt"`
	Retry     retryPolicySpec `yaml:"retry"`
}

type workflowEdgeSpec struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Condition string `yaml:"condition"`
}

type retryPolicySpec struct {
	MaxAttempts int `yaml:"max_attempts"`
}

func (s toolSpec) toCore(name string) (core.Tool, error) {
	inputSchema, err := marshalRaw(s.InputSchema)
	if err != nil {
		return core.Tool{}, fmt.Errorf("input_schema: %w", err)
	}
	outputSchema, err := marshalRaw(s.OutputSchema)
	if err != nil {
		return core.Tool{}, fmt.Errorf("output_schema: %w", err)
	}
	return core.Tool{
		Name:         name,
		Type:         s.Type,
		Description:  s.Description,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
		SideEffect:   core.SideEffectLevel(s.SideEffect),
		Approval:     core.ApprovalPolicy(s.Approval),
		LLM:          s.LLM,
		RateCap:      s.RateCap,
		Metadata:     s.Metadata,
	}, nil
}

func (s skillSpec) toCore(name string) (core.Skill, error) {
	workflow, err := s.workflowToCore()
	if err != nil {
		return core.Skill{}, err
	}
	agentPolicy, err := s.agentPolicyToCore()
	if err != nil {
		return core.Skill{}, err
	}
	return core.Skill{
		Name:             name,
		Description:      s.Description,
		Version:          s.Version,
		CompatibleAgents: s.CompatibleAgents,
		PromptFragments:  s.promptFragmentsToCore(),
		AgentPolicy:      agentPolicy,
		ToolPolicies:     s.toolPoliciesToCore(),
		Workflow:         workflow,
		Metadata:         s.Metadata,
	}, nil
}

func (s skillSpec) workflowToCore() (*core.Workflow, error) {
	if s.Workflow == nil {
		return nil, nil
	}
	workflow, err := s.Workflow.toCore()
	if err != nil {
		return nil, err
	}
	return &workflow, nil
}

func (s skillSpec) promptFragmentsToCore() []core.PromptFragment {
	if len(s.PromptFragments) == 0 {
		return nil
	}
	out := make([]core.PromptFragment, 0, len(s.PromptFragments))
	for _, fragment := range s.PromptFragments {
		out = append(out, core.PromptFragment{Name: fragment.Name, Content: fragment.Content})
	}
	return out
}

func (s skillSpec) toolPoliciesToCore() []core.SkillToolPolicy {
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

func (s skillSpec) agentPolicyToCore() (core.AgentPolicy, error) {
	outputSchema, err := marshalRaw(s.AgentPolicy.OutputSchema)
	if err != nil {
		return core.AgentPolicy{}, err
	}
	return core.AgentPolicy{
		MaxSteps:         s.AgentPolicy.MaxSteps,
		RetryLimit:       s.AgentPolicy.RetryLimit,
		OutputSchema:     outputSchema,
		HumanCheckpoints: s.AgentPolicy.HumanCheckpoints,
	}, nil
}

func (w workflowSpec) toCore() (core.Workflow, error) {
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
			Interrupt: node.Interrupt,
			Retry:     core.RetryPolicy{MaxAttempts: node.Retry.MaxAttempts},
		})
	}
	for _, edge := range w.Edges {
		out.Edges = append(out.Edges, core.WorkflowEdge{From: edge.From, To: edge.To, Condition: edge.Condition})
	}
	return out, nil
}

func marshalRaw(v map[string]any) (json.RawMessage, error) {
	if len(v) == 0 {
		return nil, nil
	}
	return json.Marshal(v)
}
