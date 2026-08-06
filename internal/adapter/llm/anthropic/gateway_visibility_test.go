package anthropic

import (
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestAnthropicMessagesDualVisibilityProjection(t *testing.T) {
	markedUser := llm.Message{Role: llm.RoleUser, Content: "old question"}
	llm.MarkUserVisibleOnly(&markedUser)
	markedSystem := llm.Message{Role: llm.RoleSystem, Content: "stale system note"}
	llm.MarkUserVisibleOnly(&markedSystem)

	tests := []struct {
		name         string
		messages     []llm.Message
		wantLen      int
		wantSystem   string
		wantNoSystem string
	}{
		{
			name: "no marks passes everything through",
			messages: []llm.Message{
				{Role: llm.RoleSystem, Content: "sys"},
				{Role: llm.RoleUser, Content: "hi"},
			},
			wantLen:    1,
			wantSystem: "sys",
		},
		{
			name: "user-only messages are filtered for the model",
			messages: []llm.Message{
				{Role: llm.RoleSystem, Content: "sys"},
				markedSystem,
				markedUser,
				{Role: llm.RoleUser, Content: "latest"},
			},
			wantLen:      1,
			wantSystem:   "sys",
			wantNoSystem: "stale system note",
		},
		{
			name:     "all marked yields an empty projection",
			messages: []llm.Message{markedUser},
			wantLen:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system, got := anthropicMessages(tt.messages)
			if len(got) != tt.wantLen {
				t.Fatalf("got %d messages, want %d: %+v", len(got), tt.wantLen, got)
			}
			if tt.wantSystem != "" && !strings.Contains(system, tt.wantSystem) {
				t.Fatalf("system %q missing %q", system, tt.wantSystem)
			}
			if tt.wantNoSystem != "" && strings.Contains(system, tt.wantNoSystem) {
				t.Fatalf("system %q must not contain %q", system, tt.wantNoSystem)
			}
		})
	}
}
