# Git 工具执行器

`agentflow.NewGitToolExecutor` 将受约束的只读 Git 命令暴露为普通的 `core.ToolExecutor`。它适合代码审查流水线、变更摘要和仓库状态检查等场景。

## 安全模型

- 宿主应用必须提供至少一个 `AllowedRoots` 路径。
- 仅允许在 allowlist 根目录下的 Git 仓库中执行命令。
- 仓库路径必须包含 `.git` 目录。
- 仅支持只读 action：`diff`、`show`、`log`、`status`。
- 命令通过 `exec.CommandContext` 执行，受 `context.Context` 取消和超时约束。

Git 工具不会替代代码托管平台的权限模型；生产环境应配合最小权限和审计。

## 装配

工具声明与执行器注册分离。场景 YAML 中声明 `type: builtin.git`，宿主应用通过 `WithToolExecutor` 绑定 executor。库不会自动注册 Git executor；`testutil.WiringOptions` 仅在测试与 examples 中注册 demo 工具。

```go
gitTool, err := agentflow.NewGitToolExecutor(agentflow.GitToolConfig{
  AllowedRoots: []string{"/workspace/repos"},
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.New(builder.CodeReviewPipeline(),
  agentflow.WithToolExecutor("git", gitTool),
)
```

## 工具输入

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `action` | 字符串 | 是 | `diff`、`show`、`log` 或 `status`。 |
| `repo` | 字符串 | 是 | Git 仓库根目录路径，必须在 `AllowedRoots` 内。 |
| `ref` | 字符串 | 否 | Git ref；`show` 必填，`diff`/`log` 可选。 |
| `path` | 字符串 | 否 | `diff` 的可选路径过滤。 |

## 工具输出

返回 JSON，包含 `action`、`repo`、`output`，以及可选的 `exit_error`（命令失败时）。

## 示例场景

- `builder.CodeReviewPipeline()`：固定工作流 + `parallel_group` + Git diff 审查。
