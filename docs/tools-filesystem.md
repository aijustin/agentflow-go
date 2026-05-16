# Filesystem Tool Executor

`agentflow.NewFilesystemToolExecutor` exposes constrained read-only filesystem access as a normal `core.ToolExecutor`. It is intended for trusted local runbooks, policy files, checked-out documentation, generated reports, and other text assets that an agent may inspect during a workflow.

## Safety Model

The executor is deny-by-default:

- At least one allowed root directory is required.
- Allowed roots are resolved and validated as directories during construction.
- Requests may use absolute paths under an allowed root or relative paths resolved against allowed roots.
- Path traversal and symlink escapes are rejected by Go's traversal-resistant `os.Root` APIs.
- Directories are rejected.
- File reads are size-limited. The default limit is 1 MiB.
- The result is structured JSON with path, byte size, and content.

This keeps the runtime governance path intact: agent tool allowlists, RBAC, approval policy, side-effect policy, rate caps, audit, and output redaction still apply before and after tool execution.

## Wiring

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

## Tool Input

```json
{
  "path": "incident-response/service-a.md"
}
```

## Tool Output

```json
{
  "path": "/srv/agentflow/runbooks/incident-response/service-a.md",
  "size": 19,
  "content": "Restart service-a..."
}
```

Do not point allowed roots at directories containing secrets, credentials, SSH keys, or broad home directories. For write access, use a purpose-built tool with domain-specific validation and approval gates.