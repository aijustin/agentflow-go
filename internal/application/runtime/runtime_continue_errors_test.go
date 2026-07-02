package runtime

import "testing"

func TestRunPausedErrorMessage(t *testing.T) {
	err := RunPausedError{RunID: "run-1", Kind: "before_final_answer", Token: "tok"}
	if got := err.Error(); got == "" || got == "runtime: run \"run-1\" paused ()" {
		t.Fatalf("unexpected error message: %q", got)
	}
}
