# codex-remote 断线韧性与恢复专项方案评审报告（r4）

- 日期：2026-09-27
- verdict：**REVISION_REQUIRED**
- 评审对象：`docs/2026-09-26-codex-remote-disconnect-resilience-plan.md`（v1.3）
- 方案 SHA-256：`811ee4526abc6db0f9af44eeda96b19132fe419559c71dd24cf0463903d23208`
- 范围：全量独立复核；先按现版方案和当前配套源码形成风险清单，再把 r1/r2/r3 与元审核作为回归清单
- 结果：**4 个阻塞，2 个建议**
- 专项：`audit-plan` 已接入；本轮重新读取 attempt-008 归档样本，但它只保留方法名、计数和 cursor 是否存在，不含真实 `seq_id` / `segment_id` chunk 帧，因此相关 wire 形状专项未通过

## 1. 核心结论

方案的大部分源码事实仍然成立，但主链的三个恢复切片没有闭合到可执行语义：S-2 不能用现有 Kernel admission 同时做到“强制对账”和“保留既有投影”；S-3 无法从 transport 缺口确定真正丢失事件所属 thread，却把“触发受影响会话对账”写成已解决；S-7 没有可判定的 host 接受信号，因此“恢复逐信封 ack”和“sentinel 上界 1 条/T”不能同时由当前状态机保证。另有 chunk wire 关键字段/顺序只有源码与协议注释，没有本轮可复核的真实帧样本。

因此旧 r3 的 APPROVED 不再可作为当前实施依据。不是锚点整体失效，而是旧评审漏掉了 admission、归属和 half-open 状态机三条跨组件约束。

### implementation_readiness

- 当前没有切片可因本次评审获得新增实施授权。
- 原有门继续保留：OD-1、OD-3、E-9、E-10。
- 新增关闭门：S-2 在 F-R4-1 闭合前不可实施；S-3 在 F-R4-2/F-R4-4 闭合前不可实施；S-7 在 F-R4-3/F-R4-4 闭合前不可实施。
- 本轮只写本报告；未修改方案、产品代码、测试、部署或真机状态。

## 2. 来源与覆盖

### 2.1 本轮来源清单（读取源码前与写报告前均重新生成）

| 仓库 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期来源 / 配套组合 | 预期产品特性 |
| --- | --- | --- | --- | --- | --- | --- |
| cordcode-macbridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `5d1c9ae648f15dface4717d97f674d71756b01e5` | 读取前干净；写入后仅新增本报告 | 当前任务工作树；配套 iOS 为同名功能分支 | codex-remote 断线恢复；不引入第二 writer/fallback |
| cordcode-ios | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `bb6f4584e93e53170862e6bac17615062e2fa55f` | 用户/其他代理已有修改：`OpenCodeiOS/OpenCodeiOSTests/NativeTimelineRowSplitTests.swift`、`OpenCodeiOS/Packages/MarkdownView/PATCHES.md`、`OpenCodeiOS/Packages/MarkdownView/Sources/MarkdownView/Supplements+Extension/UIFont+Extension.swift`；均与本评审锚点不重叠 | 与 Mac 同名功能分支唯一配套 | iOS projection retry 行为保持单一投影 writer |
| openai/codex（上游只读） | `/Users/jacklee/Projects/codex` | `main`，HEAD=`FETCH_HEAD` | `e72da2b53805894878023d01949a25a082e0a5cb` | 干净 | 方案指定的官方源码身份 | Remote Control host/transport 语义 |

当前 Mac 功能分支另有 main 与 detached 评审树；iOS 亦有 main 与 detached 评审树。本轮按 P0 分支族门选取同名功能分支组合，没有沿用方案 §2.1 的历史 main 核验组合。Mac 相关代码从方案记录的 `07721783` 到当前提交无改动；iOS 四个核心锚点中只有 `ChatViewModel.swift` 有后续本地发送 paint-fence 改动，与 disaster retry loop 不重叠。

### 2.2 检查范围

- 亲核：R-1~R-5、S-1~S-7、E-1~E-12、OD-1~OD-3、切片依赖和完成断言。
- 亲核承重源码：MacBridge `stream.go` / `envelope.go` / `session.go` / `codec.go` / `history_paginated.go`；go-bridge `handlers_projection.go` / `projection_kernel.go` / `projection_window_older_hydrate.go` / `main.go`；iOS `ProjectionStore.swift` / `ChatViewModel.swift` / `CCCodeBridgeModels.swift`；上游 `protocol.rs` / `websocket.rs` / `segment.rs`。
- 活文档：Mac `CLAUDE.md`、`GO_BRIDGE_ARCHITECTURE.md`、`think.md`；配套 iOS `CLAUDE.md`、`IOS_MAC_INTERACTION_FLOW.md`、`think.md`。
- 未运行 build/test/UI/设备流程：这是 D0 文档评审，且没有代码修改。

## 3. 阻塞意见

### F-R4-1 [阻塞] S-2 复用现有 hydrate API 时，无法同时强制 READY 会话对账并保留既有投影

- 位置：§3.2、§5 S-2，尤其“复用 `ReadColdHistory` desc 首页，经既有 `BeginHydrateTransaction` / `CommitHydrateTransaction` 提交”。
- 证据：`projection_kernel.go:1006-1026` 对 READY pathless 会话在 `sourceChanged=false` 时直接返回 `AlreadyReady`，对账成为 no-op；要强制进入 transaction 只能令 `sourceChanged=true`，但 `:1059-1083` 的新 transaction 从空 reducer 开始，且只有 `!sourceChanged` 才 restore 既有快照。codex-remote 的 `ReadColdHistory` 明确只返回最新一页（`history_paginated.go:856-923`），旧页由 producer/window 状态另行维护。按当前方案强制重建会用最新一页覆盖投影，已经 prepend 的旧 turns 可能丢失；不强制则根本不对账。
- 影响：R-2 的“断线窗口完成 turn 自动收口”没有一条能按现有 API 落地且不破坏完整时间线的路径；“复用既有事务域”这一核心可行性声明未成立。
- 最小修订方向：明确新增/复用哪一个 **READY-safe reconciliation admission**。它必须以当前 committed projection 为 baseline，只应用权威 turn 终态更新，并保留 older-window turns、producer cursor、goal/detail manifest 和 pending live fence；不要用 `sourceChanged=true` 的 page-1 rebuild 冒充增量对账。若选择专用 reconcile transaction，列出 owner、锁/fence、输入事件和 commit API。
- 闭合标准：方案给出一条可指名的 admission/commit 路径；测试至少覆盖 READY+已 prepend 旧页+断线完成 turn，断言 terminal 收口、旧 turns/producer state 不丢、并发 live delta 在同一 fence 后收敛。

### F-R4-2 [阻塞] S-3 的 gap 无法归属真正丢失的 thread，现有触发范围不能满足 R-4

- 位置：§3.3 “记录缺口范围与关联 thread；缺口触发 S-2 对账”，§3.2 对账集合 `observed ∩ turnByThread 非空`。
- 证据：transport `Envelope` 只有 client/env/stream/seq 与 payload（`envelope.go:32-48`），缺失的 seq 对应 payload 已经不存在，因此无法从“缺口后的首帧”推断丢失事件所属 thread。下一帧可能属于另一个 thread，chunk 帧在完整重组前甚至没有可解析 JSON-RPC payload。R2-A1 已指出这一点，但现版仍未修订。
- 影响：若 thread A 的 `turn/completed` 丢失、下一帧属于 thread B，按“关联 thread”实现会对账 B 而遗漏 A；日志能看到 gap，但 R-4“缺口触发受影响会话的权威对账”和 R-2 自动收口仍失败。这是正确性缺口，不只是措辞精度。
- 最小修订方向：把 gap 定义为 stream 级未知归属事件，并明确有界 fan-out，例如对账该 stream 上全部 `observed ∩ turnByThread 非空`；同时说明无在飞 turn 的 thread 只记日志还是另有权威校准。补充 gap 后游标推进，避免一次缺口反复触发。
- 闭合标准：测试注入“A 的 completion 丢失，随后收到 B 事件”，断言 A 被纳入对账；另测 gap 后高水位推进且只触发一次。

### F-R4-3 [阻塞] S-7 没有可观察的 host 接受信号，恢复与 1 条/T 噪声上界均不可判定

- 位置：§3.7 “transport 接受 → 恢复正常逐信封 ack；仍拒绝 → 重新 armed”及“sentinel 通知率上界 1 条/T”。
- 证据：现有注释明确 relay ACK 只证明 relay 收帧，不能证明 app-server 接受（`stream.go:44-47`）；client ack 本身没有 host 成功响应。sentinel 是异步的 server message。方案的完成断言只测补发与 sentinel 再现，没有定义 half-open 状态、观察窗口或解除条件。若 probe write 成功后立即解除，sentinel 到达前的每个入站帧都会恢复逐帧 ack，无法证明 1 条/T；若始终保持 disabled，又无法满足“恢复正常逐信封 ack”。
- 影响：实现者必须自行发明状态机；一种实现会重开 750/s 风暴，另一种会永远只做低频 probe，均可能通过现有断言。
- 最小修订方向：给出明确的 closed/open/half-open 状态机和唯一解除证据，并重新证明噪声上界。若协议没有可靠正向确认，删除“transport 接受”的伪事实，改成能被真实输入观测和测试的保守策略；同时说明 probe 期间普通入站帧如何处理。
- 闭合标准：fake conn/fake clock 测试覆盖 probe 后 sentinel 延迟到达、probe 后普通帧先到、连续积压帧三种交错；断言不会在观察窗内恢复 ack 风暴，且满足文档声称的恢复条件与通知上界。

### F-R4-4 [阻塞] S-3/S-7 的关键 wire 形状没有真实 chunk 样本，audit-plan 专项未闭合

- 位置：E-11/E-12、§3.3 `(seq_id, segment_id)` 游标、chunk ack 清除链。
- 证据：本轮重新读取 `agent/codex-remote/testdata/phase0/live/attempt-008-thread-resume-live-turn-stream.json`。该文件的 `metadata.redaction_procedure` 明确只保留方法名、计数、字段集合和 routing 比较；实际 observations 只有 `envelope_cursor_present=false`，没有任何真实 `server_message_chunk`、`seq_id`、`segment_id` 或 ack 帧。`phase2/thread-read-remote-envelope.json` 是 fixture，且使用 camelCase，不是可替代 live wire 的证据。官方 Rust 源码足以支持实现假设，但按 audit-plan 不能替代外部 wire 的实际字段路径/空缺/顺序样本。
- 影响：当前方案把 E-12 标为 verified，并允许 S-3/S-7 直接实施；若部署版本序列化形状、segment 起始值或缺省行为与 checkout 不一致，去重/ack 会在恢复路径静默丢数据。
- 最小修订方向：新增版本锚定、脱敏的真实 controller 捕获，至少包含 plain frame、同一消息的两个连续 chunk、对应 chunk ack，以及 segment 字段缺失/不适用的 plain 负例；用两种独立提取策略核对字段路径和 `(seq,segment)` 序列。若当前授权不允许捕获，把 E-12 改为 pending，并门住 S-3/S-7，而不是写 verified。
- 闭合标准：样本索引记录捕获版本/时间/脱敏规则并展示上述帧；或方案明确降级为 pending gate，实施不可绕过。

## 4. 建议意见

### R4-A1 [建议] S-1b 声称尊重 `retryAfterMillis`，但数据模型和测试计划都把它丢掉

- `CCCodeBridgeError` 已解码 `retryAfterMillis`，但 `ProjectionPullOutcome.retryable` 只有 code/message（`ProjectionStore.swift:61-63`），两个 catch 路径也只传这两项；`startProjectionPullLoop` 始终按本地 1s→30s 算法等待（`ChatViewModel.swift:455-493`）。
- §3.1 要求“尊重 `retryAfterMillis` 作为初始等待”，§5 测试却只断言 flag true/false。
- 建议把 retry delay 明确穿过 outcome/loop，定义与本地 backoff 取 max/min 的规则，并加 server delay 生效的 fake-clock 断言；否则删掉该承诺。

### R4-A2 [建议] 方案来源表已成为历史快照，实施前必须更新为当前配套分支身份

- 方案 §2.1 仍记录 Mac `07721783`、iOS main `fe421cdc` 和配套 iOS `8eb262da`；当前实际组合是 Mac `5d1c9ae…` + iOS `bb6f458…`，iOS 还有三项不重叠的未提交修改。
- 相关承重 Mac 代码未变化，iOS retry 锚点也未被后续 paint-fence 改动改变，所以这不是本轮事实推翻；但按项目 P0 门，不能把旧表当第一次修改前的来源清单。
- 建议下一版把 §2.1 明确标成“设计证据历史快照”，并新增当前实施来源门模板；实施者仍须现场重跑，不能复制本文哈希。

## 5. 历轮意见处置

| 历轮项 | 本轮状态 |
| --- | --- |
| r1 F-1~F-6 | 原修订文本仍在；本轮不重新打开其已闭合的局部问题 |
| R2-A1 gap 游标推进/thread 归属 | **升级为阻塞 F-R4-2**：它直接破坏 R-4，而非仅规格精度 |
| R2-A2 S-7 依赖缺 per-stream 游标 | 仍未闭合；被 F-R4-3 的状态机问题覆盖，修订时一并补依赖 |
| R2-A3 S-7 缺解除断言 | **升级为阻塞 F-R4-3**：不存在可直接观察的“transport 接受”事实 |
| R2-A4 行号精度 | 仍是建议级，不影响本轮结论 |
| r3 APPROVED | 因 F-R4-1~F-R4-4 的新反证暂停有效；其源码锚点核验可继续作为历史证据，不再支撑“可实施”结论 |

## 6. 修订后复审清单

1. S-2 明确 READY-safe reconcile transaction，并证明不丢 older windows/producer state。
2. S-3 将 gap 归属改为诚实的 stream 级范围，补 fan-out 与游标推进测试。
3. S-7 写出可测的 breaker 状态机，删除不可观察的“transport 接受”假设或补真实确认机制。
4. E-12 补真实 chunk/ack 样本，或降为 pending 严格门住 S-3/S-7。
5. S-1b 补 `retryAfterMillis` 数据通路和 fake-clock 验收。
6. 更新来源快照后做定向复审；不需要重跑 UI/snapshot/XCUITest。
