# S-3 `projection_window_v2` summary page 冻结规范评审 R2（复审）

日期：2026-10-19（Asia/Taipei）。评审对象：`docs/2026-10-19-s3-v2-summary-freeze-spec.md`（送审稿 v2，按 R1 修订）。

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-19-s3-v2-summary-freeze-spec.md`，全文 SHA-256
  `61a2bb5cd8932431fa43dbf7777ec151e4a1a5810d3c8d3cebce32522b224d5d`（与派发哈希一致；
  修订记录内登记的 v1 哈希 `3e3a80f3…` 与 R1 评审对象相符）。
- blockers: **1**；advisories: **2**。
- scope: **full 复审**＝R1 全部 8 条意见的闭合核验 + v2 修订新增文本（修订记录 / §4 四层
  资格门表 / §5 重开对账三点定案 / §6 错误集收口 / §2 字段继承与差异）的独立评审与全局
  回归。不覆盖实施代码（通过后另行实施与测试）。
- review_mode: 延续。历轮上下文经 R1 报告重建（新会话不宣称盲审）；R1 已核锚点按命题/
  锚点粒度复用并复核两侧文本与依赖未变，v2 新增锚点本轮全部亲核。
- implementation_readiness: **冻结批次实施不得开始**（本轮 REVISION_REQUIRED）。F-R2-1
  修订并过审后即可按 owner 既有授权（Mac canonical pack + iOS mirror 同批交付）开工。

## 总体判断

R1 三项阻塞（重开对账 wire 机制 / Claude 明细资格门 / 错误集收口）的修订**实质全部闭合**：
§5 三点定案与 anchor-newer 冻结纪律（服务端无 per-device 状态、判定输入随请求携带）相容且
机制可实现；§4 四层资格门链条完整、现状锚点基本准确、§11.7/§11.8/§4.2/bridge-v1 修订
全部纳入交付物 1；§6 错误集与 frozen v1 R1/R5/R6/R8/R9 逐条吻合。五条建议亦全部闭合。

本轮新发现集中在**一处 R1 漏检**：§3 例外通道引入的 `interactive: true` 标记在全部冻结
schema（Mac `projection_types.go` / `bridge-v1.md` / `types.ts`、iOS Models）中不存在，
既非已冻结字段、又未在本规范内定义（层级/类型/可选性均空白），与 liveState「原样内嵌」
及交付物 2「无草图未覆盖而实施者自定的字段」的承诺直接冲突——属冻结定义 2（always-visible
例外）的「实施者必须猜」级缺口（`bridge-v1.md` R10 冻结纪律 "implementing agents MUST NOT
guess"）。修复量级为一句定案，但因直接影响 types.ts / 冻结样本 / iOS 渲染测试三个交付物
的写法，需修订后重审。另有两条建议级表述问题（§4 表连接级能力名与实际门不符、§6 括注
易误读）。

修订记录声称「8 条意见逐条经源码亲核后全部采纳」——按复审纪律，采纳声明不作闭合证据，
本轮已逐条独立核验（见闭合表）。

## 来源与覆盖

本轮只读源码与文档，唯一写入 = 本报告。源码分析前已记录双仓身份；方案本身为工作树未提交
修订（属送审正常形态），内容身份以 SHA-256 登记。

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `d5255ed126b27de0c6decf173c1eee4a44386dd2` | 方案文档 v2 修订（SHA `61a2bb5c…`）+ R1 报告 untracked；其余干净 |
| I | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `0fbac7f57a08543def643141147ff70d9ef0c320` | 干净（git status 空） |
| 母方案 | M `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` | — | 同 M | v30（R1 已核；本轮仅取 §3.8「与 S-3 v2 的关系」:882 与 F-18 实施项 :957 两处，均属 R25 已审内容） |

M/I 提交与 R1 记录一致（仅方案文档自身修订），故 R1 已核源码锚点在「引用文本未变 + 源码
提交未变」前提下按命题复用；运行态结论（无）不涉及复用。上游 codex 参照本轮无需新取证。

外部格式专项：同 R1——新增为 CordCode 自有 wire 协议，无新外部格式 parser；JSON 草图按
拟议形状处理，冻结样本属交付物 3。

## 锚点核验表（本轮亲核；原引保持原样）

| 命题 / 方案原引用 | 实际来源及位置 | 源码原文摘录（实际行号） | 定位 / 语义结果 | 亲核或复用依据 |
| --- | --- | --- | --- | --- |
| §4 per-session 门「等价资源门 = 既有『turn 在 committed projection 且 status=completed』判定（:115-125）」 | M `go-bridge/session_turn_items_handler.go` | <code>115: if target == nil {</code><br><code>116: &nbsp;sendErr("turn_not_found",</code><br><code>120: if target.Status != "completed" {</code><br><code>122: &nbsp;sendErr("invalid_params",</code> | 定位准确（:115-125 覆盖两判定）、语义支持 | 本轮亲核 |
| §4「Claude 无 historyMode 维度（JSONL producer 全量构建）」 | M `agent/claudecode/`（grep HistoryMode/historyMode 0 命中）；`go-bridge/projection_window_older_hydrate.go:73` | <code>73: func (h \*Handlers) loadProducerState(…) \*CodexProducerState {</code> | 成立：producer state 为 codex 专属类型，claude 会话现判定 `state == nil` 即拒——支撑「不引入 HistoryMode 要求」的必要性 | 本轮亲核 |
| §4 descriptor 门「iOS `supportsTurnDetailLazy` 现实现已同时接受 chunks 声明」 | I `Services/Bridge/CCCodeBridgeBackendClient.swift` | <code>252: self.supportsTurnDetailLazy = capabilities.contains("turn_detail_lazy_v1") \|\|</code><br><code>253: &nbsp;capabilities.contains("turn_detail_chunks_v1")</code> | 成立（表引 :251-254 含注释行，跨度吻合）；`ViewModels/ChatViewModel+TurnDetail.swift:99` `func turnDetailEntryEnabled(…)` 亦吻合 | 本轮亲核 |
| §4「沿用 `agent/codex-remote/wire_descriptor.go` 模式（production const / descriptor 双面联动）」 | M `agent/codex-remote/wire_descriptor.go` | <code>22: if core.TurnDetailLazyProductionEnabled {</code><br><code>25: if core.TurnDetailChunksProductionEnabled {</code><br><code>26: &nbsp;staticCapabilities = append(staticCapabilities, "turn_detail_chunks_v1")</code> | 成立；claudecode `wire_descriptor.go:29-33` 无 turn_detail 声明（R1 已核，文本未变，复用+本轮重看） | 本轮亲核 |
| §1 F-18「两处 descriptor 语（`bridge-v1.md:2171-2175` 区段）」 | M `docs/protocol/bridge-v1.md` | <code>2171: only when BOTH (a) the server-side rollout flag is enabled and (b) the selected</code><br><code>2172: backend descriptor's projection kernel supports window admission. …</code><br><code>2174: list, not the global echo.</code>（MUST gate 句跨 :2173-2174） | 定位准确（两处语均在区段内）、语义支持：①echo 条件 (b) 与②双门句均存在且实现从未执行（server.go 单门，R1 已核前提） | 本轮亲核 |
| F-R1-2 更正「§11.7/§11.8 冻结文本位于 `unified-bridge-protocol.md`（:990 / :1101 起）；`bridge-v1.md:2374` 起 capability 节另含 v1: ONLY 句」 | M `docs/protocol/unified-bridge-protocol.md` / `bridge-v1.md` | <code>unified…md:990: ### 11.7 Session Turn Detail 按需加载（session_turn_items，2026-08-30 冻结）</code>；<code>:1101: ### 11.8 …（终案，turn_detail_chunks_v1…）</code>；<code>bridge-v1.md:2377: ### Capability: \`turn_detail_lazy_v1\` (…)</code>（:2379 "(v1: ONLY `codex-remote`…)"） | §11.7/:990、§11.8/:1101 精确；capability 节实际起始 **:2377**（原引 :2374 偏 3 行，区段语义成立）——登记为引用偏差，非意见 | 本轮亲核 |
| §4「§4.2 能力表 `session_turn_items` 行（仅 codex-remote descriptor 声明）」 | M `docs/protocol/unified-bridge-protocol.md:270` | <code>270: \| session_turn_items \| … \| turn_detail_lazy_v1（§11.7，deprecated）或 turn_detail_chunks_v1（§11.8 终案）；仅 codex-remote descriptor 声明 \|</code> | 定位准确、修订对象存在 | 本轮亲核 |
| §6 错误集「= frozen v1 window 全集 + `summary.oversized`」 | M `docs/protocol/bridge-v1.md`（window 冻结规则） | <code>R1（:2270-2272 区段）: projection_window.cursor_scope_mismatch (retryable = false)</code>；<code>R5: limit > maxWindowTurns returns projection_window.limit_exceeded (retryable = false)</code>；<code>R8: projection_window.locate_out_of_window (retryable = false)…MUST NOT fabricate a nearest-neighbor window</code>；<code>R10: implementing agents MUST NOT guess them in code</code> | 六项逐一在冻结文本中存在且 retryable 语义与 §6 一致（capability_required retryable=false 亦见 window 章节 undeclared-peer 段） | 本轮亲核 |
| §5「anchor-newer 冻结纪律 = 服务端无 per-device 状态、全部判定输入随请求携带」 | M `docs/protocol/bridge-v1.md`（Anchor-Newer FROZEN 章节） | <code>:3058-3060: Bridge 对客户端进度**完全无状态**（…）——全部判定输入随请求携带</code>；请求形状（:3079-3104：anchorTurnId/baseDigests/correctionCursor/limit）无 itemsView 字段；<code>baseDigests: null = 该行 token 失效（本地 live patch 局部修改后未获新 token），语义 =「请返回该 turn 当前终态」</code> | 成立：选「请求 additive 字段」而非「echo 耦合」的理由与冻结纪律吻合；detail commit → token null → changedTurns 回传的既有簿记语义属实 | 本轮亲核 |
| §2 字段继承与差异定案 | M `docs/protocol/schema/bridge-v1.types.ts` | <code>1382: directory?: string;</code>；<code>1386: /\*\* Opaque… Required for older/newer (R1/R2). \*/ cursor?: string;</code>；<code>1390: /\*\* locate only (R8). \*/ anchorTurnId?: string;</code>；`BridgeProjectionWindow`（:1394-1410）= windowId/generation/coverage/headTurnId/tailTurnId/hasOlder/hasNewer/nextOlderCursor/nextNewerCursor；direction 枚举 = window_0/older/newer/latest/locate（:1371-1378） | 逐字段吻合：directory 继承、cursor 仅 older/newer、anchorTurnId 仅 locate、window 字段集与 §2 清单完全一致——「统一 = 裸 turnId 锚定语义、不改组合规则」与母方案 :882「可统一 anchor 参数定义」相容（保守落点，合法） | 本轮亲核 |
| §1 echo 条件「修订后 v1 单门条件集（客户端声明 + session_sync_v2 前置 + rollout flag）」 | M `go-bridge/server.go` | <code>489: if !helloSupportsSessionSyncV2(hello) { …hello 失败 }</code>；<code>499: if s.projectionWindowEnabled && ack.Ok {</code><br><code>500: &nbsp;ack.Capabilities["projection_window_v1"] = true</code> | 成立：三条件即实现现状，§1 表述与代码一致、无循环引用 | 本轮亲核 |
| §3 `interactive: true` 是否已冻结字段 | M `go-bridge/projection_types.go`、`docs/protocol/bridge-v1.md`、`docs/protocol/schema/bridge-v1.types.ts`；I `OpenCodeiOS/OpenCodeiOS/Models/` | grep `interactive` 三处冻结 schema + iOS Models 均 0 命中（仅 bridge-v1.md :1033/:1043 无关的 "non-interactive commit" 行文）；既有 part 类型已可区分：permission 族（`projection_types.go:56-71` RequiresPermissionConfirmation/PermissionKind…）、user_input 族（:106-125 UserInputStatus pending…） | **不存在**：`interactive` 为本规范引入但未定义的新 wire 元素 → F-R2-1 | 本轮亲核 |
| §4 连接级「hello `turn_detail_chunks_v1` 声明（…:64-70 capability 门）」 | M `go-bridge/session_turn_items_handler.go` / `server.go` | <code>handler:66: if !h.eventPublisher.ConnTurnDetailV1(conn) {</code>；<code>68: "session_turn_items requires the turn_detail_lazy_v1 capability"</code>；<code>server.go:413: SetConnTurnDetailV1</code>（仅 negotiateTurnDetailLazyV1 :411 设置）；<code>server.go:361: SetConnTurnDetailChunksV1</code>（仅 :138 v2 分派用）；I `CCCodeBridgeTransport.swift:209/:219` 同时声明 lazy_v1 + chunks_v1 | 定位准确但**语义不吻合**：:64-70 门查的是 `turn_detail_lazy_v1` echo mark，非 chunks_v1 → F-R2-2 | 本轮亲核 |

复用（R1 已核、两侧文本与源码提交未变、依赖未变）：`projection_window.go:28-29`
（maxWindowTurns=256）；iOS `ProjectionBaseCacheStore.swift:26`（schemaVersion=2，I 提交未变
且干净）；样本/schemaRevision 双仓惯例（`projection-window-v1/` 样本、注释块条目）；iOS
`ProjectionStore.swift:879/:35-39` 消费链；S-6 材料 session 7f389d08；handler :71-75 backend
拒（本轮亲核重看）；`negotiateProjectionWindowV1` 双路径（main.go:693 本轮重看）。未核范围：
实施代码细节（按纪律通过后另行评审），对本结论无影响。

## R1 意见闭合表

| 原意见 | 闭合判定 | 依据 |
| --- | --- | --- |
| F-R1-1 [阻塞] 重开对账缺 wire 机制 | **闭合** | 三点齐备：(a) 机制 = anchor-newer 请求 additive `itemsView:"summary"`（首包与续页同值、响应回显；仅 v2 echo 且 summary 种子基底携带）——与冻结纪律「判定输入随请求携带」吻合（:3058-3060 亲核），legacy full 基底省略字段走 full 形态、两类基底不混用；(b) token 基准 = 底层完整 committed 内容（与 v1 行同一实现）——消除假 mismatch 且底层漂移仍被捕获；(c) 明细行合并二元规则（未持明细→仅元数据；已持明细→保留 + token 比对，不等→stale 下次展开重拉）+ detail commit→null→changedTurns summary 回传有界开销 + 例外态行不收窄。交付物 1 扩面 anchor-newer 章节、交付物 3 增 `anchor-newer-summary-response-spec.json`。实施者可直接写出两种请求/响应形状 |
| F-R1-2 [阻塞] Claude 明细资格门整面缺失 | **闭合**（余留 1 条建议级表述问题 → F-R2-2） | §4 四层表定案：连接级（不变）/ backend 门（accept 扩 {codex-remote, claude}）/ per-session 等价门（committed+completed、不引入 HistoryMode——claudecode 无 HistoryMode 维度经亲核成立，:115-125 锚点吻合）/ descriptor 门（Claude 加 `turn_detail_chunks_v1`，沿用 codex-remote const 联动模式；iOS `supportsTurnDetailLazy` 已接受 chunks 声明→descriptor 变更即解锁，:252-253 亲核）。§11.7/§11.8/§4.2/bridge-v1 capability 节修订全部纳入交付物 1（文件归属更正属实）；交付物 4 含四层断言 |
| F-R1-3 [阻塞] 错误集不完备 | **闭合** | 收口为「frozen v1 全集 + `summary.oversized`」，补 `cursor_scope_mismatch`/`locate_out_of_window`/`limit_exceeded` 及 direction 适用；与冻结文本 R1/R5/R6/R8/R9 逐条吻合（亲核）。余一括注措辞建议 → F-R2-3 |
| F-R1-4 [建议] foldReason 无预算值 | **闭合** | 增 `budget` 值，渲染形态同 long_body，且「任何 folded=true 都有定义原因值」自洽 |
| F-R1-5 [建议] snake_case 字段名 | **闭合** | §6 用 `projectionMaxBytes`（snake_case 仅存于修订记录对原意见的引述，属正常） |
| F-R1-6 [建议] 字段取舍未收口 | **闭合** | §2「字段继承与差异」定案：directory 继承、window 完整字段集（与 types.ts :1394-1410 逐字段吻合）、anchor 统一落点 = 裸 turnId 语义不改组合规则、itemsView 非 capability；cursor 注释笔误已更正；交付物 2 明示无实施者自定字段（该承诺的残余缺口见 F-R2-1） |
| F-R1-7 [建议] 悬空引用 §7.4 | **闭合** | §9 改引「§7 交付物 4」，可解析 |
| F-R1-8 [建议] F-18 范围与循环引用 | **闭合** | §1 显式覆盖两处 descriptor 语（①echo 条件 (b) ②MUST gate 双门句，:2171-2175 区段亲核存在）；echo 条件改引修订后单门条件集（与 server.go :485-502 实现一致），循环引用消除；更正记录随交付物 1 |

## 意见

### F-R2-1 [阻塞] `interactive: true` 为未定义的新 wire 元素，冻结定义 2 存留「实施者必须猜」缺口

- 位置：§3 TurnSummaryDescriptor 表 `liveState` 行（「权限卡/待答问题作为 turn 末 part
  原样携带并置 `interactive: true`」）与 §5（「处于 always-visible 例外态（liveState /
  interactive）的行」）。
- 证据：`interactive` 在全部冻结 schema 中不存在——M `go-bridge/projection_types.go`、
  `docs/protocol/bridge-v1.md`、`docs/protocol/schema/bridge-v1.types.ts` 与 I
  `OpenCodeiOS/OpenCodeiOS/Models/` grep 均 0 命中。且 §3 自述 liveState「完整 turns
  **原样**内嵌」——原样（frozen `BridgeTurnProjection`）则没有 `interactive` 字段可置，
  句子自相矛盾。既有 part 类型已可区分权限卡与待答问题（permission 族
  `projection_types.go:56-71`：RequiresPermissionConfirmation/PermissionKind…；user_input
  族 :106-125：UserInputStatus pending…），iOS 现实现即按 part 类型渲染卡片。
- 影响细化：实施者必须三选一猜测——(a) 新增 part 级 additive 布尔字段（哪些 part 类型？
  可选性？）；(b) TurnSummaryDescriptor 的 turn 级字段；(c) 无 wire 字段、按既有 part 类型
  区分（则该句误导实施者加字段）。三种选择的 types.ts（交付物 2）、冻结样本（交付物 3：
  `window-summary-spec.json` 必须实例化一个带例外态 part 的 turn）、iOS 渲染与对账测试
  （交付物 5）写法各不相同——直接违反交付物 2 自己的承诺「无『草图未覆盖而实施者自定』
  的字段」。该标记同时是 owner 显示方式 ③「权限确认卡/待回答问题完整显示」的识别机制，
  属六项冻结定义之定义 2 的组成部分。
- 影响：冻结批次无法在不发明字段形状的前提下写出类型、样本与 iOS 断言；两仓可能冻结出
  不同形状。
- 修订方向（一句定案，二选一写死）：(1) 「不新增 wire 字段：例外通道内嵌 turn 严格原样
  （frozen `BridgeTurnProjection`），权限卡 = permission 族 part、待答问题 = user_input
  pending part，iOS 按既有 part 类型识别并走 always-visible 渲染；§5『interactive』改述
  为该两类 part 的统称」；或 (2) 显式定义新字段（所在层级、类型、取值、可选性、作用 part
  范围）并同步登记 additive 规则。
- 闭合标准：修订版中例外通道的识别机制在 wire 层无歧义；按修订版可唯一写出 types.ts 字段
  与冻结样本中的例外态实例，无需实施者发明。
- 来源标注：**旧问题漏检**（R1 对 §3 定义 1+2 判「覆盖」时未核 `interactive` 的字段归属；
  v1 送审稿同句已存在），非本轮修订引入。

### F-R2-2 [建议] §4 四层表「连接级」现状描述与实际门不符（chunks_v1 ≠ 该门检查的 mark）

- 位置：§4 四层表第 1 行「hello `turn_detail_chunks_v1` 声明（backend 无关，
  `session_turn_items_handler.go:64-70` capability 门）」。
- 证据：`:64-70` 门查 `ConnTurnDetailV1`（handler :66；错误文案 "requires the
  turn_detail_lazy_v1 capability"），该 mark 仅由 `negotiateTurnDetailLazyV1` 设置
  （server.go :411-413）；`turn_detail_chunks_v1` echo 设置的是另一个 mark
  （server.go :359-361），仅用于 handler :138 的 v2 引擎分派。仅声明 chunks_v1 的连接会在
  :66 被拒；当前 iOS 同时声明两者（`CCCodeBridgeTransport.swift:209/:219`），生产链路
  可用——「不变」的定案结论本身正确，但现状描述把门的能力名写错。
- 影响：交付物 4 spec 测试作者若按表格断言「仅 chunks_v1 声明可通过连接级门」必失败；
  实施者若反向把 handler 门「对齐」为 chunks_v1，则是一次未被本冻结覆盖的协议行为变更。
- 修订方向：该行改为「连接级门 = `turn_detail_lazy_v1` echo mark（handler :64-70）；chunks
  echo mark 选择 §11.8 引擎（:136-141）；iOS 现声明 lazy_v1+chunks_v1 两者」。
- 闭合标准：表格现状列与 :64-70 实际判定一致。
- 来源标注：**本轮修订新引入**（四层表为 v2 新增文本）。

### F-R2-3 [建议] §6 通用错误行的「（重试=true）」括注易被误读为覆盖三项

- 位置：§6「`invalid_params`、`protocol.capability_required`、`projection.hydrating`
  （重试=true）——全 direction 通用」。
- 证据：frozen v1 中 `invalid_params` 与 `protocol.capability_required` 均 retryable=false
  （window 章节 undeclared-peer 段明写 `retryable = false`；handler `sendErr(…, false)`），
  仅 `projection.hydrating` 为 true。括号语法上只附着于末项，但三项并列后置注解的排布
  易被整组误读；节首「不改 retryable 语义」虽可消歧，交付物 4 的错误集全集断言作者正是
  该行读者。
- 影响：低——错误率取决于读者细心程度；一旦误读即写出与冻结 v1 矛盾的断言。
- 修订方向：把括注移到 `projection.hydrating` 紧后（如「`projection.hydrating`（重试=
  true；前两者 false 同 v1）」）或逐项标注。
- 闭合标准：该行任一读法都不得出出与 frozen v1 不同的 retryable 值。
- 来源标注：**本轮修订新引入**（错误集收口为 v2 新写文本）。

## 全局回归与继承核对（本轮复查）

- 六项冻结定义 × 母方案 §3.3：1（+budget 值后自洽）、3、4、5、6 维持 R1「覆盖/现已完整」
  判定；**2 存 F-R2-1 残余缺口**（例外通道识别机制未冻结到 wire 级）。
- OD-8 (a) older 关闭、OD-7 4MiB/256 缓存预算、scalarState additive、未协商回退 full 首开、
  F-26 双路径纪律、F-18 方向 b（owner 已裁决 + 前提 R1 亲核）——均与 R1 核对结论一致，
  修订未引入回归。
- §5 新机制与 §2「itemsView 非 capability」、§6 客户端单维度 gate、§4 例外通道、§9 回退
  （Mac 不 echo v2 → iOS full 首开）互相一致，无同禁同许冲突。
- 修订记录对 8 条意见的改动范围描述与正文实际修订逐条相符（本轮抽查 F-R1-1/2/5/6/8 全部
  对得上）；v1→v2 无未申报的实质设计变更。
- 引用偏差登记（不立意见）：修订记录「`bridge-v1.md:2374` 起 turn_detail_lazy_v1
  capability 节」——节实际起始 :2377（偏 3 行），「v1: ONLY codex-remote」句在 :2379，
  归属与语义无误。

## 复审处置与剩余门

R1 意见处置：3 阻塞全部闭合、5 建议全部闭合（闭合表见上；F-R1-2/3 的余留问题以新 ID
F-R2-2/F-R2-3 立项，均为建议级）。

仍待满足的实施门（与阻塞意见分开列示；评审通过不自动满足）：

1. F-R2-1 修订并过审（冻结文档级，修复量级约一句定案）。
2. 冻结批次七项交付物（含 anchor-newer / §11.7-11.8 / §4.2 / bridge-v1 capability 节修订
   扩面）——实施授权按 owner 既有裁决（双仓同批），评审通过后开始。
3. Claude producer 定向测试（交付物 4，含四层资格门断言）＝完成门；首开 summary 真机验收
   7f389d08（交付物 7，owner 人工矩阵）＝完成门。
4. F-18 更正记录（协议文档证据更正登记）随交付物 1 落地。

```text
verdict: REVISION_REQUIRED
plan_sha256: 61a2bb5cd8932431fa43dbf7777ec151e4a1a5810d3c8d3cebce32522b224d5d
report_path: docs/2026-10-19-s3-v2-summary-freeze-spec-review-r2.md
scope: full（R1 8 条闭合核验 + v2 修订全文独立评审与全局回归）
blockers: 1
advisories: 2
open_gates: F-R2-1 修订过审；冻结批次七项交付物（双仓同批）；Claude producer 定向测试（交付物 4）；首开 summary 真机验收 7f389d08（owner 人工）；F-18 更正记录随交付物 1
round_complete: true
contract_version: plan-contract-v1.1
```
