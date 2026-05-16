# S3-Compatible Blob Store

The S3-compatible adapter persists large runtime and workflow outputs behind the existing `runstate.BlobStore` contract. It is designed for MinIO, private S3-compatible object stores, and AWS S3 path-style endpoints.

## Dependency Strategy

The adapter uses only the Go standard library and signs requests with AWS Signature Version 4. Applications do not need to add an object-storage SDK unless they want provider-specific behavior outside the `BlobStore` contract.

## Usage

```go
blobs, err := agentflow.NewS3BlobStore(agentflow.S3BlobStoreConfig{
    Endpoint:        os.Getenv("AGENTFLOW_S3_ENDPOINT"),
    Bucket:          os.Getenv("AGENTFLOW_S3_BUCKET"),
    Region:          os.Getenv("AGENTFLOW_S3_REGION"),
    Prefix:          "agentflow/outputs",
    AccessKeyID:     os.Getenv("AGENTFLOW_S3_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("AGENTFLOW_S3_SECRET_ACCESS_KEY"),
    SessionToken:    os.Getenv("AGENTFLOW_S3_SESSION_TOKEN"),
})
if err != nil {
    log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
    "scenario.yaml",
    agentflow.WithBlobStore(blobs),
)
```

## Object Layout

Objects are content-addressed by SHA-256:

```text
/{bucket}/{prefix}/{sha256}.blob
```

The returned `runstate.BlobRef` contains the object ID, byte size, and SHA-256 checksum. Reads verify both checksum and size when those fields are present.

## Security Notes

- Credentials are required and are never logged by the adapter.
- Use per-environment credentials and least-privilege bucket policies.
- Keep endpoints static and configured by trusted deployment code.
- Use TLS for any non-local endpoint.
- Store only large opaque payloads in object storage; metadata and workflow state stay in RunState.

## Current Scope

- Path-style object URLs.
- `PUT` and `GET` operations.
- AWS Signature Version 4.
- Optional session token support.
- Checksum and size validation on read.

Provider-specific operations such as bucket creation, lifecycle policies, object versioning, KMS configuration, and multipart upload are intentionally left to deployment infrastructure or future adapters.