package interjection_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/interjection"
)

func TestDrainPolicyDefault(t *testing.T) {
	p := interjection.DrainPolicy{}.Normalize()
	if !p.Allow(interjection.DrainBeforeSample, false) {
		t.Fatal("default should allow before_sample")
	}
	if !p.Allow(interjection.DrainAfterToolBatch, false) {
		t.Fatal("default should allow after_tool_batch")
	}
	if p.Allow(interjection.DrainPostCompact, true) {
		t.Fatal("default should not use post_compact")
	}
}

func TestDrainPolicyDeferUntilPostCompact(t *testing.T) {
	p := interjection.DrainPolicy{
		BeforeSample:          true,
		AfterToolBatch:        true,
		DeferUntilPostCompact: true,
	}
	if p.Allow(interjection.DrainBeforeSample, true) {
		t.Fatal("must skip before_sample when just compacted")
	}
	if !p.Allow(interjection.DrainPostCompact, true) {
		t.Fatal("must allow post_compact when just compacted")
	}
	if !p.Allow(interjection.DrainBeforeSample, false) {
		t.Fatal("before_sample ok when not compacted")
	}
}
