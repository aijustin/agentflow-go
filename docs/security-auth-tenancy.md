# 安全、认证与租户设计

本文档定义 agentflow-go 的企业控制面边界。目标是在不混淆租户数据、不绕过工具审批的前提下，安全地向部门、项目和服务账号暴露运行时 API。

## 身份模型

生产 API 应携带显式身份上下文：

| 字段 | 作用 |
| --- | --- |
| `tenant_id` | 数据、运行状态、记忆、Blob、审计和指标的硬隔离边界。 |
| `workspace_id` | 租户内的团队或业务单元边界。 |
| `project_id` | 场景和工具所属的产品/应用边界。 |
| `principal_id` | 用户或服务主体身份。 |
| `principal_type` | `user`、`service` 或 `system`。 |
| `roles` | 用于授权的有限角色集合。 |

推荐角色：

- `admin`：管理配置和凭证。
- `operator`：运行和取消工作流。
- `viewer`：查看运行状态和输出。
- `approver`：批准、拒绝或修订 HITL checkpoint。
- `service`：以受限权限提交机器到机器的运行。

## 认证层

可用认证方式：

1. API key 中间件，用于服务到服务调用和第一批生产部署。
2. 静态 JWT Bearer 中间件，用于固定 HS256/RS256 密钥部署。
3. OIDC Discovery/JWKS 自动刷新认证器，用于接入 OIDC/OAuth2 网关或公司 SSO 签发的访问令牌。
4. 可选 mTLS 或私有网络强制策略，用于内部部署。

Debug UI 和生产 API 应保持不同部署模式。Debug 端点只应在 loopback listener 上保留本地开发默认值。

JWT Bearer 使用示例：

```go
authenticator, err := agentflow.NewJWTAuthenticator(agentflow.JWTAuthenticatorConfig{
    Issuer:   "https://sso.example.com/realms/company",
    Audience: "agentflow-api",
    Keys: []agentflow.JWTKey{{
        ID:              "sso-key-1",
        Algorithm:       agentflow.JWTAlgorithmRS256,
        RSAPublicKeyPEM: publicKeyPEM,
    }},
})
if err != nil {
    log.Fatal(err)
}

authMiddleware, err := agentflow.NewJWTMiddleware(agentflow.JWTMiddlewareConfig{
    Authenticator: authenticator,
})
```

令牌 claim 默认读取 `sub`、`tenant_id`、`workspace_id`、`project_id` 和 `roles`。如果公司 SSO 使用不同字段名，可通过 `TenantClaim`、`WorkspaceClaim`、`ProjectClaim` 和 `RolesClaim` 覆盖。

OIDC/JWKS 自动刷新示例：

```go
authenticator, err := agentflow.NewOIDCJWTAuthenticator(agentflow.OIDCJWTAuthenticatorConfig{
    Issuer:          "https://sso.example.com/realms/company",
    Audience:        "agentflow-api",
    DiscoveryURL:    "https://sso.example.com/realms/company/.well-known/openid-configuration",
    RefreshInterval: 5 * time.Minute,
})
if err != nil {
    log.Fatal(err)
}

authMiddleware, err := agentflow.NewJWTMiddleware(agentflow.JWTMiddlewareConfig{
    Authenticator: authenticator,
})
```

未知 `kid` 会触发一次同步 JWKS 刷新，用于密钥轮换窗口。若不使用 Discovery，也可以直接传入 `JWKSURL`。

## 授权策略

授权应该是端口，而不是硬编码在 HTTP Handler 中：

```go
type Policy interface {
    Authorize(ctx context.Context, principal Principal, action Action, resource Resource) error
}
```

初始动作：

- `run.submit`
- `run.read`
- `run.cancel`
- `hitl.resume`
- `tool.invoke`
- `memory.read`
- `memory.write`
- `admin.configure`

危险工具执行前，以及 HITL 决策被接受前，必须执行授权。

## 租户隔离

租户上下文必须流经：

- 运行快照。
- 任务队列载荷和状态。
- 记忆命名空间。
- Blob 引用和对象前缀。
- 事件 sink 和审计 sink。
- 具有有限基数的指标标签。

未来持久化记录应显式包含租户 ID，不应从运行 ID 中推断。

## 密钥处理

- 密钥从可信配置或密钥管理器读取。
- 密钥绝不出现在日志、事件、快照、调试响应或审计载荷中。
- Provider 凭证和工具凭证应按租户/工作区/项目设定作用域。
- 对已知密钥字段，脱敏必须失败关闭。

## 第一批实现切片

1. 增加 `pkg/security` 或 `pkg/identity` 的主体与租户上下文类型。已在 `pkg/identity` 中完成。
2. 为 HTTP Handler 增加 API-key 中间件。已通过 `NewStaticAPIKeyAuthenticator` 和 `NewAPIKeyMiddleware` 完成。
3. 增加策略端口和用于测试的 allowlist 实现。已在 `pkg/security` 中完成。
4. 围绕运行提交、恢复、读取、取消和工具调用增加授权检查。Debug HTTP 运行提交/读取/恢复、异步 HTTP 提交/状态/取消和运行时工具调用已实现。
5. 为接受和拒绝决策增加审计事件。已覆盖调试运行提交、HITL 决策、运行时工具调用和策略拒绝。
6. 增加 JWT Bearer 认证器和 HTTP 中间件。已支持 HS256、RS256、公钥 PEM、OIDC Discovery/JWKS 自动刷新、issuer/audience/exp/nbf 校验，以及 `tenant_id`、`workspace_id`、`project_id`、`roles` claim 到 `identity.Principal` 的映射。