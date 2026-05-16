package core

import "encoding/json"

type Workflow struct {
	Nodes []WorkflowNode `json:"nodes,omitempty"`
	Edges []WorkflowEdge `json:"edges,omitempty"`
}

type WorkflowNodeKind string

const (
	NodeAgent     WorkflowNodeKind = "agent"
	NodeTool      WorkflowNodeKind = "tool"
	NodeSkill     WorkflowNodeKind = "skill"
	NodeHumanGate WorkflowNodeKind = "human_gate"
	NodeTransform WorkflowNodeKind = "transform"
)

type WorkflowNode struct {
	ID        string           `json:"id"`
	Kind      WorkflowNodeKind `json:"kind"`
	Ref       string           `json:"ref,omitempty"`
	Input     json.RawMessage  `json:"input,omitempty"`
	DependsOn []string         `json:"depends_on,omitempty"`
	Condition string           `json:"condition,omitempty"`
	Retry     RetryPolicy      `json:"retry"`
}

type WorkflowEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts int `json:"max_attempts,omitempty"`
}
