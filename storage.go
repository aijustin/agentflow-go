package agentflow

import (
	"context"
	"net/http"

	blobs3 "github.com/aijustin/agentflow-go/internal/adapter/blob/s3"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// --- S3 Blob Store ---

type S3BlobStoreConfig struct {
	Endpoint        string
	Bucket          string
	Region          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	HTTPClient      *http.Client
}

// NewS3BlobStore creates an S3-compatible blob store for large runtime and
// workflow outputs. It uses path-style object URLs, AWS Signature Version 4,
// and supports providers whose S3-compatible PUT/GET behavior has been tested.
func NewS3BlobStore(config S3BlobStoreConfig) (runstate.BlobStore, error) {
	return blobs3.NewStore(blobs3.Config{
		Endpoint:        config.Endpoint,
		Bucket:          config.Bucket,
		Region:          config.Region,
		Prefix:          config.Prefix,
		AccessKeyID:     config.AccessKeyID,
		SecretAccessKey: config.SecretAccessKey,
		SessionToken:    config.SessionToken,
		Client:          config.HTTPClient,
	})
}

// --- Orphan Blob GC ---

// PurgeOrphanBlobs deletes blob objects that are no longer referenced by any run snapshot
// for the current scenario (and tenant, when a principal is present).
func (f *Framework) PurgeOrphanBlobs(ctx context.Context) (int, error) {
	if f.runs == nil || f.blobs == nil {
		return 0, nil
	}
	filter := runstate.ListFilter{ScenarioName: f.currentScenario().Name}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	return runstate.PurgeOrphanBlobs(ctx, f.runs, f.blobs, filter)
}
