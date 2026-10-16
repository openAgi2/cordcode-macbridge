# 方案评审报告 · 第 4 轮（fresh）

- verdict: **APPROVED**
- 方案：`docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md`（v4）
- 评审时内容 SHA-256：`bb67fea8f3966dcd19842ac0f050695673c340c4f6ffd485dc94d1e08355642b`（与派发记录一致，身份已确认）
- blockers: **0**；advisories: **1**（F-13）
- 评审范围：full（全文档）；评审员模式：fresh（先独立形成风险清单并亲核，后核对 r1/r2/r3 作回归清单）
- 契约版本：plan-contract-v1.1（plan-review SKILL 评审程序 + audit-plan 接入约定已执行——方案含外部 JSONL/SDK 形状声明，专项适用）
- implementation_readiness：方案层面通过。E-8（官方 oracle 对账）仍是 S-6 的实施期完成门（合法 pending，见 §6）。本评审不构成实施授权（owner 入口仅要求方案；实施须 owner 在评审通过后另行授权）。

## 1. 结论摘要

r3 的唯一阻塞（F-12：512KB head-only 在 3/22 条真实 reviewer 上不可达 entrypoint）已在 v4 正确闭合，且本轮以**三层独立证据**机器级复证：

1. **官方实现逐字核对**（本轮重新从 npm 下载 `@anthropic-ai/claude-agent-sdk@0.3.301` tarball）：`YD` 分类器、`nl`（首次原生子串）/`jt`（末次）/`dn=65536`、`bi`（head=首 64KB、tail=末 64KB、≤64KB 时 tail=head）、`D0`（`if(!t&&YD(r.head,r.tail))return null`）与 v4 D-1 的双窗口规则（head 512KB 顶层首次 ∪ tail 64KB 顶层末次、空串即停、sessionKind 只用 head、tail 跳过截断首行）逐项对应；sdk.d.ts 的六项 API 声明（dir 语义、limit/offset、includeWorktrees 默认 true、includeProgrammatic 默认 true + IDE picker 传 false、sessionStore 忽略、分类集合 `sdk-cli/sdk-ts/sdk-py` × `daemon/daemon-worker`）逐字吻合。
2. **本机 census 双策略交叉**（策略 A：逐行顶层 JSON 解析；策略 B：原生子串；两策略在 39 文件上结果完全一致）：39 个 top-level transcript，分布 `sdk-py×19 / claude-desktop-3p×15 / sdk-cli×3 / claude-desktop×2`；`sessionKind` 39 文件零出现；全 39 文件中 head 512KB 内零 `"entrypoint"` 子串的**恰好 3 条**（`0296f9ef` 1,906,241B / `1eba385c` 1,260,292B / `a9a158a0` 1,251,451B，均无 401 行——成功安审 reviewer），即 19 条 sdk-py = 16 head 可达 + 3 tail-only，与 §2.5 census 逐项一致。
3. **v4 规则模拟 + 官方 SDK 实跑对账**：按 v4 规则（head 512KB 顶层解析，未命中读 tail 64KB、跳过首行、取末次顶层命中）模拟本机 39 文件得 **hidden 22 / visible 17**；本轮再用官方 0.3.301 实跑 `includeWorktrees:false` 全量枚举得 **prog:true=39 / prog:false=17 / hidden=22**，主 session `7afc3bd7` 保留、3 条大文件 reviewer 全部在隐藏集内——E-8 的本机基线 22 在 v4 双窗口下机械可达，两侧逐项一致。

D-4/D-5 的现状锚点（synthetic 401 行 `stop_reason:"stop_sequence"` ∈ `isFinalClaudeStopReason` → 必然 `turn_completed`；mapper 无 isApiErrorMessage 等四字段；控制面只认 turn_completed；legacy default 保持 running）与真实样本（11 文件各 1 行 401，字段路径 `isApiErrorMessage/apiErrorStatus/error:"authentication_failed"/requestId` + content 内人读文案；主 session 5 行全 429）本轮全部亲核通过。`Sorted` 消费方闭环（list/discovery/recent/pin/watcher + digest），watcher 对离开 `Sorted` 的 session 会在下轮 sweep 清理 tail state（claude_web_push_watcher.go:143-147），升级过渡无残留监听；mapper 全部 6 个生产调用点共用一处，D-4 单点修改覆盖冷 hydrate / source batch / file relay / Web Push preview（+trace 诊断路径）。

历史意见 F-1..F-12 全部闭合（§5 表）。本轮新发现 1 条**建议级**意见（F-13）：§2.5/§3.1 对 3 条大文件「为什么 head 不可达」的机制归因措辞与实测字节布局不符（规则本身正确、基线不受影响），建议修正叙述并让 S-1 大文件 fixture 复刻真实形态。

## 2. 来源身份（本轮亲核）

| 来源 | 身份 | 核验方式 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `6ef1a10dacb258ba389162a5c87262e253d01f1b`，仅方案文档与 r1-r3 报告 untracked（`git rev-parse/branch/status --porcelain` 亲核，与简报一致） | 亲核 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `02a46521678950fd1df47ac951505f3dccba0d4e`，干净 | 亲核 |
| TS SDK | npm `@anthropic-ai/claude-agent-sdk@0.3.301` tarball（本轮 `npm pack` 重新下载至 `/tmp/r4sdk/`，提取 `sdk.mjs`/`sdk.d.ts` 逐字核对；并用该包 node 实跑 listSessions） | 亲核（下载+提取+运行） |
| Python SDK | PyPI `claude-agent-sdk==0.2.168` wheel 下载解包（`/tmp/r4pysdk/`）：`list_sessions(directory, limit, offset, include_worktrees)`，全包 grep 无 programmatic 参数（另核本机 security venv 实为 0.2.137，与 §2.1「venv 0.2.137」一致） | 亲核 |
| TS 公开 repo | `anthropics/claude-agent-sdk-typescript` @ `b0fc766997199a49f90c37f5ec056936c80c9491` 顶层树 = `.claude/.github/CHANGELOG.md/CLAUDE.md/LICENSE.md/README.md/examples/scripts`（GitHub contents API）——无 `src/`、无 `sdk.d.ts`，§2.1「repo 不是实现锚点」属实 | 亲核 |
| 本机 transcript 样本 | `~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/`（39 个 top-level jsonl，只读） | 亲核（双策略 dump + 字节偏移测量 + v4 规则模拟） |

## 3. 锚点核验表（本轮亲核；源码 commit 与 r1/r2/r3 相同，关键项不依赖复用）

| 命题/方案原引用 | 实际位置与原文摘录 | 定位/语义 |
| --- | --- | --- |
| catalog 扫描 top-level JSONL（`:5`、`:276-347`） | claude_session_catalog.go:5-9「只能从 `~/.claude/projects/` 下的 JSONL transcript 文件派生」；:307 `if file.IsDir() || !strings.HasSuffix(name, ".jsonl") { continue }` | 定位准 / 语义支持 |
| list 直接输出 Sorted（`:209-221`） | :214-220 `result := make(..., len(snapshot.Sorted)); for _, entry := range snapshot.Sorted { ... claudeSessionEntryToWire(entry) }` | 定位准 / 语义支持 |
| metadata scan 未保留 entrypoint/sessionKind（handlers.go:4282） | :4282 `func scanClaudeSessionMetadata`；:4297-4298 `if info.Size() > claudeSessionSummaryReadLimit { reader = io.LimitReader(f, claudeSessionSummaryReadLimit) }`；:59 `claudeSessionSummaryReadLimit = 512 * 1024`；`claudeSessionScanResult`（:4245-4268）无 entrypoint/sessionKind | 定位准 / 语义支持 |
| buildSnapshot 全量进 Sorted / lineage delete(ByKey)（`:401-404`、`:487/:557`） | :401-404 `sortedEntries` 取自全部 `nextByKey`；:418-419 先 fork 后 compact；:487 `delete(byKey, sorted[index].Key)`、:557 `delete(byKey, sorted[idx].Key)`——D-2「过滤先于 lineage 则 delete 不可触及 programmatic」推理成立 | 定位准 / 语义支持 |
| watcher 遍历 Sorted（claude_web_push_watcher.go:93-137） | :93 `refresh`；:97-98 `for _, entry := range snapshot.Sorted`；:19 `claudeWebPushWatchInterval = 3 * time.Second`；**补充：:143-147 `for key := range w.states { if _, ok := seen[key]; !ok { delete(w.states, key) } }`——session 离开 Sorted 后 tail state 下轮 sweep 即清理，过滤生效无残留监听** | 定位准 / 语义支持 |
| watcher completion gate 只认 turn_completed | :259 `if event.Event != "turn_completed" { continue }`；:237 行解析复用共享 mapper `claudeEntryToProjectionEvents`——D-4 的 turn_error 天然不产生 candidate | 定位准 / 语义支持 |
| mapper final stop → turn_completed（handlers_relay.go:2206-2451） | :2444-2450 `if isFinalClaudeStopReason(e.Message.StopReason) { ... turn_completed ... TurnDone: true }`；:1819-1826 谓词含 `"stop_sequence"`；`claudeTranscriptRelayEntry`（:1638-1662）无 isApiErrorMessage/apiErrorStatus/error/requestId 四字段 | 定位准 / 语义支持 |
| mapper 单点覆盖全部路径 | 生产调用点 6 处：claude_source_mapper.go:43、claude_source_trace.go:250、claude_web_push_watcher.go:237、handlers_relay.go:1543/:2167/:2194（grep 全仓非测试）——多于方案「三条路径」枚举，无遗漏消费方 | 语义支持 |
| reducer 已支持 turn_error（projection_reducer.go:2481-2521） | :2481 `case "turn_aborted", "turn_error":`；:2500-2502 status="error"；:2510 `dataString(data, "message")` → :2511-2519 `terminal.System`（diagnostic truth）——D-4 的 `"message"` 键与 reducer/iOS 两侧匹配 | 定位准 / 语义支持 |
| 控制面/legacy 现状（D-5） | handlers_relay.go:1615-1627 `hasTurnCompleted → broadcastIdleState + *runningObserved=false`，default → running；legacy :1571-1573 default `sendSessionEvent + *runningObserved=true`；held 只收集 turn_completed（:1556-1563） | 定位准 / 语义支持 |
| Web Push 意图门 | web_push_producer.go `pushIntentForRelayTerminal` switch 首支 `case "turn_completed":`（:130），default 返回 nil（r2 :195-196 亲核，同 commit 复用）；`WebPushKindError` 门 Passed:false（r2/r3 同 commit 复用）——且 legacy 路径仅 turn_completed 分支走 `sendSessionEventWithPushIntent`（handlers_relay.go:1565），D-4 后 synthetic error 行根本不产生 turn_completed | 语义支持（本轮亲核 :130/:1565 + 复用 r2 default 分支） |
| pin 解析消费 Sorted + gone 语义（handlers_session_pin.go:224-236） | :229 `all := h.claudeSessions.list("", nil)`；:236 `return nil, true, nil // not in catalog => gone` | 定位准 / 语义支持 |
| archived 由客户端隐藏（session_discovery.go:483-497） | :483-497 指纹输入过滤 `archivedAtMillis`（注释明示客户端可见性契约）；catalog :601-606 wire 附 `archivedAtMillis` | 定位准 / 语义支持 |
| `Sorted` 消费方闭环 | `claudeSessions.list()` 调用方：handlers.go:4079（list_sessions）、catalog_recent_view.go:205/:265（recent）、session_discovery.go:482（discovery）、handlers_session_pin.go:229（pin）；watcher :93-98 经 refresh；digest :197——与方案 D-2 清单一致，无第 6 个消费面 | 语义支持 |
| ByKey 仅缓存身份 | ByKey 非测试引用仅 :355（fingerprint 命中复用）与 :386（deleted 计数）——D-2 保留 programmatic 于 ByKey 只影响缓存，不泄漏到任何可见面 | 语义支持 |
| iOS 已解码 turn_error | iOS 主源码 `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift:1825` `case "turn_error":` → `.error(sessionId, message)`（fallback "Turn failed"）；R-5 无 iOS 改动成立 | 定位准 / 语义支持 |

## 4. 官方 SDK 语义与真实样本核验（audit-plan 专项）

### 4.1 官方实现（npm 0.3.301，本轮逐字提取）

```js
var dn=65536,SCe=new Set(["sdk-cli","sdk-ts","sdk-py"]);
function YD(e,t){let n=nl(e,"entrypoint")??jt(t,"entrypoint");
  if(n&&SCe.has(n))return!0;
  let r=e.split(`\n`).find((s)=>s.includes('"parentUuid":'))??e,o=nl(r,"sessionKind");
  return o==="daemon"||o==="daemon-worker"}
```

- `nl(e,t)`：`"key":"` / `"key": "` **首次**原生子串匹配（任意层级可命中）；命中值为空串返回 `""`（非-nullish，不触发 `??` 回退）。
- `jt(e,t)`：**末次**出现（`a>o` 保持最大 index 的命中）。
- `bi`：`Buffer.allocUnsafe(dn)` head=文件头 64KB；`a=Math.max(0,r.size-dn)`，`a>0` 时 tail=文件尾 64KB，否则 tail=head。
- `D0`：`if(!t&&YD(r.head,r.tail))return null`（t=includeProgrammatic）。
- `sdk.d.ts`（:1141-1158）：includeWorktrees 默认 true；includeProgrammatic「…(SDK entrypoints `sdk-cli`, `sdk-ts`, `sdk-py`) and daemon/daemon-worker sessions. Defaults to `true` … IDE session pickers pass `false` for parity with terminal `/resume`. … ignored when `sessionStore` is provided.」

**结论**：E-1/E-2 成立；v4 D-1 规则 2（head 顶层首次 ∪ tail 顶层末次、空串即停、tail 跳过截断首行、sessionKind 不读 tail）与规则 3（三分支：首个含 `"parentUuid":` 行内读 / 无该行回退整个 head / 读不到判非 daemon）与官方逐字对应——r2 F-7 与 r3 F-12 均闭合。**与 v3 的实质差别（entrypoint tail 回退）正是 F-12 的修复，且为本轮模拟+实跑双重证实（§4.2/§4.3）。**

### 4.2 本机样本（双策略交叉 + 字节偏移测量 + v4 规则模拟）

- **分布**：策略 A（逐行顶层解析取首个携带行）与策略 B（原生子串）在 39 文件上完全一致：`sdk-py×19、claude-desktop-3p×15、sdk-cli×3、claude-desktop×2`；`sessionKind` 全部 39 文件零出现；19 条 sdk-py 全部 `promptSource:"sdk"`、`isSidechain:false`；reviewer 均无 `<id>/subagents/` 目录（项目内 10 个 subagents 目录属主全部为 claude-desktop(-3p)，与 reviewer 零重叠）；3 条 sdk-cli（`7310d850`/`a84cd7e8`/`f450f935`）首条 user 均为「请直接用 Read 工具读取图片文件…」识图委托。
- **head/tail 可达性**：全 39 文件中 head 512KB 内零 `"entrypoint"` 子串的恰好 3 条（`0296f9ef` 1,906,241B / `1eba385c` 1,260,292B / `a9a158a0` 1,251,451B），三条 tail 64KB 内均含顶层可解析的 entrypoint 行（`type:"attachment"`，值 `sdk-py`）——§2.5「16 head + 3 tail-only」精确成立。
- **v4 规则模拟**：按 §3.1 规则 2（head 512KB 逐行顶层首次命中；未命中读 tail 64KB、跳过首行、末次顶层命中）模拟 39 文件 → **22 programmatic / 17 visible**，与 §2.5 声称的修订时模拟及官方实测一致。
- **synthetic error 行形状**：11 文件各 1 行 401（3 条大文件均无 401——成功安审 reviewer，§9.1 的「I'll read…」消失面成立）；样本行 `type:"assistant"`、`isApiErrorMessage:true`、`apiErrorStatus:401`、顶层 `error:"authentication_failed"`、`requestId:"req_…"`、`message.stop_reason:"stop_sequence"`（∈ isFinalClaudeStopReason → 现行必映射 turn_completed，根因成立）、content text「Failed to authenticate. API Error: 401 Invalid bearer token」（D-4 errorMessage 优先级 text→error 与样本一致：顶层 error 是短码不是人读文案）。主 session `7afc3bd7` 恰 5 行 synthetic 全 429（stop_sequence），§2.5「D-4 会改变这些行的投影」的既有事实声明属实。
- **归因核验（audit-plan attribution discipline）**：对 3 条大文件做了行级/字节级定位——首个携带 entrypoint 的行就是首条 user 行本身（0-based 行 2，起点 423,017–465,711B，行长 423,233–465,927B；行 0 亦为 ~422.9–465.6KB 巨行）。head 不可达的原因是 512KB 截断点落在该巨型行**内部**（部分行不可整行解析），tail 命中来自文件尾部**另一条** `type:"attachment"` 行——与 §2.5「把带 entrypoint 的行推出窗口」的叙述有出入（见 F-13；规则与基线不受影响）。

### 4.3 官方 SDK 行为实测（本轮复跑，dir=iOS 项目，0.3.301）

| 参数（均 `includeWorktrees:false`、limit 200 分页取全） | 返回 |
| --- | --- |
| `includeProgrammatic:true` | **39** |
| `includeProgrammatic:false` | **17** |
| hidden 差集 | **22**；`7afc3bd7` 保留 ✓；3 条大文件 reviewer 全部在隐藏集内 ✓ |

与 §5 E-8 写入的基线（39/17/22）及 §2.3 行为描述一致；E-3 本轮活体复现。

## 5. 历轮意见处置（回归清单）

| 意见 | 严重性 | v4 处置 | 本轮判定 |
| --- | --- | --- | --- |
| r1 F-1 archived 契约矛盾 | 阻塞 | §3.2「相对现状仅新增 Programmatic==false 过滤」+ §7 archived 回归行 | **闭合保持**（亲核 :401-404/:601-606/session_discovery.go:483-497） |
| r1 F-2 sessionKind 跨行偏差（→ r2 F-7） | 建议→阻塞 | v4 规则 3 三分支 | **闭合**（本轮官方 `YD` 逐字对照） |
| r1 F-3 npm 锚定 | 建议 | §2.1 注明 | **闭合保持**（本轮 tarball + GitHub repo 树复证） |
| r1 F-4 E-8 测量口径 | 建议 | 集合运算口径 | **闭合保持** |
| r1 F-5 过滤先于 lineage | 建议 | §3.2 定死顺序 | **闭合保持**（亲核 :487/:557） |
| r1 F-6 部署运行态五条 | 建议 | §6.2 lstart + 独有日志行 | **闭合保持** |
| r2 F-7 `?? e` 分支误读 | 阻塞 | v3/v4 三分支回退整 head | **闭合** |
| r2 F-8 worktrees/limit 口径 | 建议 | E-8 钉死 `includeWorktrees:false`+全量+基线 22 | **闭合**（本轮实跑 39/17/22 复现；基线可达性由 F-12 修复支撑） |
| r2 F-9 sdk-cli census | 建议 | §2.5 补 3 条 + 分布 + §9.1 | **闭合**（本轮 census 19/15/3/2 复证） |
| r2 F-10 pin 解析消费方 | 建议 | §3.2 清单 + gone 后果声明 | **闭合**（亲核 :229-236） |
| r2 F-11 提取层差异 | 建议 | §3.1 差异清单（嵌套键 + 空串即停） | **闭合**（本轮 `nl` 实现复证） |
| r3 F-12 head-only 3/22 不可达 | 阻塞 | v4 D-1 entrypoint tail 64KB 回退 + 差异清单重写 + §2.5 census + E-8 基线依据 + S-1/§7/§8 同步 | **闭合**（本轮三层证据：官方实现逐字 + v4 规则模拟 22/17 + 官方实跑 22；「两条路径实测收敛」虚假断言已删） |

## 6. 意见

### F-13 [建议] §2.5/§3.1 对 3 条大文件 head 不可达的机制归因与实测字节布局不符（规则与基线不受影响）

- 位置：§2.5「巨型首条 user 行（约 421–466KB）把带 entrypoint 的行推出窗口，仅文件尾 64KB 可取到」及「可达性分界是首条 user 行的字节数」；§8「大文件 head 读不到 entrypoint（本机 3/22 条即此形态：首条 user 行约 421–466KB）」。
- 证据（本轮字节级测量，3 条大文件）：携带 entrypoint 的行**就是首条 user 行本身**（0-based 行 2；39 文件中 head 可达者的 entrypoint 也在行 2），其行首 423,017–465,711B **在 512KB 窗口内**，但行 0（~422.9–465.6KB）加行长使 512KB 截断点落在该行内部——行被截断不可整行解析、entrypoint 键位于截断点之后，并非「该行被推出窗口」；tail 64KB 的命中来自文件尾部**另一条** `type:"attachment"` 顶层行，而非原携带行。§2.5 的「421–466KB」同时混用了行 0 尺寸与首条 user 行长两个口径。
- 影响：纯叙述精度问题——D-1 规则 2 的行为规格正确且本轮模拟证实 22/22 隐藏、E-8 基线 22 机械可达，不构成设计缺陷。但 S-1 的大文件 fixture 若按「entrypoint 行位于文件尾」的字面理解构造，将不覆盖真实生产形态（巨型行在 head 窗口边界被截断 + tail 命中来自 attachment 行）——该形态恰恰是 F-12 修复的回归对象。
- 修订方向：§2.5/§8 的机制句改为「行 0 与首条 user 行均为 ~423–466KB 巨行，512KB 截断点落在首条 user 行（entrypoint 携带行）内部使其不可整行解析；tail 64KB 命中来自尾部 `type:"attachment"` 顶层行」；S-1 大文件 fixture 建议复刻该形态（head 边界截断巨行 + tail attachment 携带行）。可顺带在 §3.1 差异清单补一句同类构造性分叉：携带行恰好跨 tail 64KB 起点时，Go 跳过截断首行而官方子串仍可命中（fail-open 方向，已被「head 与 tail 都读不到时保持可见」兜底覆盖）。
- 闭合标准：v5（或实施期文档同步）叙述与字节布局一致；S-1 fixture 含 head 边界截断巨行用例。**建议级——不阻塞本轮通过**；若 owner 接受现状，实施时在 fixture 注释中声明亦可闭合。

## 7. 覆盖说明与未核项

- **已核（本轮亲核）**：方案引用的全部 Go/iOS 锚点（§3 表）；官方 npm 包三重证据（tarball 实现/sdk.d.ts 逐字 + listSessions 实跑 39/17/22）；本机 39 transcript 双策略 census + 字节偏移测量 + v4 双窗口规则模拟；401/429 行形状与字段路径；`Sorted` 全部消费方与 ByKey 缓存闭环；watcher tail-state 清理（升级过渡安全）；mapper 6 个生产调用点；Web Push 意图门（亲核 :130/:1565/:259 + 复用 r2 default 分支，同 commit）；Python SDK 0.2.168 wheel 签名；TS 公开 repo 树；iOS 主源码 turn_error 解码；四拍交互（本修复无新输入面，用户可见面——列表隐藏、无 completion push、打开 session 的 turn_error 诊断+idle——在 §1.1/§3.4/§3.5/§7/§9 写清，与 r1-r3 判定一致，满足仓库规则对此类修复的要求）；D3 预算与 §6.2 部署纪律（killall+pkill 双进程、8777、lstart 代际、本次提交独有隐藏计数日志行、不跑 UI automation）与 AGENTS.md 逐条对应；门控一致性（OD-1..OD-5 decided 且与正文一致；E-1..E-7 本轮独立复证 verified；E-8 pending 为 S-6 完成门，合法 pending；S-1..S-7 依赖序无悬空、无既允许又禁止项；§6.1 正则以实际测试名为准的声明成立——`TestClaudeEntryToProjectionEvents`/`TestClaudeSessionCatalog*` 存在，复用 r3 同 commit 核验）。
- **未核**：npm 精确 publish 时间戳（§2.1 已声明未核，不影响任何命题）；`sessionKind=daemon` 活体样本（本机零出现——本轮 grep 复证；官方源码逐字支撑 + S-1 fixture 路径成立）；iPhone 截图本体（11 条 401 中 5 条 2026-10-16 下午的时间戳分布复用 r2 同 commit 核验；本轮复证 11 文件各 1 行 401 与 §2.5「11 条认证失败」一致）。
- **环境说明**：本轮一次性下载物在 `/tmp/r4sdk/`、`/tmp/r4pysdk/`，脚本在 `/tmp/r4_*.py`、`/tmp/r4_oracle.mjs`（仓库与 `~/.claude` 数据只读，未触碰；报告为本轮唯一写入仓库的文件）。

## 8. 剩余门与处置

- open gates：**E-8**（官方 TS SDK oracle 对账——实施期 S-6 完成门；基线 22 的双侧可达性本轮已机器级预证：官方实跑 hidden=22 + v4 规则模拟 hidden=22）。
- 实施前置：S-1..S-7 依赖已满足（E-1..E-7 verified、OD-1..OD-5 decided）；实施仍须 owner 另行授权，且开工前按 P0 来源门重新生成来源清单（方案 §2.1 已自我约束）。
- F-13 为建议级：可在实施期的 GO_BRIDGE_ARCHITECTURE 文档同步或 S-1 fixture 注释中闭合，不要求再走一轮方案修订；若作者选择出 v5 修订叙述，属文档级变更。

---

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md
plan_sha256: bb67fea8f3966dcd19842ac0f050695673c340c4f6ffd485dc94d1e08355642b
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan-review-r4.md
scope: full
blockers: 0
advisories: 1
open_gates: [E-8]
round_complete: true
contract_version: plan-contract-v1.1
```
