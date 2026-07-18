package interjection

import (
	"strings"
	"testing"
)

func TestFormatWrapsUserQuery(t *testing.T) {
	t.Parallel()
	out := Format("stop and fix the test first")
	if !strings.HasPrefix(out, "The user sent a message while you were working:\n<user_query>\n") {
		t.Fatalf("unexpected prefix: %q", out)
	}
	if !strings.HasSuffix(out, "\n</user_query>") {
		t.Fatalf("unexpected suffix: %q", out)
	}
	if !strings.Contains(out, "stop and fix the test first") {
		t.Fatalf("missing body: %q", out)
	}
}

func TestFormatTruncatesAtUTF8Boundary(t *testing.T) {
	t.Parallel()
	out := Format(strings.Repeat("é", LargePromptThreshold))
	if !strings.Contains(out, "... [truncated]") {
		t.Fatalf("expected truncation marker, got len=%d", len(out))
	}
}

func TestBufferPushDrain(t *testing.T) {
	t.Parallel()
	buf := NewBuffer()
	buf.Push("run-1", "one")
	buf.Push("run-1", "two")
	got := buf.Drain("run-1")
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected drain: %+v", got)
	}
	if len(buf.Drain("run-1")) != 0 {
		t.Fatal("expected empty second drain")
	}
}
