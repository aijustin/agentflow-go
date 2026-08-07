package agentflow

import (
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// TestStreamCallerGoneTimeoutDefaultsOn: the caller-gone fallback is on by
// default (DefaultStreamCallerGoneTimeout); an explicit non-positive option
// disables it.
func TestStreamCallerGoneTimeoutDefaultsOn(t *testing.T) {
	scenario := core.Scenario{
		Name:   "caller-gone-defaults",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant"}},
	}
	fw, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fw.Close(t.Context()) }()
	if fw.streamCallerGoneTimeout != DefaultStreamCallerGoneTimeout {
		t.Fatalf("default caller-gone timeout = %v, want %v", fw.streamCallerGoneTimeout, DefaultStreamCallerGoneTimeout)
	}

	fwOff, err := New(scenario, WithStreamCallerGoneTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fwOff.Close(t.Context()) }()
	if fwOff.streamCallerGoneTimeout != 0 {
		t.Fatalf("explicit WithStreamCallerGoneTimeout(0) must disable the fallback, got %v", fwOff.streamCallerGoneTimeout)
	}

	fwCustom, err := New(scenario, WithStreamCallerGoneTimeout(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fwCustom.Close(t.Context()) }()
	if fwCustom.streamCallerGoneTimeout != 30*time.Second {
		t.Fatalf("custom caller-gone timeout not applied, got %v", fwCustom.streamCallerGoneTimeout)
	}
}
