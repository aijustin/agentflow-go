package openai

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestOpenAIMessagesDualVisibilityProjection(t *testing.T) {
	markedUser := llm.Message{Role: llm.RoleUser, Content: "old question"}
	llm.MarkUserVisibleOnly(&markedUser)
	markedAssistant := llm.Message{Role: llm.RoleAssistant, Content: "old answer"}
	llm.MarkUserVisibleOnly(&markedAssistant)

	tests := []struct {
		name     string
		messages []llm.Message
		wantLen  int
		wantLast string
	}{
		{
			name: "no marks passes everything through",
			messages: []llm.Message{
				{Role: llm.RoleSystem, Content: "sys"},
				{Role: llm.RoleUser, Content: "hi"},
			},
			wantLen:  2,
			wantLast: "hi",
		},
		{
			name: "user-only messages are filtered for the model",
			messages: []llm.Message{
				{Role: llm.RoleSystem, Content: "sys"},
				markedUser,
				markedAssistant,
				{Role: llm.RoleUser, Content: "latest"},
			},
			wantLen:  2,
			wantLast: "latest",
		},
		{
			name:     "all marked yields an empty projection",
			messages: []llm.Message{markedUser},
			wantLen:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openAIMessages(tt.messages)
			if len(got) != tt.wantLen {
				t.Fatalf("got %d messages, want %d: %+v", len(got), tt.wantLen, got)
			}
			if tt.wantLen > 0 && got[len(got)-1]["content"] != tt.wantLast {
				t.Fatalf("last message content = %v, want %q", got[len(got)-1]["content"], tt.wantLast)
			}
		})
	}
}
