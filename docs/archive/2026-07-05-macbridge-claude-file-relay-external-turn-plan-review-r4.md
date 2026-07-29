# MacBridge Claude File Relay External-Turn Plan Review R4

Date: 2026-07-05

Reviewed document:
`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md` r4

## Verdict

r4 已采纳 R3 的关键要求：live-only interface 能返回 PID 并通过 agent seam
做 O(1) tick liveness recheck；live-idle TTL restart gap 用 enriched initial scan
收口；`runningMap` closure bug 升为前置 hotfix。这一版已经接近可实施。

实现前还需要补两个边界：initial-scan 决策表必须把 **process liveness** 纳入所有
running-like 分支；共享 classifier 必须明确支持“全文件初始扫描”和“offset 后增量扫描”
两种模式，否则容易重复发事件或误读旧 entry。

## Findings

### P0: initial-scan running 分支必须受 live PID 约束

r4 的 initial-scan 决策表规定：

- last entry 是 non-interrupt `user` → 发 `turn_started` + `markRunning`
- last entry 是 non-final `assistant` → 发 `session_state_changed(running)` + `markRunning`

但这两条没有写“只有 `LiveSessionProcess.Live == true` 才能这么做”。如果 transcript
最后停在 user 或 non-final assistant，而 Claude PID 已经死了（进程崩溃、被杀、旧残缺
session），打开这个 session 会立刻发 `turn_started` 或 running，随后再等 process-death
bound 收口。这重新制造了“打开已结束/不可继续 session 时误报 running”的问题，只是从
final assistant stale snapshot 换成 incomplete stale snapshot。

修订要求：

- initial scan 决策表增加 liveness 维度：只有 live PID 才允许 user-last →
  `turn_started`、non-final assistant → running。
- 如果 last entry 看起来 running 但 `Live=false`，应直接 `markIdle` + idle broadcast +
  exit，或至少不发 `turn_started`/running，并明确是否需要 `turn_completed(reason:
  process_dead)`。关键是不允许把死进程的残缺 transcript 当作 active turn。
- 增加测试：dead PID + last non-interrupt user；dead PID + non-final assistant。两者都
  不得发 `turn_started` 或 `session_state_changed(running)`。

### P1: classifier 需要区分初始全量扫描和增量扫描

r4 写 `classifyLastMeaningfulEntry(sessPath)`，并说要 refactor poll loop 的 growth logic
共用它。但当前 poll loop 的正确性依赖“只扫描 offset 之后新增内容”：没有新 user/assistant
时不应重复使用旧 last entry 触发事件。

如果 helper 只有 `sessPath` 形态，后续实现很容易在 poll tick 时扫描全文件，导致：

- 新增的是 meta / ignored line，却重新看到旧 user last entry，重复发 `turn_started`；
- 新增的是无 meaningful content 的 append，仍按旧 final assistant 或旧 user 做状态迁移；
- truncate/rewrite 场景下 offset 语义被 helper 吃掉，现有 `newSize < offset → offset=0`
  的行为变得不清楚。

修订要求：

- 把 helper 设计成同时支持全量与增量，例如
  `classifyLastMeaningfulEntryFromReader(r io.Reader)`，由 caller 决定读全文件还是
  `Seek(offset)` 后的新内容；或者显式参数 `classifyLastMeaningfulEntry(sessPath,
  offset int64)`.
- 返回值必须带 `hasMeaningfulEntry bool`，让 poll loop 在“本次增长没有 meaningful entry”
  时只更新 offset、不发任何事件。
- 增加测试：append 仅 meta/ignored line 时，不重复发上一轮 `turn_started`。

### P2: 文档结构里 Fix 2 标题丢失

Fix 1b 后直接进入 “Once the relay stays alive...” 段落，缺少 `2. Two-tier idle
lifecycle...` 标题。内容还在，但编号从 1b 跳到 3，容易让实现者漏掉 Fix 2 的边界和测试。

修订建议：恢复 `2. Two-tier idle lifecycle in the poll loop` 标题，并把相关测试条目
明确映射到 Fix 2。

## Confirmed Fixed Since R3

- `LiveSessionLister` 接口已改为 `LiveSessionProcess(ctx, sessionID)` +
  `IsProcessAlive(ctx, pid)`，不再要求 `go-bridge` 读 Claude stub 或直接调用
  `procAlive`。
- live-idle TTL restart gap 已用 warm-start `turn_started` 方案收口，并加入测试。
- `runningMap` closure 的 `"claude"`/`"claudecode"` 注册 ID bug 已升级为前置 hotfix。
- “cached stub/procAlive path” 的措辞已改为共享 stub scan + cached PID + O(1)
  `IsProcessAlive`，不再暗示已有 live stub cache。

## Required Before Implementation

1. initial-scan 决策表补 liveness 维度，禁止 dead PID + stale running-looking
   transcript 发 running/`turn_started`。
2. classifier helper 明确 full-scan vs incremental-scan API，并保留 `hasMeaningfulEntry`
   语义。
3. 恢复 Fix 2 标题，避免实现计划编号断裂。

本次仅评审文档和源码路径，未运行测试。
