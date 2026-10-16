# 方案评审报告 · 第 1 轮（fresh）

- verdict: **REVISION_REQUIRED**
- 方案：`docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md`（v1）
- 评审时内容 SHA-256：`8a2543c1924b23369a77ce1c8ac0923da5e1a379d5e77fd95dd1583e7e9afcb6`（与派发记录一致，身份已确认）
- blockers: **1**（F-1）；advisories: **5**（F-2..F-6）
- 评审范围：full（全文档）；评审员模式：fresh（首轮，无历轮报告）
- 契约版本：plan-contract-v1.1（plan-review SKILL 评审程序 + audit-plan 接入约定已执行——本方案含外部 JSONL/SDK 形状声明，专项适用）
- implementation_readiness：方案层面需先修订 D-2 措辞（F-1）后重审；E-8（官方 oracle 对账）为实施期完成门，属合法 pending。本评审不构成实施授权（owner 入口仅要求方案）。

## 1. 结论摘要

方案的两大修复方向均有硬证据支撑，且本轮**全部亲核通过**：

1. **官方语义移植（D-1/D-2/D-3）**：`@anthropic-ai/claude-agent-sdk@0.3.301` npm 包的 `sdk.mjs` 实现与 `sdk.d.ts` 类型说明逐字证实了方案声称的分类集合与默认值（详见 §3.2）。本机 19 条 `sdk-py` reviewer transcript、11 条 401、主 session `7afc3bd7`（`claude-desktop`/`claude-vscode`）与方案 §2.5 的样本断言逐项吻合。R-4（不误伤真实 session）在「只认结构化字段 + 未知值默认可见」规则下成立，且与官方实现一致。
2. **synthetic API error 收口（D-4/D-5）**：真实 401 行（`stop_reason:"stop_sequence"`、`isApiErrorMessage:true`、`apiErrorStatus:401`、`error:"authentication_failed"`、`requestId`、text block 内错误文案）与 `isFinalClaudeStopReason` 含 `stop_sequence`（handlers_relay.go:1819-1826）共同坐实「今日必然映射 turn_completed」的根因；三条消费路径（source batch / file relay / Web Push preview）确实共用同一 mapper；Web Push 意图门只认 `turn_completed`/`permission_request`；`WebPushKindError` 门 `Passed:false`。D-5 指出的两处控制面缺陷（`deliverClaudeLiveControlPlane` 只认 `turn_completed` 为 idle；`deliverClaudeLegacyRow` default 分支保持 running）源码属实。

唯一的阻塞问题在 D-2 的一句措辞：**「Sorted 只输出非 archived…」与现行 archived 契约矛盾**（详见 F-1）。其余为对齐精度与验收口径的建议。

## 2. 来源身份（本轮亲核）

| 来源 | 身份 | 核验方式 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `6ef1a10dacb258ba389162a5c87262e253d01f1b`，仅方案文档 untracked | `git rev-parse/branch/status` 亲核，与简报一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `02a46521678950fd1df47ac951505f3dccba0d4e`，干净 | 同上 |
| TS SDK | npm `@anthropic-ai/claude-agent-sdk@0.3.301`（registry 确认存在；`sdk.d.ts` 经 unpkg 核对；`sdk.mjs` 经 `curl registry tarball | tar -xzO` 流式提取核对，未落盘）。公开 repo `anthropics/claude-agent-sdk-typescript` main HEAD = `b0fc766997199a49f90c37f5ec056936c80c9491`（2026-10-15T19:21:55Z，gh api 亲核）——**该 repo 在此 commit 无任何实现/类型源码**，见 F-3 | 本轮亲核 |
| Python SDK | PyPI `claude-agent-sdk` 0.2.168 存在（2026-10-15T20:20:48，latest）；repo main `src/claude_agent_sdk/_internal/sessions.py` 的 `list_sessions(directory, limit, offset, include_worktrees)` 无 programmatic 参数。本机 security venv 为 0.2.137（无模块级 list_sessions） | 本轮亲核 |
| 本机 transcript 样本 | `~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/`（39 个 jsonl） | 本轮亲核（grep/结构字段提取） |

npm 精确 publish 时间 `2026-10-15T19:22:00Z` 本轮未核实到（per-version registry 端点无 `time` 字段；repo commit 时间 19:21:55Z 已核，与声称值相容）。

## 3. 锚点核验表（本轮全部亲核）

| 方案原引用 | 实际位置与原文摘录 | 定位/语义 |
| --- | --- | --- |
| `claude_session_catalog.go:5`、`:276-347` 扫描 `~/.claude/projects` top-level JSONL | 头注释 :5-9「只能从 `~/.claude/projects/` 下的 JSONL transcript 文件派生（本文件做的事）」；枚举循环 :277-347，:307 `if file.IsDir() || !strings.HasSuffix(name, ".jsonl") { continue }`（只取 top-level 文件，目录不进候选） | 定位准 / 语义支持 |
| `claude_session_catalog.go:209-221` 直接输出 `snapshot.Sorted` | :209-222 `list()` 遍历 `snapshot.Sorted` 产出 wire | 定位准 / 语义支持 |
| `handlers.go:4282` 有限读取开头并解析 title/model/time，未保留 entrypoint/sessionKind | :4282 `func scanClaudeSessionMetadata`；:4297-4300 `io.LimitReader(f, claudeSessionSummaryReadLimit)`（=512*1024，:59）；全函数无 entrypoint/sessionKind | 定位准 / 语义支持 |
| `claude_web_push_watcher.go:93-137` 遍历 `snapshot.Sorted` | :93 `refresh`，:97-142 遍历 `Sorted` 建 tail state；:19 `claudeWebPushWatchInterval = 3 * time.Second`（方案「3 秒 sweep」属实）；candidate 只来自 `turn_completed`（:259-274），`flushTerminal` 只发 `turn_completed`（:179-190） | 定位准 / 语义支持 |
| `handlers_relay.go:2206-2451` assistant final stop 一律映射 `turn_completed`，未解析 synthetic API error 字段 | mapper 主体 :2221-2452；:2444-2450 `isFinalClaudeStopReason(StopReason)` → `turn_completed{TurnDone:true}`；:1819-1826 该谓词含 `"stop_sequence"`；`claudeTranscriptRelayEntry`（:1638-1662）无 isApiErrorMessage/apiErrorStatus/error/requestId 字段 | 定位准 / 语义支持 |
| `projection_reducer.go:2481-2521` 已支持 `turn_error`，message 保留为 system diagnostic | :2481 `case "turn_aborted", "turn_error":`；:2500-2502 status="error"；:2509-2520 `terminal.System = &MessageProjection{… errorMessage …}` 注释明示「diagnostic truth, not an assistant reply」 | 定位准 / 语义支持 |
| iOS `CCCodeBridgeBackendClient.swift` 已支持 `turn_error` | 主工作树 `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift:1820-1830` `case "turn_error":`（settle failed + idle） | 定位准 / 语义支持 |
| （方案未引用，本轮补充）`deliverClaudeLiveControlPlane` / `deliverClaudeLegacyRow` | :1609-1636 只有 `turn_completed` → `broadcastIdleState`+`runningObserved=false`，default → `runningObserved=true`；:1571-1573 legacy default 分支保持 running | D-5 所述缺陷属实 |
| （补充）Web Push 意图门 | `web_push_producer.go:127-` `pushIntentForRelayTerminal` 仅 `turn_completed`（:130，`WebPushKindCompletion` 门）/`permission_request`（:175）产生 intent，default 返回 nil；:40 `WebPushKindError: {GateID:"EVT-ERROR-1", Passed:false}` | 「只对 turn_completed 开 completion gate」「Error 类别保持样本门关闭」属实 |
| （补充）mapper 三条消费路径 | source batch `claude_source_mapper.go:43`；file relay `handlers_relay.go:1543`（及 :2167/:2194 hydrate）；Web Push preview `claude_web_push_watcher.go:237` | D-4「一次修改服务三条路径」结构成立 |
| （补充）`Sorted` 全部消费方 | list（catalog :214-221）、discovery（`session_discovery.go:482`）、recent view（`catalog_recent_view.go:205/:265`）、watcher（:98）、pin 解析（`handlers_session_pin.go:229`）；`scanSessionsFromProjectDir*` 无生产调用方（仅 handlers_test.go:444） | E-5 属实，无遗漏消费方 |
| （补充）detail 按 ID 独立查找 | `handlers.go:3935-3967 findClaudeSessionFile` 独立遍历 `~/.claude/projects`，不经 snapshot；:3866-3870 注释明示「archived… excluded from the default list while remaining individually readable」的同款设计先例 | R-2 属实 |
| （补充）ByKey 缓存机制 | :355 fingerprint 命中 `previous.ByKey`；:392-399 `cacheHit` 快路径 | D-2「保留 ByKey 避免反复解析」机制属实 |

## 4. 官方 SDK 语义与真实样本核验（audit-plan 专项）

### 4.1 官方实现（npm 0.3.301 `sdk.mjs`，registry tarball 流式提取，逐字摘录）

```js
var SCe=new Set(["sdk-cli","sdk-ts","sdk-py"]);
function YD(e,t){let n=nl(e,"entrypoint")??jt(t,"entrypoint");
  if(n&&SCe.has(n))return!0;
  let r=e.split(`\n`).find((s)=>s.includes('"parentUuid":'))??e,o=nl(r,"sessionKind");
  return o==="daemon"||o==="daemon-worker"}
```

`listSessions`（压缩名 `L0`）：`{dir:o,limit:s,offset:i,includeWorktrees:a,includeProgrammatic:l}=e??{}, … d=l??!0 … a??!0`——`includeProgrammatic` 默认 true、`includeWorktrees` 默认 true。

`sdk.d.ts`（unpkg 核对）JSDoc：`includeProgrammatic` "Include programmatic/headless sessions (SDK entrypoints `sdk-cli`, `sdk-ts`, `sdk-py`) and daemon/daemon-worker sessions"、默认 true 供 SDK 消费者枚举自己的 session、"IDE session pickers pass `false` for parity with terminal `/resume`"、"Only applies when reading from the local filesystem; ignored when `sessionStore` is provided"。

**结论**：方案 §2.2 的 API 形状、默认值、分类集合与 D-1 的分类函数逐项吻合；E-1、E-2 成立。D-1 第 3 条「在包含 `"parentUuid":` 的行读取 sessionKind」有官方实现逐字支撑——但官方是取**首个**含 `"parentUuid":` 的行就读（含 null 行），方案写「首个非空 sessionKind」，存在细微偏差，见 F-2。

### 4.2 本机真实样本（`~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/`）

- 39 个 top-level jsonl；**19 个**含 `"entrypoint":"sdk-py"`（§2.5 的 19 条 ✓）；**11 个**含 `"apiErrorStatus":401`（11 条认证失败 ✓），其中 5 个文件首时间戳落在 2026-10-16T05/06Z（= 本地下午，对应截图 5 条 ✓）。
- 三条 §2.3 点名的 reviewer ID（`b19521e7`/`9c5c888d`/`1121093c`）文件均存在 ✓。
- reviewer 均 `promptSource:"sdk"`、`isSidechain:false`、无 customTitle、0/19 有 `<id>/subagents/` 目录 ✓。
- `entrypoint` 出现在文件第 3 行（512KB bounded head 内可达）✓。
- 主 session `7afc3bd7-c69b-4582-85cf-556fa4f54d69` 的 entrypoint 全部为 `claude-desktop`(1647 行)/`claude-vscode`(1365 行)——不在隐藏集合，保留 ✓（R-4 样本级成立）。
- 真实 synthetic 401 assistant 行（结构字段摘录）：`type:"assistant"`、`message.model:"<synthetic>"`、`message.stop_reason:"stop_sequence"`、`content:[{type:"text",text:"Failed to authenticate. API Error: 401 …"}]`、顶层 `requestId:"req_…"`、`error:"authentication_failed"`、`isApiErrorMessage:true`、`apiErrorStatus:401`。D-4 的四个结构体字段名与 errorMessage 优先级（text 优先、顶层 error 兜底）与样本一致，E-6 成立 ✓。
- **`sessionKind` 在本项目全部 transcript 中零出现**——daemon/daemon-worker 分类在本机不可由样本观测，只能由官方源码支撑（已支撑）；这也意味着 E-8 oracle 在本机测不到 sessionKind 分支（与 F-2 相关）。

### 4.3 行为断言的核验方式说明（诚实边界）

方案 §2.3 的「TS SDK true/false 两次实测」与「Python SDK 实测返回 reviewer」本轮**未复跑**（0.3.301 主包 tarball 曾于 2026-10-16 被本机下载过——npm cacache index 留痕——但内容已被 GC；本机 node_modules 仅 0.2.109）。本轮以更强的等价证据替代：官方实现代码（§4.1）+ 本机数据（§4.2）使两侧结果均可确定性推导。Python 侧 `sessions.py` 无任何 programmatic/entrypoint 逻辑 + 签名无过滤参数，行为断言同样可推导成立。精确 npm publish 时间戳未核实（§2）。

## 5. 意见

### F-1 [阻塞] D-2「Sorted 只输出非 archived…」与现行 archived 契约矛盾

- 位置：§3.2 D-2 第 2 条：「`Sorted` 只输出非 archived、经既有 fork/compact lineage 处理后、且 `Programmatic==false` 的 entry」。
- 证据：现行 `buildSnapshot`（claude_session_catalog.go:401-420）把**全部** nextByKey 条目（含 archived）放进 `Sorted`；`claudeSessionEntryToWire` :601-606 专门把 `archivedAtMillis` 放进 wire，「so clients can hide archived sessions (web session-grouping filters on archivedAtMillis)」；`session_discovery.go:483-497` 的指纹输入在消费侧过滤 archived（注释明示这是刻意的客户端可见性契约）；`handlers.go:3866-3870` 注释同样明示「archived sessions can be excluded from the default list while remaining individually readable」是既有设计。
- 影响：按字面实现会把 archived 条目从 `Sorted`/list wire 中移除——`archivedAtMillis` 不再到达客户端，web session-grouping、归档会话的列表可见性与 discovery 指纹输入都会被改变。这是一个方案未声明、未论证、验收矩阵（§7）也不覆盖的旁支行为变更，违反方案自己的非目标边界（「不删/移/改历史 transcript」的精神同样适用于列表契约）。
- 修订方向：D-2 措辞改为「`Sorted` 相对现状**仅新增** `Programmatic==false` 过滤；archived 条目维持现状（保留在 `Sorted`，经 `archivedAtMillis` 由客户端隐藏）」；并在 §7/切片验证中补一条锁定「archived 条目仍出现在 list wire（带 archivedAtMillis）」的回归断言。
- 闭合标准：v2 文本不再把「非 archived」表述为 `Sorted` 的输出条件（除非独立论证并验收该变更，那应另立决定）。

### F-2 [建议] sessionKind 提取语义与官方实现存在细微偏差

- 位置：§3.1 D-1 解析规则第 3 条「在包含 `"parentUuid":` 的行中读取**首个非空** `sessionKind`」。
- 证据：官方 `YD`（§4.1 摘录）取 `e.split('\n').find(s=>s.includes('"parentUuid":'))` 的**那一行**读取 sessionKind，读不到即判非 daemon——不做跨行「找非空」。本机样本 `sessionKind` 零出现（§4.2），E-8 无法区分两种语义；若未来 daemon session 的 sessionKind 不在首条含 `"parentUuid":` 的行（该行常为 `"parentUuid":null` 的首条 user 行），两实现的隐藏集合将分叉，且 oracle 测不出来。
- 修订方向：逐字对齐官方（只读首个含 `"parentUuid":` 的行），或在方案中声明有意差异及理由（哪种更稳健）并记入「与官方的有意保留差异」。
- 闭合标准：D-1 与官方语义一致或差异被显式声明。

### F-3 [建议] TS SDK 来源身份应注明公开 repo 无实现

- 位置：§2.1 来源表「TypeScript SDK … repo main commit `b0fc7669…`」。
- 证据：`gh api` 亲核该 repo 在此 commit 的顶层树仅有 `.claude/.github/CHANGELOG.md/CLAUDE.md/LICENSE.md/README.md/examples/scripts`——无 `src/`、无 `sdk.d.ts`、无任何实现；`includeProgrammatic` 在 repo 代码搜索 0 命中。实现与类型的唯一公开载体是 **npm 包内的 `sdk.mjs`/`sdk.d.ts`**。
- 影响：实施者按来源表去 repo 找官方实现会扑空；「repo main commit」作为实现锚点是无效引用（作为发布时间旁证有效）。
- 修订方向：来源表注明「实现/类型锚定 npm 包 `sdk.mjs`/`sdk.d.ts`（0.3.301），公开 repo 仅 docs/examples；npm 精确 publish 时间未核，repo commit 时间 2026-10-15T19:21:55Z」。
- 闭合标准：来源表不再暗示 repo 含实现。

### F-4 [建议] E-8 的「Go 默认隐藏集合」未定义测量口径

- 位置：§5 E-8 第 2 步「测试 CordCode Go catalog，记录默认隐藏集合」。
- 证据：`ByKey − Sorted` 会把 fork/compact lineage 隐藏（且 lineage 是 delete(ByKey)，:487/:557）与（若 F-1 按字面实现）archived 一并计入，与 TS true/false 差异集不可直接对比。
- 修订方向：明确口径为「同一 snapshot 中，`Sorted`(Programmatic 过滤开) 与 `Sorted`(该过滤关) 的差集」，fork/archive/desktop-delete 差异另列并逐项解释（方案已预留解释条款，口径需先定死）。
- 闭合标准：E-8 步骤写出可机械执行的集合运算。

### F-5 [建议] Programmatic 过滤与 fork/compact lineage 的先后顺序未写明

- 位置：§3.2 D-2 第 2 条（「经既有 fork/compact lineage 处理后、且 Programmatic==false」暗示 lineage 先行）。
- 证据：lineage 隐藏同时 `delete(byKey, …)`（:487/:557）——若 programmatic 条目先参与 lineage 再被过滤，边缘情形下（a）programmatic 条目被 lineage 隐藏会从 ByKey 删除，破坏 D-2 自己的缓存保留目标（下轮 sweep 重新解析）；（b）programmatic 分支理论上可成为 fork primary 而把真实分支从 ByKey/Sorted 删除（本机 reviewer 均无 customTitle，实际不可达，但规则上未封死）。
- 修订方向：写明「Programmatic 过滤在 fork/compact lineage **之前**应用于 Sorted 候选（lineage 分组只见非 programmatic 条目），ByKey 始终保留全部物理条目」，并加一条对应测试。
- 闭合标准：D-2 定死过滤顺序，且 lineage delete(ByKey) 不再能触及 programmatic 条目。

### F-6 [建议] 部署后运行态验证可补全五条对齐

- 位置：§6.2 / §9.6。
- 证据：仓库 AGENTS.md「部署后运行态验证」要求进程代际（`ps -o lstart` 晚于构建时刻）+ **本次提交独有的特征输出**。§6.2 有「确认 8777 由新 runtime PID 监听」与「日志/列表行为确认隐藏集合生效」，基本覆盖，但未写 lstart 对比与「特征输出」的具名项（如隐藏集合计数的日志行）。
- 修订方向：§6.2 补「`ps -o pid,ppid,lstart` 代际核对」与一条本次新增的独有日志/行为特征。
- 闭合标准：部署清单与 AGENTS.md 五条逐条对应。

## 6. 覆盖说明与未核项

- 已核：方案引用的全部 Go/iOS 锚点（§3 表）；官方 npm 包实现与类型（§4.1）；本机 transcript 样本断言（§4.2）；Python SDK 签名与版本；Web Push 意图门与 Error 类别门；`Sorted` 消费方枚举（grep 全仓非测试引用 + 逐个确认）；detail 按 ID 独立路径；fork/compact lineage 与 ByKey 缓存机制；D3 预算与部署纪律表述与 AGENTS.md 的一致性。
- 未核/不可核：npm 精确 publish 时间戳；§2.3 两次 SDK 实测的原始运行记录（作者未随方案归档；本轮以实现+数据等价证据替代，见 §4.3）；`sessionKind=daemon` 的活体样本（本机不存在，属官方源码支撑项）；「iPhone 截图」本身（评审未看图，但 5 条 10-16 下午 401 transcript 与截图描述吻合）。
- 四拍交互检查：本修复无新输入面（无「打开输入/发送」拍），用户可见面（列表、通知、打开 session 的终态展示）在 §1.1/§3.4/§7/§9 已按场景写清（列表隐藏、无 completion push、turn_error 收口为 system diagnostic + idle），判定为满足仓库「方案必须先写清交互流程」规则对本类修复的要求。
- 测试命令检查：§6.1 两条正则与现有测试名（`TestClaudeSessionCatalog*`、`TestClaudeWebPushWatcher*`、`TestClaudeEntryToProjectionEvents`）真实可匹配；`TestClaudeSessionFileRelay*` 目前不存在（方案已声明按实际测试名为准，非缺陷）。

## 7. 剩余门与处置

- open gates：**E-8**（官方 TS SDK oracle 对账，实施期完成门，S-6 的完成条件；按 plan-contract 属合法 pending，不阻塞方案层结论）。F-1 修订前，S-2/S-3 的实现前置不满足（D-2 规格错误）。
- 下一轮：修订版（v2）按 F-1（必须）与 F-2..F-6（建议，可逐条标注不采纳理由）更新 §3.1/§3.2/§5/§6.2/§2.1，并在第 10 节追加修订记录、开头声明本轮改动范围。

---

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md
plan_sha256: 8a2543c1924b23369a77ce1c8ac0923da5e1a379d5e77fd95dd1583e7e9afcb6
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan-review-r1.md
scope: full
blockers: 1
advisories: 5
open_gates: [E-8]
round_complete: true
contract_version: plan-contract-v1.1
```
