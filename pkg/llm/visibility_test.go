package llm

import "testing"

func TestVisibilityMarks(t *testing.T) {
	t.Run("zero value is visible to both", func(t *testing.T) {
		msg := Message{Role: RoleUser, Content: "hi"}
		if !IsAgentVisible(msg) || !IsUserVisible(msg) {
			t.Fatal("unmarked message must be visible to both audiences")
		}
	})
	t.Run("user only", func(t *testing.T) {
		msg := Message{Role: RoleUser, Content: "hi"}
		MarkUserVisibleOnly(&msg)
		if IsAgentVisible(msg) {
			t.Fatal("user-only message must not be agent-visible")
		}
		if !IsUserVisible(msg) {
			t.Fatal("user-only message must stay user-visible")
		}
		if msg.Metadata[MetadataKeyVisibility] != VisibilityUserOnly {
			t.Fatalf("unexpected metadata: %v", msg.Metadata)
		}
	})
	t.Run("agent only", func(t *testing.T) {
		msg := Message{Role: RoleAssistant, Content: "scratch"}
		MarkAgentVisibleOnly(&msg)
		if !IsAgentVisible(msg) {
			t.Fatal("agent-only message must stay agent-visible")
		}
		if IsUserVisible(msg) {
			t.Fatal("agent-only message must not be user-visible")
		}
	})
	t.Run("mark preserves existing metadata", func(t *testing.T) {
		msg := Message{Role: RoleUser, Metadata: map[string]string{"k": "v"}}
		MarkUserVisibleOnly(&msg)
		if msg.Metadata["k"] != "v" {
			t.Fatalf("existing metadata lost: %v", msg.Metadata)
		}
	})
}

func TestAgentVisibleMessages(t *testing.T) {
	marked := Message{Role: RoleUser, Content: "old"}
	MarkUserVisibleOnly(&marked)
	tests := []struct {
		name     string
		messages []Message
		want     []Message
	}{
		{
			name:     "nil",
			messages: nil,
			want:     nil,
		},
		{
			name: "no marks returns input unchanged",
			messages: []Message{
				{Role: RoleUser, Content: "a"},
				{Role: RoleAssistant, Content: "b"},
			},
			want: []Message{
				{Role: RoleUser, Content: "a"},
				{Role: RoleAssistant, Content: "b"},
			},
		},
		{
			name: "user-only filtered, order preserved",
			messages: []Message{
				{Role: RoleSystem, Content: "sys"},
				marked,
				{Role: RoleUser, Content: "latest"},
			},
			want: []Message{
				{Role: RoleSystem, Content: "sys"},
				{Role: RoleUser, Content: "latest"},
			},
		},
		{
			name:     "all filtered yields empty",
			messages: []Message{marked},
			want:     []Message{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentVisibleMessages(tt.messages)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d messages, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, msg := range got {
				if msg.Role != tt.want[i].Role || msg.Content != tt.want[i].Content {
					t.Fatalf("message %d = %+v, want %+v", i, msg, tt.want[i])
				}
			}
		})
	}
}

func TestAgentVisibleMessagesNoMarkReturnsSameSlice(t *testing.T) {
	messages := []Message{{Role: RoleUser, Content: "a"}}
	got := AgentVisibleMessages(messages)
	if &got[0] != &messages[0] {
		t.Fatal("no-mark fast path must return the input slice without copying")
	}
}

func TestUserVisibleMessages(t *testing.T) {
	agentOnly := Message{Role: RoleAssistant, Content: "scratch"}
	MarkAgentVisibleOnly(&agentOnly)
	messages := []Message{
		{Role: RoleUser, Content: "question"},
		agentOnly,
		{Role: RoleAssistant, Content: "answer"},
	}
	got := UserVisibleMessages(messages)
	if len(got) != 2 || got[0].Content != "question" || got[1].Content != "answer" {
		t.Fatalf("unexpected user projection: %+v", got)
	}
}
