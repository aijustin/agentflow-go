package orchestration

import (
	"encoding/json"
	"testing"
)

func TestResolveRuntimeSecrets(t *testing.T) {
	raw := json.RawMessage(`{"token":"${secret:api_key}","nested":{"key":"${secret:nested}"}}`)
	out, err := resolveRuntimeSecrets(raw, map[string]string{"api_key": "abc", "nested": "xyz"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["token"] != "abc" {
		t.Fatalf("expected resolved token, got %+v", decoded["token"])
	}
	nested := decoded["nested"].(map[string]any)
	if nested["key"] != "xyz" {
		t.Fatalf("expected nested secret, got %+v", nested["key"])
	}
}

func TestResolveRuntimeSecretsMissing(t *testing.T) {
	_, err := resolveRuntimeSecrets(json.RawMessage(`{"token":"${secret:missing}"}`), map[string]string{})
	if err == nil {
		t.Fatal("expected missing secret error")
	}
}
