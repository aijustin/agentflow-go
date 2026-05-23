package llmsummary

import (
	"context"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// Summarizer compresses memory records through an LLM profile with truncate fallback handled by the caller.
type Summarizer struct {
	Gateway llm.Chatter
	Profile string
}

func NewSummarizer(gateway llm.Chatter, profile string) *Summarizer {
	return &Summarizer{Gateway: gateway, Profile: profile}
}

func (s *Summarizer) Summarize(ctx context.Context, content string, maxChars int) (string, error) {
	if s == nil || s.Gateway == nil {
		return "", fmt.Errorf("llmsummary: gateway is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil
	}
	profile := strings.TrimSpace(s.Profile)
	if profile == "" {
		profile = "default"
	}
	if maxChars <= 0 {
		maxChars = 512
	}
	prompt := fmt.Sprintf("Summarize the following memory record in at most %d characters. Preserve key facts and entities.\n\n%s", maxChars, content)
	resp, err := s.Gateway.Chat(ctx, profile, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You compress long memory records into concise summaries for archival search."},
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens: maxOutputTokens(maxChars),
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

func maxOutputTokens(maxChars int) int {
	tokens := maxChars / 2
	if tokens < 64 {
		return 64
	}
	if tokens > 1024 {
		return 1024
	}
	return tokens
}
