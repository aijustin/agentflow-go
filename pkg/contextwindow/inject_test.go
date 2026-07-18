package contextwindow_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
)

func TestInsertBeforeLastUserMessage(t *testing.T) {
	msgs := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: "sys"},
		{Role: contextwindow.RoleUser, Content: "u1"},
		{Role: contextwindow.RoleAssistant, Content: "a1"},
		{Role: contextwindow.RoleUser, Content: "u2"},
	}
	out := contextwindow.InsertMessage(msgs, contextwindow.Message{
		Role:    contextwindow.RoleSystem,
		Content: "reminder",
	}, contextwindow.InsertBeforeLastUserMessage)
	if len(out) != 5 {
		t.Fatalf("len=%d", len(out))
	}
	if out[3].Content != "reminder" || out[4].Content != "u2" {
		t.Fatalf("unexpected order: %+v", out)
	}
}

func TestInsertAppend(t *testing.T) {
	msgs := []contextwindow.Message{{Role: contextwindow.RoleUser, Content: "u"}}
	out := contextwindow.InsertMessage(msgs, contextwindow.Message{
		Role: contextwindow.RoleSystem, Content: "tail",
	}, contextwindow.InsertAppend)
	if out[len(out)-1].Content != "tail" {
		t.Fatalf("got %+v", out)
	}
}

func TestInsertBeforeLastUserMessageAppendsWhenNoUser(t *testing.T) {
	msgs := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: "sys"},
		{Role: contextwindow.RoleAssistant, Content: "a"},
	}
	out := contextwindow.InsertMessage(msgs, contextwindow.Message{
		Role: contextwindow.RoleSystem, Content: "reminder",
	}, contextwindow.InsertBeforeLastUserMessage)
	if out[len(out)-1].Content != "reminder" {
		t.Fatalf("expected append when no user, got %+v", out)
	}
}
