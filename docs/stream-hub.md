# Stream Hub：多订阅、重放与审批态续订

`Framework.StreamRun` 把 LLM token 与运行时事件合并为统一的 `StreamFrame` 流，但历史上它是单订阅、无重放的：只有启动流的那一个消费者能看到帧，迟到者（刷新页面的 UI、另一个进程内的观察者）什么都拿不到，HITL 暂停还意味着流的终结。Stream Hub（根包 `stream_hub.go`）在不改变既有单订阅行为的前提下解决了这三点。

## 模型

- 每次 `StreamRun` 在 hub 上注册一个 per-run session：一个容量 1024 帧的 ring buffer（溢出丢最旧帧并计数）加一组 subscriber。
- 主 `StreamRun` 返回的 channel 不是 hub subscriber：它保持既有的阻塞式、从不丢帧的投递语义，帧序列逐字节不变。hub 只是在每帧投递给主消费者后 observe 一份。
- `Framework.AttachRunStream(ctx, runID, opts...) (<-chan StreamFrame, error)` 新增订阅：先重放 ring 存量（ring 溢出过时先发一帧带累计计数的 `events_lost` 缺口标记），再接实时帧。支持 `WithStreamEventFilterPreset` 做读侧投影（preset 过滤不算丢帧）。
- **暂停不是流的终点**：`Done{Status: Paused}` 后 session 保持可 attach 且 subscriber channel 不关闭；`ResumeAndContinue` / `ContinueRun` 恢复执行时复用同一 runID 的 session 继续 publish（恢复期间的事件经 context 上的 hub tee 流入，continue 的结果成为下一帧 Done/Error）。审批等待期间无 idle 超时。
- 终态（Done 非 Paused / Error）后 session 保留 30 秒宽限期供迟到 attach，之后由 hub 回收。
- run 不在 hub（进程重启、宽限期已过）时，`AttachRunStream` 回退到 `WithEventStore` 配置的事件仓库重放事件帧，并依据持久化运行状态补一帧合成的 Done；未配置事件仓库则返回错误。
- `Framework.Close` 清理全部 session 并关闭所有 subscriber channel；attach 的 ctx 取消会 detach 该 subscriber。

## 背压语义

subscriber channel 容量 256。publish 永远非阻塞：

- 慢订阅者丢 **event / events_lost 帧**，累计计数通过后续的 `events_lost` 标记帧（cumulative）浮出水面；
- **token / done / error 帧不丢**：放不进 channel 时进入该 subscriber 的溢出 backlog，由按需启动的 drainer goroutine 按序补投。

这与既有 tee 的契约一致：答案流（token）永远权威，事件可以丢但必须可数、可见。

## 示例

```go
frames, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hi"})
// ...主消费者读 frames...

// 另一个观察者中途加入：先收 ring 重放，再接实时帧。
replay, err := fw.AttachRunStream(ctx, "run-1", agentflow.WithStreamEventFilterPreset(agentflow.EventFilterProductUI))
for frame := range replay {
    // token / event / done / events_lost
}
```
