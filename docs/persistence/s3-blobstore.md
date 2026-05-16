# S3 兼容 Blob 存储

S3 兼容适配器会在既有 `runstate.BlobStore` 契约背后持久化大型运行时和工作流输出。它面向 MinIO、私有 S3 兼容对象存储，以及 AWS S3 path-style endpoint。

## 依赖策略

适配器只使用 Go 标准库，并用 AWS Signature Version 4 为请求签名。除非应用需要 `BlobStore` 契约之外的 Provider 特定行为，否则不需要额外引入对象存储 SDK。

## 用法

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

## 对象布局

对象按 SHA-256 内容寻址：

```text
/{bucket}/{prefix}/{sha256}.blob
```

返回的 `runstate.BlobRef` 包含对象 ID、字节大小和 SHA-256 校验和。读取时，如果这些字段存在，会校验校验和和大小。

## 安全注意事项

- 凭证是必需的，适配器永远不会记录凭证。
- 使用按环境划分的凭证和最小权限 bucket policy。
- Endpoint 应保持静态，并由可信部署代码配置。
- 任何非本地端点都应使用 TLS。
- 对象存储中只保存大型不透明载荷；元数据和工作流状态保留在 RunState 中。

## 当前范围

- Path-style 对象 URL。
- `PUT` 和 `GET` 操作。
- AWS Signature Version 4。
- 可选 session token 支持。
- 读取时校验校验和和大小。

Provider 特定操作，例如创建 bucket、生命周期策略、对象版本控制、KMS 配置和分片上传，有意留给部署基础设施或未来适配器。