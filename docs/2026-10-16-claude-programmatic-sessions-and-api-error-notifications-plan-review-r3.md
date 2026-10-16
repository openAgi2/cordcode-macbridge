# 方案评审报告 · 第 3 轮（fresh）

- verdict: **REVISION_REQUIRED**
- 方案：`docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md`（v3）
- 评审时内容 SHA-256：`bbbe5684168b33d0028eba0d7d7a58b3d6a3b569d79be5055fb64397d8d18c10`（与派发记录一致，身份已确认）
- blockers: **1**（F-12）；advisories: **0**
- 评审范围：full（全文档）；评审员模式：fresh（先独立形成风险清单并亲核，后核对 r1/r2 作回归清单）
- 契约版本：plan-contract-v1.1（plan-review SKILL 评审程序 + audit-plan 接入约定已执行——方案含外部 JSONL/SDK 形状声明，专项适用）
- implementation_readiness：v3 对 r2 F-7..F-11 的处置全部落实到文本且语义正确，但本轮以本机真实样本发现 **D-1 的 512KB head-only 提取窗口在 22 条 programmatic session 中的 3 条上不可达 `entrypoint`**（官方靠 tail 64KB 回退兜住）——R-1/验收标准 1/S-6 基线三处被推翻（F-12）。S-1/S-2 实施前置不满足，需 v4 修订后重审。本评审不构成实施授权。

## 1. 结论摘要

r2 的唯一阻塞（F-7：官方 `?? e` 分支误读）已在 v3 正确闭合——本轮从 npm tarball 逐字提取官方 `YD` 完整实现并与 v3 规则 3 三分支逐句对照确认。r2 的 4 条建议（F-8..F-11）也全部落实到文本。

但 fresh 复核中以**双策略 census + 字节偏移测量**发现一个 r1/r2 均漏检、且直接推翻方案核心可行性前提的问题：**官方 entrypoint 提取是 head 64KB ∪ tail 64KB 双窗口（`nl(head) ?? jt(tail)`），而方案 D-1 声明 Go 沿用 512KB head、不读 tail，其差异声明断言「真实样本 entrypoint 出现在第 3 行，两条路径实测收敛」。实测本机 22 条将被隐藏的 programmatic session 中有 3 条（`0296f9ef` 1.9MB / `1eba385c` 1.26MB / `a9a158a0` 1.25MB，均为无 401 的成功安审 reviewer）在 512KB head 内**连 `"entrypoint"` 子串都不存在**——它们的首条 user 行达 421–466KB，把带 entrypoint 的行推出 512KB 窗口；官方实现靠 tail 64KB 回退（`jt` 最后一次出现）取到 `sdk-py` 并隐藏它们（本轮 TS SDK 复跑 hidden=22 包含这 3 条）。Go head-only 实现将漏隐藏 3/22：E-8 机械断言 `GoHidden == TSHidden == 22` 必假（实际 19），验收标准 1「列表不再出现 'I'll read...' 安审 reviewer」失败（3 条仍在），§3.1「两条路径实测收敛」是与事实相反的断言。详见 F-12。

其余维度——官方 API 形状（sdk.d.ts JSDoc + `L0` 默认值）、本机 census 分布（19/15/3/2）、TS 行为实测（39/17→22；worktree 139/109→30）、synthetic 401 行形状、mapper/Web Push/reducer/控制面/Sorted 消费方全部锚点、archived 契约与 lineage 顺序闭合的保持——本轮全部亲核通过。

## 2. 来源身份（本轮亲核）

| 来源 | 身份 | 核验方式 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `6ef1a10dacb258ba389162a5c87262e253d01f1b`，仅方案与 r1/r2 报告 untracked | `git rev-parse/branch/status` 亲核，与简报一致 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `02a46521678950fd1df47ac951505f3dccba0d4e`，干净 | 同上 |
| TS SDK | npm `@anthropic-ai/claude-agent-sdk@0.3.301` tarball（tgz SHA-256 `1cf0f6a61294cb7edd3ef2682828139ba999f7dd263e46d75d4135ddd3a55b05`，与 r2 记录一致）；`sdk.mjs`/`sdk.d.ts` 逐字核对 + **实跑 listSessions 四组参数** | 本轮亲核（下载 + 提取 + 运行） |
| 本机 transcript 样本 | `~/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/`（39 个 top-level jsonl） | 本轮亲核（官方 nl/jt 语义复刻 census + 字节偏移测量，见 §4.2） |

## 3. 锚点核验表（本轮全部亲核，除注明复用）

| 命题/方案原引用 | 实际位置与原文摘录 | 定位/语义 |
| --- | --- | --- |
| catalog 扫描 top-level JSONL（`:5`、`:276-347`） | claude_session_catalog.go:5-9「只能从 `~/.claude/projects/` 下的 JSONL transcript 文件派生」；:307 `if file.IsDir() \|\| !strings.HasSuffix(name, ".jsonl") { continue }` | 定位准 / 语义支持 |
| list 直接输出 Sorted（`:209-221`） | :214-220 `result := make(..., len(snapshot.Sorted)); for _, entry := range snapshot.Sorted { ... claudeSessionEntryToWire(entry) }` | 定位准 / 语义支持 |
| metadata scan 未保留 entrypoint/sessionKind（handlers.go:4282） | :4282 `func scanClaudeSessionMetadata`；:4297-4298 `if info.Size() > claudeSessionSummaryReadLimit { reader = io.LimitReader(f, claudeSessionSummaryReadLimit) }`；:59 `claudeSessionSummaryReadLimit = 512 * 1024`；全函数无 entrypoint/sessionKind | 定位准 / 语义支持 |
| watcher 遍历 Sorted（claude_web_push_watcher.go:93-137） | :93 `refresh`；:97-98 `seen := make(..., len(snapshot.Sorted)); for _, entry := range snapshot.Sorted`；:19 `claudeWebPushWatchInterval = 3 * time.Second` | 定位准 / 语义支持 |
| mapper final stop → turn_completed（handlers_relay.go:2206-2451） | :2444-2449 `if isFinalClaudeStopReason(e.Message.StopReason) { out = append(out, projectionHydrateEvent{Event: "turn_completed", ..., TurnDone: true}) }`；:1819-1826 谓词含 `"stop_sequence"`；`claudeTranscriptRelayEntry`（:1638-1662）无 isApiErrorMessage 等四字段 | 定位准 / 语义支持 |
| reducer 已支持 turn_error（projection_reducer.go:2481-2521） | :2481 `case "turn_aborted", "turn_error":`；:2500-2502 `status = "error"`；:2510 `dataString(data, "message")` → :2511-2519 `terminal.System = &MessageProjection{...}`（D-4 的 `"message"` 键与 reducer 匹配） | 定位准 / 语义支持 |
| pin 解析消费 Sorted（v3 新增，F-10 闭合） | handlers_session_pin.go:229 `all := h.claudeSessions.list("", nil)`；:236 `return nil, true, nil // not in catalog => gone`——退出 Sorted 的 pinned programmatic session 按「definitively gone」处理，与方案后果声明一致 | 定位准 / 语义支持 |
| archived 由客户端隐藏 | claude_session_catalog.go:601-606「Surface archivedAtMillis so clients can hide archived sessions (web session-grouping filters on archivedAtMillis)」；session_discovery.go:483-497 指纹输入过滤 archived；buildSnapshot :401-404 全量 nextByKey（含 archived）进 Sorted | 定位准 / 语义支持（F-1 闭合保持） |
| lineage delete(ByKey) 与过滤顺序 | :418-419 `sortedEntries = hideClaudeForkChildren(nextByKey, sortedEntries); sortedEntries = hideClaudeCompactContinuationParents(...)`；:487（compact）/ :557（fork）`delete(byKey, ...)`——D-2「过滤先于 lineage 则 delete 不可触及 programmatic」推理成立 | 定位准 / 语义支持（F-5 闭合保持） |
| 控制面只认 turn_completed（D-5 现状） | handlers_relay.go:1615-1635 `hasTurnCompleted → broadcastIdleState + *runningObserved=false`，`default: *runningObserved = true`；legacy :1571-1573 `default: sendSessionEvent(...); *runningObserved = true` | 定位准 / 语义支持 |
| Web Push gate 只认 turn_completed、Error 门关闭 | web_push_producer.go:130 `case "turn_completed":`、default 分支 `return nil`（:195-196 区域）；passive `if eventName != "turn_completed" { return nil }`；`enqueueReplayFreeLivePush ... eventName != "turn_completed" → false`；:40 `WebPushKindError: {GateID: "EVT-ERROR-1", Passed: false}` | 定位准 / 语义支持（D-4 后 turn_error 不产生任何 push intent） |
| iOS 已解码 turn_error | iOS `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift:1825` `case "turn_error":` | 定位准 / 语义支持 |
| mapper 生产调用点 | handlers_relay.go:1543（legacy row）、:2167/:2194（hydrate）、claude_source_mapper.go:43、claude_source_trace.go:250（trace 回放，诊断路径）、claude_web_push_watcher.go:237——全部经同一 `claudeEntryToProjectionEvents`，D-4 单点修改自动覆盖（比方案「三条路径」枚举更全，无遗漏消费方风险） | 语义支持 |
| §6.1 正则与现有测试名 | `TestClaudeEntryToProjectionEvents`（handlers_projection_test.go:1225）、`TestClaudeSessionCatalog*`（12 个，claude_session_catalog_test.go）存在；`TestClaudeSessionFileRelay`/`TestClaude.*APIError`/`TestClaudeWebPushWatcher*` 目前不存在（方案已声明以实际测试名为准，属新建） | 语义支持 |
| E-4 Python 无过滤参数 | （复用 r2 §3 wheel AST 核验：签名 `directory/limit/offset/include_worktrees`，无 include_programmatic；方案 §2.1 该行 v3 未变） | 复用，依据充分 |

## 4. 官方 SDK 语义与真实样本核验（audit-plan 专项）

### 4.1 官方实现（npm 0.3.301 tarball，本轮逐字提取）

```js
function YD(e,t){let n=nl(e,"entrypoint")??jt(t,"entrypoint");
  if(n&&SCe.has(n))return!0;
  let r=e.split(`\n`).find((s)=>s.includes('"parentUuid":'))??e,o=nl(r,"sessionKind");
  return o==="daemon"||o==="daemon-worker"}
```

- `nl(e,t)`：在字符串 e 中找 `"key":"` / `"key": "` **首次**出现的原生子串匹配（`indexOf`，任意层级可命中——F-11 差异声明属实）；找不到返回 undefined；命中值为空串时返回 `""`（非-nullish，不触发 `??` 回退——F-11 空串语义声明属实）。
- `jt(e,t)`：**tail 中最后一次出现**。
- `bi`（读文件）：head=文件头 64KB（`Buffer.allocUnsafe(dn)`，`dn=65536`）；`a=Math.max(0,r.size-dn)`，`a>0` 时 tail=文件尾 64KB，否则 tail=head。
- `D0`：`if(!t&&YD(r.head,r.tail))return null`（t=includeProgrammatic）。
- `L0`（listSessions）：`d=l??!0`（includeProgrammatic 默认 true）、`iOe(o,a??!0,...)`（includeWorktrees 默认 true）。
- `sdk.d.ts`（:1141-1158）：`includeWorktrees` 默认 true；`includeProgrammatic`「Include programmatic/headless sessions (SDK entrypoints `sdk-cli`, `sdk-ts`, `sdk-py`) and daemon/daemon-worker sessions. Defaults to `true` … IDE session pickers pass `false` for parity with terminal `/resume`. … ignored when `sessionStore` is provided.」

**结论**：E-1/E-2 成立；v3 D-1 规则 3 三分支（首个含 `"parentUuid":` 行内读 / 无该行回退整个 head / 读不到判非 daemon）与官方 `YD` 逐字对应——**r2 F-7 闭合**。规则 2 的空串即停语义与官方一致。**但官方 entrypoint 提取的完整覆盖窗口是 head 64KB ∪ tail 64KB（tail 取最后一次），这正是 F-12 的根因。**

### 4.2 本机样本（官方 nl/jt 语义复刻 census + 字节偏移测量）

- **entrypoint 分布**（复刻官方 head64K+tail64K 提取语义）：`sdk-py`×19、`claude-desktop-3p`×15、`sdk-cli`×3、`claude-desktop`×2——与 §2.5 census 逐项一致 ✓；`sessionKind` 39 文件零出现 ✓；11 文件含 `"apiErrorStatus":401` ✓；主 session `7afc3bd7` 5 行 `isApiErrorMessage:true` 且全为 429 ✓。
- **TS SDK 行为实测复跑**（dir=iOS 项目，0.3.301）：非 worktree prog:true/false = **39/17**（hidden=**22**，`7afc3bd7` 保留 ✓）；worktree = **139/109**（hidden=**30**）——与 v3 §5 E-8 写入的本机基线数字完全一致 ✓（E-3 复现）。
- **决定性新事实（F-12 证据）**：按**子串级**测量，22 条 programmatic 文件中 3 条在 **512KB head 内零 `"entrypoint"` 出现**（`find` 返回 -1）：

  | 文件 | 大小 | 首行字节数 | 性质 |
  | --- | --- | --- | --- |
  | `0296f9ef-02a1…` | 1,906,241 | 465,572 | sdk-py，29 行，无 401（成功 reviewer） |
  | `1eba385c-6c5b…` | 1,260,292 | 422,878 | sdk-py，30 行，无 401（成功 reviewer） |
  | `a9a158a0-979a…` | 1,251,451 | 428,151 | sdk-py，35 行，无 401（成功 reviewer） |

  这 3 条的 entrypoint 值只能从文件尾 64KB 内取到（官方 `jt` tail 回退命中 `sdk-py`，故在 TS hidden=22 内）；任何 512KB-head 解析策略（子串或顶层行）都提取不到。另 6 条大 reviewer（106KB–576KB）的顶层 entrypoint 行偏移在 47KB–248KB 之间，512KB head 可达——**分界不是「第 3 行」而是首条 user 行的字节数**。

## 5. 意见

### F-12 [阻塞] 512KB head-only 提取在本机 3/22 条真实 reviewer 上不可达 entrypoint：E-8 基线 22 在 Go 侧必假，验收标准 1 失败，§3.1「两条路径实测收敛」断言与事实相反

- 位置：§3.1 D-1 差异清单「两实现仅在『head 64KB 内无 entrypoint 但文件尾 64KB 内有』的构造性场景分叉——真实样本 `entrypoint` 出现在第 3 行（§2.5），两条路径实测收敛」；§2.5「（已支撑……）」与 entrypoint 可达性表述；§5 E-8 基线「本机基线期望值为 22」；§9.1。
- 证据：见 §4.2 表。官方 entrypoint 覆盖窗口 = head 64KB（首次，任意层级）∪ tail 64KB（**最后**一次，`jt`）；方案 D-1 = head 512KB 顶层行解析、显式「不读 tail」。本机 22 条 programmatic 中 3 条（`0296f9ef`/`1eba385c`/`a9a158a0`，均为无 401 的「成功安审」reviewer，首条 user 行 421–466KB）head 512KB 内零 `"entrypoint"` 子串，仅 tail 可达（TS 实测 hidden=22 含它们）。方案把「entrypoint 出现在第 3 行」当作「512KB 内可达」的证据——行号不证明字节可达性，本机 3 文件的带 entrypoint 行被巨型 user 行推出窗口。历史核验 r1 §4.2/r2 §4.2 的「entrypoint（均）在文件第 3 行」未验证字节偏移，属漏检。
- 影响：
  1. **验收标准 1 / R-1 失败**：修复后这 3 条 reviewer 仍将显示在 iPhone 列表（它们正是 owner 要消除的「I'll read…」类安审 reviewer 中的 3 条）；
  2. **E-8 机械断言必假**：GoVisibleAll − GoVisibleDefault = 19 ≠ TSHidden = 22——S-6 要么假失败（触发「记录差异并停止交付」），要么靠「逐项记录并解释」豁免 3 条，恰是 F-8 要求消除的非机械性；
  3. **差异声明为虚假断言**：「两条路径实测收敛」在本机 3/22（14%）真实样本上即不收敛，且该场景不是「构造性」的——它就是当前生产样本。
  对 S-1（提取规则规格）、S-2（隐藏集合）、S-6（对账基线）、E-8、验收 1 全部有影响。
- 修订方向（最小充分）：D-1 对齐官方双窗口——保留 512KB head 顶层行解析，**为 `entrypoint` 增加文件尾 64KB 回退**（对齐官方 `nl(head) ?? jt(tail)`：head 未命中时在 tail 内取**最后一次**出现；tail 侧用子串还是按行顶层解析需按 F-11 同层声明差异；tail 首行可能被截断，按行解析时需跳过不完整行）。注意**只给 entrypoint 加 tail**：官方 `sessionKind` 只用 head（`nl(r,…)` 的 r 来自 head），不得给 sessionKind 加 tail。同步修正：§3.1 差异清单重写分叉条件（官方覆盖 `[0,64K)∪[size-64K,size)`、Go 覆盖 `[0,512K)∪[size-64K,size)` 后的残余分叉仅剩「entrypoint 唯一/全部出现落在 `(512K, size-64K)` 中段」一类，写明真实样本已无此形态）；§2.5 补字节偏移事实（19 条 sdk-py 中 16 条 head 512KB 顶层可达、3 条仅 tail 可达，列出 3 个 ID）；E-8 基线 22 在双窗口下可达，保持 22。若作者不采纳米 tail 回退而选其它工程手段（如无界读至首个含顶层 entrypoint 的行），需重新论证性能与等价性；「head-only + 基线改 19 + 豁免 3 条」不可取——它违反验收标准 1 与 R-1，不是等价选项。
- 闭合标准：v4 的 D-1 提取规则与官方 `nl(head)??jt(tail)` 语义一致（或差异有显式声明且本机 22 条全部仍被隐藏）；§3.1 不再含「两条路径实测收敛」类与现实相反的断言；E-8 基线 22 在修订后的 Go 行为下可机械达成；§2.5 census 写明 3 条 tail-only 文件。

## 6. r1/r2 意见处置（回归清单）

| 意见 | 严重性 | v3 处置 | 本轮判定 |
| --- | --- | --- | --- |
| r1 F-1 archived 契约矛盾 | 阻塞 | §3.2「相对现状仅新增 Programmatic==false 过滤」+ §7 archived 回归行 | **闭合保持**（亲核 :401-404/:601-606/session_discovery.go:483-497） |
| r1 F-2 sessionKind 跨行偏差 → v2 引入 F-7 | 建议→阻塞 | v3 三分支 | **闭合**（本轮官方 `YD` 逐字对照，§4.1） |
| r1 F-3 npm 锚定 | 建议 | §2.1 注明 | **闭合保持**（本轮 tarball 独立复证） |
| r1 F-4 E-8 测量口径 | 建议 | 集合运算口径 | **闭合保持**；口径本身仍成立，基线数字被 F-12 推翻 |
| r1 F-5 过滤与 lineage 顺序 | 建议 | §3.2 定死先于 lineage | **闭合保持**（亲核 :418-419/:487/:557） |
| r1 F-6 部署运行态 | 建议 | §6.2 lstart + 独有日志行 | **闭合保持**（§6.2 与 AGENTS.md 部署后验证逐条对应） |
| r2 F-7 `?? e` 分支误读 | 阻塞 | v3 三分支回退整 head | **闭合**（§4.1 逐字对照） |
| r2 F-8 worktrees/limit 口径 | 建议 | E-8 钉死 `includeWorktrees:false`+全量+基线 22 | 文本**闭合**且数字与本轮复跑一致（39/17/22、139/109/30）；但基线 22 的 **Go 侧可达性**被 F-12 推翻 |
| r2 F-9 sdk-cli census | 建议 | §2.5 补 3 条 + 分布 + §9.1 | **闭合**（本轮 census 19/15/3/2 复证） |
| r2 F-10 pin 解析消费方 | 建议 | §3.2 清单 + 后果声明 | **闭合**（亲核 handlers_session_pin.go:229-236） |
| r2 F-11 提取层差异 | 建议 | §3.1 差异清单两项 | **闭合**（亲核 `nl` 为原生子串匹配、`""` 非-nullish） |

新问题 F-12 注明来源：**旧问题漏检**（r1/r2 均把「entrypoint 在第 3 行」当作 512KB 可达性证据，未测字节偏移），非 v3 修订引入；v3 的 E-8 复跑反而把 TS 侧数字钉死了，使本轮 Go 侧不可达性可被机械证明。

## 7. 覆盖说明与未核项

- 已核（本轮亲核）：方案引用的全部 Go/iOS 锚点（§3 表）；官方 npm 包三重证据（tarball 逐字 + `sdk.d.ts` JSDoc + listSessions 四组实跑）；本机 39 transcript 的官方语义复刻 census 与 entrypoint 字节偏移测量；401/429 计数；`Sorted` 全部消费方（watcher/catalog digest/list/discovery/recent view/pin）；Web Push 三处 gate 与 Error 门；detail 按-ID 独立路径（复用 r1/r2，方案 v3 该行未变）；mapper 全部 6 个生产调用点（含 r1/r2 未列的 claude_source_trace.go:250，诊断路径，无独立语义要求）；四拍交互（本修复无新输入面，用户可见面——列表隐藏、无 completion push、turn_error 诊断+idle——在 §1.1/§3.4/§7/§9 写清，满足仓库规则对此类修复的要求）；D3 预算/部署纪律与 AGENTS.md 一致；§6.1 正则与现有测试名可匹配性；门控一致性（OD-1..OD-5 decided 且与正文一致，E-1..E-7 verified——E-4 复用 r2，其余本轮复证，E-8 pending 为 S-6 完成门属合法 pending）。
- 未核：npm 精确 publish 时间戳（与 r1/r2 同，不影响任何命题）；`sessionKind=daemon` 活体样本（本机零出现——本轮 grep 复证；官方源码+fixture 路径成立）；iPhone 截图本体；TS SDK 公开 repo 状态（方案 §2.1 表述未变，复用 r1 核验）。
- 环境清理：本轮下载物在 `/tmp/tsdk-r3-review/`（评审用一次性目录，未触碰仓库与 `~/.claude` 数据——所有本机样本检查均为只读）。

## 8. 剩余门与处置

- open gates：**E-8**（官方 oracle 对账，实施期完成门；受 F-12 约束——基线 22 需 v4 的双窗口提取规则支撑后才可达）。
- 下一轮：v4 按 F-12（必须）修订 §3.1（提取规则 + 差异清单）、§2.5（3 条 tail-only 文件）、§5 E-8（差异声明与基线可达性依据），第 10 节追加修订记录、开头声明本轮改动范围。无新增建议项。

---

```
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md
plan_sha256: bbbe5684168b33d0028eba0d7d7a58b3d6a3b569d79be5055fb64397d8d18c10
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan-review-r3.md
scope: full
blockers: 1
advisories: 0
open_gates: [E-8]
round_complete: true
contract_version: plan-contract-v1.1
```
