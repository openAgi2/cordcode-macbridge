# Claude 长 session 打开方案评审 R22（全量，fresh 模式）

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v26**
- 全文 SHA-256：`f9063e95203558e93532ecb77fe71584c6e31d61952822b48e7a31eec06c5036`（与派发记录一致，评审对象身份确定）
- 正文 SHA-256：`fafc29616609af96b9cb62ba1040590ca1e5b38291c0621e9ae621fac4c1400`（按交接块切分规则 `raw[:idx].rstrip(\n)`、`idx = "\n---\n\n设计师交接块"` 本轮实测，与交接块声明一致）
- **派发简报版本描述与实际快照的差异登记**：任务简报正文称「当前 v24 …重点 §10.28 复核点三条（F-48/F-52/F-53）」，但派发哈希对应的是 **v26**（已按 R20→v25、R21→v26 两轮修订；R20/R21 报告已存在于 docs/）。哈希身份已确认，本轮按 v26 全文评审，复核点覆盖 §10.28（R19 三条）+ §10.29（R20 三条）+ §10.30（R21 两条）三组 + R19/R20/R21 已闭合项回归 + 历轮 F-1~F-58 可追溯性。
- scope: **full**（v26 全文）
- blockers: **1**（F-59——复查集定义在 §3.8 规范文本与 §7.1.5 验收① 两处不一致，**v24引入、R20/R21 漏检的旧问题，非 v26 回归**）；advisories: **0**
- 评审员模式：fresh——先独立形成风险清单（F-57 残留 grep / F-58 相位边界唯一页序列推演 / 死锁解除算术复演 / 单调覆盖复查集语义推演 / 两级 typed 链口径 / 哈希级联 / 恢复门与复查集交错的漂移窗口），再读 R20/R21 报告核对闭合表述。R20/R21 结论未作为本轮判断来源。
- implementation_readiness: R21 两条（F-57/F-58）**全部判闭合**；R20 三条（F-54/F-55/F-56）与 §10.28 三点（死锁解除 / 自包含 cursor 单调覆盖 / 两级 typed 链）**维持闭合、无回归**；v26 新增锚点（`main.go:640` relay epoch）本轮亲核属实。新发现 F-59 为 v24 自包含 cursor 文本的旧漏检：规范定义与测试断言定义了两个不同的复查集，按其一实现则另一处断言失败——与本系列 F-54/F-57 判阻塞的「实现与测试无法同批判定」同一标准。修完即达 APPROVED 条件（零其它阻塞）。S-7 仍受 E-3 证据门；OD-9 pending 不阻塞 S-2 默认档。

## 来源身份（G1：源码分析前重新生成）

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | `M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（= 待审 v26 本体）+ 未跟踪 r10–r21 评审报告与 `2026-10-17-incremental-append-feasibility-report.md`；go-bridge/MacBridge 源码不在脏清单内；任务简报指定来源（f868af18 或更新）✓ |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `7527e1ead184fa88c58b05ef86839b7ba6489009` | **工作树干净**（本轮实测）。注意：R21 时点为 `aa3930b1…`，本轮已漂移至 `7527e1ea…`——**R21 侧 iOS 核验不可按仓级复用**；本轮依赖的 iOS 锚点文件已全部在 `7527e1ea` 重新亲核（见锚点表）。iOS 持续漂移属 native-timeline 开发线推进，方案 §2.1 已声明时点快照语义 + S-0 重生成义务，不构成方案缺陷 |
| U | `/Users/jacklee/Projects/codex` | `main` | 方案 §2.1 记录值 `ce0323f2…`（本轮未产生依赖 U 的新结论，未复核，如实登记） | 只读架构参考 |

本轮未构建、未安装、未部署、未运行测试、未修改方案与业务代码；唯一写入 = 本报告。

## 亲核锚点表（本轮全部新核；iOS 锚点因提交漂移在 `7527e1ea` 重核，无跨轮复用）

| 命题 / 方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 本轮核验方式 |
| --- | --- | --- | --- | --- |
| v26 新增锚点：relay hello_ack 携带 epoch（`main.go:640`） | M `go-bridge/main.go:640` | `640: ack.BridgeEpoch = bridgeEpoch` | 定位准确；语义支持——iOS 生产 Relay 路径可执行 epoch 比对，F-55 落地一致性锚点成立 | 本轮亲核（grep + sed） |
| direct hello_ack 携带 epoch（`server.go:780`） | M `go-bridge/server.go:780` | `780: ack.BridgeEpoch = s.bridgeEpoch` | 定位准确；语义支持 | 本轮亲核 |
| never-split 语义与上限常量（`projection_window.go:24-26` / `:27-31`） | M `go-bridge/projection_window.go:24-31` | `24-26: // …a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn.`；`28: maxWindowTurns = 256`、`29: maxWindowEncodedBytes = 4 << 20`、`30: defaultWindowTurns = 100` | 定位准确；语义支持（OD-7 数值类别对齐、46MiB 级 anchor never-split 独占的语义同源基础） | 本轮亲核 |
| window 响应现状无 scalar（`projection_window.go:77-82`） | M `go-bridge/projection_window.go:77-82` | `77-81: type ProjectionWindowResponse struct { Window…; Turns…; SyncRev…; Resume… }`（四字段） | 定位准确；语义支持 | 本轮亲核 |
| newer 现状只能从 epoch 绑定 opaque cursor 进 + 排他切片 + locate 先例（`projection_window.go:278-301`） | M `go-bridge/projection_window.go:279-301` | `:281 decodeProjectionWindowCursor(params.Cursor)`、`:284 validateProjectionWindowCursorScope`、`:288 return …, errProjectionWindowCursorStale`、`:290 slice.start, slice.end = growWindowRange(sizes, limit, boundary+1, false)`（注释 "Strict turn-chain order (R7)"）、`:298 anchor := indexOfTurn(turns, params.AnchorTurnID)`、miss → `errProjectionWindowLocateOut` | 定位准确；语义支持——`boundary+1` = 严格后继；裸 turnId 锚定有冻结协议先例；typed stale 复用 `cursor_stale` 族可行 | 本轮亲核 |
| fence 单请求生命周期 + held 注册（`projection_window_handler.go:168-210` / `:201-211`） | M `go-bridge/projection_window_handler.go:168-211` | `:168-174 proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(...)`；`:201-210 turnIDs := … for _, turn := range response.Turns …`；`:210 CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, turnIDs)`（注释 "permanently strand the detail"、"Completion failure rolls the set back"） | 定位准确；语义支持——不跨请求持有（「同 cut」撤回的事实基础）；实施注：现 `turnIDs` 只含 `response.Turns`，anchorRefresh turn 与 changedTurns 需按方案扩展注册——方案已登记（非缺陷） | 本轮亲核 |
| `kernel.Snapshot` 取当前投影、无历史版本（`projection_kernel.go:1887-1902`） | M `go-bridge/projection_kernel.go:1887-1904` | `:1887 func (k *ProjectionKernel) Snapshot(backendID, sessionID string)` → `reducer.Snapshot(...)` 返回当前投影 | 定位准确；语义支持 | 本轮亲核 |
| F-53：iOS 逻辑消息 50MiB 硬顶（`BridgeWebSocketConnection.swift:12`） | I `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeWebSocketConnection.swift:12` | `12: static let maximumMessageBytes = 50 * 1024 * 1024` | 定位准确；语义支持 | 本轮亲核（iOS @ 7527e1ea） |
| F-53：relay 解压输出同受 50MiB（`RelayBridgeFrameConnection.swift:823/:859-862`） | I 同文件 `:821-823`、`:858-863` | `:821-823 static func decompress(_ compressed: Data, maxOutputBytes: Int = BridgeWebSocketContract.maximumMessageBytes)`；`:859-863 guard output.count <= maxOutputBytes - written else { throw … "relay.decompressed_too_large" }` | 定位准确（在引区间内）；语义支持——gzip 不消解压上限 | 本轮亲核（同上） |
| F-44 反例依据（`SessionProjectionWindow.swift:96-99/:123-126/:173`） | I 同文件 | `:97-99 emptySession … execution: SessionExecutionView(phase: .idle, …)`；`:123-126 guard !page.turns.isEmpty else { … return self }`；`:173 copy.syncRev = max(syncRev, page.syncRev)` | 定位准确；语义支持（采用事务不可复用 max/return self 分支的反例依据成立） | 本轮亲核（同上） |
| 两步注入（`ProjectionReplica.swift:193-212`） | I 同文件 | `:193 func applyFull(_ projection:…, forceReplace: Bool = false)`；`:197-199 if !forceReplace, let current = …, projection.syncRev < current.syncRev { return .stale }` | 定位准确；语义支持——`forceReplace: true` 绕开 stale 判断 | 本轮亲核（同上） |
| 渲染 guard（`ChatViewModel.swift:1080`） | I `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift:1080-1090` | `:1080 func renderProjectionFromStore(sessionId: String? = nil)`，guard 含 `sessionSyncV2Active` | 定位准确；语义支持 | 本轮亲核（同上） |
| 版本级联与交接块同步 | 本轮 python 按切分规则实测 | 全文 = `f9063e95…`（= 派发哈希）；正文 = `fafc296…`（= 交接块声明）；行 4 级联含 v25 全文哈希 `a3cdf1d2…`（= R21 报告记录） | 级联与交接块同步成立 | 本轮亲测 |

**本轮运行的检查命令**（关键项）：`shasum -a 256 <plan>`；`git rev-parse HEAD` / `git status --porcelain`（双仓）；`git worktree list`（双仓）；`grep -n "ack.BridgeEpoch" go-bridge/server.go go-bridge/main.go`；`grep -n "window_0 回退\|回退 \`window_0\`\|退 window_0" <plan>`；`grep -n "剩余未对账\|原始 baseDigests" <plan>`；`grep -n "同 cut\|cut 失效\|…"`（cut 残留）；python 正文哈希切分实测。未运行 build/test（方案阶段授权边界：只读 + 唯一报告写入）。

## R21 意见闭合判定

**F-57 [阻塞]（§7.3「重开增量路径」行 v10 时代活文本）——闭合。**

- 闭合标准（R21）：全文活文本（§0/§10 历史区与更正注记除外）grep `window_0 回退|回退 \`window_0\`|退 window_0` 零命中；§7.3 三行在同一回退目标（FallbackFull）下互洽。
- 本轮 grep 实测：命中仅存在于 §0 版本历史级联（`:4`）、§3.8/§7.2.14/§7.4 场景 12 的「原『…』为 v14 前/v10 时代残留」**更正注记内嵌引用**（`:837/:987/:1037`）、§10.30 复核指令自身（`:1131`）与交接块修订说明（`:1139`）——全部属豁免语境，规范断言层面零残留。
- §7.3 三行互洽：重开增量路径行（`:1014`）＝ anchor typed stale → **FallbackFull（全量冷路径，§3.9）** 合法回退不计重试；full projection open 行（`:1019`）＝ 重开 full 仅由 FallbackFull 触发；重试次数行（`:1020`）＝ 合法回退不计重试。三行同一回退目标，与 §3.9 唯一权威总表一致。**闭合。**

**F-58 [建议]（打包相位边界）——闭合。**

- 闭合标准（R21）：§3.8 与 §7.1.5 ③ 在「装不下的 turn 之后、本页剩余空间如何处置」上只有一种读法；「42MiB anchor + 6MiB successor + changedTurns」组合能写出唯一合法页序列。
- §3.8 确定性打包顺序（`:814`）已显式定案相位边界：turn phase 内某 turn 装不下当前页剩余对账空间时本页 turn phase 即终止、当前页剩余空间**不回填**、correction phase 不得越过未交付 turns 提前装页、turns 全部交付完毕后 changedTurns 才按序进入（可与末个 turns 同页）；§7.1.5 验收③（`:961`）括注已对齐「不回填、切页即止」并带 v26 更正注记。
- 本轮独立推演唯一页序列（42MiB anchor + 6MiB successor + 2MiB changedTurns）：首包 = mandatory 2 + anchorRefresh 42（对账空间 min(8,48−2−42)=4MiB，6MiB turn 装不下 → turn phase 终止、4MiB 不回填）＝ 44MiB；续页 = mandatory 2 + turn phase 6MiB（turns 交付完毕）+ correction phase 2MiB（6+2=8 ≤ 对账预算）＝ 10MiB。每页 ≤48MiB、链不跳（R7）、确定性断言⑤可裁决（同输入字节一致）。.changedTurns 更大时按 8MiB 对账预算切页，仍唯一。**闭合。**

**R21 亲核锚点表补充（relay epoch 锚点入册）——落地。** §3.8 单调覆盖④（`:822`）已补 relay 路径 `main.go:640`，本轮亲核属实（见锚点表）。

## R20 / R19 已闭合项回归检查

- **R20 F-54**（v23 口径活文本两处）：维持闭合——组合验收④（`:960`）断言已收窄到 anchorRefresh、head 口径 = 每续页独立取当次 committed head / 采用最后一页 head、typed stale 构造 = anchor 离开保留区；§7.3 指标行（`:1011`）已对齐 v24 续页内容页规则、版本标记「v24 F-48 终版」。本轮 cut 残留 grep：规范断言层零残留（命中仅在 §0/§10 历史区、撤回声明与论证性引用）。
- **R20 F-55**（epoch 判定主体）：维持闭合——`:822` 单调覆盖④判定主体定案（anchor 保留区 = Mac 侧 typed stale；epoch 中止 = iOS 侧 hello_ack 比对；Mac 不比对、续页请求不携带 epoch；复查集兜底）；re-freeze 冻结清单登记在位（`:836`）；与 §3.4.2「epoch ≠ 当前：流程照常」的两时点/两后果区分清楚。
- **R20 F-56**（已发项被移除处置）：维持闭合——`removedTurnIds` 定义（`:807`）、单调覆盖②（`:822`）、自包含 cursor 验收②构造（`:962`）三处落位。
- **R19 F-48**（死锁解除）：维持闭合——§3.8 打包 3（`:817` 续页 = 内容页，turn phase 先于 correction phase、never-split、turns 计入超 2 页判定）+ 分页前进（`:832` 事务内水位线 = 已交付链末端）+ §7.1.5 后继 turn 交付验收①-③（`:961`）在位。本轮独立算术复演：46MiB anchor → 首包对账空间 0 → 首包 anchor-only（46+2=48 恰满）→ 续页 turn phase 交付 1KiB 后继 → 水位线前移 → 下一请求 anchorRefresh 恢复正常体量，死锁链断开；9MiB 后继走整 turn 独占页（9+2=11）；42+6 首包 4MiB 空间 < 6MiB 溢出续页。三形态均有 ≤48MiB 可实现页序列。
- **R19 F-52**（自包含 cursor）：核心闭合维持——机制事实本轮亲核（`kernel.Snapshot` 当前行、fence 单请求生命周期、`boundary+1` 排他）；不变量 6 相容（判定输入全随请求携带）；「同 cut」撤回后无残留。**但复查集集合定义存在 R20/R21 均未检出的两处不一致，本轮新列 F-59（见意见节）——属旧漏检，不构成 F-52 闭合的回归（F-52 的闭合主张「撤回同 cut + 单调覆盖论证成立」不受影响，受影响的是复查集的构造口径）。**
- **R19 F-53**（两级 typed 链）：维持闭合——§3.8 超限恢复语义（`:824-826`）、§3.9 总表、R-2 双失败线（`:353`）、§8 两行（`:1053-1054` 15s 时间线 / 字节线）、E-11 full 超限反例（`:884`）、§7.1.5 full 超限验收（`:963` 含「解压拒绝不得计入恢复成功」）本轮交叉核对一致；「contingency `window_0` 不是恢复路径」的 50MiB 双点约束本轮在 iOS 新提交重核属实。
- **R18 三处 / F-49 / F-50 / F-51**：维持闭合——46/46+1 边界统一独占额度口径（`:959`）、anchorRefresh 仅首包（`:805/:816`）、组合验收①-③/⑤（`:960`）、F-49 组合路径与 §7.1.2 范围限定（`:559/:940`）、F-50 token 全 ops 终态（`:800`）+ §7.2.13（`:986`）、F-51 Mac restart 单行（`:1009`）。**未见回归。**
- **「超 2 页」计数口径**：三处（OD-7 `:864`「turns 页数计」/ §3.8 `:817`「turns 总页数计入」/ §7.3 `:1011`「按 turns 页数计」）措辞一致，自然读法 = 按承载 turns 的页数计（anchor-only 首页不计）；三个登记用例在两种读法下均 ≤2 页。与 R21 判定一致，无新证据不重开（复审纪律：不无限生成同义意见）。

**历轮 F-1~F-58 可追溯性**：§0 修订历史 v1→v25 全文 SHA-256 级联完整（v25 哈希与 R21 报告记录一致），v26 正文哈希在交接块且本轮实测吻合；F-1~F-58 各有修订表条目与闭合记录，链可追溯。

## 意见

### F-59 [阻塞]（旧问题漏检——v24 文本，R20/R21 未检出；非 v26 回归）复查集定义两处不一致：§3.8 规范文本限定「剩余未对账项」、§7.1.5 验收① 不带限定——两个不同集合，实现与测试无法同批判定，且收缩读法存在真实漂移窗口

- **位置 1（规范定义）**：§3.8 续页数据来源与绑定规则（方案 `:820`）：「续页请求携带 `anchorTurnId` + **完整复查集** = 原始 baseDigests **剩余未对账项** ∪ 已发项的 `(turnId → 客户端已收 token)`（含首包 anchorRefresh 的 turn 与已交付 turns）」。§0.2 v24 历史表（`:39`）同口径（「原始 baseDigests 剩余 ∪ 已发项 token」）。
- **位置 2（测试断言）**：§7.1.5 自包含 cursor 验收①（方案 `:962`）：「① 续页请求携带完整复查集（**原始 baseDigests ∪ 已发项 token**，含 anchorRefresh turn 与已交付 turns）」——**无「剩余未对账项」限定**。
- **证据（两种读法产出两个不同请求集合）**：设基底 [A, B(anchor)]，页 1 复查 A token 命中（确认无修改）、changedTurns/turns 尚有剩余需续页。按 §3.8 条目级读法（「已对账」= 该项复查完成），A 在页 1 即完成对账、从复查集移除——续页请求不含 A；按验收①读法，续页请求携带全部原始 baseDigests 项 ∪ 已发项 token——含 A。**按 §3.8 实现，验收①「携带完整复查集（原始 baseDigests ∪ …）」断言失败；按验收①实现，§3.8 的「剩余未对账项」限定被违反。** §3.8 单调覆盖②（`:822`）只描述「Mac 对复查集中**已发项**比对当前 digest」、验收②（`:962`）的跨 cut 复查构造也只覆盖「已发 turn 被修改」——两处的机制描述均与收缩读法自洽、与验收①的全集读法不匹配，无法以「作者意图 = 全集」消解二义。
- **影响（不只是措辞）**：收缩读法存在真实漂移窗口——页 1 确认的基底项 A 在页间被 live patch 修改时：A 已不在复查集（不会被复查重发）、A 不是本事务已发项（不适用单调覆盖②）、其新终态不在任何对账页内；唯一交付载体是恢复门排队的 patch（§3.4.3 `:683`「排队待门释放后按序 apply，**或按 rev 版本校验丢弃重排**」）——而该 patch 的 rev ≤ 采用事务采纳的最后一页 head（修改发生在末页快照之前），走「版本校验丢弃」合法分支即被丢弃，A 内容丢失、projection 声称已达 head、syncing 已清 = R-7 假成功形态。验收②未覆盖「确认项页间被修改」构造，E-11 离线修改反例覆盖的是事务开始前的修改，均不触及此窗口。全集读法（验收①）则每页复查全部 ≤256 项 token，页间窗口被复查关闭，且末页快照之后到达的 patch rev > head、按版本校验正常应用——对恢复门两个实现分支均稳健。
- **修订方向（最小充分，二选一）**：① **采纳全集口径**（推荐，严格更安全且成本低——每页多复查 ≤256 个 token）：§3.8 `:820` 删「剩余未对账项」限定，改为「原始 baseDigests 全部项 ∪ 已发项 token」，并把单调覆盖②的复查对象从「已发项」扩为「复查集内全部项（token 不符 → changedTurns 重发；已移除 → removedTurnIds）」；§7.1.5 验收①不变，可在验收②补「页 1 确认项在续页请求前被 live patch 修改 → 复查捕获重发」构造；或 ② **保留收缩口径**：验收①改写为「原始 baseDigests 剩余未对账项 ∪ 已发项 token」，并在 §3.4.3 显式约束恢复门的「版本校验丢弃」分支——rev ≤ 采纳 head 且其内容未被任何对账页交付/复查覆盖的 patch 不得按版本丢弃（否则丢内容）；同时补验收②的确认项修改构造走 patch 队列路径断言。
- **闭合标准**：§3.8 与 §7.1.5 验收①对复查集给出**同一集合**的唯一定义（两处文本逐字对齐或在单一读法下等价）；「页 1 确认项在续页前被修改」的交错在正文有明确覆盖路径（复查重发或 patch 队列内容保证）且验收②有对应构造；冻结评审可据单一口径写出请求字段形状，无需实施者自行选择。

## 复审处置与剩余门

| 原意见 | 本轮判定 |
| --- | --- |
| R21 F-57 | 闭合（grep 活文本零残留；§7.3 三行互洽；§3.9 唯一权威） |
| R21 F-58 | 闭合（相位边界定案 + 验收③对齐；42+6+changedTurns 唯一页序列本轮独立推演可写出，与断言⑤一致） |
| R20 F-54 / F-55 / F-56 | 维持闭合，未见回归（F-55 relay 锚点 `main.go:640` 本轮亲核属实） |
| R19 F-48 / F-53 | 维持闭合，未见回归（死锁解除算术复演、两级 typed 链口径交叉核对） |
| R19 F-52 | 核心闭合维持（撤回「同 cut」+ 单调覆盖论证成立）；复查集集合定义二义单列 F-59（旧漏检，v24 文本） |
| R18 三处 / F-49 / F-50 / F-51 | 维持闭合，未见回归 |

仍待证据/决定/授权门（与方案交接块一致，非本轮阻塞）：

- **证据门**：E-3（S-7 开工前）、E-4（anchor 页 wire 口径）、E-5、E-6、E-7、E-10、E-11（实施后定向测试 + 真机场景）；
- **决定门**：OD-9（growth-trust 档，pending owner 裁决；不阻塞 S-2 默认档 (a)）；
- **冻结门**：S-7 anchor-newer re-freeze 批次（含 F-55 epoch 语义冻结条目；**F-59 闭合后复查集请求字段形状须一并冻结**）、S-8 `direct_gzip_v1` 冻结批次（`projection_max_bytes` wire 形态归此批次）；
- **授权门**：S-6 owner 真机手动矩阵；UI automation / 真机操作未获授权（2026-10-10 agent-device 永久停用裁决）。

方案通过 ≠ 实施就绪：F-59 修复后方案层面可判 APPROVED，上述门仍按各自时点约束实施。

## 本轮动作边界声明

本轮仅写入本报告文件；未修改方案、业务代码、历轮报告；未运行 build/test；未构建、安装、部署或重启任何运行时；未触碰 iOS 工作树（其当前为干净状态）。方案全文与正文哈希在评审期间未变化（读取快照身份 = 派发身份 f9063e95…）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: f9063e95203558e93532ecb77fe71584c6e31d61952822b48e7a31eec06c5036
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r22.md
scope: full
blockers: 1
advisories: 0
open_gates: [E-3(S-7 开工前), E-4, E-5, E-6, E-7, E-10, E-11, OD-9(growth-trust 档，S-2 默认档不受阻塞), S-7/S-8 re-freeze 冻结批次, S-6 owner 真机矩阵授权]
round_complete: true
contract_version: plan-contract-v1.1
```
