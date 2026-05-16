package core

import "fmt"

// Decision is the human decision made at a human-in-the-loop checkpoint.
type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
	DecisionAmend   Decision = "amend"
)

func (d Decision) Valid() bool {
	switch d {
	case DecisionApprove, DecisionReject, DecisionAmend:
		return true
	default:
		return false
	}
}

func (d Decision) String() string {
	return string(d)
}

func (d *Decision) UnmarshalText(b []byte) error {
	v := Decision(b)
	if !v.Valid() {
		return fmt.Errorf("core: invalid decision %q", string(b))
	}
	*d = v
	return nil
}
