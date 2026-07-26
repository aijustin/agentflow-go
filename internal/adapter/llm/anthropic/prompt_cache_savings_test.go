package anthropic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// Anthropic's published billing multipliers relative to the base input price:
// a cache write costs 25% more than ordinary input, a cache read costs 90%
// less. Output is priced separately and is identical in both arms here, so it
// is left out of the comparison.
const (
	cacheWriteMultiplier = 1.25
	cacheReadMultiplier  = 0.10
	uncachedMultiplier   = 1.00
)

// cacheModel is a stand-in for the provider's prompt cache. It follows the
// documented rule: a breakpoint caches everything before it, and a request is
// charged a cache read for the longest already-cached prefix it presents.
type cacheModel struct{ stored map[string]bool }

func newCacheModel() *cacheModel { return &cacheModel{stored: map[string]bool{}} }

// block is one unit of prompt content, in the order Anthropic assembles it:
// tools, then system, then messages.
type block struct {
	text     string
	endsSpan bool // carries a cache_control breakpoint
}

func tokensOf(text string) int {
	// A fixed 4-chars-per-token rule. The absolute number does not matter for
	// a ratio between two arms that are billed with the same rule.
	return (len(text) + 3) / 4
}

// charge returns (uncached, write, read) token counts for one request.
func (c *cacheModel) charge(blocks []block) (int, int, int) {
	prefixTokens := make([]int, len(blocks)+1)
	hasher := sha256.New()
	keys := make([]string, len(blocks)+1)
	keys[0] = ""
	for i, b := range blocks {
		prefixTokens[i+1] = prefixTokens[i] + tokensOf(b.text)
		hasher.Write([]byte(b.text))
		keys[i+1] = hex.EncodeToString(hasher.Sum(nil))
	}
	total := prefixTokens[len(blocks)]

	// Longest already-cached breakpoint wins the read.
	readUpTo := 0
	for i, b := range blocks {
		if !b.endsSpan {
			continue
		}
		if c.stored[keys[i+1]] {
			readUpTo = i + 1
		}
	}
	// Everything up to the last breakpoint that is not already cached gets
	// written this call.
	writeUpTo := readUpTo
	for i, b := range blocks {
		if b.endsSpan && i+1 > writeUpTo {
			writeUpTo = i + 1
		}
	}
	for i, b := range blocks {
		if b.endsSpan {
			c.stored[keys[i+1]] = true
		}
	}

	read := prefixTokens[readUpTo]
	write := prefixTokens[writeUpTo] - prefixTokens[readUpTo]
	uncached := total - prefixTokens[writeUpTo]
	return uncached, write, read
}

// decodeBlocks flattens an Anthropic request body into ordered blocks, marking
// the ones that carry a cache breakpoint.
func decodeBlocks(t *testing.T, body map[string]any) []block {
	t.Helper()
	var blocks []block
	add := func(value any, marked bool) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal block: %v", err)
		}
		blocks = append(blocks, block{text: string(raw), endsSpan: marked})
	}
	marked := func(m map[string]any) bool {
		_, ok := m["cache_control"]
		return ok
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, tool := range tools {
			m := tool.(map[string]any)
			add(m, marked(m))
		}
	}
	switch system := body["system"].(type) {
	case string:
		add(system, false)
	case []any:
		for _, item := range system {
			m := item.(map[string]any)
			add(m, marked(m))
		}
	}
	if messages, ok := body["messages"].([]any); ok {
		for _, message := range messages {
			m := message.(map[string]any)
			isMarked := false
			if content, ok := m["content"].([]any); ok {
				for _, item := range content {
					if cm, ok := item.(map[string]any); ok && marked(cm) {
						isMarked = true
					}
				}
			}
			add(m, isMarked)
		}
	}
	return blocks
}

// runToolLoop drives `turns` growing requests through the gateway, the way an
// autonomous tool loop does, and returns the total billed input cost in
// base-price token units.
func runToolLoop(t *testing.T, promptCache bool, turns int) (cost float64, cachedTokens, promptTokens int) {
	t.Helper()
	cache := newCacheModel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		uncached, write, read := cache.charge(decodeBlocks(t, body))
		cost += float64(uncached)*uncachedMultiplier +
			float64(write)*cacheWriteMultiplier +
			float64(read)*cacheReadMultiplier
		fmt.Fprintf(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":20}}`,
			uncached, write, read)
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{
		Name:        "claude",
		Model:       "claude-sonnet",
		Endpoint:    server.URL,
		PromptCache: llm.PromptCacheConfig{Enabled: promptCache},
	}}, server.Client())

	// A realistic agent prefix: a sizable system prompt and a tool catalog,
	// both re-sent verbatim on every iteration.
	tools := make([]llm.ToolSpec, 0, 8)
	for i := 0; i < 8; i++ {
		tools = append(tools, llm.ToolSpec{
			Name:        fmt.Sprintf("tool_%d", i),
			Description: strings.Repeat("describes what this tool does in detail. ", 20),
			Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}}}`),
		})
	}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("You are a meticulous operations assistant. ", 60)},
		{Role: llm.RoleUser, Content: "Reconcile the outstanding invoices."},
	}

	for turn := 0; turn < turns; turn++ {
		resp, err := gateway.ChatWithTools(context.Background(), "claude", llm.ToolCallRequest{
			ChatRequest: llm.ChatRequest{Messages: messages},
			Tools:       tools,
		})
		if err != nil {
			t.Fatal(err)
		}
		cachedTokens += resp.Usage.CachedInputTokens
		promptTokens += resp.Usage.InputTokens
		// The loop appends an assistant turn and a tool result, exactly the
		// shape that makes the prefix grow while staying byte-stable.
		messages = append(messages,
			llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("Checking batch %d.", turn)},
			llm.Message{Role: llm.RoleTool, ToolCallID: fmt.Sprintf("call-%d", turn), Content: strings.Repeat("row data ", 30)},
		)
	}
	return cost, cachedTokens, promptTokens
}

// TestPromptCacheReducesToolLoopCost quantifies the change using Anthropic's
// published billing multipliers against a modeled cache. It measures the
// request shape the adapter emits, not live provider billing.
func TestPromptCacheReducesToolLoopCost(t *testing.T) {
	const turns = 12

	coldCost, coldCached, coldPrompt := runToolLoop(t, false, turns)
	warmCost, warmCached, warmPrompt := runToolLoop(t, true, turns)

	t.Logf("%-12s %12s %12s %14s", "prompt_cache", "input_cost", "prompt_tok", "cache_hit_rate")
	t.Logf("%-12s %12.0f %12d %13.1f%%", "off", coldCost, coldPrompt, 100*float64(coldCached)/float64(coldPrompt))
	t.Logf("%-12s %12.0f %12d %13.1f%%", "on", warmCost, warmPrompt, 100*float64(warmCached)/float64(warmPrompt))
	t.Logf("saving over %d turns: %.1f%%", turns, 100*(coldCost-warmCost)/coldCost)

	if coldCached != 0 {
		t.Fatalf("expected no cache reads with caching off, got %d", coldCached)
	}
	if warmCached == 0 {
		t.Fatal("expected cache reads with caching on")
	}
	if warmCost >= coldCost {
		t.Fatalf("expected caching to reduce cost, got %.0f vs %.0f", warmCost, coldCost)
	}
	// The prefix dominates a tool loop, so the saving should be substantial
	// rather than marginal.
	if saving := (coldCost - warmCost) / coldCost; saving < 0.5 {
		t.Fatalf("expected the prefix to dominate and save >50%%, got %.1f%%", 100*saving)
	}
}

// A single call with no reuse pays the write premium and saves nothing, which
// is precisely why prompt caching is opt-in rather than the default.
func TestPromptCacheCostsMoreForASingleCall(t *testing.T) {
	coldCost, _, _ := runToolLoop(t, false, 1)
	warmCost, _, _ := runToolLoop(t, true, 1)
	t.Logf("single call: off=%.0f on=%.0f (write premium %.1f%%)", coldCost, warmCost, 100*(warmCost-coldCost)/coldCost)
	if warmCost <= coldCost {
		t.Fatalf("expected a single uncached call to cost more with caching on, got %.0f vs %.0f", warmCost, coldCost)
	}
}
