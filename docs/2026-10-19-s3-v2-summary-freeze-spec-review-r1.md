# S-3 `projection_window_v2` summary page 冻结规范评审 R1

日期：2026-10-19（Asia/Taipei）。评审对象：`docs/2026-10-19-s3-v2-summary-freeze-spec.md`（送审稿 v1）。

## 结论

- verdict: **REVISION_REQUIRED（不通过，需修订）**
- plan: `docs/2026-10-19-s3-v2-summary-freeze-spec.md`，全文 SHA-256
  `3e3a80f32c732c59cdf4b3eb3d4ad6a211329c2920c7eefaf782877678d82758`（与派发哈希一致）。
- blockers: **3**；advisories: **5**。
- scope: **full**（送审稿 §0–§9 全部）：协议形状与既有 window/anchor 家族一致性、六项
  冻结定义完备性（预算语义 × always-visible 例外相容性、summary/明细一致性规则、Claude
  producer 缺口可行性）、F-18 方向 b 正当性、交付物清单完备性、与母方案 v30 继承关系。
  不覆盖实施代码（按方案评审循环纪律，通过后另行实施与测试）。
- review_mode: 延续模式（本规范首轮；评审员已读母方案 v30 全文、R25 报告与本任务简报，
  不宣称盲审）。母方案历轮结论按命题/锚点粒度复用并抽样亲核，未逐条重测。
- implementation_readiness: **冻结批次实施不得开始**（本轮 REVISION_REQUIRED）。修订后
  通过即可按 owner 既有授权（Mac canonical pack + iOS mirror 同批交付）开工。

## 总体判断

送审稿方向正确、骨架合格：路线 A + 显示方式 ③ 的 wire 化成立；六项冻结定义与母方案
§3.3「v2 冻结前必须定义 1–6」逐项对得上；预算语义与 always-visible 例外相容（liveState /
scalarState 不计入预算、`summary.oversized` fail-closed、「杜绝为过指标截断权限卡」闭环）；
F-18 方向 b 的前提经本轮亲核成立；样本/schemaRevision/两仓镜像交付物全部有仓内先例。
**问题集中在三处冻结完备性缺口**：§5 与已冻结 anchor-newer 协议的交互只写了结果、没写
wire 机制；§4 Claude 明细加载的资格门（descriptor/§11.7/per-session 门/iOS 入口门）整面
缺失；§6 错误集枚举漏掉母方案点名的 `cursor_scope_mismatch` 与 shape 自身保留的 locate
方向所需错误。三者都是「实施者必须猜」级缺口，按冻结纪律（`bridge-v1.md:2331-2332`
"implementing agents MUST NOT guess"）需修订后重审。

## 来源与覆盖

G1 源码分析前已枚举双仓 worktree 并记录身份；本轮只读源码与文档、未修改任何文件（唯一
写入 = 本报告）。母方案引用的是本 worktree 内的 v30 文档（非 R25 当时的工作树身份）。

| 别名 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `d5255ed126b27de0c6decf173c1eee4a44386dd2` | 干净（git status 空） |
| I | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `0fbac7f57a08543def643141147ff70d9ef0c320` | 干净（git status 空） |
| 母方案 | M `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` | — | 同 M | 版本标记 v30（:4）；R25 APPROVED 后仅落地 F-62 措辞（R25 闭合标准明示无需再评审） |

与任务简报声明的双仓身份逐一相符。U（codex 上游）本轮无需新取证（送审稿未新增上游
语义引用；母方案 §3.3 的 Codex 参照沿用 R25 已核结论，本轮复用）。

外部格式专项：本规范新增的是 CordCode 自有 wire 协议（v2 summary），无新外部格式
parser 映射；audit-plan 接入规则已读，本轮按「拟议 wire shape 由冻结批次约束」处理，
不把送审稿的 JSON 草图当已捕获样本。

## 锚点核验表（本轮亲核）

原引保持原样；摘录取自实际行。

| 命题 / 方案原引用 | 实际来源及位置 | 源码原文摘录（实际行号） | 定位 / 语义结果 |
| --- | --- | --- | --- |
| §4「现 handler 拒非 codex-remote，`session_turn_items_handler.go:71`」 | M `go-bridge/session_turn_items_handler.go` | <code>71: if msg.BackendID != "codex-remote" {</code><br><code>72: &nbsp;// §11.7 v1: only codex-remote declares turn_detail_lazy_v1.</code><br><code>73: &nbsp;sendErr("unsupported_capability",</code> | 定位准确、语义支持。另核 :67-70 能力门（`ConnTurnDetailV1`）与 :127-131 per-session 门（`state.HistoryMode != "paginated"` 拒绝）——支撑 F-R1-2 |
| §2「limit ≤ maxWindowTurns=256」 | M `go-bridge/projection_window.go` | <code>28: maxWindowTurns         = 256</code><br><code>29: maxWindowEncodedBytes  = 4 &lt;&lt; 20</code> | 定位准确、常量吻合 |
| §1 协商「direct `server.go` + relay `main.go` 双路径自动覆盖」 | M `go-bridge/server.go` / `main.go` | <code>server.go:452: func (s *Server) negotiateProjectionWindowV1(…)</code>；<code>main.go:693: if !server.negotiateProjectionWindowV1(ack, &hello, conn) {</code> | 单函数双路径调用属实，F-26 纪律描述成立 |
| §1 F-18 前提「descriptor 侧从未实现」 | M 全仓 grep + `server.go`；I `CCCodeBridgeBackendClient.swift` | 非测试代码无一处把 `projection_window_v1` 写入 backend descriptor（grep 仅命中 server 协商与 handler 门）；<code>server.go:498-500: if s.projectionWindowEnabled && ack.Ok { ack.Capabilities["projection_window_v1"] = true</code>；I :1306-1311 注释 "usable only when the CURRENT connection echoed the frozen capability" | 前提成立：实现为 hello echo 单门，两端一致——方向 b 正当 |
| §6「`projection_max_bytes`」 | M `go-bridge/bridge_v1_schema.go` / `server.go`；`docs/protocol/bridge-v1.md` anchor 章节 | <code>bridge_v1_schema.go:92: `projectionMaxBytes` 协商（默认 50MiB…）</code>；<code>server.go:459: if req := hello.ProjectionMaxBytes; req != nil…</code> | 语义存在；冻结字段名是 camelCase `projectionMaxBytes`，送审稿用 snake_case（F-R1-5） |
| §5「缓存 schema v3」 | I `OpenCodeiOS/OpenCodeiOS/Models/ProjectionBaseCacheStore.swift` | <code>26: static let schemaVersion = 2</code> | 现值 v2，v3 为正确下一版本号 |
| §7 验收样本「session 7f389d08」 | M `docs/2026-10-19-s6-owner-matrix-materials.md` | <code>16: \| 事故样本 session 可达 \| ✅ 7f389d08（48+ turns，投影 22.8MB / gzip ~2.3MB）\|</code> | 样本存在且已用于 S-6 矩阵 |
| §5「重开 anchor 对账对 summary 行返回 summary 形态终态」 | M `docs/protocol/bridge-v1.md`（anchor-newer FROZEN 章节） | <code>3113: "anchorRefresh": { /* anchor turn 当前完整终态；仅首包 */ }</code>；<code>3114: "changedTurns": [ /* 复查集中 token 不一致或为 null 的当前完整 upsert */ ]</code>；请求字段（:3070-3109）无 itemsView/summary 信号 | 冻结面冲突证据：anchor-newer 冻结文本 = 完整终态/upsert，且请求无 summary 形态信号（F-R1-1） |
| §4「既有 `session_turn_items` / chunks 家族」消费链 | I `ProjectionStore.swift` / `CCCodeBridgeBackendClient.swift` | <code>ProjectionStore.swift:879: func pullOpenWindowed(…)</code>；<code>:35-39: func fetchTurnDetail(…) / fetchTurnDetailV2(… replaySinceChunkSeq: …)</code>；<code>CCCodeBridgeBackendClient.swift:1376/:1397</code> | 消费链存在，replay/seq 语义属实 |
| §4 Claude 明细资格现状 | M `agent/claudecode/wire_descriptor.go`；I `CCCodeBridgeBackendClient.swift` | <code>claudecode/wire_descriptor.go:29-33: StaticCapabilities: []string{"content_chunking","question_reply","external_turn_streaming"}</code>；I <code>:251-253: // descriptor list — v1 only codex-remote's paginated historyMode declares it — NOT the connection echo … supportsTurnDetailLazy = capabilities.contains("turn_detail_lazy_v1") \|\| …</code> | iOS 明细入口门 = backend descriptor；Claude descriptor 无 turn_detail 声明（F-R1-2 证据） |
| §6 错误族现状 | M `docs/protocol/bridge-v1.md` / `projection_window_handler.go` | <code>2271: projection_window.cursor_scope_mismatch (retryable = false)</code>；<code>2297: limit > maxWindowTurns returns projection_window.limit_exceeded</code>；<code>2318: projection_window.locate_out_of_window</code>；handler <code>:77-79: Code: "cursor_stale" … Retryable: &retryableTrue</code> | 家族错误集含 scope_mismatch 与 locate_out_of_window；cursor_stale wire 标志 retryable=true 属实（F-R1-3 证据） |
| §7 交付物惯例（样本/schemaRevision） | M/I `docs/protocol/samples/projection-window-v1/`（两仓一致：`window-response-spec.json` + `anchor-newer-response-spec.json` + README）；M `bridge_v1_schema.go:85-96` 注释块 + `BridgeProtocolSchemaRevision = "2026-10-18"`；I `AnchorNewerSpecTests.swift:24` 新增读取点 | 见左 | 独立样本文件（F-33）、两仓字节一致副本、schemaRevision 注释块条目均有先例，交付物 3/5/6 可落地 |

## 意见

### F-R1-1 [阻塞] §5 重开对账的「summary 形态终态」缺 wire 机制与冻结面修订，定义 4 不完整

- 位置：§5 第 2 条「重开 anchor 对账的 `anchorRefresh` / `changedTurns` 对 summary 行返回
  summary 形态终态（不携带完整明细——防『下次更新退回全量』）」。
- 证据：anchor-newer 是**已冻结**协议（`bridge-v1.md:3053` 起）：`anchorRefresh` =「anchor
  turn **当前完整终态**」（:3113）、`changedTurns` =「**当前完整 upsert**」（:3114），响应
  turn 元素类型为冻结的 `BridgeTurnProjection` 族；其请求形状（:3070-3109：anchorTurnId /
  baseDigests / correctionCursor / limit）**没有任何 summary/itemsView 信号字段**。要让
  summary 行以 summary 形态返回，必须扩展该冻结 RPC 的请求或响应形状（或定义等价机制），
  并修订 anchor-newer 冻结章节——但 §7 交付物 1 只含「bridge-v1.md §Projection Window v2
  章节（含 F-18 修订）」，**不含 anchor-newer 章节修订**。
- 影响细化（三者都属定义 4「summary/明细一致性」必须冻结的内容）：
  1. **wire 机制**：Mac 凭什么知道该会话的行要 summary 形态？（capability 组合推断？
     anchor_newer 请求加 additive 字段？）实施者必须猜。
  2. **token 基准**：`baseDigests` token 绑定「已交付内容」（anchor 冻结章节 token 生命
     周期）。summary 行的 token 是对 summary 形态还是底层完整内容计算？若对完整内容而
     iOS 存 summary 行，每次重开全量 mismatch → mass changedTurns，恰好触发 §5 想防的
     「退回全量」。
  3. **已下载明细行的合并/处置**：anchor 冻结语义「detail commit → 该行 token 置 null →
     对账必须回传当前终态」。用户展开过某 turn（明细已入缓存）后，该行 token=null，
     每次重开必回传——回传 summary 形态时，本地已持明细是替换（丢明细）还是合并（保留
     明细、标记 stale、下次展开重拉）？§5 只定义了 live patch 路径的对应规则（folded 行
     元数据更新 / 已展开行完整 patch），对账路径未定义。
- 影响：冻结批次（交付物 1/4/5）无法写出可实现、可测试的协议文本与 spec 断言；iOS
  重开一致性测试（§7 交付物 5）无从构造预期。
- 修订方向：在 §5/§7 补三件事——(a) summary 形态的 wire 机制（建议：anchor_newer 请求
  additive `itemsView:"summary"` 或「v2 echo 即 summary 形态」的显式规则，二选一写死）；
  (b) summary 行 token 计算基准一句话定案；(c) 已下载明细行收到 summary 形态
  anchorRefresh/changedTurns 时的合并/处置规则（对齐 live patch 路径的二元规则）。交付
  物 1 相应扩为「含 anchor-newer 章节的对应修订（additive，不触碰已冻结字段集）」。
- 闭合标准：修订版 §5 三点齐备，§7 交付物清单覆盖 anchor-newer 章节修订；实施者可直
  接写出首包/续拉两种请求与响应形状，无需发明。

### F-R1-2 [阻塞] Claude 明细加载的资格门整面缺失（定义 6 只有 handler 一维）

- 位置：§4「新增 Claude producer（现 handler 拒非 codex-remote，`…:71`——扩展为 Claude
  支持是本 slice 的 Mac 侧工作主体）」与 §7 交付物清单。
- 证据：明细可达性现有**四层门**，送审稿只覆盖其中一层：
  1. handler backend 硬拒（`session_turn_items_handler.go:71-75`，§4 已引）；
  2. **协议文本**：`bridge-v1.md` §11.7 冻结语义「v1: only codex-remote declares
     turn_detail_lazy_v1」（handler :72 注释同源）；
  3. **per-session 门**：handler :127-131 要求 `producerState.HistoryMode == "paginated"`
     ——Claude 会话无此 producer state，不改即全部被拒；
  4. **iOS 入口门是 descriptor 而非 echo**：`CCCodeBridgeBackendClient.swift:251-254`
     `supportsTurnDetailLazy = capabilities.contains("turn_detail_lazy_v1") || …`
     （此处 `capabilities` = backend descriptor 列表，注释明写 "descriptor list — NOT the
     connection echo"）；`ChatViewModel+TurnDetail.swift:99` 入口可见性同样以 descriptor
     声明为输入。而 `agent/claudecode/wire_descriptor.go:29-33` 的 StaticCapabilities
     **不含任何 turn_detail 声明**。
- 影响：即使 handler 扩展了 Claude，iOS 的「展开 → 加载详细过程」入口对 Claude 会话仍
  不可见（descriptor 未声明），交付物 5 的「展开-chunks 链测试」无法通过；§11.7 的
  codex-remote-only 冻结文本不修订则协议 pack 自相矛盾。实施者必须自行发明资格信号
  （descriptor 加声明？v2 echo 推断？per-session 换什么门？）——正是冻结要消灭的猜测。
- 修订方向：§4 增一段「Claude 明细资格门」定案：Claude 侧以什么信号广告明细能力
  （建议：Claude descriptor StaticCapabilities 增加 turn_detail 声明、与 production const
  双面联动，沿用 codex-remote 模式）、Claude 会话的 per-session 门等价物（或显式说明
  Claude 无此维度）、§11.7/§11.8 文本修订纳入交付物 1。
- 闭合标准：修订版写明 Claude 会话从 iOS 入口可见性到 handler 放行的完整判定链，且
  §7 交付物包含 §11.7/§11.8 对应修订。

### F-R1-3 [阻塞] §6 错误集枚举不完备：缺 `cursor_scope_mismatch` 与 `locate_out_of_window`

- 位置：§6「沿用 window 家族 + anchor 家族错误纪律：`cursor_stale`（重试=true）、
  `invalid_params`、`projection_window.limit_exceeded`、`protocol.capability_required`、
  `projection.hydrating`（重试=true）；新增 `summary.oversized`」。
- 证据：母方案 §3.3 冻结定义 5 点名为「**cursor stale / scope mismatch / limit 错误**」
  三类；v2 请求形状保留 `direction: "window_0 | older | newer | latest | locate"` 与
  cursor 续拉。既有冻结族含 `projection_window.cursor_scope_mismatch`
  （`bridge-v1.md:2271`，handler :84 实际下发）与 `projection_window.locate_out_of_window`
  （:2318，locate 方向锚失效的唯一 typed 出口）。§6 枚举两者皆无，也未声明「枚举是否
  穷尽 / 是否整体沿用 v1 全集」。
- 影响：v2 是保留 locate/older/newer 形状的 RPC，其冻结错误集却无 locate_out_of_window
  与 scope mismatch 的处置；交付物 4/5 的 spec 测试作者只能自行取舍，两仓可能冻结出
  不同错误契约。
- 修订方向：一句话收口——「v2 错误集 = frozen v1 window 全集（含
  `projection_window.cursor_scope_mismatch`、`projection_window.locate_out_of_window`）+
  新增 `summary.oversized`」，或显式列出 v2 各 direction 适用哪些族错误。
- 闭合标准：修订版错误集对保留的五个 direction 均有定义，无留白。

### F-R1-4 [建议] `foldReason` 枚举无「预算降级」值

- 位置：§3 `foldReason = long_body | tool_process | both` 与预算语义「超限 → 老 turns 降级
  为『仅 turnId + folded，无 brief』整 turn 折叠」。
- 证据：预算降级可命中「正文未超阈值且无工具过程」的 turn——其 `folded=true` 但三个
  foldReason 值都不描述其实际原因；iOS 渲染折叠形态的依据（§3 表内语义）出现未定义态。
- 影响：低频（brief 上限 128 chars × ≤256 turns 很难触顶 1MiB 软预算），但冻结枚举应
  自洽。
- 修订方向：加 `budget` 值，或一句「预算降级 turn 复用 long_body 折叠形态」。
- 闭合标准：任何 `folded=true` 的 turn 都有定义的 foldReason 与渲染形态。

### F-R1-5 [建议] `projection_max_bytes` 应使用冻结字段名 `projectionMaxBytes`

- 位置：§6「单 turn summary 例外通道自身超 projection_max_bytes」。
- 证据：冻结 wire 字段是 hello/hello_ack 的 camelCase `projectionMaxBytes`
  （anchor-newer 冻结章节「`projectionMaxBytes` 协商」；`bridge_v1_schema.go:92`；
  `server.go:459` 实现）。snake_case 是母方案行文惯用词，不是 wire 名。
- 修订方向：v2 章节与冻结样本统一用 `projectionMaxBytes`；送审稿§6 同步改写。
- 闭合标准：冻结文本中该上限只出现已冻结字段名。

### F-R1-6 [建议] RPC 形状草图相对 v1 家族的字段取舍需在章节定稿时收口

- 位置：§2 请求/响应草图。
- 证据与要点：(a) v1 params 含 `directory?`（types.ts:1382），v2 草图未提继承与否；
  (b) v1 `window` 对象含 `windowId/generation/coverage/headTurnId/tailTurnId/
  nextOlderCursor/nextNewerCursor`（types.ts:1391-1411），v2 草图仅
  `{direction, cursor, hasOlder, hasNewer}`——其中 `tailTurnId` 是 iOS 缓存水位线
  （`tailTurnId` 裸 turnId）的家族来源，v2 下水位线从何取（末 turn 推导 or 字段保留）
  应写明；(c) v1 `anchorTurnId` 是 locate-only、cursor 限 older/newer（types.ts:1389-1391），
  v2 草图写「cursor: older/newer/locate 续拉」且 anchorTurnId 采 anchor-newer 水位线语义
  ——是有意扩展（母方案 §3.8「冻结评审时可统一 anchor 参数定义」）还是笔误，需显式；
  (d) §6「客户端双维度 gate（v2 echo + itemsView 支持）」的第二维度判据不明（itemsView
  是 v2 请求内字段而非独立 capability，echo v2 即隐含支持）。
- 修订方向：章节定稿时逐项写死取舍；送审稿可加一行「字段继承 v1 家族，差异项：…」。
- 闭合标准：types.ts 新类型与冻结样本无「草图未覆盖而实施者自定」的字段。

### F-R1-7 [建议] §9「测试先行（§7.4）」为悬空引用

- 位置：§9「复用 codex-remote 已验框架，测试先行（§7.4）」。
- 证据：送审稿 §7 是七项交付物清单（无 §7.4 子节）；母方案 §7.4 是 owner 真机场景矩阵，
  与「测试先行」不对应。可能想指交付物 4（Mac spec 测试 + Claude producer 定向测试）。
- 修订方向：改为「（§7 交付物 4）」或删去括注。
- 闭合标准：引用可解析。

### F-R1-8 [建议] F-18 方向 b 修订范围与 §1 echo 条件的措辞耦合

- 位置：§1 第 2、3 条。
- 证据：v1 window 章节（`bridge-v1.md:2171-2175`）有两处 descriptor 语：①echo 条件 (b)
  「backend descriptor's projection kernel supports window admission」——实现（server.go
  :498-500，只查 `projectionWindowEnabled && ack.Ok`）同样从未实现该条件；②客户端
  「MUST gate on the descriptor list」双门句。方向 b 的「删除 descriptor 侧从未实现的
  第二门要求」应显式覆盖**两处**。另外 §1「echo 条件：`projection_window_v1` 全部条件
  + v2 producer 落地」中「v1 全部条件」在 F-18 修订落地前后内容不同，应写明引用修订后
  的单门条件集，避免循环引用。
- 修订方向：F-18 条目补一句「修订覆盖 echo 条件 (b) 与客户端双门句两处 descriptor 语」；
  echo 条件改为「修订后 v1 单门条件集（客户端声明 + session_sync_v2 + rollout flag）」。
- 闭合标准：修订后的 bridge-v1.md 无残留 descriptor 门措辞；§1 条件集自洽。

## 六项冻结定义 × 母方案继承核对

| 母方案 §3.3「v2 冻结前必须定义」 | 送审稿落点 | 本轮判定 |
| --- | --- | --- |
| 1. summary card 最小可渲染字段 | §3 TurnSummaryDescriptor 表 | 覆盖（foldReason 枚举缺口见 F-R1-4） |
| 2. active turn / permission / question 例外 | §3 liveState（never-split、不计预算、`interactive: true`） | 覆盖；与预算语义相容性成立（例外/scalar 不计预算 + `summary.oversized` fail-closed，无「为过指标截断权限卡」通路） |
| 3. item ID 与 turn generation 绑定 | §3 `turnGeneration` + §4 复用 chunks 家族（replay/seq/generation） | 覆盖（按引用冻结；家族语义已冻结且有 `fetchTurnDetailV2(replaySinceChunkSeq:)` 消费链锚点） |
| 4. live patch 对 summary 与 detail 的更新语义 | §5 三条规则 | **不完整**——重开对账路径（F-R1-1） |
| 5. cursor stale / scope mismatch / limit 错误 | §6 | **不完整**——scope mismatch 缺（F-R1-3） |
| 6. 与 turn_detail_lazy/chunks 的关系 | §4 复用（母方案倾向采纳，未另立第二协议） | 方向覆盖；Claude 资格门缺失（F-R1-2） |

其余继承核对：OD-8 (a) 维持关闭、older 仅游标形状预留——一致；OD-7 的 4MiB/256 缓存预算
与头部淘汰——一致；`scalarState` additive 于新响应类型、不触碰 frozen v1
`BridgeGetSessionProjectionWindowResult` 字段集——与 v14 约束一致；未协商回退 full 首开
——与 §3.9 权威表一致（路线 A 按 owner 2026-10-19 裁决改变已协商客户端的首开路径，§0
已记录，属合法 supersession）；F-26 双路径协商纪律——锚点吻合；F-18 方向 b——母方案
§6 登记的二选一选项之一，前提经本轮亲核成立。

## 复审处置与剩余门

首轮评审，无历轮意见闭合表。母方案 25 轮历史结论中与本切片相关者（R25 对 S-3「独立
冻结门、不得自行猜测」的边界、F-33 独立样本文件、F-26 双路径纪律）本轮均已按锚点亲核
或复用并确认被送审稿遵守。

仍待满足的实施门（与阻塞意见分开列示；评审通过不自动满足）：

1. 本轮 3 项阻塞修订并过审（冻结文档级）。
2. 冻结批次七项交付物（含 F-R1-1/F-R1-2 要求的 anchor-newer 与 §11.7/§11.8 修订扩面）
   ——实施授权按 owner 既有裁决（双仓同批），评审通过后开始。
3. Claude producer 定向测试（交付物 4）＝完成门；首开 summary 真机验收 7f389d08
   （交付物 7，owner 人工矩阵）＝完成门。
4. F-18 更正记录（协议文档证据更正登记）随交付物 1 落地。

```text
verdict: REVISION_REQUIRED
plan_sha256: 3e3a80f32c732c59cdf4b3eb3d4ad6a211329c2920c7eefaf782877678d82758
report_path: docs/2026-10-19-s3-v2-summary-freeze-spec-review-r1.md
scope: full
blockers: 3
advisories: 5
open_gates: 冻结批次七项交付物（含 anchor-newer/§11.7-11.8 修订扩面）、Claude producer 定向测试、首开 summary 真机验收 7f389d08（owner 人工）、F-18 更正记录
round_complete: true
contract_version: plan-contract-v1.1
```
