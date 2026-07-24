package contextwindow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONAwareTruncateKeepsParseableJSON(t *testing.T) {
	chunks := make([]map[string]any, 0, 40)
	for i := 0; i < 40; i++ {
		chunks = append(chunks, map[string]any{
			"id":      i,
			"content": strings.Repeat("api path /dev-api/login evidence ", 20),
		})
	}
	raw, err := json.Marshal(map[string]any{
		"summary": "login api docs",
		"chunks":  chunks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 8*1024 {
		t.Fatalf("expected fixture >= 8KB, got %d", len(raw))
	}
	out, meta := JSONAwareTruncate(raw, 1024)
	if !meta.Truncated {
		t.Fatal("expected truncation")
	}
	if meta.Strategy != TransformStrategyJSONAware {
		t.Fatalf("strategy=%q", meta.Strategy)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output must be valid JSON: %v\n%s", err, out)
	}
	if _, ok := parsed["summary"]; !ok {
		t.Fatalf("expected summary key preserved: %s", out)
	}
	if _, ok := parsed["chunks"]; !ok {
		t.Fatalf("expected chunks key preserved: %s", out)
	}
}

func TestApplyToolOutputTransformIntegrator(t *testing.T) {
	transforms := map[string]ToolOutputTransform{
		"knowledge_retrieve": func(tool string, raw []byte, limit int) ([]byte, TransformMeta) {
			return []byte(`{"summary":"compact","chunks":[{"content":"x"}]}`), TransformMeta{
				Truncated: true,
				Strategy:  TransformStrategyIntegrator,
			}
		},
	}
	out, meta := ApplyToolOutputTransform("knowledge_retrieve", []byte(`{"huge":true}`), 10, transforms)
	if meta.Strategy != TransformStrategyIntegrator {
		t.Fatalf("strategy=%q", meta.Strategy)
	}
	if string(out) != `{"summary":"compact","chunks":[{"content":"x"}]}` {
		t.Fatalf("unexpected out %s", out)
	}
}

func TestByteTruncateCJKHonorsTokenBudget(t *testing.T) {
	// CJK runes estimate near one token each; a fixed runes*3 cut would
	// exceed the budget threefold.
	raw := []byte(strings.Repeat("排", 100))
	out, meta := ByteTruncate(raw, 10)
	if !meta.Truncated {
		t.Fatal("expected truncation")
	}
	if got := EstimateTokens(string(out)); got > 10 {
		t.Fatalf("truncated output still over budget: %d tokens", got)
	}
}

func TestByteTruncateShortInputUnchanged(t *testing.T) {
	raw := []byte("short ascii")
	out, meta := ByteTruncate(raw, 100)
	if meta.Truncated || string(out) != string(raw) {
		t.Fatalf("short input should pass through, got %q meta=%+v", out, meta)
	}
}
