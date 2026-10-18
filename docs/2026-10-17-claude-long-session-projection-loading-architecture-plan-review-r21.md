# Claude 长 session 打开方案评审 R21（全量，fresh 模式）

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v25**
- 全文 SHA-256：`a3cdf1d2048f8ac74e62d76edeed62c9d0269275569609e05d17623aa62a11bc`（与派发记录一致，评审对象身份确定）
- 正文 SHA-256：`c89d7ccb841f88b3a98b0a8140d41d35901ed4dc2837ee077b7637b686c4b1d2`（按交接块切分规则 `raw[:idx].rstrip(\n)`、`idx = "\n---\n\n设计师交接块"` 本轮实测，与交接块声明一致）
- **派发简报版本描述与实际快照的差异登记**：任务简报正文称「当前 v24 …重点 §10.28 复核点」，但派发哈希对应的是 **v25**（已按 R20 修订）。哈希身份已确认，本轮按 v25 全文评审，复核点覆盖 §10.28（R19 三条）与 §10.29（R20 三条）两组。
- scope: **full**（v25 全文；简报点名的 §10.28 三条独立复演 + §10.29 三条闭合判定 + R19 已闭合项回归 + 历轮 F-1~F-56 可追溯性）
- blockers: **1**（F-57，F-41 时代口径活文本漏检——**非 v25 修订引入的回归**）；advisories: **1**（F-58）
- 评审员模式：fresh——先独立形成风险清单（F-54 残留 grep / F-55 epoch 判定主体事实基础 / F-56 tombstone 一致性 / 死锁解除算术 / 单调覆盖推演 / 打包确定性 / 哈希级联），再读 R20 报告核对闭合表述。R20 结论未作为本轮判断来源。
- implementation_readiness: R20 三条（F-54/F-55/F-56）**全部判闭合**；§10.28 三点（死锁解除 / 自包含 cursor 单调覆盖 / 两级 typed 链）独立复演成立；R19 已闭合项无回归。F-57 为 v10 时代指标行活文本（v19 F-41 五处清扫漏掉的第六处），与 §3.9 唯一权威规则直接冲突，按本系列自身标准（F-41/F-54 先例）判阻塞；修完即达 APPROVED 条件。S-7 仍受 E-3 证据门；OD-9 pending 不阻塞 S-2 默认档。

## 来源身份（G1：源码分析前重新生成）

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | `M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（= 待审 v25 本体）+ 未跟踪 r10–r20 评审报告与本可行性报告；go-bridge/MacBridge 源码不在脏清单内；任务简报指定来源 ✓ |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `aa3930b11e532c0aede988790732a06113800493` | **工作树干净**（R20 时点为 `da9fa03e…` + 2 项脏，本轮已推进且脏项清零）；iOS 持续漂移属 native-timeline 开发线推进，方案 §2.1 已声明时点快照语义 + S-0 重生成义务，不构成方案缺陷；本轮依赖的 iOS 锚点文件**全部本轮亲核于 `aa3930b1`**，无复用 R20 侧 iOS 核验的必要 |
| U | `/Users/jacklee/Projects/codex` | `main` | 方案 §2.1 记录值 `ce0323f2…`（本轮未产生依赖 U 的新结论，未复核，如实登记） | 只读架构参考 |

本轮未构建、未安装、未部署、未运行测试、未修改方案与业务代码；唯一写入 = 本报告。

## 亲核锚点表（本轮全部新核，无跨轮复用）

| 命题 / 方案原引用 | 实际来源及摘录（带行号） | 定位 / 语义 | 本轮核验方式 |
| --- | --- | --- | --- |
| F-55：hello_ack 携带 epoch（`server.go:780`） | M `go-bridge/server.go:780` `ack.BridgeEpoch = s.bridgeEpoch`（grep 精确命中 :780） | 定位准确；语义支持——direct 路径 hello_ack 携带 epoch | 本轮亲核（grep + sed） |
| F-55 补充：relay 路径同样携带 | M `go-bridge/main.go:640` `ack.BridgeEpoch = bridgeEpoch`；且客户端 hello 已有 `LastBridgeEpoch` 字段、服务端已有比对先例（`server.go:868/:886`） | 方案只引 direct 锚点，relay 等价机制实测同样存在——「iOS 重连后经 hello_ack 取得当前 epoch」在两条路径均成立 | 本轮亲核 |
| F-52：fence 生命周期 = 单请求内（`projection_window_handler.go:168-210`） | M `:168` `h.eventPublisher.BeginProjectionSnapshot(...)` 取 admission；错误路径 `CompleteProjectionSnapshotError`、成功路径 `:210` 附近 `CompleteProjectionSnapshotWithHeldTurns(...)` 完成即释放 | 定位准确；语义支持——不跨请求持有，「同 cut」确不可实现，撤回成立 | 本轮亲核 |
| held 注册（§3.8/§7.1.5，F-34/v23/v24 扩展） | 同上 `:201-211`：`turnIDs` 取自 `response.Turns`，注释明写 "permanently strand the detail"、"Completion failure rolls the set back" | 定位准确；语义支持。**实施注**：现 `turnIDs` 只含 `response.Turns`，anchorRefresh/anchor turn/changedTurns 需按方案扩展注册——方案已登记（非缺陷） | 本轮亲核 |
| F-52：`kernel.Snapshot` 取当前投影、无历史版本（`projection_kernel.go:1887-1902`） | M `:1887` `func (k *ProjectionKernel) Snapshot(backendID, sessionID string)` 经 `reducer.Snapshot` 返回当前投影 | 定位准确；语义支持 | 本轮亲核 |
| never-split 与上限常量（`projection_window.go:24-31`） | M `:24-26` 注释 "…never splits a turn"；`:27-31` `maxWindowTurns=256` / `maxWindowEncodedBytes=4<<20` / `defaultWindowTurns=100` | 定位准确；语义支持 | 本轮亲核 |
| window 响应现状无 scalar（`projection_window.go:77-82`） | M `:77-82` `ProjectionWindowResponse` = `Window/Turns/SyncRev/Resume` 四字段 | 定位准确；语义支持 | 本轮亲核 |
| newer 现状只能从 opaque cursor 进 + locate 裸 turnId 先例（`projection_window.go:278-301`） | M `:278-294` `case projectionWindowDirectionNewer:` → `decodeProjectionWindowCursor` + `validateProjectionWindowCursorScope`、`growWindowRange(sizes, limit, boundary+1, false)`（注释 "Strict turn-chain order (R7)"）；`:297-303` `case projectionWindowDirectionLocate:` → `indexOfTurn`，miss → `errProjectionWindowLocateOut` | 定位准确；语义支持——`boundary+1` = 严格后继；裸 turnId 锚定有冻结协议先例 | 本轮亲核 |
| F-53：iOS 逻辑消息 50MiB 硬顶（`BridgeWebSocketConnection.swift:12`） | I `:12` `static let maximumMessageBytes = 50 * 1024 * 1024` | 定位准确；语义支持 | 本轮亲核（iOS @ aa3930b1） |
| F-53：relay 解压输出同受 50MiB（`RelayBridgeFrameConnection.swift:823/:859-862`） | I ≈`:819-823` `RelayGzip.decompress(... maxOutputBytes: Int = BridgeWebSocketContract.maximumMessageBytes)`；≈`:858-863` 超限抛 `relay.decompressed_too_large` | 定位准确（在引区间内）；语义支持——gzip 不消解压上限 | 本轮亲核（同上） |
| §3.4 恢复路径锚点（`SessionProjectionWindow.swift:96-99/:123-126/:173`） | I `:95-99` `emptySession` execution 硬编码 `.idle`；`:122-126` 空页 `return self`；`:172-173` `copy.syncRev = max(syncRev, page.syncRev)` | 定位准确；语义支持（F-44 采用事务的反例依据成立） | 本轮亲核 |
| §3.4.3 两步注入（`ProjectionReplica.swift:193-212`；`ProjectionStore.swift:620-626/:1044-1056`） | I `:193-212` `applyFull(..., forceReplace:)` 绕开 stale 判断；store 层 applyFull/applyWindowPage 后紧跟 `commitIfObserved(..., lifecycle: .ready, ...)` | 定位准确；语义支持 | 本轮亲核 |
| §3.4.2 渲染 guard（`ChatViewModel.swift:1080`）与 15s cap（`ProjectionStore.swift:145`）、eligibility 单维度（`:595-599`） | I `:1080` `func renderProjectionFromStore(sessionId: String? = nil)`；`:143-147` 15s cap 注释；`:595-599` `isProjectionWindowEligible()` 只查 `supportsProjectionWindow` | 定位准确；语义支持 | 本轮亲核（grep 全量命中核对） |
| §3.2.3 source ledger 生产链（`handlers_relay.go:877/:882-884`；`claude_source_trace.go:75/:99/:122`） | M `:875-884` relay 启动即 `claudeSourceCorrelation.Observe`（注释 "Needed for every live source batch"）；`claude_source_trace.go` `:75` 对 previous cursor 重读、`:99` 调 `digestClaudeSourcePrefix(file, cursor)`、`:122` 附近 `io.CopyN` 全 prefix 读 | 定位准确；语义支持——source-ledger prefix 整读不受 trace 开关保护，v20 F-47 登记成立 | 本轮亲核 |
| 协商双路径（`server.go:808` / `main.go:693`）与 R1 冻结条款（`bridge-v1.md:2266-2272`） | M `:808` / `:693` 各自独立调用 `negotiateProjectionWindowV1`；`bridge-v1.md:2266-2272` "MUST NOT parse cursor contents, **persist cursors across bridge epochs**…" | 定位准确；语义支持（F-26 双路径纪律与水位线=裸 tailTurnId 的依据） | 本轮亲核 |
| 版本级联：v24 全文哈希声明、v25 正文哈希声明 | v25 行 4 声明 v24 全文 `4262ffd5…` 与 R20 报告记录一致（链一致，v24 本体已不在工作树、无法也无需重算）；v25 正文哈希本轮实测 = `c89d7ccb…` 与交接块声明一致 | 级联与交接块同步成立 | 本轮亲测（python 切分规则） |

## R20 意见闭合判定

**F-54（P1，v23 口径活文本两处）——闭合。**

- 闭合标准：全文活文本（§0/§10 历史区除外）无「续页不含 turns / 只含 changedTurns / 同 cut 续页 / cut 失效」残留；§7.1.5 组合验收④与后继 turn 交付验收、自包含 cursor 验收②③ 在同一续页形状下可同批通过。
- 本轮 grep 实测：`同 cut|cut 失效|同 kernel cut|续页不含|只含 changedTurns|续页只含` 全部命中仅存在于——§0 历史表（:4/:14/:27/:38）、§10 历轮评审项（:1113/:1115/:1117）、§3.8 撤回声明本身（:807/:810「『同 cut』宣称撤回」）、以及 :948/:999 的「原『…』为 v23 口径活文本」**替换注记**（文档既有的更正标注惯例，非规范断言）。规范断言层面零残留。
- §7.1.5 ④（:948）改写后：断言收窄到 anchorRefresh（turns/changedTurns 剩余片按 turn phase → correction phase 出现不属违例）；head 口径 = 每续页独立取当次 committed head、采用事务以最后一页 head 为准；typed stale 构造对齐自包含 cursor 验收③（anchor 离开保留区），不再引用 cut 概念。§7.3 行（:999）已删「续页只含 changedTurns 剩余片」、版本标记改「v24 F-48 终版」、补后继 turn 交付用例引用。三组验收（④ / 后继 turn 交付①-③ / 自包含 cursor②③）在「续页 = 内容页、每续页独立 head、采用最后一页 head」单一续页形状下可同批通过——本轮逐条比对无冲突。**闭合。**

**F-55（建议，epoch 判定主体）——闭合。**

- 闭合标准：冻结清单/正文显式覆盖，实施者无需自行发明。
- §3.8 单调覆盖④（:810）已定案判定主体：anchor 保留区 = Mac 侧 typed stale（判定输入 `anchorTurnId` 随请求携带，可执行）；epoch 中止 = **iOS 侧判定**（hello_ack epoch 比对）；**Mac 侧不比对、续页请求不携带 epoch**；re-freeze 冻结清单显式登记（:824「协议文档冻结时写明该语义，实施者无需自行发明 epoch 携带方式」）。
- 事实基础本轮亲核：direct `server.go:780`、relay `main.go:640` 均在 hello_ack 携带 epoch；服务端既有 `LastBridgeEpoch` 比对先例（`server.go:868/:886`）说明该 wire 字段已是既定机制。
- 兜底论证独立推演：「若 iOS 判定遗漏（epoch 已变而续页照发），复查集自包含幂等 + 采用事务取最后一页 head 收敛到当前 kernel 真值」——新 epoch kernel 对已发项的 token 复查（digest 不符 → changedTurns 重发覆盖）与 anchor 保留区判定（失效 → typed stale）双覆盖，未找到反例。判定正确性不依赖「epoch 变化必伴随连接断开」这一保守前提（该前提在 relay 路径即使有例外，后果只是保守中止不触发，正确性由兜底承担）。
- 与 §3.4.2 打开时点「epoch ≠ 当前：流程照常」的区分（缓存 provenance 陈旧 vs 对账事务交付链跨 epoch，两个时点两种后果）清楚，无混用空间。**闭合。**

**F-56（建议，已发项被移除处置）——闭合。**

- 单调覆盖②（:810）补「已发项不在 kernel 当前投影 → 经 `removedTurnIds` 返回 tombstone（iOS 从基底移除），不重发」；`removedTurnIds` 定义（:795）补「复查集中已发项被移除同经本字段返回」；自包含 cursor 验收②（:950）补「事务中途 compaction 移除非锚点 turn（anchor 仍在保留区）」构造。
- 与 token 生命周期不变量一致：被移除项无 token 可比，tombstone 是唯一正确处置；锚点自身被移除仍走 typed stale（Mac 侧），两分支边界清楚。水位线随 tombstone 收缩到剩余链末端可自然推导（已交付链 − 移除项），无歧义。**闭合。**

## §10.28 三点独立复演（R19 三条在 v25 的维持状态）

**① 死锁解除（F-48）——成立。** 独立算术复演：46MiB anchor → 首包对账空间 = min(8, 48−2−46) = 0 → 首包 anchor-only + mandatory 恰满 48MiB（合法）；1KiB 后继由续页 turn phase 交付（1+2 ≤48）→ 事务内水位线 = 该 turn → 下一请求 anchorRefresh 恢复正常体量，死锁链断开。9MiB 后继走整 turn 独占页（9+2=11 ≤48）；42+6MiB 首包 4MiB 空间 < 6MiB → 溢出续页（6+2=8 ≤48）。链序由 `boundary+1` 排他语义（本轮亲核 `projection_window.go` newer 分支）+ R7 保证；三个验收形态（§7.1.5 后继 turn 交付①-③）的断言（每页 ≤48MiB、链不跳、水位线最终前进、syncing 全到齐才清）在 v25 文本下可同批通过。「超 2 页」计数口径在三处（OD-7「turns 页数计」/§3.8「turns 总页数计入」/§7.3「按 turns 页数计」）措辞一致，自然读法 = 按承载 turns 的页数计（anchor-only 首页不计），三个登记用例在两种读法下均 ≤2 页，无判定分歧。

**② 自包含 cursor 单调覆盖（F-52）——论证成立，未找到反例。** 机制事实本轮亲核：`kernel.Snapshot` 当前行无历史版本（`projection_kernel.go:1887`）、fence 单请求生命周期（`projection_window_handler.go:168-210`）——「同 cut」撤回的事实基础成立；自包含 cursor（完整复查集随请求携带、每续页独立取当前 head、已发 token 失效重发覆盖、采用最后一页 head）全部判定输入在请求内，与不变量 6 相容，与现有每请求快照机制是复用而非新增。独立推演覆盖：页间 live patch（复查捕获）、事务中途 compaction（tombstone / anchor 失效双分支）、事务中途 live 新增（>2 页中止 → FallbackFull，:805 在位）、末页已发项不再复查（采用 head 即末页快照 + 稳态 patch 流 + 恢复门排队覆盖后续变化）、换连接重发幂等、双设备不串扰。§7.1.5 自包含 cursor 验收①-③ 可构造。

**③ 两级 typed 链（F-53）——三处口径一致。** typed `full.oversized` 显式终态（§3.8 超限恢复语义）+ `projection_max_bytes` hello 协商（默认 50MiB、提升档 S-7/S-8 冻结定、数值归 OD-7）+ §3.9 总表行 + R-2 双失败线（:341）+ §8 两行（:1041 15s 时间线 / :1042 字节线）+ E-11 full 超限反例 + §7.1.5 full 超限验收（含「解压拒绝不得计入恢复成功」）本轮交叉核对一致。「contingency `window_0` 不是恢复路径」论证成立：`maximumMessageBytes` 同时约束 direct 逻辑消息与 relay 解压输出（本轮 iOS 双点亲核），46MiB 级单 turn 对 window_0 同样不可传输。

**R19 已闭合项回归检查（R18 三处 + F-49/F-50/F-51）**：47MiB 边界统一独占额度口径（§7.1.5 边界验收 :947）在位；anchorRefresh 仅首包（§3.8 打包 2 :804）在位；组合验收①-③ + ⑤ 打包确定性（:948）在位；F-49 组合路径与 §7.1.2 范围限定（:547/:928）v22 口径在位；F-50 token 全 ops 终态（:788）+ §7.2.13 mixed-patch 测试（:974）在位；F-51 Mac restart 指标单行（:997，无重复）在位。**未见回归。**

**历轮 F-1~F-56 可追溯性**：§0 修订历史 v1→v25 级联完整、各版全文 SHA-256 随行；v24 声明哈希与 R20 记录一致、v25 正文哈希本轮实测吻合；v25 修订表（:8-18）如实记录 R20 三条的采纳与修订处。可追溯。

## 意见

### F-57 [阻塞]（旧问题漏检——v10 时代活文本，v19 F-41 五处清扫漏掉的第六处；非 v25 回归）§7.3「重开增量路径」行仍写「anchor typed stale → `window_0` 回退」，与 F-41 统一后的 FallbackFull 规则五处正交冲突

- **位置**：§7.3 指标表「重开增量路径」行（方案 `:1002`）：「……anchor typed stale → `window_0` 回退属合法回退不计重试」。
- **证据（冲突五处，本轮逐一核对）**：§3.8 `:821`「typed stale：anchor 不在 kernel 保留区 → 复用 cursor_stale 错误族（……fail-closed 回 **FallbackFull**，v19 更正 F-41）」；§3.9 `:834`「typed stale（anchor 离开保留区）→ **FallbackFull**」；§7.2.14 `:975`「typed stale / 超 2 页 / anchor 未 echo **一律回退全量冷路径 FallbackFull**」；§7.4 场景 14 `:1027`「→ typed stale → **全量冷路径 FallbackFull（v19 更正 F-41）**」；§8 回退边界 `:1055`「**不回 window_0——其无默认 scalar 补齐且与 v14 定案矛盾**」。§7.3 表内自身两行（`:1007`「重开路径的 full 仅由 FallbackFull 触发（typed stale…）」、`:1008`「合法回退…直接 full」）也与 `:1002` 相互矛盾。
- **影响**：同一触发条件（anchor typed stale）在正文出现两个不同默认动作——§3.9 自我声明为唯一权威可消解规范层，但实现与测试无法同批判定：按 `:1002` 写测试会断言 typed stale 触发 `window_0` 请求（将回归 v14 前形态，且 window_0 无默认 scalar 补齐 = 运行中 session 执行态显示为既有缺口），按 §3.8/§3.9 实现则该断言必失败。这与 R14 F-41 判阻塞的「回退条款仍写 window_0」、R20 F-54 判阻塞的「指标行活文本未同步」属同一性质、同一标准。
- **修订方向（最小充分）**：`:1002` 的「anchor typed stale → `window_0` 回退属合法回退不计重试」改为「anchor typed stale → **FallbackFull（全量冷路径）** 回退属合法回退不计重试」；重试计数口径本身不变。§0/§10 历史区不动。
- **闭合标准**：下一轮 grep `window_0 回退|回退 \`window_0\`|退 window_0` 全文活文本（§0/§10 历史区与「原『…』为 v14 前残留」更正注记除外）零命中；§7.3 三行（重开增量路径 / full projection open / 重试次数）在同一回退目标下互洽。

### F-58 [建议] §7.1.5 后继 turn 交付验收③ 的「首包 4MiB 空间按序装更小的对账内容」不可由 §3.8 确定性打包算法推出——相位边界行为宜显式定案

- **位置**：§7.1.5 后继 turn 交付验收③（方案 `:949`）：「42MiB anchor + 6MiB successor → 首包对账空间 4MiB < 6MiB，该 turn 溢出至续页独占交付（**首包 4MiB 空间按序装更小的对账内容**）」；对照 §3.8 打包顺序（`:802-806`）「按序填充、**装不下即切页**」+「同页内 turns 先于 changedTurns 填充」+「剩余全部 correctionsPending」。
- **证据**：在 §3.8 的自然确定性读法下，turn phase 有未决项（6MiB 后继装不进首包剩余 4MiB）时切页、首包剩余空间**不回填**——changedTurns（correction phase）不得越过未交付的 turns 提前装页（跨事务相位顺序）。括注描述的「回填更小对账内容」在该读法下不会发生。R7 严格链序同时排除更小 turns 先行的可能。
- **影响**：如所述场景（42+6MiB，无其它内容）两读法页序列相同、四条硬断言（≤48MiB / 链不跳 / 水位线前进 / syncing 全到齐才清）均通过，故不构成判定二义阻塞；但一般场景（42MiB anchor + 6MiB 后继 + changedTurns 并存）下，实施者按括注实现相位跳转回填、按 §3.8 实现切页不回填，将产出两个不同页序列——与「同一输入只允许一个合法页序列」的确定性宣称相悖，且打包确定性断言（⑤）无法裁决哪个序列才是合法序列。
- **修订方向（最小充分）**：二选一——① 删除或限定 `:949` 括注（如「首包剩余空间不回填，切页即止；changedTurns 不得越过未交付 turns 提前装页」）；② 在 §3.8 打包顺序显式定义相位边界行为（turn 装不下时本页 turn phase 终止、是否允许 correction phase 回填本页剩余空间），并使 `:949` 与之一致。
- **闭合标准**：§3.8 与 §7.1.5 ③在「装不下的 turn 之后、本页剩余空间如何处置」上只有一种读法；「42MiB anchor + 6MiB successor + changedTurns」组合能写出唯一合法页序列。

## 复审处置与剩余门

| 原意见 | 本轮判定 |
| --- | --- |
| R20 F-54 | 闭合（规范断言层零残留；残留字样仅存在于 §0/§10 历史区、撤回声明与「原『…』」替换注记；④与 §7.3 行已对齐 v24 续页内容页/独立 head 口径） |
| R20 F-55 | 闭合（判定主体定案 iOS 侧 + 双路径 hello_ack epoch 亲核 + 兜底论证独立推演成立 + re-freeze 登记在位） |
| R20 F-56 | 闭合（tombstone 处置三处落位 + 验收②构造在位 + token 生命周期一致） |
| R19 F-48 / F-52 / F-53 | 维持闭合（§10.28 三点独立复演 + 回归检查通过；F-57 为独立旧漏检，不属三者回归） |
| R18 三处 / F-49 / F-50 / F-51 | 维持闭合，未见回归 |

仍待证据/决定/授权门（与方案交接块一致，非本轮阻塞）：

- **证据门**：E-3（S-7 开工前）、E-4（anchor 页 wire 口径）、E-5、E-6、E-7、E-10、E-11（实施后定向测试 + 真机场景）；
- **决定门**：OD-9（growth-trust 档，pending owner 裁决；不阻塞 S-2 默认档 (a)）；
- **冻结门**：S-7 anchor-newer re-freeze 批次（含 v25 F-55 的 epoch 语义冻结条目）、S-8 `direct_gzip_v1` 冻结批次（`projection_max_bytes` wire 形态归此批次）；
- **授权门**：S-6 owner 真机手动矩阵；UI automation / 真机操作未获授权（2026-10-10 agent-device 永久停用裁决）。

方案通过 ≠ 实施就绪：F-57 修复后方案层面可判 APPROVED，上述门仍按各自时点约束实施。

## 本轮动作边界声明

本轮仅写入本报告文件；未修改方案、业务代码、历轮报告；未运行 build/test；未构建、安装、部署或重启任何运行时；未触碰 iOS 工作树（其当前为干净状态）。方案全文与正文哈希在评审期间未变化（读取快照身份 = 派发身份 a3cdf1d2…）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: a3cdf1d2048f8ac74e62d76edeed62c9d0269275569609e05d17623aa62a11bc
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r21.md
scope: full
blockers: 1
advisories: 1
open_gates: [E-3(S-7 开工前), E-4, E-5, E-6, E-7, E-10, E-11, OD-9(growth-trust 档，S-2 默认档不受阻塞), S-7/S-8 re-freeze 冻结批次, S-6 owner 真机矩阵授权]
round_complete: true
contract_version: plan-contract-v1.1
```
