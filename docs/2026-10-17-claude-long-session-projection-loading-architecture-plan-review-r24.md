# Claude 长 session 打开方案评审 R24（全量，fresh 模式）

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **APPROVED（通过）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v28**（R23 APPROVED 后纠错修订：落地 F-60 + 登记元审核更正记录，声明零设计变更）
- 全文 SHA-256：`dfafe1c6ceeb96ea86e284aeebe8c8bb712d47bb6b619c6e611ced5892ee8bbd`（与派发记录一致，评审对象身份确定）
- 正文 SHA-256：`7500fce782a281927ab7bd28bc6e98fca237d9c5d6636fed4e365347dc4ed07c`（按交接块切分规则 `raw[:idx].rstrip(\n)`、`idx = "\n---\n\n设计师交接块"` 本轮实测，与交接块声明一致）
- **派发简报版本描述与实际快照的差异登记**：任务简报正文称「当前 v24 …重点 §10.28 复核点三条（F-48/F-52/F-53）」，但派发哈希对应的是 **v28**（已按 R20→v25、R21→v26、R22→v27 三轮修订 + R23 APPROVED + v28 纠错；R20–R23 报告已存在于 docs/）。哈希身份已确认，本轮按 v28 全文评审：复核点覆盖简报点名的 §10.28 三条（独立复演）+ §10.29–§10.32（R20–R23 各条闭合维持）+ R19/R18 已闭合项回归 + 历轮 F-1~F-60 可追溯性 + **v28 纠错修订自身**（F-60 落地质量 + 元审核更正记录的事实基础）。
- blockers: **0**；advisories: **1**（F-61——对账续页请求的「续页标记 / 修正 cursor 回传」未显式列入请求字段清单，属冻结期 wire 形状空白，行为已由规范句 + 验收④双锁定）
- 评审员模式：fresh——先独立形成风险清单（死锁解除算术复演 / 单调覆盖对抗推演：早页确认项页间修改、anchor 自身被改与被移除、复查集 token 合并、双设备交错、中途崩溃重放 / 两级 typed 链六处口径交叉 / F-60 落地字面核对 / 勘误记录双哈希亲测 / 活文本残留 grep / 哈希级联实测），再读 R20–R23 与 r23-meta 报告核对闭合表述。历轮结论未作为本轮判断来源。
- implementation_readiness: **方案层面通过**（v28 = R23 已通过的设计 + F-60 措辞同步 + 来源记录更正，本轮独立复核零设计回归、零阻塞）。按 repo 2026-09-20 评审循环纪律，APPROVED 为开发阶段唯一入口；实施仍受下述证据/决定/冻结/授权门约束。S-7 仍受 E-3 证据门；OD-9 pending 不阻塞 S-2 默认档 (a)。

## 来源身份（G1：源码分析前重新生成）

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | `M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（= 待审 v28 本体）+ 未跟踪 r10–r23 评审报告（含 r23-meta）与 `2026-10-17-incremental-append-feasibility-report.md`；go-bridge/MacBridge 源码不在脏清单内；任务简报指定来源（f868af18 或更新）✓。`git worktree list` 实测 6 个 Mac 工作树，`feat/ios-native-message-timeline` 唯一对应本工作树——P0 配对成立 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `7527e1ead184fa88c58b05ef86839b7ba6489009` | **工作树干净**（本轮实测 `git status --porcelain` 为空）。与 R22/R23 时点完全同一提交且干净——R22/R23 在 `7527e1ea` 的 iOS 亲核按命题粒度可复用；本轮依赖的 iOS 锚点仍全部本轮重新亲核（见锚点表），无仓级复用 |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd555237da3db4e1897`（本轮实测 `git rev-parse main`；`cat-file -t` → `commit`；工作树干净） | 只读架构参考；**本轮产生了依赖 U 的新结论**（v28 勘误记录的事实基础核验，见锚点表）——按 §2.1 更正后的权威 U 身份记录 |

本轮未构建、未安装、未部署、未运行测试、未修改方案与业务代码；唯一写入 = 本报告。

## 亲核锚点表（本轮全部新核；iOS 锚点在 `7527e1ea` 亲核）

| 命题 / 方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 本轮核验方式 |
| --- | --- | --- | --- | --- |
| v28 勘误基础：Codex main 实际提交 = `…cd555237…`、R23 报告 U 行 `…cd55e237…` 为对象库不存在的无效值 | U `git rev-parse main` = `ce0323f2f2d3b25e82806cd555237da3db4e1897`；`cat-file -t` → `commit`；对照 `ce0323f2f2d3b25e82806cd55e237da3db4e1897` → `fatal: git cat-file: could not get object info` | 命令输出原文见左 | 语义支持——§0 v28 更正记录的旧/新身份、第 26 位 `5`→`e` 转写错误、方案 §2.1（:398）记录值 = 实际值，三件事全部亲测成立 | 本轮亲测（rev-parse + cat-file 双向） |
| F-52：「`BeginProjectionSnapshot` 每次取当前 `kernel.Snapshot`（`projection_kernel.go:1887-1902`）」 | M `go-bridge/projection_kernel.go:1887` | `1887: func (k *ProjectionKernel) Snapshot(backendID, sessionID string) (SessionProjection, bool) {` → 经 `reducer.Snapshot(...)` 返回当前投影 | 定位准确；语义支持——Snapshot 无 (cutRev, turnId) 历史版本，每次调用取当前行；`BeginProjectionSnapshot` 定义在 `projection_snapshot_fence.go:57`、由 `projection_window_handler.go:168` 每请求调用 | 本轮亲核（sed + grep） |
| F-52：「fence 完成即释放不跨请求持有（`projection_window_handler.go:168-210`）」+ held 注册（`:201-211`） | M `go-bridge/projection_window_handler.go` | `168: proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(`；`199-204` 注释 "…permanently strand the detail…Completion failure rolls the set back."；`205: turnIDs := make(...)`；`210: CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, turnIDs)` | 定位准确；语义支持——fence 生命周期 = 单请求内（「同 cut」撤回的事实基础）；held 现状只注册 `response.Turns`，anchorRefresh/changedTurns 扩展注册方案已登记（实施注，非缺陷） | 本轮亲核（sed 160-215） |
| F-55/v26：epoch hello_ack 双路径（`server.go:780` / `main.go:640`） | M 同名文件 | `780: ack.BridgeEpoch = s.bridgeEpoch`；`640: ack.BridgeEpoch = bridgeEpoch` | 定位准确；语义支持——iOS 侧 epoch 判定在 direct/relay 两路径均可执行 | 本轮亲核（grep + sed） |
| F-53：iOS 逻辑消息 50MiB 硬顶（`BridgeWebSocketConnection.swift:12`）+ relay 解压同限（`RelayBridgeFrameConnection.swift:823/:859-862`） | I 两文件 | `12: static let maximumMessageBytes = 50 * 1024 * 1024`；`823: maxOutputBytes: Int = BridgeWebSocketContract.maximumMessageBytes`；`859-862: guard output.count <= maxOutputBytes - written else { throw … "relay.decompressed_too_large" }` | 定位准确；语义支持——gzip 只压网络不消解压上限，两级 typed 链与「contingency window_0 不是恢复路径」的约束基础成立 | 本轮亲核（iOS @ 7527e1ea） |
| F-44 反例依据（`SessionProjectionWindow.swift:123-126/:173`；`SessionProjection.swift:696`） | I 两文件 | `123-126: guard !page.turns.isEmpty else { … return self }`；`173: copy.syncRev = max(syncRev, page.syncRev)`；`696: var revisionKey: String { "\(bridgeEpoch ?? "")|\(syncRev)" }` | 定位准确；语义支持——采用事务不可复用 max/return-self 分支的反例依据成立 | 本轮亲核 |
| 两步注入（`ProjectionReplica.swift:193-212`）与恢复门 await 边界（`ProjectionStore.swift:612-619/:1180`） | I 两文件 | `193-212: func applyFull(… forceReplace: Bool = false)`，`if !forceReplace, … projection.syncRev < current.syncRev { return .stale }`；`612-619: await replica.applyWindowPage(`；`1180: await presentationEngine.apply(` | 定位准确；语义支持 | 本轮亲核 |
| F-47：source ledger 生产链（`handlers_relay.go:882-884`；`claude_source_trace.go:75/:99/:122`） | M 两文件 | `882-884: traceCorrelation, err := h.claudeSourceCorrelation.Observe(backendID, sessionID, sessionID, sessPath, offset)`（注释 "Needed for every live source batch"）；`75: currentOldPrefix, digestErr := digestClaudeSourcePrefix(file, previous.cursor)`；`99: digest, err := digestClaudeSourcePrefix(file, cursor)`；`122: func digestClaudeSourcePrefix` → `io.CopyN(hash, file, cursor)` | 定位准确；语义支持——prefix 校验整读不受 trace 开关保护，(ii) 默认档 IO 如实登记 | 本轮亲核 |
| never-split / 上限常量 / 响应字段 / newer 与 locate 分支（`projection_window.go:24-31/:77-82/:278-301`） | M 同文件 | `24-26` 注释 "…a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn."；`28-30: maxWindowTurns=256 / maxWindowEncodedBytes=4<<20 / defaultWindowTurns=100`；`78-82: type ProjectionWindowResponse struct { Window…; Turns…; SyncRev…; Resume… }`；newer 分支 `decodeProjectionWindowCursor` + `growWindowRange(sizes, limit, boundary+1, false)`（注释 "Strict turn-chain order (R7)"）；locate 分支 `indexOfTurn` miss → `errProjectionWindowLocateOut` | 定位准确；语义支持——`boundary+1` = 严格后继（§3.8 `turns` 语义同源）；裸 turnId 锚定有冻结协议先例 | 本轮亲核 |
| F-31/F-36：opcode 自描述的机制事实（`types.go:13-33` 无 contentEncoding；`relay_conn.go:296-313` gzip 标记进 envelope） | M 两文件 | `types.go:13-33: type WireMessage struct { Type/RequestID/… }`（无 contentEncoding 字段）；`relay_conn.go:296-313: contentEncoding := "gzip"`（`if outboundGzip && len(plaintext) >= relayGzipThreshold`，且作为 `relayOutboundJob` 字段入队） | 定位准确；语义支持——direct 压缩帧需自描述机制的论证基础成立 | 本轮亲核 |
| Codex 参照：suffix-only 信任持久化 offset（`thread_history_materialization.rs:42-45`） | U 同文件 | `42-45: let start_offset = projection_state.as_ref().map_or(0, \|state\| state.next_byte_offset);` | 定位准确；语义支持——(ii) 默认档与 Codex 不重读 prefix 的差异对照成立 | 本轮亲核 |
| 版本级联与交接块同步 | 本轮 python 按切分规则实测 | 全文 = `dfafe1c6…`（= 派发哈希）；正文 = `7500fce7…`（= 交接块声明）；行 4 级联含 v27 全文哈希 `54e82035…`（= R23 报告记录值）、v26 `f9063e95…`（= R22）、v25 `a3cdf1d2…`（= R21）、v24 `4262ffd5…`（= R20） | 级联与交接块同步成立；v1→v28 各版全文哈希随行 | 本轮亲测 |

**本轮运行的检查命令**（关键项）：`shasum -a 256 <plan>`；python 切分规则正文哈希实测；`git rev-parse HEAD` / `git branch --show-current` / `git status --porcelain` / `git worktree list`（双仓）；`git -C <codex> rev-parse main` + `cat-file -t`（双哈希）；`grep -n "同 cut|同 kernel cut|cut 失效|退 window_0|只含 changedTurns|剩余未对账|自动满足|S-1"`（活文本残留，限定行号 >336 过滤）；`sed -n` 锚点提取（Mac 6 文件 + iOS 6 文件 + Codex 1 文件）。未运行 build/test（方案阶段授权边界：只读 + 唯一报告写入）。

## v28 纠错修订自身判定（本轮新覆盖对象）

**1. F-60（R23 唯一建议）——闭合。**

- 修订处核对（§3.8 :827）：`changedTurns` 定义句现为「**复查集中**与当前内容 digest 不一致或 token 为 null（失效，必须回传）的 turns 的当前完整 upsert……载体范围随之明确 = 复查集全部项的当前终态 upsert（含已发项与早页确认项的页间修改）」，附 v28 措辞同步注记（原「baseDigests 中」限定词的 v19 时代背景与 R23 定级依据随行）。
- §10.32① 闭合标准（「字面上指向同一集合、无需读者自行调和」）本轮逐字比对成立：`changedTurns` 来源 =「复查集中」（:827）＝ 单调覆盖②复查对象 =「复查集内全部项」（:843）＝ `removedTurnIds` 覆盖 =「复查集中任何项」（:828）——三处字面同集。
- 剩余「baseDigests 中」字样 grep：命中仅在 §0 v28 表意见引文与历史区（豁免语境），规范断言层零残留。
- re-freeze 誊写义务已登记（§0 v28 表「冻结门登记」+ 交接块 open_gates「F-60 changedTurns 口径誊写」）。

**2. 元审核更正记录——事实基础本轮独立亲测成立，处置恰当。**

- 三项事实全部复核：① R23 报告 U 行（r23.md:23）确实记录 `ce0323f2f2d3b25e82806cd55e237da3db4e1897` 且注「本轮实测一致」（本轮 Read 原文核对）；② 实际 main = `…cd555237…`（rev-parse + cat-file 均 pass）；③ 报告值 `cat-file -t` → `fatal: could not get object info`（对象库不存在）——「实测一致」声明按其自身字符串为假，P0 来源门违反成立。
- 方案侧处置正确：§2.1（:398）记录值本就 = 实际值（方案侧来源身份无需修正），更正记录入册 §0 v28（旧/新身份、来源、影响限定三要素齐备），并声明「后续引用 U 来源身份一律以 §2.1 为准」——本轮报告即按此执行（本报告 U 行 = `…cd555237…`）。R23 报告本体按时代性记录纪律不回写，正确。
- 影响限定成立：r23-meta 锚点抽查 11/11 通过、R23 自述未产生依赖 U 的新结论——R23 技术判定（APPROVED）不动摇，缺陷限于来源身份记录。

**3. 「零设计变更」声明的核验边界（如实登记）**：v28 相对 v27 的改动面核对 = 声明的修订处（版本行/状态行/§0 v28 表/§2.1 注记/§3.8 changedTurns 措辞/§10.32/交接块）+ 本轮全文阅读确认设计条款（切片/门/不变量/OD/预算/打包/回退/验收矩阵）自洽无内部矛盾。**v27 字节已不可复原**（工作树只保留当前版、HEAD 提交内方案为 v9 时代版本），无法做逐字节 diff 验证改动面封闭性——该声明按「内部一致性 + 声明范围」采信并在此披露限制。

## §10.28 三点独立复演（简报点名重点，在 v28 的维持状态）

**① 死锁解除（F-48）——成立。** 独立算术复演：46MiB anchor + mandatory 2MiB = 48MiB 恰满总硬顶（合法，§7.1.5 边界验收 :980 在位）→ 首包对账空间 = min(8, 48−2−46) = 0 → anchor-only 首包 + correctionsPending → 续页 turn phase 交付 1KiB 后继（1KiB + 2MiB ≤ 48）→ **事务内水位线 = 已交付链末端 = 该 1KiB turn**（:853）→ 下一请求 anchorRefresh 恢复正常体量，死锁链断开。9MiB 后继走整 turn 独占页（9+2=11 ≤48）；42+6MiB 首包空间 4MiB < 6MiB → 溢出续页（6+2=8 ≤48），相位边界「不回填、切页即止」（§3.8 :835 v26 定案 + §7.1.5 :982 括注）在位。链序由 `boundary+1` 排他语义（本轮亲核 `projection_window.go` newer 分支）+ R7 保证。三形态均有 ≤48MiB 唯一可实现页序列，§7.1.5 后继 turn 交付验收①-③ 与 §3.8/OD-7/§3.9/§3.4.2 状态机/E-11/held 注册六处口径一致。

**② 自包含 cursor 单调覆盖（F-52）——论证成立，未找到反例。** 机制事实本轮全部亲核：`kernel.Snapshot` 当前行无历史版本（:1887）、fence 单请求生命周期（:168-210）、bbolt schema 无版本化 rows（§3.2.2）——「同 cut」撤回的事实基础成立。全部判定输入随请求携带（anchorTurnId + 全集复查集，v27 F-59 口径），Mac 零 per-device 状态，与不变量 6 相容；与现有每请求快照机制是复用而非新增。本轮对抗推演新覆盖的场景（均无反例）：(a) 早页确认项页间被 live patch 修改 → 全集复查当页捕获重发（恢复门 rev ≤ head 丢弃分支不丢内容——内容已由对账页交付）；(b) **anchor 自身页间被修改** → anchor 在复查集内（anchorRefresh 附带 token，token 生命周期规则 1）→ 经 changedTurns 重发（≤46MiB 独占修正页 46+2=48 合法），路径存在且与「续页不携带 anchorRefresh」不冲突（changedTurns 是重发载体）；(c) anchor 被移除 → locate 失败 → Mac 侧 typed stale（:843 ④）；非锚点项被移除 → removedTurnIds tombstone（:828「任何项」）；(d) 双设备交错 / 中途崩溃重发同一 cursor → 每请求独立可重放、幂等（upsert 覆盖）；(e) 复查集内同一 turnId 的 token 值合并语义未显式定义——任何偏差（回传旧 token/null）触发 Mac 重发当前终态，最坏冗余无正确性影响（冻结字段形状时明确即可，与 R23 判定一致，不重开）。

**③ 两级 typed 链（F-53）——六处口径一致。** typed `full.oversized` 显式终态（§3.8 超限恢复语义 :845-847）+ `projection_max_bytes` hello 协商（默认 50MiB、提升档 S-7/S-8 冻结定、数值归 OD-7）+ §3.9 总表（:867）+ R-2 双失败线（:374，15s 时间线与字节线分立）+ §8 两行（:1074-1075）+ E-11 full 超限反例（:905）+ §7.1.5 full 超限验收（:984，含「解压拒绝不得计入恢复成功」）本轮交叉核对一致。「contingency `window_0` 不是恢复路径」论证成立：`maximumMessageBytes` 同时约束 direct 逻辑消息与 relay 解压输出（本轮 iOS 双点亲核），46MiB 级单 turn 对 window_0 同样不可传输。

## R20–R23 已闭合项回归检查 + 历轮可追溯性

- **R22 F-59**（复查集全集口径）：维持闭合——§3.8 :841（全集、不收缩）+ 单调覆盖② :843（全部项）+ removedTurnIds :828（任何项）+ 验收② :983（三个构造：跨 cut 重发 / 移除 tombstone / 早页确认项页间修改）逐处在位；冻结门经交接块 open_gates 登记。
- **R21 F-57**（`window_0 回退`活文本）：维持闭合——本轮 grep 残留仅版本行/更正注记内嵌引用/§10 历史项（:858/:1008/:1058 均为「原『…』为 v14 前/v10 时代残留」注记），规范断言层零残留；§7.3 重开增量路径行（:1035）持 FallbackFull 口径。
- **R21 F-58**（相位边界）：维持闭合——§3.8 :835 定案 + §7.1.5 :982 括注「不回填、切页即止」在位；「42+6+changedTurns」唯一页序列可写出（本轮独立推演与 R22 同构：首包 44MiB / 续页 10MiB）。
- **R20 F-54/F-55/F-56**：维持闭合——组合验收④（:981）断言收窄到 anchorRefresh、每续页独立 head、typed stale 构造 = anchor 离开保留区；epoch 判定主体（:843 ④：anchor = Mac 侧 / epoch = iOS 侧 hello_ack 比对 / 续页请求不携带 epoch / 复查集兜底）+ relay 锚点（`main.go:640` 本轮亲核）；removedTurnIds 三处落位。
- **R19 F-48/F-52/F-53**：维持闭合（§10.28 三点，见上）。
- **R18 三处 / F-49/F-50/F-51**：维持闭合——46/46+1 边界统一独占额度口径（:980）、anchorRefresh 仅首包（:826/:838）、组合验收①-③/⑤ 与打包确定性（:981/:835-839）、F-49 组合路径与 §7.1.2 范围限定（:578/:961-962）、F-50 token 全 ops 终态（:821）+ §7.2.13 mixed-patch 测试（:1007）、F-51 Mac restart 指标单行（:1030）。**未见回归。**
- **「超 2 页」计数口径**：三处（OD-7 :885 / §3.8 :838 / §7.3 :1032）措辞一致（turns 页数计），R21–R23 已裁定无判定分歧，无新证据不重开。
- **历轮 F-1~F-60 可追溯性**：§0 修订历史 v1→v28 级联完整、各版全文 SHA-256 随行；v24–v27 声明哈希与 R20–R23 报告记录值逐一吻合（本轮交叉核对）；F-1~F-60 各有修订表条目与闭合记录，链可追溯。

## 意见

### F-61 [建议] 对账续页请求的「续页标记」未显式列入请求字段清单——「anchorRefresh 仅首包」在 Mac 侧的判定输入属冻结期空白，宜随 re-freeze 显式定案

- **位置**：§3.8 自包含修正 cursor（:841）：「续页请求携带 `anchorTurnId` + 完整复查集」——请求侧字段清单仅此两项；对照 §3.8 `anchorRefresh` 条目（:826）：「**仅对账首包携带，修正续页不重复**」与打包 3（:838）「**不携带 anchorRefresh**」。
- **证据与影响**：Mac 无 per-device 状态（不变量 6），续页请求若只携带 anchorTurnId + 复查集，与一次全新对账首包请求在 Mac 侧不可区分（46MiB 死锁场景下首包后水位线 = anchor 自身，续页请求的 anchor 与首包相同、复查集仅多出 anchorRefresh token 一项——不足以作为判定输入）。要让「续页不携带 anchorRefresh」可执行，请求必须显式携带续页标记（最自然 = 回传上一响应的修正 cursor，v20 F-46 原案「correctionsPending + cursor，anchor 不变续拉」已含此意，v23 起响应侧「修正 cursor 随页携带」在位、但**请求侧回传**未随 v24 续页改写显式重申）。行为本身已被规范句 + 组合验收④（:981「全部续页响应 anchorRefresh 字段为空或省略」）双锁定，不构成实现/测试判定二义；空白仅在 wire 形状层，与 F-55（R20 建议）同型同级。
- **修订方向（最小充分）**：§3.8 自包含 cursor 段补半句「续页请求显式携带续页身份（如回传上一响应的修正 cursor），Mac 以该标记区分首包与续页（无标记 = 全新对账事务首包，携带 anchorRefresh）」；re-freeze 冻结清单把该标记字段随「全集复查集请求字段形状」一并冻结。
- **闭合标准**：冻结评审可据 §3.8 直接写出首包/续页两种请求形状，实施者无需自行发明「续页如何不被当作首包」的判定方式。

## 复审处置与剩余门

| 对象 | 本轮判定 |
| --- | --- |
| v28 F-60 | 闭合（三处字面同集 + 誊写义务登记 + 残留零命中） |
| v28 元审核更正记录 | 事实基础独立亲测成立（双哈希 cat-file）；处置（§2.1 为准 / R23 报告不回写）恰当；影响限定成立 |
| R22 F-59 / R21 F-57/F-58 / R20 F-54/F-55/F-56 | 维持闭合，未见回归 |
| R19 F-48/F-52/F-53（§10.28 三点） | 维持闭合（独立复演 + 对抗推演无反例） |
| R18 三处 / F-49/F-50/F-51 | 维持闭合，未见回归 |
| 历轮 F-1~F-60 | §0 级联完整，v24–v27 哈希与 R20–R23 记录逐一吻合 |

仍待证据/决定/授权门（与方案交接块一致，非本轮阻塞；**方案通过 ≠ 实施就绪**）：

- **证据门**：E-3（S-7 开工前）、E-4（anchor 页 wire 口径）、E-5、E-6、E-7、E-10、E-11（实施后定向测试 + 真机场景 9-15）；
- **决定门**：OD-9（growth-trust 档，pending owner 裁决 R-7 例外取舍；不阻塞 S-2 默认档 (a)）；
- **冻结门**：S-7 anchor-newer re-freeze 批次（含 F-55 epoch 语义、F-59 全集复查集请求字段形状、**F-61 续页标记字段（若采纳）**、F-60 changedTurns 口径誊写、`projection_max_bytes` wire 形态）、S-8 `direct_gzip_v1` 冻结批次；
- **授权门**：S-6 owner 真机手动矩阵；UI automation / 真机操作未获授权（2026-10-10 agent-device 永久停用裁决）。

**附带观察（不计入意见数）**：方案文件以交接块 `open_gates` 行结尾，代码围栏未闭合、无尾随换行——纯排版事项，不影响哈希切分规则与机器解析，随下次修订顺手补上即可。

## 本轮动作边界声明

本轮仅写入本报告文件；未修改方案、业务代码、历轮报告；未运行 build/test；未构建、安装、部署或重启任何运行时；未触碰 iOS 工作树未提交内容（该工作树本轮为干净状态）。方案全文与正文哈希在评审期间未变化（读取快照身份 = 派发身份 `dfafe1c6…`）。

---

评审员交接块：

```text
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: dfafe1c6ceeb96ea86e284aeebe8c8bb712d47bb6b619c6e611ced5892ee8bbd
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r24.md
scope: full
blockers: 0
advisories: 1
open_gates: [E-3(S-7 开工前), E-4, E-5, E-6, E-7, E-10, E-11, OD-9(growth-trust 档，S-2 默认档不受阻塞), S-7/S-8 re-freeze 批次(含 F-59 复查集请求字段形状、F-61 续页标记、F-60 口径誊写、projection_max_bytes wire 形态), S-6 owner 真机矩阵授权]
round_complete: true
contract_version: plan-contract-v1.1
```
