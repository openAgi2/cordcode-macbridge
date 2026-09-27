# codex-remote 断线韧性方案 r10 评审 通过结论定向复核报告（r10-meta）

- 日期：2026-09-28
- 复核对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r10.md`（verdict: APPROVED，0 阻塞 0 建议）
- 复核范围（仅此两项，按任务限定）：
  1. **锚点抽查**——从通过报告中随机抽取带行号引用，逐行核对实际文件，行号与语义都必须吻合；
  2. **来源清单完整性**——通过报告的来源清单是否覆盖任务项目规则（CLAUDE.md/AGENTS.md P0 强制来源清单）要求的全部来源组合，含配套工作树，无论是否实际引用。
- 复核人：元审核员（plan-reviewer 角色契约，除本报告外一律只读；未修改任何方案/代码/历轮报告，未 commit、未部署、未构建）
- 复核结论：**confirm = true**（未发现问题；两项复核均通过）

---

## 1. 复核方法与评审对象身份

- 实际执行的全部检查均为本轮亲跑/亲读，命令与结果逐项记录于 §2/§3；未复跑项如实标注。
- 评审对象身份先行确认：`shasum -a 256 docs/2026-09-26-codex-remote-disconnect-resilience-plan.md` 亲算 = `025bf218395667bf9f80c8152d0045e4865a4c84636f187f6bfdc86a9c549524`，与任务派发记录及 r10 报告头声明一致——复核对象身份确认。
- r10 报告另声明正文哈希 `head -n 391 | shasum -a 256` = `a1abce55…`，本轮亲跑复算 = `a1abce554720534a48d65a6a12c607d4d679fe174bf08b0170dfeb50c8e53e56`，一致。

## 2. 锚点抽查（复核项 1）

从 r10 报告随机抽取 **12 组锚点**（要求 ≥3），覆盖官方 codex checkout、MacBridge 源码、方案正文、iOS 源码/复盘库四类来源，含 r10 本轮新证（transport 层 ack 清除）、A-R9-3 勘正承重行、F-R9-1 三处修订文本、r5~r8 承重复用锚点。逐行核对结果全部吻合：

| # | r10 报告引用（报告位置） | 本轮亲核（实际文件与行号） | 结果 |
| --- | --- | --- | --- |
| 1 | 官方 `websocket.rs:200-221`，`:213` `ClientEvent::Ack` 匹配、`:217` `outbound_buffer.ack` 调用——r10 本轮新证「ack 清除为 transport 层操作」（§3.1 表第 2 行） | 亲读 `codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:198-242`：`:213 if let ClientEvent::Ack { segment_id } = &client_envelope.event`、`:214-215` seq_id/stream_id 条件、`:217 self.outbound_buffer.ack(`——逐字吻合；函数体内仅操作 `self` 状态（`outbound_buffer`/`subscribe_cursor` 等），无任何 backend RPC 调用，「不经 app-server backend」语义成立 | ✓✓ |
| 2 | 官方 `client_tracker.rs:222-242` Ping arm、`:225` send(Active)、`:239` 承重 send 行入区间、`:221` Chunk\|Ack 直通行（§3.1 表第 5 行，A-R9-3 闭合依据） | 亲读 `client_tracker.rs:218-247`：`:222 ClientEvent::Ping => {`、`:225 let _ = client.status_tx.send(PongStatus::Active);`、`:239 let _ = server_event_tx.send(server_envelope).await;`、`:221 ClientEvent::ClientMessageChunk { .. } \| ClientEvent::Ack { .. } => Ok(())`——全部精确；`:239` 确在 `:222-242` 区间内 | ✓✓ |
| 3 | MacBridge pong 分支 `stream.go:307-316`、判死 `:311`、markHostActivity `:315`、ack 仅 `:324`/`:339` 两处（§3.2 表第 1 行） | 亲读 `agent/codex-remote/stream.go:300-349`：`:307 case typePong:`、`:311 if env.Status != "active" {`、`:315 s.markHostActivity()`、`:324 s.ack(env)`（typeServerMessage）、`:339 s.ack(env)`（chunk）——逐行吻合；switch 内 `s.ack` 确仅此两处 | ✓✓ |
| 4 | MacBridge ack 构造 `stream.go:390-407`：SeqID nil 早退 `:391-393`、acksDisabled 早退 `:397-399`、构造 `:400-406` 无 SegmentID（§3.2 表第 2 行） | 亲读 `stream.go:386-410`：`:390 func (s *Stream) ack(env Envelope) {`、`:391-393` SeqID nil 早退、`:397-399 if disabled { return }`、`:400-406` `Envelope{Type: typeAck, ClientID, EnvID, StreamID, SeqID: env.SeqID}`——构造体确无 SegmentID 字段 | ✓✓ |
| 5 | MacBridge `ws.go:316-319`（ping 10s / idle 60s；`:317` ping、`:318` 判死限值）（§2.2 覆盖声明） | 亲读 `agent/codex-remote/ws.go:312-321`：`:317 pingInterval = 10 * time.Second`、`:318 streamIdleLimit = 60 * time.Second`——吻合（128×10s≈21.3 分钟推演的输入常量成立） | ✓✓ |
| 6 | MacBridge 消息 ack 链 `main.go:1046-1066`（3s catalog 循环，`:1056` ticker）→ `session.go:328-341`（`:335` thread/loaded/list）（§3.2 表第 3 行） | 亲读 `go-bridge/main.go:1045-1066`：`:1046 attachLiveCatalogPeriodically`、`:1056 ticker := time.NewTicker(3 * time.Second)`；`agent/codex-remote/session.go:328-341`：`:335 cl.RequestContext(ctx, "thread/loaded/list", …)`——链路与行号吻合 | ✓✓ |
| 7 | 方案 `:198` §3.3 pong ack 子项（§3.2 表第 4 行：到达即 ack、`(seq, MAX)`、重放幂等、acksDisabled 早退、closed ≤1 / armed ≤⌈T/ping⌉、E-12b 不新增样本项） | 亲读方案 `:196-200`：`:198` 为「pong 信封到达即 ack（v1.9 新增子项，F-R9-1）」完整子项——报告所述全部要素（到达即 `s.ack(env)`、`(seq, MAX)` 游标清除、重放幂等、`:397-399` 早退、probe 顺带清除、closed 态 ≤1 无条件成立、E-12b 不新增样本项）逐点在位 | ✓✓ |
| 8 | 方案 `:265` §4 E-11 行（§3.2 表第 5 行：v1.8 括注定性为无前提定量、隐含前提与楔死反例记录、改为「S-3 v1.9 后『在途 ≤1 条』无条件成立」） | 亲读方案 `:263-266`：`:265` E-11 行含「v1.9 修正定量表述，F-R9-1：v1.8 括注…为**无前提定量**…S-3 v1.9『pong 到达即 ack』后『在途 ≤1 条』**无条件成立**，§6 风险 5 定量随之修正」，状态列注记「v1.9 修正 pong 定量表述，F-R9-1」——吻合 | ✓✓ |
| 9 | 方案 `:279` §5 S-3 断言（§3.2 表第 7 行：pong 到达 → 出站 ack 携带该 pong 的 seq 无 SegmentID；楔死负例 fake conn 仅注入 pong 流 → 每条均 ack、在途恒 ≤1） | 亲读方案 `:277-284`：`:279` S-3 行验收列含「**pong 到达 → 出站 ack 携带该 pong 的 seq（无 `SegmentID`）；楔死负例：fake conn 仅注入 pong 流（无消息信封）持续 N×10s → 每条 pong 均被 ack、未 ack 在途恒 ≤1（F-R9-1 闭合标准）**」——逐字吻合 | ✓✓ |
| 10 | 方案 `:299` §6 风险 5（§3.2 表第 6 行：第二触发路径登记 + r8 §6.2③ 成立域修正 + 残余 ② 定量修正）与 `:285` §5 依赖序（§3.2 表第 8 行） | 亲读方案 `:297-302`：`:299` 含「**v1.9 增（F-R9-1）第二触发路径与闭合**…不经过熔断/sentinel…仅健态成立」与残余 ②「在途消息信封（通常 0–2 条）+ 未 ack pong ≤1 条，不依赖 catalog 循环健康」；`:285` 含「pong 到达即 ack——不构成 S-7 前置；pong ack 与熔断共享 `ack()` 早退路径、两态清除互补互不冲突」——均吻合 | ✓✓ |
| 11 | iOS 白名单 `ProjectionStore.swift:164-181`：11 case、无 `projection.hydrate_failed`（§3.3 表，E-3/E-5 依据） | 亲读 iOS main 树 `OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:162-183`：`isRetryablePullError` 恰 11 个 case（projection.hydrating/disconnected/not_connected/relay.not_ready/relay.closed/request_timeout/send_timeout/ping_timeout/connect_timeout/hello_timeout/websocket.closed），确无 hydrate_failed——吻合 | ✓✓ |
| 12 | iOS 复盘 `think.md:477`：thread/list 每轮 12s 超时、反复重连、2.5h 不自愈——楔死形态证据（§3.3 表） | 亲读 iOS main 树 `think.md:477`：「Mac 端 codex-remote 自 11:36 起病态（thread/list 每轮 12s 超时、stream idle/closed、pairing 流反复重连），至 14:00 未恢复」——11:36→14:00≈2.4h，报告/方案表述「2.5h 不自愈」为同义约数，语义吻合 | ✓✓ |

**抽查结论**：12 组锚点行号与语义全部吻合，零偏差。r10 报告的承重新证（transport 层 ack 清除 `:213`/`:217`）、A-R9-3 勘正承重行（`:239`）、F-R9-1 三处修订文本（`:198`/`:265`/`:299`）与断言（`:279`）、复用有效性依据（iOS 白名单、think.md、常量链）均经本轮独立重证成立。

**定位注记（非问题，记录备查）**：r10 报告 §3.1 将 `record_client_message_delivery` 引用为 `websocket.rs:200-221`，函数体实际为 `:201-224`（`:200` 为前导空行、引用区间止于 ack 调用实参内）。两处承重引用行 `:213`/`:217` 精确且在区间内，语义命题（transport 层清除、不经 backend）经亲读成立——按 r10 报告 §4 自设标准（承重行为行均在区间内、命题对整函数成立 → 定位注记不构成意见）同类处理，不构成问题。

## 3. 来源清单完整性（复核项 2）

### 3.1 工作树全量枚举亲跑（不沿用报告数值）

- `git worktree list`（MacBridge 仓）：3 树——`/Users/jacklee/Projects/cordcode-macbridge`（main）、`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`（feat/ios-native-message-timeline）、`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（detached）。
- `git -C /Users/jacklee/Projects/cordcode-ios worktree list`：3 树——`/Users/jacklee/Projects/cordcode-ios`（main）、`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`（feat/ios-native-message-timeline）、`/Users/jacklee/Projects/cordcode-ios-plan-approval`（detached）。
- 两仓全部 6 个工作树与 r10 报告 §2.1 来源身份表逐行对应，**无遗漏树、无多余树**；官方 checkout `/Users/jacklee/Projects/codex` 亦在表中。

### 3.2 逐树身份核对（`rev-parse HEAD` + `branch --show-current` + `status --porcelain` 亲跑）

| 来源 | r10 报告记录 | 本轮亲跑 | 一致性 |
| --- | --- | --- | --- |
| MacBridge 方案所在树 | feat/ios-native-message-timeline @ `73539b71c0c3552a3c16548d05417813ba907869`；1 处方案文档修改 + 7 份未跟踪评审报告（本报告为第 8 份） | 同分支同哈希；porcelain = 1 M（方案文档）+ 8 份未跟踪（r5/r6/r6-meta/r7/r7-meta/r8/r9/**r10**） | ✓（第 8 份即 r10 报告自身，报告已自记） |
| MacBridge main 树 | main @ `73539b71…`，干净 | main @ 同哈希，porcelain 为空 | ✓ |
| MacBridge plan-approval 树 | detached @ `b2b2523526b6af7990c9688fd29b7e285d7ce78c`，干净 | detached（branch --show-current 为空），同哈希，porcelain 为空 | ✓ |
| iOS main 树（锚点核验来源） | main @ `ccb5a0df647865324e11aedebbb089b9fc07bbb5`，干净 | main @ 同哈希，porcelain 为空 | ✓ |
| iOS 同族配套树 | feat/ios-native-message-timeline @ `3666d8d33a5e93e27fd77d5db02441b304ff21bf`；4 份未跟踪 docs（r2/r3/r4/r5） | 同分支同哈希（提交未再前进）；porcelain 现为 1 M（并行方案文档）+ 5 份未跟踪（r2/r3/r4/r5/**r6**） | ✓（树为移动目标，见 §3.4 注记） |
| iOS plan-approval 树 | detached @ `a336b68bb37765d2ea23c14f6508619378c839ae`，干净 | detached，同哈希，porcelain 为空 | ✓ |
| openai/codex 官方 | main（HEAD==FETCH_HEAD）@ `e72da2b53805894878023d01949a25a082e0a5cb`，干净 | `rev-parse HEAD FETCH_HEAD` 双值同为 `e72da2b5…`，porcelain 为空（exit=0） | ✓ |

### 3.3 规则要求的清单字段覆盖核对

CLAUDE.md/AGENTS.md P0 强制来源清单要求每行含 7 项：仓库路径 / 分支（游离态显式）/ 完整提交哈希 / 未提交状态 / 任务预期分支 / 配套仓库组合 / 预期产品特性。r10 报告 §2.1 表逐行核对：

- **7 项字段全部在位**：绝对工作树路径 ✓；分支名精确、两处 detached 显式标注 ✓；完整 40 字符提交哈希（本轮逐树 `rev-parse` 亲证一致）✓；未提交状态 `git status --porcelain` 亲跑且逐项列出 ✓；任务预期来源/配套组合列 ✓（简报①③④指定关系 + 配套树 P0 记录说明）；预期产品特性列 ✓（纯设计文档评审无构建场景，记 N/A——诚实且符合「无构建场景」实况）。
- **配套工作树无论是否引用均记录** ✓：iOS 同族配套树（`cordcode-ios-native-message-timeline`）与本任务无锚点引用关系，报告仍按 P0「含配套工作树，无论是否实际引用」完整记录，并附锚点有效性亲证（见下）。
- **来源门点**：报告声明「读取源码前重新生成」（§2.1 标题），符合纯评审场景适用的门点要求（无修改、无构建，后两门不适用）。
- **承重 diff 声明本轮独立复跑证实**：
  - `git -C cordcode-ios-native-message-timeline diff --stat ccb5a0df..3666d8d3` → **仅 1 文件**（`docs/2026-09-27-native-timeline-streaming-phase2-codex-absorption-plan.md`，+569 行）——四个 iOS 锚点文件区间零改动，「iOS 锚点结论与工作树选择无关」的声明成立（本轮锚点 #11/#12 即在 main 树亲核）。
  - `git diff --name-only 07721783..HEAD`（MacBridge 方案所在树）→ **9 文件全部为 docs/*.md**——「代码零改动、Mac 锚点与 07721783 等价」声明成立（本轮锚点 #3~#6 即在 HEAD 树亲核）。

### 3.4 移动目标注记（非问题，如实记录）

本轮亲核时配套 iOS 树的未提交状态相对 r10 门点又前进了（新增并行方案文档修改 + r6 评审报告，未跟踪由 4 份变 5 份），但提交哈希 `3666d8d3` 未变。该漂移方向与 r10 报告预判完全一致（owner 并行「流式优化二期方案」评审循环继续推进），且报告已显式声明「该树为移动目标……后续轮次的来源清单应以各自门点现场输出为准，不沿用本报告数值」（§2.1/§8）。本轮以现场重跑完成核对，不构成 r10 报告的来源清单缺陷。

## 4. 复核结论

- **锚点抽查**：12 组锚点（要求 ≥3）行号与语义全部吻合，含 r10 本轮新证、A-R9-3 勘正承重行、F-R9-1 三处修订文本与复用有效性依据——通过。
- **来源清单完整性**：两仓 6 工作树 + 官方 checkout 全量枚举无遗漏，7 项强制字段逐行在位，配套工作树无论引用与否均已记录，承重 diff 声明独立复跑证实——通过。
- **confirm = true**：r10 通过结论（APPROVED，0 阻塞 0 建议）经定向复核维持。本复核仅覆盖任务指定的两项，不构成对方案整体或其他评审维度的重新裁定；实施仍被 OD-1/OD-3/E-9/E-10/E-12 门住，评审通过 ≠ 实施授权。

## 复核员交接块

```
meta_review: r10
target_report: docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r10.md
target_verdict: APPROVED (0 blockers, 0 advisories)
plan_sha256: 025bf218395667bf9f80c8152d0045e4865a4c84636f187f6bfdc86a9c549524
report_path: docs/2026-09-26-codex-remote-disconnect-resilience-plan-review-r10-meta.md
scope: limited:锚点抽查 + 来源清单完整性
confirm: true
issues: 0
anchors_checked: 12（官方 2 / MacBridge 4 / 方案正文 4 / iOS 2）
sources_enumerated: 7（MacBridge 3 树 + iOS 3 树 + openai/codex checkout，全部亲跑核对）
```
