package orchestration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveRuntimeSecretsSubstitutesRefs(t *testing.T) {
	raw := json.RawMessage(`{"api_key":"${secret:OPENAI_KEY}","nested":{"token":"${secret:TOKEN}"}}`)
	out, err := resolveRuntimeSecrets(raw, map[string]string{
		"OPENAI_KEY": "sk-test",
		"TOKEN":      "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "sk-test") || !strings.Contains(string(out), "tok") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestResolveRuntimeSecretsPlainString(t *testing.T) {
	out, err := resolveRuntimeSecrets(json.RawMessage(`"${secret:KEY}"`), map[string]string{"KEY": "value"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"value"` {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestResolveRuntimeSecretsMissingRef(t *testing.T) {
	_, err := resolveRuntimeSecrets(json.RawMessage(`{"key":"${secret:MISSING}"}`), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "unresolved runtime secret") {
		t.Fatalf("expected missing secret error, got %v", err)
	}
}

func TestResolveRuntimeSecretsNoopWhenEmpty(t *testing.T) {
	out, err := resolveRuntimeSecrets(nil, nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("expected empty raw, got %q err=%v", out, err)
	}
}
