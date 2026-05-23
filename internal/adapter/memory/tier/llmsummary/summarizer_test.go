package llmsummary

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

type stubChatter struct {
	content string
	err     error
}

func (s stubChatter) Chat(_ context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	if s.err != nil {
		return llm.ChatResponse{}, s.err
	}
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: s.content}}, nil
}

func TestSummarizerReturnsLLMContent(t *testing.T) {
	s := NewSummarizer(stubChatter{content: "short summary"}, "planner")
	got, err := s.Summarize(context.Background(), "long content about project alpha", 128)
	if err != nil {
		t.Fatal(err)
	}
	if got != "short summary" {
		t.Fatalf("expected llm summary, got %q", got)
	}
}

func TestSummarizerEmptyContent(t *testing.T) {
	s := NewSummarizer(stubChatter{content: "unused"}, "default")
	got, err := s.Summarize(context.Background(), "   ", 128)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty summary, got %q", got)
	}
}
