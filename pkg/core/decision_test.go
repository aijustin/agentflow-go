package core

import "testing"

func TestDecisionUnmarshalText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Decision
		wantErr bool
	}{
		{name: "approve", input: "approve", want: DecisionApprove},
		{name: "reject", input: "reject", want: DecisionReject},
		{name: "amend", input: "amend", want: DecisionAmend},
		{name: "invalid", input: "skip", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Decision
			err := got.UnmarshalText([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
