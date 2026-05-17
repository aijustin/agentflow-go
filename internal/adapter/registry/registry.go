package registry

import (
	"context"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type Registry struct {
	LLMs   map[string]llm.Profile
	Memory map[string]memory.Repository
	Tools  map[string]core.ToolExecutor
	Skills map[string]core.Skill
	Events map[string]core.EventSink
}

func New() *Registry {
	return &Registry{
		LLMs:   make(map[string]llm.Profile),
		Memory: make(map[string]memory.Repository),
		Tools:  make(map[string]core.ToolExecutor),
		Skills: make(map[string]core.Skill),
		Events: make(map[string]core.EventSink),
	}
}

func (r *Registry) RegisterTool(name string, executor core.ToolExecutor) error {
	if name == "" {
		return fmt.Errorf("registry: tool name is required")
	}
	if executor == nil {
		return fmt.Errorf("registry: tool %q executor is nil", name)
	}
	if _, exists := r.Tools[name]; exists {
		return fmt.Errorf("registry: tool %q already registered", name)
	}
	r.Tools[name] = executor
	return nil
}

func (r *Registry) Tool(name string) (core.ToolExecutor, bool) {
	tool, ok := r.Tools[name]
	return tool, ok
}

func (r *Registry) ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	executor, ok := r.Tool(tool.Name)
	return executor, ok, nil
}
