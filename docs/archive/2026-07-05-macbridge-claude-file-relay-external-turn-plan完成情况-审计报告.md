# 完成情况审计报告：MacBridge Claude File Relay External-Turn Plan

审计日期：2026-07-05
被审计文件：`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan完成情况.md`
审计范围：完成情况报告中的全部声明（12/12 todos、关键文件改动、测试、构建/运行时、约束遵守）
审计方法：独立读取 exec-plan 状态、git diff、源码与测试源、独立重跑 Go 测试、`go vet`、核对构建产物与运行时端口/进程/runtime.json

## 1. 审计结论

**Verdict：`confirmed`（完成情况报告所述内容真实、可独立复现）。**

完成情况报告声称的 12/12 todos 全部 done 且带证据，与 `.exec-plan/state/plan-ffe58249dabc.json` 一致；所列 9 处文件改动均存在且与描述相符；定向测试和全量 `go test ./...` 均独立重跑通过；Release 构建产物、`/Applications` 安装、TCP `8777` 监听（PID `91095`）、`runtime.json` 均核实存在且与报告一致；约束（不改协议、不伪造 `text_delta`、不加生产 fallback/mock、不做 UI 自动化）均遵守。

未发现需要降级的 todo，未发现与声明不符的实现。

## 2. 逐项验证矩阵

| 报告声明 | 审计手段 | 结果 |
| --- | --- | --- |
| 12/12 todos done，全带 proof | 读 `plan-ffe58249dabc.json` | ✅ 全部 `done`，verification 均 `present`，`p0-*`/`fix0-*`/`file-relay-*`/`final-*` 四阶段闭环 |
| `core/interfaces.go` 新增 `LiveSessionProcess`/`LiveSessionLister` | `git diff` | ✅ 新增结构体 + 接口，注释明确“PID liveness only，无 transcript 检视” |
| `agent/claudecode` live-only PID lookup + stub 重构 | `git diff` | ✅ 抽出 `readSessionStubs`/`claudeSessionStub`，实现 `LiveSessionProcess`/`IsProcessAlive`；`GetRunningSessionIDs` 复用 stub 扫描但保留 executing-only 语义 |
| `go-bridge/handlers.go` runningMap 注册 hotfix | `git diff` + 源码追溯 | ✅ `getAgent("claudecode")` → `getFirstAgentByName("claudecode")`，新增 `getFirstAgentByName` 按 `agent.Name()` 解析 |
| `go-bridge/handlers_relay.go` 全部生命周期改动 | `git diff` 逐项核对 | ✅ 见 §3 |
| 4 个测试文件新增/扩展 | 读测试源 | ✅ 见 §4 |
| `go test ./go-bridge`、`./agent/claudecode`、`./...` 全部通过 | 独立重跑 | ✅ 见 §5 |
| Release 构建 + `/Applications` 安装 + 端口/runtime.json | 文件系统 + `lsof` | ✅ 见 §6 |
| 不改 wire protocol / `hello_ack` capability | 源码核对 | ✅ `LiveSessionLister` 仅 internal type-assertion，未出现在任何 wire 路径 |
| 不加生产 fallback/mock | 源码核对 | ✅ `sessionLiveProcess` 找不到 agent 时 fail-close 返回 `Live=false` → 广播 idle 退出，非 mock |
| 不做 file-relay `text_delta` 内容流 | `grep text_delta handlers_relay.go` | ✅ 0 命中 |
| 不运行 UI/simulator automation | 报告自述 + 无相关产物 | ✅ 一致 |

## 3. handlers_relay.go 生命周期改动逐项核实

| 计划要求（r6 guardrail） | 实现位置 | 核实 |
| --- | --- | --- |
| Live-gated 初始扫描（`Live == true` 才进入轮询） | `claudeSessionFileRelayLoop`：`live := err == nil && proc.Live; if !live { broadcastIdleState; return }` | ✅ 死 PID 部分 transcript 不会复活为 running |
| Reader-based classifier（全量/增量复用） | `classifyLastMeaningfulClaudeRelayEntryFromReader(io.Reader)` + `classifyClaudeTranscriptFile` 包装 | ✅ |
| Warm-start `turn_started`（重启空窗期补发锚点） | 初始 `entryType=="user"` 且非 interrupt → 立即 `markRunning` + `turn_started` | ✅ |
| Live-idle TTL 有界收口 | `claudeFileRelayLiveIdleTTL=90s`，仅 `!runningObserved` 且无增长时触发 | ✅ 不会吞掉进行中的 turn |
| 进程死亡有界收口 | `claudeFileRelayProcessDeathMisses=1`，`IsProcessAlive(cachedPID)==false` → 广播 idle 退出 | ✅ |
| Cached PID O(1) tick recheck（不每 tick 重扫 stub） | `cachedPID := proc.PID`，tick 内仅调 `lister.IsProcessAlive(ctx, cachedPID)` | ✅ |
| Interrupt-user 继续监视 | interrupt 分支 `runningObserved=false` 后继续 loop；后续非 interrupt user 行重发 `turn_started` | ✅ |
| Meta-only 增长不重复发事件 | `hasMeaningfulEntry` + resume-meta/no-response 跳过；meta-only 不更新语义 | ✅ |

补充核实：
- `detectClaudeTranscriptState` **非死代码**——仍被 `handlers_opencode.go`（OpenCode 路径）复用，重构后正确委托到 `classifyClaudeTranscriptFile`。
- 生产无残留 `getAgent("claudecode")` 硬编码（grep 仅命中测试）。
- file relay 入口 `startClaudeSessionFileRelay` 在 `handlers.go:2333`、`2582` 被生产路径调用。

## 4. 测试覆盖核实

| 测试 | 覆盖项 | 状态 |
| --- | --- | --- |
| `TestClaudeFileRelayDeadPIDWithPartialUserExitsIdle` | 死 PID + 部分 user transcript → idle 退出 | ✅ PASS |
| `TestClaudeFileRelayDeadPIDWithNonFinalAssistantExitsIdle` | 死 PID + 非 final assistant → idle 退出 | ✅ PASS |
| `TestClaudeFileRelayWarmStartUserEmitsTurnStarted` | 重启空窗 warm-start user → `turn_started` | ✅ PASS |
| `TestClaudeFileRelayMetaOnlyGrowthDoesNotReemitTurnStarted` | meta-only 增长不重复 `turn_started` | ✅ PASS |
| `TestClaudeFileRelayLiveIdleSnapshotWatchesNextUser` | live-idle 快照继续监视下一个 user 行 | ✅ PASS |
| `TestClaudeFileRelayInterruptInitialScanKeepsWatching` | interrupt 初始扫描继续监视，后续 user 重发 `turn_started` | ✅ PASS |
| `TestClaudeFileRelayTickUsesCachedPID` | tick 用 cached PID（`LiveSessionProcess` 仅调 1 次，`IsProcessAlive` 用 4242） | ✅ PASS |
| `TestClaudeFileRelayProcessDeathMidTurnBroadcastsIdleAndExits` | 进行中进程死亡 → idle 退出 | ✅ PASS |
| `TestLiveSessionProcess_LiveButIdleIsNotRunning` | live-but-idle 不被 `GetRunningSessionIDs` 报为 running | ✅ PASS |
| `TestLiveSessionProcess_UsesProcAliveAndDoesNotNeedTranscript` | live lister 用 `procAlive`、不依赖 transcript | ✅ PASS |
| `TestGetRunningMap_ProductionClaudeRegistrationFindsClaudeCodeAgent` | 生产 `RegisterAgent("claude", name="claudecode")` 下 `getRunningMap` 命中 | ✅ PASS |

r6 guardrail 列出的回归用例（dead PID 部分转录、TTL 重启空窗、interrupt 继续监视、meta-only 增长、每 tick O(1) liveness recheck）均有对应测试；TTL 退出路径在 `TestClaudeFileRelayTickUsesCachedPID` 的日志中实测到（`live-idle TTL elapsed, exiting`）。

## 5. 测试与构建独立复跑结果

```
go test ./agent/claudecode -run 'LiveSession|RunningSession' -count=1   → ok 0.7s
go test ./go-bridge -run 'TestClaudeFileRelay|...RunningMap...' -count=1 → ok 1.0s（11 用例全 PASS）
go test ./go-bridge -count=1                                            → ok 13.6s
go test ./... -count=1                                                   → 7 个 package 全 ok
go build ./go-bridge ./agent/claudecode ./core                           → OK
go vet   ./go-bridge ./agent/claudecode ./core                           → clean
```

`go test ./...` 全绿，包括此前因 codex 不在 PATH 必失败的 `agent/codex`（已被 commit `9e630b6` “Don't require codex CLI in app-server mode” 修复），与完成情况报告“`go test ./...` 通过”声明一致。

## 6. 构建产物与运行时核实

| 项 | 报告声明 | 实测 | 结果 |
| --- | --- | --- | --- |
| Release 产物 | `dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip` | 存在，mtime `Jul 5 12:03`，10.4 MB + `.sha256` | ✅ |
| `/Applications` 安装 | `/Applications/CordCodeLink.app` 内嵌 runtime | `Contents/Resources/cordcode-bridge-runtime` 存在，mtime `Jul 5 12:03` | ✅ |
| 端口监听 | runtime 在 TCP `8777` | `lsof` 显示 PID `91095` LISTEN `*:8777`，命令为 `cordcode-bridge-runtime` | ✅ |
| runtime.json PID | `91095` | `{"port":8777,"pid":91095,...,"drivers":["claude","opencode","codex"]}` | ✅ |

监听者为 `/Applications/CordCodeLink.app` 内嵌 runtime，符合 CLAUDE.md “8777 监听者必须是内嵌 cordcode-bridge-runtime” 的要求。

## 7. P0 hotfix 真实性核实（生产注册链路）

完成情况报告把 P0 描述为“生产 Claude 注册名是 `claude`、`agent.Name()=="claudecode"`，旧代码按注册 id `claudecode` 查找会落空”。独立追溯生产链路确认：

- `go-bridge/main.go:28`：默认 `-drivers claude,opencode,codex`
- `go-bridge/main.go:99`：`"claude": "claudecode"`（driver id → agent name 映射）
- `go-bridge/main.go:153`：`handlers.RegisterAgent(id, agent)`——按 driver **id** `"claude"` 注册，`agent.Name()` 返回 `"claudecode"`

旧 `getAgent("claudecode")` 按注册 id 查找，生产中确实落空 → runningMap 在 `-drivers claude` 下恒为 nil，外部 Claude session 不会被标 running。新 `getFirstAgentByName("claudecode")` 按 `Name()` 匹配，与注册 id 解耦，hotfix 真实有效。

## 8. 非阻塞观察

1. **改动尚未 commit**：所有改动停留在工作树（unstaged），完成情况报告 §0 也记 `Related Commits: none`。本轮属 owner 负责 commit，与报告一致，但发布前需 owner 提交。
2. **工作树存在无关脏文件**（`CLAUDE.md`、`plan-c7cc43114fd6.json`、`2026-07-05-...-list-sessions-runtime-cpu-plan完成情况.md`、`handoff-20260704-2303.md`）：报告 §5 已声明本轮未触碰，与实测一致。
3. **iOS 端视觉验收未做**：报告 §5 已声明外部 turn 内容仍由 iOS history sync 渲染，本轮只修 Mac 端锚点/生命周期；若 iOS 仍不刷新进行中内容，下一步查 `../cordcode-ios/` history application。属已知残余风险，非本轮范围。
4. **`detectClaudeTranscriptState` 重构后仍保留**：因 OpenCode 路径仍复用，非死代码；与“无死重”无冲突。

## 9. 对报告 §6 Audit Focus 三问的回应

1. **initial scan 是否严格受 `Live == true` gating？** 是。`!live` 立即 `broadcastIdleState` 并 `return`，所有“像 running”的分支（warm-start user / non-final assistant）都在 `live==true` 之后才可达。
2. **live-only lister 是否未读 transcript、未调 `isSessionExecuting`？** 是。`LiveSessionProcess` 仅走 `readSessionStubs` + `IsProcessAlive`(=`procAlive`)，不 `os.Stat` transcript、不调 `isSessionExecutingCached`；`TestLiveSessionProcess_UsesProcAliveAndDoesNotNeedTranscript` 在无 transcript 的 stub 上断言通过。
3. **`claude_file_relay_test.go` 是否覆盖 dead PID / warm-start user / interrupt / meta-only growth / process death / cached PID recheck？** 是。见 §4，六类场景各有命名用例覆盖且全 PASS。

## 10. 最终判断

完成情况报告 `proved-complete` 的 verdict 成立。本轮交付与计划 r6（implementation-ready）一致，证据链完整、可独立复现，未发现虚假证据、未发现需降级项、未发现约束违反。可进入 owner commit / 发布流程；唯一前置是 owner 把工作树改动提交并按需做 iOS 端真机视觉验收。
