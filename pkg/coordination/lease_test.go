package coordination

import (
	"errors"
	"testing"
)

func TestLeaseValidate(t *testing.T) {
	if err := (Lease{Key: "k", Owner: "o", Token: 1}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Lease{}).Validate(); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("expected ErrInvalidLease, got %v", err)
	}
	if err := (Lease{Key: "k", Owner: "o"}).Validate(); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("expected ErrInvalidLease for zero token, got %v", err)
	}
}
