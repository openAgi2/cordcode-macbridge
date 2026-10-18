# Claude 长 session 打开方案评审 R23（全量，fresh 模式）

日期：2026-10-18（Asia/Taipei）

## 结论

- verdict: **APPROVED（通过）**
- plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v27**
- 全文 SHA-256：`54e8203544385c50845a203a677386f7825b3b2c4a512f3f6ac4a1f1992f891e`（与派发记录一致，评审对象身份确定）
- 正文 SHA-256：`13b17fb8dc9069b9b81b7c5c0d5e1f6d5e0dc6e4b043b84e2d0c98c61c59d087`（按交接块切分规则 `raw[:idx].rstrip(\n)`、`idx = "\n---\n\n设计师交接块"` 本轮实测，与交接块声明一致）
- **派发简报版本描述与实际快照的差异登记**：任务简报正文称「当前 v24 …重点 §10.28 复核点三条（F-48/F-52/F-53）」，但派发哈希对应的是 **v27**（已按 R20→v25、R21→v26、R22→v27 三轮修订；R20/R21/R22 报告已存在于 docs/）。哈希身份已确认，本轮按 v27 全文评审：复核点覆盖 §10.28（R19 三条）+ §10.29（R20 三条）+ §10.30（R21 两条）+ §10.31（R22 一条，本轮首要对象）四组 + R19~R22 已闭合项回归 + 历轮 F-1~F-59 可追溯性。
- scope: **full**（v27 全文；简报点名的 §10.28 三条独立复核 + §10.31（F-59 修订质量）逐项判定 + R18 三处/F-49/F-50/F-51 回归 + F-1~F-59 修订历史链可追溯）
- blockers: **0**；advisories: **1**（F-60——`changedTurns` 定义句的来源限定词「baseDigests 中」未随 v27 全集复查集口径同步，字面张力但不构成实现/测试双判定冲突，见意见节定级论证）
- 评审员模式：fresh——先独立形成风险清单（复查集两处定义逐字比对 / 漂移窗口在全集口径下的关闭机理独立推演 / changedTurns 载体落点核对 / removedTurnIds 与 token 生命周期一致性 / 恢复门两分支交互 / F-54/F-57 活文本残留 grep / 打包与死锁文本未变确认 / 哈希级联实测），再读 R20/R21/R22 报告核对闭合表述。历轮结论未作为本轮判断来源。
- implementation_readiness: **方案层面通过**（F-59 闭合成立、历轮闭合项无回归、零阻塞）。按 repo 2026-09-20 评审循环纪律，APPROVED 为开发阶段唯一入口；实施仍受下述证据/决定/冻结/授权门约束。S-7 仍受 E-3 证据门；OD-9 pending 不阻塞 S-2 默认档 (a)。

## 来源身份（G1：源码分析前重新生成）

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 / 预期来源 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `f868af18bbbbff6e7f47994c11fb76b49406b33e` | `M docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（= 待审 v27 本体）+ 未跟踪 r10–r22 评审报告与 `2026-10-17-incremental-append-feasibility-report.md`；**go-bridge/MacBridge 源码不在脏清单内**；任务简报指定来源（f868af18 或更新）✓。已枚举全部 6 个 Mac 工作树，本任务唯一对应 native-message-timeline 工作树 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `7527e1ead184fa88c58b05ef86839b7ba6489009` | **工作树干净**（本轮实测）。与 R22 时点**完全同一提交且干净**——R22 在 `7527e1ea` 的 iOS 亲核本轮按命题粒度复用成立（本轮另抽查关键锚点一处确认无漂移，见锚点表） |
| U | `/Users/jacklee/Projects/codex` | `main` | `ce0323f2f2d3b25e82806cd55e237da3db4e1897`（= 方案 §2.1 记录值，本轮实测一致） | 只读架构参考；本轮未产生依赖 U 的新结论 |

本轮未构建、未安装、未部署、未运行测试、未修改方案与业务代码；唯一写入 = 本报告。

## 亲核锚点表

Mac 提交与 R22 完全一致（`f868af18`）且源码零未提交修改——R22 的 Mac 侧亲核按命题粒度复用；下表「本轮亲核」项为本轮结论直接依赖、重新执行核验的锚点。

| 命题 / 方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位 / 语义 | 亲核或复用依据 |
| --- | --- | --- | --- | --- |
| 单调覆盖④ epoch 双路径（`server.go:780` / `main.go:640`） | M `go-bridge/server.go:780`；`go-bridge/main.go:640` | `780: ack.BridgeEpoch = s.bridgeEpoch`；`640: ack.BridgeEpoch = bridgeEpoch` | 定位准确；语义支持——iOS 侧 epoch 判定在 direct/relay 两路径均可执行 | 本轮亲核（sed） |
| never-split 与上限常量（`projection_window.go:24-31`） | M 同文件 | `24-26: // …a single turn larger than the budget is still served alone — the bound limits accumulation and never splits a turn.`；`28-31: maxWindowTurns=256 / maxWindowEncodedBytes=4<<20 / defaultWindowTurns=100` | 定位准确；语义支持（OD-7 数值类别对齐、46MiB 级 never-split 独占语义同源） | 本轮亲核 |
| held 注册完成事务（`projection_window_handler.go:201-211`） | M 同文件 | `:201-206` 注释 "the held-turn set registers inside the fence release … a post-hoc registration window would … **permanently strand the detail** … Completion failure rolls the set back."；`turnIDs` 取自 `response.Turns` → `CompleteProjectionSnapshotWithHeldTurns` | 定位准确；语义支持。**实施注**：现 `turnIDs` 只含 `response.Turns`，anchorRefresh turn 与 changedTurns（含复查重发项）需按方案扩展注册——方案已登记（F-34/v23/v24），非缺陷 | 本轮亲核 |
| F-53 依据：iOS 逻辑消息 50MiB 硬顶（`BridgeWebSocketConnection.swift:12`） | I 同文件（@7527e1ea，与 R22 同提交） | `12: static let maximumMessageBytes = 50 * 1024 * 1024` | 定位准确；语义支持 | 本轮亲核（复用条件成立下的抽查） |
| F-59 修订文本四点（§3.8 :830/:832/:817、§7.1.5 :972） | 方案本体 | 见下文「F-59 闭合判定」逐字摘录 | 修订落位核对 | 本轮亲核（sed 全文提取） |
| 版本级联与交接块同步 | 本轮 python 按切分规则实测 | 全文 = `54e82035…`（= 派发哈希）；正文 = `13b17fb8…`（= 交接块声明）；行 4 级联含 v26 全文哈希 `f9063e95…`（= R22 报告记录值） | 级联与交接块同步成立 | 本轮亲测 |

**本轮运行的检查命令**（关键项）：`shasum -a 256 <plan>`；python 切分规则正文哈希实测；`git rev-parse HEAD` / `git status --porcelain`（双仓）；`git worktree list`（Mac，6 工作树枚举配对）；`git -C <codex> rev-parse main`；`grep -n "完整复查集"`；`grep -n "剩余未对账\|baseDigests 剩余"`；`grep -n "Mac 对复查集"`；`grep -n "window_0 回退\|回退 \`window_0\`\|退 window_0"`；`grep -n "同 cut\|cut 失效\|同 kernel cut"`；`grep -n "不回填"`；`sed -n` 提取 :4/:14/:816/:817/:824/:830/:832/:663/:693/:806/:972/:1155。未运行 build/test（方案阶段授权边界：只读 + 唯一报告写入）。

## R22 意见闭合判定

**F-59 [阻塞]（复查集定义二义——§3.8 收缩口径 vs §7.1.5 验收①全集口径）——闭合。**

R22 闭合标准三项逐一对照：

1. **§3.8 与 §7.1.5 验收①对复查集给出同一集合的唯一定义** ✓
   - §3.8 :830（v27 更正后规范定义）：「续页请求携带 `anchorTurnId` + **完整复查集 = 原始 baseDigests 全部项 ∪ 已发项的 `(turnId → 客户端已收 token)`（含首包 anchorRefresh 的 turn 与已交付 turns）——复查集不随页收缩，每续页携带全量**」。
   - §7.1.5 :972 验收①：「续页请求携带完整复查集（原始 baseDigests ∪ 已发项 token，含 anchorRefresh turn 与已交付 turns）」。
   - 本轮逐字比对：两处并集成分相同（原始 baseDigests + 已发项 token）、包含物相同（anchorRefresh turn + 已交付 turns）、收缩行为相同（验收①的「携带完整复查集」断言在「不随页收缩」语义下可判定）——**同一集合、单一读法**。「全部项」是全集强调语，不引入第二读法。
   - 收缩口径字样残留 grep（`剩余未对账|baseDigests 剩余`）：命中仅存在于版本行修订说明（:4）、§0 v27 表意见引文（:14）、§0.3 v24 历史表时代性记录（:49，v27 修订说明明确按时代性记录纪律保留）、§3.8 :830 的「原『…』」更正注记内嵌引文——全部属豁免语境，**规范断言层零残留**（与 F-54/F-57 闭合判定的同构口径）。
2. **「页 1 确认项在续页前被修改」的交错在正文有明确覆盖路径且验收②有对应构造** ✓
   - 机制覆盖：单调覆盖②（:832）已扩为「Mac 对复查集内**全部项**比对当前 digest……token 失效（页间被 live patch 改动——**无论该项是否已在早页确认**）→ 本页重发当前终态，upsert 覆盖早页内容（幂等）」。
   - 验收构造：§7.1.5 验收②（:972）补「再构造『早页复查命中（确认无修改）的基底项在续页请求前被 live patch 修改』（v27 补 F-59）→ 续页复查集仍含该项（全集不收缩）→ token 失效 → 续页重发新终态」。
   - removedTurnIds 同步：:817「复查集中**任何项**被移除同经本字段返回……被移除项（无论已发/未发/早页确认）的正确处置是 tombstone 而非重发」——单调覆盖②的移除分支同步扩为「全部复查项」。
3. **冻结评审可据单一口径写出请求字段形状，无需实施者自行选择** ✓
   - 请求侧形状 = 一个 `turnId → token` 全量 map（原始 baseDigests 全部项 ∪ 已发项），定义唯一；§3.8 请求形状（:806）的 baseDigests（首包，缓存基底全部 ≤256 项）与复查集（续页，基底全部项 ∪ 已发项）的关系在 :830 闭环。
   - 冻结门登记：交接块 open_gates（:1155）「S-7/S-8 re-freeze 批次**(含 F-59 全集复查集请求字段形状冻结)**」在位；§3.8 re-freeze 批次的冻结对象（anchor-newer 协议小节/schema/样本）涵盖请求形状，且 §10.31③ 将该项列为复核点自查。

**F-59 漂移窗口在全集口径下关闭的独立推演（本轮重做，非复用 R22）**：设基底 [A, B(anchor)]，页 1 复查 A token 命中（确认无修改）、仍有 correctionsPending；页 1 响应后、页 2 请求前，Mac kernel 上 A 被 live patch 改为 A′（rev r₁，页₁head < r₁ ≤ 页₂head）。iOS 收到该 patch → 恢复门（§3.4.3 :693）排队。页 2 请求携带复查集 = {A, B, 已发项…}（全集）→ Mac 独立取当前 head 快照（含 A′）→ A token 失效 → 页 2 changedTurns 重发 A′（附新 token）→ 对账完成事务采用页 2 head → 门释放后排队 patch（r₁ ≤ head）按版本校验丢弃——**内容已由对账页交付，两个恢复门实现分支（排队按序 apply / 版本校验丢弃重排）均不丢内容**；A 在页 2 后再被改由页 3 同理捕获（单调收敛）；末页快照之后的修改（rev > head）按版本校验正常应用。收缩口径下该窗口的唯一载体是恢复门 patch 且可被合法丢弃（R22 反例成立）；全集口径以每页多比对 ≤256 个 token 的成本将其关闭——**方案 :830 注记的论证经本轮独立推演成立**。附带核对：复查集 map 中同一 turnId 的 token 值合并语义（原始 null/旧 token vs 已发新 token）未显式定义，但任何偏差（回传旧 token/null）都触发 Mac 重发当前终态（幂等），最坏冗余、无正确性影响——实现细节，冻结评审定义字段形状时明确即可，不构成缺陷。

**R21 锚点表补充项（relay epoch `main.go:640` 入册）——维持落地**（:832 单调覆盖④在位，本轮亲核锚点属实）。

## §10.28 三点独立复核（简报点名重点，在 v27 的维持状态）

**① 死锁解除（F-48）——维持成立。** §3.8 打包 3（:827 续页 = 内容页：turn phase 先于 correction phase、never-split 独占页、turns 总页数计入「超 2 页」判定）+ 分页前进（:842 事务内水位线 = 已交付链末端）+ §7.1.5 后继 turn 交付验收①-③（:971）本轮全文阅读确认在位且 v27 未触碰。算术复算：46MiB anchor + 2MiB mandatory = 48 恰满总硬顶 → 首包对账空间 min(8, 48−2−46) = 0 → anchor-only 首包 → 续页 turn phase 交付 1KiB 后继（1+2 ≤48）→ 水位线前移至该 turn → 下一请求 anchorRefresh 恢复正常体量，死锁链断开；9MiB 后继整 turn 独占页（9+2=11）；42+6MiB 首包 4MiB 空间 < 6MiB 溢出续页（相位边界 :824 v26 定案在位）。三形态均有 ≤48MiB 唯一可实现页序列。

**② 自包含 cursor 单调覆盖（F-52）——维持成立，且 v27 全集口径修补后更完整。** 机制事实（`kernel.Snapshot` 当前行、fence 单请求生命周期、`boundary+1` 排他）经 R20/R21/R22 三轮在同一 Mac 提交上亲核，本轮抽查一致；不变量 6 相容（全部判定输入随请求携带）；撤回「同 cut」后无残留（grep F：命中仅历史区/撤回声明/论证性引用与「跨 cut 复查」构造名称——该名称沿自 v24，其断言内容不依赖 cut 机制，R21/R22 均判豁免，本轮维持）。**v27 的全集扩展是对该论证的修补而非回归**：R22 判 F-59 为「F-52 闭合主张不受影响、受影响的是复查集构造口径」，本轮确认构造口径已统一。

**③ 两级 typed 链（F-53）——维持成立。** §3.8 超限恢复语义（:834-836）、§3.9 总表（:856）、R-2 双失败线（:363）、§8 两行（:1063-1064 15s 时间线/字节线）、E-11 full 超限反例（:894）、§7.1.5 full 超限验收（:973 含「解压拒绝不得计入恢复成功」）本轮交叉核对一致；v27 未触碰。iOS 50MiB 双点（direct 逻辑消息 + relay 解压输出）经 R22 在 `7527e1ea` 亲核、本轮同提交抽查属实。

## R20/R21/R19 已闭合项回归检查

- **R21 F-57**（`window_0 回退`活文本）：维持闭合——grep 命中仅版本行（:4）、更正注记内嵌引用（:847/:997/:1047）、§10.30 复核指令（:1141），规范断言层零残留；§7.3 重开增量路径行（:1024）持 FallbackFull 口径。
- **R21 F-58**（相位边界）：维持闭合——:824 v26 定案 + :971 验收③括注对齐在位；v27 未触碰打包规则。
- **R20 F-54**（v23 口径活文本）：维持闭合——cut 残留 grep 仅豁免语境（见上）；组合验收④（:970）断言收窄到 anchorRefresh、每续页独立 head 口径在位。
- **R20 F-55**（epoch 判定主体）：维持闭合——单调覆盖④（:832）判定主体定案在位（anchor = Mac 侧 / epoch = iOS 侧 hello_ack 比对 / 续页请求不携带 epoch / 复查集兜底）；与 §3.4.2「epoch ≠ 当前：流程照常」两时点两后果区分清楚。
- **R20 F-56**（已发项被移除处置）：维持闭合且被 v27 扩展兼容——removedTurnIds（:817）、单调覆盖②（:832）、验收②构造（:972）三处落位，「任何项（无论已发/未发/早页确认）」是 F-56 处置的超集，无收缩。
- **R19 F-48/F-52/F-53**：维持闭合（§10.28 三点，见上）。
- **R18 三处 / F-49/F-50/F-51**：维持闭合——46/46+1 边界统一独占额度口径（:969）、anchorRefresh 仅首包（:815/:826）、组合验收①-③/⑤（:970）、F-49 组合路径与 §7.1.2 范围限定（:569/:950）、F-50 token 全 ops 终态（:810）+ §7.2.13（:996）、F-51 Mac restart 单行（:1019）；本轮全文阅读确认 v27 修订面（§3.8 cursor 定义/单调覆盖②/removedTurnIds/验收② + 版本行/状态行/§10.31/交接块）未触碰上述任何条款。

**历轮 F-1~F-59 可追溯性**：§0 修订历史 v1→v26 全文 SHA-256 级联完整（v26 哈希 `f9063e95…` 与 R22 报告记录一致）；v27 正文哈希本轮实测 = 交接块声明；F-1~F-59 各有修订表条目与闭合记录，链可追溯。

**「超 2 页」计数口径**：三处（OD-7 :874 / §3.8 :827 / §7.3 :1021）措辞一致（turns 页数计），R21/R22 已裁定无判定分歧，无新证据不重开（复审纪律）。

## 意见

### F-60 [建议] `changedTurns` 定义句的来源限定词「baseDigests 中」未随 v27 全集复查集口径同步——与单调覆盖②存在字面张力，宜一处措辞对齐

- **位置**：§3.8 响应字段 `changedTurns`（方案 `:816`）：「baseDigests 中与当前内容 digest 不一致或 token 为 null（失效，必须回传）的 turns 的当前完整 upsert——覆盖离线期间对缓存范围内旧 turn 的修改」；对照单调覆盖②（`:832`，v27 扩展后）：「Mac 对复查集内**全部项**比对当前 digest……token 失效 → 本页重发当前终态」。
- **证据**：复查集自 v24 起即超出台底 turns（含已发项 token）、v27 扩为全集，但 `:816` 的「baseDigests 中」仍持 v19 时代限定（当时复查集 = baseDigests 本身、无二义）。已发项（新交付 turns，不在原始 baseDigests 中）页间被修改时，机制上由复查捕获、经 changedTurns 重发（响应形状内唯一 upsert 载体：`turns` = 严格后继新 turn、`anchorRefresh` = 仅首包锚点、`removedTurnIds` = tombstone），但字段句字面不含该项。
- **影响与定级论证（建议而非阻塞）**：与 F-54/F-57/F-59 判阻塞的「实现与测试无法同批判定」形态不同——(a) 全部相关验收（验收②两个构造，:972）断言的是**行为**（「续页重发当前终态/新终态」），不存在任何「changedTurns ⊆ baseDigests 项」的字段来源断言，按任一读法实现，行为断言均可通过；(b) 按字段句严格实现（已发项修改不重发）将直接违反同小节相隔 16 行的机制句 :832 并使验收② v24 构造（首页已发 turn 被修改 → 重发）失败——即字段句无法被单独实施为规范，被机制句 + 验收双重锁定；(c) 两处位于同一小节同一机制块内，不存在 F-59 那种「实现者读 §3.8、测试者读 §7.1.5」的跨角色分工风险。字面张力在冻结评审誊写协议文档的 changedTurns 字段语义描述时会造成一处需要自行取舍的措辞（与「实施者无需自行选择」的精神有轻微摩擦），故登记为建议。
- **修订方向（最小充分）**：`:816`「baseDigests 中与当前内容 digest 不一致或 token 为 null……」改为「**复查集中**与当前内容 digest 不一致或 token 为 null……」，一次措辞同步；如随下版修订，re-freeze 冻结的 changedTurns 字段语义随同一口径誊写。
- **闭合标准**：§3.8 内 changedTurns 的来源范围与单调覆盖②的复查对象在字面上指向同一集合（「复查集中」），无需读者自行调和。

## 复审处置与剩余门

| 原意见 | 本轮判定 |
| --- | --- |
| R22 F-59 | **闭合**（复查集同一集合唯一定义、单调覆盖②/removedTurnIds 同步扩展、验收②补构造、漂移窗口关闭机理本轮独立推演成立、冻结门经交接块登记） |
| R21 F-57 / F-58 | 维持闭合，未见回归（grep + 文本在位） |
| R20 F-54 / F-55 / F-56 | 维持闭合，未见回归（F-56 被 v27 扩展兼容） |
| R19 F-48 / F-52 / F-53 | 维持闭合（§10.28 三点独立复核；F-52 论证经 v27 全集修补后更完整） |
| R18 三处 / F-49 / F-50 / F-51 | 维持闭合，未见回归（v27 修订面未触碰） |

仍待证据/决定/授权门（与方案交接块一致，非本轮阻塞；**方案通过 ≠ 实施就绪**）：

- **证据门**：E-3（S-7 开工前）、E-4（anchor 页 wire 口径）、E-5、E-6、E-7、E-10、E-11（实施后定向测试 + 真机场景 9-15）；
- **决定门**：OD-9（growth-trust 档，pending owner 裁决 R-7 例外取舍；不阻塞 S-2 默认档 (a)）；
- **冻结门**：S-7 anchor-newer re-freeze 批次（含 F-55 epoch 语义冻结条目 + **F-59 全集复查集请求字段形状冻结**；F-60 若采纳，changedTurns 字段语义随同一口径誊写）、S-8 `direct_gzip_v1` 冻结批次（`projection_max_bytes` wire 形态归此批次）；
- **授权门**：S-6 owner 真机手动矩阵；UI automation / 真机操作未获授权（2026-10-10 agent-device 永久停用裁决）。

## 本轮动作边界声明

本轮仅写入本报告文件；未修改方案、业务代码、历轮报告；未运行 build/test；未构建、安装、部署或重启任何运行时；未触碰 iOS 工作树（其当前为干净状态）。方案全文与正文哈希在评审期间未变化（读取快照身份 = 派发身份 `54e82035…`）。

---

评审员交接块：

```text
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 54e8203544385c50845a203a677386f7825b3b2c4a512f3f6ac4a1f1992f891e
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r23.md
scope: full
blockers: 0
advisories: 1
open_gates: [E-3(S-7 开工前), E-4, E-5, E-6, E-7, E-10, E-11, OD-9(growth-trust 档，S-2 默认档不受阻塞), S-7/S-8 re-freeze 冻结批次(含 F-59 全集复查集请求字段形状冻结), S-6 owner 真机矩阵授权]
round_complete: true
contract_version: plan-contract-v1.1
```
