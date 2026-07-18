package toolorch_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func TestDenyBreakerTrips(t *testing.T) {
	b := toolorch.NewDenyBreaker(2)
	tripped, n := b.RecordDeny("r1")
	if tripped || n != 1 {
		t.Fatalf("first deny tripped=%v n=%d", tripped, n)
	}
	tripped, n = b.RecordDeny("r1")
	if !tripped || n != 2 {
		t.Fatalf("second deny tripped=%v n=%d", tripped, n)
	}
	b.RecordAllow("r1")
	tripped, n = b.RecordDeny("r1")
	if tripped || n != 1 {
		t.Fatalf("after allow reset tripped=%v n=%d", tripped, n)
	}
}
