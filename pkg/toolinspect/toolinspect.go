// Package toolinspect defines the pluggable tool-call inspection pipeline
// used by the runtime's tool dispatcher. Each decision gate of the dispatch
// path (agent whitelist, scenario declaration, input-schema validation,
// approval cache, approval-without-gate denial, executor registry, security
// authorization, executor resolution, doom-loop/rate-cap budget, governance)
// is an Inspector; hosts can prepend or append their own inspectors around
// the built-in chain (agentflow.WithToolInspectors) without forking the
// dispatcher.
package toolinspect

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// Verdict is the outcome of inspecting one tool call.
type Verdict int

const (
	// VerdictAllow lets the call proceed to the next inspector (or to
	// execution when the chain is exhausted).
	VerdictAllow Verdict = iota
	// VerdictDeny rejects the call; the denial becomes the tool result error
	// and a ToolDenied event.
	VerdictDeny
	// VerdictRequireApproval marks the call as needing human approval. Pause
	// decisions are made before dispatch (ToolApprovalEvaluator / HumanGate),
	// so inside the dispatch chain this verdict is settled as an
	// approval-accounted soft denial (kind "approval"), matching the
	// deny-without-gate path.
	VerdictRequireApproval
)

func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictDeny:
		return "deny"
	case VerdictRequireApproval:
		return "require_approval"
	default:
		return "unknown"
	}
}

// Finding is the result of one inspection. The zero value is Allow.
type Finding struct {
	Verdict Verdict
	// Kind classifies the denial in the ToolDenied event payload ("kind" key);
	// empty omits the key, preserving the payload shape of the built-in gates
	// that never carried one.
	Kind string
	// Reason is the model-facing denial text (ToolResult.Error).
	Reason string
	// EventReason overrides the "reason" key of the ToolDenied event payload
	// when it must differ from the model-facing Reason (e.g. the security gate
	// reports the policy error to observers but a generic text to the model).
	// Empty falls back to Reason.
	EventReason string
	// NoteApprovalDeny asks the runtime to account the denial with the HITL
	// deny breaker after the ToolDenied event is emitted (approval-related
	// denials only).
	NoteApprovalDeny bool
}

// AllowFinding is the shared pass verdict.
var AllowFinding = Finding{Verdict: VerdictAllow}

// Deny builds a denial finding with a classification kind ("" for none).
func Deny(kind, reason string) Finding {
	return Finding{Verdict: VerdictDeny, Kind: kind, Reason: reason}
}

// RequireApproval builds an approval-required finding.
func RequireApproval(reason string) Finding {
	return Finding{Verdict: VerdictRequireApproval, Reason: reason}
}

// EventReasonOrDefault returns the reason to publish on the ToolDenied event.
func (f Finding) EventReasonOrDefault() string {
	if f.EventReason != "" {
		return f.EventReason
	}
	return f.Reason
}

// Reservation is the two-phase execution-budget handle a budget inspector
// hands to the dispatcher through the Request. Exactly one settlement method
// runs per reserved call; all are idempotent after the first settlement.
type Reservation interface {
	// CommitSuccess records a successful execution (counts per-tool and
	// per-input totals).
	CommitSuccess()
	// CommitAttempt records a failed or denied attempt (counts per-input only).
	CommitAttempt()
	// Release drops the reservation without counting (context cancellation).
	Release()
}

// Request describes one tool call under inspection. Inspectors run in chain
// order and may enrich the request for later inspectors: the built-in chain
// populates Tool (scenario declaration), Executor (registry resolution), and
// Reservation/CallCount/SameInputCalls (execution budget).
type Request struct {
	RunID string
	Agent core.Agent
	Call  llm.ToolCall
	// Approved reports that the call already passed human approval (approved
	// resume dispatch or full-trust mode).
	Approved bool
	// Tool is the resolved scenario tool declaration; zero until the
	// scenario-declaration stage has run.
	Tool core.Tool
	// Executor is the resolved tool executor; nil until the executor
	// resolution stage has run.
	Executor core.ToolExecutor
	// Reservation holds the execution-budget reservation once the budget
	// inspector reserved the call; the dispatcher settles it exactly once.
	Reservation Reservation
	// CallCount and SameInputCalls are the committed per-tool / per-input
	// counts observed when the budget inspector reserved the call; downstream
	// inspectors (e.g. governance) use them for rate decisions.
	CallCount      int
	SameInputCalls int
}

// Inspector evaluates one tool call and returns a verdict. Implementations
// must be safe for concurrent use: parallel tool batches inspect calls
// concurrently.
type Inspector interface {
	Name() string
	Inspect(ctx context.Context, req *Request) (Finding, error)
}

// InspectorFunc adapts a function to an Inspector with a fixed name.
type InspectorFunc struct {
	InspectorName string
	Fn            func(ctx context.Context, req *Request) (Finding, error)
}

// Name returns the inspector name.
func (f InspectorFunc) Name() string { return f.InspectorName }

// Inspect runs the wrapped function.
func (f InspectorFunc) Inspect(ctx context.Context, req *Request) (Finding, error) {
	return f.Fn(ctx, req)
}

// Chain runs an ordered inspector list with short-circuit semantics: the
// first non-Allow finding (or the first error) stops the chain and is
// returned, matching the historical gate order of the runtime dispatcher.
type Chain struct {
	inspectors []Inspector
}

// NewChain builds a chain; nil inspectors are dropped.
func NewChain(inspectors ...Inspector) Chain {
	chain := Chain{inspectors: make([]Inspector, 0, len(inspectors))}
	for _, inspector := range inspectors {
		if inspector != nil {
			chain.inspectors = append(chain.inspectors, inspector)
		}
	}
	return chain
}

// Inspect runs the chain against req.
func (c Chain) Inspect(ctx context.Context, req *Request) (Finding, error) {
	for _, inspector := range c.inspectors {
		finding, err := inspector.Inspect(ctx, req)
		if err != nil {
			return Finding{}, err
		}
		if finding.Verdict != VerdictAllow {
			return finding, nil
		}
	}
	return AllowFinding, nil
}

// Len reports the number of inspectors in the chain.
func (c Chain) Len() int { return len(c.inspectors) }

// Inspectors returns a copy of the ordered inspector list.
func (c Chain) Inspectors() []Inspector {
	return append([]Inspector(nil), c.inspectors...)
}

// Append returns a new chain with inspectors added at the end.
func (c Chain) Append(inspectors ...Inspector) Chain {
	return NewChain(append(c.Inspectors(), inspectors...)...)
}

// Prepend returns a new chain with inspectors added at the front.
func (c Chain) Prepend(inspectors ...Inspector) Chain {
	return NewChain(append(append([]Inspector(nil), inspectors...), c.Inspectors()...)...)
}
