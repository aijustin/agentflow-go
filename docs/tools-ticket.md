# Ticket 工具执行器

`agentflow.NewTicketToolExecutor` 将工单读写操作暴露为普通的 `core.ToolExecutor`。它适合客服分派、工单摘要和人工审批前上下文收集等场景。

## 安全模型

- 宿主应用必须提供 `TicketStore` 实现。
- 演示和测试可使用 `agentflow.NewMemoryTicketStore`。
- 生产环境应接入真实工单系统 adapter，并在 store 层 enforce 租户隔离和 RBAC。

Ticket 工具不会自动连接外部 SaaS；store 由宿主应用注入。

## 装配

场景 YAML 中声明 `type: builtin.ticket`，宿主应用通过 `WithToolExecutor` 绑定 executor。

```go
store := agentflow.NewMemoryTicketStore(map[string]agentflow.Ticket{
  "T-9": {ID: "T-9", Title: "Login issue", Status: "open"},
})

ticketTool, err := agentflow.NewTicketToolExecutor(agentflow.TicketToolConfig{
  Store: store,
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.New(builder.MinimalTicketHandling("support"),
  agentflow.WithToolExecutor("ticket", ticketTool),
)
```

## 工具输入

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `action` | 字符串 | 是 | `get`、`update` 或 `comment`。 |
| `id` | 字符串 | 是 | 工单 ID。 |
| `fields` | 字符串映射 | `update` 时 | 可更新 `title`、`status`、`description` 或 metadata 键。 |
| `author` | 字符串 | `comment` 时建议 | 评论作者。 |
| `body` | 字符串 | `comment` 时 | 评论内容。 |

## 工具输出

成功时返回工单 JSON（含 `comments` 和 `updated_at`）。失败时 `ToolResult.Error` 携带原因。

## 示例场景

- `builder.MinimalTicketHandling("support")`：自主模式 + HITL + `scenario.triggers` 事件路由。
