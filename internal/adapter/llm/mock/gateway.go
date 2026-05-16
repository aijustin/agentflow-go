package mock

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

var ErrNoResponse = errors.New("mock llm: no response queued")

type Gateway struct {
	mu            sync.Mutex
	caps          map[string]map[llm.Capability]bool
	responses     map[string][]llm.ChatResponse
	toolResponses map[string][]llm.ToolCallResponse
	embeddings    map[string][][][]float32
	structured    map[string][]json.RawMessage
	toolRequests  map[string][]llm.ToolCallRequest
}

func NewGateway() *Gateway {
	return &Gateway{
		caps:          make(map[string]map[llm.Capability]bool),
		responses:     make(map[string][]llm.ChatResponse),
		toolResponses: make(map[string][]llm.ToolCallResponse),
		embeddings:    make(map[string][][][]float32),
		structured:    make(map[string][]json.RawMessage),
		toolRequests:  make(map[string][]llm.ToolCallRequest),
	}
}

func (g *Gateway) SetCapabilities(profile string, caps ...llm.Capability) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.caps[profile] = make(map[llm.Capability]bool, len(caps))
	for _, cap := range caps {
		g.caps[profile][cap] = true
	}
}

func (g *Gateway) QueueChat(profile string, response llm.ChatResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.responses[profile] = append(g.responses[profile], response)
}

func (g *Gateway) QueueToolCall(profile string, response llm.ToolCallResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.toolResponses[profile] = append(g.toolResponses[profile], response)
}

func (g *Gateway) QueueStructured(profile string, response json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.structured[profile] = append(g.structured[profile], append(json.RawMessage(nil), response...))
}

func (g *Gateway) QueueEmbedding(profile string, response [][]float32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.embeddings[profile] = append(g.embeddings[profile], cloneEmbeddings(response))
}

func (g *Gateway) Supports(profile string, cap llm.Capability) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if caps, ok := g.caps[profile]; ok {
		return caps[cap]
	}
	return cap == llm.CapChat || cap == llm.CapEmbed
}

func (g *Gateway) Chat(ctx context.Context, profile string, req llm.ChatRequest) (llm.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return llm.ChatResponse{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	queue := g.responses[profile]
	if len(queue) == 0 {
		return llm.ChatResponse{}, ErrNoResponse
	}
	response := queue[0]
	g.responses[profile] = queue[1:]
	return response, nil
}

func (g *Gateway) ChatWithTools(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	if err := ctx.Err(); err != nil {
		return llm.ToolCallResponse{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.toolRequests[profile] = append(g.toolRequests[profile], req)
	queue := g.toolResponses[profile]
	if len(queue) == 0 {
		return llm.ToolCallResponse{}, ErrNoResponse
	}
	response := queue[0]
	g.toolResponses[profile] = queue[1:]
	return response, nil
}

func (g *Gateway) StructuredChat(ctx context.Context, profile string, schema json.RawMessage, req llm.ChatRequest) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	queue := g.structured[profile]
	if len(queue) == 0 {
		return nil, ErrNoResponse
	}
	response := append(json.RawMessage(nil), queue[0]...)
	g.structured[profile] = queue[1:]
	return response, nil
}

func (g *Gateway) ToolRequests(profile string) []llm.ToolCallRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	requests := g.toolRequests[profile]
	out := make([]llm.ToolCallRequest, len(requests))
	copy(out, requests)
	return out
}

func (g *Gateway) StreamChat(ctx context.Context, profile string, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := g.Chat(ctx, profile, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Content: response.Message.Content, Done: true, Usage: response.Usage}
	close(ch)
	return ch, nil
}

func (g *Gateway) Embed(ctx context.Context, profile string, input []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	queue := g.embeddings[profile]
	if len(queue) == 0 {
		return nil, ErrNoResponse
	}
	response := cloneEmbeddings(queue[0])
	g.embeddings[profile] = queue[1:]
	return response, nil
}

func cloneEmbeddings(vectors [][]float32) [][]float32 {
	out := make([][]float32, len(vectors))
	for index, vector := range vectors {
		out[index] = append([]float32(nil), vector...)
	}
	return out
}
