# 本轮任务完成情况：架构健康第四轮（最终轮）开发交接文档（Chat turn sync state-model hardening）

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge`
- Plan: `docs/2026-07-04-architecture-health-fourth-final-round-development-brief.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge/.exec-plan/state/plan-8146dd664595.json`
- Legacy State File: 无（首轮 sync，全新队列）
- Completion Report Verdict: `proved-complete`（P0 已于 2026-07-04 由 iOS spurious-idle follow-up 修复并经 owner 真机验收通过；audit 降级记录见 §0.1，已 resolve）
- Queue Summary: 20/20 todos done，20/20 proven（audit 复核：未降级任何 todo）
- Plan Status: `closed`（第四轮技术交付 proven；07-04 冷启动 Claude 真机可用性经 spurious-idle 修复后达标）
- Related Commits: 提交后回填（iOS 仓 policy/coordinator + ViewModel 调用点 + tests + iOS docs/CHANGELOG；MacBridge 仓 brief/完成报告 + hygiene gate + Mac 活文档/CHANGELOG）
- Generated At: 2026-07-04T19:30:00Z
- Audited / Invalidated At: 2026-07-04T20:10:00Z（+08:00）

## 0.1 Audit Invalidation Record（2026-07-04）

> ✅ **RESOLVED（2026-07-04，同日 follow-up）**：P0「冷启动既有 Claude session 无流式输出 / 从头重播」已定位并修复。
> 真因：冷启动时 Mac file relay 抢先广播 spurious `session_state_changed(idle)`，iOS 在首 `text_delta` 到达前 premature 收口 local turn，
> ownership 翻转后 running polling 的 history fetch 覆盖 live partial。iOS 侧修复 = 新增 `shouldKeepClaudeLocalSendAliveBeforeFirstContent`
> 守卫三处 idle 收口路径（与既有 OpenCode 兜底对齐），回归测试 `testClaudeCodeLocalSendIgnoresSpuriousIdleBeforeFirstContent`。
> owner 真机验收：冷启动连发两问均正常，不再从头重播。详见两仓 `think.md`「首 token 前 spurious idle 收口」节。
> 20/20 todos 自始至终未降级；第四轮技术交付一直 proven。本 report verdict 恢复 `proved-complete` / 专项 Closed。

**Verdict**：`proved-complete`（初稿 → audit 降级 → P0 修复后恢复）。
审计降级原委保留如下（历史记录）：

**降级原因（owner 真机验收未通过）**：冷启动既有 Claude Code session，发首条消息，iPhone 一直显示「正在思考」、无任何流式 text delta 输出。Mac 日志关键证据：
- `transport_route=direct`（iPhone 直连 Mac，session 加载走直连 WebSocket）
- Claude CLI 子进程正常启动（`--resume <session>`，父进程是 `cordcode-bridge-runtime`），go-bridge 正常产生并 broadcast 事件（`text_delta seq=1,2,3...`，每次 `broadcast targets=1`）
- `relay-router: prekey exhausted` deviceID=dev_c5ad42a3...，`/internal/relay/delivery-prekeys` 实测 iPhone `availableCount: 0`（prekey 完全耗尽；targetCount 32 / lowWatermark 10 / maxCount 64）
- `urgentRefill` 信号在 `ConsumePrekey` 耗尽时已置位，但日志无 iPhone 上传 prekey / Mac 发出 refill 信号记录
- 直连 iPhone 的 text_delta 应通过 `broadcaster.Send` 直推，prekey 耗尽只应影响离线 mailbox；为何直连 iPhone 没收到（或没渲染）broadcast 推送尚需定位

**与第四轮的关系（已排除回归）**：已把 iOS 源码回退到 `e018cb5f`（第四轮之前、07-04 单点修复版）重装真机，无流式症状依然存在 → **P0 不是第四轮 turn sync policy 的回归**，而是 pre-existing 的 relay/prekey + live event 投递链路问题。第四轮的 policy/state 重构、定向测试、strict gate 经 audit 复核依然 proven（见 §5、§6）；本轮 20/20 todos **未降级任何一条**。

**为何仍降级完成报告**：初稿 §1 称 `e018cb5f`「经 owner 真机复测通过」、§10 称专项「Closed」，把 07-04 冷启动场景的真机可用性描述为已解决；但 P0 阻塞了该场景的真机验收，报告对用户可见结果的声明过度。专项技术交付成立，但「Closed」需等 P0 作为独立修复项解决后再下。

**Re-verification（本 audit 实跑，非沿用旧 summary）**：
- `CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh` → exit 0（bridgeprovider 1629/71/27、chatviewmodel_generation 2336/56、chatviewmodel_messagesync 1577/46，三条 baseline 无净增）
- `xcodebuild test`（simulator iPhone 17 Pro Max）ChatTurnSyncPolicyTests（25）+ RemoteRunningSessionTests 定向 4 条（`testClaudeCodeInterleave_*` / `testClaudeCodeSecondTurn_*` / `testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream` / `testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream`）→ 29 tests, 0 failures（`TEST SUCCEEDED`）
- 两仓 commit 复核：MacBridge `f35e65f`+`da06183`+`cd9a178`、iOS `9ba4e1d3` 均在位；工作树仅第三轮遗留 + xcodegen `project.pbxproj` 等价 diff（非第四轮残留）

**下一步**：P0（relay delivery prekey 自动恢复 + 直连客户端 live event 投递）作为独立修复项推进；通过 owner 真机复测后，再把本完成报告 verdict 改回 `proved-complete` / 专项 Closed。

## 1. Overall Verdict (总体结论)

第四轮（最终轮）按 brief Phase A → E 全量执行，**判定为 proved-complete**：iOS `ChatViewModel` 的 local send / live event / history sync / running-session polling / session switch 互斥与优先级规则，从散落在多个 extension 的 Claude-only ad-hoc 条件（`isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend`）重构为 backend-agnostic 的显式 policy/coordinator，并用定向测试 + strict net-growth gate 防回涨。

本轮不修一个仍活跃的 bug——2026-07-04 Claude 冷启动「重复从头输出」症状（history 覆盖 live partial）已由 iOS `e018cb5f` 单点修复；本轮把该单点修复背后的结构性 race 泛化为 backend-agnostic policy：`ChatTurnSyncPolicy`（纯函数）+ `ChatTurnSyncState`（`@MainActor` holder），在 MainActor 同步段内做 ownership 读写 + apply 前复核，并用定向交错测试证明复核真实存在。

> ⚠️ **审计更正（2026-07-04，详见 §0.1）**：初稿曾称 `e018cb5f`「经 owner 真机复测通过」。后续 owner 真机验收发现：冷启动 Claude session 发首条消息时 iPhone **无任何流式输出**。把 iOS 源码回退到 `e018cb5f` 重装真机后该无流式症状依然存在 → 这是 pre-existing 的 Relay delivery prekey 耗尽 + live event 投递链路 P0，**不是**第四轮 turn sync policy 引入的回归，也**不是** `e018cb5f` 的 loadMessages overwrite 修复能覆盖的范围。本轮 policy/state 重构本身经定向测试 + strict gate 复核成立（§5、§6），但 07-04 冷启动场景的真机可用性仍被该 P0 阻塞。

**本次架构健康专项的技术交付（policy refactor + 测试 + gate + 文档）已完成且经 audit 复核 proven，但专项暂不 Closed**：07-04 冷启动 Claude session 真机无流式输出的 P0（pre-existing relay/prekey 问题）阻塞 owner 真机验收，需作为独立修复项推进后再收口。剩余大文件作为普通维护债进入日常 backlog，不派生「第五轮架构健康」；未来若出现新系统性 gap，需另立专项并重新定义范围。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence Summary |
| --- | --- | --- | --- | --- | --- |
| Phase A（冻结现状 + policy 小测试） | proven-done | proven-done | proven-done | done | 基线/guard/callsite/parity 盘点完成；ChatTurnSyncPolicy 纯类型；P0 25 条纯函数单测全绿；生产调用点未接入（freeze gate） |
| Phase B（接入 loadMessages + local send ownership） | proven-done | proven-done | proven-done | done | ChatTurnSyncState holder + sendMessage ownership；loadMessages 经 decideLoad/beginLoadIfAllowed/canApply/finishLoad；P1 interleave + second-turn 测试通过；Claude-only guard 退场核验 |
| Phase C（live event / polling / session switch） | proven-done | proven-done | proven-done | done | turnStarted/turnCompleted/switchSession ownership 转移；session-switch 边界 6 测试通过；Codex/OpenCode merge-only 直通无回归 |
| Phase D（gate + 文档收口） | proven-done | proven-done | proven-done | done | hygiene gate 扩展（Generation/MessageSync baseline）+ STRICT 通过；BridgeProvider gate 保留；4 份活文档/CHANGELOG 同步；根因口径与 think.md 一致 |
| Phase E（构建、安装、提交） | proven-done | — | proven-done | done | iPhone 16 Pro Debug 构建+安装+启动成功；71 条定向测试全绿；两仓 commit 提交后回填 hash |

## 3. 关键文件变更

### iOS 仓（`../cordcode-ios`）
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatTurnSyncPolicy.swift`（**新增**）：纯函数 policy。`Ownership`（`.none`/`.localSend`/`.remoteLive`/`.reconciling`）、`LoadTrigger`（8 case）、`LoadDecision`（5 case）；`decideLoadMessages` 只接受显式入参，不访问 `ChatViewModel`/全局状态/网络，无副作用。backend-aware：Claude `.localSend` defer、Codex/OpenCode `.localSend` merge-only。
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatTurnSyncState.swift`（**新增**）：`@MainActor` state holder。`ownershipBySession` 字典 + `decideLoad`/`beginLoadIfAllowed`/`canApply`/`finishLoad`；`canApply` 复核 ownership/session/initializationID/token。`.defer*`/`.reject*` 在 `beginLoadIfAllowed` 返回 nil（网络请求前短路）。
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift`：新增 `turnSyncState` lazy 属性（捕获 `currentSessionId`/`initializationID`/`backendKind`）；新增 `loadMessages(sessionId:loadTrigger:)` 便捷重载（protocol witness 仍是无 trigger 的 `loadMessages(sessionId:)`）。
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift`：`beginGenerationTurn` 同步 ownership（`.localSend`/`.remoteLive`/`.reconciling`）；`completeGenerationCycle` 转入 `.reconciling`；`isClaudeCodeLocalSendInProgress` 退化为 `turnSyncState.ownership(for:)` 的 thin wrapper（保留 Claude-only 语义仅作日志区分）。
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift`：`loadMessages` 入口经 `turnSyncState.decideLoad`/`beginLoadIfAllowed`/`canApply`/`finishLoad`；fetch 后 apply 前 `canApply` 复核；`allowDuringClaudeLocalSend` 兼容参数映射为 `.sendCompletionRecovery` trigger；新增 `inferLoadMessagesTrigger` 桥接旧入口。
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`：`authoritativeReconcileRequired` 显式传 `.reconnectReconcile` trigger。
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`：`switchSession` 离开旧 session 时 `turnSyncState.clearOwnership`；reconnect reconcile 显式传 `.reconnectReconcile` trigger。
- `OpenCodeiOS/OpenCodeiOSTests/ChatTurnSyncPolicyTests.swift`（**新增**）：25 条纯函数单测（6 P0 case + backend-aware 差异 + 纯函数性冻结 + 全 trigger×ownership 矩阵）。
- `OpenCodeiOS/OpenCodeiOSTests/RemoteRunningSessionTests.swift`：新增 `testClaudeCodeInterleave_inFlightHistoryLoadDoesNotOverwriteLivePartialAfterLocalSend`（fetch 在途 ownership 翻转，apply 前复核丢弃迟到历史）+ `testClaudeCodeSecondTurn_finalReconcileClearsOwnershipForNextTurn`（final reconcile 清回 .none 不阻塞下一轮）；`RemoteRunningBackendClient` 加 `suspendFetch`/`resumePendingFetch` continuation hook 供 interleave 模拟。
- `IOS_MAC_INTERACTION_FLOW.md`：新增 §5.1「Turn ownership / history sync gate / final reconcile」。
- `CHANGELOG.md`：新增第四轮 policy 硬化条目（与 `e018cb5f` 单点修复口径一致）。

### MacBridge 仓
- `scripts/hygiene-baseline.json`：新增 `chatviewmodel_generation`（2336/56）+ `chatviewmodel_messagesync`（1577/46）两条 baseline；保留第三轮 `bridgeprovider`（1629/71/27）。
- `scripts/check-architecture-hygiene.sh`：泛化为遍历所有 baseline 条目（python3 解析，无 python3 时回落 BridgeProvider-only）；STRICT 净增即 fail。
- `.github/workflows/ci.yml`：步骤名改为「Net-growth gate (BridgeProvider + ChatViewModel strict)」。
- `GO_BRIDGE_ARCHITECTURE.md`：新增「iOS live event vs history polling 消费边界」小节。
- `CHANGELOG.md`：修订既有 07-04 Claude 冷启动条目（Mac `relayRunningKind` 拆分标为 latent-bug / 独立 hardening，iOS `loadMessages` 覆盖标为症状主因）；新增第四轮 policy 硬化条目 + Closed 声明。
- `docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md`（本文件）。

## 4. 状态模型规则落地说明

### 4.1 Turn ownership（§3.1）
- `Ownership` 4 case，由 `ChatTurnSyncState.ownershipBySession` 在 `@MainActor` 维护；
- `beginGenerationTurn(origin:)` 同步设置：`.localSend` / `.remoteLive` / `.reconciling`；
- `completeGenerationCycle` 转入 `.reconciling`（允许随后的 final reconcile）；
- final reconcile `loadMessages` apply 成功后（decision == `.allowFinalReconcile` 或 ownership == `.reconciling`）清回 `.none`；
- `switchSession` 离开旧 session 时 `clearOwnership`。

### 4.2 History sync gate（§3.2）
- 所有 `loadMessages` 入口经 `turnSyncState.decideLoad(trigger:)` → `beginLoadIfAllowed`（短路 `.defer*`/`.reject*`）→ fetch → `canApply`（复核）→ apply → `finishLoad`；
- `LoadTrigger` 8 case；旧入口经 `inferLoadMessagesTrigger` 推断；`allowDuringClaudeLocalSend==true` 映射为 `.sendCompletionRecovery`；
- `isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend` 不再是独立真值：前者是 `turnSyncState.ownership(for:)` 的 thin wrapper，后者是兼容参数映射为 trigger。生产路径由 policy 取代。

### 4.3 Backend-aware 差异（§3.3）
- Claude Code（`.deferBecauseLocalLiveTurn`）：CLI 子进程无跨 session 共享 live bus；
- Codex / OpenCode（`.mergeOnlyBecauseRemoteRunning`）：app-server/SSE live 权威、merge 幂等；
- 这是能力判断（live event 是否权威、merge 是否幂等），不是「Claude 就跳过」粗规则。

### 4.4 MainActor 原子读写 + apply 前复核（§4.2 硬约束）
- `decideLoad` / `beginLoadIfAllowed` 与 ownership 读取在同一 `@MainActor` 同步段，不跨 `await`；
- `canApply` 复核 ownership/session/initializationID/token；`.defer*`/`.reject*` 经 `beginLoadIfAllowed` 返回 nil 在网络请求前短路；
- 定向交错测试 `testClaudeCodeInterleave_*` 证明复核真实存在（fetch 在途 ownership 翻转后 apply 被放弃）。

### 4.5 Session switch 边界（§3.4）
- `switchSession` `clearOwnership` 清旧 session；迟到结果经 `canApply` 的 session 复核 + `initializationID` 复核被拦截。

## 5. 测试矩阵与命令输出摘要

### 5.1 定向 unit test 命令
```bash
cd ../cordcode-ios
xcodebuild test-without-building -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  -only-testing:CCCodeTests/ChatTurnSyncPolicyTests \
  -only-testing:CCCodeTests/SessionSwitchCancelTests \
  -only-testing:CCCodeTests/MessageDeduplicationTests \
  -only-testing:CCCodeTests/ExecutionStateSemanticsTests \
  -only-testing:CCCodeTests/RemoteRunningSessionTests/testClaudeCodeInterleave_inFlightHistoryLoadDoesNotOverwriteLivePartialAfterLocalSend \
  -only-testing:CCCodeTests/RemoteRunningSessionTests/testClaudeCodeSecondTurn_finalReconcileClearsOwnershipForNextTurn \
  -only-testing:CCCodeTests/RemoteRunningSessionTests/testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream \
  -only-testing:CCCodeTests/RemoteRunningSessionTests/testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream \
  -only-testing:CCCodeTests/RemoteRunningSessionTests/testOpenCodeLocalSend_emptyServerAssistantDoesNotCompleteBeforeFirstDelta
```

### 5.2 结果
- **71 tests, 0 failures**（ChatTurnSyncPolicyTests 25 + SessionSwitchCancelTests 9 + MessageDeduplicationTests 26 + ExecutionStateSemanticsTests 5 + RemoteRunningSessionTests 定向 6）。`TEST EXECUTE SUCCEEDED`。
- 模拟器 destination 仅用于运行 unit test target；**未执行 UI automation / snapshot / simulator automation / 自动 UI 操作**（brief P4 明确允许 simulator destination 跑 unit test，禁止视觉/UI 自动化）。

### 5.3 pre-existing failing test（非本轮引入）
- `RemoteRunningSessionTests/testClaudeCodeAssistantFinishedCompletesWithoutIdleEvent`：经 baseline 复跑确认在未修改代码上同样失败（4 个 XCTAssert），属 commit `e018cb5f` 引入的 pre-existing 状态，**非第四轮工作引入**。已记录，不在本轮 scope 内修复（属普通维护债）。

## 6. strict gate 输出摘要

```bash
CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh
```

结果：`Result: STRICT passed — no net growth across all baseline files.`（exit 0）

三条 baseline 实测：bridgeprovider 1629/71/27、chatviewmodel_generation 2336/56、chatviewmodel_messagesync 1577/46，均无净增。+1 行 sanity 测试正确产生 `STRICT FAILED`（exit 1）后回退到 2336。

## 7. iOS 真机构建/安装状态

- 设备：iPhone 16 Pro（UDID `BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`，状态 `available (paired)`）。
- 命令：`scripts/run.sh device --device BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`。
- 结果：`构建成功` / `安装到真机` / `Launched application with org.openagi.cordcode` / `全部完成 🎉`。
- **未执行 UI automation / snapshot / 自动点击 / 视觉验收**（仅授权设备探测、Debug 构建、安装、启动）。

## 8. 未做事项和原因（§8 明确不做）

- 不继续拆 `BridgeProvider`：第三轮 extract-and-test 已闭环，继续拆会变成「永远还有下一个子域」。
- 不拆 `ChatUIKitContainerView.swift`：当前真实产品风险不在 UI 结构，而在 generation/history/live 状态覆盖。
- 不拆 `agent/claudecode/claudecode.go` / `agent/codex/appserver_session.go`：07-04 事故 Mac 没有重复发送，优先级低于 iOS 状态模型。
- 不继续细分 `go-bridge/handlers.go`：同上。
- 不把所有 hygiene warning inventory 升级成 strict：只新增 Generation/MessageSync 两条 baseline。
- 不改 Bridge protocol / Relay server / pairing / HPKE / TLS pin / backend capability 字面契约。
- 不做 UI/snapshot/simulator automation。

这些不是「留给第五轮」的任务；它们作为普通维护债进入日常 backlog。**本次架构健康专项到第四轮结束（Closed）。**

## 9. 两仓 commit hash

- iOS 仓（`../cordcode-ios`）：`9ba4e1d3` — `Harden Chat turn sync state-model (round 4 final)`（policy/coordinator + ViewModel 调用点 + tests + iOS docs/CHANGELOG，12 files changed, +1224/-21）。
- MacBridge 仓：`cd9a178` — `Record fourth (final) architecture health pass`（brief/完成报告 + hygiene gate + Mac 活文档/CHANGELOG，6 files changed, +288/-32）+ `da06183` — `Restore executable bit on check-architecture-hygiene.sh`（mode fix）。

## 10. 收口结论（audit-invalidated，暂不 Closed）

**本次架构健康专项的技术交付已完成并经 audit 复核 proven（20/20 todos re-verified：hygiene gate + 29 iOS 定向测试实跑通过），但因 P0 真机回归未通过，专项暂不 Closed。** 第四轮把 2026-07-04 Claude 冷启动结构性 race（history 覆盖 live partial）泛化为 backend-agnostic turn sync policy，并用定向测试 + strict gate 防回涨。

owner 真机验收发现的「冷启动 Claude session 无流式输出」P0（pre-existing relay/prekey 投递问题，已通过回退 `e018cb5f` 重装真机排除第四轮回归）需作为**独立修复项**解决后，才能把本专项标为 Closed，并把本报告 verdict 改回 `proved-complete`。未来若出现新的系统性事故或新证据，需另立专项并重新定义范围；本文明确不做的事项不得自动续成下一轮架构健康专项。
