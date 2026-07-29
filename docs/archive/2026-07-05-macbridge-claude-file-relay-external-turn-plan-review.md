# MacBridge Claude File Relay External-Turn Plan Review

Date: 2026-07-05

Reviewed document:
`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md`

## Verdict

方案的主判断成立：`go-bridge/handlers_relay.go` 的 Claude file relay
在初始 transcript state 为 `idle` 时直接广播 idle 并退出，这是 Mac-originated
external turn 进不了后续轮询循环的直接原因。文档也已经按要求读完了 235-407
的轮询生命周期；该循环确实能在新增 user 行时发 `turn_started`，并在 final
assistant 行时发 `turn_completed` + idle 后退出。

但方案还不能直接进入实现，需先修正三处设计风险。尤其是 live-only 进程判定
不能复用 `GetRunningSessionIDs` / `runningMapCache` 的结果，否则会把“进程活着但
transcript 暂时 idle”的核心场景继续判成不活跃，修复会失效。

## Findings

### P0: `GetRunningSessionIDs` 不能作为 liveness gate

文档前半多次说可复用 `h.getRunningMap` / `GetRunningSessionIDs` 来交叉确认
liveness（原文 122-125、205-206），但后文 140-143 又正确指出
`GetRunningSessionIDs` 是 executing-only，不是 live-only。源码确认：
`agent/claudecode/claudecode.go:625-652` 只有在 `procAlive(pid)` 且
`isSessionExecutingCached(...) == true` 时才把 session 放进 running map。

这会直接破坏本方案的核心场景：外部 Claude 进程活着、但刚好 transcript 初始
snapshot 仍是上一轮 final assistant，因此 state 为 `idle`。如果用 running map
做 gate，该 session 不在 map 里，file relay 仍会广播 idle 并退出。

修订要求：

- 文档中所有“复用 `h.getRunningMap` / `GetRunningSessionIDs` 判定进程活着”的表述要删掉或改成反例。
- 必须新增 live-only 能力，例如 `core.LiveSessionLister` / `GetLiveSessionIDs(ctx)`，语义是“stub sessionId 对应 PID 活着”，不读取 transcript、不调用 `isSessionExecutingCached`。
- `GetRunningSessionIDs` 可以复用底层 stub 扫描 helper，但不能作为 relay 的 live 判定 API。

### P1: live-but-idle 外部进程会让 file relay 长驻

方案要求“live process + initial idle snapshot → 进入 poll loop，且不广播 running”
是正确的防误报策略。但若 Claude Code app/Terminal 保持一个活着但空闲的长期进程，
transcript 不再增长，且 PID 一直 alive，文档当前的 Fix 2 只覆盖“PID 死亡 + no growth”
退出，未覆盖“长期 live idle + no growth”的退出边界。

当前 `startClaudeSessionFileRelay` 使用 `relayRunning[sessionID]` 排他。只要这个
file relay 长驻，后续 iOS 每次 `get_session_messages` 都不会再启动新的 file relay。
如果同一个外部进程稍后真的开始新 turn，长驻 relay 可以继续看到增长；这是好处。
但如果很多已打开 session 都有 live idle Claude 进程，goroutine 会随会话数保留，
且没有明确生命周期。文档的 acceptance 只说“one relay goroutine per active session”，
但没有定义 active session 何时结束。

修订要求：

- 明确 live-idle watch 的生命周期策略：例如仅在“iOS 正在观察该 session”期间保留，
或引入较长 no-growth idle watch TTL 后退出，并说明退出后下一次
`get_session_messages` 会重新启动 relay。
- 如果选择 TTL，必须区分“已发过 `turn_started` 的 mid-turn no-growth”与“尚未发过
`turn_started` 的 live-idle watch”。前者不能因为 Claude 长思考无文件增长而过早
idle；后者可以更保守地退出以释放资源。

### P1: `session_state_changed(running)` 的接受标准与实现计划不一致

Acceptance 写“relay emits `turn_started` (+ `session_state_changed(running)`)”
（原文 276-278），但当前 poll loop 在新增 user 行时只发送 `turn_started`
并 `markRunning`，没有广播 `session_state_changed(running)`：
见 `go-bridge/handlers_relay.go:328-342`。初始 `running` 分支才会广播
`session_state_changed(running)`（217-230）。

因此有两种合法路线，但文档必须选定一种：

- 保持代码不变：acceptance 改为“`turn_started` 已足够作为 per-turn running signal；
  session registry 内部 `markRunning` 更新本地状态，客户端若需要 running state 依赖
  `turn_started` 或下一次 list_sessions”。
- 或者把实现范围扩大：在 poll loop 的 user growth 分支也广播
  `session_state_changed(running)`，并增加测试证明 iOS 不会因双事件乱序或重复 running
  出现回退。

不建议在文档中同时写“只让 loop 现有行为工作”和“期望额外 running 事件”，否则实现者会
误判验收口径。

### P2: 建议固定接口形态，不留“实现时二选一”

文档 145-152 给了 `core.LiveSessionLister` 和 package-level helper 两个选项。按本仓边界，
`go-bridge/` 不应反向依赖 `agent/claudecode` 的 package helper；现有能力发现也是通过
`core/interfaces.go` optional interface。这里应收敛为新增 `core.LiveSessionLister`，
由 `agent/claudecode.Agent` 实现，`Handlers` 通过已注册 agent type assertion 调用。

这样测试也更干净：`go-bridge` 用 fake agent 实现 live-only interface；`agent/claudecode`
单测验证真实 stub scan 复用 `procAlive` seam。

### P2: 测试夹具需要补齐可测试性 seam

文档测试计划方向正确，但当前 `claudeSessionFileRelayLoop` 直接：

- 调 `findClaudeSessionFile(sessionID, "")`；
- 用真实 `time.NewTicker(3s)`；
- 通过 broadcaster 异步发送事件；
- 在 idle 分支立即 `return`。

如果直接按文档写测试，会慢且易竞态。实现前应在文档里要求最小测试 seam：

- live-only lister 可注入；
- poll interval / no-growth timeout 在测试中可缩短；
- transcript path 仍可通过 `HOME` 临时目录布局驱动，不需要 mock 生产路径；
- 事件断言优先读 broadcaster/test connection，避免用日志作为测试 oracle。

## Confirmed Correct

- 文档对早退 bug 的定位准确：`handlers_relay.go:186-205` 的 initial idle 分支确实会广播 idle、`markIdle`、然后退出。
- 文档对 loop 剩余生命周期的阅读准确：`handlers_relay.go:235-407` 在新增 user 行发 `turn_started`，在 interrupt user 行发 `turn_completed` + idle 并继续监视，在 final assistant 行发 `turn_completed` + idle 并退出。
- 不做 production mock / fallback、不做后台全量 scanner、不把外部 turn 内容流伪造成 `text_delta`，这些边界符合项目约束。
- 保留“已完成 session 不能被误标 running”的目标是必要的；`think.md` 2026-07-04 已记录过 file relay spurious idle/running 相关风险，方案没有忽略这一历史。

## Required Document Changes Before Implementation

1. 把 live 判定统一改为新增 `core.LiveSessionLister`，明确返回 live PID session map，不读取 transcript。
2. 删除“用 `h.getRunningMap` / `GetRunningSessionIDs` 判定 liveness”的说法，只保留“复用底层 stub scan 和 `procAlive` seam”。
3. 明确 live-idle watch 的退出策略，覆盖 PID 长期 alive 但无增长的情况。
4. 统一 acceptance：到底只要求 `turn_started`，还是同时新增 `session_state_changed(running)`。
5. 把测试计划拆成两层：`agent/claudecode` 验 live-only lister；`go-bridge` 验 file relay idle-gate、growth、completion、death/no-growth 退出。

## Suggested Targeted Tests

```bash
go test ./agent/claudecode -run 'LiveSession|RunningSession' -count=1
go test ./go-bridge -run 'ClaudeSessionFileRelay|FileRelayExternalTurn' -count=1
```

本次仅评审文档和源码路径，未运行测试。
