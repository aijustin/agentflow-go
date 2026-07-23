// Package interjection formats and buffers mid-turn user messages.
package interjection

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// LargePromptThreshold matches grok-build's mid-turn truncation limit.
const LargePromptThreshold = 25_000

// Format wraps interjection text as a synthetic user message.
func Format(text string) string {
	truncated := truncateUTF8(strings.TrimSpace(text), LargePromptThreshold)
	return "The user sent a message while you were working:\n<user_query>\n" + truncated + "\n</user_query>"
}

func truncateUTF8(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	end := 0
	for i, r := range text {
		if i >= limit {
			break
		}
		end = i + utf8.RuneLen(r)
	}
	if end <= 0 || end >= len(text) {
		return text
	}
	return text[:end] + "... [truncated]"
}

// Buffer holds pending mid-turn interjections keyed by run ID.
type Buffer struct {
	mu    sync.Mutex
	byRun map[string][]string
}

// NewBuffer creates an empty interjection buffer.
func NewBuffer() *Buffer {
	return &Buffer{byRun: make(map[string][]string)}
}

// Push queues a raw user message for later drain into the tool loop.
func (b *Buffer) Push(runID, text string) {
	if b == nil {
		return
	}
	text = strings.TrimSpace(text)
	if runID == "" || text == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byRun == nil {
		b.byRun = make(map[string][]string)
	}
	b.byRun[runID] = append(b.byRun[runID], text)
}

// Drain removes and returns all pending messages for runID (oldest first).
func (b *Buffer) Drain(runID string) []string {
	if b == nil || runID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := b.byRun[runID]
	if len(pending) == 0 {
		return nil
	}
	delete(b.byRun, runID)
	out := make([]string, len(pending))
	copy(out, pending)
	return out
}

// PendingCount reports how many messages are queued for runID.
func (b *Buffer) PendingCount(runID string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.byRun[runID])
}
