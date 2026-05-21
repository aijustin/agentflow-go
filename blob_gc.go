package agentflow

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// PurgeOrphanBlobs deletes blob objects that are no longer referenced by any run snapshot
// for the current scenario (and tenant, when a principal is present).
func (f *Framework) PurgeOrphanBlobs(ctx context.Context) (int, error) {
	if f.runs == nil || f.blobs == nil {
		return 0, nil
	}
	filter := runstate.ListFilter{ScenarioName: f.scenario.Name}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	return runstate.PurgeOrphanBlobs(ctx, f.runs, f.blobs, filter)
}
