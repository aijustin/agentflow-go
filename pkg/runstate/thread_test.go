package runstate

import "testing"

func TestResolveThreadID(t *testing.T) {
	if got := ResolveThreadID(RunSnapshot{RunID: "run-a"}); got != "run-a" {
		t.Fatalf("got %q", got)
	}
	if got := ResolveThreadID(RunSnapshot{RunID: "run-b", ThreadID: "thread-1"}); got != "thread-1" {
		t.Fatalf("got %q", got)
	}
}
