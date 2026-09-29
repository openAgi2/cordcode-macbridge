# codex-remote 断线韧性与恢复专项方案 评审报告（r10）

- 日期：2026-09-28
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（**v1.9**——r9 REVISION_REQUIRED（F-R9-1）后的修订轮）
- 评审时方案 SHA-256：`025bf218395667bf9f80c8152d0045e4865a4c84636f187f6bfdc86a9c549524`（整文件 `shasum -a 256` 亲算，**与派发记录一致**——评审对象身份确认）；正文哈希 `head -n 391 | shasum -a 256` = `a1abce554720534a48d65a6a12c607d4d679fe174bf08b0170dfeb50c8e53e56` 亲算，与方案内设计师交接块自报一致（口径同 v1.3~v1.8）
- 评审范围：**full**（任务简报字段 7；评审员模式 fresh——先独立形成风险清单并逐锚点亲核，后读 r9 报告作回归基线；按复审纪律复用 r1~r9 已核未变项，重点执行 r9 §8 裁定的三处定向复核 + v1.8→v1.9 增量归属 + 门控一致性 + A-R6-1 状态 + r5~r8 承重锚点抽查；简报重点①所列 F-R8-1 四处经 r9 闭合确认后本轮以「v1.9 未回归 + 承重锚点本轮独立重核」方式覆盖）
- 契约版本：plan-contract-v1.1（plan-review SKILL.md + plan-contract.md + review-guide.md + audit-plan 接入约定均已读取；audit-plan 纪律按 E-12b pending 处置复核，见 §7）
- 结论：**APPROVED**（blockers 0，advisories 0）
- 授权边界遵守声明：本轮唯一写入文件为本报告；方案、产品代码、历轮报告、两仓源码、官方 checkout 一律只读，未 commit、未部署、未构建、未采集任何真机/wire fixture（E-12b 需 owner 授权，未触碰）。评审通过 ≠ 实施授权。
- 任务简报时效性注记：简报为 r9 启动前亲核时点文本，其叙述仍写「本轮评审对象 v1.8」「本轮 r9」——r9 已完成（REVISION_REQUIRED 1 阻塞 2 建议）并产出 v1.9，本轮（r10）实际评审对象为 **v1.9**；以派发 SHA（025bf218…，与当前文件一致）与本报告路径（r10）为准，不构成身份歧义。配套 iOS 树简报记录 bb3e1802，本轮亲核已前进至 **3666d8d3**（owner 并行「流式优化二期方案」v5/v6 两个 docs 提交）——简报已预警该树为移动目标，四锚点文件零改动已按当前实况重新亲证（见 §2.1），后续轮次来源清单以各自门点现场输出为准。

---

## 1. 结论

### verdict: APPROVED（通过；S-3/S-7 通过依据按方案 §4 复审门自设的裁定恢复，实施仍被 E-12b 等门住）

**F-R9-1 闭合确认成立**。r9 要求的三处修订（§3.3「pong 信封到达即 ack」子项 / §4 E-11 行定量表述修正 / §6 风险 5 第二触发路径登记与残余 ② 定量修正）经本轮逐处对照官方源码（FETCH_HEAD @ e72da2b5）与本仓源码（@ 73539b71 工作树，代码与 07721783 等价本轮亲跑复证）**全部在位、语义准确、与设计一致**（锚点表见 §3.1/§3.2）；r9 闭合标准三条款逐项满足：① §3.3 对 pong 信封有明确 ack 规则（到达即 `s.ack(env)`、`(seq, MAX)` 构造、经既有 `acksDisabled` 早退、重放 pong 幂等清除、closed 态在途 ≤1 / armed 态 ≤⌈T/ping⌉ 两态有界）；② E-11 行/风险 5 残余 ② 的定量与设计一致（v1.8 无前提定量已撤回并记录隐含前提与楔死反例，改为随修复成立）；③ §5 S-3 断言覆盖 pong ack（含楔死负例：fake conn 仅注入 pong 流 → 每条 pong 均被 ack、未 ack 在途恒 ≤1）。联动五处（§3.7 依赖注 closed/armed 两态分述、§3.7 残余 ②、§5 S-3/S-7 行、§5 依赖序、§6 验证分层）全部在位且互相一致。

**本轮独立新增的关键亲证（超出 r9 证据链的一环）**：host 侧 ack 清除发生在 `record_client_message_delivery`（官方 `websocket.rs:200-221`，`outbound_buffer.ack` 调用点 `:217`）——这是 **transport 层状态操作，不经过 app-server backend**。因此「backend 楔死（RPC 无响应）、transport 存活」场景下 pong ack 仍被 host 正常处理、缓冲仍被清除——修复机制在楔死场景**确实生效**，而非仅健态成立。这一环是 F-R9-1 修复语义的可达性收口（r9 证明了累积后果与无清除路径，本轮补证了修复路径不受楔死影响）。

两条建议随改核验：**A-R9-2 闭合**（§7.2 v1.8 处置表「修订位置」列补记 §2.3 术语同步，标注 v1.9 补记）；**A-R9-3 闭合**（活文本 pong 路径锚点四处 `:221-237`→`:222-242` 勘正——§2.2/§3.3/§4 E-11/文档头 v1.8 声明，send 行 `:239` 本轮亲读确认入区间；旧区间仅存于更正记录与 §7.2 历史表，后者按勘误-4 先例声明不回写，处置自洽）。

**其余核验**：v1.8→v1.9 增量全部可归属、无夹带（20 处 v1.9 标记逐一归属，方法与结果见 §6）；门控一致性（E-12b 门住 S-3/S-7、验收 4 联动、复审门「不自判通过」）八处一致；F-R8-1 四处修订在 v1.9 中零回归（r9 闭合结论维持，承重官方锚点本轮独立重核吻合）；A-R6-1 维持「实施前随手补入」路径合规（r6 闭合标准，r7/r8/r9 维持，本轮 grep 复核维持）；r5~r8 承重锚点抽查全部吻合（§3.3）；稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3）不变。

**本轮 fresh 独立风险清单的主动排查（均未成立，记录以备回归）**：① pong ack 在 0.154.0-alpha.6.2 sentinel 缺陷形态下是否新增 ack→sentinel 风暴路径——不成立：pong ack 经 `ack()` 既有 `acksDisabled` 早退（`stream.go:397-399`），sentinel 到达即 arm 熔断，armed 态 pong 不逐条 ack、由 probe 按最高游标清除，噪声上界仍 1 条/T（§3.3/§3.7 声明与源码一致）；② 缺口后 pong ack 越过缺口清除 host 缓冲内未收信封是否新损失——不成立：与既有 plain 消息 ack 同型同取舍，§3.3 replay 取舍已显式登记「既有取舍不变」且缺口内容本就不从 replay 恢复；③ pong ack 是否需要 E-12b 新增样本项——维持 r9 §7 裁定：pong 帧 wire 形状由样本集⑥覆盖、ack 回程形状与既有生产 plain 消息 ack 同构（`stream.go:400-406` 构造、官方 `protocol.rs:120-121` `skip_serializing_if` 对称、生产已长期验证），不新增；④ pong ack 是否破坏验收 4「无缺口时零开销」——不成立：pong ack 是每 10s 一条的常量缓冲管理写，不触发任何对账/fan-out（假缺口问题已由 F-R8-1 游标修复消除）；⑤ 判定表「理论不可达」混合帧型分支对 pong 的排除依据——本轮亲证 writer loop 每 seq 唯一归属一个信封单元（`websocket.rs:1031-1038`+`:1066-1067`），pong 不拆分（`segment.rs:327-329` 二选一），排除成立。

### implementation_readiness（方案通过 ≠ 实施就绪）

- 授权边界：owner 未授权任何代码改动，**可开工切片为 0**；评审通过不构成实施授权。
- 实施前置门（与意见分开列，不变）：**OD-1**（owner 确认重试语义，门住 S-1a/S-1b）、**OD-3**（随 E-9）、**E-9**（ctrlExp 真机 fixture，门住 S-4 整体）、**E-10**（受控断线复现，S-1/S-2/S-3 完成验收门 + S-7 可见性，实施期）、**E-12**（E-12b wire 样本含第⑥项 pong 帧，owner 授权后按 §4 捕获清单采集，门住 S-3/S-7 整体实施）。
- 复审门：F-R9-1 闭合经本轮确认，**S-3/S-7 通过依据恢复**（r7 的 APPROVED 对该两切片的通过依据不再暂停）；S-1a/S-1b/S-2/S-4/S-5/S-6 通过依据维持（r6/r7/r8/r9 确认，本轮抽查维持）。
- 待办（非门、非阻塞）：A-R6-1（§3.2 Abort 规格补 `finishHydrateLocked` 关闭句）维持「实施前随手补入」路径（r6 闭合标准，r7/r8/r9 维持，本轮 grep 亲证 §3.2 Abort 条仍无该句、仅存于登记处三处——未夹带，合规）。
- E-10 真机验证、E-12b wire 捕获与 UI automation 仍需 owner 另行授权。

---

## 2. 来源与覆盖

### 2.1 来源身份表（本轮亲核，读取源码前重新生成；两仓各 3 树全量枚举 + 官方 checkout，每行完整 40 字符提交哈希）

| 仓库 | 工作树路径 | 分支 | 完整提交 | 未提交状态（`git status --porcelain` 亲跑） | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge（方案与评审所在树） | /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline | feat/ios-native-message-timeline | 73539b71c0c3552a3c16548d05417813ba907869 | 仅方案文档 1 处未提交修改（v1.9，评审循环正常形态）+ 未跟踪评审报告 r5/r6/r6-meta/r7/r7-meta/r8/r9 七份（只读；本报告为第 8 份未跟踪产物） | 简报①指定树；`git diff --name-only 07721783..HEAD` 本轮亲跑 9 文件**全部为 docs/*.md**——代码零改动，Mac 锚点与 07721783（r1~r9 核验基线）等价 | 纯设计文档评审，无构建/安装——N/A（无构建场景） |
| cordcode-macbridge（main 树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge | main | 73539b71c0c3552a3c16548d05417813ba907869 | 干净（亲跑为空） | 枚举记录（P0：含配套工作树，无论是否实际引用）；与方案所在树同提交 | N/A |
| cordcode-macbridge（第三树，枚举项） | /Users/jacklee/Projects/cordcode-macbridge-plan-approval | detached | b2b2523526b6af7990c9688fd29b7e285d7ce78c | 干净（亲跑为空） | 枚举记录 | N/A |
| cordcode-ios（iOS 锚点核验来源，简报③指定） | /Users/jacklee/Projects/cordcode-ios | main | ccb5a0df647865324e11aedebbb089b9fc07bbb5 | 干净（亲跑为空；与 r6/r7/r8/r9 核验来源同提交，未漂移） | 简报③指定 main 工作树 | N/A（纯设计） |
| cordcode-ios（同族配套树，P0 要求记录） | /Users/jacklee/Projects/cordcode-ios-native-message-timeline | feat/ios-native-message-timeline | **3666d8d33a5e93e27fd77d5db02441b304ff21bf**（简报记录时点 bb3e1802、r9 时点 2ee093cb，本轮亲核又前进 2 个 docs 提交——owner 并行「流式优化二期方案」v5/v6，移动目标如实记录） | 4 个未跟踪 docs 文件（2026-09-27-native-timeline-streaming-phase2 评审报告 r2/r3/r4/r5，与本方案无关，只读） | 非本任务锚点来源。**锚点有效性（本轮亲跑）**：`git diff --stat ccb5a0df..3666d8d3` 仅 1 文件（并行方案文档本身，+569 行）；四个 iOS 锚点文件（ProjectionStore/CCCodeBridgeModels/ChatViewModel/CCCodeBridgeTransport）区间零改动——iOS 锚点结论与工作树选择无关 | N/A |
| cordcode-ios（第三树，枚举项） | /Users/jacklee/Projects/cordcode-ios-plan-approval | detached | a336b68bb37765d2ea23c14f6508619378c839ae | 干净（亲跑为空）；`git merge-base --is-ancestor` r6/r7/r8/r9 已证为 iOS main 祖先，本方案无任何锚点取自该树 | 枚举记录 | N/A |
| openai/codex（官方，只读） | /Users/jacklee/Projects/codex | main（HEAD == FETCH_HEAD 本轮亲核，`rev-parse` 双值一致） | e72da2b53805894878023d01949a25a082e0a5cb | 干净（`git status --porcelain` 亲跑为空，exit=0） | 简报④：一律以 FETCH_HEAD 为准（亲核一致，与方案 §2.1 及 r1~r9 记录相同） | N/A |

**评审对象身份**：派发哈希与读取哈希一致（报告头）。HEAD 73539b71 内方案为 v1.4 §3.2 时点半成品快照（方案 §2.1 自记，r8/r9 亦核）；v1.5~v1.8 字节快照均不可从 git 复原（历史形态，r6/r7/r8/r9 同此声明）——v1.8→v1.9 增量以「v1.9 标记全文定位逐一归属 + r9 报告引用的 v1.8 原文逐点比对 + 声明范围一致性」三重交叉印证法核验（方法与结果见 §6）。

### 2.2 覆盖声明

- **本轮 fresh 亲核**（约 45 组锚点，三仓 15 文件）：F-R9-1 闭合链官方侧全环（`client_tracker.rs:179/:221/:222/:225/:233/:239`（send 行入 A-R9-3 勘正区间亲读）`/:247/:273/:312-324`（close_client 全体 + `remove_client :326-337` 亦无 buffer 引用）、`websocket.rs:101-110`（insert 无类型过滤、`:109` used+1）`/:112-138`（`:123` unwrap_or(MAX)/`:129` ≤ 比较/`:131` used−1/`:136` remove 仅 ack 内）`/:200-221`（**ack 清除为 transport 层、`:217` 调用点——本轮新证**）`/:410`（单一 server_event 通道）`/:996/:999-1008/:1010-1019/:1020`（全局反压与 ping 分支豁免）`/:1031-1038/:1046-1062/:1066-1067`（seq 无条件分配与逐信封 insert）`/:965-988`（重连重放仅 clone+重发）`/:1140-1152/:1154-1168`（join-set/idle sweep 仅 invalidate）、`protocol.rs:114-121`（ack 语义覆盖一切 server envelopes + `:120-121` skip_serializing_if）、`transport/mod.rs:24`（CHANNEL_CAPACITY=128）、`segment.rs:19-22/:327-329/build_chunk_envelope seq_id: envelope.seq_id`、`tests.rs:1650/:1528/:1086`、`websocket.rs:3062`）；官方开源仓无信封层 ping/ack 参考实现的全仓 grep（`ClientEvent::Ack` 构造仅 tests.rs:1650、`ClientEvent::Ping` 仅 host 侧 `client_tracker.rs:122/:222` + tests、app-server-transport 之外零出现）；MacBridge 侧（`ws.go:316-319`（ping 10s/idle 60s）、`stream.go:305-306/:307-316`（pong 分支、`:311` 判死、`:315` markHostActivity）`/:324/:339`（ack 仅两处消息分支）`/:359-373`（sentinel arm 现状）`/:390-407`（ack()：`:391-393` SeqID nil 早退、`:397-399` acksDisabled 早退、`:400-406` 构造无 SegmentID）、`envelope.go:16/:45`、`main.go:1046-1066`（3s catalog 循环）、`session.go:328-341`（`:335` thread/loaded/list）、`rpc.go:58-61`）；r5~r8 承重锚点抽查（§3.3 表）；iOS 白名单（`ProjectionStore.swift:164-181`）；think.md 双仓（macbridge `:2-6`/`:45`/`:415-418`、ios `:477`）；交接块正文哈希复算；v1.9 标记全文定位（20 处）；v1.8 旧定量表述全文残留 grep（仅更正记录与历史表）。
- **复用（按命题/锚点粒度；复用合法性依赖的三项源码身份等价本轮全部亲自重跑证实：Mac 代码 07721783≡HEAD（diff --name-only 9 文件全 docs）、iOS 四锚点文件跨候选零改动（含最新 3666d8d3）、官方 checkout e72da2b5 未动且干净）**：r5/r6/r7/r8/r9 已亲核且 v1.9 未改动的锚点——§2.2 复用表其余各行、§2.3 现状摘要其余句、§3.1/§3.2/§3.4/§3.5/§3.6、E-1~E-8/E-10 各行、E-11 行 v1.8 前的既有子声明、`backoff.go`/`pairing_persist.go`/`pairing.go`/`agent.go`/`codec.go`/`session.go:161-212`/`history_paginated.go`/`handlers_projection.go` 各段、官方 `remote.rs`/`reconnect.rs`/`enroll.rs`/`server_api.rs`/`backoff.rs` 引用、`websocket.rs:74-79/:201-207/:1275-1330/:1342-1351`。r9 §3.1/§3.2 锚点表作为 F-R8-1 闭合与 F-R9-1 证据链的复用依据；其指出的 v1.8 定量缺陷即本轮 §3.2 复核对象。
- **未核项（如实声明）**：① relay（chatgpt.com 闭源服务端）内部行为——本轮结论只依赖 host 侧与 controller 侧源码；pong 携带 seq_id 到达 controller 的 wire 级确认归入 E-12b pending 门（样本集⑥已在位）；② E-9/E-10/E-12b 为 pending 运行证据，按授权边界未采集，状态如实；③ v1.5~v1.8 字节快照不可复算（历史形态，见 §6 方法说明）；④ 本轮未重做 r5~r9 级的全部锚点逐项重查（复用范围见上；未重查项经 §6 增量核验确认 v1.9 零触碰）；⑤ F-R9-1 的「楔死 backend」场景可达性以方案自引事故（ios think.md:477）形态一致性为证据，未做活体复现（授权边界内不可得，如实声明）；修复机制在楔死场景的生效性本轮以源码结构亲证（§3.2 首行），非运行态验证。

---

## 3. 锚点核验表（本轮承重项摘录；「✓✓」=定位与语义均成立）

### 3.1 F-R9-1 闭合链——官方侧（r10 重点，全部亲核）

| 方案原引用（v1.9） | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| pong 计入 host outbound buffer，清除仅 client Ack 一途 | `insert` `websocket.rs:101-110`、ack `:112-138` | `:101 fn insert(&mut self, server_envelope: &ServerEnvelope)` … `:108 .push_back(server_envelope.clone());` `:109 self.used_tx.send_modify(\|used\| *used += 1);`；`:123 let acked_cursor = (acked_seq_id, acked_segment_id.unwrap_or(usize::MAX));` `:129 let is_acked = envelope_cursor <= acked_cursor;` `:131 *used -= 1` `:136 self.buffer_by_stream.remove(&key)`（仅 ack 内） | ✓✓ insert 无事件类型过滤（pong 计入全局 used）；ack 按 `(seq, seg.unwrap_or(MAX))` 游标清除——pong ack 的 `(seq, MAX)` 语义依据成立 |
| **ack 清除为 transport 层操作，不依赖 backend（本轮新证——楔死场景修复生效的可达性收口）** | `record_client_message_delivery` `websocket.rs:200-221` | `:213 if let ClientEvent::Ack { segment_id } = &client_envelope.event && let Some(acked_seq_id) = client_envelope.seq_id && let Some(stream_id) = client_envelope.stream_id.as_ref() { :217 self.outbound_buffer.ack(&client_envelope.client_id, stream_id, acked_seq_id, *segment_id) }` | ✓✓ ack 处理在 WebsocketState（transport 层）内完成，不经过 app-server backend——backend 楔死（RPC 无响应）时 pong ack 仍被 host 处理、缓冲仍被清除 |
| 官方协议预期 ack 清除「一切」server envelopes ≤seq（含 pong） | `protocol.rs:114-119` | `:116-118 /// Backend-generated acknowledgement for all server envelopes addressed to client_id and stream_id whose envelope seq_id is less than or equal to this ack's seq_id.`（`:120-121 #[serde(skip_serializing_if = "Option::IsNone")] segment_id: Option<usize>`） | ✓✓ pong 是 server envelope，不 ack 即滞留；ack 无 segment_id 时字段按 serde 规则缺席——与 MacBridge `envelope.go:45 omitempty` 对称 |
| pong status 仅反映客户端注册，不反映 backend 健康度 | `client_tracker.rs:179/:225/:233` | `:179 let (status_tx, status_rx) = watch::channel(PongStatus::Active);`（注册即 Active）；`:225 let _ = client.status_tx.send(PongStatus::Active);`（已知客户端 Ping 恒回 Active）；`:233 PongStatus::Unknown`（仅未注册客户端） | ✓✓ 楔死 backend 时客户端保持注册、pong 恒 Active 持续流动，本仓 `stream.go:311` 判死不触发——r9 推演前提成立 |
| pong 两条路径均入单一 server_event 通道（A-R9-3 勘正后区间） | Ping arm `client_tracker.rs:222-242`、`run_client_outbound :247-293` | `:222 ClientEvent::Ping => {`；已知客户端 `:223-227`（`:225 send(Active)` 后 return，经 `run_client_outbound` `:273 ServerEvent::Pong{status: status_rx.borrow().clone()}` 回 Pong）；未知客户端 `:229-240` 直接 spawn；**:239 `let _ = server_event_tx.send(server_envelope).await;`（承重 send 行，本轮亲读确认入 `:222-242` 区间）**；`:221` 为 `ClientMessageChunk\|Ack` 直通行 | ✓✓ A-R9-3 勘正正确（`:221` 非 Ping arm 起点、send 行入区间）；两条路径均入通道命题锚点自足 |
| writer loop 对通道内一切信封无条件 seq 分配并逐信封 insert | `websocket.rs:1031-1038/:1046-1062/:1066-1067`、通道 `:410` | `:410 let (server_event_tx, server_event_rx) = mpsc::channel(super::CHANNEL_CAPACITY);`；`:1035-1038 let seq_id = *state.next_seq_id_by_stream.entry(seq_key.clone()).or_insert(1);`；`:1062 state.outbound_buffer.insert(&server_envelope);`（对 split 后每个信封）；`:1066-1067 .insert(seq_key, seq_id.saturating_add(1))` | ✓✓ pong 不豁免、每 seq 唯一归属一个信封单元——「pong 恒为新 seq、不命中理论不可达混合分支」的排除依据成立 |
| 全局反压 128、ping 分支不受门限 | `websocket.rs:996/:999-1027`、`transport/mod.rs:24` | `:996 let outbound_has_capacity = *used_rx.borrow() < super::CHANNEL_CAPACITY;`；`:999-1008 ping_interval.tick() → websocket_writer.send(Ping)`（不检查容量）；`:1010-1019 used_rx.changed(), if !outbound_has_capacity`（满则等待）；`:1020 recv, if outbound_has_capacity`；`mod.rs:24 pub const CHANNEL_CAPACITY: usize = 128;` | ✓✓ 128×10s=1280s≈21.3 分钟填满的推演成立（`ws.go:317` ping 10s 本轮亲证） |
| 无清除路径（被弃 stream 的 pong 永久占用） | `close_client` `client_tracker.rs:312-324`、重连重放 `websocket.rs:965-988`、join-set `:1140-1152`、idle sweep `:1154-1168` | close_client 仅 `remove_client`（`:326-337` 亦无 buffer 引用）+ `disconnect_token.cancel()` + `ConnectionClosed`；重放 `:965-971` 仅 `outbound_buffer.server_envelopes().cloned()` 重发；join-set/idle sweep 仅 invalidate 重组器/消息流 | ✓✓ E-11 既有子声明维持（定位注记：函数体实际 `:312-324`，方案 §2.2 记 `:312-323` 少收尾一行——全部承重行为行均在两区间内，无承重行被排除，不构成 A-R9-3 类问题） |
| 官方开源仓无信封层 ping/ack 参考实现 | 全仓 grep | `ClientEvent::Ack` 构造仅 `tests.rs:1650`（`event: ClientEvent::Ack { segment_id: None }`）；`ClientEvent::Ping` 仅 host 侧 `client_tracker.rs:122`（`matches!` 检查）`/:222`（match arm）+ tests 四处；app-server-transport 之外全仓零出现（本轮 grep 亲证） | ✓✓ 本仓 ping/ack 为自加策略、累积后果由本仓 controller 设计承担——方案归责表述准确 |

### 3.2 F-R9-1 闭合链——MacBridge 侧与三处修订文本对照（r10 重点）

| 方案原引用（v1.9） | 实际位置 | 源码摘录（带行号） | 定位/语义 |
| --- | --- | --- | --- |
| pong 分支 `stream.go:307-316`、判死 `:311`、ack 仅 `:324`/`:339` | 同 | `:305-306 case typeAck: continue`；`:307 case typePong:`（`:308-310` 注释）`:311 if env.Status != "active" {` `:312-313 s.fail(...); return` `:315 s.markHostActivity()` `:316 continue`；`:324 s.ack(env)`（typeServerMessage）、`:339 s.ack(env)`（chunk） | ✓✓ pong 分支现无 seq/ack 处理（修复插入点在 `:315` 后成立）；「ack 仅两处消息分支」属实——F-R9-1 的本仓侧前提维持 |
| ack 构造 `:390-407` 无 SegmentID、熔断早退 `:397-399` | 同 | `:390 func (s *Stream) ack(env Envelope) {` `:391-393 if env.SeqID == nil { return }`；`:397-399 if disabled { return }`；`:400-406 Envelope{Type: typeAck, ClientID, EnvID, StreamID, SeqID: env.SeqID}`（无 SegmentID） | ✓✓ pong ack 复用该构造 → `(seq, MAX)` 游标；SeqID nil 早退使「对携带 SeqID 的 pong」防御性条件天然满足；acksDisabled 早退使熔断语义优先——两态互不冲突的机制基础成立 |
| 消息 ack 由 3s catalog 循环 RPC 响应保证、楔死即停 | `main.go:1046-1066` → `session.go:335` → `stream.go:324` | `:1056 ticker := time.NewTicker(3 * time.Second)` → `AttachLiveCatalog`（`session.go:328-341`）`:335 cl.RequestContext(ctx, "thread/loaded/list", …)` → 响应经 typeServerMessage 分支 `:324 s.ack(env)`；`rpc.go:58-61` RequestContext 包装 | ✓✓ v1.8「≤1 条」隐含前提的成立域（健态）与失效域（楔死）边界准确 |
| §3.3 pong ack 子项文本（:198） | 方案 `:198` | 到达即 `s.ack(env)`、`(seq, MAX)` 清除该 pong 及更早一切已收信封、单连接流内有序故语义安全、重放 pong 幂等清除、经 `acksDisabled` 早退（armed 态由 probe 顺带清除、窗口 ≤⌈T/ping⌉ 有界）、closed 态在途 ≤1 无条件成立、E-12b 不新增样本项 | ✓✓ 与官方源码逐环吻合（§3.1 表）；两态定量（closed ≤1 / armed ≤⌈T/ping⌉）均在位；「不依赖 catalog 循环健康」经 §3.1 transport 层 ack 处理亲证成立 |
| §4 E-11 行文本（:265） | 方案 `:265` | v1.8 括注定性为无前提定量、隐含前提（3s catalog RPC 响应）与楔死反例记录在案、改为「S-3 v1.9 后『在途 ≤1 条』无条件成立，§6 风险 5 定量随之修正」；状态列注记 v1.9 修正 | ✓✓ 定量与设计一致（closed 态语义；armed 态上界在 §3.3/§3.7 明示，E-11 行指向 §3.3 全量推演，读法自洽） |
| §6 风险 5 文本（:299） | 方案 `:299` | 第二触发路径（无 sentinel/熔断参与）登记 + r8 §6.2③ 排除的成立域修正（仅健态）+ 残余 ② 定量修正（「消息在途通常 0–2 条 + 未 ack pong ≤1 条——无条件有界；修复前楔死场景可达 128 条」） | ✓✓ 与 §3.3 推演一致；残余 ② 的「无条件有界」表述准确（closed ≤1 + armed ≤⌈T/ping⌉，均有界） |
| §5 S-3 断言（:279） | 方案 `:279` | 「pong 到达 → 出站 ack 携带该 pong 的 seq（无 `SegmentID`）；楔死负例：fake conn 仅注入 pong 流（无消息信封）持续 N×10s → 每条 pong 均被 ack、未 ack 在途恒 ≤1（F-R9-1 闭合标准）」 | ✓✓ 断言可判定（fake conn + pong-only 流）；楔死场景熔断保持 closed（无 sentinel 到达）故「每条均 ack、恒 ≤1」与设计一致 |
| §3.7 依赖注/残余 ②（:239/:243）、§5 依赖序（:285） | 方案 `:239/:243/:285` | closed/armed 两态清除路径分述（熔断语义优先、两态互不冲突）；pong ack 列入「不构成 S-7 前置」的 S-3 其余部分 | ✓✓ 联动一致；pong ack 不依赖 S-7、S-7 probe 输入（游标含 pong seq）不依赖 pong ack——依赖方向无环、无矛盾 |

### 3.3 r5~r9 承重锚点抽查（复用有效性确认，本轮亲核）

| 命题 | 核验 | 结果 |
| --- | --- | --- |
| F-R5-1：commit 先取 liveSnap、按 tx 标志二选一 merge、Restore 前置；merge 第一分支回撤终态；tx 标志先例 | 亲读 `projection_kernel.go:1319`（CommitHydrateTransaction）、`:1335`（liveSnap）、`:1352-1356`（unionLiveTurns else mergeHydrateBaselineWithLiveExecution）、`:1357`（Restore）、`:1457-1458`（executionInFlight(live) && !executionInFlight(cold) → cold.Execution = live.Execution）、`:682-688`（liveOnlyAdmission/unionLiveTurns） | ✓✓（v1.5 reconcile 标志设计依据维持） |
| F-R4-1：READY pathless 两分支均不可行 | 亲读 `:1020-1026`（pathless+sourceChanged → break，否则 AlreadyReady）、`:969-976`（pathlessRichHistoryBackend 名单：opencode/grokbuild/claude/claudecode/opencode-web/codex-web——**无 codex-remote**）、`:1069-1072`（pathless 空重建分支） | ✓✓（专用 reconcile 事务必要性维持） |
| 勘误-4 修正值：runner 侧 commit 后处理 | 亲读 `handlers_projection.go:1163`（releaseDeferredPushCandidates）、`:1178`（PublishProjectionPatch）、`:1182-1184`（persistCodexProducerSeed 门控 backendID == "codex-remote"） | ✓✓（v1.6 勘误后锚点正确） |
| chunk 共享 seq / 拆分阈值 / 二选一 / 重组上限 | 亲读 `segment.rs:20`（150KB）、`:21`（100MB）、`:327-329`（≤MAX 整体返回）、`build_chunk_envelope` `seq_id: envelope.seq_id` | ✓✓ |
| iOS 白名单不含 hydrate_failed | 亲读 `ProjectionStore.swift:164-181`（11 case：projection.hydrating/disconnected/not_connected/relay.not_ready/relay.closed/request_timeout/send_timeout/ping_timeout/connect_timeout/hello_timeout/websocket.closed——无 projection.hydrate_failed） | ✓✓（E-3/E-5 维持） |
| think.md 复盘锚点 | 亲读 macbridge `:2-6`（26.924 升级断裂）/`:45`（backend_status_changed 待立项另立——OD-2 decided 依据）/`:415-418`（2026-09-14 风暴）；ios `:477`（thread/list 每轮 12s 超时、反复重连、2.5h 不自愈——楔死形态证据） | ✓✓ |
| 官方测试不变量引用 | 亲读 `websocket.rs:3062`（websocket_state_drops_replayed_client_chunks_after_completion）、`tests.rs:1528`（remote_control_transport_clears_outgoing_buffer_when_backend_acks）、`tests.rs:1086`（remote_control_transport_reconnects_after_disconnect） | ✓✓ |
| F-R8-1 四处修订零回归 | 亲读 §3.3 判定表 pong 分支（:190）/§1 R-4 括注（:33）/§2.2 两行（:107-108，含 A-R9-3 勘正）/§2.3（:133）/§4 E-11·E-12b ⑥（:265-266）/§5 S-3 pong 交错负例（:279） | ✓✓（r9 闭合结论维持；承重官方锚点本轮于 §3.1 独立重核吻合） |

---

## 4. 意见（F-ID）

**本轮无阻塞、无建议。** r9 的 1 阻塞 2 建议全部闭合（§5）；fresh 独立风险清单的主动排查五项均不成立（§1）；未发现 v1.9 新引入缺陷或 r1~r9 已核内容回归。

以下三处定位注记经核验**不构成意见**（记录以备后续轮次回归，避免同型问题重复争议）：

1. **close_client 区间双记**：§2.2 E-11 行记 `client_tracker.rs:312-323`、§7.1 记 `:312-324`，函数体实际 `:312-324`。差值为无行为内容的收尾行（`}`），全部承重行为行（remove_client 调用/cancel/ConnectionClosed）均在两区间内——与 A-R9-3 类（承重 send 行被区间排除）不同类，命题（无 buffer 引用）对整函数成立。
2. **pong 分支区间双记**：§2.3/§3.7 记 `:307-314`（判死子分支——status 检查与 fail/return，命题为「pong status != active 判死」）、§3.3 v1.9 记 `:307-316`（全分支含 markHostActivity/continue，命题为 pong ack 插入点）——各自对其命题精确。
3. **E-11 行「无条件」的读法**：§4 E-11 行「『在途 ≤1 条』无条件成立」未带 closed 态括注，但其「无条件」指撤回 v1.8 的「消息 ack 持续到达」前提依赖（该行上下文即楔死反例修正）；armed 态上界 ≤⌈T/ping⌉ 在被引用的 §3.3 同子项与 §3.7 依赖注明示，两态定量均在位、无矛盾。实施规格以 §3.3 为准（精确），E-11 为证据行并显式指向 §3.3 全量推演。

---

## 5. 复审处置与回归核查

### 5.1 r9 意见处置裁决

| 意见 | 本轮裁决 | 依据 |
| --- | --- | --- |
| F-R9-1（阻塞） | **闭合** | 三处修订逐处对照官方源码亲核全部在位、语义准确、与设计一致（§3.1/§3.2 表）：§3.3 pong ack 规则明确（到达即 ack、`(seq, MAX)`、熔断早退、两态有界、重放幂等）、E-11 行/风险 5 残余 ② 定量随修复一致、§5 S-3 断言含 pong ack 与楔死负例；修复语义安全经官方源码逐环亲证（ack 清除为 transport 层 `websocket.rs:200-221`——楔死场景生效的可达性收口本轮新证）；联动五处在位。**S-3/S-7 通过依据恢复**（方案 §4 复审门自设「由 r10 复审员判定」，本轮判闭合） |
| A-R9-2（建议）v1.8 处置登记遗漏 §2.3 | **闭合** | §7.2（v1.8 处置表）「修订位置」列已补记「§2.3 术语同步（v1.9 补记，A-R9-2）」；v1.8 diff 可审计性恢复 |
| A-R9-3（建议）pong 路径锚点区间未含 send 行 | **闭合** | 活文本四处（§2.2/§3.3/§4 E-11/文档头 v1.8 声明）已勘正 `:222-242`（§2.2 子区间 `:223-227`/`:229-240`）；send 行 `:239` 本轮亲读确认入区间；旧区间仅存于更正记录（:5/:313）与 §7.2 历史表（:319，按勘误-4 先例声明不回写）——处置自洽 |

### 5.2 历轮意见处置裁决（回归清单）

| 意见 | 本轮裁决 | 依据 |
| --- | --- | --- |
| r1 F-1~F-6 / r2 R2-A1~A4 / r2-meta / r3 勘误 / r4 F-R4-1~4 + R4-A1/A2 / r5 F-R5-1 + F-R5-A1~A3 / r8 F-R8-1 | 维持闭合 | 承重锚点本轮抽查吻合（§3.3 表：F-R5-1 链/F-R4-1 admission 链/segment/protocol/iOS 白名单/官方测试）；对应文本不在 v1.9 改动范围（§6 增量核验零触碰）；F-R8-1 四处修订零回归（§3.3 末行） |
| r6 A-R6-1（建议） | **维持开放（实施前随手补入），处置合规** | v1.9 未夹带（grep 亲证 `finishHydrateLocked` 仅存于文档头 v1.6 声明/§6 下一阶段入口/§8.2 未夹带说明三处登记位，§3.2 Abort 条仍无该句）；r6 闭合标准（「v1.6（或实施前随手）该句在位即可；不构成实施前置」）继续有效，r7/r8/r9 维持 |
| r6-meta 两项 / r7-meta 问题（勘误-5） | 维持闭合 | §2.1 六树记录、§8.1/§8.2 勘误记录在位；勘误-5 的 40 字符正确值在 §2.1/§8.1 亲读一致 |
| r8 §6.2③「空闲流 pong 累积」排除 | 维持 r9 裁决（被推翻一般性、健态成立） | v1.9 §3.3/§6 已按 r9 修正记录（排除前提仅健态成立）；非 r8 核验错误，如实登记 |
| 稳定 ID（R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3） | 不变，亲核属实 | 全文精读 + v1.9 标记定位无 ID 变更 |

---

## 6. v1.8→v1.9 增量核验（修订轮纪律）

**方法**：v1.8 字节快照不可从 git 复原（HEAD 73539b71 内为 v1.4 §3.2 时点半成品；r9 报告记录 v1.8 整文件哈希 d1c1cdbb…/正文哈希 d7c55ca4…，与方案交接块自述一致但快照本身已随修订消失），以三重交叉印证：① 全文 `grep -n "v1.9"` 20 处逐一归属分类；② r9 报告引用的 v1.8 原文（E-11 行括注「未 ack pong 由后续消息 ack 的 `(seq, MAX)` 游标顺带清除，稳态在途累积 ≤1 条」、风险 5 残余 ②「通常 0–2 条」、§3.3 ack 子项仅 chunk 补 SegmentID、§3.7 依赖注仅 armed 态）与当前文本逐点比对——v1.9 各处均以「引用旧文 + 更正」形态在位，无静默改写；③ 文档头 v1.9 声明的修订位置清单与实际 v1.9 标记分布比对。

**结果**：
- **20 处 v1.9 标记全部可归属**：文档头声明 2 处（:4-5）、§2.2 A-R9-3 勘正 1 处（:108）、§3.3 pong ack 子项 + 尾句 2 处（:198/:200）、§3.7 依赖注 + 残余 ② 2 处（:239/:243）、§4 E-11 行 + 复审门 2 处（:265/:270）、§5 S-3/S-7 行 + 依赖序 3 处（:279/:283/:285）、§6 风险 5 + 阻塞清单 + 下一阶段入口 3 处（:299/:301/:303）、§7.1 表 4 处（:307/:311-313）、交接块 2 处（:399/:405）——全部映射到 F-R9-1/A-R9-2/A-R9-3 及文档头声明的状态同步联动（§4 复审门/§5 行与依赖序/§6 三处），**无不可归属标记、无夹带**。
- **r9 所验 v1.8 承重内容零回归**：§3.3 判定表（plain/chunk/pong 三类、缺口检测与归属、chunk ack 子项）、§3.7 状态机与八断言（①~⑧ 逐项在位）、E-12b 捕获清单结构（六项含第⑥项 pong 帧）、§3.2 全节（F-R5-1 reconcile 标志链）、A-R6-1 未夹带——除声明的 F-R9-1 修订（pong ack 子项 + 定量修正 + 联动）外逐点一致。
- **交接块**：正文哈希 `head -n 391 | shasum` 亲算 = `a1abce55…` 与自报一致；整文件哈希 `025bf218…` 与派发记录一致；open_gates [OD-1, OD-3, E-9, E-10, E-12] 与 §6 阻塞清单一致；review_round 链（…→r9→v1.9→r10 定向复审）与实况一致；「v1.8 正文哈希 d7c55ca4… 已被本版替代」的更正关系与 r9 报告记录一致。

---

## 7. 门控一致性、audit-plan 专项与交接块

- **E-12 pending → 门住 S-3/S-7 整体实施**：§3.3 尾句（:200）、§4 E-12 行（:266 阻塞列「S-3 整体实施；S-7 整体实施」）+ Gate A 段（:270）、§5 S-3/S-7 依赖列（:279/:283）+ 依赖序段（:285）、§6 验证分层（:290）+ 阻塞清单（:301）+ 下一阶段入口（:303「S-3/S-7 待 E-12 解锁」）、交接块 open_gates——**八处一致**（本轮全文精读逐处核对）✓；S-2 不依赖 E-12 ✓。E-12b 样本集第⑥项（pong 帧）在位并明示为 S-3 缺口规则前置 wire 声明 ✓。
- **验收 4 与 pong 规则联动**：§1 验收 4（:49）↔ §3.3 信封级游标（:183「系统性破坏验收 4」推演 + :193 fail-visible）↔ §5 S-3「正常流零告警（含 pong 稳态零假缺口）」——F-R8-1 修复后「无缺口时零开销」成立；**pong ack（F-R9-1）不破坏该联动**：每 10s 一条常量缓冲管理写，不触发对账/fan-out（本轮独立排查第 ④ 项，见 §1）✓。
- **OD-1 pending → 门住 S-1a/S-1b**（§3.8/§4 Gate B/§5 三处）；OD-2 decided（macbridge think.md:45 本轮亲核）→ S-6 仅桥内；OD-3 随 E-9；E-9 → S-4 整体；E-10 → S-1/S-2/S-3 完成验收 + S-7 可见性——各处一致 ✓。
- **复审门状态**：§4（:270）/§5 依赖序（:285）/§6（:301/:303）均写「S-3/S-7 通过依据维持暂停、待 r10 定向复核三处、本方案不自判通过」——与评审循环实况一致，非超前自判 ✓。本轮结论：F-R9-1 闭合，S-3/S-7 通过依据恢复；S-2 及其余切片维持。
- **audit-plan 专项**：E-12a（源码事实）锚点本轮亲证；E-12b 如实 pending（「同一门有未完成子项，整门不得标 verified」在位）；捕获清单（版本锚定/最小样本集六项含 pong/双独立提取策略/脱敏/计划路径「尚不存在」/授权边界）完整。**F-R9-1 的 pong-ack 修复不新增样本项**（r9 §7 裁定维持：pong 帧 wire 形状由⑥覆盖；ack 回程形状与既有生产 plain 消息 ack 同构——`stream.go:400-406` 构造、官方 `protocol.rs:120-121` serde 对称、生产已长期验证，本轮源码亲证补强）。
- **R→S→验收**：R-1→S-1a/S-1b（验收 1/3）、R-2→S-2（验收 2）、R-3→S-4（E-9 门内）、R-4→S-3+E-12（验收 4——F-R8-1/F-R9-1 修复后该链完整：游标对 pong 推进消除假缺口、pong ack 消除滞留）、R-5→S-6——对应齐全 ✓。

---

## 8. 剩余门与下一阶段

- **本轮意见**：无（0 阻塞 0 建议）。r9 的 1 阻塞 2 建议全部闭合（§5.1）。
- **复审门状态**：F-R9-1 闭合确认，**S-3/S-7 通过依据恢复**（设计层面）；实施仍被 E-12b 门住。
- **仍待满足的实施门（与意见分开）**：OD-1（owner 确认重试语义）、OD-3（随 E-9）、E-9（ctrlExp fixture ≥5 样本）、E-10（受控断线复现，实施期）、E-12（E-12b wire 样本，owner 授权后按 §4 捕获清单——含第⑥项 pong 帧——采集）。
- **待办（非门）**：A-R6-1 实施前随手补入（§3.2 Abort 规格 `finishHydrateLocked` 关闭句）。
- 通过后按切片实施仍需 owner 实施授权；E-10 真机验证、E-12b wire 捕获与 UI automation 另行授权。实施前按 §2.1 实施来源门模板现场重跑来源清单。
- **配套树时效性提醒**：本轮亲核时配套 iOS 树已前进至 3666d8d3（简报 bb3e1802、r9 时点 2ee093cb 之后 owner 并行方案又落 2 个 docs 提交）；四锚点文件零改动已按当前实况复验（`git diff --stat ccb5a0df..3666d8d3` 仅并行方案文档 1 文件）。后续轮次的来源清单应以各自门点现场输出为准，不沿用本报告数值。

---

## 评审员交接块

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan.md
plan_sha256: 025bf218395667bf9f80c8152d0045e4865a4c84636f187f6bfdc86a9c549524
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r10.md
scope: full
blockers: 0
advisories: 0
open_gates: [OD-1, OD-3, E-9, E-10, E-12]
round_complete: true
contract_version: plan-contract-v1.1
```
