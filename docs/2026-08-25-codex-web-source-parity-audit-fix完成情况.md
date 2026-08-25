# codex-web 源码对齐审计修复完成情况

- 日期：2026-08-25 起
- 驱动：owner 监工指令（codex-web 源码对齐审计修复）
- 审计文档（豁免卡登记簿 + 事实基准）：[2026-08-25-codex-web-source-parity-audit.md](2026-08-25-codex-web-source-parity-audit.md)
- 纪律：每个修复先在本文档写明「官方实现位置 + 我方实现的第一处分歧」再动代码（设计 §3.4）；代码注释带上游锚点或豁免卡编号；每批定向测试 + go test ./... + go vet 通过后进下一批。

## 批次 1：收口对称性与小修

### 1-首笔 A1/B1：InteractionRegistry 移植声明 + history 有界化 + 不变量测试

- **官方实现位置**：`tui/src/app/app_server_requests.rs:74-360`（PendingAppServerRequests：分类型 HashMap :74-80、会话重置 clear() :89、note_server_request :97、take_resolution :201、resolve_notification :305）+ 收口唯一路径 `ServerRequestResolved`（`app_server_events.rs:118-142`）。
- **我方实现的第一处分歧**：官方是**单连接单视角**——resolve_notification 一次 dismiss、会话重置 clear() 整体清空；本仓是**长驻 bridge + 双泵（主/观察连接）两视角**——每泵须各发一次收口事件（kernel 幂等）、`resolvedByRequest` 为晚到第二泵保留归属、`DropEpoch` 承担官方 clear() 的断线等价物。次级分歧：官方 TUI 进程生命周期短且 clear() 兜底，本仓进程长驻而 `history` map 只增不减（进程生命周期无界）。
- **处置**：
  1. interactions.go 文件头补移植母本声明 + 上述差异清单；
  2. `history` 有界化（与 `resolvedByRequest` 同策略：1024 全清）；
  3. clear() 语义对照审查落注释（DropEpoch = 断线版 clear；官方重发是重 surface 的唯一真相，重连后 Register 刷新 epoch）；
  4. 不变量测试补齐（B1 卡待补项）：permission kind 每 epoch 恰一次、第二泵经 resolvedByRequest 归属发自己那份、DropEpoch 后死 epoch 无 pending 可归属不产出；
  5. resolvedEvents/registry 注释补 B1 豁免卡引用。

### 1a A2：permission 乐观收口清算

- **官方实现位置**：TUI 收口唯一路径 = `ServerRequestResolved` 通知（`app_server_events.rs:118-142`）；发送应答后不本地关闭。
- **我方实现的第一处分歧**：`go-bridge/handlers.go` `handleResolvePermission`（:4083-4096）在 RespondPermission 成功后**立即乐观 publish `permission_resolved`**。git 取证（`git log -L`）：该块由 **630fb8d（fix(dsh-web): 审批/问答同步到 iPhone）** 引入——它是 **dsh-web 的产品缺口修复**（dsh 宿主无官方 resolved 广播到达 SSV2 投影），不是 codex-web 发明；但 handler 为多 backend 共享，codex-web 会连带吃到本地乐观收口，违反「官方 resolved 是唯一收口真相」。
- **复现与修复设计**：codex-web 的官方路径与 user_input 完全同构（202b41c 后 `interactions.go resolvedEvents` 对 permission kind 双泵产出 `EventPermissionResolved` → `go-bridge/events.go:197` 映射为同名 `permission_resolved` logical event → reducer 收卡，与乐观 publish 产物同构）。复现测试将证明：跳过乐观 publish 后，仅靠官方 per-pump 事件投影卡即收口。
- **处置**：引入 `core.OfficialResolutionSource` 标记接口（codex-web Agent/agentSession 实现）——handler 对标记 backend 不做本地乐观收口；dsh-web/opencode-web 等无官方广播的 backend 保留原行为并补声明注释。若真机复测发现官方路径覆盖不了某场景，按 B 类登记豁免卡（不允许无声明保留）。
- **验收**：允许/拒绝后卡片立即收口（owner 真机）；测试断言收口事件源 = serverRequest/resolved 驱动（非 handler 本地 publish）。

### 1c C2：plan 合成终态

- **官方实现位置**：官方 wire 的 plan item 仅携带 text（Phase 0 catalog/interaction 样本，`protocol/v2/item.rs` plan variant 无 status 字段）；turn 级 `turn.status` 是官方唯一终态来源（`protocol/v2/thread.rs` Turn status；设计 §9.2 封口只认官方 turn status）。
- **我方实现的第一处分歧**：`history.go` `mapHistoryItem` plan 分支无条件合成 `status:"completed"`——turn 处于 interrupted/failed/inProgress 时读取历史也会伪造终态，违反 §9.2「不本地猜完成」。
- **处置**：plan 卡 status 改为从官方 `ht.Status` 推导（turn completed → completed，否则 unknown）；system note 合成 ID `turnID+":"+note` 保留并补碰撞不可能性注释（官方 turn id 全局唯一 × note 枚举单 turn 内唯一）；文案前缀「官方 turn 失败：」保留（产品文案非事实编造）。单测覆盖三态映射。

### 1d B5：turn/completed 缺 turn.id 归属

- **官方实现位置**：`app-server-protocol/src/protocol/v2/thread_data.rs:352`——`Turn.id: String` 为必有字段（非 Option、无 serde default）；`TurnCompletedNotification { thread_id, turn: Turn }`（turn.rs:407-410）随帧必带 turn.id。
- **我方实现的第一处分歧**：`codec.go` turn/completed 处理在 `p.Turn.ID == ""` 时静默回退 `ActiveTurn(threadID)` 归属——wire 契约异常（官方不可能发空 id）被本地推断掩盖。
- **处置**（官方必有 → 诊断 + 丢弃）：空 turn.id 记 warn 诊断并丢弃该帧（不静默归属）；测试断言 ActiveTurn 存在时空 id 帧仍零产出。

## 批次 1 完成（2026-08-25）

- 提交链：`3d84b28`（A1/B1 结构项 + 不变量测试）→ `e319ea5`（A2 官方收口唯一真相）→ 本笔（C2 + B5）。
- 门验证：`go test ./... -count=1` 全绿（0 失败）、`go vet ./...` 干净；定向：interactions 不变量 4 用例、permission closure 3 用例、plan 状态映射 4 态、B5 丢弃用例均 PASS。
- 真机验收（允许/拒绝后卡片立即收口）保留 owner，未代填。

## 批次 2：A3 官方算法回迁

### 2a A3：steer/interrupt 失配 resync-retry

- **官方实现位置**：解析 `tui/src/app.rs:643-703`——steer 失配消息 ``expected active turn id `X` but found `Y` ``（反引号包裹，:659-674）、`no active turn to steer` Missing 分支（:656-657）、interrupt 失配 `expected active turn id X but found Y`（无反引号，:676-692）；重试语义 `tui/src/app/thread_routing.rs:604-627`（interrupt attempt 0 失配 → 重同步 `actual` 后 continue 重试一次）与 `:683-727`（steer Missing → 清本地观测转 should_start_turn；ExpectedTurnMismatch 且未重试过且 actual ≠ 本地 id → 重同步重试一次）。
- **我方实现的第一处分歧**：`events.go` `currentTurnForControl`/`CancelTurnForThread`/`Steer` 对官方 -32600 失配一律「原样透传，不重试伪装」（注释明示）——官方**有**错误驱动的 resync-retry 算法，本仓选择了相反行为且未声明。
- **处置**：移植两解析器 + 重试一次语义；三源观测顺序保留（liveCodec > 本端 start 返回 > 冷基线，冷基线仍为最后手段，豁免卡 §3.2-B2）；失配 resync 作为权威纠正（actual id 来自服务器错误）。Steer Missing 分支按官方 should_start_turn 语义转普通 Send。
- **验收**：两种失配消息解析 + 重试单测；正常路径不再因过期 local id 报 -32600。

## 批次 2 完成（2026-08-25）

- 提交：见 git log（A3 resync-retry 回迁 + B2 卡登记状态回写）。
- 门验证：`go test ./... -count=1` 全绿、`go vet ./...` 干净；定向 7 用例（解析 2 + interrupt 重试成功/持续失败 2 + steer 重试 1 + Missing 转 Send 1）均 PASS。
- 修复过程事故：初版重试路径 `return TurnInterrupt(...)` 直接返回 nil `*RPCError` → typed-nil error 接口陷阱（err != nil 但打印 <nil>），测试捕获后改为显式判空返回——无生产影响（新代码首次提交前被测试拦下）。
- 真机验收（iOS 停止 Mac 发起 turn 不再因过期 local id 报 -32600）保留 owner。

## 批次 3 完成（代码加固 2026-08-25；§0.3 修订案停等 owner 批准）

### 3a C1：冷用量加固（owner 裁决「保留并加固 + 设计修订」）

- **官方实现位置**：官方确无冷用量读取 RPC（live 仅 `thread/tokenUsage/updated` 通知；官方冷用量由 server 侧 resume 自行解析 rollout）。rollout 记录结构 pin 源码：`protocol/src/protocol.rs:2094-2100`（TokenUsageInfo，model_context_window 为 Option）、`:2072-2088`（TokenUsage 字段族）、`:2160-2164`（TokenCountEvent{info: Option}）、`:1346`（EventMsg::TokenCount）、`history/src/rollout_payload.rs:49-51`（RolloutItemWire::EventMsg 行形状）。
- **我方实现的第一处分歧**：`context_usage.go` 直接 tail 解析 rollout JSONL，形状不吻合时静默回退 cache（违反 §0.3 数据面红线与 §1「禁止静默 fallback」），且无版本门控、无可见性标注。
- **处置（三层加固）**：
  1. 契约 fixture：真实脱敏样本落盘 `agent/codex-web/testdata/official-0.149.0-alpha.4/dumps/usage/rollout-tail.jsonl`（+ README 溯源锚点）；运行时最新一条 token_count 与 fixture 形状不符（Info null / model_context_window null·≤0 / 负用量）→ 弃用文件路径 + warn 诊断，不静默回退；
  2. 版本门控：`persistedUsageVerifiedCLIFamilies`（当前 0.149.x 族）——initialize 记录的 CLI 版本族外不走文件路径（Info 诊断）；
  3. 可见性：descriptor `StaticCapabilities` 标注 `usage-source: rollout-tail-experimental`；解析失败/门控跳过均打日志。
- **门验证**：`go test ./... -count=1` 全绿、`go vet ./...` 干净；定向 4 用例（fixture 读取/版本门控跳过/形状不吻合弃用/版本族单测）PASS。

### 3b 设计文档 §0.3 修订案（草案——停等 owner 批准，批准后回写设计文档）

> **§0.3 修订建议（新增小节「记录在案的豁免：rollout 尾部冷用量」）**：
> codex-web 的冷用量（已加载 thread 的当前 context 占用）读取，在官方无对应 RPC 的前提下，允许唯一一条受控文件路径：仅打开官方 `thread/read` 返回的 `Thread.path`，tail 读取 8MB 内最新的 `event_msg/token_count` 记录。该路径登记为记录在案的豁免，约束：(1) 契约 fixture 冻结形状，不吻合即弃用并打诊断，不静默回退；(2) CLI 版本门控（已验证版本族外不走文件路径）；(3) descriptor 与日志显式标注 `usage-source: rollout-tail-experimental`；(4) 该路径只读、不做 session 发现或第二目录；(5) 官方提供冷用量 RPC 后立即退役本路径。除本豁免外，§0.3「官方 API 唯一数据面」红线对其余全部用量事实仍然生效。

**状态：本段落为草案，待 owner 批准后回写设计文档 §0.3；代码加固已按裁决先行提交。**

## 批次 4：B3/B4（待填写）

## 批次 4 完成（2026-08-25）

### 4a B3：isConnectionLoss 结构化分类

- **官方实现位置**：官方无重连（`Disconnected`→`FatalExitRequest` 直接退出）——豁免区域；本卡登记 bridge 自愈发明。
- **我方实现的第一处分歧**：连接死亡判定依赖错误文案匹配（"broken pipe"/"websocket: close" 等七种子串），无类型化分类。
- **处置**：transport 层新增 `TransportConnectionError`（gorilla `CloseError` → `ws-close` 携带官方 close code；读写网络错误 → `ws-io`），`wsTransport.Send/Recv` 在源头包装；`isConnectionLoss` 分类优先级 = 类型化（errors.As 经 %w 链）→ syscall/net 结构化 → 文案兜底（命中打 Warn 诊断标记"未被类型化的错误源"）。退避参数（2s×2→60s）与 §8.3 冷校准顺序登记入审计文档 B3 卡。
- **门验证**：分类单测（类型化直连/经包装链/CloseError code 提取/syscall/兜底/RPC 拒绝不误判）PASS；既有死 socket 回归保持。

### 4b B4：willRetry 呈现核对 + 豁免卡登记

- **官方实现位置**：`tui/src/chatwidget/protocol.rs:127-143`——`will_retry=true` → `on_stream_error` 瞬态行（每帧独立渲染、不中断 turn）；`false` → `last_non_retry_error` + 终态处理；`app-server-protocol/src/protocol/v2/notification.rs:54-56` 语义注释。官方**无 attempt 计数**。
- **核对结论**：我方呈现本体已对齐（EventRetryStatus 瞬态行不落终态 / EventError 官方原文）；唯一发明 = `RetryAttempt` 连续计数（iOS「第 N 次」UX 信号）。按裁决登记豁免卡（审计文档 §3.2-B4：不变量/失败模式/回归测试齐备），代码注释引用。

## 批次 5：流程项（待填写）

## 批次 6：iOS 抽查（待填写）
