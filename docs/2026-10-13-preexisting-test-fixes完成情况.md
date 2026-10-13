# 2026-10-13 既有测试问题修复完成情况（relay 事件顺序红测试 + stream 桩数据竞争）

> 背景：两项均为**既有**问题，在 P2 实施跑全量定向测试时暴露，经多基线
> worktree bisect 证明与本日事故三部曲（P0/P1/P2）无关（基线 535f447a /
> bc311b6c / e6a37c88 均复现），由 owner 指示当场修复。修复 commit
> `0508191a`，已随 main 推送（origin/main = 0508191a）；feat 分支保留
> 继续开发（owner 2026-10-13 指示）。

## 1. relay 事件顺序红测试（红了 12 天，2026-10-01 起）

**现象**：`TestRelayEventsForwardsUserInputRequested`（events_test.go:1809）
断言客户端收到 `["turn_completed", "session_state_changed"]`，实际生产发射
`["session_state_changed", "turn_completed"]`。单跑 5/5 稳定红（非 flaky）。

**根因链**（git bisect 于隔离 worktree /tmp/p2bis，good=140a66b7 / bad=535f447a）：

- 首个坏提交：**15ec0ce3**（2026-10-01「黄点实时 + claude stub 官方 status
  秒级推送——owner 验收两差距」）；
- 机制：`user_input_requested` 事件经 `publishQuestionPendingState`
  （15ec0ce3 在 relayEvents 循环新增的钩子，handlers_relay.go:3173）同步发布
  `session_state_changed:{"state":"requiresAction"}`，**先于**随后
  EventResult 的 `turn_completed`；
- 实证（临时探针变体测试抓实际帧）：frame0 =
  `{"state":"requiresAction","event":"session_state_changed"}`、frame1 =
  `turn_completed`（perSessionSeq 1→3，中间 seq 为 projection-only 直投，
  无 raw 帧——与测试注释的设计一致）。

**判定**：生产行为**正确**（15ec0ce3 是 owner 验收过的功能；提问挂起发生在
turn 完成之前，生命周期顺序 requiresAction → turn_completed 语义正确）；
**测试期望过时**——15ec0ce3 落地时漏更这条被钉住的断言，红 12 天未被发现。

**修复**：更新期望为 `["session_state_changed", "turn_completed"]`，锚点注释
写明 15ec0ce3 语义、旧期望来源与本次修正（events_test.go:1854-1866 一带）。
**不改生产**。

**教训**：功能提交若新增控制面发射（尤其「既有 projection-only 事件现在也
发控制面」类），必须检索 events_test.go 中钉住顺序的断言；红 12 天才被
发现说明「功能提交当天全量定向跑绿」不是可省步骤。

## 2. stream 测试桩数据竞争（go test -race 下报 DATA RACE）

**现象**：`go test -race ./agent/codex-remote/` 报两处竞争，均在测试桩自身、
不涉生产代码（`git diff` 证明生产文件未触碰）：

- `stream_s7_test.go`：`s7Harness.clock` 裸 `time.Time`——`nowFunc` 回调在
  stream 读循环 goroutine 读（maybeProbe→nowLocked 路径），`advance` 在测试
  主 goroutine 写；
- `stream_s3_test.go` `TestStreamS3GapFiresFanOutOncePerNewGap`：`gaps` 裸
  int——`onEnvelopeGap` 回调（读循环 goroutine）自增，测试主 goroutine 轮询读。

**修复**：s7 桩 `clock` 加 `sync.Mutex`（nowFunc/advance 双侧加锁）；s3 桩
计数改 `atomic.Int64`（Add/Load）。各带来源注释（2026-10-13 发现的既有问题）。

## 3. 验证

| 套件 | 结果 |
| --- | --- |
| `go test ./go-bridge/ -count=1`（**全量无跳过**，含此前红测试） | ok 174.3s |
| `go test ./agent/codex-remote/ -count=1 -race`（整包） | ok 14.7s |
| 修复定向：relay 测试 -count=3；stream S3/S7 -race ×3 | 全绿，零 DATA RACE |

P2 实施期间曾以 `-skip TestRelayEventsForwardsUserInputRequested` 跑绿
go-bridge（当时如实登记为既有红并挂任务）——本报告修复后该跳过不再需要，
套件完全绿。

## 4. 交付与状态

- 修复 commit：`0508191a`（go-bridge/events_test.go +
  agent/codex-remote/stream_s3_test.go + stream_s7_test.go，+35/−11）；
- 已随既定流程并入 main 并推送（ed9ce476 → 0508191a）；
- 两个任务卡片（task_4c4a511e / task_5b253f6c）已撤（均在本轮直接修复）；
- feat 分支 `feat/ios-native-message-timeline` 保留，继续后续开发
  （owner 2026-10-13 指示）。
