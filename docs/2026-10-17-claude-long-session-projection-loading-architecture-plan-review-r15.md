# Claude 长 session 打开方案复审 R15

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**。
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，**v19**。
- 全文 SHA-256：`87efb0fc42a203ed61747fe194d4ec2cc17e9a466be5a891de55aa2ab596f013`。
- 正文 SHA-256：`776bfb65d90b7b8e3276d2e6fd1b1d123c14bfe938883c3312e655eed4ceca04`，按方案交接块切分规则复算，与自报值一致。
- scope: **R14 五项闭合复核 + v19 新设计的组合行为、门控和验收回归**；不是重新枚举全部上游功能或重跑历轮所有测试。
- blockers: **5**：原 F-42、F-44、F-45 未完全闭合；新增 F-46、F-47。advisories: **0**。
- implementation_readiness: S-2 的可信恢复/完整 IO 上界、S-4/S-7 的对账和采用事务仍不可定稿。S-0 只读取证不受影响；本轮没有发现 S-5/S-8 opcode 方案的新阻塞，也没有授权实施。

**F-41 的主要路径冲突已闭合，F-43 的锚点刷新/分页前进语义已闭合。**baseDigests 可以检查 iOS 缓存是否与一个正确的 Kernel 一致，但不能检查 Kernel 自己是否正确地反映了 transcript。因此“baseDigests 与 growth-trust 互相兜底”不成立：它们共享同一个可能出错的基底，并非两个独立校验层。

## 本轮来源与证据

按方案 §2.1 指定配对，分别枚举两仓 worktree 后选择 native-message-timeline；源码分析前 G1、首次写报告前 G2 重新核对如下。任务预期产品能力为 Claude SSV2、拟议 full+gzip 首开、缓存对账+anchor 增量、Kernel/Replica 唯一 writer 与增量物化；不构建或部署。

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态、预期来源与配套 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | G1/G2 同下列逐项清单；方案指定来源；配套 I 的本行表述路径/分支/完整提交 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `8b6e44f9bdfb5ad5e5b708de8450a1506c7c3adc` | G1/G2 **干净**；方案指定来源；配套 M 的上述路径/分支/完整提交 |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897` | 干净；方案指定只读架构参考，关联 M/I；不代表目标运行二进制版本 |

M 的 G1/G2 未提交清单：

```text
 M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r12.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r13.md
?? docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r14.md
?? docs/2026-10-17-incremental-append-feasibility-report.md
```

已完整重读 R14 报告，SHA-256 为 `01e856910313a8473e1d4b1368904774f59a8ecbe7f31ef3bb275783a41805dd`。这是同一评审员的延续复审，不宣称无历史影响的 fresh 审查，没有创建额外代理。

I 从 R14 的 `e52daa275946ee0d41749aa7146ec8bbc8b05c29` 到本轮提交只改变四个文件：`CHANGELOG.md`、`OpenCodeiOS/OpenCodeiOS/App/NativeTimeline/NativeTimelineViewController.swift`、`OpenCodeiOS/OpenCodeiOSTests/NativeTimelineSmoothHeightTests.swift`、`docs/2026-10-17-native-timeline-smooth-height-growth-plan.md`。本轮使用的 replica/store/window/transport/presentation 源码及指引不在该 diff 内。M/U 提交与 R14 一致；旧低风险事实可复用，本轮高风险锚点仍重新打开亲核。

没有重新核验已安装 app/runtime/手机身份，不使用方案中 PID 8303 的历史记录证明运行态；没有构建、安装、重启或外部账号操作，不触发构建/部署 G3。

### 亲核锚点表

I 的文件路径以 `OpenCodeiOS/OpenCodeiOS/` 为根；其余带仓库相对路径。原引用保持方案写法；新增调查位置明确标注，不冒充原引用。

| 方案命题 / 原引用 | 实际位置与最小源码摘录 | 定位结果 / 语义结果 |
| --- | --- | --- |
| §3.4.2.5：window 用 `BeginProjectionSnapshot`，原引 `projection_window_handler.go:168` | M 该文件 `:168`：`proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(`；M `projection_snapshot_fence.go:123-124`：`cutRev: projection.SyncRev, expectedRev: projection.SyncRev` | 原定位准确；仅保证 post-cut，不是离线改动重放。v19 承认原错误，方向正确 |
| §3.8：严格后继复用 `projection_window.go:278-296` | M `go-bridge/projection_window.go:290`：`growWindowRange(sizes, limit, boundary+1, false)`；`:192-207` 从传入 boundaryIndex 向后增长并返回 | 原定位准确；与 v19 `turns` 排他、另发 anchorRefresh 的语义相容 |
| §3.4.3：`ProjectionReplica.swift:193-212/:199` | I `Models/ProjectionReplica.swift:199`：`projection.syncRev < current.syncRev`；`:207-208`：`state.projection = projection; state.appliedRev = projection.syncRev` | 定位准确；改写副本可消除旧大 rev 的初始污染，但不能自动解决后续空页 head |
| §3.4.3：`SessionProjectionWindow.swift:123-126/:173` | I 该文件 `:123-126`：`guard !page.turns.isEmpty else { ... return self }`；`:173`：`copy.syncRev = max(syncRev, page.syncRev)`；I `ProjectionReplica.swift:350-351`：`state.projection = merged; state.appliedRev = page.syncRev` | 定位准确；支持本轮 F-44 的 0/head 分裂反例 |
| §3.4.3：新 epoch 来源 `server.go:780`；revisionKey `SessionProjection.swift:696` | M `go-bridge/server.go:780`：`ack.BridgeEpoch = s.bridgeEpoch`；I `Models/SessionProjection.swift:696`：`var revisionKey: String { "\(bridgeEpoch ?? "")|\(syncRev)" }` | 两处定位/语义准确；缓存 provenance 与当前身份分离方向保留 |
| §3.4.3：MainActor 串行即可防事务交错（原引 store 调用点 `:620-626/:1044-1056`） | I `Models/ProjectionStore.swift:612-619`：`await replica.applyWindowPage(...)` 后 `await commitIfObserved(...)`；`:1180`：`let presentation = await presentationEngine.apply(...)`，`:1197-1203` 才写 store State | 原调用点成立；“@MainActor 自动保证跨 await 事务不交错”的语义不成立，需额外 session 事务门 |
| §3.8：digest 随 turn/page 下发、客户端原样存储；无 production digest 锚点（拟议） | M `go-bridge/projection_types.go:373-379`：`BaseRev/SyncRev/Execution/UpsertTurns/PartOps/TurnStateOps`；I `Models/ProjectionStore.swift:1241-1243`：依次 apply upserts/stateOps/partOps | 现有 live 内容会局部改变；新 token 尚无实现，不据此反对计划新增 token，但其生命周期必须覆盖此路径（F-42） |
| §3.2.3：`projection_kernel.go:407` 为全 prefix digest | M `go-bridge/projection_kernel.go:407`：`digestProjectionSourcePrefix(f, checkpoint.Cursor)`；`:1129`：`store.LoadValidated(...)`；`:1138/:1151` 采用 checkpoint 的 Projection 并 Restore | 原定位准确；无效派生基底若被采用，Kernel 和客户端摘要比较会共享错误（F-45） |
| §3.2.3：Codex `thread_history_materialization.rs:42-45` 参考 | U `codex-rs/thread-store/src/local/thread_history_materialization.rs:42-45` 从持久 state 取 `next_byte_offset`；`:64-69` 同时传 start_offset/expected_ordinal/read_projection_steps | 原定位准确；仅支持 Codex 自身恢复方式，不能证明 Claude 原地重写安全 |
| §3.2.3：借现有 source generation 检测保正确；**本轮新增调查** | M `go-bridge/claude_source_trace.go:75`：`digestClaudeSourcePrefix(file, previous.cursor)`；`:99`：`digestClaudeSourcePrefix(file, cursor)`；`:122`：`io.CopyN(hash, file, cursor)` | generation 确有内容校验，但本身全读 prefix；与拟议零整读不能同时直接复用（F-47） |
| 同上，**本轮新增生产调用点** | M `go-bridge/handlers_projection.go:972`：`h.claudeSourceCorrelation.Observe(...)`；M `go-bridge/handlers_relay.go:877/:882-884`：安装 source ledger 后再 Observe；注释 `:878-881` 明确该 correlation 是每批 live source 所需 | 不受 trace 输出开关保护；relay 启动真实执行，不只是调试脚本 |
| §3.8 对账大响应 / **本轮新增 transport 边界** | I `Services/Bridge/BridgeWebSocketConnection.swift:12`：`maximumMessageBytes = 50 * 1024 * 1024`；I `RelayBridgeFrameConnection.swift:823` 默认同上限，`:859-862` 超限报 `relay.decompressed_too_large` | 总响应仍受实际消息上限约束；gzip 不取消解压上限（F-46） |

只读重读事故 checkpoint，原文件 SHA 仍为 `9d55c187859c37cf9036d0c6f6c9b689998a6312351ac9b02c0bb1f5548a2c9b`；119 个 turn 记录，两种遍历计数一致；尾 17 数组两种字节计算均为 3,801,579 B，最大单 turn 的 Python 口径为 6,347,762 B。原捕获属于 2026-10-17，本轮 2026-10-18 只重读，不是新 wire capture。内容未输出，不复算无关压缩等级。没有新增 Claude record 字段映射；baseDigests/anchorRefresh 是拟议自有协议，不能宣称已取得真实新 wire。沿用 audit-plan 接入边界，不要求未实现协议现在先产出成功样本。

## R14 闭合结果与阻塞意见

### F-41：主要设计冲突已闭合

§3.9 现已规定首开与 typed stale/超限/未 echo 回退均走 full；不变量 3、R-3/R-4、full open 指标和 Gate B 已相应修正。它同时规定与其他章节冲突时以该表为准，足以消除 R14 指出的默认动作歧义，不重复按原问题阻塞。

尚有旧表述，例如 §7.3 的重开计数行仍称 typed stale→window_0、§3.1 图头仍带早期内存入口。它们不支持“未修正残留=0”的自述，但有明确优先级，故本轮不单独升格为阻塞。§7.4.12 full 后“更早消息未显示”的提示也应按 full coverage 判定，而不是固定显示；以 §3.9 full 返回形状为准。

### F-42 [P1 / 仍阻塞] baseDigests 尚未定义摘要与实际缓存内容的全生命周期绑定

**位置**：§3.4.1 写盘规则、§3.8 `:710/:714`、§7.1.5 和 E-11。

**已修好**：增加 Mac 生成摘要、changedTurns 和 tombstone 后，若摘要确实对应缓存版本且 Kernel 正确，原 R14 的 A旧→A新反例可检测；不需要 Mac 维护 per-device 进度。这一方向保留。

**未闭合点**：方案仅写“随每个曾下发的 turn/page 附带”，但缓存内容还会被 `partOps`、`turnStateOps`、detail/state commit 改变。v19 没有规定此时摘要是随 patch 同 cut 更新、失效后禁止用于匹配，还是保留哪个绑定版本；§3.4.1 又允许退后台时直接保存活跃 replica。全量首开是种子，摘要在 full/patch/新对账字段中的承载、缓存迁移或缺 token 时行为也没有纳入明确的冻结义务。

**后果与反例**：如果缓存保存的是 V2 内容，却继续回传 V1 的 token，比较得到的是“Mac 与 V1 一致”，不能推出“缓存 V2 与 Mac 一致”。当源重建/改写使 Mac 恢复到 V1 时，当前设计会因 token 匹配不发该旧 turn 的修正，随后清 syncing。这里不依赖 hash 碰撞，也不能以“本机 digest 没变”证明缓存内容没变。即使未发生该反例，长期保留旧 token 也会把零变更重开变成整 turn 重传。

**最小修订 / 闭合标准**：声明每一缓存行的 `(内容, token, source/schema/version binding)` 必须来自同一有效提交，并逐项覆盖 full 种子、局部 patch、state/detail、对账响应和原子写盘。局部变化没有新 token 时必须有明确的失效/重新对账规则，不能把旧 token 当新内容摘要。E-11 增加“页后收到 partOps/stateOps→写盘→重开”与“缓存内容/token 不匹配、服务端值回到旧版本”的反例。具体字段名和哈希长度可在既定 re-freeze 门冻结，不要求现在实现；但内容与 token 的绑定不变量必须先成立。

### F-43：刷新与翻页前进语义已闭合；新增响应预算问题另列 F-46

将 anchorRefresh 独立于严格后继 turns，确实解决 anchor==tail 仍须刷新、helper 排他语义以及 limit=1/超大 anchor 卡住后继的问题。只要 anchorRefresh 返回的是该 cut 的当前值（running 时也须合法，不能要求必须 settled），原 F-43 可关闭。不能由这一闭合进一步推导总响应有界；F-46 是 v19 修订带来的新预算缺口。

### F-44 [P1 / 仍阻塞] 零新增页仍保持 projection rev=0，采用事务也未落实跨 actor 的隔离

**位置**：§3.4.3 `:594-596`，§7.2.12、E-11。

**已修好**：以当前 epoch/rev 0 的副本开始恢复，避免旧 epoch 大 rev 污染初始 max 比较；缓存 provenance 独立保存也合理。

**仍有确定冲突**：`:595` 明写“**空页保持 0 直到下一条 patch**”，但新对账响应即使零新增也有当前 head，例如 rev 5。复用的 replica 会把 appliedRev 设为 5，投影空页却返回 self/0，依旧违反 E-11 要求的 projection/replica/store/presentation/metadata 一致。若把 store 也留 0，则 `pullDeltaOrFull` 会传 nil sinceRev 重拉 full；若 store 为 5，post-cut patch `baseRev=5` 虽能通过，却掩盖了无后续 patch 时永远存在的混合状态。不能把“等待下一条 patch”当采用成功的条件。

**隔离缺口**：@MainActor 只串行执行同步片段。指定复用链包含 `await replica...` 和 `await presentationEngine.apply...`，这些等待允许同一 MainActor 的 patch/recovery 方法重入；仅写“@MainActor 串行，所以不交错”不能提供事务隔离。首个对账 cut 到达前的 push 也不能直接按恢复用 rev 0 校验，否则现有 `applyFrame` 的 base mismatch 会安排恢复 pull。

**最小修订 / 闭合标准**：无论新增 turns 是否为空，对账完成事务都必须显式采用响应 epoch/head、scalar 和修正集，再一次性发布一致状态；不要沿用空 turns→return self 的终止规则。定义 per-session 的恢复/采用门、排队或版本校验，使跨 actor await、早到 patch、失效/换连接和 recovery pull 无法插入部分采用状态。定向测试要验证“零新增且不再发生任何 patch”即可进入当前 head，以及在每个 await 边界插 patch 不会产生恢复风暴或覆盖新状态。

### F-45 [P1 / 仍阻塞] growth-trust 与 baseDigests 共享同一错误源，不能相互保证正确性

**位置**：§3.2.3 `:468`（尤其 (c)）、§7.1.2 `:826` 和交付说明中的“两层互相兜底”结论。

**证据与反例（对拟议路径推导，不冒充现行运行事故）**：旧 store/客户端都有 A旧；runtime 停止时 transcript 的已提交 prefix 被等长改成 A新，再追加合法 JSONL，使长度增长、membership 不变、suffix 可解析。若按 growth-trust 跳过 prefix 验证并恢复 A旧，Kernel 仍是 A旧；客户端回传 A旧的摘要，Mac 又从 A旧算摘要，结果完全匹配，changedTurns 为空。全量冷拉同样读这个 Kernel，也不会自动修好。消费者对账只能抓住“消费者与 Kernel 的差异”，抓不住“Kernel 与真实 source 的差异”。

**与现有 generation 的关系**：保留现有内容 generation 哈希可以识别部分重写，但它也会整读 prefix，见 F-47。因此不能一边依赖其正确性，一边假定它零成本地支持 growth-trust。Codex 偏移恢复仅是参考，不是 Claude 文件不可变性的证据。

**最小修订 / 闭合标准**：在 source→Kernel 拥有层给出独立的可信 append 判据或保持必要校验；也可明确缩小性能承诺，保留争议路径整读。不得以 iOS 对账代替源完整性验证。若要接受未检测重写造成静默旧数据，那是改变 R-7 的产品取舍，不能由本轮“全部采纳”的工程修订默认授权。

验收必须加入“旧 prefix 改写+合法 suffix+同 membership+客户端也持旧摘要”，对账两侧即使相等也不能被记为 source 正确。§7.1.2 的“prefix 改写且长度变化→(iv)”还与增长进入 (ii) 冲突，需要先定义可判定的分类；`current ≥ committed` 也须区分相等/增长及等长 mtime 变化。

### F-46 [P1 / 新增阻塞] 只限制 turns，新增修正集可把有界对账页重新膨胀成大快照

**位置**：§3.8 `:713-715`、§3.9 两页阈值、§7.3 `:890`，R-1/不变量 3 的有界重开承诺。

anchorRefresh 明确不计预算，changedTurns 则要求返回全部不匹配 turn 的完整 upsert，没有页数/字节限制。缓存过去 ≤4MiB，并不能约束这些 turns **现在**的大小；最多 256 个 ID 只限制条目数。比如过去 64 个小 turn 合计不足 4MiB，现在各自变为约 1MiB，零新增时仍会返回约 64MiB changedTurns，turns 页数为零，“超两页退 full”根本不触发。此例是长度算术反例，不是伪造真实 transcript 证据。I 的逻辑消息及 gunzip 默认上限为 **50MiB**，压缩后在网络上很小也无法避开解压拒绝。

**最小修订 / 闭合标准**：对 **整个对账响应** 定义预算、重复项去重和超限行为，覆盖 turns、anchorRefresh、changedTurns、scalar/metadata。可以分页修正或在发送前给明确 typed 超限及规定恢复，不强迫某种实现；但不能在只收到部分修正时推进为“已对账”，也不能用排除字段的计量证明总载荷有界。保留 never-split 的单 turn 特例时，须单列总大小的合法范围、失败线与耗时指标。增加“多条旧 turn 膨胀、零新增、总修正超过预算/transport 上限”验收。

### F-47 [P1 / 新增阻塞] source generation 的生产计算仍全读 prefix，S-2 遗漏了另一条承重 IO 路径

**位置**：§3.2.3 (i)/(ii)/(iii) 的 IO 上界、§6 S-2 和 §7.1.2/§7.3。

**实际生产链**：`claudeSessionFileRelay` 启动在 `handlers_relay.go:877` 安装 source ledger，`:882-884` 调 `claudeSourceCorrelation.Observe`。新 tracker 的 `Observe` 在 `claude_source_trace.go:99` 对 cursor 之前的全部内容哈希；即便有同进程观察，`:75` 也重读 previous cursor 的 prefix，增长时 `:83` 再计算新 prefix。`digestClaudeSourcePrefix:122` 是 `io.CopyN(..., cursor)`。该调用是 source ledger/generation 身份所需，**不受 `GO_BRIDGE_CLAUDE_SOURCE_TRACE` 日志开关保护**。

因此即使 bbolt 恢复完全跳过 `validateProjectionSourceCheckpoint`，重启后启动实际 Claude relay 仍可能整读约 97MiB。只测新 store 的读取区间会漏掉它。与此同时，v19 还把 source generation 当成 growth-trust 正确性的保障，不能不登记其来源和成本就继续沿用。

**最小修订 / 闭合标准**：将 source ledger/correlation 的生成、持久化、恢复及 live relay 启动纳入 S-2，和 F-45 一起选择正确的身份校验方式及 IO 预算。测试必须测真实 owning 调用链的所有文件读取，而非只包围 bbolt loader：分别验证“store 恢复→source ledger→relay 启动”，trace 开/关均有可解释读量，并保留既有 generation/rewrite 身份护栏。未完成设计前撤回完整路径 suffix-only 的已证明表述。

## 门控、复用与交付边界

旧核验复用限定到未改命题/路径：W1 scalar/fence 的基本分层、direct/relay 协商接线纪律、direct gzip 方向、独立 anchor fixture 和事务内 held 注册机制维持；未据此声称 v19 新字段已经被这些机制覆盖。特别是新对账字段的 cut、一致 apply、摘要及 held 集合，必须在本轮新增契约中明确，再落入 S-7 的 re-freeze 与测试。没有把“文中关键词出现多次”当作接线证明。

E-3、S-7/S-8 协议冻结仍是实施前门；E-4/E-5/E-6/E-7/E-10/E-11 和 owner 真机矩阵是相应实施/完成门。尚无实现的真实新 wire、性能及设备验收保持 pending，不为本轮补样本而操作真实会话。上面五项属于核心设计仍不闭合，不能以实施后测试或未来冻结评审替代答案。

本轮只新增此报告，不修改方案或业务代码、不提交既有改动；未运行 build/unit/UI/snapshot/模拟器自动化，未安装、重启或部署。执行的是源码阅读、来源/diff 核对、只读 checkpoint 与正文哈希复算、文档静态检查。结论仅适用于上述 v19 内容身份。

交付复核：v19 全文仍为 `87efb0fc42a203ed61747fe194d4ec2cc17e9a466be5a891de55aa2ab596f013`；三仓提交/分支未变，I/U 仍干净，M 仅比 G2 多本报告。报告的 `git diff --no-index --check /dev/null <report>` 通过。
