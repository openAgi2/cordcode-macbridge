# codex-remote 断线韧性与恢复专项方案 评审报告（r1）

- 日期：2026-09-26
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.1）
- 评审时方案 SHA-256：`2187f2c8f97a27a8c3dc068f90ec51bf82b7f04f0089981291f9bccea8f3a180`（与派发记录一致，`shasum -a 256` 亲算）
- 评审范围：**full**（首轮，无历轮报告；评审员模式 fresh——先独立形成风险清单，再逐锚点核对）
- 契约版本：plan-contract-v1.1（plan-review SKILL + plan-contract.md + review-guide.md + audit-plan-adapter.md + audit-plan SKILL 均已读取；audit-plan 接入已按 adapter 执行，见 §5）
- 结论：**REVISION_REQUIRED**（blockers 2，advisories 4）
- 授权边界遵守声明：本轮仅写入本报告文件；方案、产品代码、其他文档一律只读，未 commit、未部署、未采集任何真机 fixture、未触碰工作树中 2 个无关未跟踪 docs。

---

## 1. 结论

### verdict: REVISION_REQUIRED

方案整体质量高：来源组合记录完整、复用调查表与证据门的锚点绝大多数精确定位且语义吻合（本轮亲核约 50 个锚点，仅 3 处精度问题）、门控关系（OD-1/OD-2/OD-3、E-9→S-4、E-10 完成验收、「方案通过 ≠ 实施就绪」）内部一致、R-1~R-5 与切片/验收对应齐全、S-1a/S-2/S-4/S-5/S-6 的设计在源码层面可行。

但全量核验发现 **2 个阻塞级缺陷**，都在 v1.1 新增的官方 transport 深挖增量里，且都是「方案写为确定事实/确定规则，但源码证据不支持或反证」的形态：

- **F-1（S-3 去重规则）**：官方 `build_chunk_envelope` 把一条消息拆成的所有 chunk 信封**共享同一 `seq_id`**（`segment.rs:445-467` 原文 `seq_id: envelope.seq_id`）。方案 S-3 写的规则「`seq_id <= last_seen` → 跳过」按字面实施会把每条大消息（>150KB 触发拆分）的第 2..n 个 chunk 段当重复丢弃，重组永不完成——**静默数据丢失**，恰好丢的是大 payload。且「镜像官方 `client_tracker.rs:134-143`」所选的镜像源是 client→host 方向、仅 `ClientEvent::ClientMessage` 分支的去重（chunk 在 `client_tracker.rs:221` 是直通），语义不可平移。
- **F-2（E-11 清除子声明 + 风险 5 恢复界）**：方案 E-11 写「清除只发生在 relay Ack 或 **client shutdown**」——官方 `BoundedOutboundBuffer` 只有 `new/insert/ack/server_envelopes` 四个方法，`buffer_by_stream.remove` 仅存在于 `ack()`（`websocket.rs:135-137`）；client 关闭三条路径（`ClientClosed`→`close_client` `client_tracker.rs:243`、idle sweep `websocket.rs:1154-1168`、join-set `websocket.rs:1140-1152`）**均不触及 outbound_buffer**。「client shutdown 清除」无源码支撑。由此风险 5 把停摆界定为「只能等 60s 判死→重连」是错的：MacBridge 重连换新 stream_id 后，被弃 stream 的未 ack 信封在 Desktop 进程存活期间**永久占用全局 128 容量**（反压阈值是全局 `used` 计数对 `CHANNEL_CAPACITY`，`websocket.rs:996`），单次熔断满缓冲事件即可让 host writer 对**所有 stream** 永久停止拉新事件、重连不可愈、整腿死亡直到 Desktop 重启——这正是本方案要消灭的「驱动长时间不自愈」失败类（ios think.md:432/477）。而对策「若 E-10 复现停摆才修」的触发条件与致因不匹配（熔断器由 transport-error sentinel 风暴激活，E-10 是干净断 relay 10–60s，大概率不会复现该场景）。

两项都必须在实施前修订方案文档。修订量小（S-3 一段规则 + E-11 一行 + 风险 5 一段对策定位），不影响方案主体架构与切片划分。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 即使修订后通过，可开工切片仍为 **0**（授权边界：owner 未授权任何代码改动）。
- 实施前置门（与阻塞意见分开列）：**OD-1**（owner 确认重试语义，阻塞 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，阻塞 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门，实施期）。
- OD-2 已决（think.md:45 亲核：「**待立项（协议面）**：桥侧 `backend_status_changed` 推送」）；E-1~E-8 本轮亲核全部成立；**E-11 主体成立但含 F-2 所述的一处未支撑子声明**。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核）

| 仓库 | 工作树路径 | 分支 | 提交 | 未提交状态 | 核验方式 |
| --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 0772178358fb582474eb5aada5f50b7d8608053e | 干净（仅 2 个无关未跟踪 docs：2026-09-24-zcode-plan-agents-{design,acceptance}.md，未触碰） | `git rev-parse HEAD`/`git status --porcelain` 亲跑 |
| cordcode-macbridge（代码锚点核验来源） | 同上工作树（与 main @ 07721783 同提交） | — | 07721783 | — | 方案 §2.1 记录 715104c6；`git log/diff 715104c6..07721783` 亲跑：仅 92f13c22/07721783 两个 docs 提交、唯一改动文件即本方案文档（185 insertions），**代码零改动，锚点核验等价**（与简报声明一致） |
| cordcode-ios | /Users/jacklee/Projects/cordcode-ios | main | fe421cdc136930e3729b6b3d5c4479d5ecb90df4 | 干净 | `git rev-parse HEAD` 亲跑；方案记录 bd46169，`git diff --stat bd46169..fe421cdc -- ProjectionStore.swift CCCodeBridgeModels.swift ChatViewModel.swift` 为空（三锚点文件零改动、无行号漂移，与简报声明一致）；**think.md 在该区间 +45 行，方案引用的 ios think.md:432 内容漂移到 :477（内容亲核一致，见 §3.4）** |
| openai/codex（官方） | /Users/jacklee/Projects/codex | FETCH_HEAD（本地 main 落后） | e72da2b53805894878023d01949a25a082e0a5cb | 只读 checkout | `git rev-parse FETCH_HEAD` 亲跑，与方案 §2.1 一致；`git cat-file -t d047c33a1b` 亲跑确认该 commit 存在（`fix(remote-control): avoid server token refresh retry storms (#30201)`） |

### 2.2 覆盖声明

- 本轮为 full 全量评审：§2.2 复用调查表 13 行、§2.3 现状摘要、§3 设计（S-1~S-6 + OD 表）、§4 证据门 E-1~E-8/E-11/E-9/E-10、§5 切片表、§6 风险 1–5 与官方测试引用，**全部锚点逐一亲核**（见 §3 锚点核验表）；门控一致性、设计可行性、覆盖完整性、audit-plan 专项均已检查。
- 未核项（如实声明）：①「会话列表 thread/list 8s 超时」一句（属 2026-09-24 已交付方案范围，本方案明示不在范围内，未复核）；② relay（chatgpt.com 闭源服务端）内部行为——F-2 的结论只依赖 host 侧源码（host 侧不存在任何 client 关闭清除 outbound_buffer 的路径），不依赖对 relay 内部的假设；③ E-9/E-10 为 pending 运行证据，本轮按授权边界未采集，状态如实登记。
- 本轮无历轮报告（首轮），无复用核验；全部为亲核。

---

## 3. 锚点核验表

说明：定位=行号指向实际行为；语义=原文支持方案命题。「✓✓」=定位与语义均成立。仅列差异与关键项摘录；全部锚点均已亲核，未核项已在 §2.2 声明。

### 3.1 MacBridge agent/codex-remote + go-bridge

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `backoff.go:15-56` 退避形状（1s 基准→30s 封顶、封顶归零、±10% jitter） | backoff.go:15-56 | `:16 reconnectBackoffBase = 1 * time.Second`；`:17 reconnectBackoffCap = 30 * time.Second`；`:42-46 if d >= reconnectBackoffCap { b.attempt = 0 }`；`:47 jitter := 0.9 + 0.2*b.rng.Float64()` | ✓✓ |
| `agent.go:103-135` WaitForRestore；`agent.go:110-119` ErrNotConfigured 条件 | agent.go:103-135 | `:110-111 if a.pairing == nil \|\| !a.pairing.hasPersistedIdentity() { return ErrNotConfigured }`；`:118-119 if snapshot.Phase == PairPhaseFailed { return ErrNotConfigured }`；`:131 return core.ErrRestoreInProgress` | ✓✓（S-1a 分类的事实基础成立：无身份/phase=failed → 终态；offline/restoring → 瞬态） |
| `ws.go:318` streamIdleLimit 60s；`ws.go:316-344` 判活 | ws.go:316-344 | `:317 pingInterval = 10 * time.Second`；`:318 streamIdleLimit = 60 * time.Second`；`:328 if idle := stream.IdleFor(); idle > streamIdleLimit` | ✓✓ |
| `ws.go:87` WSS 握手 15s | ws.go:87 | `:87 HandshakeTimeout:  15 * time.Second,` | ✓✓ |
| `chatgpt_auth.go:51` 探针 12s | chatgpt_auth.go:51 | `:51 ctx, cancel := context.WithTimeout(ctx, 12*time.Second)` | ✓✓ |
| `stream.go:279-283` readLoop 错误判死 | stream.go:279-283 | `:279-282 env, err := s.conn.Read(); if err != nil { s.fail(err); return }` | ✓✓ |
| `stream.go:307-314` pong status 判死 | stream.go:307-314 | `:311-312 if env.Status != "active" { s.fail(...) }` | ✓✓ |
| `stream.go:273-345` readLoop 现状不校验 seq | stream.go:273-345 | 全 switch 仅 routing/cursor/ack/pong/deliver/chunk，无 seq 单调性检查 | ✓✓（E-6/S-3 的「现状无缺口检测」成立） |
| `stream.go:359-373` ack 熔断器 | stream.go:359-373 | `:362-367 if bytes.Contains(payload, transportErrorSentinel) { ... s.acksDisabled = true }`；`:397-399 if disabled { return }`（ack 直接跳过） | ✓✓（熔断激活后**本 stream 生命周期内**停发一切 ack，无有界时长、无最低频率——风险 5 的机制描述成立；另注：`ack()` 仅在 `env.SeqID != nil` 时发送，`:391-393`） |
| `stream.go:458-466` / E-1 `stream.go:459-460` SubscribeCursorHeader 死代码注释 | stream.go:458-466 | `:459-460 // Owner-accepted known gap: live target never delivered one; callers must not fabricate it.` | ✓✓ |
| `pairing_persist.go:23-24,60-61,167` ctrlExp 持久化 | pairing_persist.go:23-24, 60, 167 | `:23 CtrlExp string \`json:"ctrlExp"\``；`:60 CtrlExp: p.state.ctrlExp`；`:167 p.state.ctrlExp = rec.CtrlExp` | ✓✓ |
| `pairing_persist.go:186-191` restoreOnce 强制 refresh | pairing_persist.go:186-191 | `:186 if err := p.refreshControlToken(ctx); err != nil {`（`:187-189` errPairingRevoked 透传；`:190` 其余仅 Warn 后用旧 token 继续） | ✓✓ |
| `pairing_persist.go:250-296` watchBinding/restoreOnce；`:262-274` 2s 轮询 | pairing_persist.go:250-296 | `:262 time.Sleep(2 * time.Second)`；`:280 ctx, cancel := context.WithTimeout(..., 45*time.Second)`；`:287-289 errPairingRevoked → invalidateRevokedPairing → return`（永久停止）；`:291 a.pairing.markOffline("已配对，等待 ChatGPT Desktop")` | ✓✓（S-6 的前提成立：watchBinding 把一切非 revoked 错误折叠为同一句横幅；对比 `:126-131`/`:236-240` reconnectFromStore/restorePersistedPairing 反而有细分文案） |
| `pairing.go:158-182` revoked→删配对永久停止 | pairing.go:158-182 | `:162 p.forgetPersistedPairing()`；`:177 p.state = pairState{phase: PairPhaseFailed, ...}`（401/403 判定点在 `:383-384`/`:408-409`） | ✓✓（锚点指向终态清理路径，语义成立；401/403 检测行未单独引用，不影响命题） |
| `pairing.go:370` refreshControlToken（S-4 复用） | pairing.go:370-419 | `:383-384/:408-409 status==401/403 → errPairingRevoked`；`:414 applyCtrlToken`；`:417 _ = p.savePersistedPairing()`（成功即持久化） | ✓✓（S-4「成功则原地更新持久化 token」直接成立；revoked 处置见 F-5） |
| E-7「全仓 grep 无调度调用」 | 全仓 grep 亲跑 | `grep -rn "refreshControlToken" --include="*.go"` 非测试命中仅 `pairing.go:370`（定义）与 `pairing_persist.go:186`（restoreOnce 调用） | ✓✓（无到期前调度，E-7 成立） |
| `history_paginated.go:830-941` mapColdPage/ReadColdHistory；S-2 `:871-941` desc 首页 | history_paginated.go:830-854, 871-941 | `:871 func (a *Agent) ReadColdHistory(...)`；`:910 page, err := a.readTurnsPage(ctx, threadID, "")` 后 `:918-920` 逆序转 asc（上游取 desc 首页） | ✓✓ |
| `session.go:161-212` BindClient 只订阅+baseline | session.go:161-212 | `:195-210 for _, threadID := range observed { go func() { attachLiveThreadOn + refreshSessionCollaborationAfterAttach + refreshSessionGoalAfterAttach } }`——无 turn 对账；attach 为 per-thread goroutine（见 F-3） | ✓✓（E-6 成立） |
| `codec.go:112-126` turnByThread 跨重连保留；`:118-124` idempotent 注释 | codec.go:22, 112-126, 161-171, 320, 355 | `:22 turnByThread map[string]string`；ResetNativeSessionState（:112-126）重置 lastErrorParams/collaborationByThread/goalByThread 而**不含** turnByThread；`:118-124` 注释含 "re-observed items are idempotent" | ✓✓（「保留」由 reset 函数的刻意缺席证明，锚点成立） |
| `envelope.go:16` 1GB | envelope.go:16 | `:16 ReassembledMessageMaxBytes = 1073741824` | ✓✓ |
| `diagnostics.go:19-35` StructuredInstanceReadiness | diagnostics.go:19-35 | `:24-25 available`；`:30-32 service_not_running + snap.Message`；`:34 pairing_required`——现有三分类不含 offline/restoring/revoked/env-missing 细分（S-6 扩展点成立） | ✓✓ |
| `handlers_projection.go:122-127` forceColdInspection 含 codex-remote | handlers_projection.go:122-127 | `:127 msg.BackendID == "codex-web" \|\| msg.BackendID == "codex-remote"` | ✓✓ |
| `handlers_projection.go:152-156` hydrating 可重试 | handlers_projection.go:152-156 | `:153-154 code = "projection.hydrating"; retryable = true` | ✓✓ |
| `handlers_projection.go:176-181` WireError 三字段 | handlers_projection.go:176-181 | `:176-181 conn.SendResult(..., &WireError{Code, Message, Retryable, RetryAfterMillis, Attempts})` | ✓✓ |
| `handlers_projection.go:210-226` delta_at_head 不经 driver | handlers_projection.go:210-226 | `:211 if params.SinceRev != 0 && params.SinceRev == headRev && !resumeSelection.EpochChanged` → 空 patches 返回，无 agent 调用 | ✓✓ |
| `handlers_projection.go:599-606`/`:603-605` source_inspection_failed 一律 retryable=true | handlers_projection.go:599-606 | `:603-605 h.markHydrateFailed(backendID, sessionID, "projection.source_inspection_failed", err.Error(), true,)` | ✓✓（S-1a 的问题陈述精确成立） |
| `handlers_projection.go:863-871`（§2.3 :864-871）WaitForRestore 接入 | handlers_projection.go:863-882 | `:864-865 if waiter, ok := agent.(core.RestoreWaiter); ok { if err := waiter.WaitForRestore(ctx); err != nil {`；`:866-868 ErrRestoreInProgress → errProjectionHydrating`；`:869 其余原样返回` | ✓✓ |
| E-5 mid-hydrate 落点 | handlers_projection.go:1079-1083（方案未引） | `:1080-1082 h.markHydrateFailed(backendID, sessionID, "projection.source_read_failed", err.Error(), true,)` | 定位成立/语义成立，但**方案 E-5 只锚 :599-606，mid-hydrate 实际落点 :1080 未引用**（见 F-4） |
| `projection_kernel.go:1115-1123` 孤儿收口仅桥重启 | projection_kernel.go:1115-1123 | `:1115-1121 if backendID == "codex-remote" { ... RecoverOrphanDetailLoadingV2(&restored) }`（checkpoint restore 路径） | ✓✓ |
| `main.go:1046-1067` 3s AttachLiveCatalog 周期 | main.go:1046-1066 | `:1056 ticker := time.NewTicker(3 * time.Second)`；`:1050 attacher.AttachLiveCatalog(attachCtx)`（现仅订阅，见 F-3） | ✓✓ |
| `pairing_persist.go:163-165`（方案未引，F-2 佐证） | pairing_persist.go:163-165 | `:163-164 // stream_id is connection-epoch state, never enrollment state.` | ✓✓（重连换新 stream_id 成立） |

### 3.2 官方 openai/codex（FETCH_HEAD @ e72da2b）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `websocket.rs:74-75` pong timeout 60s | websocket.rs:74-75 | `:74-75 REMOTE_CONTROL_WEBSOCKET_PONG_TIMEOUT = Duration::from_secs(60)` | ✓✓ |
| `websocket.rs:78` cap 30s；`:1343-1345` cap 归零 | websocket.rs:78, 1342-1351 | `:78-79 REMOTE_CONTROL_RECONNECT_BACKOFF_CAP = Duration::from_secs(30)`；`:1343-1345 let reconnect_backoff_reset = reconnect_delay == REMOTE_CONTROL_RECONNECT_BACKOFF_CAP; *reconnect_attempt = if reconnect_backoff_reset { 0 } ...` | ✓✓ |
| `websocket.rs:201-207` host 记录 client 回传 cursor | websocket.rs:201-207 | `:206-207 if let Some(cursor) = client_envelope.cursor.as_deref() { self.subscribe_cursor = Some(cursor.to_string()) }` | ✓✓ |
| `websocket.rs:1275-1330` 重连带 x-codex-subscribe-cursor | websocket.rs:1275-1330 | `:1322-1327 if let Some(subscribe_cursor) = subscribe_cursor { set_remote_control_header(headers, REMOTE_CONTROL_SUBSCRIBE_CURSOR_HEADER, ...) }` | ✓✓（E-1「cursor 是 host↔relay 腿机制」成立） |
| `remote.rs:181-632` API 面无 reconnect | remote.rs:180-632 | `:181 pub async fn connect` … `:625 pub async fn next_event` … `:632 pub async fn shutdown`——impl 块内无任何 reconnect 方法（grep 亲核） | ✓✓ |
| `remote.rs:510-518` 断线即终止、pending 全失败 | remote.rs:510-518 | `:516-518 for (_, response_tx) in pending_requests { let _ = response_tx.send(Err(IoError::new(err_kind, err_message.clone()))); }` | ✓✓ |
| `reconnect.rs:30-122` TUI caller 侧模式 | tui/src/app/reconnect.rs:30-122 | `:53 let deadline = ... + Duration::from_secs(120);`；`:54 for delay in [0, 1, 2, 4].into_iter().chain(std::iter::repeat(8))`；`:57 let client = crate::app_server_connection::connect(&target).await?;` + `:62 session.bootstrap` + `:65 resume_thread`；`:1-3 //! ... no user operation is retried.` | ✓✓（120s 共享 deadline、[0,1,2,4]+恒8s、每次全量重建、不重发输入——四项全中） |
| `websocket.rs:86-145` BoundedOutboundBuffer | websocket.rs:86-145 | `:87 buffer_by_stream: HashMap<(ClientId, StreamId), VecDeque<ServerEnvelope>>`；`:124-134` ack 按 `(seq_id, segment_id.unwrap_or(usize::MAX))` 游标 retain 清除；`:135-137 if buffer.is_empty() { self.buffer_by_stream.remove(&key); }` | ✓✓ 定位成立；**「清除只发生在 relay Ack 或 client shutdown」后半句无源码支撑**（见 F-2） |
| `transport/mod.rs:24` CHANNEL_CAPACITY=128 | transport/mod.rs:24 | `:24 pub const CHANNEL_CAPACITY: usize = 128;` | ✓✓ |
| `websocket.rs:996`（方案未引，F-2 佐证）反压为全局阈值 | websocket.rs:996 | `:996 let outbound_has_capacity = *used_rx.borrow() < super::CHANNEL_CAPACITY;`（used 为跨 stream 全局计数，`:109`/`:131` 增减） | ✓✓ |
| `websocket.rs:965-988` host 重连全量重发未 ack | websocket.rs:965-988 | `:965-971 let server_envelopes = state.lock().await.outbound_buffer.server_envelopes().cloned().collect::<Vec<_>>();` 后逐条重发 | ✓✓（重放携带原 seq → 同 stream 重复 seq 场景成立，S-3 去重动机成立） |
| `websocket.rs:1031-1067` seq 按 (client_id, stream_id) 从 1 起单调 | websocket.rs:1031-1067 | `:1035-1038 let seq_id = *state.next_seq_id_by_stream.entry(seq_key.clone()).or_insert(1);`；`:1065-1067 state.next_seq_id_by_stream.insert(seq_key, seq_id.saturating_add(1));` | 定位成立/语义**在消息级**成立；**chunk 信封共享消息 seq_id 的限定缺失**（`segment.rs:301-405` 拆分、`segment.rs:445-467` `build_chunk_envelope` 原文 `seq_id: envelope.seq_id`）→ 见 F-1 |
| `client_tracker.rs:134-143` client→host 只去重不查缺口 | client_tracker.rs:134-143 | `:134-141 ClientEvent::ClientMessage { message } => { if let Some(seq_id) = seq_id && ... last_inbound_seq_id.is_some_and(\|last\| last >= seq_id) && !is_initialize { return Ok(()); } }`（chunk 在 `:221 ClientEvent::ClientMessageChunk { .. } \| ClientEvent::Ack { .. } => Ok(())` 直通） | ✓✓ 定位成立；**作为 host→controller 镜像源不可平移**（方向不同 + 仅 ClientMessage 分支）→ 见 F-1 |
| `enroll.rs` server_token_refresh_requirement_at（5 分钟提前量） | enroll.rs:26, 246-264 | `:26 const REMOTE_CONTROL_SERVER_TOKEN_REFRESH_SKEW_SECS: i64 = 5 * 60;`；`:256 if expires_at > now + ...SKEW_SECS ... return NotNeeded;`（到期前 5 分钟内为 Proactive） | ✓✓ |
| `websocket.rs:1511-1652` connect 前判定刷新 | websocket.rs:1588-1592 | `:1591-1592 enrollment...should_refresh_server_token()` → `:1610 refresh_remote_control_server(...)` | ✓✓ |
| `server_api.rs:27-28, 366-386` 24–36s 均匀随机退避 | server_api.rs:27-28, 381-386 | `:27-28 BACKOFF_MIN_SECS: u64 = 24; BACKOFF_MAX_SECS: u64 = 36;`；`:382-385 rand::rng().random_range(MIN..=MAX)` | ✓✓；commit `d047c33a1b` 亲核存在 |
| `backoff.rs:12-17` 官方退避 200ms | async-utils/src/backoff.rs:12-19 | `:12 const INITIAL_DELAY_MS: u64 = 200;`；`:13 BACKOFF_FACTOR: f64 = 2.0;`；`:19 let jitter = rand::rng().random_range(0.9..1.1);` | 定位成立/语义成立；**「cap 30s、cap 后归零」归于此锚不准确**——backoff.rs 无 cap（cap/归零在 websocket.rs:78 与 :1342-1351，方案第一行已另引）→ 见 F-6 |
| `segment.rs:21` 官方重组上限 100MB | segment.rs:21 | `:21 pub(super) const REMOTE_CONTROL_REASSEMBLED_MAX_BYTES: usize = 100 * 1024 * 1024;` | ✓✓ |
| `tests.rs:1086` / `tests.rs:1528` | tests.rs:1086 / :1528 | `:1086 async fn remote_control_transport_reconnects_after_disconnect()`；`:1528 async fn remote_control_transport_clears_outgoing_buffer_when_backend_acks()`（测试体亲读：ack 后 buffer 清除断言） | ✓✓ |
| `websocket.rs:3062` | websocket.rs:3062 | `:3062 fn websocket_state_drops_replayed_client_chunks_after_completion()`（测试体亲读：完成后重放 chunk 被丢弃） | ✓✓ |
| `websocket_refresh_tests.rs:716` | websocket_refresh_tests.rs:716 | `:716-718 async fn expired_token_refresh_failure_throttles_reconnect_without_websocket() { assert_refresh_failure_blocks_websocket(...).await }`（helper 亲读：refresh 失败期间出现新 WS 连接即 panic） | ✓✓ |

### 3.3 cordcode-ios（@ fe421cdc；三锚点文件 bd46169..fe421cdc 零改动）

| 方案原引用 | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| `ProjectionStore.swift:164-180` 可重试 code 白名单 | ProjectionStore.swift:164-181 | `:164 private static func isRetryablePullError(code: String?) -> Bool {`；`:166-176` 白名单 11 项：projection.hydrating / disconnected / not_connected / relay.not_ready / relay.closed / request_timeout / send_timeout / ping_timeout / connect_timeout / hello_timeout / websocket.closed——**不含 projection.hydrate_failed** | ✓✓（§3.1 S-1b 引 163-180 起行差 1，见 F-6） |
| `ProjectionStore.swift:887-900` 只读 code、标志被忽略 | ProjectionStore.swift:884-908 | `:885 let code = (error as? CCCodeBridgeError)?.code`；`:887 if Self.isRetryablePullError(code: code)`——retryable 字段未读取 | ✓✓（E-3「已解码未消费」成立） |
| `CCCodeBridgeModels.swift:314-331` 三字段已解码 | CCCodeBridgeModels.swift:314-333 | `:317-319 let retryable: Bool?; let retryAfterMillis: Int64?; let attempts: Int?` | ✓✓ |
| `ChatViewModel.swift:438-490` 灾难重试 1s→30s | ChatViewModel.swift:438-494 + CCCodeBridgeTransport.swift:81-83 | `:438 func startProjectionPullLoop`；`:467-470 case .loading, .invalidated: attempt += 1；case .disabled, .ready, .failed: return`；`:481-484 min(retryInitialSeconds * (1 << boundedAttempt), retryMaximumSeconds)`；`CCCodeBridgeTransport.swift:81-82 retryInitialSeconds = 1 / retryMaximumSeconds = 30` | ✓✓（`.failed` 即停 → hydrate_failed 今天是硬错误的链路成立） |

### 3.4 文档/复盘类锚点

| 方案原引用 | 实际位置 | 摘录 | 定位/语义 |
| --- | --- | --- | --- |
| macbridge think.md:2-6（Desktop 26.924 升级事故） | think.md:2-6 | 「报『已配对，等待 ChatGPT Desktop』横幅 + 『加载失败：codex-remote: stream closed』」 | ✓✓ |
| macbridge think.md:45（OD-2 裁决另立） | think.md:45 | 「**待立项（协议面）**：桥侧 `backend_status_changed` 推送」 | ✓✓ |
| macbridge think.md:421（attach failed 318 次） | think.md:421 | 「live catalog attach failed: stream closed（318 次）；iOS/web 间歇卡『执行中』」 | ✓✓ |
| cordcode-ios think.md:432（2026-09-20 病态 2.5h 不自愈） | **当前 fe421cdc 下为 think.md:477**（该文件在 bd46169..fe421cdc 间 +45 行；方案记录的 :432 对应其记录时点） | `:477 Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时、stream idle/closed、pairing 流反复重连），至 14:00 未恢复` | **行号漂移（须显式标注）/语义成立** |
| 前置方案 plan:588-599（Gate P0 fail-closed 裁决） | docs/2026-08-26-...plan.md:588-599 | 「官方**仍然不**在 controller WSS envelope 上交付 reconnect cursor…（产品 fail-closed，不得广告）」 | ✓✓ |
| 前置方案 plan:508-509（未取样不得写死提前量） | 同文件 :508-509 | 「『controller token 到期前主动刷新』的具体调度仍是 assumption pending Phase 0 fixture，未取样前不得写死提前量或退避」 | ✓✓（S-4 被 E-9 门的依据成立） |

---

## 4. 意见（F-ID）

### F-1 [阻塞] S-3 去重规则忽略「chunk 信封共享消息 seq_id」，按字面实施将静默丢弃大消息的后续 chunk 段

- **位置**：§3.3 S-3（「去重（`seq_id <= last_seen` → 跳过+计数，镜像官方 `client_tracker.rs:134-143` 的 `last_inbound_seq_id >= seq_id → drop` 语义，覆盖 host 侧未 ack 重放造成的同 stream 重复）」）；§2.2「入站 seq 语义」行（「`ServerEnvelope.seq_id` 按 `(client_id, stream_id)` 严格单调、从 1 起」）。
- **证据**：官方 `segment.rs:445-467` `build_chunk_envelope` 原文 `seq_id: envelope.seq_id`——一条消息被 `split_server_envelope_for_transport`（`segment.rs:301-405`，序列化超 150KB 即拆）后，**所有 chunk 信封携带同一 seq_id**，仅以 `segment_id` 区分；官方 ack 清除也按 `(seq_id, segment_id)` 双元游标（`websocket.rs:123-129`，`segment_id.unwrap_or(usize::MAX)`）。MacBridge 侧同构：`observeChunk` 以 `*env.SeqID` 为组装键（`stream.go:425-434`），ack 按信封逐条发送（`stream.go:390-407`）。而方案所镜像的 `client_tracker.rs:134-143` 是 **client→host 方向、仅 `ClientEvent::ClientMessage` 分支**的去重（chunk 在 `client_tracker.rs:221` 直通不查 seq），不可平移到 host→controller 的 chunk 流。
- **影响**：S-3 按字面实施后，每条触发拆分的大消息（大 tool 输出/长回复，正是 chunk 主路径）第 2..n 个 chunk 满足 `seq_id <= last_seen` 被跳过 → 重组永不完成 → **静默数据丢失**；同时方案自述的动机（「覆盖 host 侧未 ack 重放造成的同 stream 重复」——重放同样包含 chunk，`websocket.rs:965-988`）只完成一半。缺口检测半段（`seq_id > last_seen + 1`）在消息级语义下仍成立，不受影响。
- **修订方向**（最小充分）：把去重键改为 per-stream 的 `(seq_id, segment_id)` 游标并镜像官方 ack 游标比较语义（`websocket.rs:123-129`：`(seq, seg.unwrap_or(MAX)) <= cursor` → 重复），或改为消息完成级去重；§2.2「严格单调」补一句「chunk 信封共享消息 seq_id（`segment.rs:445-467`），单调性为消息级」。不要求改变 S-3 的切片边界、触发关系或 fail-visible 定位。
- **闭合标准**：v1.2 的 S-3 规则对 plain 与 chunk 两类信封分别可判定，且引用官方 `(seq, segment)` 游标锚点；§5 S-3 的单测计划含「重放 chunk 去重、同消息不同 segment 不丢弃」负例。

### F-2 [阻塞] E-11「清除只发生在 relay Ack 或 client shutdown」无源码支撑；风险 5 的「60s 判死→重连」恢复界错误——被弃 stream 可永久占死 host 全局 128 反压容量

- **位置**：§2.2「host↔relay 腿至少一次投递」行；§4 E-11 行；§6 风险 5。
- **证据**：`BoundedOutboundBuffer` 仅有 `new/insert/ack/server_envelopes` 四个方法（`websocket.rs:86-145`，方法枚举亲核）；`buffer_by_stream.remove` 仅存在于 `ack()` 内（`:135-137`）。client 关闭的全部三条路径——显式 `ClientClosed`（`client_tracker.rs:243 → close_client`，`close_client` 全文无 outbound_buffer 引用）、idle sweep（`websocket.rs:1154-1168`）、join-set 清理（`:1140-1152`）——只 invalidate 重组器与 client_message 流，**均不触及 outbound_buffer**。反压阈值是全局 `used` 计数（`:109`/`:131` 跨 stream 增减）对 `CHANNEL_CAPACITY=128`（`websocket.rs:996`）。MacBridge 重连即换新 stream_id（`pairing_persist.go:163-165` 注释亲核）。
- **影响**：熔断器激活（`stream.go:359-373`，激活后本 stream 停发一切 ack）→ host 侧未 ack 信封累积至 128 → writer 全局停止拉新（`:996-1019`）→ MacBridge 60s 静默判死重连 → **被弃 stream 的 ≤128 条未 ack 信封在 Desktop 进程存活期间无任何清除路径** → 单次事件即可让 host writer 对所有 stream 永久停摆、host↔relay 连接因 ping 存活而不触发 host 侧重连 → MacBridge 反复重连全部无效，**整腿死亡直到 Desktop 重启**。这与方案目标要消灭的「驱动长时间不自愈」（ios think.md:477：2.5h 不自愈）同构且更严重，而方案把它界定为「只能等 60s 判死触发重连」（隐含重连可恢复）并把修复推迟到「若 E-10 复现停摆」——E-10 是干净断 relay 10–60s，不会激活熔断器（激活需 transport-error sentinel 风暴，即 2026-09-14 事故形态，macbridge think.md:421），**该触发条件大概率永不点火，已知永久死亡模式将默认留在产品里**。
- **修订方向**（最小充分）：① E-11 该句改为源码准确表述：「清除只发生在 client Ack（`websocket.rs:112-138`）；client 关闭/过期清理不触及 outbound_buffer」；② 风险 5 重写恢复界：明确「重连不可清除 host 侧被弃 stream 缓冲；熔断满缓冲事件可致整腿永久停摆至 Desktop 重启」；③ 对策从「若 E-10 复现停摆才修」改为二选一并有据：(a) 把「熔断器有界时长或熔断期间维持最低 ack 频率」升为主链切片（给出完成断言，如「熔断激活期间每 N 秒至少 ack 最高 seq」），或 (b) 论证为何可接受（需给出源码证据或 owner 裁决记录）。E-10 场景矩阵中可另加一条「sentinel 风暴复现」作为该修复的验收项（实施期、仍受 owner 授权门）。
- **闭合标准**：v1.2 的 E-11 行与官方源码一致（无「client shutdown 清除」表述或给出其真实所指）；风险 5 明确永久停摆模式与重连的无效性；熔断修复的取舍有明确决定（进主链 / 有据推迟），不再以「E-10 复现」作为与致因不匹配的触发条件。

### F-3 [建议] S-2 的触发信号与失败重试接线未指明

- **位置**：§3.2 S-2（「`BindClient` 全部 attach 完成后…对账失败 → 记日志，下一轮 3s `AttachLiveCatalog` 周期（`go-bridge/main.go:1046-1067`）重试，不新增定时器」）；§5 S-2。
- **证据**：`BindClient` 的 attach 是 per-thread goroutine（`session.go:195-211`），现状无完成聚合点；`turnByThread` 在 agent 层 codec 内部（`codec.go:22`），桥侧（Kernel/ReadColdHistory 编排方）现状拿不到「哪些 thread 有在飞 turn」与「rebind 完成」信号；3s 周期的宿主 `AttachLiveCatalog`（`session.go:328`）现状仅做 `thread/loaded/list` 订阅，不含任何对账步骤。
- **影响**：S-2 可实施性无虞（现有 seam 足够：如仿 `CatalogRefreshSignals`（`agent.go:168`）加 rebind/对账信号通道，或把对账挂入 `AttachLiveCatalog` 周期），但「全部 attach 完成后」「下一轮 3s 周期重试」两处按现状读不通，实施者需自行发明接线，容易偏离「不新增定时器/不新增 writer」的约束。
- **修订方向**：S-2 补一段接线说明：完成聚合点（如 WaitGroup/计数）、agent→桥信号 seam（指名现有先例）、失败重试的宿主（挂入 3s 周期的哪一步）。
- **闭合标准**：v1.2 中「attach 完成」「3s 周期重试」各有可指名的宿主与信号路径，无需实施者新造机制。

### F-4 [建议] S-1a「其余 → retryable=true」桶隐含 errProjectionSourceUnavailable；E-5 缺 mid-hydrate 实际落点锚

- **位置**：§3.1 S-1a；§4 E-5（锚仅 `handlers_projection.go:599-606`）。
- **证据**：`prepareProjectionHydrateSource` 对 codex-remote 除 `ErrNotConfigured`/`ErrRestoreInProgress` 外还可返回 `errProjectionSourceUnavailable`（`handlers_projection.go:852`、`:876`：agent 查不到且 `ProjectionTurnCount==0`）。该错误今天因 iOS 白名单缺失而表现为硬错误；S-1b 接上标志消费后将变为**无限自动重试**。mid-hydrate 传输失败的真实落点是 `projection.source_read_failed`（`:1079-1083`，retryable=true），不是 `:599-606` 的 `source_inspection_failed`。
- **影响**：边缘场景（backend 未挂载/会话在本代不存在）下从「硬错误」变为「无限重试」是行为回退；E-5 的「mid-hydrate」命题锚点错位会让实施者把分类改动做错位置。
- **修订方向**：S-1a 显式列出 `errProjectionSourceUnavailable` 的归类（false 或论证 true 的理由）；E-5 锚点补 `handlers_projection.go:1080`。
- **闭合标准**：v1.2 的 S-1a 分类枚举覆盖该 call site 全部错误源；E-5 锚点含 mid-hydrate 落点。

### F-5 [建议] S-4 调度刷新遇 errPairingRevoked 的处置未指明

- **位置**：§3.4 S-4（「失败沿用现有路径（连接死亡→重连时强制 refresh），不新增死亡路径」）。
- **证据**：`refreshControlToken` 在 401/403 时返回 `errPairingRevoked`（`pairing.go:383-384`、`:408-409`）；现有 restore 路径对 revoked 的语义是 invalidate+永久停止（`pairing_persist.go:186-189`、`:287-289`），不是「连接死亡→重连」。
- **影响**：调度刷新撞上 revoked 时，「沿用现有路径」的表述指向不明（等 token 到期连接死亡再重连收敛？还是立即 invalidate？）。两条路最终都收敛，但方案应写明，避免实施者给调度器加出新的 revoked 分叉语义。
- **修订方向**：S-4 补一句：调度刷新返回 `errPairingRevoked` → 立即走 `invalidateRevokedPairing`（与 restoreOnce 语义一致），其余失败沿用连接死亡路径。
- **闭合标准**：v1.2 中 revoked 在 S-4 的失败处理里有唯一明确去向。

### F-6 [建议] 三处锚点精度修正（不影响命题成立）

- **位置/证据/修订**：① §2.2「重连退避基准」行把「cap 30s、cap 后归零」归入 `backoff.rs:12-17`——官方 `backoff.rs` 无 cap（`INITIAL_DELAY_MS=200` `:12`、jitter `:19`），cap/归零在 `websocket.rs:78` 与 `:1342-1351`（方案第一行已另引，仅归属需拆开）；② §3.1 S-1b 引 `ProjectionStore.swift:163-180`，函数实为 `:164-181`；③ §2.2「入站 seq 语义」行补 chunk 共享 seq 限定（随 F-1 一并修）。
- **闭合标准**：v1.2 锚点归属与行号与源码一致。

---

## 5. audit-plan 专项接入状态

按 adapter 执行：方案的外部内容形状声明（Remote Control envelope/seq/segment/cursor/token refresh/TUI 重连/测试不变量）**全部锚定官方源码**，属「描述已实现行为、以读实现验证」类别，无需新样本即可核验；本轮逐锚点读取官方源码核验，结果：除 F-1（chunk 共享 seq_id 导致 S-3 规则与镜像源错位）与 F-2（E-11「client shutdown 清除」子声明无源码支撑）两项外，其余外部形状声明全部成立。方案未提出任何新的外部格式解析（S-1/S-2/S-4/S-5 复用已实现路径；S-3 使用已解析的 `Envelope.SeqID/SegmentID`），故无「描述了但无样本/无锚点的内容类型」遗留。E-9/E-10 为运行证据门，按授权边界本轮未采集，状态如实 pending。

## 6. 门控一致性与覆盖核查记录

- OD-1 pending → 阻塞 S-1a/S-1b 实施：§3.7/§4 Gate B/§5 三处一致 ✓；选项验收独立（标志方案满足验收 1+3；白名单方案自认无法满足验收 3）✓。
- OD-2 decided（think.md:45 亲核）→ S-6 仅桥内暴露、不新增跨仓协议 ✓。
- OD-3 pending 随 E-9；E-9 pending → S-4 整体阻塞（§3.4/§4/§5 三处一致）✓；「未取样前不得写死提前量」约束源 plan:508-509 亲核 ✓；官方 5 分钟先例仅作量级参照、方案明示 ctrl token 与 server token 是不同凭证、E-9 仍须实测 ✓（诚实边界处理正确）。
- E-10 pending（实施期）→ S-1/S-2/S-3 完成验收门 ✓；§5 S-3 行未单列 E-10 但 §4 E-10 行已覆盖，无矛盾。
- 「方案通过 ≠ 实施就绪」：§6 阻塞清单 + 头部授权声明 ✓。
- R→S→验收：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（切片级验证：fake 时钟单测+真机长连接观测；行为级验收未列入 §1，与任务基线 ①–④ 一致，非方案缺陷）、R-4→S-3（验收 4）、R-5→S-6（切片级断言）——对应齐全 ✓。
- 切片依赖序（S-1a→S-1b、S-2→S-3、S-4/S-5/S-6 独立）与失败处理列完整；无数据迁移、无不可逆动作 ✓。
- 风险 1–4 对策与源码相符（风险 3 的 v1.1 降级依据 `websocket.rs:1031-1067` 亲核成立，但须随 F-1 补 chunk 限定）；风险 5 见 F-2。

## 7. 复审处置与剩余门

- 本轮为首轮，无原意见闭合表。
- **仍待满足的实施门（与阻塞意见分开）**：OD-1（owner 确认重试语义，门住 S-1a/S-1b）、OD-3（S-4 提前量 T，随 E-9）、E-9（ctrlExp 真机 fixture ≥5 样本，门住 S-4 整体）、E-10（受控断线复现，S-1/S-2/S-3 完成验收门，实施期采集）。
- 下一轮期望：v1.2 修订 F-1/F-2（必改）与 F-3~F-6（建议），修订版开头声明本轮改动范围；F-1/F-2 闭合后本方案主体（来源、门控、切片结构、其余设计）本轮已核部分不要求重查，按复审纪律以命题/锚点粒度复用。

---

## 评审员交接块

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: 2187f2c8f97a27a8c3dc068f90ec51bf82b7f04f0089981291f9bccea8f3a180
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r1.md
scope: full
blockers: 2
advisories: 4
open_gates: [OD-1, OD-3, E-9, E-10]
round_complete: true
contract_version: plan-contract-v1.1
```
