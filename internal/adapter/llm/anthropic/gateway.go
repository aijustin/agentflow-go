package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

type Gateway struct {
	profiles map[string]llm.Profile
	client   *http.Client
}

func NewGateway(profiles []llm.Profile, client *http.Client) *Gateway {
	if client == nil {
		client = http.DefaultClient
	}
	index := make(map[string]llm.Profile, len(profiles))
	for _, profile := range profiles {
		index[profile.Name] = profile
	}
	return &Gateway{profiles: index, client: client}
}

func (g *Gateway) Supports(profile string, cap llm.Capability) bool {
	switch cap {
	case llm.CapChat:
		_, ok := g.profiles[profile]
		return ok
	default:
		return false
	}
}

func (g *Gateway) Chat(ctx context.Context, profileName string, req llm.ChatRequest) (llm.ChatResponse, error) {
	profile, ok := g.profiles[profileName]
	if !ok {
		return llm.ChatResponse{}, fmt.Errorf("anthropic: profile %q not found", profileName)
	}
	endpoint := strings.TrimRight(profile.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1"
	}
	body := map[string]any{
		"model":      profile.Model,
		"messages":   req.Messages,
		"max_tokens": req.MaxTokens,
	}
	if body["max_tokens"] == 0 {
		body["max_tokens"] = 1024
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/messages", bytes.NewReader(payload))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if profile.APIKeyEnv != "" {
		if key := os.Getenv(profile.APIKeyEnv); key != "" {
			httpReq.Header.Set("x-api-key", key)
		}
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.ChatResponse{}, fmt.Errorf("anthropic: unexpected status %s", resp.Status)
	}
	var decoded struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return llm.ChatResponse{}, err
	}
	text := ""
	if len(decoded.Content) > 0 {
		text = decoded.Content[0].Text
	}
	return llm.ChatResponse{
		Message: llm.Message{Role: llm.RoleAssistant, Content: text},
		Usage: llm.TokenUsage{
			InputTokens:  decoded.Usage.InputTokens,
			OutputTokens: decoded.Usage.OutputTokens,
			TotalTokens:  decoded.Usage.InputTokens + decoded.Usage.OutputTokens,
		},
	}, nil
}
