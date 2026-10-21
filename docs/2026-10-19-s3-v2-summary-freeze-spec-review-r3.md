# S-3 `projection_window_v2` summary page 冻结规范评审 R3（复审）

日期：2026-10-19（Asia/Taipei）。评审对象：`docs/2026-10-19-s3-v2-summary-freeze-spec.md`（送审稿 v3，按 R2 修订）。

## 结论

- verdict: **APPROVED（通过）**
- plan: `docs/2026-10-19-s3-v2-summary-freeze-spec.md`，全文 SHA-256
  `14a359713c3b2dac3ccd7f146023204a1cd20d47cd96c42b9c9adfed092f3a95`（与派发哈希一致；
  修订记录登记的 v2 哈希 `61a2bb5c…` 与 R2 评审对象、v1 哈希 `3e3a80f3…` 与 R1 评审对象
  逐一相符）。
- blockers: **0**；advisories: **2**（均为命名/措辞精度级，不构成开工前置，见「意见」）。
- scope: **full 复审**＝R2 全部 3 条意见的闭合核验 + v3 修订新增文本（§3 liveState 行 /
  §4 连接级行 / §6 retryable 逐项 / §5 例外行改述 / 交付物 3 增句 / 修订记录 v3 条目）的
  独立评审 + 六项冻结定义与门控关系全局回归。不覆盖实施代码（按方案评审循环纪律，通过后
  另行实施与测试）。
- review_mode: 延续（R1/R2 评审员同一延续链）。历轮上下文经 R1/R2 报告重建，不宣称盲审；
  R1/R2 已核锚点按命题/锚点粒度复用（两侧文本与源码提交未变前提），v3 新增/变更锚点本轮
  全部亲核。
- implementation_readiness: **冻结批次七项交付物可开工**（owner 既有授权：Mac canonical
  protocol pack + iOS mirror 双仓同批；实施自本报告通过起算）。仍余完成门见「复审处置与
  剩余门」。

## 总体判断

R2 唯一阻塞 F-R2-1（`interactive: true` 未定义 wire 元素）**实质闭合**：v3 定案「不新增
wire 字段」，例外通道内嵌严格原样（frozen `BridgeTurnProjection`），识别机制 = permission
族 part（`requiresPermissionConfirmation` / `permissionKind`）与 `user_input` 族 pending
part，本轮亲核 `projection_types.go:57-75` / `:98-115` 两段锚点精确吻合，且该识别条件与
投影 kernel 自身 requires-action 谓词（`projection_reducer.go:924-933`）逐字段同构——
实施者无需发明任何字段即可写出 types.ts 与冻结样本。两条建议（F-R2-2 连接级门能力名、
F-R2-3 retryable 括注）亦闭合，锚点亲核吻合。全文 `interactive` 仅存于修订记录引述与 §3
否定句（「无 `interactive` 标记」），属正常形态。

v3 修订未引入回归：六项冻结定义全部落位；修订记录 v3 条目与正文实际改动逐项相符，无未
申报实质设计变更；门控关系（OD-8 (a)、OD-7、scalarState additive、未协商回退 full、
F-26 双路径、F-18 方向 b）与 R1/R2 核对结论一致。本轮仅余两条建议级精度备注（wire 字段名
与样本覆盖措辞），均不阻塞。

## 来源与覆盖

本轮只读源码与文档，唯一写入 = 本报告。源码分析前已记录双仓身份。

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `d5255ed126b27de0c6decf173c1eee4a44386dd2` | 方案文档 v3（SHA `14a35971…`）+ R1/R2 报告 untracked；其余干净 |
| I | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `0fbac7f57a08543def643141147ff70d9ef0c320` | 干净（git status 空） |
| 母方案 | M `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` | — | 同 M | v30 / R25 APPROVED（R1 已核；v3 未触碰其引用关系） |

M/I 提交与 R1/R2 记录一致、源码文件零改动（M 工作树 diff 仅方案文档与评审报告），故
R1/R2 已核锚点在「引用文本未变 + 源码提交未变」前提下按命题复用；F-R2 三条闭合所依赖
的锚点与 v3 新增锚点本轮全部亲核。上游 codex 参照无需新取证（v3 未新增上游语义引用）。
外部格式专项：同 R1/R2——新增为 CordCode 自有 wire 协议，无新外部格式 parser；JSON
草图按拟议形状处理，冻结样本属交付物 3。

## 锚点核验表（本轮亲核；原引保持原样）

| 命题 / 方案原引用 | 实际来源及位置 | 源码原文摘录（实际行号） | 定位 / 语义结果 | 亲核或复用依据 |
| --- | --- | --- | --- | --- |
| §3 权限确认卡 = permission 族 part（`requiresPermissionConfirmation` / `permissionKind`，`projection_types.go:57-75`） | M `go-bridge/projection_types.go` | <code>57: // RequiresPermissionConfirmation marks a pending tool that must be approved</code><br><code>61: RequiresPermissionConfirmation bool \`json:"requiresPermissionConfirmation,omitempty"\`</code><br><code>67: PermissionKind string \`json:"permissionKind,omitempty"\`</code><br><code>75: PermissionPlan interface{} \`json:"permissionPlan,omitempty"\`</code> | 定位准确（:57-75 恰为 permission 族字段块）、语义支持：两 wire 名 camelCase 无误；flag 语义即「pending tool」 | 本轮亲核 |
| §3 待回答问题 = `user_input` 族 pending part（`:98-115`） | M `go-bridge/projection_types.go` | <code>98: // user_input (Type=="user_input") — structured user input interaction (design §6/§10).</code><br><code>108: UserInputStatus string \`json:"status,omitempty"\` // pending\|answered\|rejected\|auto_resolved\|unavailable\|failed</code><br><code>115: UserInputDiagnosticCode string \`json:"diagnosticCode,omitempty"\`</code> | 定位准确（:98-115 恰为 user_input 族字段块）、语义支持；**wire 字段名为 `status`**（Go 属性 UserInputStatus）→ F-R3-1 | 本轮亲核 |
| §3/§5 识别条件与 kernel 语义同构（「iOS 按既有 part 类型识别」的前提） | M `go-bridge/projection_reducer.go` | <code>925: if t.Assistant.Parts[i].Type == "user_input" && t.Assistant.Parts[i].UserInputStatus == "pending" {</code><br><code>928-929: if …Parts[i].Type == "tool" && …Parts[i].RequiresPermissionConfirmation && (…ToolStatus == "" \|\| …ToolStatus == "pending") {</code><br><code>2117: t.Assistant.Parts[i].RequiresPermissionConfirmation = false</code>（决议时清 flag） | 成立：kernel 自身 requires-action 谓词即按 part 字段识别，permission flag 决议即清——§3/§5 的字段级识别与现语义一致，无「历史 permission part 永久触发例外」歧义 | 本轮亲核 |
| §4 连接级「handler `:64-70` 查 `ConnTurnDetailV1`，错误文案即 `turn_detail_lazy_v1`」 | M `go-bridge/session_turn_items_handler.go` | <code>66: if !h.eventPublisher.ConnTurnDetailV1(conn) {</code><br><code>67-68: sendErr("protocol.capability_required", "session_turn_items requires the turn_detail_lazy_v1 capability", false)</code> | 定位准确、语义支持（F-R2-2 闭合依据） | 本轮亲核 |
| §4「chunks echo 仅用于 `:136-141` 选择 §11.8 v2 引擎」 | M `go-bridge/session_turn_items_handler.go` | <code>138: if h.eventPublisher.ConnTurnDetailChunksV1(conn) {</code><br><code>139: h.handleSessionTurnItemsV2(conn, msg, params.SessionID, params.TurnID, target, proj.SyncRev)</code> | 定位准确、语义支持（:136-141 为 v2 dispatch 块） | 本轮亲核 |
| §4「iOS 现声明 lazy_v1 + chunks_v1 两者（`CCCodeBridgeTransport.swift:209/:219`）」 | I `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift` | <code>209: capabilities.append("turn_detail_lazy_v1")</code><br><code>219: capabilities.append("turn_detail_chunks_v1")</code> | 定位准确（两 append 恰在 :209/:219）、语义支持 | 本轮亲核 |
| §6 通用三项 retryable 逐项标注（false / false / true） | M `go-bridge/session_turn_items_handler.go` | <code>68: …"session_turn_items requires the turn_detail_lazy_v1 capability", false)</code><br><code>80: sendErr("invalid_params", "session_turn_items params decode: "+err.Error(), false)</code><br><code>95: code, retryable = "projection.hydrating", true</code> | 逐项与实现一致：capability_required=false、invalid_params=false、hydrating=true（F-R2-3 闭合依据） | 本轮亲核 |
| §4 backend 门（`:71-75`）/ 资源门（`:115-125`）/ HistoryMode 门（`:127-131`） | M `go-bridge/session_turn_items_handler.go` | <code>71: if msg.BackendID != "codex-remote" {</code>；<code>115-118: turn_not_found</code>；<code>120-123: not completed → invalid_params</code>；<code>130: state == nil \|\| state.HistoryMode != "paginated"</code> | 三行号与判定逐项吻合（R2 已核；本轮重看原文一致） | 本轮亲核（复看） |
| `userInputStatus` 属性名归属（F-R3-1 证据） | I `OpenCodeiOS/OpenCodeiOS/Models/SessionProjection.swift` / `SessionProjectionMapping.swift` | <code>SessionProjection.swift:355: case userInputStatus = "status"</code>（CodingKey：Swift 属性 userInputStatus ↔ wire `status`）；<code>SessionProjectionMapping.swift:392: let status = (part.userInputStatus.flatMap { UserInputStatus(rawValue: $0) }) ?? .unavailable</code> | `userInputStatus` 是双仓内部属性名，冻结 wire 名为 `status`；iOS 确按 part 字段渲染（支撑 §3「现实现即按 part 类型识别」） | 本轮亲核 |
| 全文 `interactive` 残留检查 | M 方案文档 v3 | grep 命中仅 2 处：:18（修订记录对原意见的引述）与 :149（§3「无 `interactive` 标记」否定句） | 无正文级残留（F-R1-5 先例：修订记录引述属正常） | 本轮亲核 |

复用（R1/R2 已核、M/I 提交未变且引用文本未变）：`projection_window.go:28`
maxWindowTurns=256；`server.go` 协商单门（:489/:498-500）与双路径（`main.go:693`）、
chunks mark 独立设置（server.go :359-361/:411-413）；`bridge-v1.md` 两处 descriptor 语
（:2171-2175）、window 错误冻结值（:2271/:2297/:2318）、capability 节（:2377，
"v1: ONLY" 句 :2379）、anchor-newer 冻结章节（:3053 起；无 per-device 状态 :3058-3060、
请求无 itemsView）；`unified-bridge-protocol.md` :990/:1101/:270；`types.ts`
:1371-1410（direction 枚举 / directory / cursor / anchorTurnId / BridgeProjectionWindow
字段集）；`claudecode/wire_descriptor.go:29-33` 无 turn_detail 声明；
`codex-remote/wire_descriptor.go` const 联动模式；iOS `BackendClient :252-253`
supportsTurnDetailLazy 接受 chunks 声明；`ProjectionBaseCacheStore.swift:26`
schemaVersion=2；`ProjectionStore.swift:879/:35-39` 消费链；session 7f389d08 样本。
未核范围：实施代码细节（按纪律通过后另行评审），对本结论无影响。

## R2 意见闭合表

| 原意见 | 闭合判定 | 依据 |
| --- | --- | --- |
| F-R2-1 [阻塞] `interactive: true` 为未定义新 wire 元素 | **闭合** | 修订方向 (1) 落地：§3 定案「不新增 wire 字段」，例外通道内嵌严格原样 frozen `BridgeTurnProjection`；识别 = permission 族 part / `user_input` 族 pending part（两段锚点本轮亲核吻合）；§5 例外行同步改述为「（§3 识别定义）」；交付物 3 增例外态实例要求（原样 part 内嵌、无新 wire 字段）。识别条件与 kernel 谓词（reducer :924-933）及 iOS 现实现（Mapping :392）同构；types.ts 零新字段、冻结样本可唯一写出——满足原闭合标准「wire 层无歧义、无需实施者发明」。余一命名精度建议 → F-R3-1 |
| F-R2-2 [建议] §4 连接级现状描述与实际门不符 | **闭合** | 表行改述为「门 = `turn_detail_lazy_v1` echo mark（handler :64-70 查 `ConnTurnDetailV1`，错误文案即 `turn_detail_lazy_v1`）；chunks_v1 为另一 mark 仅 :136-141 选 §11.8 引擎；iOS 现声明两者（:209/:219）」——逐项亲核吻合；「不变」定案未动 |
| F-R2-3 [建议] §6 retryable 括注易误读 | **闭合** | 改为逐项标注：`invalid_params`（false）、`protocol.capability_required`（false）、`projection.hydrating`（true），并声明「逐项与 frozen v1 相同」——与 handler :68/:80/:95 亲核一致，任一读法不再有歧义 |

引用偏差登记（R2 已登记，v3 修订记录如实转记）：capability 节实际起始 `bridge-v1.md:2377`
（"v1: ONLY" 句 :2379）——与 R2 亲核结论一致，归属与语义无误。

## 意见

### F-R3-1 [建议] §3 例外态识别条件使用属性名 `userInputStatus`，冻结 wire 字段名为 `status`

- 位置：§3 liveState 行「待回答问题 = `user_input` 族 pending part
  （`userInputStatus=="pending"`，`:98-115`）」（§7 交付物 4 四层断言同引）。
- 证据：冻结 wire 形状中该字段为 `status`（`bridge-v1.types.ts:884-886`
  `type: "user_input"; status: "pending" | …`；Go json tag `json:"status,omitempty"`
  `projection_types.go:108`；iOS CodingKey `case userInputStatus = "status"`
  `SessionProjection.swift:355`）。`userInputStatus` 是 Go/Swift 内部属性名，不是 wire 名。
- 影响：低——同句已声明「不新增 wire 字段」+ part 原样内嵌，实施者无字段可发明；但冻结
  样本/spec 断言作者若按字面把 `userInputStatus` 写进 JSON 样本，即造成事实上的新字段、
  与同句自相矛盾（F-R1-5 同类问题）。
- 修订方向：实施冻结文本（交付物 1/2/3）时把条件写成 wire 形状——「part `type ==
  "user_input"` 且 `status == "pending"`」（如需保留属性名，注明其为 Go/Swift 属性名）。
  方案文档本身可不改，或顺手替换。
- 闭合标准：最终冻结文本与冻结样本中该条件只出现 wire 字段名 `status`。
- 来源标注：**v3 修订新引入**（F-R2-1 修复句为 v3 新写文本）。

### F-R3-2 [建议] 交付物 3 例外态样本覆盖要求可读作单类即达标

- 位置：§7 交付物 3「`window-summary-spec.json` 须含至少一个 always-visible 例外态实例
  （permission 族 part 与 `user_input` pending part，按原样 part 类型内嵌、无新 wire
  字段）」。
- 证据：句式「至少一个…实例（A 与 B）」未写明 A、B 两类是否各需至少一个实例；字面可读
  作「一个 permission 类实例即满足」。两类识别路径（F-R2-1 定案的两组 part 判定）各自
  需要 frozen 样本锚定，交付物 4/5 的例外断言以样本为基准。
- 影响：低——最坏情形是一类识别路径缺 frozen 样本锚，spec 断言覆盖不均；无设计风险。
- 修订方向：明示「permission 族与 `user_input` pending 两类各至少一个实例（或一个同时
  含两类的实例）」。
- 闭合标准：交付物 3 的样本覆盖要求无「单类即达标」读法。
- 来源标注：**v3 修订新引入**（该句为 F-R2-1 修复新增）。

两条建议均不阻塞：不改变任何冻结定义、门控或验收结构，实施冻结批次时顺手纳入即可。

## 全局回归与继承核对（本轮复查）

- 六项冻结定义 × 母方案 §3.3：1（+budget 自洽）、2（识别机制 wire 级定案，A1 命名精度
  余留）、3、4、5、6 全部落位——与 R1/R2 累计核对结论一致，v3 未引入回归。
- OD-8 (a) older 关闭、OD-7 4MiB/256 缓存预算、scalarState additive 不触碰 frozen v1 字段
  集、未协商 v2 → full 回退、F-26 双路径协商纪律、F-18 方向 b（两处 descriptor 语 + 更正
  记录随交付物 1）——v3 均未触碰，维持 R1/R2 判定。
- §5 重开对账机制（itemsView additive 请求字段 / token 基准 = 底层完整 committed 内容 /
  明细行合并二元规则 / 例外行不收窄）v3 仅改述例外行措辞，机制文本未变，R2 闭合结论维持。
- 修订记录 v3 条目与正文实际改动逐项相符（§3 liveState 行、§4 连接级行、§6 三项标注、
  §5 例外行、交付物 3 增句、:2377 引用更正）；v2→v3 无未申报的实质设计变更；v1/v2/v3
  哈希链与 R1/R2 评审对象逐环相符。
- 无同禁同许冲突；「不新增 wire 字段」与交付物 2「无实施者自定字段」承诺现已一致成立
  （F-R2-1 闭合后该承诺不再有残余缺口）。

## 复审处置与剩余门

R2 意见处置：1 阻塞闭合、2 建议闭合（闭合表见上）；本轮新立 2 条建议（F-R3-1/F-R3-2），
均为非阻塞精度项。

仍待满足的实施/完成门（评审通过不自动满足；与意见分开列示）：

1. 冻结批次七项交付物（交付物 1 含 F-18 方向 b 修订 + anchor-newer additive 修订 +
   §11.7/§11.8/§4.2/capability 节扩面；交付物 3 两仓字节一致副本）——owner 既有授权
   （双仓同批），自本报告通过起可开工；F-R3-1/F-R3-2 在实施交付物 1/2/3 时顺手纳入。
2. Claude producer 定向测试（交付物 4，含四层资格门断言）＝完成门。
3. 首开 summary 真机验收 7f389d08（交付物 7，owner 人工矩阵；summary 载荷 ≤1MiB、首屏
   时长、展开明细到达；v2 未协商 → full 回归）＝完成门。
4. F-18 更正记录（协议文档证据更正登记）随交付物 1 落地。

```text
verdict: APPROVED
plan_sha256: 14a359713c3b2dac3ccd7f146023204a1cd20d47cd96c42b9c9adfed092f3a95
report_path: docs/2026-10-19-s3-v2-summary-freeze-spec-review-r3.md
scope: full（R2 3 条闭合核验 + v3 修订全文独立评审与全局回归）
blockers: 0
advisories: 2
open_gates: 冻结批次七项交付物（双仓同批，可开工）；Claude producer 定向测试（交付物 4）；首开 summary 真机验收 7f389d08（owner 人工）；F-18 更正记录随交付物 1；F-R3-1/F-R3-2 随冻结文本实施纳入
round_complete: true
contract_version: plan-contract-v1.1
```
