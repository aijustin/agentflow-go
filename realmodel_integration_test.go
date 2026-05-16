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
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRealModelFrameworkFlow(t *testing.T) {
	baseURL, model := realModelEnv(t)
	scenario := []byte(`
scenario:
  name: real-model-flow
  llms:
    default:
      provider: openai-compatible
      model: ` + model + `
      endpoint: ` + baseURL + `
      api_key_env: AGENT_REALMODEL_API_KEY
  memories:
    session:
      type: in_memory
      scope: session
  agents:
    assistant:
      llm: default
      memory: session
      instructions: "你是 agentflow-go 的真实模型流程测试助手。必须严格按用户要求原样输出固定短句，不要输出 Markdown。"
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: false
`)
	loaded, err := agentflow.LoadScenario(scenario)
	if err != nil {
		t.Fatal(err)
	}
	fw, err := agentflow.New(loaded, agentflow.WithLLMGateway(realModelGateway(baseURL, model)))
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
	scenario := []byte(`
scenario:
  name: real-model-context-governance
  llms:
    default:
      provider: openai-compatible
      model: ` + model + `
      endpoint: ` + baseURL + `
      api_key_env: AGENT_REALMODEL_API_KEY
      context_window_tokens: 1400
      max_output_tokens: 1024
      temperature: 0
      extra_body:
        chat_template_kwargs:
          enable_thinking: false
      context:
        strategy: sliding_window_with_summary
        max_input_tokens: 220
        reserved_output_tokens: 1024
        summary_tokens: 80
        system_prompt_protection: true
        compression:
          enabled: true
          trigger_ratio: 0.5
  memories:
    session:
      type: in_memory
      scope: session
  agents:
    assistant:
      llm: default
      memory: session
      instructions: "你是上下文治理测试助手。请忽略上下文噪声，并严格原样输出用户要求的固定短句。"
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: false
`)
	loaded, err := agentflow.LoadScenario(scenario)
	if err != nil {
		t.Fatal(err)
	}
	events := &captureSink{}
	fw, err := agentflow.New(
		loaded,
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
