package llm

import "testing"

func TestParseCapability(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Capability
		ok   bool
	}{
		{name: "chat", raw: "chat", want: CapChat, ok: true},
		{name: "tool", raw: "tool_call", want: CapToolCall, ok: true},
		{name: "structured", raw: "structured_output", want: CapStructuredOutput, ok: true},
		{name: "stream", raw: "stream", want: CapStream, ok: true},
		{name: "embed", raw: "embed", want: CapEmbed, ok: true},
		{name: "unknown", raw: "nope", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseCapability(tt.raw)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseCapability(%q) = %v,%v want %v,%v", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestProfileSupportsUsesExplicitCapabilitiesWhenPresent(t *testing.T) {
	profile := Profile{Capabilities: []Capability{CapChat, CapEmbed}}
	if !ProfileSupports(profile, CapEmbed, CapChat, CapToolCall) {
		t.Fatal("expected explicit embed capability")
	}
	if ProfileSupports(profile, CapToolCall, CapChat, CapToolCall) {
		t.Fatal("explicit capability list should not fall back to defaults")
	}
}

func TestProfileSupportsUsesDefaultsWhenCapabilitiesEmpty(t *testing.T) {
	if !ProfileSupports(Profile{}, CapToolCall, CapChat, CapToolCall) {
		t.Fatal("expected default capability")
	}
	if ProfileSupports(Profile{}, CapEmbed, CapChat, CapToolCall) {
		t.Fatal("unexpected default embed capability")
	}
}
