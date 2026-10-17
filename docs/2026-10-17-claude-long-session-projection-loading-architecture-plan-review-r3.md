# 评审报告 R3：Claude 长 session 打开架构优化方案（v2 第三轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成亲核，后以 R1/R2 作回归清单核对）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 v2
方案 SHA-256（评审时实测，与派发记录、R2 报告头一致）：`9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18`
范围：full（全量第三轮）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **2**（F-11、F-12）
- advisories: **4**（F-13、F-14、F-15、F-16）
- 与 R2 的关系：R2 判 APPROVED。本轮**推翻**该结论——不是推翻 R2 已核事实（本轮独立复测其全部数字与锚点仍命中），而是 R2 的评审视野存在两处结构性盲区：只核对了「方案写了什么是否成立」，未核验「方案**没写**的既有链路约束是否会让设计失效」。两个阻塞项都属于后一类，且都落在 S-1/S-2 的核心路径上。

总体评价：方案的来源纪律、证据门结构、pending/verified 诚实度、R1 九条闭合质量均**维持成立**（本轮逐项复验）。但方案存在一个此前三轮均未触及的前提性缺陷：**`projection_window_v1` 的响应在 wire 上不携带 `execution` / `planMode` / `goal` 等会话级标量状态**，而 `window_0` 是 iOS 的**冷开唯一入口**。这意味着「S-1 让首开真正走 window」这一修复本身会引入 R-6 所禁止的状态丢失，而方案把 R-6 写成「保持现有语义」，未把它识别为需要新增设计的问题。第二个阻塞项是 S-2 的存储技术选型在仓内既有约束下不可直接落地（模块无 SQLite 驱动、构建走纯 Go 交叉编译、iOS 侧亦无 SQLite 栈），方案以「拟议新增 materialization.sqlite」一句带过，未给出依赖引入与构建链决策。

## 2. 来源与覆盖

### 2.1 来源身份（本轮独立复核，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，`11caa1332cf4ad56041f3935f3bc1a0ab4e36a82`；未跟踪 4 个 docs 文件（本方案 + R1 + R2 + R2-meta；本轮新增 R3 报告） | ✓ 一致（「唯一变更是本文档」为写入时点快照） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，`1e895ea9ef42bc83c287c3acd66c3d7616340b4a`；未提交 8 路径（M `project.pbxproj`、M `LiveTailFollowController.swift`、M `NativeTimelineViewController.swift`、M `TimelineStreamingMarkdownCell.swift`、M `LiveTailFollowControllerTests.swift`、?? `StreamingHeightPresentation.swift`、?? `NativeTimelineSmoothHeightTests.swift`、?? `StreamingHeightPresentationTests.swift`） | 🟡 枚举仍为 4 项（R2 F-10 已指出漂移，v2 未修订；本轮确认漂移持续存在且**锚点文件仍不在脏清单内**） |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，`ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | ✓ 一致 |
| 正式 Mac runtime | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，PID 8303 监听 8777；`strings` 含 `get_session_projection_window`（2 处）、`projection_window_v1`（3 处）、`protocol.capability_required`（1 处）；`Info.plist` `CFBundleShortVersionString=0.1.0`；management API `/internal/status` 返回 `version:"0.1.0"`、`registeredDrivers:[claude codex-remote dsh-web grokbuild opencode-web]`、`bridgeId:"brg_qyFyiSOtsTDmPyVLsMhyvQ"`、`uptime 6h22m` | ✓ 一致（方案正确保留「runtime 与源码 HEAD 不同、结论需分层」的告诫） |

配套工作树配对：iOS 仓 `git worktree list` 中该分支仅 `cordcode-ios-native-message-timeline` 一个工作树，配对唯一无歧义。

### 2.2 事故样本复测（audit-plan 专项）

本轮**不使用 R1/R2 的任何数字**，用 python3 从 checkpoint 独立重算。过程中发现一个**计量口径分歧**，必须显式记录（这本身是 R1/R2 均未披露的方法论缺口）：

| 口径 | 整棵 projection | 最大单 turn | 尾 17 turns |
| --- | --- | --- | --- |
| `json.dumps` 默认分隔符 + `ensure_ascii=True`（转义 CJK） | 40,663,541 B | 7,940,153 B | 4,632,303 B |
| `ensure_ascii=False` + 紧凑分隔符（≈真实 UTF-8 wire） | 34,336,858 B | 6,336,926 B | 3,790,518 B |
| `ensure_ascii=False` + 默认分隔符 | 34,427,361 B | **6,347,762 B** | 3,801,579 B |

R1/R2 使用的是第三行口径，数字可复现（6,347,762 B 逐字节吻合）。该样本含大量 CJK，`ensure_ascii=True` 会把每个中文字符膨胀为 6 字节 `\uXXXX`，导致整棵 projection 虚高 **18%**、最大单 turn 虚高 **25%**。方案 §1.1 与 R-1 全部字节结论**只有在这个未声明的口径下才成立**；Go 侧 `turnEncodedSizes` 用 `json.Marshal`（`go-bridge/projection_window.go:146-155`）输出的是真实 UTF-8、紧凑分隔符，更接近第二行。因此：

- 方案「4MiB 截到约 17 turns」在 Go 实际编码口径下**仍成立**（3,790,518 B < 4MiB）；
- 但「最大单 turn 6.35MB」在 Go 口径下是 **6,336,926 B**（差 10,836 B），结论不变；
- **真正的问题不在数字，而在于方案从未声明口径**。S-2/§7.3 的验收若用不同工具重算，会得到系统性偏离 18% 的结论，且无法判断是回归还是口径差异。见 F-15。

其余复测：checkpoint 文件 34,537,988 B；`len(turns)=119`；最大 turn 为 index 1（不在尾 17 内，`1 >= 119-17` → False）——**均与方案一致**。

日志复测（独立 grep，未复用 R1/R2 结论）：全日志 `get_session_projection_window` 计数 = **0**；`hydrate_failed` 计数 = **0**；req_18（17:30:54.827 mac_receive → 17:30:56.861 `elapsedMillis=1794` → 17:30:56.864 `outcome=snapshot`）、req_33（17:31:36.688，`connectionGeneration=2`）、req_54/req_58（17:32:23/25，`connectionGeneration=3`）均 `sinceRev=0 outcome=snapshot`。E-1/E-2 verified 成立，且**事故期间实际重试了 4 次**（req_18/33/54/58），比方案 §1.1 描述的「重新打开一至多次」更严重——这点对 R-3「正常 bounded path 不应触达」的验收强度有影响。

### 2.3 锚点核验表（本轮亲核）

**v2 新增与 R-1 关键锚点（全部亲核命中）：**

| 方案原引用 | 实际位置与原文摘录 | 定位 / 语义 |
| --- | --- | --- |
| §3.4.1 `data_dir.go:15-23`（bridgeId 持久） | :15 `identity.json — Bridge 身份（bridgeId, displayName, createdAt, runtimeVersion, protocol）`；:22-23 `BridgeID string \`json:"bridgeId"\`` | ✓ 精确 / ✓ |
| §3.4.1 `hello_handler.go:108-111` | :107-111 `HelloBridgeInfo` 注释 + `BridgeID string \`json:"bridgeId"\`` | ✓ 精确 / ✓ |
| §3.4.1 `runtime_startup.go:137-140` | :137-139 `generateBridgeEpoch returns a process-unique 128-bit random UUID… make recovery treat a restarted bridge as the old process` | ✓ 精确 / ✓ |
| §3.4.1 `main.go:158` | :158 `bridgeEpoch, err := generateBridgeEpoch()` | ✓ 精确 / ✓ |
| §1.1/§1.2 `projection_window.go:24-26`（never-split） | :24-26 `maxWindowEncodedBytes caps the encoded window payload accumulation (a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn)`；:27-31 `maxWindowTurns=256` / `maxWindowEncodedBytes=4<<20` / `defaultWindowTurns=100` | ✓ 精确 / ✓（R-1 v2 措辞与该语义一致） |
| §3.2.3 `projection_kernel.go:266-278`（prefix 整读） | :266 `func digestProjectionSourcePrefix(r io.Reader, cursor int64)`；:272 `n, err := io.CopyN(h, r, cursor)`；函数体止于 :278 | ✓ 精确 / ✓ |
| §2.2 `projection_kernel.go:280` / `:1123` | :280 `func (s *ProjectionCheckpointStore) LoadValidated(`；:1123 `checkpointHit := false` | ✓ 精确 / ✓ |
| §2.2 `main.go:560` | :560 `server.SetProjectionWindowEnabled(true)`；`git blame` → `ebab32f91` 2026-08-26 | ✓ 精确 / ✓ |
| §7.1.1 `projection_window_handler.go:93-99` | :93-99 `Same session-open side effects…` → `h.subscribeConnToSession(...)` | ✓ 精确 / ✓ |
| §7.2.8 `projection_window_handler.go:124-126` | :124 `case errors.Is(err, errProjectionHydrating):` / :125 `code = "projection.hydrating"` / :126 `retryable = true` | ✓ 逐字符精确 / ✓ |
| §2.2 `projection_window_handler.go:32` | :32-34 冻结门注释；:36 `if !h.eventPublisher.ConnProjectionWindowV1(conn)` | ✓（注释块起点）/ ✓ |
| §2.3 `codex-rs/cloud-client/README.md:18`（F-7 修订） | :18 `` `Attach` has no history snapshot or replay guarantee.`` | ✓ 精确 / ✓ |
| §2.3 `thread_history_materialization.rs:42` | :42-44 `projection_state` → `start_offset` | ✓ 精确 / ✓ |
| §2.5 think.md 六条（:450/:834/:1205 + iOS :3343/:3485/:3491） | 六条行号全部命中，内容与方案描述一致 | ✓ 全命中 / ✓ |
| iOS `ProjectionStore.swift:590` / `:145`/`:152` | :590 `func windowCoverage(for sessionId:)`；:145 hard cap 文档首行；:152 `var pullHardCapNanoseconds: UInt64 = 15_000_000_000` | ✓ 精确 / ✓ |
| iOS `ProjectionStore.swift:763-779`（未传 limit） | :763 `func pullOpenWindowed`；内部 `pullWindow(sessionId:direction:)` 调用无 limit；`.unsupported/.skipped → pullFull` | ✓ 精确 / ✓ |
| iOS `ChatViewModel+SessionManagement.swift:851` | :851 `let outcome = await self.projectionStore.pullOpenWindowed(sessionId: sessionId)` | ✓ 精确 / ✓ |
| iOS `Bridge/ProjectionWireDecoder.swift:9` | :9-17 `JSONSerialization.jsonObject` 全量 parse → 取 `data` → `JSONSerialization.data` re-encode → `JSONDecoder` decode | ✓ 精确 / ✓ |
| iOS `MessageCacheManager.swift:6` / `SessionSnapshotStore.swift:101` | :6 `actor MessageCacheManager`；:101 `let maxSnapshotBytes: Int`、:112 默认 10MiB | ✓ 精确 / ✓ |
| iOS `ChatViewModel+MessageSync.swift:98` | :98 `loadMessages` 参数行；:103 起 §10.5.3 ring 2 step 1 注释 | ✓（函数入口）/ ✓ |

**本轮新核、方案未引用但影响设计的锚点（支撑 F-11/F-12/F-13）：**

| 锚点 | 内容 | 用途 |
| --- | --- | --- |
| `go-bridge/projection_window.go:77-82` | `type ProjectionWindowResponse struct { Window; Turns; SyncRev; Resume }` — **无 `Execution`、无 `PlanMode`/`SessionMode`/`CollaborationMode`/`CodexGoal`/`Goal`** | F-11 核心 |
| `go-bridge/projection_window.go:226-241` | `sliceProjectionWindowWithUpstream` 只处理 `proj.Turns`；全函数（及 handler、older_hydrate）**零处引用 `Execution`** | F-11 核心 |
| `go-bridge/projection_snapshot_fence.go:230-234` | fence 完成后对非持有 turn 的排队 patch 执行 `resolved.Execution = nil` | F-11 加剧项 |
| iOS `SessionProjectionWindow.swift:96-99` | `emptySession(...)` 硬编码 `execution: SessionExecutionView(phase: .idle, activeTurnId: nil)`；`applyingWindowPage` 全程**只处理 `turns`**，从不写 `execution` | F-11 核心 |
| iOS `ChatViewModel.swift:1227-1233` / `:2419-2426` / `ChatViewModel+TaskDock.swift:33` | 执行态、输入框锁、`composerAwaitingUserAction` 全部读 `projection.execution.phase` | F-11 影响链 |
| `go.sum` / `go.mod` | **无任何 SQLite 驱动**（无 `modernc.org/sqlite`、无 `mattn/go-sqlite3`） | F-12 核心 |
| `MacBridge/project.yml:53` | `GOOS=darwin GOARCH="$GO_ARCH" go build -trimpath -ldflags "$RUNTIME_LDFLAGS" -o ...` — 未设 `CGO_ENABLED` | F-12 核心 |
| `agent/grokbuild/session_catalog.go:14-16` | 注释明写「SQLite is queried via the optional `sqlite3` CLI … to avoid adding a CGO/pure-Go sqlite dependency to the main MacBridge module」 | F-12 既有先例 |
| `agent/opencode-web/import_guard_test.go:23-24` | `"sqlite"+"3": "direct storage access"` 被列为 forbidden surface | F-12 既有约束 |
| iOS `OpenCodeiOS/project.yml:14-24` + `grep -r "import SQLite\|GRDB"` | `project.yml` 只声明 2 个 vendored package（MarkdownView / BorderBeamKit；`Packages/` 目录下另有其 sibling DisplayLink、Litext），**零 SQLite 栈** | F-12 加剧项 |
| `go-bridge/server.go:373-395` | `negotiateProjectionWindowV1`：只检查客户端声明 + `s.projectionWindowEnabled`，**不检查 backend descriptor** | F-13 |
| `docs/protocol/bridge-v1.md:2173-2175` | 「Per-backend support is additionally reported on the backend descriptor's `capabilities` list; clients MUST gate window RPCs on the descriptor list, not the global echo」 | F-13 |
| `go-bridge/backend_capabilities.go:5-...` + `grep "\"projection_window_v1\""` | `deriveBackendCapabilities` 与全仓非测试代码**均未把 `projection_window_v1` 写入 descriptor** | F-13 核心 |
| iOS `CCCodeBridgeBackendClient.swift:1308-1310` | `supportsProjectionWindow` 只查 `transport.supportsAcknowledgedCapability("projection_window_v1")`，**不查 descriptor** | F-13 影响 |
| `go-bridge/projection_window_handler.go:102-106` | window 的 `forceColdInspection` 对 claude/opencode/opencode-web/grokbuild/deepseek/dsh-web/codex-web **无条件为 true**（与 full path 的 `params.SinceRev == 0 &&` 不同） | F-14 |
| `go-bridge/handlers_projection.go:598-602` | `sourceChanged := forceColdInspection && ready && (opencode/grokbuild/... ) && source.Path == ""`；Claude 走 composite Segments 分支 | F-14 |
| `go-bridge/projection_kernel.go:1030-1045` | Ready 分支：Segments 有变化 → `break` 走重建；`source.Path != "" && source.Cursor > committed` → `break` | F-14 语义 |
| `agent/claudecode/continuation.go:379-392` | `richHistoryTranscriptSegments` 只 `os.Stat` 每个 segment，不读内容（轻量） | F-14 降级依据 |
| `go-bridge/handlers_projection.go:915-954` | `projectionJSONLStartCut` 只做尾部 4KiB 反向扫描定位换行（**不是**整读） | F-14 降级依据 |

### 2.4 覆盖声明

- 已核：方案 §2 全部锚点；§1.1 全部事故数字（本轮独立重算并披露口径分歧）；E-1/E-2 日志证据（独立 grep，发现 4 次重试而非「一至多次」）；v2 新增锚点全部（F-1/F-3/F-7/F-8 依据）；§2.5 think.md 六条；§2.4 关键矛盾；交接块双哈希（`9b97736b…` 与正文 `645819a0…` 均实测命中）；R1 九条闭合表逐条复核；R2 F-10 漂移复核。
- **本轮新增核验面（R1/R2 均未覆盖）**：window 响应的**字段完备性**（vs `SessionProjection` 的 scalar state）、Go 模块的 SQLite 依赖与 CGO 构建链、iOS 的持久化技术栈、descriptor capability 与 hello echo 的双门关系、window 与 full 两条路径的 `forceColdInspection` 条件差异。
- 未核（不影响结论）：截图 heic（无视觉授权；日志时间线已闭环）；生产 runtime 的精确构建 commit（未探 runtime 内部；S-0 职责）；transcript JSONL 逐行 type 分布（以 checkpoint turn 级测量 + 文件字节数交叉印证）。

## 3. 意见

### F-11 [阻塞] `window_0` 冷开不携带会话级标量状态（`execution` / `planMode` / `goal` 等），S-1 一旦生效即违反 R-6

- 位置：§1.2 R-6（「窗口化不得丢失 active turn、permission card、执行状态和 patch 顺序」）；§3.3 Phase A（「恢复并强制使用既有 `projection_window_v1`」）；§3.1 不变量 2；§7.1.1 验收项；§8 风险表。
- 证据（全部亲核）：
  1. Mac 侧 `ProjectionWindowResponse`（`projection_window.go:77-82`）字段只有 `Window` / `Turns` / `SyncRev` / `Resume`。对比 `SessionProjection`（`projection_types.go:341-359`）有 `Execution`、`PlanMode`、`SessionMode`、`CollaborationMode`、`CodexGoal`、`Goal`。`sliceProjectionWindowWithUpstream`（`:226-287`）与 `projection_window_handler.go`、`projection_window_older_hydrate.go` **零处**引用 `Execution`（`grep -n "Execution" 三文件` → 无输出）。
  2. iOS 侧 `applyingWindowPage`（`SessionProjectionWindow.swift:116-176`）只合并 `turns`；`emptySession`（`:96-98`）把 `execution` 硬编码为 `.idle`。`ProjectionReplica.applyWindowPage`（`ProjectionReplica.swift:298-390`）构造的 `IncrementalProjectionChangeSet` 全部 `execution: nil`、`executionChanged: false`。
  3. fence 完成后对「不持有该 patch turn」的窗口连接，`completeProjectionSnapshot` 显式执行 `resolved.Execution = nil`（`projection_snapshot_fence.go:234`）——即后续 live patch 也不补 execution。
  4. 消费侧：`isSessionExecuting`（`ChatViewModel.swift:2419-2426`）、输入框锁、`composerAwaitingUserAction`（`ChatViewModel+TaskDock.swift:33`）、session badge（`ChatViewModel.swift:1227-1233`）全部读 `projection.execution.phase`。
- 影响：`window_0` 是 `pullOpenWindowed` 的**唯一冷开入口**（`ProjectionStore.swift:763-777`）。S-1 修好协商后，长 session 首开将：turn 内容正确，但 `execution` 恒为 `.idle`。后果是**运行中的 Claude session 在 iOS 上显示为「空闲」**——不显示执行中、输入框不禁用、`requires_action`（权限卡/提问）不锁发送。R-6 明写「不得丢失 active turn、permission card、执行状态」，而方案把 R-6 当作「保持现有语义」的被动目标，没有把它识别为 window 路径的**新增缺口**。更严重的是：方案 §7.1.1/§7.2 的验收项里没有任何一条会捕捉到这个缺失——它们只验 turn 集合与 cursor，不验 scalar state。这正是 R2 的盲区：R2 核了「`window_0` 能返回正确 turns」，未核「它返回的东西是否够 iOS 渲染一个完整的 session」。
- 缓解事实（避免夸大）：iOS 有独立于 projection 的 `runtimeStateStore` badge 路径（`ChatViewModel+TaskDock.swift:36-39`）与 `isGenerating` 本地状态，因此**不是完全无显示**；但 `composerAwaitingUserAction` 与 `isSessionExecuting` 的 projection 分支会持续给出 `.idle`，与 badge 路径产生**双源不一致**，且方案 §3.1 不变量 2「单一 iOS timeline writer」并未覆盖 scalar state 的写入责任归属。
- 修订方向（三选一，需在方案层定案）：① Phase A 的 window 响应 additive 携带 `execution` + 其余 scalar 字段（属 wire 变更，需评估 frozen v1「additive 不破坏旧 peer」是否允许，并同步 protocol pack）；② window 页到达后由 iOS 追加一次轻量 scalar 拉取（需定义新 RPC 或复用现有字段，且不得退回 full projection）；③ 明确把 scalar state 交由既有 `runtimeStateStore` 单源承担，并显式修改 R-6 措辞、补一条验收项证明「window 路径下执行态/权限卡不丢失」。无论哪种，都必须在 §7.1/§7.2 增加**针对 scalar state 的定向验收**（如：running session 经 window 冷开后 `isSessionExecuting == true`、`requires_action` 时输入框禁用）。
- 闭合标准：方案明确 window 路径的 scalar state 来源与写入责任，R-6 有对应的可测验收项，且不依赖 full projection 兜底。

### F-12 [阻塞] S-2 的 SQLite 选型与仓内既有构建/依赖约束冲突，方案未给出依赖与构建链决策

- 位置：§3.2.2（「拟议新增 `materialization.sqlite`」+ 概念 schema）；§3.2.3；§6 S-2 前置条件（「DDL + 测试计划」）；§8 风险表（只提「SQLite 写入成为 live bottleneck」，未提依赖引入）。
- 证据（全部亲核）：
  1. `go.mod` / `go.sum` **无任何 SQLite 驱动**（`grep "modernc.org/sqlite\|mattn/go-sqlite3"` → 空）。引入即新增第三方依赖，属供应链与 `govulncheck` 门（CI 已跑）的实质变更。
  2. `MacBridge/project.yml:53` 的 runtime 构建命令**未设 `CGO_ENABLED`**；`mattn/go-sqlite3` 需 CGO，会改变交叉编译前提并影响 Xcode 构建脚本。若选 `modernc.org/sqlite`（纯 Go）则无 CGO 问题，但体积与性能特征需评估——方案对二者均未提及。
  3. 仓内已有**明确的相反先例**：`agent/grokbuild/session_catalog.go:14-16` 注释写「SQLite is queried via the optional `sqlite3` CLI … to avoid adding a CGO/pure-Go sqlite dependency to the main MacBridge module」；`agent/opencode-web/import_guard_test.go:23-24` 更把 `sqlite3` 列为 forbidden surface。方案要新增 SQLite，却未说明为何推翻这两条既有纪律。
  4. iOS 侧 `ProjectedPreviewCacheStore`（§3.4.1）同样指定 SQLite，但 iOS 仓只有 4 个 vendored package，**零 SQLite 栈**（`grep -r "import SQLite\|GRDB"` → 空）。这意味着 S-4 也要新增依赖。
- 影响：S-2 与 S-4 各自新增一个跨平台持久化依赖，但方案的 Gate A 只要求「冻结 DDL 和不变量测试清单」，**没有任何门要求先裁决存储技术选型**。实施者会在 S-2 开工时自行决定 `modernc.org/sqlite` / `mattn/go-sqlite3` / `sqlite3` CLI 子进程 / 自研 append-only 日志（方案 §3.2.1 的诉求「可增量追加、可分页查询」用 append-only segment + 内存索引也能满足，未必需要 SQLite）。这是一个会污染整个第二阶段、且回退成本高的决策，不应留给实施期。
- 另需注意：§3.2.1 论证的是「不该用单个大 JSON checkpoint」，但**没有论证「必须用 SQLite」**。结论与手段之间存在跳跃。
- 修订方向：① 在 §3.2.2 增加「存储选型决策」小节，列出候选（纯 Go SQLite / CGO SQLite / `sqlite3` CLI / append-only segment + 索引）及对 `go.mod`、CGO、Xcode 构建脚本、CI `govulncheck`、iOS 依赖的具体影响；② 说明与 `session_catalog.go` / `import_guard_test.go` 既有纪律的关系（推翻或规避，需显式理由）；③ 把「存储选型定案」加入 Gate A（与 DDL 冻结同级）；④ iOS 侧 S-4 同样补选型（或明确改为复用现有文件型 `SessionSnapshotStore` 的演进版）。
- 闭合标准：S-2/S-4 的存储技术选型有明确决策与理由，含依赖/构建/CI 影响评估，且纳入开工门。

### F-13 [建议] window capability 只做了 hello echo 单门，与 protocol pack 的「双门」规范不一致，且 E-3 的取证清单未含此项

- 位置：§2.2「window capability gate」行（`projection_window_handler.go:32`）；§2.4 关键矛盾；§5 E-3 取证方向（「hello capabilities、iOS `supportsProjectionWindow`、安装包身份」）。
- 证据：
  - `docs/protocol/bridge-v1.md:2173-2175` 明写：「Per-backend support is additionally reported on the backend descriptor's `capabilities` list; **clients MUST gate window RPCs on the descriptor list, not the global echo**」。
  - 但 `negotiateProjectionWindowV1`（`server.go:377-395`）只检查客户端声明 + `s.projectionWindowEnabled`，**不检查 backend descriptor**；`deriveBackendCapabilities`（`backend_capabilities.go`）与全仓非测试代码**从未把 `projection_window_v1` 写进 descriptor**（`grep "\"projection_window_v1\"" go-bridge/*.go` 仅命中 `server.go` 三处）。
  - iOS 侧 `supportsProjectionWindow`（`CCCodeBridgeBackendClient.swift:1308-1310`）同样只查 `transport.supportsAcknowledgedCapability`，不查 descriptor。
- 影响：这是**规范与实现的既有分歧**（不是本方案引入）。但方案 §2.2 把该 gate 写成「复用；未协商连接 fail-closed」，读起来像完整门；§5 E-3 的三项取证方向也不含「descriptor 是否应广告」。若 S-1 修复后行为仍异常，这条分歧会被误判为已排除。属建议级：不阻塞 S-1，但应在 E-3 取证清单中补一项，或显式声明「已知偏离、本次不改、理由」。
- 修订方向：§2.2 该行补注「实现仅 hello echo 单门，与 `bridge-v1.md:2173-2175` 的 descriptor 双门规范存在既有偏离；S-1 不改，登记为已知项」；E-3 取证方向补「descriptor capabilities 是否含 projection_window_v1」。
- 闭合标准：该偏离在方案中有记录，E-3 取证清单覆盖 descriptor 维度。

### F-14 [建议] window 路径的 `forceColdInspection` 与 full 路径条件不一致，S-2 的「重启后不整读」预算可能被这条路径吃掉

- 位置：§3.2.3「重启后首开 Mac 侧时间预算」（「快速匹配命中时 IO 为 O(head/segments 行读取)，不整读 97MB prefix」）；§7.3「Mac restart 首开」行。
- 证据：
  - full path（`handlers_projection.go:122`）：`forceColdInspection := params.SinceRev == 0 && (backend ∈ {...})` —— 只在**冷拉**时触发。
  - window path（`projection_window_handler.go:102-106`）：`forceColdInspection := params.SessionID != "" && (backend ∈ {...})` —— **无 `SinceRev` 条件**（window RPC 本身没有 `sinceRev` 参数），因此**每次 window_0 都强制冷源检查**。
  - 该检查在 Claude 上会走 `prepareProjectionHydrateSource` → `RichHistoryTranscriptSegments`（两次调用，含 membership 一致性复核）+ `projectionJSONLStartCut`（每 segment 一次尾部扫描）。
- 缓解事实（避免夸大）：`richHistoryTranscriptSegments`（`continuation.go:379-392`）只做 `os.Stat`，不读内容；`projectionJSONLStartCut`（`handlers_projection.go:915-954`）只反向扫 4KiB 块找换行。**两者都不是整读 97MB**。真正的成本在 `BeginHydrateTransaction` 的 Ready 分支（`projection_kernel.go:1030-1045`）：Segments 无变化且 cursor 未前进时返回 `AlreadyReady`，不重建。
- 影响：因此 F-3 所担心的「整读 prefix」在**稳态**下不会发生；但在 **transcript 有 append 后首次 window_0**（`source.Cursor > committedSourceCursor`）会走 catch-up 重建，此时 §3.2.3 的「快速匹配命中即只折叠 suffix」是否生效、是否仍会触发 `LoadValidated` 的 `validateProjectionSourceCheckpoint`（`:389-415`，内含 `digestProjectionSourcePrefix` **整读 prefix**）取决于 store 是否已替换。方案 §3.2.3 只描述了「重启后首开」的 segment 快速匹配，**未覆盖「window 每次强制冷检 + append 后 catch-up」这条组合路径**，而它恰好是长 session 最常发生的场景。
- 修订方向：§3.2.3 与 §7.3 补一行，说明 window 路径的 `forceColdInspection` 无 `sinceRev` 门、每次 window_0 都会触发冷源检查，并明确 S-2 落地后该路径的 IO 上界（或提出给 window handler 补一个「kernel 已 Ready 且 source 未变则跳过冷检」的等价门）。
- 闭合标准：§3.2.3/§7.3 覆盖「window 强制冷检」这条路径的 IO 行为。

### F-15 [建议] 全部字节指标未声明 JSON 编码口径，样本含大量 CJK 时不同口径相差 18–25%

- 位置：§1.1（全部字节数字）；R-1；§7.1.3（`page encoded bytes`）；§7.3（`window encoded bytes` 行）。
- 证据：见 §2.2 表。同一 checkpoint 在 `ensure_ascii=True`（默认）下整棵 projection 为 40,663,541 B、最大 turn 为 7,940,153 B；在 `ensure_ascii=False` + 默认分隔符下为 34,427,361 B / 6,347,762 B。方案数字对应后者，但**从未声明**。Go 生产侧 `turnEncodedSizes`（`projection_window.go:146-155`）用 `json.Marshal`，输出真实 UTF-8 + 紧凑分隔符，对应 §2.2 第二行（34,336,858 B / 6,336,926 B）。
- 影响：S-1/S-2 的验收会同时用 Go 测试（真实编码）与脚本（可能默认 `ensure_ascii=True`）度量，两侧系统性偏离 18%。验收员无法判断 4MiB 阈值是否被突破，或把口径差异当成回归。考虑到 R-5 的 4MiB 是**硬边界**，口径不明会直接影响「是否 fail」的判定。
- 修订方向：§1.1 与 §7.3 的字节指标统一标注「wire 口径 = UTF-8 不转义 + 紧凑分隔符（等价 Go `json.Marshal`）」，并说明与脚本默认口径的差异。
- 闭合标准：字节类指标带可复现的编码口径说明。

### F-16 [建议] 事故期实际发生 4 次 full snapshot 重试，R-3 的「不应触达」需要更强的验收项

- 位置：§1.1 第 3 点（「重新打开一至多次后才成功」）；§1.2 R-3；§7.3（`15s timeout | 0 次`、`full projection open | 0 次`）。
- 证据：独立 grep 全日志，同一 session `bbe396e6` 在 17:30:56 / 17:31:36 / 17:32:23 / 17:32:25 共 **4 次** `sinceRev=0 outcome=snapshot`，`connectionGeneration` 依次 1→2→3→3。方案描述为「一至多次」，弱化了实际严重度；而 §7.3 的验收行只写「0 次 timeout / 0 次 full open」，未约束**重试次数**。
- 影响：R-3 说「超时后重试也只重试有界 page」，但验收表没有对应指标。若 bounded path 生效后仍有 2–3 次重试但每次都成功，当前表格会判「通过」，而用户体验仍是「开好几次才出来」——这正是事故的主诉之一。
- 修订方向：§7.3 增加一行「单次打开内 full-snapshot / window_0 重试次数 = 0（或 ≤1 且需给出理由）」，并在 §7.4 矩阵记录每次打开的重试计数。
- 闭合标准：验收表包含重试次数指标。

## 4. §10 评审重点逐项结论（方案自列 7 项）

1. **S-1 应作为 P0 架构功能恢复而非补丁：方向成立，但方案低估了它的范围。** 两端实现完整、正式 runtime 含能力符号、事故 0 次 window RPC——这些经本轮独立复测全部成立。但「让首开走 window」不是纯协商修复：由于 F-11，它同时是一次**功能语义变更**（首开从「full projection 含全部 scalar state」变成「window 页只有 turns」）。S-1 的范围必须扩到 scalar state，否则它把 34MB 问题换成了状态丢失问题。
2. **materialization store 放 Mac：正确（维持 R1/R2 结论）。** Mac 是唯一真相 writer，物化放真相侧使多设备共享收益，与 Codex thread-store 同构。但**存储手段**的选型未定（F-12），这不影响「放 Mac」的判断，影响的是 S-2 能否按当前描述开工。
3. **preview cache 不进 `ProjectionReplica` 的边界：足够强（维持 R2 结论）。** §3.4.2 五关键点 + E-7 + §7.2.5 seal 测试 + epoch 语义（F-1 修订）经本轮复验成立。**但需注意**：preview cache 若只缓存 window page 的 turn rows（§3.4.1「内容：最近 authoritative window page 的 turn rows」），它同样不含 scalar state——与 F-11 是同一缺口的两个表现，修复 F-11 时应一并覆盖 preview 的 scalar 字段。
4. **Phase A `turn_blob_json` / Phase B item rows：可接受（维持 R2 结论）。** 行为闭环、回退边界明确、样本最大单 turn 6.34MB（本轮口径复测）SQLite 单行 blob 可行。依赖 F-12 的选型定案。
5. **v2 与 `turn_detail_lazy_v1`/chunks 关系：倾向正确（维持 R2 结论）。** 「演进/复用、不平行新建」的倾向与「推翻需显式论证」的门控结构合理。
6. **OD-1/OD-2/OD-3 推荐：满足产品语义且保持 pending（维持 R2 结论）。** 本轮确认三者在 §4 仍为 pending，设计章节描述均为推荐形态。**新增建议**：F-11 的三种修复路线中，②「window 后追加 scalar 拉取」会引入一次额外往返，可能影响 R-2 的 ≤3s/≤5s 目标，建议在 OD 裁决时把该影响一并纳入。
7. **目标耗时与测试矩阵：需再调整。** 除 R2 已覆盖的冷热分行、多设备并发、hydrating 重试外，本轮要求补：scalar state 定向验收（F-11）、重试次数指标（F-16）、字节口径声明（F-15）、window 强制冷检的 IO 上界（F-14）。

## 5. 复审处置与剩余门

### R1 九条闭合复核（本轮独立重判，未直接采信 R2）

| R1 意见 | 级别 | 本轮判定 |
| --- | --- | --- |
| F-1 preview cache 身份 key 与 epoch 行为 | 阻塞 | **闭合**。bridge ID/epoch 四锚点本轮亲核全命中；§3.4.2 epoch 分支有安全性论证；§7.4 场景 4 有可对照预期；`server.go:780` 的 `ack.BridgeEpoch` 使判定可实现。 |
| F-2 R-1 与 frozen v1 never-split 偏差 | 阻塞 | **闭合（数字需补口径）**。6,347,762 B 在本轮复现（默认分隔符 + `ensure_ascii=False` 口径）；R-1 措辞与 `projection_window.go:24-26` 语义一致，无「按字面验收即失败」组合。但口径未声明，见 F-15。 |
| F-3 重启 prefix 校验 IO | 建议 | **部分闭合**。(path,size,mtime) 快速匹配 + 失败才全量重算的策略已写入；但未覆盖 window 路径的强制冷检组合（F-14）。 |
| F-4 首屏目标未分冷热 | 建议 | **闭合**。R-2 带 warm 前提，§7.3 冷热分行，§7.4 要求记录状态。 |
| F-5 OD-2 与 S-1 门控 | 建议 | **闭合**。§3.3/§4/§5 三处一致：S-1 按默认 limit 100 开工。 |
| F-6 未引用 think.md | 建议 | **闭合**。§2.5 六条行号本轮全部命中。 |
| F-7 Codex 路径不完整 | 建议 | **闭合**。`codex-rs/cloud-client/README.md:18` 精确。 |
| F-8 测试矩阵缺两项 | 建议 | **闭合**。多设备并发、hydrating 重试均入表且有预期。 |
| F-9 v2 与 turn_detail 关系 | 建议 | **闭合**。倾向明确。 |

### R2 F-10 复核

**未闭合，且漂移持续。** v2 文档 §2.1 仍列 4 项，实际 8 项（本轮 P0 门重生成实测）。R2 已判其为建议级，且确认全部锚点文件不在脏清单内，因此**不影响任何已核命题**。但按 repo P0 门「未提交状态逐项列出、三个门点重新生成」的要求，建议 S-0 开工时以当时实测清单为准，并在此处标注「时点快照」。本轮将其并入 F-13 同级处理（不再单列）。

### 实施前仍未满足的门

- **证据门**：E-3（window 未生效根因——S-1 修复前必须闭合；本轮建议补 descriptor 维度，见 F-13）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时 signposts——S-5 完成宣告）；E-6（materialization 正确性——S-2 完成）；E-7（preview 不污染权威 writer——S-4 完成）。
- **决定门**：OD-1、OD-2、OD-3（owner 裁决；OD-1/OD-3 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。
- **本轮新增决定门**：F-11 的 scalar state 修复路线（属产品语义取舍，需 owner 或方案层明确）；F-12 的存储技术选型（建议加入 Gate A，与 DDL 冻结同级）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 6. 评审过程记录

- 评审员模式 fresh：先独立形成风险清单（三仓锚点、样本数字、日志证据、门控、§10 七项、audit-plan 形状、哈希记账），独立完成本轮全部亲核后才读 R1/R2 并逐条核对闭合；R1/R2 仅作回归清单，未作为任何结论来源。
- **本轮与 R2 的分歧记录**：R2 判 APPROVED，本轮判 REVISION_REQUIRED。分歧根因是评审视野而非事实冲突——R2 的 §2.3 锚点表与 §2.2 数字本轮全部复现成立。R2 未覆盖的三类核验面（window 响应字段完备性、Go/iOS 依赖与构建链、window vs full 路径的条件差异）是本轮两个阻塞项的唯一来源。
- 方案哈希：派发记录、R2 报告头、本轮实测三方一致（`9b97736b…`）；交接块正文哈希 `645819a0…` 以「正文 rstrip + `\n\n`」切分实测命中。
- 本轮唯一写入文件：本报告。方案、R1/R2/R2-meta、三仓源码、iOS 8 个未提交文件一律只读未触碰。
- 使用的契约：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（外部内容形状专项实际运行：checkpoint dump、三种编码口径字节测量、gzip、日志 grep；本轮**额外披露了口径分歧**，见 §2.2）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 9b97736b1af438ebda37fb6f093cbdb10561e055530b17abda9d5cb9005b8b18
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r3.md
scope: full
blockers: 2
advisories: 4
open_gates: E-3, E-4, E-5, E-6, E-7, OD-1, OD-2, OD-3, F-11 scalar-state route decision, F-12 storage selection, S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
