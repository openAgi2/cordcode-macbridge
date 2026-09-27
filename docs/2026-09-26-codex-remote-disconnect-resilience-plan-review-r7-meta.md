# codex-remote 断线韧性与恢复专项方案 元审核报告（r7-meta，定向复核 r7 通过结论）

- 日期：2026-09-27
- 复核对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r7.md`（r7 通过报告，verdict APPROVED）
- 关联方案：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.6；本轮 `shasum -a 256` 亲算 = `a3ce49c7592dd3fb78564fa2407fb7aff3c628bcc975c1b28a9704411ed78f3d`，与派发记录及 r7 报告记录一致——评审对象身份确认）
- 复核范围（仅两项，按任务指定，不做两项之外的复核）：
  1. **锚点抽查**——从 r7 通过报告抽 6 组带行号引用（覆盖其 §3.1/§3.2/§3.3 三张锚点表、三仓六文件），逐行核对实际文件，行号与语义都必须吻合；
  2. **来源清单完整性**——r7 报告来源清单是否覆盖任务项目规则（CLAUDE.md P0 来源门）要求的全部来源组合（含配套工作树，无论是否实际引用）。
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、r7 报告、两仓源码、官方 checkout 一律只读，未 commit、未构建、未部署。
- 结论：**confirm=false**（问题 1 项）——锚点抽查 6 组全部通过；来源清单**覆盖面**完整（六树 + 官方 checkout 全枚举，其余五树 + 官方哈希/状态逐项亲核吻合），但来源表第 1 行（方案与评审所在树）记录的提交哈希为 **39 字符的无效值**（缺 1 个字符，`git rev-parse` 无法解析），不构成 P0 要求的「完整提交哈希」。

---

## 1. 锚点抽查（6 组，全部通过）

| # | r7 报告引用（出处） | 本轮实际核对（亲读/亲跑命令与结果） | 结果 |
| --- | --- | --- | --- |
| 1 | `handlers_projection.go`：`:1163` = `h.releaseDeferredPushCandidates(commit.AppliedPendingEventIDs)`；`:1177` = `if commit.PendingPatch != nil {`（守卫行）；`:1178` = `h.eventPublisher.PublishProjectionPatch(...)`；`:1182-1184` = `backendID == "codex-remote"` 门控 `persistCodexProducerSeed`（§3.1，v1.6 勘误-4 对象） | Read `go-bridge/handlers_projection.go:1152-1191`：`:1163`/`:1177`/`:1178`/`:1182-1184` 四组行号逐行吻合；勘误-4 修正后区间 `:1163-1178` 恰好覆盖两调用行、`:1177` 为 if 守卫、`:1182-1184` 为门控块——语义全部成立 | ✓✓ |
| 2 | `projection_kernel.go`：`:1319` CommitHydrateTransaction；`:1335` 先取 liveSnap；`:1352-1356` 按 `tx.unionLiveTurns` 二选一；`:1357` Restore（§3.2 F-R5-1 闭合链） | Read `go-bridge/projection_kernel.go:1315-1359`：`:1319` `func (k *ProjectionKernel) CommitHydrateTransaction(`、`:1335` `liveSnap, liveOK := k.reducer.Snapshot(...)`、`:1352` `if tx.unionLiveTurns {`、`:1355` merge 分支、`:1357` `k.reducer.Restore(...)`——逐行吻合 | ✓✓ |
| 3 | 官方 `websocket.rs:112-138` ack 游标含 `:123 (seq, seg.unwrap_or(usize::MAX))`、`envelope_cursor <= acked_cursor` 清除（§3.3） | Read `codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:110-141`（@ e72da2b5）：`:112` `fn ack(`、`:123` `(acked_seq_id, acked_segment_id.unwrap_or(usize::MAX))`、`:129` `let is_acked = envelope_cursor <= acked_cursor;`（retain 清除）——吻合 | ✓✓ |
| 4 | iOS `ProjectionStore.swift:63`/`:164-181` 11 项白名单无 hydrate_failed/`:885-887` 只读 code（§3.3，@ ccb5a0df） | Read `OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift`（@ `/Users/jacklee/Projects/cordcode-ios` main = ccb5a0df）：`:63` `case retryable(code: String?, message: String)`、`:164-181` `isRetryablePullError` switch 恰 11 case（projection.hydrating/disconnected/not_connected/relay.not_ready/relay.closed/request_timeout/send_timeout/ping_timeout/connect_timeout/hello_timeout/websocket.closed，无 hydrate_failed）、`:885-887` 取 `CCCodeBridgeError.code` 走白名单——吻合 | ✓✓ |
| 5 | `stream.go:390-407` ack 的 Write 字面量仅 Type/ClientID/EnvID/StreamID/SeqID；`s.ack(env)` 两处 `:324`/`:339`；`envelope.go:32-49` 含 `SegmentID *int`（`:45`）（§3.3 E-12a） | Read `agent/codex-remote/stream.go:388-412`（Write 字面量无 SegmentID）+ `envelope.go:30-51`（`:34-49` Envelope struct，`:45` `SegmentID *int`）+ `grep -n 's\.ack(env)' stream.go` → `:324`/`:339` 两处——吻合 | ✓✓ |
| 6 | 官方 `segment.rs:20-21` 150KB/100MB（§3.3） | Read `codex-rs/app-server-transport/src/transport/remote_control/segment.rs:15-24`：`:20` `REMOTE_CONTROL_SEGMENT_MAX_BYTES: usize = 150 * 1024`、`:21` `REMOTE_CONTROL_REASSEMBLED_MAX_BYTES: usize = 100 * 1024 * 1024`——吻合 | ✓✓ |

**锚点抽查结论：6 组引用的行号与语义全部吻合，未发现锚点问题。**

## 2. 来源清单完整性

### 2.1 覆盖面与逐树核对（通过）

本轮亲跑两仓 `git worktree list`：Mac 仓 3 树（main @ 73539b71、native-message-timeline @ 73539b71、plan-approval detached @ b2b25235）；iOS 仓 3 树（main @ ccb5a0df、native-message-timeline @ 61e5c32e、plan-approval detached @ a336b68b）。r7 报告 §2.1 来源身份表共 7 行 = 两仓全部 6 个工作树（含未引用枚举树与配套树）+ 官方 openai/codex checkout——**覆盖任务规则要求的全部来源组合，无遗漏树**。

除第 1 行外，其余六行逐项亲核全部吻合：

- Mac main：`73539b71c0c3552a3c16548d05417813ba907869`，`status --porcelain` 干净 ✓
- Mac plan-approval：`b2b2523526b6af7990c9688fd29b7e285d7ce78c`，detached，干净 ✓
- iOS main：`ccb5a0df647865324e11aedebbb089b9fc07bbb5`，干净 ✓
- iOS native-message-timeline：`61e5c32e7ad03580cf576cadeb9f5cf20f40e348`；未提交状态 = 1 个未跟踪 docs 文件（`2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan-review-r2.md`）——与报告「1 个未跟踪 docs 文件（2026-09-27 native-timeline streaming phase2 评审报告，与本方案无关）」一致 ✓；报告声称的四锚点文件跨候选零改动，本轮在**当前** 61e5c32e 复跑 `git diff --stat ccb5a0df..61e5c32e -- <ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport 四文件>` 仍为空 ✓
- iOS plan-approval：`a336b68bb37765d2ea23c14f6508619378c839ae`，detached，干净；`git merge-base --is-ancestor a336b68b ccb5a0df` 亲跑通过（exit=0）✓
- 官方 codex：`e72da2b53805894878023d01949a25a082e0a5cb`，`rev-parse HEAD` == `FETCH_HEAD`，分支 main，干净 ✓
- 报告第 1 行的佐证声明 `git diff 07721783..HEAD --name-only` = 9 文件全 docs/*.md：本轮亲跑吻合（9 文件，全部 `docs/`）✓；方案文档自身 §2.1 六树记录（第 75/78/79 行）与实际一致 ✓

### 2.2 发现的问题（1 项）

**问题 1（来源清单）：r7 报告 §2.1 来源表第 1 行（方案与评审所在树）的提交哈希记录错误——39 字符，缺 1 个字符，不是有效提交标识。**

- r7 报告第 45 行记录：`73539b71c0c352a3c16548d05417813ba907869`（**39 字符**）
- 实际 HEAD（本轮 `git rev-parse HEAD` 亲跑，工作树 `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`）：`73539b71c0c3552a3c16548d05417813ba907869`（**40 字符**；记录值在第 14 位少一个 `5`）
- `git rev-parse 73539b71c0c352a3c16548d05417813ba907869` 亲跑失败：`fatal: ambiguous argument ... unknown revision`（exit=128）——该字符串不指向任何提交
- 对照组：r7 报告第 46 行（Mac main 树）记录**同一提交**为正确的 40 字符值；方案文档 §2.1（第 75/78 行）亦记录正确 40 字符值——错误为 r7 报告侧转写引入，方案侧记录无误
- 违反的规则：CLAUDE.md P0 来源门「提交=<完整提交哈希>」「路径 + 分支 + 完整提交 + 未提交状态是不可拆分的来源身份」；按「任一被引用仓库缺少路径 + 分支 + 完整提交，评审不得判定通过」，来源表第 1 行——全表最承重的身份（方案、r7 报告本身与全部 Mac 锚点所在树）——记录缺少有效完整提交
- 定性与修复口径：属转写笔误（前 13 字符 `73539b71c0c35` 仍可唯一定位实际提交，无读错工作树情节，锚点结论不受影响）；但 r7 §2.1 本身是 r6-meta 问题 1（来源清单完整性）的补记产物，补记表中出现无效哈希属同类来源身份缺陷，元审核按「来源清单完整性」口径必须判 confirm=false。修复仅需更正 r7 报告（当前为未跟踪文件，尚未归档提交）或以可追踪勘误记录补正该一行 39→40 字符；方案文档无需改动。

## 3. 结论

- **锚点抽查**：6 组全部通过（行号与语义均吻合，覆盖 Mac 仓 Go 源码、官方 Rust checkout、iOS Swift 三处来源）。
- **来源清单完整性**：覆盖面完整（六树 + 官方 checkout 全枚举、含配套树与未引用枚举树），其余六行哈希/状态逐项亲核吻合；但第 1 行提交哈希为 39 字符无效值，不满足 P0「完整提交哈希」要求。
- **confirm=false**，问题 1 项（§2.2）。r7 的 APPROVED 结论在来源表第 1 行提交哈希修复前不予确认。

---

## 元审核交接块

```
meta_verdict: confirm=false
reviewed_report: docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r7.md (verdict APPROVED)
plan_sha256: a3ce49c7592dd3fb78564fa2407fb7aff3c628bcc975c1b28a9704411ed78f3d
issues: 1（r7 报告第 45 行来源表第 1 行提交哈希 39 字符无效）
anchor_spot_checks: 6 组全部通过
source_list_coverage: 完整（六树 + 官方 checkout）
report_path: docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r7-meta.md
```
