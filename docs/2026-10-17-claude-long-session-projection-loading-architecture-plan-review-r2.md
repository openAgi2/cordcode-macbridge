# 评审报告 R2：Claude 长 session 打开架构优化方案（v2 复审）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并独立完成核验，后以 R1 作回归清单核对意见闭合）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 v2
方案 SHA-256（评审时实测，与派发记录一致）：`9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18`
范围：full（全量复审）

## 1. 结论

- **verdict: APPROVED（通过）**
- blockers: **0**；advisories: **1**（F-10）
- implementation_readiness：方案层面通过。按 Gate A，S-0 可立即开工，S-1 可开工（E-1/E-2 已 verified + 源码锚点齐备）但**完成宣告**仍被 E-3（window 未生效根因）/E-4（wire 级验证）阻断；S-2 需先冻结 DDL；S-3 需 OD-2 + v2 协议冻结；S-4 需 OD-1/OD-3；S-6 需 owner 真机矩阵授权。方案评审通过不构成实施授权（repo 2026-09-20 评审循环纪律：通过是开发阶段唯一入口，本报告即该入口的评审侧闭合）。

总体评价：v2 对 R1 全部 9 条意见（2 阻塞 + 7 建议）的修订经逐项核验**全部闭合**，且修订质量高于最低闭合标准——F-1 的闭合不是补一段文字，而是引入了四个可亲核的源码锚点把 bridge ID / bridgeEpoch 术语钉死在源码上；F-2 的 6,347,762 B 补充实测与本轮独立复测**逐字节吻合**。本轮独立复测的全部样本数字、日志证据、v2 新增锚点均命中；唯一新发现是 iOS 工作树脏文件枚举自快照后发生漂移（F-10，建议级，不影响任何已核命题）。

## 2. 来源与覆盖

### 2.1 来源身份（本轮独立复核）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 |
| --- | --- | --- |
| MacBridge | `11caa1332cf4ad56041f3935f3bc1a0ab4e36a82` @ `feat/ios-native-message-timeline`；未跟踪：本方案文档 + R1 评审报告（均为 docs/，非代码） | ✓ 一致（「当前唯一变更是本文档」为写入时点快照，R1 报告此后产生，非方案缺陷） |
| iOS | `1e895ea9ef42bc83c287c3acd66c3d7616340b4a` @ `feat/ios-native-message-timeline`；未提交 **8 个路径**（详见 F-10）：M `CordCode.xcodeproj/project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift` | 🟡 提交/分支一致；未提交枚举漂移（方案列 4 项，现为 8 项，见 F-10）。**全部锚点文件（ProjectionStore / ChatViewModel+* / MessageCacheManager / SessionSnapshotStore / ProjectionWireDecoder / CCCodeBridgeTransport）均不在脏文件清单中**，内容 = 提交 1e895ea9 内容，本轮与 R1 的核验对象身份未变 |
| Codex 上游 | `ce0323f2f2d3b25e82806cd555237da3db4e1897` @ `main`，干净 | ✓ 一致 |
| 正式 Mac runtime | `strings …/cordcode-bridge-runtime \| grep -c get_session_projection_window` → **2**；`lsof -nP -iTCP:8777 -sTCP:LISTEN` → PID 8303（`cordcode-` 进程监听 `*:8777`） | ✓ 一致（含「runtime 与源码 HEAD 不同、需分层下结论」的告诫） |

复核命令：各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`。

### 2.2 事故样本复测（audit-plan 专项：内容形状声明逐项 dump）

本轮用 python3（`json.load` checkpoint + `json.dumps` 字节测量 + `gzip.GzipFile` 双级别 + 尾部 4MiB 累积模拟）独立重算，不依赖 R1 数字：

| 方案声明 | 本轮实测 | 结果 |
| --- | --- | --- |
| transcript 约 97MB | 101,520,809 B（96.8 MiB） | ✓ |
| full projection 约 34MB / checkpoint 约 34.5MB | checkpoint 34,537,988 B；turn JSON 总和 34,426,926 B；整棵 projection 序列化 34,427,361 B | ✓ |
| 119 turns | `len(projection.turns) == 119` | ✓ |
| 尾部 17 turns 约 3.79MB | 尾 17 turns = 3,801,545 B（3.63 MiB / 3.80 MB 十进制）；按 4MiB 从尾累积恰好 17 turns | ✓（0.01MB 序列化分隔符差异，方案自述「估算」口径内） |
| **v2 补充实测：最大单个 turn 6,347,762 B > 4MiB 且不在尾部 17 turns 内**（§1.1，F-2 修订核心） | max single turn = **6,347,762 B**（index 1/119）；`mi >= len-17` → **False** | ✓ **逐字节精确命中** |
| gzip level 1 ≈11.2MB / level 6 ≈9.6MB | 11,142,266 B / 9,602,913 B | ✓（11.2→11.1MB 为序列化差异，R1 已同口径接受） |
| Relay gzip level 6 后约 1.08MB | 尾 17 turns 页 gzip6 = 1,084,899 B | ✓ 精确 |
| Mac 冷 hydrate 约 1.794s | log 行 7722：`17:30:56.861 … sessionPrefix=bbe396e6 checkpointHit=false startCursor=0 … elapsedMillis=1794` | ✓ |
| E-2：事故只有 `get_session_projection`、无 window RPC | 全日志 `grep -c get_session_projection_window` → **0**；req_18（17:30:56.864 `hydrate_ready sinceRev=0` → `response_enqueue outcome=snapshot`）与 req_33（17:31:36.688/714，同形态，`connectionGeneration=2`） | ✓ verified 成立 |
| E-1：非 Mac hydrate failed | 全日志 `hydrate_failed` 计数 = **0**；时间线 req_18 17:30:56.864 → iOS 15s cap ≈17:31:12 → 截图 17:31:18 → req_33 17:31:36（重连后 `remote=192.168.1.2:62220`） | ✓ verified 成立 |
| 当时连接为 Direct LAN | 事故窗口 hello 行 `remote=192.168.1.2:62219/62220`（RFC1918 直连 bridge WS，非 relay 路径） | ✓ |

hash 记账核验：方案交接块自报正文哈希 `645819a0…`——本轮以「正文 rstrip 尾部换行 + `\n\n`」切分实测命中，记账真实；v1 哈希 `35fb3d33…` 与 R1 报告头记录一致，版本链可追溯。

### 2.3 锚点核验表

**本轮亲核（v2 新增锚点全部亲核 + 高价值复用项抽检亲核）：**

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 |
| --- | --- | --- |
| §3.4.1 `go-bridge/data_dir.go:15-23`（bridgeId 跨重启持久） | :15 `<dataDir>/identity.json — Bridge 身份（bridgeId, displayName, createdAt, runtimeVersion, protocol）`；:22-23 `type BridgeIdentity struct { BridgeID string \`json:"bridgeId"\`` | ✓ 精确 / ✓ |
| §3.4.1 `go-bridge/hello_handler.go:108-111`（bridge profile 下发 bridgeId） | :107-111 `// HelloBridgeInfo … 对应 schema BridgeV1BridgeProfile` / `BridgeID string \`json:"bridgeId"\`` | ✓ 精确 / ✓ |
| §3.4.1 `go-bridge/runtime_startup.go:137-140`（epoch 为进程唯一 UUID） | :137-139 `// generateBridgeEpoch returns a process-unique 128-bit random UUID. … make recovery treat a restarted bridge as the old process.` | ✓ 精确 / ✓ |
| §3.4.1 `go-bridge/main.go:158` | :158 `bridgeEpoch, err := generateBridgeEpoch()` | ✓ 精确 / ✓ |
| §1.2 R-1 / §1.1 `projection_window.go:24-26`（never-split-a-turn） | :24-26 `// R5 assertable bounds. maxWindowTurns caps the requested limit; maxWindowEncodedBytes caps the encoded window payload accumulation (a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn).`；:27-31 `maxWindowTurns = 256` / `maxWindowEncodedBytes = 4 << 20` / `defaultWindowTurns = 100` | ✓ 精确 / ✓（R-1 v2 措辞与该语义完全一致） |
| §2.2 `projection_window_handler.go:32`（capability gate） | :32-34 冻结门注释；:36 `if !h.eventPublisher.ConnProjectionWindowV1(conn)` | ✓（注释块起点）/ ✓ |
| §7.1.1 `projection_window_handler.go:93-99`（每 conn 独立 subscribe） | :93-99 `// Same session-open side effects as the full projection pull: subscribe the conn so projection_patch frames flow…` → `h.subscribeConnToSession(conn, msg, params.SessionID, "")`；snapshot fence 语义见 projection_window.go:11-13 分层注释 | ✓ 精确 / ✓ |
| §7.2.8 `projection_window_handler.go:124-126`（hydrating retryable） | :124-126 `case errors.Is(err, errProjectionHydrating): code = "projection.hydrating"; retryable = true` | ✓ 精确 / ✓ |
| §3.2.3 `projection_kernel.go:266-278`（prefix 整读） | :266 `func digestProjectionSourcePrefix(r io.Reader, cursor int64)`；:272 `n, err := io.CopyN(h, r, cursor)`（顺序读全部 prefix 再哈希） | ✓ 精确 / ✓（v2 引用比 R1 的 :265-278 更准） |
| §2.2 `projection_kernel.go:280` / `:1123` | :280 `func (s *ProjectionCheckpointStore) LoadValidated(`；:1123 `checkpointHit := false` | ✓ 精确 / ✓ |
| §2.2 `main.go:560` | :560 `server.SetProjectionWindowEnabled(true)`（上方 PERF-S4D release gate 注释） | ✓ 精确 / ✓ |
| （附加核验，支撑 §3.4.2 可实现性）iOS 获知「当前 epoch」的输入 | `server.go:780` `ack.BridgeEpoch = s.bridgeEpoch`；`hello_handler.go:70` `BridgeEpoch string \`json:"bridgeEpoch,omitempty"\``——hello_ack 已携带当前 epoch，§3.4.2 的 `metadata.epoch ≠ 当前 epoch` 判定在现有 wire 数据上可实现 | ✓（方案未引用此锚点，非缺陷） |
| （附加核验，E-3 取证方向完整性）协商链 | `server.go:373-394` `negotiateProjectionWindowV1`：客户端 hello 须同时声明 `projection_window_v1` + `session_sync_v2`，且服务端 `projectionWindowEnabled` 才 echo + `SetConnProjectionWindowV1`——E-3 列的三个取证方向（hello capabilities / iOS supportsProjectionWindow / 安装包身份）覆盖该链每一环 | ✓（R1 引 :375-394，函数体实际 :378 起，±3；语义一致） |
| iOS `ProjectionStore.swift:590` / `:145` | :590 `func windowCoverage(for sessionId: String)`（PERF-S4C 区）；:145 hard cap 文档首行、:152 `var pullHardCapNanoseconds: UInt64 = 15_000_000_000` | ✓ 精确 / ✓ |
| iOS `ProjectionStore.swift:763-779`（pullOpenWindowed 现状未传 limit） | :763-779 `pullWindow(sessionId:direction:)` 调用无 limit 参数；`.unsupported, .skipped → pullFull` 回落现状 | ✓ 精确 / ✓（§3.3「iOS 增加请求 limit 默认值」是真实增量；「禁止非 typed 错误静默 full」确有改造对象） |
| iOS `ChatViewModel+SessionManagement.swift:851` | :851 `let outcome = await self.projectionStore.pullOpenWindowed(sessionId: sessionId)` | ✓ 精确 / ✓ |
| Codex `thread_history_materialization.rs:42-45`（不重读 prefix） | :42-44 `let projection_state = super::thread_history::projection_state(store, thread_id).await?; let start_offset = projection_state.as_ref().map_or(0, \|state\| state.next_byte_offset);` | ✓ 精确 / ✓ |
| Codex `codex-rs/cloud-client/README.md:18`（F-7 修订） | :18 `` `Attach` has no history snapshot or replay guarantee.`` | ✓ 精确 / ✓（F-7 闭合） |
| Codex `app-server-protocol/…/v2/thread.rs:422` | :419-434 `exclude_turns` doc（"return only thread metadata … use this with `thread/turns/list` and `thread/items/list`"）+ `initial_turns_page` 字段 | ✓（doc 块内）/ ✓ |
| Mac `think.md:450 / :834 / :1205`；iOS `think.md:3343 / :3485 / :3491`（§2.5，F-6 修订） | :450「③iOS 27h 重连 91 次，每次重拉全量投影快照」；:834「15s 超时。审批卡本身是控制面部件」；:1205「gate 永不 ready → iOS 15s 超时重试死循环（ceffc9c）」；iOS :3343「历史页 RPC timeout 15s」；:3485 重连漏传 preferredBackendID；:3491 `rpc("get_session_messages", 15s)` 触发链 | ✓ 六条全命中 / ✓（「同症状不同根因、无冲突结论」的定性成立） |

**复用 R1（本轮未重读原文，复用依据：三仓提交哈希与 R1 时相同、Codex 干净、Mac 仅新增 docs 未跟踪文件、iOS 脏文件不含任何锚点文件——锚点文件内容身份未变）：**
`relay_conn.go:296`、`docs/protocol/relay-frame-compression.md:3`、`MessageCacheManager.swift:6`、`Storage/SessionSnapshotStore.swift:101`、`ChatViewModel+MessageSync.swift:98`、`Bridge/ProjectionWireDecoder.swift:9-17`、`CCCodeBridgeTransport.swift:39/51/1845-1850`（§3.5 五步 decode 现状）、Codex `0001_thread_history.sql:1`、`thread_processor.rs:5888`、`tui/…/history.rs:28`、`core/src/config/mod.rs:4050`、`thread_lifecycle.rs:18`、`model_context.rs:30`。R1 对上述各项均附原文摘录与命令，具备可核对依据。

### 2.4 覆盖声明

- 已核：方案 §0 修订范围表逐项 vs R1 意见；§2.1 三仓身份；§1.1 全部事故数字（本轮独立复测）；E-1/E-2 日志证据；E-8/E-9 的锚点（复用+抽检）；v2 新增全部锚点（F-1/F-3/F-7/F-8 闭合依据）；§2.5 think.md 六条；§2.4 关键矛盾（0 次 window RPC）；交接块双哈希记账。
- 未核（不影响结论）：截图 heic（无视觉授权；日志时间线已闭环）；生产 runtime 二进制的精确构建 commit（未探 management API——S-0 职责，方案已声明运行级/源码级需区分）；transcript JSONL 逐行 type 分布（R1 已测，本轮以 checkpoint turn 级测量 + 文件字节数交叉印证）。

## 3. R1 意见闭合表（复审处置）

| R1 意见 | 级别 | v2 修订处 | 本轮闭合判定 |
| --- | --- | --- | --- |
| F-1 preview cache 身份 key 与 epoch 行为未定义 | 阻塞 | §3.4.1 术语 + §3.4.2 epoch 分支 + §6 S-4 + §7.2.9 + §7.4 场景 4 | **闭合**。bridge ID/epoch 四锚点亲核全命中；「key 不含 epoch、epoch 只进 metadata 作 staleness」有安全性论证（preview 纯显示层不进 replica；compact/重写由权威 page 整体替换；输入框按权威状态）；场景 4 有可对照预期（不退 loading、不清 cache、升级 stale 标记）；且本轮附加核验 hello_ack 本就携带当前 epoch（server.go:780），判定输入在现有协议上可实现 |
| F-2 R-1 与 frozen v1 never-split-a-turn 偏差 | 阻塞 | §1.1 补实测 + R-1 改规则约束 + §7.3 行同步 | **闭合**。6,347,762 B 本轮逐字节复测吻合；R-1 措辞与 projection_window.go:24-26 语义一致；§7.1.1/§7.3 与 R-1 同口径，无「按字面验收即失败」组合 |
| F-3 重启 prefix 校验 IO 未定义 | 建议 | §3.2.3 策略 + §7.3 指标行 | **闭合**。(path,size,mtime) 快速匹配 + 失败才全量重算 + prefix_sha256 兜底持久化；两锚点（kernel :266-278 / Codex :42-45）亲核；重启预算入 §7.3 |
| F-4 首屏目标未分冷热 | 建议 | R-2 warm 前提 + §7.3 分行 + §7.4 状态记录 | **闭合**。冷场景第一阶段不并入达标判定、第二阶段并入，前提可复现 |
| F-5 OD-2 与 S-1 门控歧义 | 建议 | §3.3 / §4 / §5 三处对齐 | **闭合**。三处一致：S-1 按默认 limit 100 turns 开工，OD-2 只影响 limit 数值与 S-3 |
| F-6 未引用 think.md | 建议 | §2.5 | **闭合**。六条行号本轮全部亲核命中，定性准确 |
| F-7 Codex 路径不完整 | 建议 | §2.3 改 `codex-rs/cloud-client/README.md:18` | **闭合**。亲核 :18 精确 |
| F-8 测试矩阵缺两项 | 建议 | §7.1.1 / §7.2.8 / §7.3 | **闭合**。多设备并发（锚点 :93-99 亲核）、hydrating 重试（锚点 :124-126 亲核）、冷场景计入，均有预期结果 |
| F-9 v2 与 turn_detail 关系缺倾向 | 建议 | §3.3 冻结清单第 6 项 | **闭合**。倾向明确（演进/复用，不平行新建），推翻需显式论证 |

修订回归检查：v2 改动集中于 §0/§1.1/§1.2/§2.5/§3.2.3/§3.4/§5/§7，本轮重读了这些章节及其全部交叉引用（R-1↔§7.1.1↔§7.3 口径、OD-2↔Gate A↔§3.3、epoch↔§3.2.2 provenance 注记），未发现修订引入的新矛盾、悬空引用或能力范围变化；架构方向、切片划分、门结构未动（与 §0 声明一致）。

## 4. 意见

### F-10 [建议] 方案 §2.1 的 iOS 未提交文件枚举已随时间漂移（4 项 → 8 项）

- 位置：§2.1 iOS 行（任务简报字段 5 同样只列 4 项）。
- 证据：本轮 `git status --porcelain`（iOS 仓）实测 8 个路径：M `project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift`。方案列 3M+1??。
- 影响：新增 4 个路径（工程配置 + NativeTimeline UI/测试文件）与本方案的锚点、设计或实施范围**零重叠**——全部锚点文件在未修改状态，本轮全部核验（含 R1 复用项）对象身份未变，无结论被污染。但后续实施者若把 §2.1 清单当当前完整快照使用，会违反 repo P0 来源门「未提交状态逐项列出、三个门点重新生成」的精神。
- 修订方向：§2.1 iOS 行标注「时点快照」语义（或更新枚举），并一句声明 S-0 开工时按 P0 门重新生成来源清单。
- 闭合标准：下一版方案标注快照语义 / 补全清单，或由 S-0 切片的来源清单再生成义务显式承接（方案 S-0 本就含运行身份对齐，最小改动即一句声明）。

无其他新发现。本轮未发现需要编造凑数的意见：数字、锚点、门控、覆盖均核验通过。

## 5. §10 评审重点逐项结论（方案自列 7 项，本轮独立复核）

1. **S-1 是 P0 架构功能恢复而非补丁：成立（维持 R1 结论，本轮独立验证）。** 两端实现完整（Mac main.go:560 flag on + handler/producer 齐备；iOS pullOpenWindowed:763 完整且 capability-gated，亲核）；正式 runtime 二进制含 2 处 window RPC 符号；全日志 0 次 window RPC + req_18/req_33 均 full snapshot（亲核）→ 部署/协商断层而非功能缺失。E-3 pending 正确阻断 S-1 完成宣告；协商链（server.go:373-394，亲核）证明 E-3 三个取证方向覆盖完整。
2. **materialization store 放 Mac：正确。** Mac kernel 是唯一真相 writer（projection_window.go:9-13 分层注释亲核佐证「SLICES committed kernel snapshots…never reduces」）；物化放真相侧使多设备共享收益；与 Codex thread-store 服务端同构（0001 migration，复用 R1 + 本轮 thread.rs 抽检）。
3. **preview cache 不进 ProjectionReplica 的边界：足够强。** §3.4.2 五关键点（v2 增补第 5 点含安全性依据）+ §8 风险行 + E-7 + §7.2.5 seal 测试 + §7.2.9/§7.4.4 epoch 场景预期；R1 指出的唯一缺口（F-1）已闭合且经本轮源码级验证。
4. **Phase A turn_blob_json / Phase B item rows：可接受。** Phase A 行为闭环、回退边界明确（停用 store 回旧 checkpoint、不删除）；样本最大单 turn 6.35MB，SQLite 单行 blob 可行；写入瓶颈已入风险表并有 revision 下限约束。
5. **v2 与 turn_detail_lazy_v1/chunks_v1 关系：应重用/演进，方案的倾向（F-9 修订）正确**，且把最终定案放在协议冻结评审、推翻需显式论证——门控结构合理。
6. **OD-1/OD-2/OD-3 推荐满足产品语义且保持 pending**（B「last-good + syncing」符合 R-4 显式标记；无 TTL + identity/schema/容量淘汰与 last-good 语义一致；分阶段窗口大小合理）。本轮确认 v2 未替 owner 决定：三者在 §4 均为 pending，设计章节中的相应描述均为「推荐形态」而非既成事实。
7. **目标耗时与测试矩阵：v2 调整后无需再改。** 冷/热分行、window bytes 规则口径、多设备并发、hydrating 重试、重启 prefix 预算均已入表；每行可测。

## 6. 复审处置与剩余门

实施前仍未满足的门（与阻塞意见分开列示；pending 门不自动计入方案阻塞数）：

- **证据门**：E-3（window 未生效根因——S-1 修复前必须闭合）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时 signposts——S-5 完成宣告）；E-6（materialization 正确性——S-2 完成）；E-7（preview 不污染权威 writer——S-4 完成）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。
- 本轮新增记录事项：iOS 工作树脏文件枚举漂移（F-10）——S-0 开工时按 P0 门重新生成来源清单。

## 7. 评审过程记录

- 评审员模式 fresh：先独立形成风险清单（三仓锚点、样本数字、日志证据、门控、§10 七项、audit-plan 形状、哈希记账），独立完成本轮全部亲核后才读 R1 并逐条核对闭合；R1 仅作回归清单，未作为任何结论来源。
- 方案哈希派发时与读取时一致（报告头）；交接块正文哈希 `645819a0…` 以「正文 rstrip + `\n\n`」切分实测命中。
- 本轮唯一写入文件：本报告。方案、代码、其他文档一律只读；iOS 8 个未提交文件未触碰。
- 使用的契约：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（外部内容形状专项实际运行：checkpoint/transcript dump、字节测量、gzip 双级别、日志 grep，见 §2.2/§2.3）。

---

评审员交接块：

```text
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r2.md
scope: full
blockers: 0
advisories: 1
open_gates: E-3, E-4, E-5, E-6, E-7, OD-1, OD-2, OD-3, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
