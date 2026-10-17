# 评审报告 R1：Claude 长 session 打开架构优化方案

日期：2026-10-17
评审员：plan-reviewer（fresh 模式，无历轮意见输入）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`
方案 SHA-256（评审时实测）：`35fb3d333e220deb6f4fc8e0fc53ad7927c72557879dc97b0214c28cbf04fbfb`（与派发记录一致）
范围：full（全量评审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **2**（F-1、F-2）
- advisories: **7**（F-3 … F-9）
- implementation_readiness：方案整体架构方向、来源纪律、证据门结构与 Codex 复用映射经亲核成立；两个阻塞缺陷修订前，S-4（preview cache）与 R-1 验收行不可进入实施/验收，其余切片的前置门见 §5。

总体评价：这是一份高质量方案——来源身份三仓齐全且与实测一致；全部 Mac/iOS/Codex 锚点亲核 20/21 命中（1 处路径不完整）；audit-plan 专项通过（所有外部内容形状声明均有真实样本支持，关键数字全部实测吻合）；pending/verified 状态诚实（E-3 根因 pending 正确阻断 S-1 完成宣告）；OD-1/2/3 保持 pending 未替 owner 决定。阻塞缺陷集中在 preview cache 的 epoch 语义缺口与 R-1 验收措辞与冻结 v1 语义的偏差，均为局部修订，不动摇架构方向。

## 2. 来源与覆盖

### 2.1 来源身份（本轮独立复核，与方案 §2.1 逐项一致）

| 来源 | 实测身份 | 方案声明 |
| --- | --- | --- |
| MacBridge | `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82` @ `feat/ios-native-message-timeline`；仅本方案文档未跟踪 | ✓ 一致 |
| iOS | `1e895ea9ef42bc83c287c3acd66c3d7616340b4a` @ `feat/ios-native-message-timeline`；4 个 NativeTimeline 未提交文件（LiveTailFollowController / NativeTimelineViewController / TimelineStreamingMarkdownCell / 未跟踪 StreamingHeightPresentation.swift） | ✓ 一致（未触碰） |
| Codex 上游 | `ce0323f2f2d3b25e82806cd555237da3db4e1897` @ `main`，干净 | ✓ 一致 |
| 正式 runtime | PID 8303，`/Applications/CordCodeLink.app/.../cordcode-bridge-runtime` 监听 8777；二进制含 `get_session_projection_window`（2 处）与 `projection_window_v1` 符号 | ✓ 一致（「runtime 含能力符号」成立） |

复核命令：各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`strings …/cordcode-bridge-runtime | grep -c get_session_projection_window` → `2`。

### 2.2 事故样本实测（audit-plan：内容形状声明逐一 dump 验证）

| 方案声明 | 本轮实测 | 结果 |
| --- | --- | --- |
| transcript 约 97MB | 101,520,809 B = 96.8 MiB；24,736 行；type 分布 assistant 6371 / attachment 4433 / user 4132 … | ✓（MiB 口径） |
| full projection 约 34MB；checkpoint 约 34.5MB | checkpoint 34,537,988 B；turn JSON 总和 34.43MB | ✓ |
| 119 turns | `len(projection.turns) == 119` | ✓ |
| 尾部 17 turns 约 3.79MB | 尾 17 turns JSON = 3.80MB；按 4MiB 从尾累积恰好 17 turns | ✓ |
| gzip level 1 ≈11.2MB / level 6 ≈9.6MB | 实测 11.1MB / 9.6MB | ✓（0.1MB 序列化差异） |
| Relay gzip level 6 后 ≈1.08MB | 尾 17 turns 页 gzip6 = 1.08MB | ✓ 精确 |
| Mac 冷 hydrate ≈1.794s | log `projection_shadow stage=hydrate_commit … elapsedMillis=1794`（17:30:56.861） | ✓ |
| 事故只有 `get_session_projection sinceRev=0`、无 window RPC | 全日志 `get_session_projection_window` 计数 = **0**；req_18/req_33 均 `sinceRev=0 outcome=snapshot` | ✓（E-2 verified 成立） |
| 无 hydrate_failed | 全日志计数 = 0 | ✓（E-1 verified 成立） |
| 时间线 | req_18 enqueue 17:30:56.864 → iOS 15s cap ≈17:31:12 → 截图 17:31:18 → 重试 req_33 17:31:36 | ✓ 自洽 |

dump 脚本：python3 逐行 `json.loads` 计数 + `json.dumps` 字节测量 + `gzip.GzipFile` 双级别压缩（单策略充分：均为顶层直接测量，无嵌套抽取歧义）。

**样本补充事实（方案未写、对评审重点 1/7 有用）**：该样本最大单 turn 序列化后 **6.35MB > 4MiB**，且不在尾部 17 turns 内。见 F-2。

### 2.3 锚点核验表（本轮全部亲核）

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 | 
| --- | --- | --- |
| Mac `go-bridge/main.go:560` SetProjectionWindowEnabled(true) | main.go:560 `server.SetProjectionWindowEnabled(true)`；上方注释「the iOS replica (S4C) and the producer (S4B) have shipped behind this flag」 | ✓ 精确 / ✓ 支持 |
| `projection_window.go:24` 256 turns / 4MiB / 默认 100 | :27-31 `maxWindowTurns = 256`、`maxWindowEncodedBytes = 4 << 20`、`defaultWindowTurns = 100`；:24-26 注释「a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn」 | ✓ / ✓（超大 turn 语义见 F-2） |
| `projection_window_handler.go:32` capability gate | :32 注释行「Frozen gate: only connections that negotiated projection_window_v1 (hello echo + SetConnProjectionWindowV1) may call this RPC」；实际判断 :36 `if !h.eventPublisher.ConnProjectionWindowV1(conn)` | ✓（注释块起点，±2）/ ✓ |
| `projection_kernel.go:280` checkpoint | :281 `func (s *ProjectionCheckpointStore) LoadValidated(`；:318-324 composite 校验 `if stored.Identity != segment.Identity || stored.Cursor != segment.Cursor { return … "composite source cut mismatch" }` | ✓（±1）/ ✓「append 后易整体重建」有源码证据：cursor 精确相等才有效 |
| `projection_kernel.go:1123` | :1123 `checkpointHit := false`；hydrate 的 checkpoint 恢复分支起点 | ✓ 精确 / ✓ |
| `docs/protocol/relay-frame-compression.md:3` | :3「It does not change direct WebSocket traffic」 | ✓ 精确 / ✓ |
| `relay_conn.go:296` | :296 `contentEncoding := ""` 紧邻 :297 `if outboundGzip && len(plaintext) >= relayGzipThreshold`；:91 `relayGzipThreshold = 32 * 1024` | ✓（±1）/ ✓「≥32KiB 且压缩更小才启用」（:301-303） |
| iOS `ProjectionStore.swift:590` window replica | :590 `func windowCoverage(for sessionId: String)`，PERF-S4C 区；apply/pull/coverage 均在 | ✓ 精确 / ✓ |
| `ChatViewModel+SessionManagement.swift:851` | :851 `let outcome = await self.projectionStore.pullOpenWindowed(sessionId: sessionId)`，上方注释「capability-gated windowed open (S4C)」 | ✓ 精确 / ✓ |
| `ProjectionStore.swift:145` hard cap | :145 hard cap doc 注释首行；:152 `var pullHardCapNanoseconds: UInt64 = 15_000_000_000`；注释确认「in-flight RPC is not cancelled — a late success still applies」 | ✓（±7，注释块）/ ✓ |
| `MessageCacheManager.swift:6` | :6 `actor MessageCacheManager`，LRU `maxSessionCount = 20` | ✓ 精确 / ✓ |
| `Storage/SessionSnapshotStore.swift:101` | :101 `let maxSnapshotBytes: Int`；:112 默认 `10 * 1024 * 1024` | ✓ 精确 / ✓「10MB 上限不适合长 session」成立（34MB > 10MiB） |
| `ChatViewModel+MessageSync.swift:98` | :98 为 `loadMessages` 签名参数行；:103 起注释「§10.5.3 ring 2 step 1 — hard gate (no trigger whitelist…)」 | ✓（函数入口）/ ✓ |
| `Bridge/ProjectionWireDecoder.swift:9` | :9-17：`JSONSerialization.jsonObject` 全量 parse → 取 `data` → `JSONSerialization.data` re-encode → `JSONDecoder` decode | ✓ 精确 / ✓ |
| （§3.5 五步 decode 现状） | ① envelope `data: BridgeAnyCodable?`（CCCodeBridgeTransport.swift:39/51）② trace re-encode：handleResult → `inboundEnvelopeJSON`（:1845-1850 `JSONEncoder().encode(envelope)` + `JSONSerialization.jsonObject`）③④⑤ WireDecoder.swift:11-17 | ✓ 全部成立 |
| Codex `state/thread_history_migrations/0001_thread_history.sql:1` | :1 `CREATE TABLE thread_turns (`；thread_turns/thread_items 列结构与方案 §3.2.2 projection_turns/items 镜像（rollout_ordinal/status/duration_ms/first_user_item_id/final_agent_item_id/item_json） | ✓ 精确 / ✓ |
| `thread-store/src/local/thread_history_materialization.rs:42` | :42-45 `let projection_state = …state.next_byte_offset` → `start_offset` | ✓ 精确 / ✓「只处理 suffix」 |
| `app-server-protocol/src/protocol/v2/thread.rs:422` | :422 `exclude_turns` doc 注释内（字段 :427；`initial_turns_page` :430-434） | ✓（注释块内）/ ✓ |
| `app-server/src/request_processors/thread_processor.rs:5888` | :5887-5894 `THREAD_TURNS_DEFAULT_LIMIT=25 / MAX=100 / THREAD_ITEMS_*` | ✓（±1）/ ✓ |
| `tui/src/app_server_session/history.rs:28` | :28 `pub(crate) const INITIAL_HISTORY_TURN_LIMIT: u32 = 5;`；:29 `HISTORY_ITEM_PAGE_LIMIT: u32 = 100` | ✓ 精确 / ✓ |
| `core/src/config/mod.rs:4050` | :4050 `let thread_unload_delay =`（默认 1800s） | ✓ 精确 / ✓ |
| `app-server/src/request_processors/thread_lifecycle.rs:18` | :18 `pub(super) thread_unload_delay: Duration,` | ✓ / ✓ |
| `thread-store/src/local/model_context.rs:30` | :30 doc「Paginated JSONL rollouts use a reverse scan. It stops at the newest `CompactedItem`…」 | ✓ 精确 / ✓ |
| `cloud-client/README.md:16` | 实际路径 **`codex-rs/cloud-client/README.md`**；`Attach` 声明在 **:18**：「`Attach` has no history snapshot or replay guarantee.」 | 🟡 路径缺 `codex-rs/` 前缀（仓根不解析）；定位 ±2；语义 ✓（见 F-7） |

附加亲核（方案未直接引用、支撑评审结论）：

- `go-bridge/server.go:375-394` `negotiateProjectionWindowV1`：capability echo 要求客户端 hello 同时声明 `projection_window_v1` + `session_sync_v2` 且服务端 `projectionWindowEnabled`——E-3 的取证方向（hello capabilities / supportsProjectionWindow / 安装包身份）完整覆盖该链每一环。
- `ProjectionStore.swift:763-779` `pullOpenWindowed`：现状**未传 limit**（Phase A 拟议默认 25 是真实增量）；`.unsupported/.skipped` 现状回落 `pullFull`——与方案「不允许新 peer 非 typed 错误静默 full fallback」改造点描述一致。
- `projection_window_handler.go:119-128`：hydrate in-flight 时 window 请求返回 `projection.hydrating`（retryable=true）——iOS 重试节奏影响冷 Mac 场景首屏耗时（见 F-8）。
- 正式 runtime 二进制含 window 符号但**符号存在不等于该构建 main() 已置 flag true**——安装包身份核查（S-0/E-3）确有必要，方案未把「有符号」误当「已启用」✓。

### 2.4 覆盖声明

- 已核：方案 §2 全部锚点（21 处）+ §1.1 全部事故数字 + §2.4 关键矛盾（日志 0 次 window RPC）+ §3.5 decode 五步现状 + audit-plan 专项（§2.2 表）。
- 未核（不影响结论）：截图 heic（无视觉工具授权，且日志时间线已闭环）；Mac 侧 Codex `thread/turns/list` RPC 处理器函数体（锚点常量区已足以支持「分页存在」命题）。
- think.md 检索（repo 活文档纪律）：Mac 仓 `think.md` 相关条目（:450「iOS 27h 重连 91 次每次重拉全量投影」、:1205 grokbuild 15s 超时死循环、:834 审批卡 15s）与 iOS 仓 `think.md`（:3343/:3485/:3491 legacy get_session_messages 15s）均为**不同根因**的历史复盘，无与本案冲突或可直接复用的既有结论；方案未引用 think.md，见 F-6。

## 3. 意见

### F-1 [阻塞] preview cache 的身份 key 与 epoch 变化行为未定义，S-4 验收场景预期不可测

- 位置：§3.4.1（「key：backend identity + bridge ID + session ID」；metadata 含 bridge epoch）、§3.4.2 状态机（CacheMiss/CacheHit/AuthoritativeApplied/AuthoritativeFailed 四态）、§6 S-4 验证（「epoch change」）、§7.4 场景 4（「Mac runtime 重启后重开」）。
- 证据：`bridgeEpoch` 随 runtime 重启变化（`runtime.json` 含 bridgeEpoch；事故日志 req_18 与 req_33 的 connectionGeneration 1→2）。§3.4.1 的 key 使用未定义术语「bridge ID」（与 metadata 的「bridge epoch」是何关系未说明）；§3.4.2 状态机没有 EpochChanged 分支；S-4 测试列了「epoch change」却没有任何一处定义其预期结果；§7.4 场景 4 的「应看到」未写明是 preview+syncing 还是纯 loading。
- 影响：S-4 实施时工程师必须在「key 含 epoch（重启即全 miss，场景 4 退化为无 preview）」与「key 不含 epoch、跨 epoch 显示旧 preview（若 session 在新 epoch 被 compact/重写则误导）」之间自行二选一，两种行为都符合当前方案文本；owner 矩阵场景 4 无法验收。
- 修订方向：① 定义「bridge ID」术语（持久配对身份还是每代 epoch）；② 在 §3.4.2 增加 epoch 变化分支及其预期（推荐：key 用持久身份，epoch 进 metadata 校验，epoch 不匹配时仍可显示 last-good 但标记更强 stale，并说明安全性依据——preview 只是显示层、不进 replica、输入框按权威状态）；③ §7.4 场景 4 写明预期「应看到」。
- 闭合标准：下一版方案中「bridge ID」有定义、状态机覆盖 epoch 变化、场景 4 有可对照的预期结果。

### F-2 [阻塞] R-1「window_0 逻辑 payload 受既有 4MiB 上限约束」与冻结 v1 语义存在偏差，且事故样本真实存在 6.35MB 单 turn

- 位置：§1.2 R-1；对照 §3.3 Phase A、§7.1.1。
- 证据：`projection_window.go:24-26`「maxWindowEncodedBytes caps the encoded window payload accumulation (**a single turn larger than the budget is still served alone** — the bound limits accumulation and never splits a turn)」；本轮实测事故样本最大单 turn 6.35MB > 4MiB（虽不在当前尾部 17 turns 内，但 Phase A 验收同类长 session 可能命中）。
- 影响：R-1 按字面验收时，任何含超大 turn 的首开 payload 可合法超过 4MiB，验收员将判不达标；实施者若反过来为满足 R-1 硬截 4MiB，会违反 frozen v1 的 never-split-a-turn。方案 §7.1.1 已有「单个超大 turn 单独成页」测试（语义正确），与 R-1 措辞内部不一致。
- 修订方向：R-1 改为「受既有上限**规则**约束（累积上限 4MiB；单个超限 turn 单独成页，可超 4MiB）」，并注明超大 turn 场景由 Phase B summary page（≤1MiB 目标）收口。
- 闭合标准：R-1 与 §7.1.1 语义一致，无按字面验收即失败的组合。

### F-3 [建议] 重启场景 prefix 校验的 IO 成本未定义，「增量」表述易被误读为重启后零全量 IO

- 位置：§3.2.2 `projection_segments.prefix_sha256`、§3.2.3 步骤 2「校验 …和 prefix」、§7.3「Mac source advance：append 只处理 suffix，不重读 97MB」。
- 证据：现有 prefix 校验 `digestProjectionSourcePrefix`（projection_kernel.go:265-278）为 `io.CopyN(cursor)` 顺序读全部 prefix——97MB 文件重启后校验需整读。Codex 参考机制不重读 prefix（thread_history_materialization.rs:42-45 直接信任 `next_byte_offset`）。§7.3 验收行只约束 append 场景（live relay tailer 已增量，成立），但重启场景方案未写 prefix 校验成本与策略（全量重算 / 按 (path,size,mtime) 缓存 / 抽样）。
- 影响：S-2 DDL 冻结时必须回答该问题；不写明则「增量物化」的重启收益与 3s 目标的 Mac 侧预算无法核算。
- 修订方向：在 §3.2.2/§3.2.3 或 S-2 测试计划中写明 prefix 校验的频率与成本策略，并给出重启后首开的 Mac 侧时间预算。
- 闭合标准：重启场景的校验 IO 行为有明确定义并纳入 §7.3 指标表。

### F-4 [建议] 首屏 ≤3s/≤5s 目标未区分 Mac 冷/热前提

- 位置：R-2、§7.3「authoritative first page | Direct ≤3s；Relay ≤5s」。
- 证据：事故实测冷 hydrate 1.794s（checkpoint miss 全量重建，log 7722 `checkpointHit=false startCursor=0`）；3.79MB Direct 传输 + iOS decode/apply 后冷路径贴线。第一阶段（S-0/S-1/S-5）尚无 S-2，冷 Mac 全量 hydrate 仍在路径上。
- 影响：验收时 Mac 冷热状态不固定会导致同一 build 时过时不过。
- 修订方向：§7.3 该行注明前提（如「Mac materialization/checkpoint warm」达标值 3s/5s；Mac 冷全量 hydrate 单列预算或注明第二阶段才承诺），或 S-6 矩阵固定 Mac 预热步骤。
- 闭合标准：目标行带可复现的前提条件。

### F-5 [建议] OD-2 标注影响 S-1，但 S-1 前置未含 OD-2，门控关系需一句明确

- 位置：§4 OD-2「影响切片 S-1、S-3」；§5 Gate A「S-1 开始实施只需 E-1/E-2」；§3.3 Phase A「iOS 增加请求 limit 的默认值（拟议 25）」。
- 证据：OD-2 选项 B 即 Phase A 的 limit 25；S-1 的功能恢复本体（走通 window_0）不依赖该数值（默认 100 turns 亦被 4MiB 截到 17）。
- 影响：读者无法判断 OD-2 pending 是否阻塞 S-1 开工。
- 修订方向：明确「S-1 用现有默认 limit 开工；limit 25 调整等 OD-2 裁决后实施」，或把 OD-2 影响切片改为「S-3（及 S-1 的 limit 参数化部分）」。
- 闭合标准：OD-2 与 S-1 的阻塞关系无歧义。

### F-6 [建议] 未引用 think.md 复盘（repo 活文档纪律）

- 位置：§2 现状调查。
- 证据：repo CLAUDE.md 要求排查 session/history/加载异常「必须先检索本仓 think.md 和相邻 iOS 仓 think.md」。本轮代查：Mac think.md :450（27h 重连 91 次每次重拉全量投影）、:1205（grokbuild gate 卡死致 iOS 15s 超时重试死循环）与本案同症状不同根因；iOS think.md :3343/:3491 为 legacy get_session_messages 路径。无冲突结论。
- 影响：流程完整性；且 :1205 条目证明「15s 超时重试死循环」在本产品有先例，可作为 §1.1 问题的佐证背景。
- 修订方向：§2 增加一小节列 think.md 检索结论（引用行号），声明无被遗漏的既有结论。
- 闭合标准：方案含 think.md 检索记录。

### F-7 [建议] Codex 锚点 `cloud-client/README.md:16` 路径不完整

- 位置：§2.3 末段。
- 证据：仓根无 `cloud-client/`；实际为 `codex-rs/cloud-client/README.md`，`Attach` 无 history snapshot/replay 声明在 :18（:16 是 `ResumeRequest.wait` 段落）。
- 修订方向：改为 `codex-rs/cloud-client/README.md:18`。
- 闭合标准：路径可从仓根解析、行号指向声明本体。

### F-8 [建议] 测试矩阵建议补两项：多设备并发 window 拉取；`projection.hydrating` retryable 错误的 iOS 重试节奏

- 位置：§7.1/§7.2/§7.3。
- 证据：`projection_window_handler.go:95-99` 每 conn 独立 subscribe + snapshot fence；`:119-128` hydrate in-flight 返回 `projection.hydrating`（retryable=true）——冷 Mac 上首个 window_0 可能先吃一次 hydrating 再成功，重试节奏直接影响 F-4 的冷场景耗时。多设备（iPhone+iPad）同时打开同一长 session 的并发 window 请求未列入矩阵。
- 修订方向：§7.1 加并发 window 请求一致性测试；§7.2 加 hydrating retryable 的重试/退避行为测试；冷场景耗时计入 §7.3。
- 闭合标准：两项出现在测试矩阵并有预期结果。

### F-9 [建议] v2 与 turn_detail_lazy_v1/chunks_v1 的关系宜给出倾向，避免两套 item detail 通道并存

- 位置：§3.3 Phase B「v2 冻结前必须定义」第 6 项。
- 证据：既有 `turn_detail_lazy_v1`/`turn_detail_chunks_v1` 已覆盖「按需加载 turn 明细」语义（main.go:560 下方 release gate 注释；iOS SessionTurnItems 状态机）；v2 的 item page 若平行新建将出现两条 detail 通道。
- 修订方向：在冻结前置清单中写明倾向（如「v2 item page 演进/复用 turn_detail 家族的绑定与 chunk 语义，不平行新建第二套明细协议」），最终以冻结评审为准。
- 闭合标准：冻结清单第 6 项有可评审的倾向性答案。

## 4. §10 评审重点逐项结论（方案自列 7 项）

1. **S-1 是 P0 架构功能恢复而非补丁：成立。** 两端源码实现完整（main.go:560 flag on、handler/producer 齐备、iOS pullOpenWindowed:763 完整且 capability-gated），正式 runtime 含能力符号；事故全日志 0 次 window RPC 证明是部署/协商断层而非功能缺失。E-3 pending 正确阻断 S-1 完成宣告。
2. **materialization store 放 Mac：正确。** Mac 是 projection 唯一真相 writer（kernel）；物化索引放真相侧使多设备共享收益，iOS 只持 preview cache；与 Codex thread-store 放服务端同构（0001 migration 锚点）。
3. **preview cache 不进 ProjectionReplica 的边界：足够强**（§3.4.2 四关键点 + §8 风险行 + E-7/S-4 测试门 + §7.2「replica 唯一 writer seal」），唯 epoch 语义缺口见 F-1。
4. **Phase A turn_blob_json / Phase B item rows：可接受。** Phase A 行为闭环且回退边界明确（停用 store 回旧 checkpoint、旧 checkpoint 不删除）；SQLite 单行 6MB blob 可行；「SQLite 写入成为 live bottleneck」已入风险表并有异步批量提交处理。
5. **v2 与 lazy/chunks 关系：方案把该问题列为冻结前必答（第 6 项）结构正确**；建议补倾向见 F-9。
6. **OD-1/2/3 推荐：满足产品语义且保持 pending**（B「last-good + syncing watermark」符合 R-4 显式标记；无 TTL + identity/schema/容量淘汰与 last-good 语义一致；分阶段窗口大小合理）。门控小张力见 F-5。
7. **目标耗时与测试矩阵：需小幅调整**——F-4（冷热前提）、F-8（两项补充）、F-2（R-1 措辞）。

## 5. 复审处置与剩余门

首轮评审，无历史意见闭合表。

实施前仍未满足的门（与阻塞意见分开列示；按共享约定，pending 门不自动计入方案阻塞数）：

- **证据门**：E-3（window 未生效根因，S-1 修复前必须闭合）；E-4 wire 级验证（S-1 验收）；E-5（iOS 阶段耗时，S-5 完成宣告）；E-6（materialization 正确性，S-2 完成）；E-7（preview 不污染权威 writer，S-4 完成）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决，S-4/S-3 前置）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则，agent 不自动执行）。

## 6. 评审过程记录

- 方案哈希派发时与读取时一致（见报告头）。
- 本轮唯一写入文件：本报告。方案、代码、其他文档一律只读；iOS 未提交 4 文件未触碰。
- 使用的契约：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（外部内容形状专项已实际运行：dump/计数/压缩实测见 §2.2）。
