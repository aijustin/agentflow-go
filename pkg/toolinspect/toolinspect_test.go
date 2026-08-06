package toolinspect

import (
	"context"
	"errors"
	"testing"
)

type stubInspector struct {
	name    string
	finding Finding
	err     error
	calls   *int
}

func (s stubInspector) Name() string { return s.name }

func (s stubInspector) Inspect(_ context.Context, _ *Request) (Finding, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.finding, s.err
}

func TestVerdictString(t *testing.T) {
	cases := []struct {
		verdict Verdict
		want    string
	}{
		{VerdictAllow, "allow"},
		{VerdictDeny, "deny"},
		{VerdictRequireApproval, "require_approval"},
		{Verdict(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.verdict.String(); got != tc.want {
			t.Fatalf("Verdict(%d).String()=%q want %q", tc.verdict, got, tc.want)
		}
	}
}

func TestFindingEventReasonOrDefault(t *testing.T) {
	cases := []struct {
		name    string
		finding Finding
		want    string
	}{
		{"falls back to reason", Finding{Reason: "denied"}, "denied"},
		{"explicit event reason wins", Finding{Reason: "generic", EventReason: "detailed"}, "detailed"},
		{"empty", Finding{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.finding.EventReasonOrDefault(); got != tc.want {
				t.Fatalf("EventReasonOrDefault()=%q want %q", got, tc.want)
			}
		})
	}
}

func TestChainInspect(t *testing.T) {
	cases := []struct {
		name        string
		inspectors  []Inspector
		wantVerdict Verdict
		wantReason  string
		wantCalls   []int // expected call counts per inspector
		wantErr     bool
	}{
		{
			name:        "empty chain allows",
			inspectors:  nil,
			wantVerdict: VerdictAllow,
		},
		{
			name: "all allow passes through",
			inspectors: []Inspector{
				stubInspector{name: "a", finding: AllowFinding},
				stubInspector{name: "b", finding: AllowFinding},
			},
			wantVerdict: VerdictAllow,
			wantCalls:   []int{1, 1},
		},
		{
			name: "deny short-circuits",
			inspectors: []Inspector{
				stubInspector{name: "a", finding: AllowFinding},
				stubInspector{name: "b", finding: Deny("custom", "nope")},
				stubInspector{name: "c", finding: AllowFinding},
			},
			wantVerdict: VerdictDeny,
			wantReason:  "nope",
			wantCalls:   []int{1, 1, 0},
		},
		{
			name: "require approval short-circuits",
			inspectors: []Inspector{
				stubInspector{name: "a", finding: RequireApproval("ask human")},
				stubInspector{name: "b", finding: AllowFinding},
			},
			wantVerdict: VerdictRequireApproval,
			wantReason:  "ask human",
			wantCalls:   []int{1, 0},
		},
		{
			name: "error aborts the chain",
			inspectors: []Inspector{
				stubInspector{name: "a", err: errors.New("boom")},
				stubInspector{name: "b", finding: AllowFinding},
			},
			wantErr:   true,
			wantCalls: []int{1, 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counts := make([]int, len(tc.inspectors))
			withCounters := make([]Inspector, len(tc.inspectors))
			for i, inspector := range tc.inspectors {
				stub := inspector.(stubInspector)
				stub.calls = &counts[i]
				withCounters[i] = stub
			}
			finding, err := NewChain(withCounters...).Inspect(context.Background(), &Request{})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr {
				if finding.Verdict != tc.wantVerdict {
					t.Fatalf("verdict=%v want %v", finding.Verdict, tc.wantVerdict)
				}
				if finding.Reason != tc.wantReason {
					t.Fatalf("reason=%q want %q", finding.Reason, tc.wantReason)
				}
			}
			for i, want := range tc.wantCalls {
				if counts[i] != want {
					t.Fatalf("inspector %d calls=%d want %d", i, counts[i], want)
				}
			}
		})
	}
}

func TestChainAppendPrepend(t *testing.T) {
	base := NewChain(stubInspector{name: "builtin"})
	chain := base.Prepend(stubInspector{name: "pre"}).Append(stubInspector{name: "post"})
	names := make([]string, 0, chain.Len())
	for _, inspector := range chain.Inspectors() {
		names = append(names, inspector.Name())
	}
	want := []string{"pre", "builtin", "post"}
	if len(names) != len(want) {
		t.Fatalf("names=%v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names=%v want %v", names, want)
		}
	}
	// The base chain is unchanged (copy-on-write).
	if base.Len() != 1 {
		t.Fatalf("base chain mutated: len=%d", base.Len())
	}
}

func TestNewChainDropsNil(t *testing.T) {
	chain := NewChain(nil, stubInspector{name: "a"}, nil)
	if chain.Len() != 1 {
		t.Fatalf("len=%d want 1", chain.Len())
	}
}
