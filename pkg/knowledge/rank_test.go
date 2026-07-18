package knowledge

import (
	"math"
	"testing"
	"time"
)

func TestApplyTemporalDecayExemptsEvergreen(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	results := []SearchResult{
		{Document: Document{ID: "1", Metadata: map[string]string{"source": "session", "created_at": "1690000000"}}, Score: 1},
		{Document: Document{ID: "2", Metadata: map[string]string{"source": "global", "created_at": "1690000000"}}, Score: 1},
	}
	out := ApplyTemporalDecay(results, DecayConfig{HalfLifeDays: 30, Now: now})
	if out[0].Document.ID != "2" {
		t.Fatalf("expected evergreen ranked first, got %+v", out)
	}
	if out[1].Score >= 1 {
		t.Fatalf("expected session score to decay, got %v", out[1].Score)
	}
	if math.Abs(out[0].Score-1) > 1e-9 {
		t.Fatalf("evergreen score should stay 1, got %v", out[0].Score)
	}
}

func TestApplyMMRReducesNearDuplicates(t *testing.T) {
	results := []SearchResult{
		{Document: Document{ID: "a", Content: "alpha beta gamma delta"}, Score: 1.0},
		{Document: Document{ID: "b", Content: "alpha beta gamma epsilon"}, Score: 0.95},
		{Document: Document{ID: "c", Content: "completely different topic here"}, Score: 0.7},
	}
	out := ApplyMMR(results, MMRConfig{Lambda: 0.5, Limit: 2})
	if len(out) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out))
	}
	ids := map[string]bool{out[0].Document.ID: true, out[1].Document.ID: true}
	if !ids["a"] || !ids["c"] {
		t.Fatalf("expected diverse pair a+c, got %+v", out)
	}
}
