# Phase 3 iOS 回归基线对照（proof-carrying artifact）

**目的**：为 exec-plan `plan-6e34cf39c628.json` todos `phase3-ios-state-regression` /
`phase3-ios-ux-*` 的 verification 提供持久化证据——「本批次新增失败为零」不是自述，
是同日、同模拟器、同命令下的批次树 vs 基线树对照结果。

## 环境

- iOS 工作树：`/Users/jacklee/Projects/cordcode-ios-codex-remote` @ `codex/codex-remote-backend-ios`
- 模拟器：iPhone 17 Pro Max（iOS Simulator）
- 命令模板：
  - 全量：`xcodebuild -project CordCode.xcodeproj -scheme CordCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test-without-building -only-testing:CCCodeTests`
  - 基线（仅失败套件）：同上，`-only-testing:CCCodeTests/<suite>` × 10
- 方法：批次全量跑出失败清单后，`git stash push --staged` 暂存整个批次 → 基线 HEAD 重新
  `build-for-testing` → 只重跑失败套件 → `git stash pop` 恢复 → 逐例对照。

## 三次运行

| # | 树 | 时间（2026-08-30） | 范围 | 结果 |
| --- | --- | --- | --- | --- |
| A | 批次工作树（dc2a35ff 内容，测试期望修正前） | 18:13:54–18:18:02 | 全量 CCCodeTests | 2192 passed / **14 failed** |
| B | 基线 `cacb77ee`（HEAD，批次已 stash） | 18:20:23–18:20:33 | A 的 10 个失败套件 | **13 failed**（见下表） |
| C | 批次树（含测试期望修正，提交前） | 18:32:17 | Transport + ContractFixture + TurnDetailLazyPhase3 | 29 passed / 0 failed |

## 失败清单对照（A ∩ B 逐例相同 = 预先存在）

| # | 用例 | A（批次） | B（基线） | 归因 |
| --- | --- | --- | --- | --- |
| 1 | ArchitectureGuardrailTests.testActiveSessionSyncV2SealsEveryLegacyHistoryEntryAndLateApply | FAIL | FAIL | 预先存在 |
| 2 | ArchitectureGuardrailTests.testSessionSyncV2HasNoOptimisticOrRawPermissionTimelineFallback | FAIL | FAIL | 预先存在 |
| 3 | CCCodeBridgeHandshakeRaceTests.testCodexSessionResumeWaitsForProtocolReconcileReadyAfterDisconnectedOpen | FAIL | FAIL | 预先存在 |
| 4 | CCCodeBridgeReconnectTests.testRecoveryBarrierCompleteAndFinishErrorsRouteThroughReconnectOwner | FAIL | FAIL | 预先存在 |
| 5 | CCCodeBridgeReconnectTests.testRecoveryProtocolFailureStartsReconnectInsteadOfExplicitDisconnect | FAIL | FAIL | 预先存在 |
| 6 | CCCodeBridgeReconnectTests.testRecoveryTimeoutReconnectThenProjectionPullRecovers | FAIL | FAIL | 预先存在 |
| 7 | ChatViewModelSessionSyncV2Tests.testSSV2SendWaitsForProjectionWithoutOptimisticTimelineWriter | FAIL | FAIL | 预先存在 |
| 8 | MessageWebCodeHighlightSampleTests.testCollectOrVerifyRealHighlightSamples | FAIL | FAIL | 预先存在（committed 样本与引擎再生成漂移） |
| 9 | MessageWebPerformanceFixtureBuilderTests.testBuildOrVerifyWebPerformanceFixtures | FAIL | FAIL | 预先存在（同上类漂移） |
| 10 | SessionsViewModelDirectorySelectionTests.testDraftSessionCreateFailure_surfacesFriendlyDirectoryError | FAIL | FAIL | 预先存在 |
| 11 | TodoRouteCorrelationTests.testRoutedTodosUpdatedAcceptAppliesDock | FAIL | FAIL | 预先存在 |
| 12 | TodoRouteCorrelationTests.testRoutedTodosUpdatedSameEventIDIsDuplicateConsumed | FAIL | FAIL | 预先存在 |
| 13 | CCCodeBridgeTransportTests.testHelloCapabilitiesAdvertiseRelayEncodingAndChunkingOnlyForRelay | FAIL | FAIL | 预先存在（期望字面量早已缺 `projection_window_v1`）；**本批顺带修复至当前真值**（含 `projection_window_v1` + `turn_detail_lazy_v1`），运行 C 绿 |
| 14 | MessageWebContractFixtureTests.testWebEventFixturesDecodeEncodeAndCoverEveryCase | FAIL | —（B 绿） | **本批引入**：新增 `turnDetailAction` 事件后期望清单未同步；**已在提交 dc2a35ff 内修复**，运行 C 绿 |

## 结论

- 本批次（iOS `dc2a35ff`）**新增失败 = 0**（唯一批次引入的 #14 在提交前修复并复验）。
- 基线 `cacb77ee` 上预先存在 **12 例**失败 + 1 例（#13）被本批修复；这 12 例与本批改动无关，
  未在本批越权处理，留给独立立项（多为陈旧字面量/工具链再生成漂移/时序敏感）。
- 新 suite `TurnDetailLazyPhase3Tests` 11/11 绿（运行 C 及此前单独运行均验证）。
- iPhone 当批次未连接 → 真机安装按 todo `phase3-ios-state-regression` 的条件措辞顺延至
  G3（`phase3-ios-ux-regression`，owner 亲手）。

## 复现

```bash
cd /Users/jacklee/Projects/cordcode-ios-codex-remote/OpenCodeiOS
xcodebuild -project CordCode.xcodeproj -scheme CordCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  build-for-testing
xcodebuild -project CordCode.xcodeproj -scheme CordCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  test-without-building -only-testing:CCCodeTests | tee run-a.log
# 基线：git stash push --staged → build-for-testing → 重跑 run-a 的失败套件 → stash pop
```

运行 A/B 原始日志（本文件撰写时）：`/tmp/cccode-fullsuite.log`、`/tmp/cccode-baseline.log`、
`/tmp/cccode-fixverify.log`（/tmp 不持久，故本文件固化完整失败清单与判定）。
