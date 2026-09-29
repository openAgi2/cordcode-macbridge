# codex-remote 断线韧性与恢复专项方案 评审报告（r9）

- 日期：2026-09-28
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（**v1.8**——r8 REVISION_REQUIRED（F-R8-1）后的修订轮）
- 评审时方案 SHA-256：`d1c1cdbb3dd701d5f813721c9e6385097311185f97a770350d27e07ddd0ed04b`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 381 | shasum -a 256` = `d7c55ca478143725a904af0dc2f2e9ec6112ebdf942220d54973fda045a13c71` 亲算，与方案内设计师交接块自报一致
- 评审范围：**full**（任务简报字段 7；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r8 报告作回归基线；按复审纪律复用 r1~r8 已核未变项，重点执行 r8 §8 裁定的四处定向复核 + v1.7→v1.8 diff 归属 + 门控一致性 + A-R6-1 状态 + r5~r8 承重锚点抽查）
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md + audit-plan 接入约定均已读取；audit-plan 纪律按 E-12b pending 处置复核，见 §7）
- 结论：**REVISION_REQUIRED**（blockers 1，advisories 2）
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未构建、未采集任何真机/wire fixture（E-12b 需 owner 授权，未触碰）。
- 任务简报时效性注记：简报为 r9 启动前亲核时点文本，其中配套 iOS 树记录为 bb3e1802；本轮亲核时该树已前进至 **2ee093cb**（owner 并行的「流式优化二期方案」v5 提交，另有 3 份未跟踪评审报告）——简报已预警该树为移动目标，四锚点文件零改动已按当前实况重新亲证（见 §2.1），不构成身份歧义；以派发 SHA（d1c1cdbb…，与当前文件一致）与报告路径（r9）为准。

---

## 1. 结论

### verdict: REVISION_REQUIRED（不通过，需修订；修订量小、范围收敛于 S-3 的 ack 设计与 E-11/§6 风险 5 的定量表述）

**F-R8-1 本身闭合确认成立**：r8 要求的四处修订（§3.3 判定表 pong 分支与信封级游标、§2.2/§1 R-4/§2.2·E-11 表述限定、§5 S-3 pong 交错负例、E-12b 样本集第⑥项）经本轮逐处对照官方源码亲核**全部在位、语义准确**（锚点表见 §3.1）；v1.8 对 r8 行号小偏差的勘误（`insert` 实际 `:101-110`，r8 引 `:91-99` 实为 `impl` 块起始与 `new()`）**裁定正确**；v1.7→v1.8 增量全部 hunk 可归属、无夹带（方法与结果见 §6）；门控一致性（E-12b 门住 S-3/S-7、验收 4 联动、复审门「不自判通过」）八处一致；A-R6-1 维持「实施前随手补入」路径合规（r6 闭合标准，r7/r8 维持，本轮复核维持）。

但本轮在 F-R8-1 闭合核验的同一对象——§4 E-11 行 v1.8 新增的 pong 子声明——上发现新阻塞 **F-R9-1**：该声明断言「未 ack pong 由后续消息 ack 的 `(seq, MAX)` 游标顺带清除，稳态在途累积 ≤1 条，§6 风险 5『在途窗口 0–2 条』定量仍成立」，此定量**隐含前提是「被 ack 的消息信封持续到达」**（现网由 3s catalog 循环的 `thread/loaded/list` RPC 响应保证——r8 §6.2③ 排除同源推理）；但在「transport 存活、app-server backend 楔死（RPC 无响应）」的可达场景下（方案自引的 2026-09-20 事故 `ios think.md:477` 即此形态：连接在、`thread/list` 每轮 12s 超时、2.5h 不自愈），pong 是唯一入站流量且 **S-3/S-7 设计均不 ack pong**（S-3 ack 子项只覆盖 chunk 信封补 `SegmentID`；S-7 probe 仅在 open/half-open，无 sentinel 则恒 closed）→ 未 ack pong 以每 10s 一条**无界累积**（本轮源码闭环亲证：官方 `insert` 无类型过滤计入全局 used、清除仅 client Ack 一途、close_client/重连重放/idle sweep/join-set 均不触及 buffer）→ ~21.3 分钟填满 128 全局容量 → writer 反压停摆 → 本仓 60s 静默判死重连 → 被弃 stream 的 pong 无任何清除路径 → 全局容量永久占用、后续所有 stream 零入站 → **永久重连循环直到 Desktop 重启**。这正是 §6 风险 5 的停摆模式，但触发**不需要 sentinel 风暴/熔断**——S-7 不覆盖、S-3 设计留空，且楔死 backend 本可在解楔后自愈，pong 累积把可恢复故障升级为不可恢复停摆（与方案「断线韧性」主旨直接冲突）。定性：**设计缺口为旧问题漏检**（v1.2 E-11/S-3 起潜伏，与 F-R8-1 同 lineage）；**E-11 行的无前提定量声明为 v1.8 新引入文本**（本轮评审对象）。修订量小：S-3 ack 子项扩展到 pong 信封（v1.8 游标已对 pong 推进，ack 输入现成）+ E-11/风险 5 定量声明补前提或随修复改为无条件成立 + §5 S-3 补 pong ack 断言。

**对 r8 处置的联动**：F-R8-1 的特定缺陷（游标不对 pong 推进 → 每 10s 假缺口）**判闭合**；但 S-3/S-7 的通过依据因 F-R9-1（同一子系统内相邻新缺陷）**继续暂停**（与 r8 对 F-R4-2/F-R8-1 的处置同型）；S-1a/S-1b/S-2/S-4/S-5/S-6 的通过依据不受影响（F-R9-1 不触及对账事务、错误分类、token 调度、常量对齐、状态暴露各设计，本轮独立核验均成立）。r8 §6.2③ 对本问题的排除（「空闲累积上限 ~1 条，不构成问题」）经本轮新证据（`protocol.rs:116-119` ack 官方语义=清除 ≤seq 的**一切** server envelopes、`PongStatus` 仅反映注册状态而非 backend 健康度、方案自引事故形态）**被推翻其一般性**——排除仅在「RPC 响应持续到达」的健态成立。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**；评审通过不构成实施授权。
- 实施前置门（与意见分开列，不变）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期）、**E-12**（E-12b wire 样本含 v1.8 补入的第⑥项 pong 帧，owner 授权后按 §4 捕获清单采集，门住 S-3/S-7 整体实施）。
- 复审门：F-R9-1 修订 + 定向复审前，S-3/S-7 维持阻塞未闭合状态；S-2 维持闭合（其设计不依赖 seq 游标/ack 形状规则）。
- 待办（非门、非阻塞）：A-R6-1（§3.2 Abort 规格补 `finishHydrateLocked` 关闭句）维持「实施前随手补入」路径（r6 闭合标准，r7/r8 维持，本轮 grep 亲证 §3.2 Abort 条仍无该句、仅存于登记处——未夹带，合规）。
- E-10 真机验证、E-12b wire 捕获与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核，读取源码前重新生成；两仓各 3 树全量枚举 + 官方 checkout，每行完整 40 字符提交哈希）

| 仓库 | 工作树路径 | 分支 | 完整提交 | 未提交状态（`git status --porcelain` 亲跑） | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 73539b71c0c3552a3c16548d05417813ba907869 | 仅方案文档 1 处未提交修改（v1.8，评审循环正常形态）+ 未跟踪评审报告 r5/r6/r6-meta/r7/r7-meta/r8 六份（只读；本报告为第 7 份未跟踪产物） | 简报①指定树；`git diff --name-only 07721783..HEAD` 亲跑 9 文件**全部为 docs/*.md**——代码零改动，Mac 锚点与 07721783（r1~r8 核验基线）等价 | 纯设计文档评审，无构建/安装——N/A（无构建场景） |
| cordcode-macbridge（main 树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 73539b71c0c3552a3c16548d05417813ba907869 | 干净（亲跑为空） | 枚举记录（P0：含配套工作树，无论是否实际引用）；与方案所在树同提交 | N/A |
| cordcode-macbridge（第三树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b2523526b6af7990c9688fd29b7e285d7ce78c | 干净（亲跑为空） | 枚举记录 | N/A |
| cordcode-ios（iOS 锚点核验来源，简报③指定） | /Users/jacklee/Projects/cordcode-ios | main | ccb5a0df647865324e11aedebbb089b9fc07bbb5 | 干净（亲跑为空；与 r6/r7/r8 核验来源同提交，未漂移） | 简报③指定 main 工作树 | N/A（纯设计） |
| cordcode-ios（同族配套树，P0 要求记录） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | **2ee093cb492a4c34e52a21aed2186e37aad7cb9e**（简报记录时点为 bb3e1802，本轮亲核已前进 2 个 docs 提交——owner 并行「流式优化二期方案」v4/v5，移动目标如实记录） | 3 个未跟踪 docs 文件（2026-09-27-native-timeline-streaming-phase2 评审报告 r2/r3/r4，与本方案无关，只读） | 非本任务锚点来源。**锚点有效性（本轮亲跑）**：`git diff --stat ccb5a0df..2ee093cb -- <ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport 四文件>` 为**空**——iOS 锚点结论与工作树选择无关 | N/A |
| cordcode-ios（第三树，枚举项） | /Users/jacklee/Projects/cordcode-ios-plan-approval | detached | a336b68bb37765d2ea23c14f6508619378c839ae | 干净（亲跑为空）；`git merge-base --is-ancestor` r6/r7/r8 已证为 iOS main 祖先，本方案无任何锚点取自该树 | 枚举记录 | N/A |
| openai/codex（官方，只读） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD 亲核） | e72da2b53805894878023d01949a25a082e0a5cb | 干净（亲跑为空） | 简报④：一律以 FETCH_HEAD 为准（亲核一致，与方案 §2.1 记录相同） | N/A |

**评审对象身份**：派发哈希与读取哈希一致（报告头）。HEAD 73539b71 内方案为 v1.4 §3.2 时点半成品快照（方案 §2.1 自记，r8 亦核）；v1.5/v1.6/v1.7 字节快照均不可从 git 复原（历史形态，r6/r7/r8 同此声明）——v1.7→v1.8 增量以「r8 报告引用的 v1.7 原文 + v1.8 头部声明范围 + HEAD→工作树全 hunk 归属」三重交叉印证法核验（方法与结果见 §6）。

### 2.2 覆盖声明

- **本轮 fresh 亲核**（约 40 组锚点，三仓 15 文件）：F-R8-1 四处修订的完整官方链（`client_tracker.rs:222-242` Ping arm 双路径/:273-287 run_client_outbound/`websocket.rs:410/:412/:443/:996/:999-1027/:1031-1038/:1046/:1062/:1066-1067`/`insert :101-110` 含 `:109` used+1/ack `:112-138` 含 `:123/:129/:131/:136`）；E-11 无清除路径三锚（`client_tracker.rs:312-324` close_client 无 buffer 引用、`websocket.rs:965-988` 重连重放不清、`:1140-1152` join-set 与 `:1154-1168` idle sweep 仅 invalidate 重组器）；F-R9-1 新证据链（`protocol.rs:114-119`、`client_tracker.rs:179/:225/:233` PongStatus 仅注册语义、`transport/mod.rs:24` CHANNEL_CAPACITY=128、`segment.rs:19-22/:327-329/:445-468` 含 `:467 seq_id: envelope.seq_id`）；MacBridge 侧（`ws.go:316-319/:324-344`、`stream.go:304-343` 四分支含 `:305-306/:307-316/:324/:339`、`:347-357` deliver→inbound、`:390-407` ack 构造无 SegmentID、`:468` Transport 断言、`main.go:1046-1066` 3s catalog 循环、`session.go:328-341` AttachLiveCatalog→thread/loaded/list RPC、`rpc.go:58-61` RequestContext 包装）；r5~r8 承重锚点抽查（`projection_kernel.go:1319/:1335/:1352-1357/:1457-1458/:682-688` F-R5-1 链、`:1020-1026/:969-976/:1069-1083` F-R4-1 admission 链）；iOS（`ProjectionStore.swift:164-181` 白名单 11 项无 hydrate_failed）；think.md 双仓（macbridge `:2-6`/`:45`/`:415-418`、ios `:477`）；官方开源仓无信封层 ping/ack 参考实现的 grep 亲证（`ClientEvent::Ack` 构造仅 tests.rs:1650、`ClientEvent::Ping` 仅 handler+tests）；交接块正文哈希复算；v1.8 标记全文定位（23 处）；HEAD→工作树 12 hunk 全归属。
- **复用（按命题/锚点粒度；复用合法性依赖的三项源码身份等价本轮全部亲自重跑证实：Mac 代码 07721783≡HEAD、iOS 四锚点文件跨候选零改动（含最新 2ee093cb）、官方 checkout e72da2b5 未动）**：r5/r6/r7/r8 已亲核且 v1.8 未改动的锚点——§2.2 复用表其余各行、§2.3 现状摘要其余句、§3.1/§3.4/§3.5/§3.6、E-1~E-8/E-10 各行、E-11 行 v1.8 前的既有子声明、`backoff.go`/`pairing_persist.go`/`pairing.go`/`agent.go`/`codec.go`/`session.go:161-212`/`history_paginated.go`/`handlers_projection.go` 各段、官方 `remote.rs`/`reconnect.rs`/`enroll.rs`/`server_api.rs`/`backoff.rs`/tests 引用、`websocket.rs:74-79/:78-79/:201-207/:1275-1330/:1342-1351`。r8 §3.1/§3.2 锚点表作为复用依据；其指出的 r8 行号偏差（insert :91-99）即 v1.8 §7.1 勘误对象，本轮已亲自重核修正后值（:101-110）。
- **未核项（如实声明）**：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码；pong 携带 seq_id 到达 controller 的 wire 级确认归入 E-12b pending 门（样本集⑥已在位）；② E-9/E-10/E-12b 为 pending 运行证据，按授权边界未采集，状态如实；③ v1.5/v1.6/v1.7 字节快照不可复算（历史形态，见 §6 方法说明）；④ 本轮未重做 r5~r8 级的全部锚点逐项重查（复用范围见上；未重查项经 §6 增量核验确认 v1.8 零触碰）；⑤ F-R9-1 的「楔死 backend」场景可达性以方案自引事故（ios think.md:477）形态一致性为证据，未做活体复现（授权边界内不可得，如实声明）。

---

## 3. 锚点核验表（本轮承重项摘录；「✓✓」=定位与语义均成立）

### 3.1 F-R8-1 闭合链（r9 重点①——四处修订的官方/MacBridge 语义前提，本轮全部亲核）

| 方案原引用（v1.8） | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| pong 两条路径 `client_tracker.rs:221-237`/`:273-287` | Ping arm 实际 `:222-242`（已知客户端 `:223-227`、未知客户端 spawn `:229-240`、通道 send `:239`）；run_client_outbound `:247-293`（Pong 构造 `:273`、send `:281-286`） | `:225 let _ = client.status_tx.send(PongStatus::Active);`；`:239 let _ = server_event_tx.send(server_envelope).await;`；`:273 let event = ServerEvent::Pong { status: status_rx.borrow().clone() };` `:281-286 server_event_tx.send(QueuedServerEnvelope{event, client_id, stream_id, …})` | ✓✓ 语义成立（两条 pong 路径均入 server_event 通道）；定位注记：引用区间 `:221-237` 未覆盖未知客户端路径的 send 行（实际 `:239`）——承重行为锚点区间截断 2 行，登记为 A-R9-3（建议级），不影响命题 |
| 单一 server_event 通道 `websocket.rs:410` | 同 | `:410 let (server_event_tx, server_event_rx) = mpsc::channel(super::CHANNEL_CAPACITY);`（`:412` tx 入 ClientTracker、`:443` rx 存入 state） | ✓✓ |
| writer loop 无条件 seq 分配 `:1031-1038`、逐信封 insert `:1046-1062`、计数器递增 | 同（insert 调用行 `:1062`） | `:1035-1038 let seq_id = *state.next_seq_id_by_stream.entry(seq_key.clone()).or_insert(1);`；`:1062 state.outbound_buffer.insert(&server_envelope);`（对 split 后每个信封）；`:1066-1067 next_seq_id_by_stream.insert(seq_key, seq_id.saturating_add(1))` | ✓✓（seq 按 (client,stream) 从 1 起无条件分配给通道内一切信封单元，pong 不豁免） |
| `insert :101-110` 无类型过滤（v1.8 对 r8 `:91-99` 的勘误） | 同（r8 引 `:91-99` 实为 `impl` 块起始 + `new()` `:92-99`） | `:101 fn insert(&mut self, server_envelope: &ServerEnvelope) {` … `:108 .push_back(server_envelope.clone());` `:109 self.used_tx.send_modify(\|used\| *used += 1);` | ✓✓ **v1.8 勘误正确**：insert 函数体 `:101-110`，无事件类型过滤，pong 计入全局 used |
| 全局反压 `websocket.rs:996`、ping 分支不受门限 | 同 | `:996 let outbound_has_capacity = *used_rx.borrow() < super::CHANNEL_CAPACITY;`；`:999-1008 ping_interval.tick() → websocket_writer.send(Ping)`（不检查容量）；`:1010-1019 used_rx.changed(), if !outbound_has_capacity`（满则等待）；`:1020-1027 server_event_rx.recv(), if outbound_has_capacity` | ✓✓（§6 风险 5 的反压机制描述准确） |
| ack 游标语义 `websocket.rs:112-138`、`:123` | 同 | `:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));` `:129 let is_acked = envelope_cursor <= acked_cursor;` `:131 *used -= 1` `:136 buffer_by_stream.remove(&key)`（仅 ack 内） | ✓✓（后续消息 ack 的 `(seq, MAX)` 确实顺带清除 ≤seq 的一切信封——含 pong；亦为 F-R9-1 修复方向的语义依据） |
| 本仓 ping 10s `ws.go:317`、pong 分支无 seq/ack `stream.go:307-314`、ack 仅 `:324/:339` | 同（pong 分支 `:307-316`） | `:317 pingInterval = 10 * time.Second`；`:307-316 case typePong: if env.Status != "active" {fail} … markHostActivity; continue`（无 seq/ack）；`:324 s.ack(env)`（typeServerMessage）、`:339 s.ack(env)`（chunk）；`:305-306 case typeAck: continue` | ✓✓（假缺口推演前提成立；**同时证实 pong 无 ack 处理——F-R9-1 的本仓侧前提**） |
| E-12a：ack 构造无 SegmentID `stream.go:390-407`、字段已备 `envelope.go:45` | 同 | `:400-406 Envelope{Type: typeAck, ClientID, EnvID, StreamID, SeqID: env.SeqID}`（无 SegmentID 字段） | ✓✓（维持 r2~r8 结论） |

**F-R8-1 四处修订文本逐处对照（r9 重点①收口）**：① §3.3（:182-198）：「信封级高水位游标」+ 判定表 plain/chunk/pong 三类可判定（pong 专条 :189：`seq_id > lastSeq` → 推进置 lastSeg 空、`<= lastSeq` → 重复）+ 缺口语义「任意信封丢失——可能只是一条无害的 pong…维持 fail-visible」（:192）+ 尾句依据「消息与 pong 共用计数器」（:198）——与官方源码逐环吻合，判定表内部一致性推演成立（「理论不可达」混合帧型分支的排除依据「每 seq 唯一归属一个事件单元、pong 不拆分」经 `:1035-1038`+`:1066-1067`+`segment.rs:327-329` 亲证；重放 pong 落入 `<= lastSeq` 重复分支与 host 重连重放携带原 seq 的 E-11 事实一致）✓；② §1 R-4 括注（:32）/§2.2 两行（:107-108）/§2.3 术语同步（:132）✓；③ §4 E-11 pong 子声明（:263）+ E-12b 样本集第⑥项（:264，明确「该 wire 声明自此成为 S-3 缺口规则的前置事实」纳入 pending 门）✓；④ §5 S-3 pong 交错负例（:277「消息 seq N → pong seq N+1 → 消息 seq N+2 → 不触发缺口」+「正常流零告警（含 pong 稳态零假缺口）」）+ S-7 行/§3.7 依赖段注记（:237/:281，probe 游标输入随修复自动一致）✓。**F-R8-1 判闭合**。

### 3.2 F-R9-1 新证据链（本轮独立风险清单产出，全部亲核）

| 命题 | 实际位置 | 源码摘录（带行号） | 结果 |
| --- | --- | --- | --- |
| 官方协议预期 ack 清除「一切」server envelopes ≤seq（含 pong） | `protocol.rs:114-119` | `:116-118 /// Backend-generated acknowledgement for all server envelopes addressed to client_id and stream_id whose envelope seq_id is less than or equal to this ack's seq_id.` | ✓✓（pong 是 server envelope；不 ack 即永久滞留 buffer——本仓 ping 每 10s 为本仓自加策略，官方开源仓无信封层 ping/ack 参考实现，grep 亲证 `ClientEvent::Ack` 构造仅 tests.rs:1650） |
| pong status 仅反映客户端注册，不反映 backend 健康度 | `client_tracker.rs:179/:225/:233` | `:179 watch::channel(PongStatus::Active)`（注册即 Active）；`:225 status_tx.send(PongStatus::Active)`（已知客户端 Ping）；`:233 PongStatus::Unknown`（未注册客户端） | ✓✓（楔死 backend——连接在、RPC 超时——时客户端保持注册、pong 恒 Active，本仓 `stream.go:311` 的 status!=active 判死不触发，pong 持续流动） |
| 被 ack 消息信封的持续到达依赖 catalog 循环 RPC 响应 | `main.go:1046-1066`/`session.go:328-341`/`rpc.go:58-61`/`stream.go:317-324`/`:468` | `:1056 time.NewTicker(3 * time.Second)`→`AttachLiveCatalog`→`:335 cl.RequestContext(ctx, "thread/loaded/list", …)`；响应经 `typeServerMessage` 分支 `:324 s.ack(env)`；`var _ Transport = (*Stream)(nil)` | ✓✓（健态下 ack 每 ~3s 一条、pong 累积 ≤1——r8 排除的成立域；RPC 无响应（楔死）则 ack 停止、累积无界） |
| 无清除路径（被弃 stream 的 pong 永久占用全局容量） | `client_tracker.rs:312-324`/`websocket.rs:965-988/:1140-1152/:1154-1168` | close_client 仅 remove_client+cancel token+ConnectionClosed（无 buffer 引用）；重连重放 `:965-971` 仅 clone+重发（不清）；join-set/idle sweep 仅 invalidate 重组器/消息流 | ✓✓（E-11 既有子声明，本轮重核维持；128 容量 `transport/mod.rs:24` 亲证） |
| 楔死场景可达性（形态一致性证据） | `cordcode-ios/think.md:477` | 「Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时、stream idle/closed、pairing 流反复重连），至 14:00 未恢复」 | ✓（方案自引的 2.5h 不自愈事故与「~21 分钟累积 → 反压 → 静默判死 → 重连不可愈」签名形态一致；活体复现本轮授权边界内不可得，如实声明为形态证据而非因果证明） |

### 3.3 r5~r8 承重锚点抽查（复用有效性确认，本轮亲核）

| 命题 | 核验 | 结果 |
| --- | --- | --- |
| F-R5-1：commit 先取 liveSnap、按 tx 标志二选一 merge、Restore 前置；merge 第一分支回撤终态；tx 标志先例 | 亲读 `projection_kernel.go:1319`（CommitHydrateTransaction）、`:1335`（liveSnap）、`:1352-1356`（unionLiveTurns else mergeHydrateBaselineWithLiveExecution）、`:1357`（Restore）、`:1457-1458`（executionInFlight(live) && !executionInFlight(cold) → cold.Execution = live.Execution）、`:682-688`（liveOnlyAdmission/unionLiveTurns） | ✓✓（v1.5 reconcile 标志设计依据维持） |
| F-R4-1：READY pathless 两分支均不可行 | 亲读 `:1020-1026`（pathless+sourceChanged → break，否则 AlreadyReady）、`:969-976`（pathlessRichHistoryBackend 名单无 codex-remote）、`:1069-1083`（唯一 restore 分支要求 `!sourceChanged`） | ✓✓（专用 reconcile 事务的必要性维持） |
| chunk 共享 seq / 拆分阈值 / 重组上限 / 二选一 | 亲读 `segment.rs:20`（150KB）、`:21`（100MB）、`:327-329`（≤MAX 整体返回）、`:467`（`seq_id: envelope.seq_id`） | ✓✓ |
| iOS 白名单不含 hydrate_failed | 亲读 `ProjectionStore.swift:164-181`（11 case，无 projection.hydrate_failed） | ✓✓（E-3/E-5 维持） |
| think.md 复盘锚点 | 亲读 macbridge `:2-6`（26.924 升级断裂）/`:45`（backend_status_changed 待立项另立——OD-2 decided 依据）/`:415-418`（2026-09-14 风暴）；ios `:477` | ✓✓ |

---

## 4. 意见（F-ID）

### F-R9-1 [阻塞] S-3/S-7 设计不 ack pong + E-11 v1.8 新增的「稳态在途累积 ≤1 条」为无前提定量声明——楔死 backend 场景下未 ack pong 无界累积，无熔断也触发 §6 风险 5 的永久停摆

- **位置**：§4 E-11 行 v1.8 括注（「未 ack pong 由后续消息 ack 的 `(seq, MAX)` 游标顺带清除，稳态在途累积 ≤1 条，§6 风险 5『在途窗口 0–2 条』定量仍成立」）；§3.3 ack 子项（仅 chunk 补 `SegmentID`）；§3.7 依赖段 v1.8 注（「probe ack…顺带清除 host 缓冲内未 ack 的 pong」——仅在 armed 态成立）；§6 风险 5 残余 ②（「单次量为在途窗口（通常 0–2 条）」）。
- **证据**（本轮全部亲核，官方 @ e72da2b5 + 本仓 @ 73539b71 工作树）：
  1. **pong 计入 buffer 且清除仅 Ack 一途**：`insert :101-110` 无类型过滤、`:109` 全局 used+1；ack `:123/:129` 按 `(seq, seg.unwrap_or(MAX))` 清除；close_client `:312-324`、重连重放 `:965-988`、join-set `:1140-1152`、idle sweep `:1154-1168` 均不触及 buffer（§3.1/§3.2 表）。官方协议文档明示 ack 语义覆盖「all server envelopes…seq_id less than or equal」（`protocol.rs:116-118`）——pong 是 server envelope，不 ack 即滞留。
  2. **本仓 pong 无 ack 且 ping 为本仓自加**：`stream.go:307-316` pong 分支无 seq/ack 处理；`s.ack(env)` 仅 `:324/:339` 消息分支；`ws.go:317` pingInterval 10s。官方开源仓无信封层 ping/ack 参考实现（`ClientEvent::Ack` 构造仅 tests.rs:1650、`ClientEvent::Ping` 仅 handler+tests，grep 亲证）——累积后果由本仓 controller 设计承担。
  3. **「≤1 条」的隐含前提是消息 ack 持续到达**：健态由 3s catalog 循环保证（`main.go:1046-1066` → `session.go:335` `thread/loaded/list` RPC → 响应经 `typeServerMessage` `:324` ack，本轮亲证该链路）。但 **RPC 响应不是 transport 层产物**——app-server backend 楔死（连接在、请求超时）时：pong 恒 Active 持续流动（`client_tracker.rs:179/:225`——status 仅注册语义，本仓 `stream.go:311` 判死不触发）、无任何消息信封、无任何 ack → pong 每 10s 一条无界累积 → 128 条（`transport/mod.rs:24`）约 21.3 分钟 → `websocket.rs:996` 全局反压、writer 停拉（`:1010-1019`）→ pong 停达 → 本仓 60s 静默判死（`ws.go:318`）重连 → 被弃 stream 的 pong 无清除路径 → 全局容量永久占用 → 后续所有 stream 零入站、永久重连循环直到 Desktop 重启。**该场景可达性有方案自引事故的形态一致性证据**（`ios think.md:477`：`thread/list` 每轮 12s 超时 + stream idle/closed + 反复重连 + 2.5h 不自愈——楔死签名与「~21 分钟累积后停摆」时间线相容）。
  4. **S-3/S-7 设计均不覆盖**：S-3 的 ack 子项只写「chunk 信封补 SegmentID」（§3.3），无 pong ack 规则——实施者按方案字面保持 `:324/:339` 两处调用点，pong 永不被 ack；S-7 的 probe ack 仅在 open/half-open（需 sentinel 激活熔断），无 sentinel 则恒 closed、零 ack（§3.7 状态机）。r8 §6.2③ 曾推演并排除「空闲流 pong 无界累积」，其排除依据（「3s catalog 循环保证消息帧 ≤3s 到达」）**未考虑 RPC 响应停止而 transport 存活的楔死场景**——本轮新证据（`protocol.rs:116-118` ack 覆盖一切信封 + `PongStatus` 仅注册语义 + 事故形态）推翻该排除的一般性：排除仅在健态成立。
- **影响**：① §6 风险 5 的永久停摆模式获得第二个触发路径（无 sentinel、无熔断参与），且把「backend 楔死、解楔后可自愈」的**可恢复故障**升级为「全局容量永久占用、**重连不可愈**」的不可恢复停摆（与方案主旨「断线韧性」直接冲突）；② E-11 行（verified 门）内含无前提的定量子声明，v1.8 新引入文本与官方源码行为在可达场景冲突——与本链 r1 F-1/r8 F-R8-1 同型（设计文本与官方行为冲突必须在方案层修订，不能留给实施者发现）；③ 风险 5 残余 ② 的「0–2 条」定量在楔死场景可达 128 条，停摆概率评估失真；④ 楔死场景下 S-3 缺口检测亦失效（pong 停达前无消息帧、无缺口可检；停达后 stream 已死）——R-4 的「不静默丢失」在该场景无任何补偿。影响范围：S-3/S-7（含 E-11/风险 5 表述）；不触及 S-1a/S-1b/S-2/S-4/S-5/S-6。
- **修订方向**（最小充分）：① §3.3 ack 子项扩展到 pong 信封——v1.8 游标已对 pong 推进，ack 输入现成：pong 到达即以 `(seq, MAX)` ack（构造同 `:390-407`，SegmentID 缺省按官方 `:123` 语义恰好清除该 pong 及更早一切已收信封；流内有序故语义安全，与既有 plain 消息 ack 同型），累积上界无条件 ≤1，不依赖 catalog 循环健康；② E-11 行 v1.8 括注与 §6 风险 5 残余 ② 的定量声明随修复改为无条件成立（或如作者有证据证明楔死场景不可达，则撤回「≤1 条/0–2 条」无条件表述、如实登记该场景为 MacBridge 侧可修而未修的残余——需给出可达性反证）；③ §5 S-3 断言补「pong 到达 → ack 携带该 seq」负例；E-12b 样本集⑥已在位（pong 帧 wire 形状随门确认），无需新增样本项。
- **闭合标准**：v1.9 的 §3.3 对 pong 信封有明确 ack 规则（或登记残余并撤回无条件定量、附可达性反证）；E-11 行/风险 5 残余 ② 的定量与设计一致；§5 S-3 断言覆盖 pong ack。修订后定向复核 §3.3 ack 子项/§4 E-11 行/§6 风险 5 三处即可，不需全量重审。

### A-R9-2 [建议] v1.8 处置登记遗漏 §2.3 术语同步位置

- **位置**：§7.1 处置表「修订位置」列与文档头 v1.8 声明的联动位置清单。
- **证据**：正文 `:132`（§2.3）带「v1.8 术语同步，F-R8-1」标记（「该丢失不产生**信封级** seq 缺口」），内容可归属声明的「消息级→信封级」清扫；但 §7.1「修订位置」列（§3.3/§1 R-4/§2.2 两行/§4 E-11·E-12/§5 S-3·S-7/§3.7/§4 复审门·§6）与文档头声明均未列入 §2.3（本轮 grep 全文 v1.8 标记 23 处逐一归属时发现）。
- **影响**：纯登记完整性——v1.8 diff 可审计性轻微受损，无设计/语义影响（改动本身在位且自声明）。
- **修订方向/闭合标准**：v1.9 在 §7.1 修订位置列（或文档头声明）补记 §2.3；一轮内补记即可。

### A-R9-3 [建议] client_tracker.rs pong 路径引用区间未覆盖未知客户端路径的通道 send 行

- **位置**：§2.2「入站 seq 语义」行、§4 E-11 行、文档头、§7.1 多处引用 `client_tracker.rs:221-237`。
- **证据**：Ping arm 实际 `:222-242`；「未知客户端直接 spawn 发 Pong 入通道」的承重行为 send 在 `:239`（`server_event_tx.send(server_envelope).await`），落在引用区间外 2 行；`:221` 为 `ClientMessageChunk|Ack` 分支行而非 Ping arm 起点。语义本轮全文亲读成立（两条路径均入单一通道，§3.1 表）；已知客户端路径的 send 由 `:273-287` 覆盖。r8 原引 `:221-227/:229-236` 同源截断，v1.8 沿用。
- **影响**：锚点精度（与本链 r6-meta 勘误-4、F-R5-A2 同类）；「均入单一 server_event 通道」命题的锚点自足性轻微受损（依赖 `:229` 的 `server_event_tx.clone()` 间接支撑）。
- **修订方向/闭合标准**：v1.9 顺手把 `:221-237` 改为 `:222-242`（或 `:223-240`）使 send 行入区间；一轮内修正即可。

---

## 5. 复审处置与回归核查

### 5.1 r8 意见处置裁决

| 意见 | 本轮裁决 | 依据 |
| --- | --- | --- |
| F-R8-1（阻塞） | **闭合** | 四处修订逐处对照官方源码亲核全部在位、语义准确（§3.1 表）：判定表对 pong（及一切带 seq 信封）可判定、全文无「消息级」误导残留（§1/§2.2/§2.3/§4 同步）、§5 S-3 含 pong 交错负例、E-12b 清单含第⑥项；v1.8 对 r8 insert 行号偏差的勘误（:91-99→:101-110）裁定正确。**但 S-3/S-7 通过依据不因此恢复**——同对象新阻塞 F-R9-1（§4） |
| r8 §6.2③「空闲流 pong 累积」排除 | **被推翻一般性（新证据）** | 排除依据「3s catalog 循环保证消息帧 ≤3s 到达」隐含 RPC 响应持续到达前提；楔死 backend 场景前提不成立（§4 F-R9-1 证据 3/4）。排除在健态仍成立，非 r8 核验错误，如实记录 |
| r8 对 r7 APPROVED 的暂停处置 | 维持 | S-3/S-7 通过依据继续暂停（现因 F-R9-1）；S-1a/S-1b/S-2/S-4/S-5/S-6 通过依据维持（本轮独立核验 §3.2/§3.3 抽查 + F-R9-1 不触及各该设计） |

### 5.2 历轮意见处置裁决（回归清单）

| 意见 | 本轮裁决 | 依据 |
| --- | --- | --- |
| r1 F-1~F-6 / r2 R2-A1~A4 / r2-meta / r3 勘误 / r4 F-R4-1~4 + R4-A1/A2 / r5 F-R5-1 + F-R5-A1~A3 | 维持闭合 | 承重锚点本轮抽查吻合（§3.3 表：F-R5-1 链/F-R4-1 admission 链/segment/protocol/iOS 白名单）；对应文本不在 v1.8 改动范围（§6 增量核验零触碰） |
| r6 A-R6-1（建议） | **维持开放（实施前随手补入），处置合规** | v1.8 未夹带（grep 亲证 `finishHydrateLocked` 在 §3.2 Abort 条仍无该句，仅存于 §8.2 未夹带说明/§4/§6 登记处三处）；r6 闭合标准（不构成实施前置）继续有效，r7/r8 维持 |
| r6-meta 两项 / r7-meta 问题（勘误-5） | 维持闭合 | §2.1 六树记录、§8.1/§8.2 勘误记录在位；勘误-5 事实声明 r8 已亲跑复现，本轮复核方案侧记录一致（40 字符正确值在 §2.1 两处亲读） |
| 稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3） | 不变，亲核属实 | 全文精读 + v1.8 标记定位无 ID 变更 |

---

## 6. v1.7→v1.8 增量核验（修订轮纪律）

**方法**：v1.7 字节快照不可从 git 复原（HEAD 73539b71 内为 v1.4 §3.2 时点半成品），以三重交叉印证：① `git diff HEAD -- <plan>` 全部 12 hunk 逐 hunk 归属分类（HEAD→v1.8 粒度）；② v1.8/F-R8-1 标记全文定位（23 处）逐一映射到文档头声明的修订位置；③ r8 报告引用的 v1.7 原文（「消息级高水位游标」「消息级缺口」「消息级严格单调」、八断言、A-R6-1 无句、E-12b 五项）与当前文本逐点比对。

**结果**：
- **12 hunk 全部可归属**：hunk 1（文档头 v1.5~v1.8 声明+状态行）、hunk 2（§1 R-4 v1.8 括注 + think.md 双框架 v1.4）、hunk 3（§2.1 v1.4/v1.6 亲核记录）、hunk 4（§2.2 信封级+pong v1.8 + R4-A1 注记 v1.4）、hunk 5（§2.3 v1.8 术语同步）、hunk 6（§3.1 :161-173 v1.4）、hunk 7（§3.2 v1.5 F-R5-1 重写 + v1.6 勘误-4 锚点）、hunk 8（§3.2 事务域 v1.5 + §3.3 v1.4/v1.8）、hunk 9（§3.7 v1.4 状态机 + v1.5 帧型/八断言 + OD-1 v1.4）、hunk 10（§3.8/§4/§5/§6 v1.4/v1.5/v1.8）、hunk 11（§7.1 v1.8 处置表 + §7.2 v1.5 + §7.3 v1.4 + §8.1 v1.7 + §8.2 v1.6 + §8.3 重编号）、hunk 12（交接块 v1.8）。**无不可归属 hunk、无夹带**；v1.8 专属改动全部为 pong/信封级主题。
- **r8 所验 v1.7 承重内容零回归**：§3.2 F-R5-1 链、§3.7 八断言与 probe 帧型、A-R6-1 未夹带、E-12b 捕获清单结构——除声明的 F-R8-1 清扫（消息级→信封级 + pong 限定）外逐点一致。
- **交接块**：正文哈希 `head -n 381 | shasum` 亲算 = `d7c55ca4…` 与自报一致；open_gates [OD-1, OD-3, E-9, E-10, E-12] 与 §6 阻塞清单一致；review_round 链（…→r8→v1.8→r9 定向复审）与实况一致。
- **登记遗漏**：§2.3 术语同步未列入 §7.1 修订位置列/文档头联动清单（A-R9-2，内容本身可归属、无夹带）。

---

## 7. 门控一致性、audit-plan 专项与交接块

- **E-12 pending → 门住 S-3/S-7 整体实施**：§3.3 尾句（:198）、§4 E-12 行（:264 阻塞列「S-3 整体实施；S-7 整体实施」）+ Gate A 段（:268）、§5 S-3/S-7 依赖列（:277/:281）+ 依赖序段（:283）、§6 验证分层（:288）+ 阻塞清单（:299）+ 下一阶段入口（:301「S-3/S-7 待 E-12 解锁」）、交接块 open_gates——**八处一致**（本轮全文精读逐处核对）✓；S-2 不依赖 E-12 ✓。E-12b 样本集第⑥项（pong 帧）在位并明示为 S-3 缺口规则前置 wire 声明 ✓。
- **验收 4 与 pong 规则联动**：§1 验收 4（:48）↔ §3.3 信封级游标（:182「系统性破坏验收 4」推演 + :192 fail-visible）↔ §5 S-3「正常流零告警（含 pong 稳态零假缺口）」——修复后「无缺口时零开销」恢复成立 ✓。**F-R9-1 不破坏该联动**（假缺口问题已修），其影响在 ack 侧与风险 5 定量（§4）。
- **OD-1 pending → 门住 S-1a/S-1b**（§3.8/§4 Gate B/§5 三处）；OD-2 decided（think.md:45 亲核）→ S-6 仅桥内；OD-3 随 E-9；E-9 → S-4 整体；E-10 → S-1/S-2/S-3 完成验收 + S-7 可见性——各处一致 ✓。
- **复审门状态**：§4（:268）/§5 依赖序（:283）/§6（:299/:301）均如实写「S-3/S-7 待 r9 定向复核、本方案不自判通过」——与评审循环实况一致，非超前自判 ✓。本轮结论：F-R8-1 闭合、F-R9-1 新开，S-3/S-7 维持待复审。
- **audit-plan 专项**：E-12a（源码事实）锚点本轮亲证；E-12b 如实 pending（「同一门有未完成子项，整门不得标 verified」在位）；捕获清单（版本锚定/最小样本集六项含 pong/双独立提取策略/脱敏/计划路径「尚不存在」/授权边界）完整。**F-R9-1 的 pong-ack 修复不新增样本项**（pong 帧 wire 形状已由⑥覆盖；ack 回程形状由③覆盖）。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（E-9 门内）、R-4→S-3+E-12（验收 4——F-R8-1 修复后该链恢复；F-R9-1 影响的是该链之外的 ack/风险 5 面）、R-5→S-6——对应齐全 ✓。

---

## 8. 剩余门与下一阶段

- **本轮意见**：F-R9-1（阻塞，S-3 pong ack 设计缺口 + E-11/风险 5 无前提定量）——修订量小（§3.3 ack 子项一条规则 + E-11/风险 5 定量修正 + §5 S-3 一个断言），修订后**只需定向复核 §3.3 ack 子项/§4 E-11 行/§6 风险 5 三处**，不需全量重审。A-R9-2/A-R9-3（建议，登记与锚点精度，随 v1.9 顺手）。
- **复审门状态**：F-R9-1 修订 + 定向复审前，S-3/S-7 维持阻塞未闭合；S-2 维持闭合。
- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义）、OD-3（随 E-9）、E-9（ctrlExp fixture ≥5 样本）、E-10（受控断线复现，实施期）、E-12（E-12b wire 样本，owner 授权后按 §4 捕获清单——含 pong 项——采集）。
- **待办（非门）**：A-R6-1 实施前随手补入（§3.2 Abort 规格 `finishHydrateLocked` 关闭句）。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证、E-12b wire 捕获与 UI automation 另行授权。实施前按 §2.1 实施来源门模板现场重跑来源清单。
- **配套树时效性提醒**：本轮亲核时配套 iOS 树已前进至 2ee093cb（简报 bb3e1802 之后 owner 并行方案又落 2 个 docs 提交）；四锚点文件零改动已按当前实况复验。后续轮次的来源清单应以各自门点现场输出为准，不沿用本报告数值。

---

## 评审员交接块

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: d1c1cdbb3dd701d5f813721c9e6385097311185f97a770350d27e07ddd0ed04b
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r9.md
scope: full
blockers: 1
advisories: 2
open_gates: [OD-1, OD-3, E-9, E-10, E-12]
round_complete: true
contract_version: plan-contract-v1.1
```
