# MacBridge Claude File Relay External-Turn Plan Review R5

Date: 2026-07-05

Reviewed document:
`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md` r5

## Verdict

r5 已把前四轮评审中会导致实现返工的核心问题全部收口：live-only interface
现在返回 PID 并通过 agent seam 做 O(1) 复查；initial scan 有 live 维度；
classifier 区分 full scan 与 post-offset incremental scan；TTL restart gap 和
dead-PID incomplete transcript 都有测试覆盖。方案主体可以进入实现。

剩余只有一个需要在开工前改掉的文档矛盾，以及两个实现时不要漏的细节。

## Findings

### P1: Out of Scope 与 Priority 0 hotfix 矛盾

r5 把 `runningMap` closure 修复列为 Implementation Prerequisite / Priority 0，
要求先修 `h.getAgent("claudecode")` hardcode，并补 `"claude"` 注册测试。这是正确的。

但 Out of Scope 里仍写：

> Any list_sessions / catalog / pagination / running-map change.

这会和 Priority 0 直接冲突。实现者如果按 Out of Scope 执行，会跳过前置 hotfix；
如果按 Priority 0 执行，又违反 Out of Scope。这个矛盾必须删掉，否则交付范围不清。

修订要求：Out of Scope 改为类似：

> Any list_sessions / catalog / pagination change, except the explicit Priority 0
> `runningMap` backend-ID hotfix required before this plan.

### P2: `sessionLiveProcess` helper 应返回 process struct，不只是 bool

正文伪代码仍写：

```text
live := h.sessionLiveProcess(sessionID, backendID)
```

但 r5 后续要求 relay loop 保存 `PID` 并在 tick 调 `IsProcessAlive(ctx, cachedPID)`。
实现时 helper 不能只返回 bool；它至少要返回 `{PID, Live}` 和 lister 引用，或一个小的
relay-local process handle。

建议把伪代码改成：

```text
proc, liveLister := h.sessionLiveProcess(sessionID, backendID)
live := proc.Live
cachedPID := proc.PID
```

并在测试里断言 tick 复查走 `IsProcessAlive(cachedPID)`，不是重新调用
`LiveSessionProcess`。

### P2: initial interrupt-user 分支要明确是否退出

r5 决策表写 live + interrupt user → 按 poll loop 语义发 `turn_completed(idle)`，然后继续
watching。这和当前 poll loop 行为一致，可以接受。但测试计划的 “Initial-scan decision
table” 只泛写 `turn_completed(idle)`，没有明确“继续 watch、不退出”。

建议补一句：interrupt-user initial scan emits `turn_completed` + idle state and remains
in the poll loop, matching current incremental behavior. 这样测试不会只断言事件而漏掉
后续 watch 生命周期。

## Confirmed Ready

- R4 P0 已修：dead PID 不论 last entry 是 user / non-final assistant / final assistant，
  都 idle + exit，不发 `turn_started`/running。
- R4 P1 已修：classifier reader-based，增量扫描有 `hasMeaningfulEntry`，不会因 meta-only
  append 重放旧 entry。
- R4 P2 已修：Fix 2 标题和测试映射恢复。
- 前置 hotfix、file relay 修复、测试层次和验收口径已经足够具体，可以实现。

## Cross-Repo Think Notes

本仓 `think.md` 和 `../cordcode-ios/think.md` 里的既有复盘支持 r5 的方向，不需要再改
主方案，但实现时应保留这些约束：

- Mac 侧 file relay 基于旧 transcript 终态广播 spurious idle 是已知 artifact；r5 的
  `LiveSessionProcess.Live` gating 正是在清这个 Mac 侧债。
- iOS 侧已加的 “Claude local turn 首 token 前忽略 idle” 是必要防御，Mac 修复后也不应删除；
  冷启动、重连、事件乱序仍可能产生短暂 spurious state。
- `turn_started` 只提供 per-turn anchor；外部 turn 内容仍靠 iOS 历史同步渲染。若 Mac 修完后
  仍有内容不刷新，优先查 iOS history application / ownership，而不是继续给 file relay 造
  `text_delta`。
- 排障顺序沿用 think.md：先排除重复 `send_message` / Claude CLI 重跑，再看 file relay
  状态事件，最后看 iOS 是否高频 `get_session_messages` 覆盖 timeline。

## Required Before Implementation

1. 修正 Out of Scope 中的 running-map 矛盾。
2. 把 `sessionLiveProcess` 伪代码改成返回 PID-bearing process info。
3. 明确 initial interrupt-user 分支继续 watch。

本次仅评审文档和源码路径，未运行测试。
