# codex-remote 断线韧性与恢复专项方案 评审报告（r3）

- 日期：2026-09-26
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.3——r2 通过后的通过后纠错勘误轮）
- 评审时方案 SHA-256：`811ee4526abc6db0f9af44eeda96b19132fe419559c71dd24cf0463903d23208`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 265 | shasum -a 256` = `5ab5f9208d362788d6685d4e6196475b0ce954ffb5fc348935e3119e8377cd1b` 亲算，与方案内设计师交接块自报一致
- 评审范围：**full**（全量评审；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r1/r2/r2-meta 作回归清单）
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md + audit-plan-adapter.md 均已读取；audit-plan 接入已按 adapter 执行，见 §6）
- 结论：**APPROVED**（blockers 0，advisories 4——4 条均为 r2 遗留建议 R2-A1~A4 的延续开放项，本轮无新增意见；按 r2 §8 与方案 §8，它们不构成通过前置条件，待 owner 排期）
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未采集任何真机 fixture、未触碰工作树中无关未跟踪 docs。

---

## 1. 结论

### verdict: APPROVED（方案层面通过；「方案通过 ≠ 实施就绪」）

v1.3 是 r2（APPROVED，blockers 0）之后的**通过后纠错勘误轮**：仅来源清单纠错（§2.1 补全）+ §8 勘误记录 + 4 处评审状态注记同步，声明设计内容零改动。本轮作为 fresh 评审员对 v1.3 全量重审，三项核心裁决全部成立：

1. **勘误声明逐项属实（本轮亲核）**：工作树枚举、分支族解析、配套树身份与移动目标记录、四锚点文件跨 4 个候选 ref 字节级零差异、官方 checkout HEAD==FETCH_HEAD、正文哈希算术——全部与实况吻合（见 §4.1）。r2-meta 对 r2 报告来源清单的两项程序性缺陷，在方案侧的同源缺陷已经 §8 勘误-1/勘误-2 补全。
2. **勘误未夹带设计改动（本轮 diff 逐 hunk 核验）**：HEAD 提交内 v1.1 → 工作树 v1.3 共 8 个 diff hunk，逐 hunk 对照后全部可归属为——(a) v1.2 的 F-1~F-6 处置与 E-12/S-7 新增（r2 全量评审通过的内容，hunk 文本与 r2 报告引用逐点吻合），或 (b) §8 声明的勘误范围（§2.1 重写、§8 新增、4 处状态注记、版本头注记）。未发现任何 r2 未覆盖且 §8 未声明的设计改动；R2-A1~A4 确认未夹带处理（与 §8「未夹带说明」一致，本轮逐条复核 4 条在 v1.3 文本中均维持原状，见 §4.3）。
3. **设计内容全量重审通过（本轮独立亲核，不向 r1/r2 复用）**：§2.2 复用调查表 13 行、§2.3 现状摘要、§3.1~§3.7 设计、§4 E-1~E-8/E-11/E-12、§5 切片表、§6 风险 1–5 与官方测试引用的全部源码锚点（约 70 个）本轮逐一亲核，**全部定位与语义吻合**（承重锚点摘录见 §3，其余以清单列出）；门控一致性（OD-1/OD-2/OD-3、E-9→S-4、E-10 完成验收门、「通过 ≠ 实施就绪」）、R-1~R-5 与切片/验收对应、S-1a/S-2/S-3/S-4/S-5/S-6/S-7 设计可行性、audit-plan 外部形状纪律——全部成立（见 §5、§6）。

本轮**无新增阻塞、无新增建议**。r2 的 4 条建议级意见（R2-A1~A4）按 plan-contract「通过后纠错」第 4 条正确地未夹带进勘误，仍开放待 owner 排期——它们是规格精度/断言完备性事项，不影响方案可行性、门控一致性或验收成立性（r2 §4 已逐条论证，本轮复核维持该判断）。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**。
- 实施前置门（与意见分开列）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（S-4 提前量 T，随 E-9）、**E-9**（ctrlExp 真机 fixture ≥5 样本，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性验收，实施期采集）。
- OD-2 已决（macbridge think.md:45 本轮亲核）。E-1~E-8、E-11、E-12 本轮亲核全部 verified；E-9/E-10 如实 pending。
- 实施期真机验证（E-10）与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 工作树路径 | 分支 | 提交 | 未提交状态 | 核验方式 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 0772178358fb582474eb5aada5f50b7d8608053e | 方案文档本身为未提交 v1.3（HEAD 提交内为 v1.1，评审循环正常形态）；另有 5 个未跟踪 docs（2026-09-24-zcode-plan-agents-{design,acceptance}.md 与 r1/r2/r2-meta 三份评审报告）——与方案 §2.1 记录一致，均未触碰 | `git branch --show-current`/`git rev-parse HEAD`/`git status --porcelain`/`git worktree list` 亲跑 |
| cordcode-macbridge（main 工作树，§2.1 枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 07721783 | — | `git worktree list` 亲跑：与评审所在树同提交 |
| cordcode-macbridge（第三树，§2.1 枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b25235 | — | `git worktree list` 亲跑 |
| cordcode-ios（iOS 锚点核验来源，简报 ③ 显式指定） | /Users/jacklee/Projects/cordcode-ios | main | fe421cdc136930e3729b6b3d5c4479d5ecb90df4 | 干净（`git status --porcelain` 亲跑为空） | `git rev-parse HEAD` 亲跑；四锚点文件在 bd46169/fe421cdc/f4a81ee4/8eb262da 全部候选 ref 下 `git diff --stat` 亲跑均空（见 §4.1-④） |
| cordcode-ios（同族配套树，§2.1 记录项） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | 8eb262da2f3d8523f8270a9e5d2e4a9b0e5db7a5 | 干净 | `git rev-parse`/`git merge-base --is-ancestor f4a81ee4 8eb262da` 亲跑：全哈希与方案 §2.1 一致，f4a81ee4 确为其祖先 |
| cordcode-ios（第三树） | /Users/jacklee/Projects/cordcode-ios-plan-approval | detached | a336b68b | — | `git worktree list` 亲跑 |
| openai/codex（官方） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD） | e72da2b53805894878023d01949a25a082e0a5cb | 干净 | `git rev-parse HEAD FETCH_HEAD` 亲跑：两者相等，与方案 §2.1「HEAD 已等于 FETCH_HEAD」更正一致 |

**任务简报与实况的两处陈旧差异（如实记录，非方案缺陷）**：① 简报「未提交状态干净（另有 2 个无关未跟踪 docs）」——实况为方案文档未提交修订 + 5 个未跟踪 docs（r1/r2/r2-meta 三份评审报告在简报撰写后产生）；方案 §2.1 自身记录准确。② 简报评审范围③写「S-3 …… 去重语义镜像 client_tracker.rs:134-143」——这是 v1.1 旧状态；v1.2 起（F-1 处置）方案已正确改引官方 ack 游标语义（`websocket.rs:112-138`）并显式声明 client_tracker.rs 不可平移，本轮亲核证实 v1.2 的修正是正确方向（见 §3.2）。另：简报「评审范围」字段内「首轮无历轮报告」字样与实际（r3，有 3 份历轮报告）不符，按简报显式给出的历轮报告路径与 fresh 模式指示执行。

### 2.2 覆盖声明

- 本轮为 full 全量评审：§2.2 复用调查表 13 行、§2.3 现状摘要、§3.1~§3.7 设计（S-1~S-7 + OD 表）、§4 证据门 E-1~E-8/E-11/E-12/E-9/E-10、§5 切片表、§6 风险 1–5 与官方测试引用、§7 处置表、§8 勘误记录——**全部锚点逐一亲核**（本轮约 70 个，全部为本轮亲核，未向 r1/r2 复用；历轮报告仅作回归清单）。勘误专项（工作树枚举/分支族/跨 ref 一致性/哈希算术/diff hunk 归属）为本轮新增核验维度。
- 未核项（如实声明）：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码，不依赖对 relay 内部的假设；② E-9/E-10 为 pending 运行证据，按授权边界本轮未采集，状态如实登记；③ v1.2 快照字节级内容——v1.2 为未提交工作树状态、已被 v1.3 覆盖，字节级 v1.2→v1.3 diff 不可复算；「设计零改动」的核验以 §4.2 的 hunk 归属分析替代（v1.2 整文件哈希 `c80296e5…` 由 r2 报告头与 r2-meta 报告双记录，身份链完整）。
- 评审对象身份：派发哈希与读取哈希一致（见报告头），无并发改动。

---

## 3. 锚点核验表（本轮全部亲核）

说明：定位=行号指向实际行为；语义=原文支持方案命题。「✓✓」=定位与语义均成立。承重锚点列摘录；其余以 §3.5 清单列出。**未核项已在 §2.2 声明；本表覆盖本轮声明已核的全部锚点类别。**

### 3.1 勘误承重声明（v1.3 §2.1/§8）

| 方案声明 | 本轮核验（亲跑命令） | 结果 |
| --- | --- | --- |
| 两仓各 3 个工作树，枚举与提交完全一致 | `git worktree list`（两仓）：macbridge 3 树（main@07721783 / feat@07721783 / detached@b2b25235）、ios 3 树（main@fe421cdc / feat@8eb262da / detached@a336b68b） | ✓✓ 逐树吻合 |
| 配套树全哈希 8eb262da2f3d…；f4a81ee4 为其祖先 | `git rev-parse feat/ios-native-message-timeline` = 8eb262da2f3d8523f8270a9e5d2e4a9b0e5db7a5；`git merge-base --is-ancestor f4a81ee4 8eb262da` → exit 0 | ✓✓ |
| 四 iOS 锚点文件在 bd46169/fe421cdc/f4a81ee4/8eb262da 全候选 ref 下字节级零差异 | 正确路径（`OpenCodeiOS/OpenCodeiOS/{Models/ProjectionStore.swift, Services/Bridge/CCCodeBridgeModels.swift, Services/Bridge/CCCodeBridgeTransport.swift, ViewModels/ChatViewModel.swift}`）下四组 `git diff --stat <ref> -- <四文件>` 亲跑均空（exit 0、无输出）；`git ls-tree fe421cdc` 确认四文件存在于该 ref | ✓✓（注：首轮用错误路径试跑得空 diff，判无效后以真实路径重跑证实——空 diff 以路径存在为前提） |
| 官方 checkout HEAD == FETCH_HEAD @ e72da2b5 | `git rev-parse HEAD FETCH_HEAD` 两者相等；`git status --porcelain` 干净 | ✓✓（§2.1 对 v1「本地 main 落后」表述的过时更正属实） |
| 正文哈希算术：`head -n 265 \| shasum -a 256` = 5ab5f920… | 亲跑得 `5ab5f9208d362788d6685d4e6196475b0ce954ffb5fc348935e3119e8377cd1b` | ✓✓ |
| MacBridge 树未提交状态描述（方案为未提交修订 + 5 个无关未跟踪 docs） | `git status --porcelain`：1 M（方案）+ 5 ??（与 §2.1 一致） | ✓✓ |

### 3.2 MacBridge agent/codex-remote + go-bridge（@ 07721783）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `stream.go:390-407` ack 构造无 SegmentID（E-12 承重） | stream.go:390-407 | `:400-406 _ = s.conn.Write(Envelope{Type: typeAck, ClientID:…, EnvID:…, StreamID:…, SeqID: env.SeqID})`——确无 SegmentID；`envelope.go:45 SegmentID *int` 字段已备；`:339` chunk 分支逐条 `s.ack(env)` | ✓✓ |
| `stream.go:359-373`/`:397-399` 熔断无界（S-7 承重） | 同 | `:362-367 if bytes.Contains(payload, transportErrorSentinel) { … s.acksDisabled = true }`；`:397-399 if disabled { return }`；注释 `:56-57` "Connection-scoped: a fresh stream re-arms"——无有界时长 | ✓✓ |
| `stream.go:273-345` readLoop 现状不校验 seq | 同 | 全 switch 仅 routing/cursor/ack/pong/deliver/chunk，无 seq 单调性检查 | ✓✓ |
| `stream.go:279-283`/`:307-314` 判死；`:425-434` 组装键；`:458-466`/`:459-460` SubscribeCursorHeader | 同 | `:280-282 s.fail(err)`；`:311-312 if env.Status != "active" { s.fail(…) }`；`:425 asm := s.assembly[*env.SeqID]`；`:459-460 // Owner-accepted known gap: live target never delivered one; callers must not fabricate it.` | ✓✓ |
| `ws.go:87`/`:316-344`/`:318` | ws.go:87, 316-344 | `:87 HandshakeTimeout: 15 * time.Second`；`:317 pingInterval = 10s`；`:318 streamIdleLimit = 60 * time.Second`；`:328 if idle > streamIdleLimit` | ✓✓ |
| `backoff.go:15-56` 退避形状 | backoff.go:15-56 | `:16 base = 1 * time.Second`；`:17 cap = 30 * time.Second`；`:42-46 封顶归零`；`:47 jitter := 0.9 + 0.2*b.rng.Float64()` | ✓✓ |
| `envelope.go:16` 1GB | envelope.go:16 | `:16 ReassembledMessageMaxBytes = 1073741824` | ✓✓ |
| `pairing_persist.go:23-24,60-61,167` ctrlExp；`:163-165` 新 stream_id；`:186-191` 强制 refresh；`:250-296`/`:262-274`/`:287-289` | 同行号 | `:23 CtrlExp string`；`:167 p.state.ctrlExp = rec.CtrlExp`；`:163-164 // stream_id is connection-epoch state, never enrollment state.` + `:165 p.state.streamID = ""`；`:262 time.Sleep(2 * time.Second)`；`:287-289 revoked → invalidateRevokedPairing → return`（永久停止） | ✓✓（E-7「无到期前调度」另经全仓 grep 亲跑证实：ctrlExp 非测试命中仅持久化/解析/校验三类） |
| `pairing.go:158-182` 删配对；`:383-384`/`:408-409` 401/403→revoked；`pairing.go:21` 5s | 同行号 | `:162 p.forgetPersistedPairing()`；`:177 PairPhaseFailed`；`:383-384/:408-409 if status == http.StatusUnauthorized \|\| http.StatusForbidden { return errPairingRevoked }`；`:21 remoteRestoreWaitTimeout = 5 * time.Second` | ✓✓ |
| `agent.go:103-135`/`:110-119`/`:168-180` | 同行号 | `:110-111 无持久化身份 → ErrNotConfigured`；`:118-119 phase=failed → ErrNotConfigured`；`:131 return core.ErrRestoreInProgress`；`:168-175 one-slot chan struct{}`、`:182-185 非阻塞发送` | ✓✓（S-1a 错误面枚举与源码一致，无遗漏） |
| `codec.go:22`/`:112-126`/`:118-124` | 同行号 | `:22 turnByThread map[string]string`；ResetNativeSessionState 重置 lastErrorParams/suppressed/collab/goal 四项**不含 turnByThread**；`:120 "re-observed items are idempotent"` | ✓✓ |
| `session.go:161-212`/`:195-211` | 同行号 | `:195-210 for … observed { go func() { attachLiveThreadOn + collab + goal baseline } }`，`:198` 15s ctx；无 turn 对账 | ✓✓（S-2 触发点宿主存在；E-6 成立） |
| `history_paginated.go:830-941`/`:871-941` | 同行号 | `:830 mapColdPage`；`:871 ReadColdHistory`（`:941` 收尾）；`:910 readTurnsPage(ctx, threadID, "")` desc 首页 | ✓✓ |
| `handlers_projection.go:603-605` source_inspection_failed 一律 true（S-1a 承重） | 同行号 | `:603-605 h.markHydrateFailed(backendID, sessionID, "projection.source_inspection_failed", err.Error(), true,)`；grep 亲核全仓仅此一处该 code | ✓✓ |
| `handlers_projection.go:430`/`:852`/`:876` errProjectionSourceUnavailable | 同行号 | `:430 var errProjectionSourceUnavailable = errors.New(...)`；`:852 agent 未挂载（getFirstAgentByName !ok）且 ProjectionTurnCount==0 → 该错误`；`:876 缺 RichHistoryProvider 断言 → 该错误` | ✓✓ |
| `handlers_projection.go:864-871`/`:866-868` WaitForRestore 接入 | 同行号 | `:864 if waiter, ok := agent.(core.RestoreWaiter); ok {`；`:865 waiter.WaitForRestore(ctx)`；`:866-868 ErrRestoreInProgress → errProjectionHydrating 包装`；`:869 其余原样返回` | ✓✓ |
| mid-hydrate 落点 `:1015`/`:1079-1083`/`:1142`/`:1157`/`:483` 全部 retryable=true | 同行号 | 逐处亲读均为 `markHydrateFailed(..., true)`；`:1015-1018 hydrate_queue_timeout`、`:1080-1081 source_read_failed`、`:1142-1143 bare_source_wait_failed`、`:1157-1158 commit_failed`、`:483 commit_failed` | ✓✓ |
| `handlers_projection.go:147`/`:152-156`/`:176-181`/`:210-226`/`:122-127` | 同行号 | `:147 code := "projection.hydrate_failed"`；`:152 case errors.Is(err, errProjectionHydrating):`→`:153-154 retryable=true`；`:176-181 WireError{Retryable, RetryAfterMillis, Attempts}`；`:212-226` delta_at_head 空 patches 不经 agent；`:122-127` forceColdInspection 含 codex-remote | ✓✓ |
| `handlers_projection.go:160-171` kernel 失败记录透传 | 实际 default 分支 :161-173 | `:161 default:` → `:163 if failure := status.Failure; failure != nil { retryable/attempts/retryAfterMillis 透传 }` | 定位微偏（±2 行）/语义成立 → R2-A4① 延续开放 |
| `projection_kernel.go:889/996/1160/1319`/`:940-942`/`:1115-1123` | 同行号 | `:889 BeginHydrate`；`:996 BeginHydrateTransaction`；`:1160 ApplyHydrateEvent`；`:1319 CommitHydrateTransaction`；`:941 failure.RetryAt = k.now().Add(k.retryPolicy.delay(...))`；`:1121 RecoverOrphanDetailLoadingV2(&restored)`（checkpoint Restore 路径，仅桥重启） | ✓✓ |
| `main.go:1046-1066`/`session_discovery.go:159` | 同行号 | `:1046 func attachLiveCatalogPeriodically`、`:1059 ticker := time.NewTicker(3 * time.Second)`；`:158-159 if signaler, ok := agent.(core.CatalogRefreshSignaler); ok { refreshC = signaler.CatalogRefreshSignals() }` | ✓✓ |
| `chatgpt_auth.go:51`/`diagnostics.go:19-35` | 同行号 | `:51 ctx, cancel := context.WithTimeout(ctx, 12*time.Second)`；`:19-35` StructuredInstanceReadiness 三分类 | ✓✓ |
| `catalog_lifecycle.go:32` thread/list 8s（§2.3 范围外引用，抽核） | go-bridge/catalog_lifecycle.go:32 | `:32 const defaultCatalogFetchTimeout = 8 * time.Second` | ✓✓ |

### 3.3 官方 openai/codex（@ e72da2b，HEAD==FETCH_HEAD 亲核）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `websocket.rs:112-138` ack 游标（S-3/S-7/E-12 承重） | websocket.rs:112-138 | `:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));`；`:129 let is_acked = envelope_cursor <= acked_cursor;`；`:136 self.buffer_by_stream.remove(&key);`（remove 仅在 ack 内） | ✓✓ |
| `websocket.rs:86-145` Buffer 四方法；`:109`/`:131` used 增减；`transport/mod.rs:24` | 同 | `:91-144` 仅 new/insert/ack/server_envelopes；`:109 *used += 1`；`:131 *used -= 1`；mod.rs `:24 pub const CHANNEL_CAPACITY: usize = 128;` | ✓✓ |
| `websocket.rs:996` 全局门限 + `:996-1019` select 结构（风险 5 承重） | websocket.rs:996-1028 | `:996 let outbound_has_capacity = *used_rx.borrow() < super::CHANNEL_CAPACITY;`；`:999-1009` WS 传输 ping 分支不受门限；`:1015` `recv_result = server_event_rx.recv(), if outbound_has_capacity`（满则停拉新事件） | ✓✓ |
| `websocket.rs:1031-1067` seq 消息级分配（S-3 承重） | websocket.rs:1031-1067 | `:1035-1038 entry(seq_key).or_insert(1)`；`:1046 split_server_envelope_for_transport`（拆分在 seq 分配后）；`:1066 insert(seq_key, seq_id.saturating_add(1))`（整消息写完才递增） | ✓✓ |
| `segment.rs:445-467`/`:20`/`:21`/`:301`/`:327` | 同行号 | `:466 seq_id: envelope.seq_id`（chunk 共享消息 seq，原文）；`:20 REMOTE_CONTROL_SEGMENT_MAX_BYTES = 150 * 1024`；`:21 REASSEMBLED_MAX_BYTES = 100 * 1024 * 1024`；`:301 split_server_envelope_for_transport`；`:327 if envelope_size_bytes <= MAX_BYTES { return Ok(vec![envelope]) }`（整体/全 chunk 二选一） | ✓✓ |
| `protocol.rs:114-119` Ack 协议文档（E-12 承重） | protocol.rs:114-119 | "Chunk acknowledgements carry `segment_id` so the sender can retain only the still-unacked wire chunks on reconnect." | ✓✓ 逐字吻合 |
| `client_tracker.rs:134-143`/`:221`/`:312-323` | 同行号 | `:134-143` 去重仅 ClientMessage 分支（`last_inbound_seq_id >= seq_id && !is_initialize → return Ok(())`）；`:221 ClientMessageChunk \| Ack => Ok(())` 直通；`:312-323 close_client` 全文无 outbound_buffer 引用 | ✓✓（「镜像源不可平移」与 E-11 清除子声明均成立） |
| `websocket.rs:201-207`/`:1275-1330`/`:965-988` cursor 机制与重放 | 同行号 | `:206-207 if let Some(cursor) = client_envelope.cursor.as_deref() { self.subscribe_cursor = Some(...) }`；`:1325 REMOTE_CONTROL_SUBSCRIBE_CURSOR_HEADER`（重连 header）；`:965-971 server_envelopes().cloned().collect()` 后逐条重发 | ✓✓（E-1 成立；同 stream 重复信封含 chunk——S-3 去重动机成立） |
| `websocket.rs:74-79`/`:1342-1351` | 同行号 | `:74-75 PONG_TIMEOUT = 60s`；`:78-79 BACKOFF_CAP = 30s`；`:1342-1351 next_reconnect_delay`（min(cap) + 封顶归零） | ✓✓ |
| `enroll.rs:26,246-264` 5 分钟提前量；`websocket.rs:1511-1652` connect 前判定 | 同行号 | `:26 SKEW_SECS: i64 = 5 * 60`；`:256 expires_at > now + SKEW → NotNeeded`；`:1591 .should_refresh_server_token()`（在 prepare_remote_control_enrollment 内） | ✓✓（OD-3 量级先例成立） |
| `server_api.rs:27-28,366-386` 24–36s | 同行号 | `:27-28 MIN=24/MAX=36`；`:382-385 rand::rng().random_range(MIN..=MAX)` | ✓✓（S-7 的 T 量级参照成立） |
| `async-utils/src/backoff.rs:12-19` | backoff.rs:7-19 | `:8 INITIAL_DELAY_MS = 200`；`:9 BACKOFF_FACTOR = 2.0`；`:15 jitter 0.9..1.1`；**无 cap** | ✓✓（F-6① 归属拆分后与源码一致） |
| `remote.rs:181-632` 无 reconnect；`:510-518` pending 全失败 | remote.rs:181-632, 510-518 | 全文 grep "reconnect" **0 处**（亲跑，grep 计数=0）；`:516-518 for (_, response_tx) in pending_requests { send(Err(...)) }` | ✓✓（E-2 前半成立） |
| `tui/src/app/reconnect.rs:30-122` TUI caller 侧模式 | reconnect.rs:30-122 | `:53 deadline = now + 120s`；`:54 for delay in [0,1,2,4].chain(repeat(8))`；`:57 connect(&target)` + 新 AppServerSession + resume；`:45-48` bail 文案 "Your prompt is editable"（不重发输入的旁证） | ✓✓（E-2 四要素吻合） |
| `tests.rs:1086`/`:1528`、`websocket.rs:3062`、`websocket_refresh_tests.rs:716` | 同行号 | 四个测试函数名与行号逐字亲核 | ✓✓（§6 官方不变量引用成立） |

### 3.4 cordcode-ios（@ fe421cdc；四锚点文件跨候选 ref 零差异，见 §3.1）与文档类

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `ProjectionStore.swift:164-180`/`:164-181` 白名单 | ProjectionStore.swift:164-181 | `:164 private static func isRetryablePullError(code: String?) -> Bool`；11 项白名单——**不含 projection.hydrate_failed** | ✓✓ |
| `ProjectionStore.swift:887-900` 只读 code | 同 | `:887 if Self.isRetryablePullError(code: code)` → `.retryable` / else `markFailed`——retryable 字段未读取 | ✓✓（E-3 成立） |
| `CCCodeBridgeModels.swift:314-331` 三字段 | CCCodeBridgeModels.swift:314-331 | `:316-318 let retryable: Bool?; let retryAfterMillis: Int64?; let attempts: Int?` | ✓✓ |
| `ChatViewModel.swift:438-490` 灾难重试 1s→30s | ChatViewModel.swift:438-490 + CCCodeBridgeTransport.swift:81-82 | `:459 projectionDisasterRetryDelayNanoseconds(attempt:)`；`retryInitialSeconds * (1 << boundedAttempt)` 封顶 `retryMaximumSeconds`；Transport `:81-82 retryInitialSeconds = 1 / retryMaximumSeconds = 30` | ✓✓ |
| macbridge think.md:2-6 / :45 / :421 | 同行号 | 「报『已配对，等待 ChatGPT Desktop』横幅 + 『加载失败：codex-remote: stream closed』」；「**待立项（协议面）**：桥侧 backend_status_changed 推送」；「attach failed: stream closed（318 次）；iOS/web 间歇卡『执行中』」 | ✓✓ |
| cordcode-ios think.md:432（§1） | :432@bd46169（`git show bd46169:think.md` 亲核该行即此内容）；当前 fe421cdc 下同内容在 **:477** | 「Mac 端 codex-remote 自 11:36 起病态…至 14:00 未恢复」 | 内容真实；**双框架混用无漂移注记** → R2-A4② 延续开放 |
| cordcode-ios think.md:477（§6 风险 5） | fe421cdc 下 :477 即上述内容（亲核） | 同上 | 定位成立（当前框架）；「2.5h 不自愈」为方案对该行（11:36→14:00 = 2h24m）的**转述加引号**，非逐字引用——语义成立，精度注记并入 R2-A4② |
| 前置方案 plan:508-509 / :588-599 / :597 | docs/2026-08-26-…plan.md 同行号 | 「未取样前不得写死提前量或退避」；Gate P0 fail-closed 裁决；任务 7 遗留缺口 | ✓✓（r1/r2 已核，本轮经方案 §1/§3.4/非目标引用与源码门关系复核一致） |
| 2026-09-24 文档污染声明 | 该文档 grep "Kimi" = 6 处（:20 F-13 自证、:61/:76/:120/:416 残留） | 方案正文 grep "Kimi" 仅 1 处——即污染声明自身（声明必须点名），设计内容零扩散 | ✓✓ |

### 3.5 其余已亲核锚点清单（定位+语义均成立，摘录从略）

ws.go:316-344 全区间；stream.go 各段（:279-283/:307-314/:339/:359-373/:390-407/:397-399/:425-434/:458-466）；backoff.go:15-56；envelope.go:16/:45；pairing_persist.go/pairing.go/agent.go/codec.go/session.go/history_paginated.go/chatgpt_auth.go/diagnostics.go 各段（见 §3.2 行号）；handlers_projection.go:122-127/147/152-156/176-181/210-226/430/483/599-606/603-605/852/864-871/866-868/876/1015/1079-1083/1142/1157；projection_kernel.go:889/941/996/1115-1123/1160/1319；main.go:1046-1066；session_discovery.go:158-159；官方 websocket.rs:74-79/86-145/109/123/129/131/136/201-207/965-988/996-1028/1031-1067/112-138/1140-1152/1154-1168/1275-1330/1342-1351/1511-1652/1591/3062；transport/mod.rs:24；client_tracker.rs:134-143/221/312-323；segment.rs:19-23/301/327/445-467；protocol.rs:114-119；enroll.rs:26/246-264；server_api.rs:27-28/366-386；backoff.rs:7-19；remote.rs:181-632/510-518；reconnect.rs:30-122；tests.rs:1086/1528；websocket_refresh_tests.rs:716；iOS四文件（§3.4）；catalog_lifecycle.go:32（范围外抽核）。

---

## 4. 勘误专项核验（v1.3 本轮核心）

### 4.1 §2.1/§8 勘误声明逐项核验

| # | 勘误声明 | 核验结果 |
| --- | --- | --- |
| ① | 工作树枚举（两仓各 3 树，含 detached 提交） | ✓ 亲跑 `git worktree list` 逐树吻合（§3.1） |
| ② | 分支族解析 + owner 裁决记录（main 撰写、同步至当前分支、评审在当前树） + 简报 ③ 显式指定 iOS 锚点来源 = main @ fe421cdc | ✓ 与任务简报一致；方案遵循指定并记录 |
| ③ | 配套树身份（8eb262da 全哈希；元审核采样 f4a81ee4 为其祖先；移动目标如实标注） | ✓ 亲跑证实（§3.1） |
| ④ | 四锚点文件在 bd46169/fe421cdc/f4a81ee4/8eb262da 全候选 ref 字节级零差异 | ✓ 正确路径下四组 diff 亲跑均空（§3.1；含 CCCodeBridgeTransport.swift，超出 r1/r2 只核三文件的范围） |
| ⑤ | P0 模板补「任务预期分支」「预期产品特性」两列（后者 N/A + 理由） | ✓ 表在（§2.1 七列表），N/A 理由（纯设计、无构建场景）成立 |
| ⑥ | 正文哈希算术（head -n 265 = 5ab5f920…；v1.2 正文哈希 0fe1511d… 被替代并留更正关系） | ✓ 新哈希亲算吻合；v1.2 完整整文件哈希 c80296e5… 由 r2 报告头 + r2-meta 双记录，身份链完整（v1.2 字节快照本身不可复算，见 §2.2 未核项③，以 §4.2 hunk 归属分析替代核验） |

### 4.2 「设计内容零改动」核验（diff hunk 归属分析）

HEAD（v1.1，提交 07721783 内）→ 工作树（v1.3）共 **8 个 diff hunk**（`git diff` 亲跑）。逐 hunk 核验归属：

| hunk | 内容 | 归属 |
| --- | --- | --- |
| @@ -1,9 +1,11 | 版本头：v1.2/v1.3 改动范围声明、勘误轮状态 | v1.2 头注（r2 已核）+ v1.3 头注（§8 声明） |
| @@ -22,7 +24,7 | R-4 补「消息级…chunk 共享 seq_id」限定 | v1.2 F-1 处置（r2 已核） |
| @@ -42,13 +44,21 | §2.1 重写：工作树枚举/分支族/七列表 | **v1.3 勘误-1/2（§8 声明）** |
| @@ -60,10 +70,11 | §2.2 host↔relay 行重写（F-2）、入站 seq 行重写（F-1）、新增 chunk ack 行（E-12）、backoff 归属修正（F-6①） | v1.2 处置（r2 已核；hunk 文本与 r2 报告引用逐点吻合）+ 1 处状态注记（「r2 已确认采纳」，§8 声明） |
| @@ -81,40 +92,66 | §2.3 精化（F-4/E-12 句）、§3.1 S-1a 枚举补全（F-4）、§3.2 接线四要素（F-3）、§3.3 去重重写（F-1）+ ack 子项（E-12，含状态注记） | v1.2 处置（r2 已核）+ 1 处状态注记（§8 声明） |
| @@ -126,7 +163,21 | §3.7 S-7 新增（F-2 对策升主链） | v1.2 新增（r2 明确确认采纳） |
| @@ -142,35 +193,37 | §4 E-5 锚点补（F-4）、E-11 重写（F-2）、E-12 新增（含状态注记）、E-10 增观测项（F-2） | v1.2 处置（r2 已核）+ 1 处状态注记（§8 声明） |
| @@ -178,8 +231,50 | §6 风险 5 重写（F-2）、下一阶段入口改写（状态注记）、§7 处置表（v1.2）、**§8 勘误记录（v1.3 新增）** | v1.2 处置（r2 已核）+ §8 声明的 v1.3 项 |

**结论**：8 个 hunk 全部可归属为 (a) r2 全量评审通过的 v1.2 内容（hunk 文本与 r2 报告 §3/§5 的引用与闭合描述逐点吻合）或 (b) §8 声明的勘误范围（§2.1、§8、4 处状态注记、版本头注记）。**未发现任何 r2 未覆盖且 §8 未声明的设计改动**——「设计内容（规则/锚点/门控/验收）零改动」声明成立（在 v1.2 字节快照不可复算的限制下，以 hunk 归属 + r2 引用对照 + 本轮全量锚点重审三重证据支撑，见 §2.2 未核项③）。

### 4.3 R2-A1~A4 未夹带复核（逐条在 v1.3 文本中确认维持原状）

| ID | r2 意见 | v1.3 现状（本轮亲核） | 状态 |
| --- | --- | --- | --- |
| R2-A1 | S-3 缺口检测游标推进与 thread 归属语义未写明 | §3.3 缺口检测段仍无游标推进语句、无 thread 归属机制声明 | 开放（与 §8 声明一致） |
| R2-A2 | S-7 依赖声明语序/游标状态来源不一致 | §5 依赖序仍为「S-3 的 ack `SegmentID` 子项 → S-7」+ 原括号句 | 开放（同上） |
| R2-A3 | S-7 完成断言缺「接受→解除熔断→恢复正常 ack」路径 | §3.7 完成断言仍仅三项（armed→T→补发；sentinel 重现→重新 armed；未 armed→与现状一致） | 开放（同上） |
| R2-A4 | ① `handlers_projection.go:160-171` 行号精度；② iOS think.md :432/:477 双框架无注记 | ① §3.1 仍引 `:160-171`（实际 :161-173，本轮复核确认微偏仍在）；② §1 仍引 `:432` 无漂移注记、§6 引 `:477` | 开放（同上） |

四条均维持原状，与 §8「未夹带说明」一致；按 r2 §8 与 plan-contract「通过后纠错」第 4 条，它们不构成通过前置条件，待 owner 排期（可并入 v1.4 或实施期）。

---

## 5. 门控一致性、设计可行性与覆盖核查记录（本轮独立重查）

- **OD-1** pending → 阻塞 S-1a/S-1b 实施：§3.8/§4 Gate B/§5 三处一致 ✓；选项验收独立（标志方案满足验收 1+3；白名单方案自认无法满足验收 3、需另设 code 拆分）✓。
- **OD-2** decided（think.md:45 本轮亲核）→ S-6 仅桥内暴露、不新增跨仓协议 ✓；与任务基线「backend_status_changed 另立」一致。
- **OD-3** pending 随 E-9；**E-9** pending → S-4 整体阻塞（§3.4「E-9 未满足前 S-4 不得实施」/§4/§5 一致）✓；「未取样前不得写死提前量」约束源 plan:508-509 ✓；官方 5 分钟先例仅量级参照、方案明示 ctrl token 与 server token 是不同凭证、E-9 仍须实测 ✓。
- **E-10** pending（实施期）→ S-1/S-2/S-3 完成验收门 + S-7 可见性验收 ✓；S-7 行为验收以定向单测为准（sentinel 风暴依赖上游 host 版本缺陷、真机不可控复现——与致因匹配）✓。
- 「方案通过 ≠ 实施就绪」：§6 阻塞清单 + 头部授权声明 + §6 下一阶段入口 ✓。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（E-9 门内；行为级验收未列入 §1，与任务基线 ①–④ 一致）、R-4→S-3+E-12（验收 4）、R-5→S-6——对应齐全 ✓。
- **S-1a 可行性**：分类问题真实（`:603-605` 一律 true，本轮亲核）；错误源枚举与 `agent.go:103-135`（nil/ErrNotConfigured/ctx/ErrRestoreInProgress 四类）+ `:852`/`:876`（source-unavailable）实际分支一致，无遗漏；mid-hydrate 五落点维持 true 的范围声明与 grep 亲核一致（source_inspection_failed 全仓唯一 call site）✓。
- **S-2 可行性**：四接线宿主全部亲核存在（per-thread attach goroutine `session.go:195-211`、`CatalogRefreshSignals` one-slot 先例 `agent.go:168-180` + 桥侧类型断言消费 `session_discovery.go:158-159`、3s 循环 `main.go:1046-1066`、Kernel hydrate 事务域四 API `projection_kernel.go:889/996/1160/1319`）；「与冷开同一 fence 串行化」经 hydrate 域 API 与 RetryAt 门（`:902`/`:1042`）亲核成立；竞态对策（既有 fence + re-observed 幂等 `codec.go:118-124` + 并发负例单测）为设计级充分 ✓。
- **S-3 可行性**：消息级单调性依据（`websocket.rs:1031-1067`：seq 拆分前分配、整消息写完才递增）与 chunk 共享 seq（`segment.rs:466` 原文）本轮亲核成立；去重判定表按 `(seq, segment)` 游标、镜像官方 ack 清除路径（`:123`/`:129`），对 plain/chunk 分别可判定，「理论不可达」分支以 `segment.rs:327` 二选一为据且 fail-visible——逻辑与官方语义一致 ✓。简报③所述「镜像 client_tracker.rs:134-143」为 v1.1 旧案，v1.2 已废除（该镜像源 client→host 方向、仅 ClientMessage 分支、chunk `:221` 直通，本轮亲核证实不可平移），方案现引 ack 游标语义是正确修正。
- **S-4 可行性**：E-7（无到期前调度）全仓 grep 亲核成立；revoked 唯一去向 = 立即 invalidateRevokedPairing（`pairing.go:383-384`/`:408-409` → `pairing_persist.go:287-289` 既有语义，本轮亲核）；其余失败沿用连接死亡路径——不新增死亡路径 ✓。
- **S-5/S-6/S-7**：S-5 纯对齐（1GB→100MB、死代码注释升级）锚点亲核成立；S-6 扩展点（`diagnostics.go:19-35` 三分类 → 细分类别）成立、无跨仓协议；S-7 现状无界熔断 + 全局反压 + ping 不受门限 + 重连换 stream_id + 无 buffer 清除路径——风险 5 链条每一环本轮亲核成立，对策（T 秒有界 + 最高游标补发 ack + 噪声上界 1 条/T）与官方 ack 游标语义兼容且正确依赖 E-12 子项；残余风险（host 侧持续拒绝、在途信封泄漏）如实登记为 MacBridge 侧不可修 ✓。
- **风险 5 对策充分性**（简报④重点）：S-7 把「已知永久死亡模式默认留在产品里」修正为主链切片，验收以定向单测为准（不依赖真机复现上游缺陷），E-10 只承担可见性——与致因匹配、可验收；残余两项的「完整修复需官方 host 侧改动（非目标）」边界诚实 ✓。

## 6. audit-plan 专项接入状态

按 adapter 执行：本方案全部外部内容形状声明（Remote Control envelope/seq/segment/chunk ack 形状与 `(seq, seg)` 游标/cursor header/token refresh 时序/TUI 重连模式/官方测试不变量）**全部锚定官方源码**，属「描述已实现行为、以读实现验证」类别；本轮逐锚点读取官方源码核验（§3.3），**全部成立**——含 v1.2 新增的 chunk ack 形状（`protocol.rs:114-119` 逐字 + `websocket.rs:123`）与反压/ping 交互（`:996-1028`）。方案未提出任何无锚点的新外部格式解析（S-3 消费已解析的 `Envelope.SeqID/SegmentID`；S-1/S-2/S-4/S-5 复用已实现路径），无「描述了但无样本/无锚点的内容类型」遗留。E-9（ctrlExp 分布）与 E-10（断线复现）为运行证据门，按授权边界本轮未采集、如实 pending，且分别精确门住 S-4 与完成验收，不存在「先实现后补票」。

## 7. 复审处置与回归核查（历轮意见 → 本轮裁决）

| 历轮意见 | 本轮裁决 | 依据（本轮亲核，不向历轮复用） |
| --- | --- | --- |
| r1 F-1（阻塞）S-3 去重规则丢弃大消息后续 chunk | **维持闭合** | §3.3 判定表 + 官方 `(seq, seg)` 游标锚点（`websocket.rs:123/:129`）+ `segment.rs:466` chunk 共享 seq 本轮全部亲核吻合；§5 S-3 单测含「同消息不同 segment 不丢弃」负例 |
| r1 F-2（阻塞）E-11 清除子声明无源码支撑 + 风险 5 恢复界错误 | **维持闭合** | Buffer 四方法/remove 仅 ack `:136`/close_client `:312-323`/join-set `:1140-1152`/idle sweep `:1154-1168`/全局 used `:109`/`:131` 对 128 `:996`/ping 不受门限 `:999-1009`/重连换 stream_id `pairing_persist.go:163-165`——本轮逐环亲核；S-7 主链 + 完成断言在位 |
| r1 F-3/F-4/F-5/F-6（建议） | **维持闭合** | S-2 四接线宿主、S-1a source-unavailable 归类（`:852`/`:876`）、E-5 mid-hydrate 落点（`:1079-1083`）、S-4 revoked 去向、backoff 归属——本轮全部亲核在位 |
| r2 E-12/S-7 两项 v1.2 自报新增 | **维持确认采纳** | 本轮独立亲核两端源码 + 官方协议文档，因果链成立（§3.2/§3.3） |
| r2-meta 问题 1（配套工作树未覆盖） | **已修复（方案侧）** | §2.1 工作树枚举 + 分支族解析 + 配套树记录 + 跨 ref 锚点有效性，本轮逐项亲核属实（§4.1） |
| r2-meta 问题 2（P0 模板字段不全） | **已修复（方案侧）** | §2.1 七列表含「任务预期分支」「预期产品特性」（N/A + 理由），本轮亲核 |
| r2 R2-A1/A2/A3/A4（建议） | **开放，正确未夹带** | §4.3 逐条复核维持原状；按 r2 §8 不构成通过前置条件，待 owner 排期 |

**回归检查**：v1.3 勘误未引入新的源码引用错误（§4.2 hunk 归属分析 + 本轮全量锚点重审）；全局引用、OD/门关系、能力范围、切片依赖序（S-1a→S-1b、S-2→S-3、S-3 ack 子项→S-7、S-4/S-5/S-6 独立）经 §5 重查无回归。

## 8. 剩余门与下一阶段

- **仍待满足的实施门（与意见分开，不阻塞方案通过）**：OD-1（owner 确认重试语义，门住 S-1a/S-1b）、OD-3（S-4 提前量 T，随 E-9）、E-9（ctrlExp 真机 fixture ≥5 样本，门住 S-4 整体）、E-10（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期采集）。
- 4 条建议（R2-A1~A4）待 owner 排期：可并入 v1.4 修订或实施期处理（均为小改；R2-A1/A2/A3 建议在对应切片实施前落进方案，避免实施者按现文自行发明语义）。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证与 UI automation 另行授权。

---

## 评审员交接块

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: 811ee4526abc6db0f9af44eeda96b19132fe419559c71dd24cf0463903d23208
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r3.md
scope: full
blockers: 0
advisories: 4
open_gates: [OD-1, OD-3, E-9, E-10]
round_complete: true
contract_version: plan-contract-v1.1
```
