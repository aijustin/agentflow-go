package coordination

import (
	"errors"
	"testing"
)

func TestLeaseValidate(t *testing.T) {
	if err := (Lease{Key: "k", Owner: "o"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Lease{}).Validate(); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("expected ErrInvalidLease, got %v", err)
	}
}
