package agentflow

import (
	"context"
	"fmt"
	"net/http"
	"time"

	retentionhttp "github.com/aijustin/agentflow-go/internal/adapter/retention/http"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type RetentionHTTPHandlerConfig struct {
	Framework    *Framework
	Policy       security.Policy
	Audit        audit.Sink
	MaxBodyBytes int64
}

type retentionAdapter struct {
	framework *Framework
}

func (a *retentionAdapter) PurgeRuns(ctx context.Context, filter runstate.ListFilter) (int, error) {
	return a.framework.PurgeRuns(ctx, filter)
}

func (a *retentionAdapter) PurgeExpired(ctx context.Context, maxAge time.Duration) (int, error) {
	return a.framework.PurgeExpired(ctx, maxAge)
}

func (a *retentionAdapter) PurgeWithPolicy(ctx context.Context, policy retentionhttp.RetentionPolicy) (int, error) {
	return a.framework.PurgeWithPolicy(ctx, RetentionPolicy{
		MaxAge:       policy.MaxAge,
		Status:       policy.Status,
		ScenarioName: policy.ScenarioName,
		Limit:        policy.Limit,
	})
}

func (a *retentionAdapter) PurgeOrphanBlobs(ctx context.Context) (int, error) {
	return a.framework.PurgeOrphanBlobs(ctx)
}

// NewRetentionHTTPHandler serves admin retention routes:
//   - POST /v1/admin/retention/purge-runs
//   - POST /v1/admin/retention/purge-expired
//   - POST /v1/admin/retention/purge-policy
//   - POST /v1/admin/retention/purge-blobs
func NewRetentionHTTPHandler(config RetentionHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: retention handler requires framework")
	}
	return retentionhttp.NewHandler(retentionhttp.HandlerConfig{
		Purger:       &retentionAdapter{framework: config.Framework},
		Policy:       config.Policy,
		Audit:        config.Audit,
		MaxBodyBytes: config.MaxBodyBytes,
	})
}
