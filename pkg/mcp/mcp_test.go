package mcp

import (
	"encoding/json"
	"testing"
)

func TestProtocolVersionForMode(t *testing.T) {
	tests := []struct {
		mode ProtocolMode
		want string
	}{
		{mode: "", want: ProtocolVersionLegacy},
		{mode: ProtocolModeLegacy, want: ProtocolVersionLegacy},
		{mode: ProtocolModeModern, want: ProtocolVersionModern},
	}
	for _, test := range tests {
		got, err := ProtocolVersionForMode(test.mode)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("mode %q version = %q, want %q", test.mode, got, test.want)
		}
	}
	if _, err := ProtocolVersionForMode("invalid"); err == nil {
		t.Fatal("expected invalid protocol mode error")
	}
}

func TestAddModernRequestMetadata(t *testing.T) {
	options, err := NormalizeClientOptions(ClientOptions{Mode: ProtocolModeModern}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := AddModernRequestMetadata(json.RawMessage(`{"cursor":"next","_meta":{"extension":"kept"}}`), options)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["cursor"] != "next" {
		t.Fatalf("business params were not preserved: %s", raw)
	}
	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("missing _meta object: %s", raw)
	}
	if meta["extension"] != "kept" {
		t.Fatalf("existing metadata was not preserved: %+v", meta)
	}
	if meta["io.modelcontextprotocol/protocolVersion"] != ProtocolVersionModern {
		t.Fatalf("protocol metadata = %+v", meta)
	}
	info, ok := meta["io.modelcontextprotocol/clientInfo"].(map[string]any)
	if !ok || info["name"] != "agentflow-go" || info["version"] != "v1" {
		t.Fatalf("client info metadata = %+v", meta)
	}
	if _, ok := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any); !ok {
		t.Fatalf("client capabilities metadata = %+v", meta)
	}
}

func TestAddModernRequestMetadataRejectsNonObject(t *testing.T) {
	options, err := NormalizeClientOptions(ClientOptions{Mode: ProtocolModeModern}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddModernRequestMetadata(json.RawMessage(`["not","an","object"]`), options); err == nil {
		t.Fatal("expected non-object params to fail")
	}
}
