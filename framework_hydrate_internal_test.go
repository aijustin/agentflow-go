package agentflow

import (
	"encoding/json"
	"testing"
)

func TestMergeWorkflowContextObjectInput(t *testing.T) {
	user := json.RawMessage(`{"input":"hi","topic":"go"}`)
	hydrated := json.RawMessage(`{"steps":{"n1":{"text":"done"}}}`)

	merged, err := mergeWorkflowContext(user, hydrated)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged is not a JSON object: %v", err)
	}
	if string(got["input"]) != `"hi"` {
		t.Fatalf("user input dropped: %s", got["input"])
	}
	if string(got["topic"]) != `"go"` {
		t.Fatalf("user field dropped: %s", got["topic"])
	}
	if _, ok := got["steps"]; !ok {
		t.Fatalf("workflow steps not merged: %s", merged)
	}
}

func TestMergeWorkflowContextNonObjectInput(t *testing.T) {
	user := json.RawMessage(`"plain string prompt"`)
	hydrated := json.RawMessage(`{"steps":{"n1":{"text":"done"}}}`)

	merged, err := mergeWorkflowContext(user, hydrated)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged is not a JSON object: %v", err)
	}
	if string(got["input"]) != `"plain string prompt"` {
		t.Fatalf("non-object input not nested under input: %s", merged)
	}
	if _, ok := got["steps"]; !ok {
		t.Fatalf("workflow steps not merged: %s", merged)
	}
}

func TestMergeWorkflowContextNullInput(t *testing.T) {
	user := json.RawMessage(`null`)
	hydrated := json.RawMessage(`{"steps":{"n1":{"text":"done"}}}`)

	merged, err := mergeWorkflowContext(user, hydrated)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged is not a JSON object: %v", err)
	}
	if string(got["input"]) != "null" {
		t.Fatalf("null input not nested under input: %s", got["input"])
	}
	if _, ok := got["steps"]; !ok {
		t.Fatalf("workflow steps not merged: %s", merged)
	}
}

func TestMergeWorkflowContextPreservesUserSteps(t *testing.T) {
	user := json.RawMessage(`{"steps":{"user":"keep"}}`)
	hydrated := json.RawMessage(`{"steps":{"n1":{"text":"done"}}}`)

	merged, err := mergeWorkflowContext(user, hydrated)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged is not a JSON object: %v", err)
	}
	if string(got["steps"]) != `{"user":"keep"}` {
		t.Fatalf("user-provided steps clobbered: %s", got["steps"])
	}
	if _, ok := got["workflow_steps"]; !ok {
		t.Fatalf("workflow steps not placed under workflow_steps: %s", merged)
	}
}
