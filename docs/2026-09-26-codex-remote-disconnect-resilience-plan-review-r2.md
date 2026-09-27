# codex-remote 断线韧性与恢复专项方案 评审报告（r2）

- 日期：2026-09-26
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.2）
- 评审时方案 SHA-256：`c80296e5260c1ea94ce6ba075301f0942c71e09dbce82d423ddc673cb6673950`（整文件 `shasum -a 256` 亲算，**与派发记录一致**；正文哈希 `head -n 241 | shasum -a 256` = `0fe1511d…` 亦与方案内设计师交接块自报一致——评审对象身份确认）
- 评审范围：**full**（全量复审；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r1 作回归清单）
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md + audit-plan-adapter.md + audit-plan SKILL.md 均已读取；audit-plan 接入已按 adapter 执行，见 §6）
- 结论：**APPROVED**（blockers 0，advisories 4）
- 授权边界遵守声明：本轮仅写入本报告文件；方案、产品代码、其他文档（含 2 个无关未跟踪 docs 与 r1 报告）一律只读，未 commit、未部署、未采集任何真机 fixture。

---

## 1. 结论

### verdict: APPROVED（方案层面通过；「方案通过 ≠ 实施就绪」）

v1.2 对 r1 全部 6 条意见的处置经本轮**逐项独立亲核**全部闭合，且闭合方式与源码逐点吻合：

- **F-1（阻塞，已闭合）**：S-3 去重规则重写为 per-stream `(seq_id, segment_id)` 双元游标，判定表对 plain 与 chunk 两类信封分别可判定；镜像源从不可平移的 `client_tracker.rs:134-143`（client→host 方向、仅 ClientMessage 分支）改为官方 ack 清除路径的 `(seq, seg.unwrap_or(usize::MAX))` 游标语义（`websocket.rs:112-138`）。本轮亲核确认新规则在关键场景下正确：**host 重放未送达 chunk（同消息 seg > lastSeg）按新规则接受、不丢弃**——这正是 v1.1 规则会静默丢失的形态；§5 单测补了「同消息不同 segment 不丢弃」负例。
- **F-2（阻塞，已闭合）**：E-11 清除子声明改为源码准确表述（清除仅 client Ack 一途；client 关闭/过期清理不触及 outbound_buffer——`close_client` `client_tracker.rs:312-323`、join-set `websocket.rs:1140-1152`、idle sweep `:1154-1168` 本轮全部亲核无 buffer 引用）；风险 5 重写为「单次熔断满缓冲 → host writer 全局停摆、重连不可愈、整腿死亡直到 Desktop 重启」；对策升主链为 S-7（熔断 T 秒有界 + 最高游标补发 ack），「E-10 复现才修」的错配触发条件废除。**本轮对风险 5 链条的每一环做了独立源码裁决，含 r1 未展开的一环：controller 面 pong 信封经 `run_client_outbound`（`client_tracker.rs:250-289`）送入与 ServerMessage 同一条受门限的 `server_event_tx`，而 `websocket.rs:999-1009` 的 WS 传输层 ping 不受反压门限——故「host↔relay 连接存活不触发 host 侧重连」与「MacBridge 60s 入站静默判死重连」同时成立，链条内部自洽且与源码一致。**
- **F-3/F-4/F-5/F-6（建议，均已闭合）**：S-2 接线四要素各有可指名宿主（per-thread attach goroutine `session.go:195-211`、镜像 `CatalogRefreshSignals` 的信号 seam `agent.go:168-180` + 桥侧消费先例 `session_discovery.go:159`、3s 循环宿主 `main.go:1046-1066`）；S-1a 补 `errProjectionSourceUnavailable → retryable=false` 显式归类（返回点 `:852`/`:876` 亲核）；E-5 补 mid-hydrate 实际落点 `:1079-1083`；S-4 revoked 唯一去向 = 立即 invalidate（`pairing.go:383-384`/`:408-409` 亲核）；三处行号/归属修正全部落实。

**v1.2 两项超出 r1 范围的新增（方案已显式送审确认），本轮独立亲核均确认成立**：

1. **E-12（chunk ack 缺 `SegmentID`）**：MacBridge `stream.go:390-407` ack 构造确实只带 `SeqID`（`envelope.go:45` 字段已备而未用），readLoop 逐 chunk ack（`:339`）；官方 `protocol.rs:114-119` 协议文档原文要求 chunk ack 携带 `segment_id`，`websocket.rs:123` 对缺省 segment_id 取 `usize::MAX`、`:129` 按 `(seq, seg)` 元组 `<=` 比较清除——**「首 chunk ack 等价 (seq, MAX) 游标、提前清除整消息全部 chunk 的 host 重放缓冲；host 腿在 chunk 送达窗口中断时消息尾部不可重放且不产生消息级 seq 缺口」的因果链在源码层面完全成立**，是 S-3「缺口即真实丢失」的已证盲区，修复（补一字段）为官方文档对齐的最小改动。**评审确认采纳。**
2. **S-7（ack 熔断有界化）**：现状熔断（`stream.go:359-373` armed、`:397-399` 早退）确无有界时长；S-7 的 T 秒补发 + 最高游标 ack + 噪声上界 1 条/T 的设计与官方 ack 游标语义兼容，且正确依赖 E-12 子项（否则补发 `(seq, MAX)` 会重演 E-12 盲区）。残余风险（host 侧持续拒绝、正常断线在途信封泄漏）如实登记为 MacBridge 侧不可修。**评审确认采纳。**

本轮新发现 **4 条建议级事项（R2-A1~A4），无阻塞**：均为规格精度/断言完备性问题，不影响方案可行性、门控一致性或验收成立性（详见 §4）。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**。
- 实施前置门（与意见分开列）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（S-4 提前量 T，随 E-9）、**E-9**（ctrlExp 真机 fixture ≥5 样本，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性验收，实施期采集）。
- OD-2 已决（think.md:45 亲核）。E-1~E-8、E-11、E-12 本轮亲核全部 verified；E-9/E-10 如实 pending。
- 实施期真机验证（E-10）与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 工作树路径 | 分支 | 提交 | 未提交状态 | 核验方式 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 0772178358fb582474eb5aada5f50b7d8608053e | 方案文档本身为未提交 v1.2 修订（HEAD 提交内为 v1.1，属评审循环正常形态）；另有 2 个无关未跟踪 docs（2026-09-24-zcode-plan-agents-{design,acceptance}.md）与 r1 报告未跟踪，均未触碰 | `git branch --show-current`/`git rev-parse HEAD`/`git status --porcelain` 亲跑 |
| cordcode-macbridge（代码锚点核验来源） | 同上工作树 | — | 07721783（与 main 同提交） | — | `git diff 715104c6..HEAD --stat` 亲跑：唯一改动文件即本方案文档（+185 行，两个 docs 提交 92f13c22/07721783），**代码零改动，与方案 §2.1 记录的 715104c6 锚点核验等价**（与简报声明一致） |
| cordcode-ios | /Users/jacklee/Projects/cordcode-ios | main | fe421cdc136930e3729b6b3d5c4479d5ecb90df4 | 干净 | `git rev-parse HEAD`/`git status` 亲跑；方案记录 bd46169，`git diff bd46169..HEAD --stat -- ProjectionStore.swift CCCodeBridgeModels.swift ChatViewModel.swift` 亲跑为空（**三锚点文件零改动、无行号漂移**）；think.md 该区间 +45 行 → 方案 §1 引用的 ios think.md:432 内容漂移到当前 :477（内容亲核一致，见 §3.4 与 R2-A4） |
| openai/codex（官方） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD） | e72da2b53805894878023d01949a25a082e0a5cb | 干净 | `git rev-parse FETCH_HEAD HEAD` 亲跑：两者相等且与方案 §2.1 记录一致，无漂移（方案「本地 main 落后」的表述相对当前状态已过时，但不影响锚点身份——工作树内容即 e72da2b）；`git log -S` 亲核 commit `d047c33a1b` = "fix(remote-control): avoid server token refresh retry storms (#30201)" |

### 2.2 覆盖声明

- 本轮为 full 全量复审：§2.2 复用调查表 13 行、§2.3 现状摘要、§3 设计（S-1~S-7 + OD 表）、§4 证据门 E-1~E-8/E-11/E-12/E-9/E-10、§5 切片表、§6 风险 1–5 与官方测试引用、§7 处置表——**全部锚点逐一亲核**（本轮约 60 个锚点，全部为本轮亲核，无向 r1 复用；r1 仅作回归清单）。门控一致性、设计可行性、覆盖完整性、audit-plan 专项均已检查。
- 未核项（如实声明）：①「会话列表 thread/list 8s 超时」范围外引用——本轮抽核其常量锚点 `catalog_native_membership.go:16 catalogRequestTimeout = 8s` 成立，完整重验属 2026-09-24 已交付方案范围，本方案明示不在范围内；② relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码，不依赖对 relay 内部的假设（sentinel 风暴形态引用本仓 think.md:415-425 实证记录）；③ E-9/E-10 为 pending 运行证据，按授权边界本轮未采集，状态如实登记。
- 评审对象身份：派发哈希与读取哈希一致（见报告头），无并发改动。

---

## 3. 锚点核验表

说明：定位=行号指向实际行为；语义=原文支持方案命题。「✓✓」=定位与语义均成立。本轮全部亲核；仅列差异项与承重项摘录，其余以行号清单列于 §3.5。

### 3.1 MacBridge agent/codex-remote + go-bridge（@ 07721783，代码与 715104c6 等价）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `stream.go:390-407` ack 构造无 SegmentID（E-12 承重锚点） | stream.go:390-407 | `:400-406 _ = s.conn.Write(Envelope{Type: typeAck, ClientID:…, EnvID:…, StreamID:…, SeqID: env.SeqID})`——无 SegmentID 字段 | ✓✓（E-12 成立；`envelope.go:45 SegmentID *int` 字段已备、`:339` readLoop 对 chunk 逐条 ack 亦亲核） |
| `stream.go:359-373` 熔断器无界；`:397-399` armed 早退 | stream.go:359-373, 397-399 | `:362-367 if bytes.Contains(payload, transportErrorSentinel) { … s.acksDisabled = true }`；`:397-399 if disabled { return }`；注释 `:53-58` "Connection-scoped: a fresh stream re-arms" | ✓✓（S-7 的问题陈述成立：本 stream 生命周期内停发一切 ack、无有界时长） |
| `stream.go:273-345` readLoop 现状不校验 seq | stream.go:273-345 | 全 switch 仅 routing/cursor/ack/pong/deliver/chunk，无 seq 单调性检查 | ✓✓ |
| `stream.go:279-283`/`:307-314` 判死路径 | 同 | `:280-282 if err != nil { s.fail(err); return }`；`:311-312 if env.Status != "active" { s.fail(…) }` | ✓✓ |
| `stream.go:425-434` 组装键 `*env.SeqID` | stream.go:425-434 | `:425 asm := s.assembly[*env.SeqID]` | ✓✓ |
| `stream.go:458-466`/E-1 `:459-460` SubscribeCursorHeader | stream.go:458-466 | `:459-460 // Owner-accepted known gap: live target never delivered one; callers must not fabricate it.` | ✓✓ |
| `envelope.go:16` 1GB / `:45` SegmentID | envelope.go:16, 45 | `:16 ReassembledMessageMaxBytes = 1073741824`；`:45 SegmentID *int` | ✓✓ |
| `backoff.go:15-56` 退避形状 | backoff.go:15-56 | `:16 base = 1 * time.Second`；`:17 cap = 30 * time.Second`；`:42-46` 封顶归零；`:47 jitter := 0.9 + 0.2*b.rng.Float64()` | ✓✓ |
| `ws.go:87`/`:316-344`/`:318` | ws.go:87, 316-344 | `:87 HandshakeTimeout: 15 * time.Second`；`:318 streamIdleLimit = 60 * time.Second`；`:328 if idle := stream.IdleFor(); idle > streamIdleLimit` | ✓✓ |
| `pairing_persist.go:23-24,60-61,167` ctrlExp；`:163-165` 新 stream_id；`:186-191` 强制 refresh；`:250-296`/`:262-274`/`:287-289` | pairing_persist.go 同行号 | `:167 p.state.ctrlExp = rec.CtrlExp`；`:163-164 // stream_id is connection-epoch state, never enrollment state.`；`:186-189` refresh 失败 revoked 透传/其余 Warn 继续；`:262 time.Sleep(2 * time.Second)`；`:287-289 revoked → invalidateRevokedPairing → return`；`:291 markOffline("已配对，等待 ChatGPT Desktop")` | ✓✓（E-7「全仓 grep 无调度调用」亲跑证实：ctrlExp 非测试命中仅持久化/校验/赋值三类，无到期前调度；S-6 前提成立——watchBinding 把一切非 revoked 错误折叠为同一句横幅） |
| `pairing.go:158-182` 删配对；`:383-384`/`:408-409` 401/403→revoked；`pairing.go:21` 5s | pairing.go 同行号 | `:162 p.forgetPersistedPairing()`；`:383-385/:408-410 status==401/403 → errPairingRevoked`；`:21 remoteRestoreWaitTimeout = 5 * time.Second` | ✓✓ |
| `agent.go:103-135` WaitForRestore 错误面；`:110-119`；`:168-180` CatalogRefreshSignals | agent.go 同行号 | `:110-111 无持久化身份 → ErrNotConfigured`；`:118-119 phase=failed → ErrNotConfigured`；`:131 ErrRestoreInProgress`；`:168-175 one-slot chan struct{}`、`:177-187 非阻塞发送` | ✓✓（S-1a 分类的错误面枚举与源码一致：nil/ErrNotConfigured/ctx/ErrRestoreInProgress 四类，无遗漏） |
| `codec.go:22` turnByThread；`:112-126` reset 刻意不含它 | codec.go:22, 112-126 | `:22 turnByThread map[string]string`；ResetNativeSessionState 重置 lastErrorParams/suppressed/collab/goal 四项，**不含 turnByThread**；`:118-124` 注释 "re-observed items are idempotent" | ✓✓ |
| `session.go:161-212` BindClient；`:195-211` per-thread attach | session.go 同行号 | `:195-210 for _, threadID := range observed { go func() { attachLiveThreadOn + collab + goal baseline } }`，`:198` 15s ctx；无 turn 对账 | ✓✓（S-2 触发点宿主真实存在；E-6 成立） |
| `history_paginated.go:830-941`/`:871-941` | history_paginated.go:830-854, 871-941 | `:910 readTurnsPage(ctx, threadID, "")` 后 `:918-920` 逆序转 asc（上游 desc 首页） | ✓✓ |
| `handlers_projection.go:603-605` source_inspection_failed 一律 true（全仓唯一 call site） | handlers_projection.go:603-605 | `:603-605 h.markHydrateFailed(backendID, sessionID, "projection.source_inspection_failed", err.Error(), true,)`；grep 亲核：`source_inspection_failed` 全仓仅此一处 | ✓✓（S-1a「分类只落此 call site」的范围声明成立） |
| `handlers_projection.go:430`/`:852`/`:876` errProjectionSourceUnavailable | 同 | `:430 var errProjectionSourceUnavailable = errors.New(...)`；`:847-852 agent 查不到 && ProjectionTurnCount==0 → 该错误`；`:872-876 缺 RichHistoryProvider 断言 → 该错误` | ✓✓（F-4 处置的归类依据成立） |
| mid-hydrate 落点 `:1015`/`:1079-1083`/`:1142`/`:1157`/`:483` 全部 retryable=true | 同 | 逐处亲读，均为 `markHydrateFailed(..., true)`；另 `:1128` coverage_failed 为 Claude 专属分支，不属 codex-remote，方案枚举无遗漏 | ✓✓ |
| `handlers_projection.go:160-171` kernel 失败记录透传 | **实际 :161-173** | `:161 default:` → `:162-173 status.Failure → retryable/attempts/retryAfterMillis` | 定位微偏（差 2 行）/语义成立 → R2-A4① |
| `handlers_projection.go:147`/`:152-156`/`:176-181`/`:210-226`/`:122-127` | 同 | `:147 code := "projection.hydrate_failed"`；`:153-154 hydrating retryable=true`；`:176-181 WireError{Retryable, RetryAfterMillis, Attempts}`；`:211 delta_at_head` 空 patches 不经 agent；`:127 forceColdInspection 含 codex-remote` | ✓✓ |
| `projection_kernel.go:889/996/1160/1319` hydrate 事务域 API；`:940-942` RetryAt；`:1115-1123` 孤儿收口 | 同 | `:889 BeginHydrate`；`:996 BeginHydrateTransaction`；`:1160 ApplyHydrateEvent`；`:1319 CommitHydrateTransaction`；`:941 failure.RetryAt = now + retryPolicy.delay(...)`；`:1118-1122 if backendID == "codex-remote" { RecoverOrphanDetailLoadingV2(&restored) }`（checkpoint Restore 路径，即仅桥重启） | ✓✓（S-2 事务域宿主与「孤儿收口只在进程重启后」均成立） |
| `main.go:1046-1066` 3s attach 循环；`session_discovery.go:159` 类型断言先例 | 同 | `:1056 ticker := time.NewTicker(3 * time.Second)`；`:158-159 if signaler, ok := agent.(core.CatalogRefreshSignaler); ok { refreshC = signaler.CatalogRefreshSignals() }` | ✓✓（S-2 消费宿主与 seam 先例成立） |
| `chatgpt_auth.go:51` 12s；`diagnostics.go:19-35` | 同 | `:51 WithTimeout(ctx, 12*time.Second)`；`:19-35` StructuredInstanceReadiness 三分类（available/service_not_running/pairing_required） | ✓✓（S-6 扩展点成立） |
| `agent.go:113` + `pairing.go:21` 5s restore 等待 | pairing.go:21 | `:21 var remoteRestoreWaitTimeout = 5 * time.Second` | ✓✓（§2.2「瞬态 offline → 5s 后 ErrRestoreInProgress」成立） |

### 3.2 官方 openai/codex（@ e72da2b，HEAD==FETCH_HEAD 亲核）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `websocket.rs:112-138` ack 游标（S-3/S-7/E-12 承重锚点） | websocket.rs:112-138 | `:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));`；`:125-128 envelope_cursor = (seq_id, event.segment_id().unwrap_or_default())`；`:129 let is_acked = envelope_cursor <= acked_cursor;`；`:136 buffer_by_stream.remove` 仅此处 | ✓✓（元组比较语义 = 首 chunk ack 缺 segment_id 即 (seq, MAX) 提前清除整消息 chunk——E-12 因果链源码成立） |
| `websocket.rs:86-145` Buffer 四方法；`:109`/`:131` used 增减；`transport/mod.rs:24` | 同 | `:91-144` 仅 `new/insert/ack/server_envelopes`；`:109 *used += 1`；`:131 *used -= 1`；mod.rs `:24 pub const CHANNEL_CAPACITY: usize = 128;` | ✓✓ |
| `websocket.rs:996` 全局门限 + `:996-1028` select 结构（风险 5 承重锚点） | websocket.rs:996-1028 | `:996 let outbound_has_capacity = *used_rx.borrow() < super::CHANNEL_CAPACITY;`；`:999-1009` WS 传输 ping 分支**不受门限**；`:1010-1019` 满则等 used 变化；`:1020-1027` 有容量才拉新事件 | ✓✓（「ping 分支不受反压门限」指 WS 传输层 ping；**本轮补充裁决：controller 面 pong 信封走 `client_tracker.rs:250-289 run_client_outbound → server_event_tx`，与 ServerMessage 同通道受门限**——故反压停摆时 MacBridge 收不到 pong、60s 判死重连成立，风险 5 链条两分支同时成立、内部自洽） |
| `websocket.rs:1031-1067` seq 消息级分配 | websocket.rs:1031-1067 | `:1035-1038 entry(seq_key).or_insert(1)`；`:1046 split_server_envelope_for_transport`（拆分在 seq 分配后）；`:1066-1067 seq_id + 1` | ✓✓（消息级严格单调、从 1 起、chunk 共享消息 seq——S-3 缺口检测与 F-1 修正的依据成立） |
| `segment.rs:445-467` build_chunk_envelope；`:20`/`:21`/`:301`/`:327` | segment.rs 同行号 | `:467 seq_id: envelope.seq_id`（原文）；`:20 MAX_BYTES = 150 * 1024`；`:21 REASSEMBLED_MAX_BYTES = 100 * 1024 * 1024`；`:301 split_server_envelope_for_transport`；`:327 if envelope_size_bytes <= REMOTE_CONTROL_SEGMENT_MAX_BYTES { return Ok(vec![envelope]) }`（整体或全 chunk 二选一） | ✓✓（F-1/E-12/E-8 全部锚点成立） |
| `protocol.rs:114-119` Ack 协议文档 | protocol.rs:114-119 | "Chunk acknowledgements carry `segment_id` so the sender can retain only the still-unacked wire chunks on reconnect." | ✓✓（E-12 的官方文档依据逐字吻合） |
| `client_tracker.rs:134-143`/`:221`/`:312-323` | 同 | `:134-143` 去重仅 ClientMessage 分支；`:221 ClientMessageChunk \| Ack => Ok(())` 直通；`:312-323 close_client` 全文无 outbound_buffer 引用 | ✓✓（E-11 清除子声明 + 「镜像源不可平移」均成立） |
| `websocket.rs:201-207`/`:1275-1330` cursor 机制 | 同 | `:206-207 host 记录 client 回传 cursor`；`:1322-1328 重连时 x-codex-subscribe-cursor header` | ✓✓（E-1 成立：cursor 是 host↔relay 腿机制） |
| `websocket.rs:965-988` 重连全量重放未 ack | 同 | `:965-971 server_envelopes().cloned().collect()` 后逐条重发 | ✓✓（同 stream 重复信封场景成立，S-3 去重动机成立；重放含 chunk） |
| `websocket.rs:74-79`/`:1342-1351` | 同 | `:74-75 PONG_TIMEOUT 60s`；`:78-79 BACKOFF_CAP 30s`；`:1343-1345 cap 归零` | ✓✓ |
| `enroll.rs:26,246-264` 5 分钟提前量；`websocket.rs:1511-1652` connect 前判定 | 同 | `:26 SKEW_SECS: i64 = 5 * 60`；`:256 expires_at > now + SKEW → NotNeeded`（否则 Proactive）；`:1591 should_refresh_server_token()`（在 prepare_remote_control_enrollment 内） | ✓✓（OD-3 量级先例锚点成立） |
| `server_api.rs:27-28,366-386` 24–36s | server_api.rs:27-28, 381-386 | `:27-28 MIN=24/MAX=36`；`:382-385 random_range(MIN..=MAX)`；commit `d047c33a1b` 经 `git log -S` 亲核存在 | ✓✓（S-7 的 T 量级参照锚点成立） |
| `async-utils/src/backoff.rs:12-19` | backoff.rs:7-17 | `:7 INITIAL_DELAY_MS = 200`；`:8 BACKOFF_FACTOR = 2.0`；`:15 jitter 0.9..1.1`；**无 cap** | ✓✓（F-6① 归属拆分后与源码一致） |
| `remote.rs:181-632` 无 reconnect；`:510-518` pending 全失败 | remote.rs:181-632, 510-518 | 全文 grep "reconnect" **0 处**；pub API 面 connect…shutdown；`:516-518 for (_, response_tx) in pending_requests { send(Err(...)) }` | ✓✓（E-2 前半成立） |
| `tui/src/app/reconnect.rs:30-122` TUI caller 侧模式 | reconnect.rs:30-122 | `:53 deadline = now + 120s`；`:54 for delay in [0,1,2,4].chain(repeat(8))`；`:57-62 connect + AppServerSession::new + bootstrap`；`:65 resume_thread`；无输入重发 | ✓✓（E-2 四要素逐项吻合） |
| `tests.rs:1086`/`:1528`、`websocket.rs:3062`、`websocket_refresh_tests.rs:716` | 同 | 四个测试函数名与行号逐字亲核 | ✓✓（§6 官方不变量引用成立） |

### 3.3 cordcode-ios（@ fe421cdc；三锚点文件 bd46169..fe421cdc 零改动亲核）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `ProjectionStore.swift:164-181` 白名单（S-1b/E-5） | ProjectionStore.swift:164-181 | `:164 private static func isRetryablePullError(code: String?) -> Bool`；`:166-176` 11 项白名单——**不含 projection.hydrate_failed** | ✓✓ |
| `ProjectionStore.swift:887-900` 只读 code（E-3） | ProjectionStore.swift:884-908 | `:885 let code = (error as? CCCodeBridgeError)?.code`；`:887 if Self.isRetryablePullError(code: code)`——retryable 字段已解码未读取（另 `:949` late-apply 调用点同构） | ✓✓（E-3「只认 code」成立；:887-900 为调用点、:164-181 为函数定义，两锚各自成立） |
| `CCCodeBridgeModels.swift:314-331` 三字段 | CCCodeBridgeModels.swift:314-333 | `:317-319 let retryable: Bool?; let retryAfterMillis: Int64?; let attempts: Int?` | ✓✓ |
| `ChatViewModel.swift:438-490` 灾难重试 1s→30s | ChatViewModel.swift:438-494 + CCCodeBridgeTransport.swift:81-82 | `:438 startProjectionPullLoop`；`:467-470 .loading/.invalidated → attempt+=1，.failed → return`；`CCCodeBridgeTransport.swift:81-82 retryInitialSeconds = 1 / retryMaximumSeconds = 30` | ✓✓（「hydrate_failed 今天是硬错误」链路成立——`.failed` 即停） |

### 3.4 文档/复盘类锚点

| 方案原引用 | 实际位置 | 摘录 | 定位/语义 |
| --- | --- | --- | --- |
| macbridge think.md:2-6 / :45 / :415-425（含 :421） | 同 | 「已配对，等待 ChatGPT Desktop」横幅 + stream closed；「**待立项（协议面）**：backend_status_changed 推送」；「~750/s 累积…attach failed: stream closed（318 次）…0.154.0-alpha.6.2 对每条客户端 ack 回一条终态 error 通知」 | ✓✓（§1 复盘证据、OD-2 裁决、S-7 事故形态引用全部成立） |
| cordcode-ios think.md:432（§1）/ :477（§6） | :432 对应方案记录的 bd46169（`git show bd46169:think.md` 亲核该行即此内容）；当前 fe421cdc 下同内容在 **:477**（think.md 该区间 +45 行） | 「Mac 端 codex-remote 自 11:36 起病态…至 14:00 未恢复」 | **双框架混用，内容均真实**（§1 用 bd46169 框架、§6 用当前框架，无漂移说明）→ R2-A4② |
| 前置方案 plan:508-509 / :588-599 / :597 | docs/2026-08-26-…plan.md 同行号 | 「未取样前不得写死提前量或退避」；「官方仍然不在 controller WSS envelope 上交付 reconnect cursor…（产品 fail-closed，不得广告）」；「任务 7 官方 iOS controller 共存 / HTTP 409」 | ✓✓（S-4 门约束、Gate P0 裁决、非目标引用全部成立） |
| 2026-09-24 文档污染声明 | 该文档 :20（F-13 自证「无 Kimi 系 backend，r1–r3 评审证据均误」）；残留 :61/:120/:416（另 :434）；所引锚点 `catalog_recent_view.go`、`RuntimeManager.swift:205` 真实存在 | — | ✓✓（污染声明准确；本方案引用未扩散污染命名） |

### 3.5 其余已亲核锚点清单（定位+语义均成立，摘录从略）

backoff.go:15-56 全区间；ws.go:316-344 全区间；stream.go 各段；pairing_persist.go/pairing.go/agent.go/codec.go/session.go/history_paginated.go/chatgpt_auth.go/diagnostics.go 各段（见 §3.1 行号）；handlers_projection.go:122-127/147/152-156/176-181/210-226/430/483/599-606/852/863-871/876/1015/1079-1083/1142/1157；projection_kernel.go:889/925-941/996/1115-1123/1160/1319；main.go:1046-1066；session_discovery.go:159；官方 websocket.rs:74-79/86-145/201-207/965-988/996-1028/1031-1067/112-138/1140-1152/1154-1168/1275-1330/1342-1351/1511-1652/3062；transport/mod.rs:24；client_tracker.rs:134-143/221/250-289/312-323；segment.rs:19-23/301/327/445-467；protocol.rs:114-119；enroll.rs:26/235-264；server_api.rs:24/27-28/140/177/366-386；backoff.rs:7-17；remote.rs:181-632/510-518；reconnect.rs:30-122；tests.rs:1086/1528；websocket_refresh_tests.rs:716；iOS 三文件（§3.3）；`catalog_native_membership.go:16`（8s，范围外抽核）。

---

## 4. 本轮意见（R2-ID；无阻塞）

### R2-A1 [建议] S-3 缺口检测的游标推进与触发语义未写明

- **位置**：§3.3「缺口检测」段（「`seq_id > lastSeq + 1` → 记录缺口范围与关联 thread…缺口 → 触发 S-2 对账」）。
- **证据**：去重判定表对「正常推进」只写了 `== lastSeq + 1` 的情形；`> lastSeq + 1` 分支未说明高水位游标是否推进到收到的 seq。信封传输层不携带 thread id（`envelope.go:34-49`，thread 身份在 payload 内），「关联 thread」的归属机制（按缺口后首条信封 payload？按 stream 的 observed 集？）与「受影响会话」的覆盖边界（S-2 对账集合为 `observed ∩ turnByThread 非空`——无在飞 turn 的 thread 缺口会被记录但不会被对账）未显式声明。
- **影响**：按字面实施不推进游标时，一次缺口后的每个后续信封都会重复记缺口/重复触发对账（受 3s 循环限速、对账幂等，非正确性缺陷，但与「无缺口零开销」的设计意图不符且浪费权威拉取）；thread 归属歧义会让实施者自行发明范围。
- **修订方向**：§3.3 补两句——① 检测到缺口后高水位游标推进到收到信封的 `(seq, seg)`，缺口范围记录一次；② 「关联 thread」的判定方式与「无在飞 turn 的 thread 仅记日志、不进对账集合」的诚实边界（或说明为何按 stream 全量对账）。
- **闭合标准**：v1.3 缺口检测段对游标推进与触发范围可判定，单测计划含「缺口后不重复触发」断言。

### R2-A2 [建议] S-7 的依赖声明与 §3.7 自述不一致（补发 ack 的游标状态来源）

- **位置**：§5 依赖序「S-3 的 ack `SegmentID` 子项 → S-7 有序依赖（S-3 的去重/缺口部分不阻塞 S-7 之外的工作）」vs §3.7「立即以 S-3 的 per-stream 最高已见 `(seq, segment)` 游标补发一条 ack（**游标状态与 S-3 共享，不新增状态**）」。
- **证据**：S-7 补发 ack 所需的 `(lastSeq, lastSeg)` 游标是 §3.3 去重节维护的状态，不属于「ack `SegmentID` 子项」；若实施者按 §5 字面只做 ack 子项即开工 S-7，「最高已见游标」无来源。括号句「S-3 的去重/缺口部分不阻塞 S-7 之外的工作」语序歧义（读不出主语）。
- **影响**：依赖图与设计正文不一致，实施排序可能产生无游标可用的 S-7；§6「S-1a/S-1b/S-2/S-3/S-7 为主链」的实际排序不受影响，风险低。
- **修订方向**：§5 S-7 依赖改为「S-3（至少：ack `SegmentID` 子项 + per-stream `(seq, segment)` 游标维护）」，并改写括号句为明确语序（如「S-3 其余部分不构成 S-7 的前置」或如实列出）。
- **闭合标准**：v1.3 中 S-7 的前置覆盖游标状态来源，两处表述一致。

### R2-A3 [建议] S-7 完成断言缺「接受→解除熔断→恢复正常逐信封 ack」路径

- **位置**：§3.7 完成断言（仅列：armed→T→补发且携带最高游标；sentinel 重现→重新 armed；未 armed→与现状逐字节一致）。
- **证据**：S-7 的核心恢复行为「transport 接受（无 sentinel 再现）→ 熔断解除、后续信封恢复正常逐条 ack」在断言清单中无对应项。ack 正常情况下不被应答（`stream.go:44-47` 注释：relay ACK 只证明 relay 收帧），「接受」只能以「补发后无 sentinel 再现」判定——该判定窗口与解除时点也未写明。
- **影响**：实现可以做成「永不解除熔断、仅每 T 秒补发一条 ack」而通过全部所列断言——该形态功能上仍可缓慢排空缓冲（每条补发按最高游标清除），但与 §3.7「恢复正常逐信封 ack」的设计声明不符，且恢复路径（S-7 的存在意义）无回归保护。
- **修订方向**：§3.7 补一条断言：「补发后观察窗口内无 sentinel → 熔断解除，后续信封恢复正常逐条 ack（与未 armed 行为一致）」，并写明解除判定的观察窗口语义（如「补发后的下一条入站信封未携带 sentinel 即解除」或等效可测表述）。
- **闭合标准**：v1.3 断言清单覆盖解除路径，定向单测可据此判定。

### R2-A4 [建议] 两处锚点行号精度（不影响命题）

- **位置/证据**：① §3.1「kernel 失败记录…透传（`handlers_projection.go:160-171`）」——实际 default 分支为 **:161-173**（`:160` 是上一个 case 行；语义成立，本轮亲核）；② 同一 iOS think.md 条目在 §1 引 `:432`（方案记录来源 bd46169 框架，`git show bd46169:think.md` 亲核该行内容吻合）而 §6 引 `:477`（当前 fe421cdc 框架，亦吻合）——同文档双框架混用无说明，实施者按当前树查 §1 的 :432 会落到无关内容。
- **修订方向**：① 行号改 :161-173；② 统一 iOS think.md 引用并加一行漂移注记（如「:432@bd46169，当前 fe421cdc 下为 :477」）。
- **闭合标准**：v1.3 两处与源码/来源框架一致。

---

## 5. 复审处置与回归核查（r1 → v1.2）

| r1 意见 | 本轮裁决 | 依据（本轮亲核） |
| --- | --- | --- |
| F-1（阻塞）S-3 去重规则会丢弃大消息后续 chunk | **闭合** | §3.3 新判定表对 plain/chunk 分别可判定且与官方 `(seq, seg)` 游标语义一致（`websocket.rs:123/129`）；关键负例「重放未送达 chunk（seg > lastSeg）→ 接受」在新规则下正确；镜像源已改引 ack 游标路径；§5 单测含「同消息不同 segment 不丢弃」负例；「理论不可达」分支以 `segment.rs:327`（整体或全 chunk 二选一）为据、fail-visible 处理安全 |
| F-2（阻塞）E-11 清除子声明无源码支撑 + 风险 5 恢复界错误 + 对策触发错配 | **闭合** | E-11 重写与源码逐点一致（Buffer 四方法/remove 仅 ack :136/close_client :312-323/join-set :1140-1152/idle sweep :1154-1168/全局 used :109/:131 对 128 :996）；风险 5 链条本轮逐环亲核成立，**含 r1 未展开的 pong 环节**（pong 信封走受门限的 server_event_tx：`client_tracker.rs:250-289`，WS 传输 ping 不受门限：`websocket.rs:999-1009`——「host 连接存活」与「MacBridge 60s 判死重连」同时成立）；S-7 升主链有完成断言；「E-10 复现才修」已废除，S-7 验收以定向单测为准、E-10 只承担可见性，与致因匹配；残余风险如实登记 |
| F-3（建议）S-2 接线未指明 | **闭合** | 四要素宿主全部亲核存在：触发点 per-thread attach goroutine（`session.go:195-211`，免造 WaitGroup、单 attach 卡 15s 不阻塞他 thread）；seam 先例 `agent.go:168-180`（one-slot、数据不随信号走）+ 桥侧消费先例 `session_discovery.go:159`；消费宿主 `main.go:1046-1066` 3s 循环；「全部 attach 完成后」已修正为 per-thread 完成点 |
| F-4（建议）S-1a 分类枚举缺 source-unavailable；E-5 缺 mid-hydrate 落点 | **闭合** | `errProjectionSourceUnavailable`（`:430` 定义、`:852`/`:876` 返回点）→ retryable=false 的归类理由（静态条件、防 S-1b 接上后无限空转）成立且与现状 iOS 行为（白名单缺 → 硬错误）无回归；E-5 补 `:1079-1083` 落点亲核属实 |
| F-5（建议）S-4 revoked 去向未指明 | **闭合** | §3.4 二分明确：revoked → 立即 invalidateRevokedPairing（与 `pairing_persist.go:186-189`/`:287-289` 既有语义一致）；其余失败沿用连接死亡路径，无新分叉 |
| F-6（建议）三处锚点精度 | **闭合** | ① backoff.rs 归属拆分与源码一致（backoff.rs 无 cap，cap/归零在 `websocket.rs:78-79`/`:1342-1351`）；② S-1b `:164-181` 与函数实际行号一致；③ chunk 共享 seq 限定已入 §2.2 |
| （无 F-ID，v1.2 自报）E-12 chunk ack 缺 SegmentID | **确认采纳** | 见 §1；两端源码 + 官方协议文档逐字核验，因果链成立，修复为最小字段补齐，同时是 S-7 补发 ack 的正确性前提 |
| （无 F-ID，v1.2 自报）S-7 熔断有界化 | **确认采纳** | 见 §1；设计依据（E-11）与量级参照（`server_api.rs:27-28` 24–36s）亲核成立；残余风险登记诚实；本轮仅提出断言完备性建议 R2-A3 与依赖边建议 R2-A2 |

**回归检查**：v1.2 修订未引入新的源码引用错误；全局引用、OD/门关系、能力范围经 §7 核查无回归（见 §7 记录）。

---

## 6. audit-plan 专项接入状态

按 adapter 执行：本方案的外部内容形状声明（Remote Control envelope/seq/segment/chunk ack 形状/cursor header/token refresh 时序/TUI 重连模式/官方测试不变量）全部锚定官方源码，属「描述已实现行为、以读实现验证」类别（audit-plan SKILL.md「When NOT To Use」第 2 条明确此模式），本轮逐锚点读取官方源码核验，**全部成立**——含 v1.2 新增的 chunk ack 形状（`protocol.rs:114-119` + `websocket.rs:123`）与 pong/反压交互（`client_tracker.rs:250-289` + `websocket.rs:996-1028`）。方案未提出任何无锚点的新外部格式解析（S-3 消费已解析的 `Envelope.SeqID/SegmentID`；S-1/S-2/S-4/S-5 复用已实现路径），无「描述了但无样本/无锚点的内容类型」遗留。E-9（ctrlExp 分布）与 E-10（断线复现）为运行证据门，按授权边界本轮未采集、如实 pending，且已分别精确门住 S-4 与完成验收，不存在「先实现后补票」。

## 7. 门控一致性与覆盖核查记录

- **OD-1** pending → 阻塞 S-1a/S-1b 实施：§3.8/§4 Gate B/§5 三处一致 ✓；选项验收独立（标志方案满足验收 1+3；白名单方案自认无法满足验收 3、需另设 code 拆分）✓。
- **OD-2** decided（think.md:45 亲核）→ S-6 仅桥内暴露、不新增跨仓协议 ✓；与任务基线「backend_status_changed 另立」一致。
- **OD-3** pending 随 E-9；**E-9** pending → S-4 整体阻塞（§3.4/§4/§5 一致）✓；「未取样前不得写死提前量」约束源 plan:508-509 亲核 ✓；官方 5 分钟先例仅作量级参照、方案明示 ctrl token 与 server token 是不同凭证、E-9 仍须实测 ✓。
- **E-10** pending（实施期）→ S-1/S-2/S-3 完成验收门 + S-7 可见性验收 ✓；S-7 行为验收以定向单测为准（sentinel 风暴依赖上游 host 版本缺陷、真机不可控复现——与致因匹配，v1.2 已修正 v1.1 的错配触发）✓。
- 「方案通过 ≠ 实施就绪」：§6 阻塞清单 + 头部授权声明 + 本报告 §1 implementation_readiness ✓。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（E-9 门内，行为级验收未列入 §1 与任务基线 ①–④ 一致）、R-4→S-3+E-12（验收 4）、R-5→S-6——对应齐全 ✓；风险 5 对策（S-7）升主链后 §6 与 §5 一致 ✓。
- 切片依赖序：S-1a→S-1b、S-2→S-3、S-3→S-7（见 R2-A2 精度意见）、S-4/S-5/S-6 可独立；失败处理列完整；无数据迁移、无不可逆动作 ✓。
- 风险 1–4 对策与源码相符（风险 3 的 v1.1 降级依据 `websocket.rs:1031-1067` 亲核成立且已补 chunk 限定）；风险 5 重写后本轮逐环亲核成立（含 pong 环节裁决）✓。

## 8. 剩余门与下一阶段

- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义，门住 S-1a/S-1b）、OD-3（S-4 提前量 T，随 E-9）、E-9（ctrlExp 真机 fixture ≥5 样本，门住 S-4 整体）、E-10（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期采集）。
- 4 条建议（R2-A1~A4）可在下一版一并修订（均为小改），也可按 owner 判断留待实施期处理——**不构成方案通过的前置条件**；若修订则版本号递增至 v1.3 并在开头声明改动范围。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证与 UI automation 另行授权。

---

## 评审员交接块

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: c80296e5260c1ea94ce6ba075301f0942c71e09dbce82d423ddc673cb6673950
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r2.md
scope: full
blockers: 0
advisories: 4
open_gates: [OD-1, OD-3, E-9, E-10]
round_complete: true
contract_version: plan-contract-v1.1
```
