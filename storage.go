package agentflow

import (
	"net/http"

	blobs3 "github.com/aijustin/agentflow-go/internal/adapter/blob/s3"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

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
// workflow outputs. It uses path-style object URLs and AWS Signature Version 4.
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
