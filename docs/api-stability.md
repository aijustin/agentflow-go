# API 稳定性与迁移策略

本项目当前处于 v1 之前阶段，定位为 **可被 import 的 Go 库**。目标是在第一批公开版本中保持可用、可预期。

## 稳定性边界

计划作为公共稳定表面的内容包括：

- 根包 `github.com/aijustin/agentflow-go`。
- `pkg/` 下的包。
- 示例和 README 中记录的 YAML 场景字段。
- `docs/` 中记录的库集成行为（Framework、Handler 构造函数、适配器端口）。

以下内容不是稳定公共 API：

- `internal/` 下的包。
- 具体适配器内部实现、测试驱动和辅助函数。
- `examples/go/*` 中的 `main` 程序（可复制，不保证 CLI 兼容）。
- `migrations/` 下的 SQL 文件名与列定义可能随适配器演进调整。

## v0 兼容规则

- 对公共结构体、选项、场景字段和构造函数的追加式变更，可以在小版本中发布。
- 破坏性变更必须在 `CHANGELOG.md` 中标明，并附带迁移说明。
- 公共构造函数应优先通过新的选项/配置字段扩展，而不是改变既有行为。
- `pkg/` 中的公共接口只有在既有契约阻碍重要企业场景时才应变更。
- 场景 YAML 字段应尽量保留向后兼容的默认值。
- `internal/` 包可以变化，不提供迁移保证。

## 驱动与适配器策略

框架避免通过根模块强行引入具体基础设施依赖。宿主应用负责提供自己的数据库驱动、对象存储凭证、HTTP 客户端、LLM gateway 和企业集成，然后通过稳定接口或根门面的配置传入 agentflow-go。

### v0.3 适配器迁移

v0.3 把根包的便利构造器层整体外迁（BREAKING，迁移表见 CHANGELOG）：

- **`pkg/adapters`**：具体适配器构造器（run-state/blob/memory 存储、job 队列、LLM providers、catalog manifests、knowledge、MCP、工具执行器、分层记忆、observability sinks/stores）。该包**不依赖根包**，仅需这些构造器的应用可以单独依赖它。
- **`pkg/httpx`**：HTTP 适配器构造器（checkpoint、retention、studio、webhook/human-gate、async jobs、生产组合、observability dashboard）与返回 `[]agentflow.Option` 的 knowledge/MCP 接线函数；其配置引用根包 `Framework`/`Option`，因此允许依赖根包。
- **`pkg/testutil`**：mock LLM gateway（`NewMockLLMGateway`）与既有测试接线。
- 根包不再保留任何转发壳/别名。`ProductionHTTPHandlerConfig`/`NewProductionHTTPHandler` 因组合引用其他 HTTP 构造器且根包不能反向依赖 httpx，随迁 `pkg/httpx`；`ValidateWiring`/`WiringOptions`/`WithRequireLLM` 与根 `options` 机器不可分，保留在根包。

## 测试辅助

`pkg/testutil` 提供 mock LLM 与 demo 工具接线（`WiringOptions`），仅用于测试和 `examples/`，不属于生产稳定面。
