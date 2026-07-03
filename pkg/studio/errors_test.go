package studio_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/studio"
)

func TestErrorPayloadFromNil(t *testing.T) {
	payload := studio.ErrorPayloadFrom(nil)
	if payload.Code != "studio.internal" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestErrorPayloadFromKnownErrors(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{studio.ErrGraphRequired, "studio.graph_required"},
		{studio.ErrRunStateMissing, "studio.run_state_missing"},
		{studio.ErrCheckpointMissing, "studio.checkpoint_missing"},
		{studio.ErrCompareRunsMissing, "studio.compare_runs_missing"},
		{studio.ErrUnsupportedMode, "studio.unsupported_mode"},
		{studio.ErrNodeIDRequired, "obs.node_id_required"},
		{studio.ErrVersionRequired, "obs.version_required"},
		{studio.ErrStreamingUnsupported, "obs.streaming_unsupported"},
		{fmt.Errorf("graph: invalid node"), "graph.invalid"},
		{fmt.Errorf("decode body: unexpected EOF"), "studio.invalid_json"},
		{fmt.Errorf("run compare is not configured"), "obs.not_configured"},
	}
	for _, tc := range cases {
		payload := studio.ErrorPayloadFrom(tc.err)
		if payload.Code != tc.code {
			t.Fatalf("for %v expected code %q, got %+v", tc.err, tc.code, payload)
		}
	}
}

func TestFormatMessageWithParams(t *testing.T) {
	msg := studio.FormatMessage(studio.ErrorPayload{
		Code:    "graph.duplicate_node",
		Message: "duplicate node",
		Params:  map[string]string{"id": "review"},
	})
	if msg == "" || msg == "duplicate node" {
		t.Fatalf("expected formatted message with code, got %q", msg)
	}
}

func TestCodedErrorStringNil(t *testing.T) {
	var coded *studio.CodedError
	if coded.Error() != "" {
		t.Fatal("nil coded error should return empty string")
	}
}

func TestWrapGraphErrorPassthroughInternal(t *testing.T) {
	original := errors.New("database unavailable")
	if studio.WrapGraphError(original) != original {
		t.Fatal("internal errors should pass through unchanged")
	}
	wrapped := studio.WrapGraphError(errors.New("graph: duplicate workflow node \"dup\""))
	var coded *studio.CodedError
	if !errors.As(wrapped, &coded) || coded.Code != "graph.duplicate_node" {
		t.Fatalf("expected coded graph error, got %v", wrapped)
	}
}

func TestErrorPayloadFromNotConfiguredFeatures(t *testing.T) {
	cases := []struct {
		msg     string
		feature string
	}{
		{"run steps is not configured", "run_steps"},
		{"studio save is not configured", "studio_save"},
		{"run fork is not configured", "run_fork"},
		{"graph export is not configured", "graph_export"},
		{"resume-from-step is not configured", "resume_from_step"},
		{"checkpoint loading is not configured", "checkpoint_loading"},
		{"resume-from-checkpoint is not configured", "resume_from_checkpoint"},
		{"run compare is not configured", "run_compare"},
		{"studio validate is not configured", "studio_validate"},
		{"studio codegen is not configured", "studio_codegen"},
		{"studio yaml is not configured", "studio_yaml"},
		{"studio run is not configured", "studio_run"},
		{"run thread is not configured", "run_thread"},
		{"unknown feature is not configured", "feature"},
	}
	for _, tc := range cases {
		payload := studio.ErrorPayloadFrom(errors.New(tc.msg))
		if payload.Code != "obs.not_configured" || payload.Params["feature"] != tc.feature {
			t.Fatalf("for %q got %+v", tc.msg, payload)
		}
	}
}

func TestErrorPayloadFromCheckpointHistoryMessage(t *testing.T) {
	payload := studio.ErrorPayloadFrom(errors.New("checkpoint history is not configured"))
	if payload.Code != "studio.checkpoint_missing" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestPayloadFromUsesCustomMessage(t *testing.T) {
	payload := studio.ErrorPayloadFrom(errors.New("graph is required for this request"))
	if payload.Code != "studio.graph_required" || payload.Message != "graph is required for this request" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
