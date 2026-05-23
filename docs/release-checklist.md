# 发布检查清单

在打公开版本标签之前使用这份清单。

## 必需检查

运行发布检查目标：

```sh
GOTOOLCHAIN=auto make release-check
```

该目标会执行 `fmt`、`test`、`vet`、`security`（govulncheck）、`validate-builder`（19 条 Go DSL catalog stack）、`validate-catalog`（`examples/catalog/` 下 tool/skill manifest）。

## 推荐的人工检查

当发布内容涉及持久化或并发行为时，运行以下检查：

```sh
GOTOOLCHAIN=auto make test-integration
GOTOOLCHAIN=auto make test-race
```

只有在有意配置了本地或兼容模型端点时，才运行真实模型测试：

```sh
GOTOOLCHAIN=auto make test-realmodel
```

## 文档检查

- `README.md`（中文，默认）与 `README.en.md`（英文）已描述新的用户可见行为；示例路径见 README「示例路径对照表」。
- 新增 builder stack 可以通过 `go run ./examples/go/validate -kind builder <id>` 校验；catalog manifest 用 `-kind tool|skill` 或 `make validate-catalog`。
- `CHANGELOG.md` 包含公共 API 与场景相关变更。
- 破坏性变更包含迁移说明。
- 安全敏感能力记录了安全默认值和运维责任。

## 公共 API 检查

- 用户需要装配的公共适配器已有根门面构造函数。
- 新的稳定契约位于 `pkg/` 下。
- 面向框架消费者的示例没有导入 `internal/` 适配器。
- 除非适配器的目标就是基础设施耦合，否则公共接口不应绑定具体 Provider 或基础设施。

## 发布说明

打标签前应总结：

- 主要功能。
- 安全或治理变更。
- 迁移说明。
- 已知限制。
- `make release-check` 的验证证据。