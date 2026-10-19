# S-3 `projection_window_v2` summary page 冻结规范（送审稿 v1）

日期：2026-10-19。母方案：v30（R25 APPROVED）§3.3 Phase B / §6 S-3。owner 裁决
（2026-10-19，Codex 三路线分析后）：**路线 A + 显示方式 ③（正文优先）**。本文档是
v2 协议冻结的完整定义送审稿——按方案评审纪律，评审通过后才开始实施；冻结物
（协议 pack / schema / 样本 / 双仓 spec 测试 / schemaRevision）在同批次交付。

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
- echo 条件：`projection_window_v1` 全部条件 + v2 producer 落地（Claude）。
- **F-18 capability 双门偏离收口（随本冻结定案，方向 b）**：修订
  `docs/protocol/bridge-v1.md` window 章节为「hello echo 单门」语义（与两端实现一致），
  删除 descriptor 侧从未实现的第二门要求；登记更正记录。理由：descriptor 双门自 F-13
  登记以来从未实现，v2 不新增第二门负担。

## 2. RPC 形状

### 请求 `get_session_projection_window_v2`

```json
{
  "sessionId": "…", "backendId": "claude",
  "direction": "window_0 | older | newer | latest | locate",
  "cursor": "…?",           // older/newer/locate 续拉
  "limit": 100?,            // turns 数上限（≤ maxWindowTurns=256）
  "anchorTurnId": "…?",     // 与 anchor-newer 家族共语义（裸 turnId 水位线）
  "itemsView": "summary"    // summary | full；v2 首开 = summary
}
```

### 响应 `BridgeGetSessionProjectionWindowV2Result`

```json
{
  "window": { "direction": "…", "cursor": "…", "hasOlder": true, "hasNewer": false },
  "turns": [ /* TurnSummaryDescriptor（§3） */ ],
  "scalarState": { "execution": {…}, "planMode": null, … },  // W1 语义镜像（v2 原生携带，不是 v1 补丁）
  "syncRev": 5601, "resume": null,
  "itemsView": "summary",
  "summaryBudget": { "payloadBytes": 812345, "foldedTurnIds": ["…"], "exceptions": ["turn-live-1"] }
}
```

- `scalarState` 必选（v2 从形状上消除 F-11——不依赖 v1 re-freeze）。
- `summaryBudget` 可观测账本：本页摘要净载荷、被折叠 turn、走例外通道的 turn。

## 3. TurnSummaryDescriptor（冻结定义 1+2）

每 turn 首屏交付物（③ 语义的 wire 化）：

| 字段 | 语义 |
|---|---|
| `turnId` / `status` / `startedAt` / `completedAt` / `durationMs` | 身份与时间 |
| `userBrief` | 用户提问正文；**≤128 chars 时完整**（直接显示），超长截断至 128 chars + `truncated: true` |
| `agentBrief` | 回答正文末段（结论优先）；同上 128 chars 规则 |
| `agentBriefHead` | 回答正文开头 64 chars（③「正文优先」的双端锚定：开头+结尾，中段折叠） |
| `folded` | `true` = 该 turn 有未随页交付的完整内容（正文超阈值或含工具过程） |
| `foldReason` | `long_body | tool_process | both`（iOS 渲染折叠形态依据） |
| `toolProcessCount` / `toolProcessBytes` | 工具过程占位摘要（「本条含 3 段工具过程 · 47KB」的事实数据） |
| `liveState` | **例外通道（定义 2）**：正在执行的轮次完整 turns 原样内嵌（never-split，不受 1MiB 预算约束）；权限卡/待答问题作为 turn 末 part 原样携带并置 `interactive: true` |
| `turnGeneration` | 与 turn_detail 家族身份绑定（定义 3） |

**预算语义（定案）**：≤1MiB 是 `turns[].(userBrief|agentBrief|agentBriefHead|工具占位)`
合计的软预算（超限 → 老 turns 降级为「仅 turnId + folded，无 brief」整 turn 折叠，不截断
已交付 brief）；`liveState` 例外与 `scalarState` **不计入预算**。杜绝「为过指标截断权限卡」。

## 4. 明细按需加载（定义 6：与 turn_detail 家族关系）

- 展开折叠 turn / 工具过程 → iOS 经**既有** `session_turn_items`（v1）/ `turn_detail_chunk`
  （v2 overlay）拉取完整 parts；**新增 Claude producer**（现 handler 拒非 codex-remote，
  `session_turn_items_handler.go:71`——扩展为 Claude 支持是本 slice 的 Mac 侧工作主体）。
- chunks 家族语义（replay/seq/generation）原样复用；不平行新建第二套明细协议。
- 明细一旦经 chunks 交付并入本地投影，后续 live patch / anchorRefresh 以**增量 partOps**
  更新（不回退为完整明细重发——定义 4 的一致性约束）。

## 5. live patch / 缓存 / 对账一致性（定义 4）

- summary 视图下的 live patch：`partOps` 照常应用；若目标 turn 处于 `folded` 状态，patch
  只更新 `folded` 元数据（工具字节数等）不展开内容；用户已展开的 turn（本地已有明细）
  按常规完整 patch。
- 首开 summary 成功 → 基底缓存播种 **summary 形态**（缓存 schema v3：rows 带 brief +
  folded 元数据）；重开 anchor 对账的 `anchorRefresh` / `changedTurns` 对 summary 行返回
  summary 形态终态（**不携带完整明细**——防「下次更新退回全量」）。
- 用户已下载的明细随基底缓存持久化（受 4MiB 预算，从头部淘汰）。

## 6. typed error（定义 5）

沿用 window 家族 + anchor 家族错误纪律：`cursor_stale`（重试=true）、
`invalid_params`、`projection_window.limit_exceeded`、`protocol.capability_required`、
`projection.hydrating`（重试=true）；新增 `summary.oversized`（单 turn summary 例外通道
自身超 projection_max_bytes → fail-closed，不静默截断）。未协商 v2 的旧端组合：客户端
双维度 gate（v2 echo + itemsView 支持）fail-closed 回 full 首开（现状路径），不假启用。

## 7. 交付物清单（冻结批次）

1. `docs/protocol/bridge-v1.md` §Projection Window v2 章节（含 F-18 方向 b 修订）；
2. `docs/protocol/schema/bridge-v1.types.ts` 新类型；
3. 冻结样本 `docs/protocol/samples/projection-window-v2/window-summary-spec.json`
   （两仓字节一致副本 + README 哈希）；
4. Mac spec 测试（冻结常量/断言）+ Claude producer 定向测试；
5. iOS spec 测试（镜像）+ summary 消费/折叠/例外/展开-chunks 链测试；
6. `schemaRevision` bump + 注释块条目；
7. 验收：首开 summary 实测（样本 session 7f389d08：summary 载荷 ≤1MiB、首屏时长、
   展开 turn 明细到达）；v2 未协商 → full 回归。

## 8. 非目标（本轮不做）

- older 方向网络加载（OD-8 维持 (a) 关闭；v2 的 older 只作为游标形状预留）。
- 非 Claude backend 的 v2 producer（只留协议推广空间）。
- 离线查看未下载明细（owner 已接受）。

## 9. 风险与回退

- 回退 = Mac 不 echo `projection_window_v2` → iOS 自动回 full 首开（现状），无数据迁移。
- 最大实施风险：Claude turn_detail producer 的身份绑定（turn generation ↔ JSONL 行）；
  复用 codex-remote 已验框架，测试先行（§7.4）。
- summary/明细一致性回归由 §5 三条规则 + 定向测试锁定。
