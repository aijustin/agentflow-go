# S3 兼容 Blob 存储

S3 兼容适配器会在既有 `runstate.BlobStore` 契约背后持久化大型运行时和工作流输出。它面向 MinIO、私有 S3 兼容对象存储、AWS S3 path-style endpoint，以及通过验证的云厂商 S3 兼容接口。

## 适用场景

BlobStore 主要用于把大块步骤输出从 RunState 中拆出去，避免 PostgreSQL 行膨胀、Redis 内存膨胀和快照 CAS 更新成本过高。常见载荷包括：

- SQL 或搜索工具返回的大结果集。
- RAG 检索上下文、文档切片和引用材料。
- 模型生成的长 JSON、报告草稿、迁移摘要、PRD 或审计输出。
- 需要在人审暂停、恢复执行、重试或下游步骤中继续读取的中间产物。

当 `scenario.runtime.step_output_threshold` 大于 0，且步骤输出序列化后的字节数超过该阈值时，框架会把输出写入已配置的 BlobStore，并在 RunState 中保存 `runstate.BlobRef`。如果没有配置 BlobStore，或输出未超过阈值，输出会继续内联保存在 RunState 中。

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

场景文件中按字节设置外置阈值：

```yaml
runtime:
    step_output_threshold: 262144
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
- 不要把未脱敏 secret、API key、HITL token 或高敏感原始工具输出直接作为大对象保存；需要先通过治理层脱敏或使用受控 bucket、KMS 和生命周期策略。

## 国内对象存储

当前适配器实现的是 S3-compatible 协议，不是腾讯云 COS 或阿里云 OSS 的原生 SDK 适配器。它使用 path-style URL、AWS Signature Version 4、`PUT` 和 `GET`。因此：

- 腾讯云 COS：如果使用 COS 的 S3 兼容接口，并且目标 endpoint 支持 path-style URL、SigV4、`PUT` 和 `GET`，可以用 `NewS3BlobStore` 接入；上线前应跑一组集成测试确认 region、bucket、临时凭证和签名行为。
- 阿里云 OSS：如果使用 OSS 的 S3 兼容接口，也可以按同样方式验证；如果使用 OSS 原生 API 或原生签名，当前适配器不直接支持。
- 如果组织必须使用 COS/OSS 原生 API、厂商 KMS、原生 STS 语义、生命周期治理或 multipart upload，建议新增独立适配器，例如 `NewTencentCOSBlobStore` 或 `NewAliyunOSSBlobStore`，并继续实现同一个 `runstate.BlobStore` 契约。

最小兼容性验证应覆盖：写入一个 blob、按返回的 `BlobRef` 读取、校验 SHA-256/大小、使用目标生产 region、使用最小权限凭证，以及在 TLS endpoint 下执行。

## 当前范围

- Path-style 对象 URL。
- `PUT` 和 `GET` 操作。
- AWS Signature Version 4。
- 可选 session token 支持。
- 读取时校验校验和和大小。

Provider 特定操作，例如创建 bucket、生命周期策略、对象版本控制、KMS 配置和分片上传，有意留给部署基础设施或未来适配器。