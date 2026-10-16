# 方案评审报告 · 第 2 轮（fresh）

- verdict: **REVISION_REQUIRED**
- 方案：`docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md`（v2）
- 评审时内容 SHA-256：`88266be750986070df0a1c66bd1f70b02b564db328a43a232e473ff06c21d448`（与派发记录一致，身份已确认）
- blockers: **1**（F-7）；advisories: **4**（F-8..F-11）
- 评审范围：full（全文档）；评审员模式：fresh（本轮先独立形成风险清单并独立亲核，后读 r1 作回归清单）
- 契约版本：plan-contract-v1.1（plan-review SKILL 评审程序 + audit-plan 接入约定已执行——方案含外部 JSONL/SDK 形状声明，专项适用）
- implementation_readiness：方案层面需先修订 D-1 规则 3 的官方语义引用（F-7）后重审；E-8（官方 oracle 对账）为实施期完成门，属合法 pending。本评审不构成实施授权。

## 1. 结论摘要

v2 对 r1 F-1..F-6 的处置全部落实到文本（F-1/F-3/F-5/F-6 闭合，F-2 的「跨行找非空」问题闭合），方案的两大修复方向（programmatic 分类移植、synthetic API error 收口）本轮以**独立证据链**重新证实：

1. **官方语义**：本轮从 npm registry 下载 `@anthropic-ai/claude-agent-sdk@0.3.301` tarball（SHA-256 `1cf0f6a6…`）逐字核对了 `sdk.mjs` 的 `YD`/`nl`/`jt`/`dn`/`bi` 与 `sdk.d.ts` 的 `includeProgrammatic` JSDoc——分类集合 `{sdk-cli,sdk-ts,sdk-py}`×`{daemon,daemon-worker}`、默认 true、IDE picker 传 false、sessionStore 忽略、64KB head+tail 窗口全部属实（§4.1）。
2. **行为实测复跑**（r1 §4.3 曾声明未复跑，本轮补上）：用官方 SDK 0.3.301 对本机 iOS 项目目录实跑 `listSessions` 三组参数，`includeProgrammatic:false` 下 19 条 sdk-py reviewer 全部消失、主 session `7afc3bd7` 保留，与 §2.3 一致；另见 F-8/F-9 的新证据。
3. **本机样本**：§2.5 全部断言（19 条 reviewer＝11×401＋8 成功、5 条 2026-10-16 下午、entrypoint 一律第 3 行、promptSource=sdk、isSidechain=false、无 subagents 目录、sessionKind 39 文件零出现、主 session 5 行全 429）逐项吻合（§4.2）。synthetic 401 行为 top-level `isApiErrorMessage/apiErrorStatus/error/requestId` + `message.stop_reason:"stop_sequence"` + content 内人读错误文本——D-4 的字段路径与 errorMessage 优先级与样本一致，`stop_sequence ∈ isFinalClaudeStopReason`（handlers_relay.go:1821）坐实「今日必然映射 turn_completed」根因。
4. **结构与消费方**：mapper `claudeEntryToProjectionEvents` 的全部生产调用点（cold hydrate :2167/:2194、source batch claude_source_mapper.go:43、legacy row :1543、Web Push preview claude_web_push_watcher.go:237）共用一处，D-4「一次修改服务三条路径」结构成立；Web Push 意图门三处（producer :130/:195-196/:205-206/:215）只认 `turn_completed`/`permission_request`；`WebPushKindError` 门 `Passed:false`（web_push_producer.go:40）；按-ID detail 走 `findClaudeSessionFile`（handlers.go:3935）独立文件查找不经 snapshot；iOS `CCCodeBridgeBackendClient.swift:1825` 已解码 `turn_error`。D-2 的 ByKey/Sorted 拆分、过滤先于 lineage、archived 契约回归均已写死且与源码一致。

唯一阻塞是 v2 修订**新引入的一处官方语义误引**：D-1 规则 3 把官方 `?? e` 分支解读成「读不到 parentUuid 行即判非 daemon」，而官方实现是回退到**整个 head 文本**搜 sessionKind（F-7）。其余 4 条建议为 E-8 测量口径补强与行为面盘点补全。

## 2. 来源身份（本轮亲核）

| 来源 | 身份 | 核验方式 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `6ef1a10dacb258ba389162a5c87262e253d01f1b`，仅两份方案文档 untracked | `git rev-parse/branch/status` 亲核，与简报一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `02a46521678950fd1df47ac951505f3dccba0d4e`，干净 | 同上 |
| TS SDK | npm `@anthropic-ai/claude-agent-sdk@0.3.301` tarball 落盘 `/tmp` 提取（tgz SHA-256 `1cf0f6a61294cb7edd3ef2682828139ba999f7dd263e46d75d4135ddd3a55b05`）；`sdk.mjs`/`sdk.d.ts` 逐字核对；**并用该包实跑 listSessions 行为实测** | 本轮亲核（下载+运行） |
| Python SDK | PyPI `claude-agent-sdk==0.2.168` wheel 下载解包；AST 提取 `_internal/sessions.py` `list_sessions` 签名 = `(directory, limit, offset, include_worktrees)`，全包 grep 无 programmatic 参数 | 本轮亲核 |
| 本机 transcript 样本 | `~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/`（39 个 top-level jsonl） | 本轮亲核（双策略 dump，见 §4.2） |

## 3. 锚点核验表（本轮全部亲核）

| 命题/方案原引用 | 实际位置与原文摘录 | 定位/语义 |
| --- | --- | --- |
| catalog 扫描 top-level JSONL（`:5`、`:276-347`） | claude_session_catalog.go:5-9「只能从 `~/.claude/projects/` 下的 JSONL transcript 文件派生」；:307 `if file.IsDir() || !strings.HasSuffix(name, ".jsonl") { continue }` | 定位准 / 语义支持 |
| list 直接输出 Sorted（`:209-221`） | :214-220 `result := make(..., len(snapshot.Sorted)); for _, entry := range snapshot.Sorted { ... claudeSessionEntryToWire(entry) }` | 定位准 / 语义支持 |
| metadata scan 未保留 entrypoint/sessionKind（handlers.go:4282） | :4282 `func scanClaudeSessionMetadata`；:4297-4298 `if info.Size() > claudeSessionSummaryReadLimit { reader = io.LimitReader(f, claudeSessionSummaryReadLimit) }`；:59 `claudeSessionSummaryReadLimit = 512 * 1024`；全函数无 entrypoint/sessionKind | 定位准 / 语义支持 |
| watcher 遍历 Sorted（claude_web_push_watcher.go:93-137） | :93 `refresh`，:97-98 `seen := make(..., len(snapshot.Sorted)); for _, entry := range snapshot.Sorted`；:19 `claudeWebPushWatchInterval = 3 * time.Second` | 定位准 / 语义支持 |
| mapper final stop → turn_completed（handlers_relay.go:2206-2451） | :2444-2449 `if isFinalClaudeStopReason(e.Message.StopReason) { out = append(out, projectionHydrateEvent{Event: "turn_completed", ..., TurnDone: true}) }`；:1821 谓词含 `"stop_sequence"`；`claudeTranscriptRelayEntry`（:1638-1662）无 isApiErrorMessage 等四字段 | 定位准 / 语义支持 |
| reducer 已支持 turn_error（projection_reducer.go:2481-2521） | :2481 `case "turn_aborted", "turn_error":`；:2500-2502 `status = "error"`；:2509-2520 `terminal.System = &MessageProjection{... errorMessage ...}`（读 `dataString(data,"message")`） | 定位准 / 语义支持。注意 D-4 用 `"message"` 键——正确；同仓另一 emitter（handlers_projection.go:1712）用 `"error"` 键与 reducer 不匹配，属既有旁支问题，不影响本方案 |
| control plane 只认 turn_completed（D-5 现状） | handlers_relay.go:1615-1635 `hasTurnCompleted → broadcastIdleState + *runningObserved=false`，`default: *runningObserved = true`；legacy :1571-1573 `default: sendSessionEvent(...); *runningObserved = true`；held 只收集 turn_completed（:1417-1426、:1556-1563） | 定位准 / 语义支持 |
| Web Push completion gate 只认 turn_completed | web_push_producer.go:130 `case "turn_completed":`、:195-196 `default: return nil`；:205-206 passive `if eventName != "turn_completed" { return nil }`；:215 `enqueueReplayFreeLivePush ... eventName != "turn_completed" → false`；:40 `WebPushKindError: {GateID: "EVT-ERROR-1", Passed: false}` | 定位准 / 语义支持 |
| mapper 三条路径共用 | claude_source_mapper.go:43、handlers_relay.go:1543/:2167/:2194、claude_web_push_watcher.go:237（grep 全部生产调用点） | 定位准 / 语义支持 |
| detail 按 ID 独立查找（R-2） | handlers.go:3935 `func (h *Handlers) findClaudeSessionFile(sessionID string, optDir string)` 独立遍历 `~/.claude/projects`，不经 snapshot | 定位准 / 语义支持 |
| archived 由客户端隐藏 | claude_session_catalog.go:601-606 wire 附 `archivedAtMillis`；session_discovery.go:483-497 指纹输入过滤 archived（注释明示客户端可见性契约） | 定位准 / 语义支持 |
| buildSnapshot 全量进 Sorted / lineage delete(ByKey) | :401-404 `sortedEntries` 取自全部 `nextByKey`；:487 `delete(byKey, sorted[index].Key)`（compact）；:557 `delete(byKey, sorted[idx].Key)`（fork） | 定位准 / 语义支持（D-2「过滤先于 lineage 则 delete 不可触及 programmatic」推理成立） |
| iOS 已解码 turn_error | iOS 主工作树 `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift:1825` `case "turn_error":`（settle failed + idle） | 定位准 / 语义支持 |
| turn_error+TurnDone 事件形状有同族先例 | handlers_projection.go:1712 既有 `projectionHydrateEvent{Event:"turn_error", ..., TurnDone:true}` emitter，经同一 kernel/hydrate 机制 | D-4 形状可行 |
| E-5 消费方枚举 | Sorted/ByKey 非测试消费：catalog `list()`（handlers.go:4079、catalog_recent_view.go:205/:265、session_discovery.go:482）+ watcher :98；**另见 F-10：pin 解析 handlers_session_pin.go:225-235 也经 `list()`** | 语义支持，消费方清单本身见 F-10 |
| E-4 Python 无过滤参数 | wheel AST：`args: ['directory','limit','offset','include_worktrees']`，无 include_programmatic | 定位准 / 语义支持 |

## 4. 官方 SDK 语义与真实样本核验（audit-plan 专项）

### 4.1 官方实现（npm 0.3.301 tarball，逐字摘录）

```js
var dn=65536,SCe=new Set(["sdk-cli","sdk-ts","sdk-py"]);
function YD(e,t){let n=nl(e,"entrypoint")??jt(t,"entrypoint");
  if(n&&SCe.has(n))return!0;
  let r=e.split(`\n`).find((s)=>s.includes('"parentUuid":'))??e,o=nl(r,"sessionKind");
  return o==="daemon"||o==="daemon-worker"}
```

- `nl(e,t)`：在整个字符串 e 中找 `"key":"` / `"key": "` 的**首次**出现（原生子串匹配，非按行 JSON 解析）；`jt`：tail 中**最后**一次出现。`""` 非-nullish：head 首次命中值为空串时**不**回退 tail。
- `bi`（读文件）：`Buffer.allocUnsafe(dn)` → head=文件头 64KB；`a=Math.max(0,r.size-dn)` → tail=文件尾 64KB（文件 ≤64KB 时 tail=head）。
- `sdk.d.ts` JSDoc：`includeProgrammatic`「Include programmatic/headless sessions (SDK entrypoints `sdk-cli`, `sdk-ts`, `sdk-py`) and daemon/daemon-worker sessions. Defaults to `true`… IDE session pickers pass `false` for parity with terminal `/resume`. Only applies when reading from the local filesystem; ignored when `sessionStore` is provided.」
- `D0`：`if(!t&&YD(r.head,r.tail))return null`（t=includeProgrammatic）。

**结论**：E-1/E-2 成立；§2.2 的 API 形状与默认值逐项吻合；D-1 的窗口差异声明（512KB head vs 64KB head+tail）与实际相符。**但规则 3 对 `?? e` 分支的解读与官方相反，见 F-7。**

### 4.2 本机样本（双策略交叉）

- **策略 A**（`message.isApiErrorMessage` 嵌套路径）：0 命中；**策略 B**（top-level `isApiErrorMessage`）：命中。两策略分歧定位出字段实际在**行顶层**——D-4 的四个 `json:"..."` 顶层字段路径正确，且 mapper 现行 `claudeTranscriptRelayEntry` 无这些字段（E-6 机制成立）。
- 顶层分布：401×11、429×51、500×8、402×1、None×3，全部 `type:"assistant"`、`message.stop_reason:"stop_sequence"`、`message.model:"<synthetic>"`、content 内含人读错误文本（如 `Failed to authenticate. API Error: 401 Invalid bearer token`——与截图通知文案一致）。
- 39 文件：`sdk-py`×19（=11×[401]+8×成功，成功者首条 assistant 文本均为 "I'll read…"；2026-10-16 的 401 恰 5 条：05:17/05:58/06:15/06:22/06:22Z）、`claude-desktop-3p`×15、`sdk-cli`×3、`claude-desktop`×2；`sessionKind` 零出现；reviewer 无 `<id>/subagents/` 目录；entrypoint 均在文件**第 3 行**；主 session `7afc3bd7` 恰 5 行且全为 429。§2.5 逐项吻合；**同一项目内另有 3 条 `sdk-cli` programmatic session（vision-agent 识图委托）为 §2.5 未提及的隐藏增量，见 F-9**。
- 双策略二次核验（`isApiErrorMessage:true` 计数）：per-file 结果与策略 B 全量一致（11 文件各 1 行、`7afc3bd7` 5 行等）。

### 4.3 官方 SDK 行为实测（本轮补跑，r1 §4.3 的未核项）

对 `dir=/Users/jacklee/Projects/cordcode-ios-native-message-timeline` 实跑 0.3.301：

| 参数 | 返回 |
| --- | --- |
| `{limit:100, includeProgrammatic:true}`（=§2.3 原问法） | 100（**被 limit 截断**，全集 139） |
| `{limit:500, includeWorktrees:true, includeProgrammatic:true/false}` | 139 / 109；hidden 差集 **30** |
| `{limit:500, includeWorktrees:false, includeProgrammatic:false}` | **17** = 39 − 22（19 sdk-py + 3 sdk-cli）——与 Go catalog 宇宙（top-level、worktree 目录被 `claudeWorkspaceVisibleForCatalog` 隐藏）**精确同构** |

- `includeProgrammatic:false` 下 `7afc3bd7` 保留、19 条 reviewer 全部消失 ✓（E-3 复现）；3 条 sdk-cli 也在隐藏集内（全集下确认）。
- 30 − 22 = 8 条额外隐藏来自**兄弟工作树项目目录** `-Users-jacklee-Projects-cordcode-ios`（`includeWorktrees:true` 把同仓 worktree session 并入本 dir 清单）——Go catalog 从不枚举它们 → E-8 若按默认 `includeWorktrees:true` 对账，机械断言 `GoHidden == TSHidden`（22 ≠ 30）必假，见 F-8。

## 5. 意见

### F-7 [阻塞] D-1 规则 3 误引官方 `?? e` 分支：head 无 parentUuid 行时应回退整个 head 搜 sessionKind，而非「判非 daemon」

- 位置：§3.1 D-1 解析规则第 3 条：「`sessionKind`：仅在 head 内**首个**包含 `"parentUuid":` 子串的行…中读取 `sessionKind`；**该行读不到即判非 daemon，不跨行继续找非空值**（对齐官方 `e.split('\n').find(s => s.includes('"parentUuid":')) ?? e` 的单行读取）」。
- 证据：官方 `YD`（§4.1 摘录）：`let r = e.split('\n').find(...) ?? e, o = nl(r,"sessionKind")`——`?? e` 中的 `e` 是**整个 head 字符串**（`YD(head, tail)` 首参）。即 head 内不存在任何含 `"parentUuid":` 的行时，官方在**整个 head 文本**内找 `sessionKind` 首次出现；plan 文本把该分支写成「读不到即判非 daemon」并引用同一段代码作依据，与官方实现相反。
- 影响：head 含 `"sessionKind":"daemon"` 但无任何 `"parentUuid":` 行的 transcript（如零消息行的 daemon 存根）官方隐藏、CordCode 显示——与 R-1「对齐官方 includeProgrammatic:false 语义」相悖。本机 daemon 零样本（§2.5），E-8 测不到该分支；S-1 单测 fixture 若按现文本编写，会把非官方语义固化为「对齐官方」的回归基线，恰是 oracle+fixture 机制要防的情形。D-1 开头「逐字对齐官方 sdk.mjs 实现」的核心声明在规则 3 上不成立。
- 修订方向：规则 3 改为「在 head 内首个含 `"parentUuid":` 子串的行读取 sessionKind；若 head 内不存在这样的行，则在整个 head 文本内读取 sessionKind 首次出现（对齐官方 `?? e` 整体回退）」，并同步 S-1 fixture（补「无 parentUuid 行 + head 含 sessionKind」用例）。
- 闭合标准：v3 规则 3 与官方 `YD` 的三分支（命中行 / 无行回退整 head / 读不到）逐字对应，不再存在未声明的语义差。

### F-8 [建议] E-8 需钉死 `includeWorktrees:false` 与全量枚举，否则机械断言在本机必假

- 位置：§5 E-8 第 1 步「官方 TS SDK 对同一 `dir` 分别执行 `includeProgrammatic:true/false`」。
- 证据：本轮实测（§4.3）：默认 `includeWorktrees:true` 下 TSHidden=30，其中 8 条来自兄弟工作树项目目录，Go catalog 从不枚举；GoHidden（本机）=22 → `GoHidden == TSHidden` 必假。且 §2.3 原问法 `limit:100` 在全集 139 下截断（limit:100 时 sdk-cli×3 因排序在 100 名外根本不进差集）。
- 影响：S-6 对账要么假失败（触发「记录差异并停止交付」的误报），要么依赖 step 3 的「逐项记录并解释」手工豁免 8 条——口径失去机械性（F-4 的闭合标准打折）。
- 修订方向：E-8 第 1 步写明 `includeWorktrees:false` + 不截断枚举（limit ≥ 全集或 offset 分页取全），并记录两组数（含/不含 worktree）备查。
- 闭合标准：按 v3 口径复算，本机 `GoHidden == TSHidden == 22` 无需逐项豁免即成立。

### F-9 [建议] §2.5 行为面盘点缺 3 条 sdk-cli programmatic session（同项目、当前可见、修复后将新增隐藏）

- 位置：§2.5「当前项目共有 19 条 security-guidance reviewer transcript」。
- 证据：本项目 39 文件中另有 3 条 `entrypoint:"sdk-cli"`（`7310d850`/`a84cd7e8`/`f450f935`，ZCode vision-agent 识图委托 session，首条 user 为「请直接用 Read 工具读取图片文件…」）；它们当前在 catalog 可见，D-1/D-2 生效后将从列表消失（官方同样隐藏，实测在 §4.3 全集 hidden 内）。
- 影响：属预期语义（sdk-cli ∈ 官方隐藏集）而非缺陷，但 §2.5/§9.1 的「变化清单」没把它计入——owner 部署后核对列表会看到比方案所述多 3 条消失；E-8 差集解释时也会遇到。
- 修订方向：§2.5 补一行「同项目另含 3 条 `sdk-cli` programmatic session（识图委托），修复后一并隐藏（官方语义内）」。
- 闭合标准：§2.5 census 覆盖全部 22 条将被隐藏的 programmatic session。

### F-10 [建议] D-2 消费方清单漏 pin 解析：pinned programmatic session 将被判定「definitively gone」

- 位置：§3.2 D-2 理由「`list_sessions`、session discovery、recent view 和 Web Push watcher 都消费 `Sorted`」。
- 证据：`resolveClaudePin`（handlers_session_pin.go:225-235）经 `h.claudeSessions.list()` 查找 pinned session，找不到即返回「definitively gone」。programmatic session 退出 Sorted 后，先前误 pin 的 reviewer session 的 pin 将按「会话已不存在」处理。
- 影响：机制上与「隐藏=从默认面消失」一致（文件仍在、按 ID detail 仍可读），但它是清单外的第 5 个消费面，pin 被判 gone 是一个未声明的用户可见后果。
- 修订方向：D-2 消费方清单补 pin 解析，并一句话声明「pinned programmatic session 的 pin 按 not-found 处理（文件与按-ID detail 不受影响）」。
- 闭合标准：v3 消费方清单含 pin 解析及其后果声明。

### F-11 [建议] D-1 提取层与官方 `nl` 的未声明微差（子串匹配 vs 逐行顶层解析）

- 位置：§3.1 D-1 规则 1-2「逐行读取 top-level JSON 字段」。
- 证据：官方 `nl` 是**原生子串**首次匹配——可命中嵌套 JSON 键（如 tool_use input 内的 `"entrypoint":"sdk-py"`，作为 JSON 结构而非转义字符串出现）；Go 按行顶层解析只命中顶层键。两者仅当「嵌套 entrypoint/sessionKind 键出现在顶层字段之前」时分叉（真实样本顶层 entrypoint 在第 3 行，实际不可达）。另：官方 head 首次命中为空串时不回退 tail（`""` 非-nullish），与窗口差异声明部分重叠但属不同分支。
- 影响：极构造性；但 D-1 声明「逐字对齐」且只声明了一处窗口差异，这两处提取层差异未列入「有意差异」清单，oracle 对账若遇构造样本会产出未解释分叉。
- 修订方向：在 §3.1「有意差异」处补两句：Go 按行顶层解析（不匹配嵌套键）；head 首次命中空串即停（不回退、不续扫）。
- 闭合标准：差异清单覆盖提取层，或 fixture 注释声明该差异。

## 6. r1 意见处置（回归清单）

| r1 意见 | 严重性 | v2 处置 | 本轮判定 |
| --- | --- | --- | --- |
| F-1 「Sorted 只输出非 archived」矛盾 | 阻塞 | §3.2 改「相对现状仅新增 Programmatic==false 过滤」+ §7 archived 回归行 + S-2 断言 | **闭合**（与 claude_session_catalog.go:401-404/:601-606、session_discovery.go:483-497 现状一致） |
| F-2 sessionKind「首个非空」跨行偏差 | 建议 | 改为单行读取 | 「跨行找非空」**闭合**；但 v2 新引用的 `?? e` 分支解读反向 → 升格为新阻塞 **F-7**（旧问题漏检 + v2 措辞新引入误引） |
| F-3 repo 无实现应锚 npm | 建议 | §2.1 注明 npm 包锚定、repo 仅旁证、publish 时间未核 | **闭合**（本轮 npm tarball 独立复证） |
| F-4 E-8 测量口径 | 建议 | 集合运算口径 + 不用 ByKey−Sorted + daemon 由 fixture 覆盖 | **闭合**；F-8 为口径补强（worktree/limit 实测新证据） |
| F-5 过滤与 lineage 顺序 | 建议 | §3.2 定死先于 lineage + S-2/§7 fixture | **闭合**（:487/:557 delete 不可触及 programmatic 的推理经源码核实） |
| F-6 部署运行态五条 | 建议 | §6.2 lstart + 独有日志行 + §9.6 | **闭合**（与 AGENTS.md 部署后验证逐条对应） |

## 7. 覆盖说明与未核项

- 已核（本轮亲核）：方案引用的全部 Go/iOS 锚点（§3 表）；npm 包实现/类型/行为三重（tarball 逐字 + listSessions 实跑）；本机 39 transcript 双策略 dump；Python SDK 签名（wheel AST）；Web Push 意图门与 Error 门；Sorted/ByKey 全部消费方（含 r1 未展开的 pin 解析，见 F-10）；detail 按-ID 独立路径；ByKey 缓存与 cacheHit 快路径（:392-399）；`subagentsAggregateDigest` 只遍历 Sorted（:197）；四拍交互（本修复无新输入面，用户可见面——列表隐藏、无 completion push、打开 session 的 turn_error 诊断+idle——在 §1.1/§3.4/§7/§9 写清，判定满足仓库规则对此类修复的要求）；D3 预算/部署纪律与 AGENTS.md 一致；§6.1 正则与现有测试名可匹配（`TestClaudeSessionCatalog*` 12 个、watcher/projection 测试文件存在；方案已声明以实际名为准）。
- 未核：npm 精确 publish 时间戳（与 r1 同，不影响任何命题）；`sessionKind=daemon` 活体样本（本机不存在，官方源码+fixture 路径成立）；iPhone 截图本体（5 条 10-16 下午 401 transcript 与描述吻合）。
- 门控一致性：OD-1..OD-5 全部 decided 且与正文一致；E-1..E-7 本轮独立复证 verified；E-8 pending 为 S-6 完成门（合法 pending，不阻塞方案层结论）；S-1..S-7 依赖序无悬空引用、无既允许又禁止项。

## 8. 剩余门与处置

- open gates：**E-8**（官方 oracle 对账，实施期完成门；另受 F-8 口径补强约束）。
- 下一轮：v3 按 F-7（必须）与 F-8..F-11（建议，可逐条标注不采纳理由）修订 §3.1/§5/§2.5/§3.2，第 10 节追加修订记录、开头声明本轮改动范围。

---

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md
plan_sha256: 88266be750986070df0a1c66bd1f70b02b564db328a43a232e473ff06c21d448
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan-review-r2.md
scope: full
blockers: 1
advisories: 4
open_gates: [E-8]
round_complete: true
contract_version: plan-contract-v1.1
```
