# 文件系统工具执行器

`agentflow.NewFilesystemToolExecutor` 将受约束的只读文件系统访问暴露为普通的 `core.ToolExecutor`。它适合可信的本地运行手册、策略文件、已检出的文档、生成报告，以及 Agent 在工作流中可能需要查看的其他文本资产。

## 安全模型

该执行器默认拒绝访问：

- 必须至少配置一个允许访问的根目录。
- 构造时会解析允许访问的根目录，并确认它们确实是目录。
- 请求可以使用允许根目录下的绝对路径，也可以使用相对路径并由允许根目录解析。
- 路径穿越和符号链接逃逸会被 Go 的抗遍历 `os.Root` API 拒绝。
- 目录会被拒绝。
- 文件读取大小受限，默认限制为 1 MiB。
- 结果以结构化 JSON 返回，包含路径、字节大小和内容。

这可以保持运行时治理路径不变：Agent 工具 allowlist、RBAC、审批策略、副作用策略、速率限制、审计和输出脱敏，都会在工具执行前后继续生效。

## 装配

```go
filesystemTool, err := agentflow.NewFilesystemToolExecutor(agentflow.FilesystemToolConfig{
  AllowedRoots: []string{"/srv/agentflow/runbooks", "/srv/agentflow/reports"},
  MaxBytes:     1 << 20,
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithToolExecutor("fs.read", filesystemTool),
)
```

## 工具输入

```json
{
  "path": "incident-response/service-a.md"
}
```

## 工具输出

```json
{
  "path": "/srv/agentflow/runbooks/incident-response/service-a.md",
  "size": 19,
  "content": "Restart service-a..."
}
```

不要把允许根目录指向包含密钥、凭证、SSH key 或宽泛用户主目录的目录。如果需要写访问，请使用带领域校验和审批门禁的专用工具。