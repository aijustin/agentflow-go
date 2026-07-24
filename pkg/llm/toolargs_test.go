package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestNormalizeToolArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   json.RawMessage
		want string
	}{
		{name: "nil", in: nil, want: `{}`},
		{name: "empty", in: json.RawMessage(""), want: `{}`},
		{name: "whitespace", in: json.RawMessage("  \n\t"), want: `{}`},
		{name: "empty_string_json", in: json.RawMessage(`""`), want: `{}`},
		{name: "object", in: json.RawMessage(`{"title":"x"}`), want: `{"title":"x"}`},
		{name: "encoded_object_string", in: json.RawMessage(`"{\"title\":\"x\"}"`), want: `{"title":"x"}`},
		{name: "truncated_object", in: json.RawMessage(`{"title":`), want: `{}`},
		{name: "truncated_array", in: json.RawMessage(`{"fields":[`), want: `{}`},
		{name: "single_brace", in: json.RawMessage(`{`), want: `{}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := llm.NormalizeToolArguments(tc.in)
			if string(got) != tc.want {
				t.Fatalf("NormalizeToolArguments(%q)=%q want %q", string(tc.in), string(got), tc.want)
			}
			if !json.Valid(got) {
				t.Fatalf("result not valid JSON: %q", string(got))
			}
			calls := []llm.ToolCall{{ID: "c1", Name: "request_user_interaction", Input: got}}
			if _, err := json.Marshal(calls); err != nil {
				t.Fatalf("marshal tool calls: %v", err)
			}
		})
	}
}

func TestNormalizeMessageToolInputsMakesCheckpointMarshalable(t *testing.T) {
	t.Parallel()
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "你好"},
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "request_user_interaction", Input: json.RawMessage(`{"title":"选影院","fields":[`)},
			},
		},
	}
	if _, err := json.Marshal(messages); err == nil {
		t.Fatal("expected truncated input to fail json.Marshal before normalize")
	}
	normalized := llm.NormalizeMessageToolInputs(messages)
	raw, err := json.Marshal(normalized)
	if err != nil {
		t.Fatalf("marshal after normalize: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("normalized messages JSON invalid: %s", raw)
	}
	if string(normalized[1].ToolCalls[0].Input) != `{}` {
		t.Fatalf("input=%s want {}", normalized[1].ToolCalls[0].Input)
	}
}
