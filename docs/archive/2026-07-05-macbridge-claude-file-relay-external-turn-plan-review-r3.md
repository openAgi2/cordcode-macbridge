# MacBridge Claude File Relay External-Turn Plan Review R3

Date: 2026-07-05

Reviewed document:
`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md` r3

## Verdict

r3 已完整吸收 R2 的 3 条评审意见：backendID-aware agent lookup、live-idle
TTL 的 registry cleanup、`LiveSessionLister` 仅作为内部接口，这些方向都对。

但 r3 新增的“启动时解析 PID、tick 只查单个 PID”与当前拟定的
`LiveSessionLister` 接口不匹配；另外 live-idle TTL 退出会引入一个新的
`turn_started` 丢失窗口。实现前需要再收口这两个点。另，r3 标出的
`runningMap` closure 生产 no-op 是真实 bug，建议作为前置 hotfix，而不是只写在
file-relay 方案的 Related 节。

## Findings

### P0: `LiveSessionIDs(ctx) map[string]bool` 不足以支持“tick 只查单个 PID”

r3 的接口定义仍是：

```go
type LiveSessionLister interface {
    LiveSessionIDs(ctx context.Context) (map[string]bool, error)
}
```

但 Fix 1 又要求“resolve the session's PID once at relay start”并在 poll tick
里“via the `procAlive` seam”只检查这个 PID。这个实现链路目前断开：

- `LiveSessionIDs` 只返回 sessionID → bool，不返回 PID。
- `agent/claudecode.procAlive` 是包内变量，不导出；`go-bridge` 不能直接调用这个 seam。
- 让 `go-bridge` 自己读 `~/.claude/sessions/<pid>.json` 会复制 `agent/claudecode`
  的 stub 语义，还绕过 `procAlive` 测试 seam，破坏 r3 自己要求的内部能力边界。

修订要求：

- 把 live-only interface 扩展为能返回单 session 的进程信息，例如：
  `LiveSessionProcess(ctx, sessionID) (pid int, live bool, error)`；或
  `LiveSessionProcesses(ctx) (map[string]core.LiveSessionProcess, error)`。
- process-death bound 的 tick 复查也应通过 agent 暴露的 seam 完成，例如
  `IsSessionProcessAlive(ctx, sessionID, pid)`，或让 `LiveSessionProcess` 可被廉价调用
  且内部只查对应 PID。
- 不建议让 `go-bridge` 直接解析 Claude stub 或直接实现 PID liveness；这会把
  Claude-specific process model 漏进 wire handler 层。

### P1: live-idle TTL 退出后可能吞掉下一轮 `turn_started`

r3 选择 live-idle watch TTL：无增长 60-120s 后退出，下一次
`get_session_messages` 再启动 relay。这个策略释放 goroutine 是合理的，但有一个
时序洞：

1. relay 因 live-idle TTL 退出，清掉 `relayRunning`；
2. 外部 Claude 写入新的 user 行；
3. iOS 下一次 `get_session_messages` 才重启 relay；
4. 当前 `claudeSessionFileRelayLoop` 启动时先把 `offset` 设为当前文件大小；
5. `detectClaudeTranscriptState` 看到 last entry 是 user，返回 `running`；
6. 现有 initial running 分支只广播 `session_state_changed(running)`，不发
   `turn_started`。

结果是 r3 要修的 per-turn anchor 仍可能丢失，只是从“initial idle 早退”变成
“TTL 空窗期间 user 行已写入，被新 relay 的 offset 吞掉”。

修订要求二选一：

- 放弃纯 TTL 退出，改为“iOS 观察期间保留 live-idle watch”，由连接/订阅生命周期结束来退出；
- 或者增强 initial scan：当 initial state 为 running 且最后有效 entry 是非 interrupt
  user，必须发 `turn_started` + `markRunning`，即使该 user 行已经在 relay 启动前写入。
  这需要 `detectClaudeTranscriptState` 返回更细的状态（lastEntryType / interrupt /
  final assistant），不能只返回 `"running"` 字符串。

同时补一条测试：live-idle TTL 退出后，在下一次 relay 启动前 append user 行；重启 relay
必须仍然发 `turn_started`，不能只发 `session_state_changed(running)`。

### P1: `runningMap` closure bug 应作为前置 hotfix

r3 的 Related Latent Bug 判断正确：`handlers.go` 中 runningMap recompute closure
硬编码 `h.getAgent("claudecode")`，而生产默认注册 key 是 `"claude"`。这会让
`GetRunningSessionIDs` 在生产 list path 上不被调用，外部 turn 的 session-level
running 检测退化为 registry-only。

虽然它不是 file relay 逻辑的一部分，但 r3 的 Summary 和 Acceptance 仍依赖
“session-level executing indicator reaches iOS via list_sessions”。如果该 hotfix 不先做，
file relay 修完后仍会出现状态表现打折，验收也会混入另一个已知 bug。

修订要求：把 runningMap closure hotfix 标为 implementation prerequisite：
先修 `getAgent("claudecode")` hardcode，并补一个只注册 `"claude"` 的 running-map
测试；然后再实现 file relay r3。

### P2: `LiveSessionIDs` 的“cached stub/procAlive path”表述不够精确

Acceptance 写 `LiveSessionIDs` “reuses the cached stub/`procAlive` path”。当前仓库
只有 `runningMapCache` 和 `isSessionExecutingCached`，没有 live stub cache。若实现者
按字面新建缓存，要明确缓存层级和失效策略；若不新建缓存，就不要写“cached”。

建议改为：`LiveSessionIDs` 复用共享 stub-scan helper 和 `procAlive` seam，不读
transcript；tick 级 liveness 由单 PID check 或明确 TTL cache 保证成本。

## Confirmed Fixed Since R2

- backend lookup 已明确为 `backendID → "claude" → "claudecode" → scan by agent.Name()`。
- `LiveSessionLister` 已明确为内部 wiring，不进入 `deriveBackendCapabilities` /
  `hello_ack`，不改协议。
- live-idle TTL 退出时的 stale registry running cleanup 已补齐，并有测试要求。
- per-tick 成本问题已被识别并加入 acceptance/test plan；只剩 interface 形态需要对齐。

## Required Before Implementation

1. 修改 live-only interface，使它能返回/复查单 session PID；不要让 `go-bridge`
   绕过 agent seam 自己读 Claude stub。
2. 解决 live-idle TTL 空窗吞掉 user growth 的问题，确保重启 relay 也能发
   `turn_started`。
3. 先 hotfix shipped `runningMap` closure 的 `"claude"`/`"claudecode"` 注册 ID bug。
4. 澄清 live liveness 的缓存/成本表述，避免不存在的 cache 被误认为已经有。

本次仅评审文档和源码路径，未运行测试。
