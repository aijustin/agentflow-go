package orchestration

import (
	"testing"
)

func TestWorkflowPausedErrorMessage(t *testing.T) {
	err := WorkflowPausedError{Token: "tok", NodeID: "gate"}
	if err.Error() == "" {
		t.Fatal("expected error message")
	}
}

func TestMapKeysHelper(t *testing.T) {
	got := mapKeys(map[string]bool{"a": true, "b": true})
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
