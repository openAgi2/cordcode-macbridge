# Claude 长 session 打开方案评审 R25

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **APPROVED（通过）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，**v29**。
- 全文 SHA-256：`0f975321372234bb1aa2fbc6cff2f50c9a73f872946a047a8bbf9e627f332494`。
- 正文 SHA-256：`a970e8f4ae8be4f1bd463f4e45c43394a6e2c6c409598adb1a681ea14af547aa`；按 `raw[:idx].rstrip(b"\n")`，`idx = "\n---\n\n设计师交接块"` 的 UTF-8 字节位置计算，与交接块一致。
- blockers: **0**；advisories: **1**（F-62，首包预算句的计量对象措辞，不改变设计及实施门）。
- scope: **full**：当前规范部分 §1–§9、v29 修订处/§10.33/交接块；重点重演 F-42/F-44/F-48/F-52/F-53/F-59 及 F-61，检查需求—决定—证据—切片—验收关系。历史修订链用于追溯，未把 F-1–F-61 每条历史事实全部重测为本轮事实。
- review_mode: 本会话单评审员复审；已接触历轮结论，**不宣称盲审，也未使用 fresh 子代理**。本轮结论依赖重新阅读的规范、实际源码及反例推演，不由两张 APPROVED 卡片或轮次上限导出。
- implementation_readiness: **方案阶段可以结束；各切片按自身前置门进入开发**。APPROVED 不把 E-3、冻结批次、实施后测试或执行授权自动标为完成。

两份 META-FAIL 的具体问题均复现，影响限定成立：R23 的 U 提交转写错误与 R24 的子行号偏移属于报告证据记录缺陷，本轮用有效来源和准确摘录重新建立相关依据。**本报告直接批准实际 v29 快照**，不再以“v29 是通过后的低风险修改”代替对 v29 的评审。无需再增加一层默认元审核作为终审许可。

## 来源与范围

G1 源码分析前已枚举双仓 worktree，所选分支各唯一对应下列功能工作树；G2 第一次写报告前重新生成路径、分支、完整提交及脏清单。两门身份相同。方案 §2.1 的旧身份属于注明时点的历史快照，本轮不用它代替实际来源。

| 别名 | 绝对工作树路径 | 分支 | 完整提交 |
| --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `b77faf3c579b571a9df86f6e3cbae3eacaa8a21e` |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897` |

任务预期来源：M/I 均为 `feat/ios-native-message-timeline` 功能分支族，配对依据为方案 §2.1 记录的两条绝对路径及本轮 worktree 元数据；M 的配套为 I 表列路径/分支/提交，I 的配套为 M 表列路径/分支/提交。U 仅作只读上游参照，不是产品构建来源。

预期产品特性：Claude SSV2 单 Kernel 真相、单 iOS writer、首开 full+gzip、重开 restore+anchor 对账、Direct/Relay 协商与 typed 失败；本轮不生成或验证任何运行产物。

M（G2 写报告前；与 G1 来源相同）：
```text
 M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r14.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r15.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r16.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r17.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r18.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r19.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r20.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r21.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r22.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23-meta.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24-meta.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

I（G2 写报告前；与 G1 来源相同）：
```text
 M .exec-plan/state/plan-b4b554001ea8.json
 M OpenCodeiOS/CordCode.xcodeproj/project.pbxproj
 M OpenCodeiOS/OpenCodeiOSTests/MarkdownLibraryVersion.swift
 M OpenCodeiOS/OpenCodeiOSTests/StreamingMarkdownIsomorphismTests.swift
 M OpenCodeiOS/Packages/MarkdownView/Sources/MarkdownView/Components/TableView/Witness/WitnessValues.swift
?? OpenCodeiOS/OpenCodeiOSTests/StreamingMarkdownTableInputGateTests.swift
```

U（G2 写报告前；与 G1 来源相同）：
```text
干净
```

I 相对 R24 的 `7527e1ead184fa88c58b05ef86839b7ba6489009` 已前进。对本轮所查的 `ProjectionStore/ProjectionReplica/SessionProjectionWindow/SessionProjection/ProjectionPresentationEngine/BridgeWebSocketConnection/RelayBridgeFrameConnection/ProjectionWindowSpecTests` 作 path 限定 diff，结果为空；这些文件也不在脏清单中。本轮仍重新亲核下表锚点；未把整个 iOS 工作树称为干净，未读改上述他人未提交内容来形成结论。

## 源码锚点核验

原引保留，实际行及原文由文件读取生成；位置与语义分别判断。表内均为本轮亲核，未把旧报告的核验当作新核验。

| 命题 / 方案原引用 | 实际来源 | 最小原文摘录（实际行号） | 定位 / 语义结果 | 核验方式 |
| --- | --- | --- | --- | --- |
| §3.8 原引 projection_kernel.go:1887-1902；每请求当前 head | M `go-bridge/projection_kernel.go` | <code>1887: func (k *ProjectionKernel) Snapshot(backendID, sessionID string) (SessionProjection, bool) {</code><br><code>1895: if projection, ok := reducer.Snapshot(backendID, sessionID); ok {</code><br><code>1896: return projection, true</code> | 引用覆盖函数及当前 reducer 返回；支持，不提供历史 cut。 | 本轮亲核 |
| §3.8 原引 projection_window_handler.go:168-210；单请求 fence | M `go-bridge/projection_window_handler.go` | <code>168: proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(</code><br><code>210: if err := h.eventPublisher.CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, turnIDs); err != nil {</code> | 两个端点准确；同一请求 begin/complete。另亲核 projection_snapshot_fence.go:112 的取快照调用。 | 本轮亲核 |
| §3.8 原引 projection_window_handler.go:201-211；held 注册；v29 勘误 | M `go-bridge/projection_window_handler.go` | <code>204: // connection a no-op revision patch and permanently strand the detail</code><br><code>205: // behind the stale response. Completion failure rolls the set back.</code><br><code>206: turnIDs := make([]string, 0, len(response.Turns))</code><br><code>207: for _, turn := range response.Turns {</code><br><code>208: turnIDs = append(turnIDs, turn.TurnID)</code><br><code>210: if err := h.eventPublisher.CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, turnIDs); err != nil {</code> | 区间覆盖被引注释、turnIDs 构造和完成调用；原报告 199-204/205 不准确，实际为 200-205/206。 | 本轮亲核 |
| §3.8 原引 projection_window.go:278-296；排他后继 | M `go-bridge/projection_window.go` | <code>279: cursor, err := decodeProjectionWindowCursor(params.Cursor)</code><br><code>283: if err := validateProjectionWindowCursorScope(cursor, backendID, sessionID, bridgeEpoch); err != nil {</code><br><code>286: boundary := indexOfTurn(turns, cursor.AnchorTurnID)</code><br><code>290: slice.start, slice.end = growWindowRange(sizes, limit, boundary+1, false)</code> | 定位覆盖实际行为；boundary+1 为严格后继，非含锚。 | 本轮亲核 |
| §3.8 原引 projection_window.go:297-301；locate 裸 ID 先例 | M `go-bridge/projection_window.go` | <code>295: case projectionWindowDirectionLocate:</code><br><code>296: anchor := indexOfTurn(turns, params.AnchorTurnID)</code><br><code>297: if anchor &lt; 0 {</code><br><code>300: return ProjectionWindowResponse{}, errProjectionWindowLocateOut</code> | 分支入口实际在 295、查找在 296，原区间覆盖 miss 判定与 typed 错误；语义支持。未把偏移的入口行默认为准确。 | 本轮亲核 |
| §1.1 / OD-7 原引 projection_window.go:24-31；never-split | M `go-bridge/projection_window.go` | <code>24: // R5 assertable bounds. maxWindowTurns caps the requested limit; maxWindowEncodedBytes</code><br><code>25: // caps the encoded window payload accumulation (a single turn larger than the budget is</code><br><code>26: // still served alone — the bound limits accumulation and never splits a turn).</code><br><code>28: maxWindowTurns         = 256</code><br><code>29: maxWindowEncodedBytes  = 4 &lt;&lt; 20</code><br><code>30: defaultWindowTurns     = 100</code> | 定位与语义支持；46MiB 是未来协议额度，不是此处已有常量。 | 本轮亲核 |
| §3.8 / §3.4.3 原引 server.go:780；direct epoch | M `go-bridge/server.go` | <code>780: ack.BridgeEpoch = s.bridgeEpoch</code> | 定位准确；支持 hello_ack 携带 epoch。 | 本轮亲核 |
| §3.8 原引 main.go:640；relay epoch | M `go-bridge/main.go` | <code>640: ack.BridgeEpoch = bridgeEpoch</code> | 定位准确；支持 relay hello_ack 携带 epoch。 | 本轮亲核 |
| §3.7 / §3.8 原引 server.go:808、main.go:693；共享协商 | M `go-bridge/server.go` | <code>808: if !s.negotiateProjectionWindowV1(ack, &amp;hello, connection) {</code> | direct 调用点准确；relay main.go:693 同函数调用另行亲核，新增能力仍须实现并冻结。 | 本轮亲核 |
| §3.2.3 原引 handlers_relay.go:877/:882-884；生产 source ledger | M `go-bridge/handlers_relay.go` | <code>877: h.ensureClaudeSourceStateInstalled(backendID, sessionID, sessPath, offset)</code><br><code>878: // Compute the segment correlation once at relay startup. Needed for every live source batch</code><br><code>879: // (the Kernel cursor/gap/generation fence keys on SegmentStableKey/SegmentGeneration) and reused</code><br><code>882: traceCorrelation, err := h.claudeSourceCorrelation.Observe(</code><br><code>883: backendID, sessionID, sessionID, sessPath, offset,</code><br><code>884: )</code> | 定位准确；Observe 在生产启动路径，无 trace 开关包围。 | 本轮亲核 |
| §3.2.3 原引 claude_source_trace.go:75/:99/:122；整读 | M `go-bridge/claude_source_trace.go` | <code>75: currentOldPrefix, digestErr := digestClaudeSourcePrefix(file, previous.cursor)</code><br><code>99: digest, err := digestClaudeSourcePrefix(file, cursor)</code><br><code>115: </code><br><code>122: if _, err := io.CopyN(hash, file, cursor); err != nil {</code> | 75/99 为调用，122 为 io.CopyN；函数声明实际在 115。支持 prefix 整读，不沿用 R24 将 122 写作声明行的转述。 | 本轮亲核 |
| §3.8 原引 BridgeWebSocketConnection.swift:12；50MiB | I `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeWebSocketConnection.swift` | <code>12: static let maximumMessageBytes = 50 * 1024 * 1024</code> | 定位准确；direct 消息现状上限。 | 本轮亲核 |
| §3.8 原引 RelayBridgeFrameConnection.swift:823/:859-862；解压上限 | I `OpenCodeiOS/OpenCodeiOS/Services/Bridge/RelayBridgeFrameConnection.swift` | <code>823: maxOutputBytes: Int = BridgeWebSocketContract.maximumMessageBytes</code><br><code>859: guard output.count &lt;= maxOutputBytes - written else {</code><br><code>861: code: "relay.decompressed_too_large",</code><br><code>862: message: "Relay 解压后消息超过大小上限"</code> | 定位准确；gzip 不绕过逻辑输出上限。 | 本轮亲核 |
| §3.4.3 原引 SessionProjectionWindow.swift:123-126/:173 | I `OpenCodeiOS/OpenCodeiOS/Models/SessionProjectionWindow.swift` | <code>123: guard !page.turns.isEmpty else {</code><br><code>126: return self</code><br><code>173: copy.syncRev = max(syncRev, page.syncRev)</code> | 定位准确；恢复必须用专用 head 采用语义，不能直接沿用空页 return self / max。 | 本轮亲核 |
| §3.4.3 原引 SessionProjection.swift:696；revisionKey | I `OpenCodeiOS/OpenCodeiOS/Models/SessionProjection.swift` | <code>696: var revisionKey: String { "\(bridgeEpoch ?? "")&#124;\(syncRev)" }</code> | 定位与 epoch/rev 身份语义支持。 | 本轮亲核 |
| §3.4.3 原引 ProjectionReplica.swift:193-212；forceReplace | I `OpenCodeiOS/OpenCodeiOS/Models/ProjectionReplica.swift` | <code>193: func applyFull(</code><br><code>196: forceReplace: Bool = false</code><br><code>199: if !forceReplace, let current = state.projection, projection.syncRev &lt; current.syncRev {</code><br><code>200: return .stale</code><br><code>207: state.projection = projection</code><br><code>208: state.appliedRev = projection.syncRev</code> | 定位准确；forceReplace 绕过 stale，且 replica 同时写 projection/appliedRev。 | 本轮亲核 |
| §3.4.3 原引 ProjectionStore.swift:612-619/:1180；await 重入 | I `OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift` | <code>612: switch await replica.applyWindowPage(</code><br><code>619: await commitIfObserved(</code><br><code>1180: let presentation = await presentationEngine.apply(</code><br><code>1203: states[sessionId] = next</code> | 原引覆盖 await；1203 是本轮另查 store 发布点。支持跨 await 恢复门的必要性。 | 本轮亲核 |
| §3.6 / S-8 原引 BridgeWebSocketConnection.swift:6/:304-306/:326/:334 | I `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeWebSocketConnection.swift` | <code>6: func receiveData() async throws -&gt; Data</code><br><code>304: func send(_ data: Data) async throws {</code><br><code>305: try await sendFrame(opcode: .binary, payload: data)</code><br><code>326: NSLog("[NetworkWebSocket] 收到完整消息 frameType=%d bytes=%d", opcode.rawValue, accumulated.count)</code><br><code>327: return accumulated</code><br><code>334: NSLog("[NetworkWebSocket] 收到分片消息完成 frameType=%d bytes=%d", opcode.rawValue, accumulated.count)</code><br><code>335: return accumulated</code> | 定位准确；send 已是 binary，receive API 仅返回 Data；S-8 暴露 opcode 有实际改造对象。 | 本轮亲核 |
| §3.2.3 原引 thread_history_materialization.rs:42-45；上游 offset | U `codex-rs/thread-store/src/local/thread_history_materialization.rs` | <code>42: let projection_state = super::thread_history::projection_state(store, thread_id).await?;</code><br><code>43: let start_offset = projection_state</code><br><code>44: .as_ref()</code><br><code>45: .map_or(0, &#124;state&#124; state.next_byte_offset);</code> | 定位准确；仅架构参照，不证明 Claude 原始格式或 CordCode 整链 IO。 | 本轮亲核 |
| §3.7 原引 bridge-v1.md:2328-2332；re-freeze 门 | M `docs/protocol/bridge-v1.md` | <code>2331: producer/replica sides. Open product semantics discovered later are BLOCKERS to be re-frozen</code><br><code>2332: here first; implementing agents MUST NOT guess them in code.</code> | 定位准确；实施前冻结不可绕过。 | 本轮亲核 |
| §3.8 原引 projection_window_spec_test.go:33/:73-75；独立 fixture | M `go-bridge/projection_window_spec_test.go` | <code>33: raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "samples", "projection-window-v1", "window-response-spec.json"))</code><br><code>73: if got, want := sortedRawKeys(response), []string{"resume", "syncRev", "turns", "window"}; !reflect.DeepEqual(got, want) {</code><br><code>74: t.Fatalf("response keys = %v, want %v", got, want)</code> | 定位准确；旧 fixture 精确 key 比对，新增响应单独冻结合理。 | 本轮亲核 |

## 两份 meta 的处置

1. **R23 来源更正成立**：U 的实际 HEAD/main 为 `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git cat-file -t` 返回 `commit`；R23 转写值 `ce0323f2f2d3b25e82806cd55e237da3db4e1897` 返回 exit 128、`could not get object info`。R23 原报告确有该错误和“实测一致”声明。v28/v29 保留原报告、登记更正的做法可追溯。本轮不沿用该失实声明。
2. **R24 子行号更正成立**：M `projection_window_handler.go` 注释块实际 200–205，构造 `turnIDs` 实际 206，完成事务调用实际 210；原报告记录的 199–204/205 有偏差。方案原引 201–211 覆盖所需行为，语义成立，见本轮逐行摘录。
3. 两份 meta 的判定规则是对报告记录“发现任何问题即不确认”；这不能自动转写为方案有阻塞设计缺陷。反之，作者说明“仅笔误”也不能取代复核。本轮完成了必要复核，受影响技术依据已重新建立。
4. 未读取或认证 workflow 卡片的执行实现、actor 工具收窄或 fresh 代理独立性；它们不作为本报告通过依据。R23/R24 当时的原始方案字节未在当前工作树保留，不能据哈希字符串证明 v28→v29 逐字节零设计差异；本轮按现版完整规范评审消除此依赖。

## 关键组合行为与回归

| 对象 | 本轮判断与依据 |
| --- | --- |
| F-61 续页身份 | **闭合**。§3.8:853 明确回传上一响应修正 cursor；无标记=首包，有标记=续页。§3.8:869、交接块明确同批冻结。46MiB anchor 的第二请求即使 anchor 未前移也能区分，不需要 Mac 保存设备进度。 |
| F-60 changedTurns 范围 | **维持闭合**。§3.8:839 的“复查集中”与 :853 全集、不收缩定义、:855 全部项比对及 :840 tombstone 覆盖一致。 |
| F-48 预算及后继交付 | **维持闭合**。按明确的“anchor 不计对账预算”及验收重演：46MiB anchor+2MiB mandatory 首包占满；1KiB/9MiB 后继在无 anchorRefresh 的续页交付；42MiB anchor+6MiB 后继首包空余4MiB不回填，续页交付后继。46+9、20+46、46+46 的 correction 独占页均可在48MiB内，超46MiB走 typed oversized。尚有一处措辞见 F-62。 |
| F-52/F-59 当前 head 与全集复查 | **维持闭合**。假设页1确认 A，页间 A→A′；页2仍携带 A token，Mac 比对发现变化、通过 changedTurns 交付 A′。若修正装不下则 correctionsPending 继续，不能发布最终 head；末页完成才一次采用其 head/scalar，rev≤head 的排队 patch 可按版本丢弃，内容已由对账覆盖。末页 cut 后的 patch 则 rev>head 正常接续。旧确认项、早页已发项、anchor 自身均在全集；删除用 tombstone，anchor 被删用 typed stale 中止。 |
| F-44 跨 epoch / await | **维持闭合**。恢复身份副本 epoch=当前、rev=0，强制替换并同时复位 appliedRev/coverage；最终采用不依赖空页 return self 或 max 分支。多页期间只暂存，最终一次提交；恢复门隔离 patch/recovery，不能在 rev=0 上触发缺口恢复。§3.4.3:715–716 与 §3.8:855、§7.1.5/§7.2 的跨 await/最终一次采用断言共同约束，不将单个中间页视为恢复完成。 |
| F-42/F-50 token 生命周期 | **维持闭合**。full/对账内容-token 成对；局部 patch 未提供最终态 token 即置 null；mixed patch token 绑定全部 ops 之后终态。缓存逐行保存有效性，null 强制回传，E-11 与 §7.2.13 均有负例。 |
| F-53 full 超限 | **维持闭合**。48MiB 对账硬顶与默认50MiB逻辑传输顶分开；reconciliation.oversized→FallbackFull→full.oversized 是可观察终态，不能用 gzip 或 transport 拒绝冒充成功。提升 projection_max_bytes 必须同批冻结并更新 Direct/Relay 两侧上限。 |
| F-49/F-45/F-47 IO | **维持闭合**。默认重启=prefix 校验整读+suffix 重投影；window handler 冷检=stat/尾部扫描，不代替生产全链读量。Observe/CopyN 生产调用已亲核；OD-9 仍 pending，不能默认跳过校验，不能声称 iOS 摘要对账能发现同一 Kernel 的错误。 |
| F-41/F-54/F-57 路径一致性 | **维持闭合**。§3.9、§3.8、§7.2.14、§7.3 重开行及 §8：typed stale/超2页/未 echo→FallbackFull；网络/hydrating 重试对账；首开 full 是设计行为。window_0 仅 contingency，启用前需 scalar 冻结。历史注记中的旧规则不充当现行分支。 |
| F-58 相位 / F-51 指标 | **维持闭合**。turn 装不下即切页，不跨 phase 回填；Mac restart 指标只有一行。 |
| held 注册 | 新交付 turns、anchorRefresh、changedTurns 及续页内容都须在同一 fence 完成事务中注册，正文已登记。另检查过“未重发缓存 turn 未注册”的猜想：现有 detail RPC 在 `session_turn_items_handler.go:71–75` 明确仅 codex-remote，不能把此猜想当作 Claude 当前范围已可达阻塞；S-3 明细语义仍有独立冻结门，不宣称已验收未来实现。 |

未执行上述实现测试；表内为源码与规范层反例推演，实际通过证据仍归 E-6/E-7/E-10/E-11。

## 意见

### F-62 [建议] 首包打包句“三者合计”的计量对象宜同步到已定案预算定义

- 位置：方案 §3.8:849：“anchorRefresh → turns → changedTurns；**三者合计受本页对账空间 = min(8MiB, 48MiB − 2MiB − anchorRefresh 序列化大小) 约束**”。
- 证据：字面“三者”把 anchorRefresh 再计一次；同节 :846 明确“anchorRefresh 不计入”，§7.1.5:991–993 则明确46MiB anchor合法、首包普通对账空间为0，OD-7与§7.3也采用分层计量。
- 影响：此句若脱离定义引用，会误导为对 anchor 重复扣账。它没有形成第二个可满足整份规范及边界验收的有效打包方案；预算集合已被定义句和多个边界测试唯一锁定，因此本轮定级为措辞建议，**不重开 F-48、不阻塞方案通过**。
- 最小修订：把“三者合计”改为“**turns 与 changedTurns 使用本页剩余对账空间；scalar/metadata 按已定义预算计入，anchorRefresh 另按独占额度及总硬顶计量**”，保持既有额度、顺序、续页行为和验收不变。
- 闭合标准：该句不再将 anchorRefresh 计入已明确排除它的对账空间。可随冻结协议誊写同步，无需为此再启动方案评审循环。

## 门控与批准边界

| 项目 | 仍然有效的约束 |
| --- | --- |
| S-0 | 可按已有授权进行诊断；先重新固定实际 builds、运行态与协商证据，不能用本轮源码评审替代运行态证明。 |
| S-2 | 选型已定；开工仍需 bucket/key 格式冻结与不变量测试清单；E-6 是完成门。OD-9未裁决时只能用默认校验档(a)，不影响设计通过。 |
| S-5 / S-8 | 第一阶段承重项；S-8需要其协议/fixture/schemaRevision冻结；E-5与冷路径execution护栏等是交付证据。性能≤8s/≤10s是目标，未被本报告写成已达到。 |
| S-7 | **E-3 开工门仍未满足**；anchor re-freeze 要覆盖 F-55 epoch、F-59 全集请求、F-60语义、F-61续页身份、projection_max_bytes及双路径协商。E-4/E-10等仍为完成门。 |
| S-4 | 依赖 S-7 落地，完成需 E-7/E-11；不能提前接入未冻结协议。 |
| S-3 | summary UI / active例外 / detail关系 / 错误语义等独立协议冻结门，不能因本次APPROVED自行猜测。 |
| S-6 | owner人工矩阵与真实运行证据；未授权UI automation、构建、安装或部署。agent-device永久停用。 |

外部格式专项范围：本版新增的是 CordCode 自有协议、持久化与投影对账，不新增 Claude JSONL 内容类型→字段路径的 parser 映射；现有内部实现可由源码核验，拟议 wire shape 由冻结批次和 E-4 真实 capture 约束。已读取 audit-plan 接入规则，本轮不声称完成“外部格式真实样本专项通过”，也没有把 synthetic fixture 当外部格式证据。现场事故日志/截图、原始 transcript体积等历史观测本轮未重新采集，不充当新运行证据。

## 本轮动作与限制

完整阅读 R23、R24 与两份 meta；读取当前规范并重新核对上述源码。未运行 build、unit/UI/snapshot test，未修改方案、业务代码、旧报告，未提交、安装、部署、重启或发送消息给其他会话。唯一新增文件为本报告。

交付检查为 `git diff --no-index --check /dev/null <本报告>`（新文件空白检查）及 `git diff --check -- <本报告>`；另复算计划全文/正文哈希。报告中的源码摘录在写入时逐行取自表列来源，避免再次手写偏移行号。

```text
verdict: APPROVED
plan_version: v29
plan_sha256: 0f975321372234bb1aa2fbc6cff2f50c9a73f872946a047a8bbf9e627f332494
plan_body_sha256: a970e8f4ae8be4f1bd463f4e45c43394a6e2c6c409598adb1a681ea14af547aa
scope: full
blockers: 0
advisories: 1
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, E-11, OD-9(default a), storage-format freeze, S-3/S-7/S-8 protocol freezes, S-6 owner matrix/execution authorization
round_complete: true
contract_version: plan-contract-v1.1
```
