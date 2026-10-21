# S-3 `projection_window_v2` summary page 冻结规范（送审稿 v3，按 R2 修订）

日期：2026-10-19。母方案：v30（R25 APPROVED）§3.3 Phase B / §6 S-3。owner 裁决
（2026-10-19，Codex 三路线分析后）：**路线 A + 显示方式 ③（正文优先）**。本文档是
v2 协议冻结的完整定义送审稿——按方案评审纪律，评审通过后才开始实施；冻结物
（协议 pack / schema / 样本 / 双仓 spec 测试 / schemaRevision）在同批次交付。

## 修订记录

**v3（2026-10-19，按评审报告 R2
`docs/2026-10-19-s3-v2-summary-freeze-spec-review-r2.md` 修订。R2 共 3 条意见
（1 阻塞 + 2 建议），逐条经源码亲核后**全部采纳**，无不采纳项）：**

- **F-R2-1（阻塞）**：例外通道识别机制定案为**不新增 wire 字段**（修订方向 1）：
  例外通道内嵌 turn 严格原样（frozen `BridgeTurnProjection`）；权限确认卡 =
  permission 族 part（`requiresPermissionConfirmation` / `permissionKind` 等，
  `projection_types.go:57-75`）、待回答问题 = `user_input` 族 pending part
  （`userInputStatus=="pending"`，`:98-115`）。删除 §3/§5 的 `interactive: true`
  表述，§5 改述为按上述两类 part 类型识别；交付物 3 增例外态实例要求。
- **F-R2-2（建议）**：§4 四层表「连接级」现状更正——该门 = `turn_detail_lazy_v1`
  echo mark（handler `:64-70` 查 `ConnTurnDetailV1`，错误文案即
  `turn_detail_lazy_v1`）；`turn_detail_chunks_v1` echo 为**另一** mark，仅用于
  `:136-141` 选择 §11.8 v2 引擎；iOS 现声明 lazy_v1 + chunks_v1 两者。定案
  「不变」不变。
- **F-R2-3（建议）**：§6 通用错误行改逐项标注 retryable（`invalid_params` /
  `protocol.capability_required` = false、`projection.hydrating` = true），与
  frozen v1 逐项相同。
- 引用偏差更正（R2 登记，非意见）：v2 记录中「`bridge-v1.md:2374` 起
  `turn_detail_lazy_v1` capability 节」的节起始行号应为 **:2377**（「v1: ONLY
  codex-remote」句在 :2379），文件归属与语义不变。

**v2（2026-10-19，按评审报告 R1
`docs/2026-10-19-s3-v2-summary-freeze-spec-review-r1.md` 修订，全文 SHA-256
`61a2bb5cd8932431fa43dbf7777ec151e4a1a5810d3c8d3cebce32522b224d5d`（R2 评审
对象）。R1 共 8 条意见
（3 阻塞 + 5 建议），逐条经源码亲核后**全部采纳**，无不采纳项）：**

- **F-R1-1（阻塞）**：§5 补齐重开对账三点定案——wire 机制（anchor-newer 请求
  additive `itemsView`）、token 基准（底层完整 committed 内容）、已下载明细行合并
  规则；§7 交付物 1 扩面 anchor-newer 章节修订，交付物 3 增对账 summary 冻结样本。
- **F-R1-2（阻塞）**：§4 新增「Claude 明细资格门」四层定案；§7 交付物 1 扩面
  §11.7/§11.8 文本修订。核实更正一处文件归属：§11.7/§11.8 冻结文本位于
  `docs/protocol/unified-bridge-protocol.md`（:990 / :1101 起；`bridge-v1.md:2374`
  起 `turn_detail_lazy_v1` capability 节另含 "v1: ONLY codex-remote" 句），交付物
  按实际文件归属扩面。
- **F-R1-3（阻塞）**：§6 错误集收口为「frozen v1 window 全集 + `summary.oversized`」，
  补 `projection_window.cursor_scope_mismatch`、`projection_window.locate_out_of_window`
  与 direction 适用说明。
- **F-R1-4（建议）**：`foldReason` 增 `budget` 值（预算降级整 turn 折叠）。
- **F-R1-5（建议）**：`projection_max_bytes` 统一改冻结 wire 字段名 `projectionMaxBytes`。
- **F-R1-6（建议）**：§2 增「字段继承与差异」定案（`directory?` / `window` 字段集 /
  anchor 参数统一 / `itemsView` 非 capability）；请求草图 cursor 注释更正——locate
  不经 cursor 续拉（v1 草图该处为笔误）。
- **F-R1-7（建议）**：§9 悬空引用「§7.4」改「§7 交付物 4」。
- **F-R1-8（建议）**：§1 F-18 修订显式覆盖 v1 window 章节两处 descriptor 语；v2 echo
  条件改引修订后单门条件集，消除循环引用。

**v1（2026-10-19）**：初版送审稿（全文 SHA-256
`3e3a80f32c732c59cdf4b3eb3d4ad6a211329c2920c7eefaf782877678d82758`，R1 评审对象）。

## 0. 决策输入（已定案，不再开放）

| 决策点 | 定案 |
|---|---|
| 路线 | A：v2 summary 首开（首屏 ≤1MiB 量级，明细按需） |
| 显示方式 | ③ 正文优先：普通问答正文直接显示；**超长正文折叠**（展开交互）；**工具过程统一收进可点开区域** |
| always-visible 例外 | 正在执行的轮次、权限确认卡、待回答问题**完整显示**，不折叠不摘要 |
| 未下载明细离线不可查看 | owner 接受（决策单明示） |
| B/C 路线 | 不采用（B 的 4MiB 非硬顶 + 需改 v14 裁决；C 保留首开痛点） |

## 1. 能力与协商

- 新增 additive capability **`projection_window_v2`**（hello 声明 + Mac echo；协商沿用
  `negotiateProjectionWindowV1` 内扩展纪律——direct `server.go` + relay `main.go` 双路径
  自动覆盖，若独立函数必须两处接线 + 双路径 echo 断言）。
- echo 条件：**修订后 v1 单门条件集**（客户端声明 `projection_window_v1` +
  `session_sync_v2` 前置 + 服务端 rollout flag——即 F-18 方向 b 修订落地后的条件集，
  不再含 descriptor 条件）+ v2 producer 落地（Claude）。不引用「v1 全部条件」这类随
  F-18 修订变动的表述，避免循环引用。
- **F-18 capability 双门偏离收口（随本冻结定案，方向 b）**：修订
  `docs/protocol/bridge-v1.md` window 章节为「hello echo 单门」语义（与两端实现一致），
  删除 descriptor 侧从未实现的第二门要求。修订显式覆盖该章节**两处** descriptor 语：
  ① echo 条件 (b)「the selected backend descriptor's projection kernel supports window
  admission」；② 客户端「MUST gate window RPCs on the descriptor list, not the global
  echo」双门句（`bridge-v1.md:2171-2175` 区段）。登记更正记录（随交付物 1 落地）。
  理由：descriptor 双门自 F-13 登记以来从未实现（`server.go:498-500` 只查 rollout
  flag + hello 接受；iOS 侧只消费连接 echo），v2 不新增第二门负担。

## 2. RPC 形状

### 请求 `get_session_projection_window_v2`

```json
{
  "sessionId": "…", "backendId": "claude",
  "direction": "window_0 | older | newer | latest | locate",
  "cursor": "…?",           // older/newer 续拉（v1 R1/R2；locate 不经 cursor 续拉）
  "limit": 100?,            // turns 数上限（≤ maxWindowTurns=256）
  "anchorTurnId": "…?",     // locate 必填（v1 R8）：裸 turnId 锚定，与 anchor-newer 水位线同族语义
  "itemsView": "summary"    // summary | full；v2 首开 = summary
}
```

### 响应 `BridgeGetSessionProjectionWindowV2Result`

```json
{
  "window": { "direction": "…", "cursor": "…", "hasOlder": true, "hasNewer": false
              /* 完整继承 v1 BridgeProjectionWindow 字段集，见下 */ },
  "turns": [ /* TurnSummaryDescriptor（§3） */ ],
  "scalarState": { "execution": {…}, "planMode": null, … },  // W1 语义镜像（v2 原生携带，不是 v1 补丁）
  "syncRev": 5601, "resume": null,
  "itemsView": "summary",
  "summaryBudget": { "payloadBytes": 812345, "foldedTurnIds": ["…"], "exceptions": ["turn-live-1"] }
}
```

- `scalarState` 必选（v2 从形状上消除 F-11——不依赖 v1 re-freeze）。
- `summaryBudget` 可观测账本：本页摘要净载荷、被折叠 turn、走例外通道的 turn。

### 字段继承与差异（相对 frozen v1 window 家族定案）

- `directory?` 继承 v1（可选，语义同 v1，types.ts `BridgeGetSessionProjectionWindowParams`）。
- 响应 `window` 对象**完整继承 v1 字段集**：`windowId / generation / coverage /
  headTurnId / tailTurnId / hasOlder / hasNewer / nextOlderCursor / nextNewerCursor`
  （types.ts `BridgeProjectionWindow`）。上方草图仅示意差异项，不是删减。`tailTurnId`
  保留 = iOS 基底缓存重开水位线（anchor-newer 裸 `anchorTurnId`）的权威来源，不由
  turns 数组末元素推导代替。
- **anchor 参数统一（母方案 §3.8「与 S-3 v2 的关系」的落点）**：v1 家族组合规则
  **不变**——`cursor` 仅 older/newer 续拉（R1/R2）；`anchorTurnId` 仅 locate 必填
  （R8）。「统一」的落点 = 两者均为**裸 turnId 锚定**语义（window locate 锚点与
  anchor-newer 水位线同族、无 opaque cursor），v2 不扩展 v1 的 cursor/anchor 组合规则。
- `itemsView` 是 v2 **请求内字段**，不是独立 capability；echo `projection_window_v2`
  即承诺 summary/full 两种取值（客户端 gate 见 §6）。

## 3. TurnSummaryDescriptor（冻结定义 1+2）

每 turn 首屏交付物（③ 语义的 wire 化）：

| 字段 | 语义 |
|---|---|
| `turnId` / `status` / `startedAt` / `completedAt` / `durationMs` | 身份与时间 |
| `userBrief` | 用户提问正文；**≤128 chars 时完整**（直接显示），超长截断至 128 chars + `truncated: true` |
| `agentBrief` | 回答正文末段（结论优先）；同上 128 chars 规则 |
| `agentBriefHead` | 回答正文开头 64 chars（③「正文优先」的双端锚定：开头+结尾，中段折叠） |
| `folded` | `true` = 该 turn 有未随页交付的完整内容（正文超阈值或含工具过程） |
| `foldReason` | `long_body \| tool_process \| both \| budget`（iOS 渲染折叠形态依据）。`budget` = 预算降级整 turn 折叠：渲染形态与 `long_body` 相同（仅 turnId + folded、无 brief），独立枚举值保证任何 `folded=true` 的 turn 都有定义的原因值，供观测与测试断言 |
| `toolProcessCount` / `toolProcessBytes` | 工具过程占位摘要（「本条含 3 段工具过程 · 47KB」的事实数据） |
| `liveState` | **例外通道（定义 2）**：正在执行的轮次完整 turns **原样**内嵌（frozen `BridgeTurnProjection`，never-split，不受 1MiB 预算约束）；权限确认卡 / 待回答问题同样以 turn 末 part **原样**内嵌，**不新增 wire 字段**（R2 定案，无 `interactive` 标记）：权限确认卡 = permission 族 part（`requiresPermissionConfirmation` / `permissionKind`，`projection_types.go:57-75`）、待回答问题 = `user_input` 族 pending part（`userInputStatus=="pending"`，`:98-115`）；iOS 按既有 part 类型识别并走 always-visible 渲染（现实现即按 part 类型渲染卡片） |
| `turnGeneration` | 与 turn_detail 家族身份绑定（定义 3） |

**预算语义（定案）**：≤1MiB 是 `turns[].(userBrief|agentBrief|agentBriefHead|工具占位)`
合计的软预算（超限 → 老 turns 降级为「仅 turnId + folded=true + foldReason=budget，
无 brief」整 turn 折叠，不截断已交付 brief）；`liveState` 例外与 `scalarState`
**不计入预算**。杜绝「为过指标截断权限卡」。

## 4. 明细按需加载（定义 6：与 turn_detail 家族关系）

- 展开折叠 turn / 工具过程 → iOS 经**既有** `session_turn_items`（v1）/ `turn_detail_chunk`
  （v2 overlay）拉取完整 parts；**新增 Claude producer**（现 handler 拒非 codex-remote，
  `session_turn_items_handler.go:71`——扩展为 Claude 支持是本 slice 的 Mac 侧工作主体）。
- chunks 家族语义（replay/seq/generation）原样复用；不平行新建第二套明细协议。
- 明细一旦经 chunks 交付并入本地投影，后续 live patch / anchorRefresh 以**增量 partOps**
  更新（不回退为完整明细重发——定义 4 的一致性约束）。

### Claude 明细资格门（四层定案：iOS 入口可见性 → handler 放行的完整判定链）

明细可达性现有四层门，本冻结逐层定案（现状锚点经亲核）：

| 层 | 现状 | v2 定案 |
|---|---|---|
| 连接级 | 门 = `turn_detail_lazy_v1` echo mark（backend 无关：handler `:64-70` 查 `ConnTurnDetailV1`，错误文案即 `turn_detail_lazy_v1`）；`turn_detail_chunks_v1` echo 为**另一** mark（`server.go` 协商独立设置），仅用于 `:136-141` 选择 §11.8 v2 引擎；iOS 现声明 lazy_v1 + chunks_v1 两者（`CCCodeBridgeTransport.swift:209/:219`） | 不变（门保持 `turn_detail_lazy_v1` echo，不随本冻结对齐为 chunks_v1） |
| backend 门 | handler 拒非 codex-remote（`:71-75`） | accept 扩为 `{codex-remote, claude}` |
| per-session 门 | codex-remote 要求 producer `HistoryMode == "paginated"`（`:127-131`） | **Claude 无 historyMode 维度**（JSONL producer 全量构建，不存在 legacy-history 会话）：等价资源门 = 既有「turn 在 committed projection 且 status=completed」判定（`:115-125`），**不引入 HistoryMode 要求**；hydrate / `turn_not_found` / 未完成 turn 判定照旧 |
| descriptor 门（iOS 入口） | 入口可见性以 backend descriptor 列表为输入（`CCCodeBridgeBackendClient.swift:251-254` `supportsTurnDetailLazy`、`ChatViewModel+TurnDetail.swift:99` `turnDetailEntryEnabled`），Claude descriptor 无 turn_detail 声明（`agent/claudecode/wire_descriptor.go:29-33`） | Claude descriptor StaticCapabilities 增加 **`turn_detail_chunks_v1`**（§11.8 终案权威声明；不加 deprecated 的 `turn_detail_lazy_v1`）。iOS `supportsTurnDetailLazy` 现实现已同时接受 chunks 声明 → descriptor 变更即解锁 Claude 会话入口，无需 iOS 门代码改动；descriptor 与 production const / descriptor 测试双面联动（沿用 `agent/codex-remote/wire_descriptor.go` 模式） |

配套协议文本修订（纳入 §7 交付物 1）：`unified-bridge-protocol.md` §11.7「适用
backend：v1 仅 `codex-remote`」与方法门控句、§11.8 对应声明语义、§4.2 能力表
`session_turn_items` 行（「仅 codex-remote descriptor 声明」→ 增 claude）；
`bridge-v1.md` `turn_detail_lazy_v1` capability 节「v1: ONLY `codex-remote`」句同步
扩展标注（v2 producer = claude）。

## 5. live patch / 缓存 / 对账一致性（定义 4）

- summary 视图下的 live patch：`partOps` 照常应用；若目标 turn 处于 `folded` 状态，patch
  只更新 `folded` 元数据（工具字节数等）不展开内容；用户已展开的 turn（本地已有明细）
  按常规完整 patch。
- 首开 summary 成功 → 基底缓存播种 **summary 形态**（缓存 schema v3：rows 带 brief +
  folded 元数据）；**legacy full 种子基底（缓存 schema ≤v2）继续以 full 形态对账**
  （anchor-newer 请求省略 `itemsView`），两类基底形态不混用。
- **重开对账的 wire 机制（定案）**：`get_session_projection_anchor_newer` 请求新增
  **additive 可选字段 `itemsView: "summary"`**（首包与续页同值；响应回显 `itemsView`）。
  客户端仅当 **v2 echo 且基底为 summary 种子**时携带；Mac 据此将 `turns` /
  `anchorRefresh` / `changedTurns` 的行以 §3 TurnSummaryDescriptor（summary 形态）
  返回——不携带完整明细，防「下次更新退回全量」。选请求字段而非「v2 echo 即
  summary 形态」的理由：anchor-newer 冻结纪律 = 服务端无 per-device 状态、全部判定
  输入随请求携带；且 legacy full 种子基底在 v2 连接上仍需 full 形态对账，echo 耦合
  会把两类基底混形态。未实现 v2 的 Mac 收到该字段按 additive 惯例忽略、行仍为 full
  形态（fail-safe；客户端 gate 保证实际不发生）。
- **token 基准（定案）**：summary 行的 token 基准 = **底层完整 committed turn 内容**
  （与 v1 anchor-newer 行同一 token 实现）；summary 形态只是响应行形状，不改变 token
  基准。由此：summary 规则演进（如 brief 长度调整）不引发假 mismatch；summary 不可见
  的底层漂移同样被复查捕获（配合下条 stale 规则触发明细重拉）。
- **已下载明细行的合并/处置（定案：对账路径二元规则，对齐 live patch 路径）**：
  - 未持明细的 folded 行收到 summary upsert → 只更新 summary 元数据（brief / folded /
    foldReason / 工具占位 / liveState），不触发明细加载。
  - 已持明细的行收到 summary upsert → 更新 summary 行并**保留明细**；upsert 携带的
    新 token 与该行**最近一次有效 token**（置 null 前最后收到的非 null token）相等 →
    底层内容未变，明细维持有效；不等或无可比对 → 明细标记 stale，**下次展开重拉**
    （fail-safe，不静默沿用可能过期明细）。
  - 既有 token 簿记不变量照旧：detail commit 属未附带新 token 的局部修改 → 该行
    token 置 null，重开由 `changedTurns` 以 summary 形态回传当前终态 + 新 token
    （有界开销：summary 行极小）；是否真有内容漂移由上一条 token 比对区分。
  - 处于 always-visible 例外态的行——正在执行的轮次（liveState），或含
    permission 族 part / `user_input` 族 pending part 的行（§3 识别定义）——
    在对账页同样走例外通道完整形态，不因 `itemsView:"summary"` 收窄。
- 用户已下载的明细随基底缓存持久化（受 4MiB 预算，从头部淘汰）。

## 6. typed error（定义 5）

**错误集 = frozen v1 window 全集 + 新增 `summary.oversized`**（不删减、不改名、不改
retryable 语义；direction 适用与 v1 一致）：

- `cursor_stale`（重试=true）——cursor 携带方向（older/newer）的解码失败 / epoch 失效；
- `projection_window.cursor_scope_mismatch`（重试=false）——cursor 跨 scope
  `(backendId, bridgeEpoch, sessionId)`；
- `projection_window.limit_exceeded`（重试=false）——`limit > maxWindowTurns(256)`；
- `projection_window.locate_out_of_window`（重试=false）——locate 专用：`anchorTurnId`
  未知或超出 kernel 保留区（诚实回 full 拉取，不伪造邻近窗口）；
- `invalid_params`（重试=false）、`protocol.capability_required`（重试=false）、
  `projection.hydrating`（重试=true）——全 direction 通用；retryable 逐项标注、
  逐项与 frozen v1 相同；
- 新增 `summary.oversized`（重试=false）——`itemsView:"summary"` 时单 turn 例外通道
  （liveState 完整内嵌）自身超 `projectionMaxBytes` → fail-closed，不静默截断。

未协商 v2 的旧端组合：客户端 gate = **v2 echo 单维度**（`itemsView` 是 v2 请求内字段
而非独立 capability，echo 即承诺两种取值）fail-closed 回 full 首开（现状路径），不假启用。

## 7. 交付物清单（冻结批次）

1. `docs/protocol/bridge-v1.md`：§Projection Window v2 章节（含 F-18 方向 b 修订——
   覆盖 echo 条件 (b) 与客户端 descriptor 双门句**两处**）+ anchor-newer 冻结章节
   additive 修订（请求 `itemsView` 字段 / summary 形态响应行 / token 基准句 / 明细行
   合并规则，不触碰已冻结字段集）+ `turn_detail_lazy_v1` capability 节 v2 producer
   （claude）扩展标注；`docs/protocol/unified-bridge-protocol.md`：§11.7/§11.8 适用
   backend 与方法门控文本 + §4.2 能力表 `session_turn_items` 行修订；
2. `docs/protocol/schema/bridge-v1.types.ts` 新类型（v2 window/summary 类型 +
   anchor-newer params `itemsView` additive 字段；字段继承与差异按 §2 定案，无
   「草图未覆盖而实施者自定」的字段）；
3. 冻结样本 `docs/protocol/samples/projection-window-v2/window-summary-spec.json` +
   `anchor-newer-summary-response-spec.json`（对账 summary 形态首包样本；两仓字节一致
   副本 + README 哈希；独立新文件，不动既有 v1 / anchor-newer 样本与其断言）；
   `window-summary-spec.json` 须含至少一个 always-visible 例外态实例（permission 族
   part 与 `user_input` pending part，按原样 part 类型内嵌、**无新 wire 字段**）；
4. Mac spec 测试（冻结常量/断言，含 §6 错误集全集与 direction 适用）+ Claude producer
   定向测试（含 §4 四层资格门断言：descriptor 声明、handler 放行、per-session 等价门、
   detail commit token 簿记）；
5. iOS spec 测试（镜像）+ summary 消费/折叠/例外/展开-chunks 链测试 + **重开 summary
   对账一致性测试**（itemsView 请求形态、token 基准、明细行合并/stale 规则、legacy
   full 种子基底不混用）；
6. `schemaRevision` bump + 注释块条目（v2 summary + anchor-newer `itemsView` additive +
   §11.7/§11.8 backend 扩展）；
7. 验收：首开 summary 实测（样本 session 7f389d08：summary 载荷 ≤1MiB、首屏时长、
   展开 turn 明细到达）；v2 未协商 → full 回归。

## 8. 非目标（本轮不做）

- older 方向网络加载（OD-8 维持 (a) 关闭；v2 的 older 只作为游标形状预留）。
- 非 Claude backend 的 v2 producer（只留协议推广空间）。
- 离线查看未下载明细（owner 已接受）。

## 9. 风险与回退

- 回退 = Mac 不 echo `projection_window_v2` → iOS 自动回 full 首开（现状），无数据迁移。
- 最大实施风险：Claude turn_detail producer 的身份绑定（turn generation ↔ JSONL 行）；
  复用 codex-remote 已验框架，测试先行（§7 交付物 4）。
- summary/明细一致性回归由 §5 规则（live patch 二元规则 / 对账 wire 机制与 token
  基准 / 明细行合并与 stale 规则）+ 定向测试锁定。
