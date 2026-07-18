package toolorch_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func TestFreezeSamplingStepContext(t *testing.T) {
	step := toolorch.FreezeSamplingStepContext([]llm.ToolSpec{
		{Name: "echo"},
		{Name: "http"},
		{Name: "echo"},
	})
	if !step.Frozen() {
		t.Fatal("expected frozen")
	}
	if !step.Allows("echo") || !step.Allows("http") {
		t.Fatalf("expected advertised tools allowed, got %#v", step.AdvertisedTools)
	}
	if step.Allows("shell") {
		t.Fatal("unexpected tool allowed")
	}
}

func TestSamplingStepContextZeroAllowsAll(t *testing.T) {
	var step toolorch.SamplingStepContext
	if step.Frozen() {
		t.Fatal("zero value should not be frozen")
	}
	if !step.Allows("anything") {
		t.Fatal("zero value must allow all")
	}
}
