package agentflow

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func tokenFrame(content string) StreamFrame {
	chunk := llm.ChatChunk{Content: content}
	return StreamFrame{Kind: StreamFrameToken, Chunk: &chunk}
}

func eventFrame(runID, eventType string) StreamFrame {
	event := core.Event{Type: core.EventType(eventType), RunID: runID}
	return StreamFrame{Kind: StreamFrameEvent, Event: &event}
}

func doneFrame(status runstate.RunStatus) StreamFrame {
	return StreamFrame{Kind: StreamFrameDone, Result: &RunResult{RunID: "run-hub", Status: status}}
}

// drainSub reads a subscriber channel until it closes (or the test deadline
// hits) and returns the received frames.
func drainSub(t *testing.T, sub *streamSubscriber) []StreamFrame {
	t.Helper()
	var frames []StreamFrame
	timeout := time.After(10 * time.Second)
	for {
		select {
		case frame, ok := <-sub.ch:
			if !ok {
				return frames
			}
			frames = append(frames, frame)
		case <-timeout:
			t.Fatal("timed out draining subscriber")
		}
	}
}

// TestStreamHubSlowSubscriberDropsEventsNotTokens: a subscriber that never
// reads falls behind; event frames are dropped with a cumulative events_lost
// marker while every token frame is delivered in order.
func TestStreamHubSlowSubscriberDropsEventsNotTokens(t *testing.T) {
	hub := newStreamHub()
	hub.register("run-hub")
	sub, err := hub.attach("run-hub", core.EventFilterPreset(""))
	if err != nil {
		t.Fatal(err)
	}
	const tokens = 300
	const events = 300
	for i := 0; i < tokens; i++ {
		hub.publish("run-hub", tokenFrame(fmt.Sprintf("t%d", i)))
		hub.publish("run-hub", eventFrame("run-hub", fmt.Sprintf("Event%d", i)))
	}
	hub.publish("run-hub", doneFrame(runstate.RunStatusCompleted))

	frames := drainSub(t, sub)
	var gotTokens []string
	gotEvents := 0
	var lost int64
	sawDone := false
	for _, frame := range frames {
		switch frame.Kind {
		case StreamFrameToken:
			gotTokens = append(gotTokens, frame.Chunk.Content)
		case StreamFrameEvent:
			gotEvents++
		case StreamFrameEventsLost:
			lost = frame.EventsLost
		case StreamFrameDone:
			sawDone = true
		}
	}
	if len(gotTokens) != tokens {
		t.Fatalf("token frames must never be dropped: got %d of %d", len(gotTokens), tokens)
	}
	for i, content := range gotTokens {
		if content != fmt.Sprintf("t%d", i) {
			t.Fatalf("token order broken at %d: %q", i, content)
		}
	}
	if gotEvents == events {
		t.Fatal("expected the slow subscriber to lose event frames")
	}
	if int64(gotEvents)+lost != events {
		t.Fatalf("events received (%d) + lost (%d) must equal published (%d)", gotEvents, lost, events)
	}
	if lost == 0 {
		t.Fatal("expected an events_lost marker with the cumulative count")
	}
	if !sawDone {
		t.Fatal("terminal Done frame must be delivered")
	}
}

// TestStreamHubAttachReplaysRingWithGapMarker: ring overflow evicts the
// oldest frames; a late attach first observes an events_lost gap marker and
// then the surviving tail in order.
func TestStreamHubAttachReplaysRingWithGapMarker(t *testing.T) {
	hub := newStreamHub()
	hub.register("run-hub")
	const published = streamHubRingCapacity + 76
	for i := 0; i < published; i++ {
		hub.publish("run-hub", tokenFrame(fmt.Sprintf("t%d", i)))
	}
	sub, err := hub.attach("run-hub", core.EventFilterPreset(""))
	if err != nil {
		t.Fatal(err)
	}
	hub.publish("run-hub", doneFrame(runstate.RunStatusCompleted))

	frames := drainSub(t, sub)
	if len(frames) != streamHubRingCapacity+2 { // gap marker + ring tail + done
		t.Fatalf("got %d frames, want %d", len(frames), streamHubRingCapacity+2)
	}
	if frames[0].Kind != StreamFrameEventsLost || frames[0].EventsLost != 76 {
		t.Fatalf("first frame must be the gap marker for 76 evicted frames: %+v", frames[0])
	}
	if frames[1].Kind != StreamFrameToken || frames[1].Chunk.Content != "t76" {
		t.Fatalf("replay must start at the oldest surviving frame: %+v", frames[1])
	}
	last := frames[len(frames)-2]
	if last.Kind != StreamFrameToken || last.Chunk.Content != fmt.Sprintf("t%d", published-1) {
		t.Fatalf("replay must end at the newest frame: %+v", last)
	}
	if frames[len(frames)-1].Kind != StreamFrameDone {
		t.Fatalf("terminal frame must close the replay: %+v", frames[len(frames)-1])
	}
}

// TestStreamHubGracePeriodExpiry: a terminal session stays attachable for the
// grace period and is reclaimed afterwards; attaching then reports no active
// session so callers fall back to the event store.
func TestStreamHubGracePeriodExpiry(t *testing.T) {
	defer func(orig time.Duration) { streamSessionGracePeriod = orig }(streamSessionGracePeriod)
	streamSessionGracePeriod = 50 * time.Millisecond

	hub := newStreamHub()
	hub.register("run-hub")
	hub.publish("run-hub", tokenFrame("hello"))
	hub.publish("run-hub", doneFrame(runstate.RunStatusCompleted))

	sub, err := hub.attach("run-hub", core.EventFilterPreset(""))
	if err != nil {
		t.Fatalf("attach within grace must succeed: %v", err)
	}
	frames := drainSub(t, sub)
	if len(frames) != 2 || frames[1].Kind != StreamFrameDone {
		t.Fatalf("expected replay + done within grace, got %+v", frames)
	}

	time.Sleep(200 * time.Millisecond)
	if _, err := hub.attach("run-hub", core.EventFilterPreset("")); !errors.Is(err, errStreamRunNotActive) {
		t.Fatalf("attach after grace must report no active session, got %v", err)
	}
}

// TestStreamHubPauseIsNotTerminal: a paused Done keeps the session attachable
// and open; the post-resume terminal frame closes subscriber channels.
func TestStreamHubPauseIsNotTerminal(t *testing.T) {
	hub := newStreamHub()
	hub.register("run-hub")
	hub.publish("run-hub", tokenFrame("partial"))
	hub.publish("run-hub", doneFrame(runstate.RunStatusPaused))

	sub, err := hub.attach("run-hub", core.EventFilterPreset(""))
	if err != nil {
		t.Fatalf("paused session must stay attachable: %v", err)
	}
	if !hub.sessionActive("run-hub") {
		t.Fatal("paused session must stay active")
	}

	// Resume: more frames, then the real terminal frame.
	hub.publish("run-hub", tokenFrame("rest"))
	hub.publish("run-hub", doneFrame(runstate.RunStatusCompleted))

	frames := drainSub(t, sub)
	var kinds []StreamFrameKind
	for _, frame := range frames {
		kinds = append(kinds, frame.Kind)
	}
	want := []StreamFrameKind{StreamFrameToken, StreamFrameDone, StreamFrameToken, StreamFrameDone}
	if len(kinds) != len(want) {
		t.Fatalf("got kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("got kinds %v, want %v", kinds, want)
		}
	}
	if frames[1].Result.Status != runstate.RunStatusPaused || frames[3].Result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected done statuses: %+v", frames)
	}
	if hub.sessionActive("run-hub") {
		t.Fatal("terminal done must end session activity")
	}
}

// TestStreamHubDetachOnContextCancel detaches a live subscriber.
func TestStreamHubDetachOnContextCancel(t *testing.T) {
	hub := newStreamHub()
	hub.register("run-hub")
	sub, err := hub.attach("run-hub", core.EventFilterPreset(""))
	if err != nil {
		t.Fatal(err)
	}
	hub.detach("run-hub", sub.id)
	select {
	case _, ok := <-sub.ch:
		if ok {
			t.Fatal("expected closed channel after detach")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detach must close the subscriber channel")
	}
}
