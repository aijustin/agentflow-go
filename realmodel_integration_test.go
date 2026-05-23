//go:build realmodel

package agentflow_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/openai"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRealModelFrameworkFlow(t *testing.T) {
	baseURL, model := realModelEnv(t)
	scenario := realModelAutonomousScenario(baseURL, model)
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(realModelGateway(baseURL, model)))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := fw.Run(ctx, agentflow.RunRequest{
		RunID:  "real-model-run",
		Agent:  "assistant",
		Prompt: "请原样输出：真实模型流程测试成功",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, runstate.RunStatusCompleted)
	}
	if !strings.Contains(result.Output, "真实模型流程测试成功") {
		t.Fatalf("unexpected model output: %s", result.Output)
	}
	t.Logf("real model output: %s", result.Output)
}

func TestRealModelContextGovernanceLongInputFlow(t *testing.T) {
	baseURL, model := realModelEnv(t)
	scenario := realModelContextGovernanceScenario(baseURL, model)
	events := &captureSink{}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(realModelGateway(baseURL, model)),
		agentflow.WithEventSink(events),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := fw.Run(ctx, agentflow.RunRequest{
		RunID:   "real-model-context-run",
		Agent:   "assistant",
		Prompt:  "请原样输出：上下文治理真实模型测试成功",
		Context: []byte(`{"noisy_history":"` + strings.Repeat("历史噪声 信息片段 工具结果 ", 500) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, runstate.RunStatusCompleted)
	}
	stats := events.contextStats(t)
	if !stats.Summarized || stats.DroppedMessages == 0 || stats.AfterTokens > stats.MaxInputTokens {
		t.Fatalf("context governance did not compress as expected: %+v", stats)
	}
	if !strings.Contains(result.Output, "上下文治理真实模型测试成功") {
		t.Fatalf("unexpected model output %q with context stats %+v", result.Output, stats)
	}
	t.Logf("context stats: %+v", stats)
	t.Logf("real model context output: %s", result.Output)
}

func realModelEnv(t *testing.T) (string, string) {
	t.Helper()
	baseURL := os.Getenv("AGENT_REALMODEL_BASE_URL")
	model := os.Getenv("AGENT_REALMODEL_MODEL")
	apiKey := os.Getenv("AGENT_REALMODEL_API_KEY")
	if baseURL == "" || model == "" || apiKey == "" {
		t.Skip("AGENT_REALMODEL_BASE_URL, AGENT_REALMODEL_MODEL, and AGENT_REALMODEL_API_KEY are required")
	}
	return baseURL, model
}

func realModelGateway(baseURL, model string) llm.Gateway {
	return openai.NewGateway([]llm.Profile{{
		Name:      "default",
		Provider:  "openai-compatible",
		Model:     model,
		Endpoint:  baseURL,
		APIKeyEnv: "AGENT_REALMODEL_API_KEY",
	}}, nil)
}

func realModelAutonomousScenario(baseURL, model string) core.Scenario {
	scenario := builder.MinimalAutonomous("assistant")
	scenario.Name = "real-model-flow"
	profile := scenario.LLMs["default"]
	profile.Provider = "openai-compatible"
	profile.Model = model
	profile.Endpoint = baseURL
	profile.APIKeyEnv = "AGENT_REALMODEL_API_KEY"
	scenario.LLMs["default"] = profile
	agent := scenario.Agents["assistant"]
	agent.Instructions = "你是 agentflow-go 的真实模型流程测试助手。必须严格按用户要求原样输出固定短句，不要输出 Markdown。"
	scenario.Agents["assistant"] = agent
	return scenario
}

func realModelContextGovernanceScenario(baseURL, model string) core.Scenario {
	scenario := builder.ContextGovernance("assistant")
	scenario.Name = "real-model-context-governance"
	profile := scenario.LLMs["default"]
	profile.Endpoint = baseURL
	profile.Model = model
	temp := float32(0)
	profile.Temperature = &temp
	profile.Thinking = llm.ThinkingConfig{}
	profile.ExtraBody = map[string]any{
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	}
	scenario.LLMs["default"] = profile
	agent := scenario.Agents["assistant"]
	agent.Instructions = "你是上下文治理测试助手。请忽略上下文噪声，并严格原样输出用户要求的固定短句。"
	scenario.Agents["assistant"] = agent
	return scenario
}

type captureSink struct {
	mu     sync.Mutex
	events []core.Event
}

func (s *captureSink) Emit(ctx context.Context, event core.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *captureSink) contextStats(t *testing.T) struct {
	Strategy        string `json:"strategy"`
	BeforeTokens    int    `json:"before_tokens"`
	AfterTokens     int    `json:"after_tokens"`
	MaxInputTokens  int    `json:"max_input_tokens"`
	DroppedMessages int    `json:"dropped_messages"`
	Summarized      bool   `json:"summarized"`
} {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event.Type != core.EventContextPrepared {
			continue
		}
		var stats struct {
			Strategy        string `json:"strategy"`
			BeforeTokens    int    `json:"before_tokens"`
			AfterTokens     int    `json:"after_tokens"`
			MaxInputTokens  int    `json:"max_input_tokens"`
			DroppedMessages int    `json:"dropped_messages"`
			Summarized      bool   `json:"summarized"`
		}
		if err := json.Unmarshal(event.Payload, &stats); err != nil {
			t.Fatal(err)
		}
		return stats
	}
	t.Fatal("ContextPrepared event not found")
	return struct {
		Strategy        string `json:"strategy"`
		BeforeTokens    int    `json:"before_tokens"`
		AfterTokens     int    `json:"after_tokens"`
		MaxInputTokens  int    `json:"max_input_tokens"`
		DroppedMessages int    `json:"dropped_messages"`
		Summarized      bool   `json:"summarized"`
	}{}
}
