# MacBridge Claude File Relay External-Turn Plan Review R2

Date: 2026-07-05

Reviewed document:
`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md` r2

## Verdict

r2 已采纳上一轮 5 条评审意见，主方案现在可以作为实现依据：它明确区分
executing-only 的 `GetRunningSessionIDs` 与 live-only 的 PID 活性，收敛到
`core.LiveSessionLister`，补齐 live-idle watch TTL、process-death bound，并统一选择
`turn_started` 作为 per-turn running signal。

仍建议在实现前补两处细节，否则容易出现“方案正确但生产 wiring 漏掉”的问题。

## Findings

### P1: live lister 查找不能只按 `claudecode` 注册 ID

r2 写 `h.sessionLiveProcess(sessionID)` 会“type-assert the registered claude
agent”（文档 210-213），但没有规定具体查找哪个 backend ID。生产注册逻辑是：
`go-bridge/main.go:98-153` 用 `drivers` 里的 id 注册 agent，默认 driver id 是
`claude`，只是 `agentName` alias 到 `claudecode`。因此默认生产态 agent 很可能注册在
`h.agents["claude"]`，不是 `h.agents["claudecode"]`。

现有 `runningMap` closure 硬编码 `h.getAgent("claudecode")`（`handlers.go:123-132`）
本身就可能只覆盖测试/显式 claudecode ID；新 live helper 如果照这个模式写，会导致默认
`backendID == "claude"` 的 file relay 找不到 lister，返回 false，然后继续走 idle 早退。

修订要求：

- 把 helper 签名写成 `sessionLiveProcess(sessionID, backendID string)`，优先用当前
  `backendID` 查找 agent。
- 为兼容历史/测试，再 fallback 查 `claude` 与 `claudecode`，或扫描已注册 agent 中
  `agent.Name() == "claudecode"` 且实现 `core.LiveSessionLister` 的实例。
- 增加 go-bridge 测试：agent 只注册为 `"claude"` 时，file relay 的 live gate 仍调用
  `LiveSessionIDs`，不能回退到 dead/idle。

### P2: live lister 调用频率与 live-idle TTL 退出的状态清理需写明

r2 要在 poll tick 中做 process-death bound（文档 231-235）。如果每个 file relay 每 3s
都调用一次 `LiveSessionIDs(ctx)`，而该方法每次全量扫描 `~/.claude/sessions/*.json`，
复杂度会变成 relay 数量 × stub 数。live-only 比 running map 便宜很多，因为不读
transcript，但仍应避免在多会话观察时形成新的小型轮询热点。

同时 r2 写 live-idle watch TTL 到期时“不广播 idle，只 release goroutine”（226-229）。
这对“本 relay 从未发 running”成立，但若 registry 里已有 stale running（例如上一轮
file relay 发过 `turn_started` 后异常退出，或历史状态未及时 markIdle），单纯退出可能留下
registry running。Claude list 路径通常用 running map 纠正，但 detail / cleanup / 测试路径
仍会看到 registry 状态。

修订建议：

- 在文档中要求 `sessionLiveProcess` 带短 TTL cache，或在进入 relay 时解析并保存该
  session 的 PID/stub 信息，tick 只检查该 PID；不要每个 tick 全量扫所有 stub。
- 为 live-idle TTL 退出定义状态条件：如果本 relay 从未发过 `turn_started` 且 registry
  当前不是 running，可以只退出；如果 registry 已是 running，必须 `markIdle` 并可选择广播
  idle，避免 stale registry。
- 增加测试覆盖 stale registry：进入 live-idle watch 前人为 `markRunning(sessionID)`，
  TTL 到期后不能留下 `h.sessions.isIdle(sessionID)==false`。

### P3: `LiveSessionLister` 是内部能力，不应进入 `hello_ack` 能力矩阵

r2 第 183-185 行说“Capability is surfaced to the bridge via existing
type-assertion discovery; `hello_ack` capability derivation already follows
interface assertion。”这句话容易被实现者理解成要把 `LiveSessionLister` 加进
`deriveBackendCapabilities` 并下发给 iOS。这个接口只是 MacBridge 内部 wiring，不是客户端
可调用能力。

修订建议：改成“Handlers 内部通过 registered agent type assertion 使用该接口；不新增
`hello_ack.backends[].capabilities` 字段，不改变协议。”

## Confirmed Fixed Since R1

- P0 已修正：文档明确 `GetRunningSessionIDs` / `runningMapCache` 是 executing-only，
  不能做 live gate。
- P1a 已修正：补了 live-idle watch TTL 与 mid-turn no-growth 无超时的 two-tier 生命周期。
- P1b 已修正：选择 Option A，不在 user-growth 分支新增 `session_state_changed(running)`。
- P2a 已修正：接口收敛为 `core.LiveSessionLister`，没有 package-level helper 二选一。
- P2b 已修正：补了 injectable lister、shortenable timers、HOME fixture、event assertions。

## Required Before Implementation

1. 明确 `sessionLiveProcess(sessionID, backendID)` 的 agent 查找规则，覆盖默认
   `"claude"` 注册 ID。
2. 明确 live liveness 复查的成本边界：session-scoped PID check 或短 TTL cache。
3. 明确 live-idle TTL 退出时如何处理 stale registry running。
4. 把 `LiveSessionLister` 表述为内部 interface，不改 wire capability。

本次仅评审文档和源码路径，未运行测试。
