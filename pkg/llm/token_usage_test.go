package llm

import "testing"

func TestCacheHitRate(t *testing.T) {
	cases := []struct {
		name  string
		usage TokenUsage
		want  float64
	}{
		{"no prompt", TokenUsage{}, 0},
		{"cold", TokenUsage{InputTokens: 1000}, 0},
		{"warm", TokenUsage{InputTokens: 1000, CachedInputTokens: 900}, 0.9},
		{"fully cached", TokenUsage{InputTokens: 1000, CachedInputTokens: 1000}, 1},
		// A provider that reported more cached than prompt tokens would
		// otherwise yield a rate above 1 and corrupt a dashboard average.
		{"clamped", TokenUsage{InputTokens: 1000, CachedInputTokens: 1200}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.CacheHitRate(); got != tc.want {
				t.Fatalf("CacheHitRate()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestUncachedInputTokens(t *testing.T) {
	cases := []struct {
		name  string
		usage TokenUsage
		want  int
	}{
		{"cold", TokenUsage{InputTokens: 1000}, 1000},
		{"warm", TokenUsage{InputTokens: 1000, CachedInputTokens: 900}, 100},
		{"fully cached", TokenUsage{InputTokens: 1000, CachedInputTokens: 1000}, 0},
		{"never negative", TokenUsage{InputTokens: 1000, CachedInputTokens: 1200}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.UncachedInputTokens(); got != tc.want {
				t.Fatalf("UncachedInputTokens()=%d want %d", got, tc.want)
			}
		})
	}
}
