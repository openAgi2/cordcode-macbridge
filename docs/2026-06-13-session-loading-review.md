# Session Loading 重构方案 — 评审报告

> 评审对象：[docs/2026-06-13-session-loading-systemic-redesign.md](2026-06-13-session-loading-systemic-redesign.md)
> 评审日期：2026-06-13
> 评审范围：方案文档本身 + 对照 `cccode-macbridge` 仓库实际代码验证其论断
> 评审方法：逐条核对文档引用的代码位置与数据结构，评估设计正确性、可实施性、完整性
> 未能验证：iOS 侧（`opencodeIosNew` / `OpenCodeiOS`）引用位于另一仓库，本文无法核对，仅评估其 Bridge 侧依赖是否成立

---

## 0. 评审结论（TL;DR）

方向正确，但**作为"下一个 agent 的直接输入"尚不可用**。最严重的问题是文档对当前代码状态存在关键性误判：支柱 A 的 Codex 半边**几乎已经实现且已测试**，文档却把它当成新建工作；同时有几处设计与现有代码相矛盾（最典型：claudecode 列表路径根本不走 `agent.ListSessions()`，改它无效）。此外根因模型建立在**未经测量的性能假设**之上。

| 严重度 | 数量 | 含义 |
| --- | --- | --- |
| 🔴 Blocker（不修正就实施会做无用功或埋 bug） | 4 | 必须在动工前解决 |
| 🟠 Major（设计缺陷 / 论断不准确） | 7 | 强烈建议修正 |
| 🟡 Minor（精度 / 一致性 / 可读性） | 6 | 建议修正 |

**建议处置**：不要按文档原样进入实施。先补一次"性能测量 + 代码现状核对"（见 §6 M1、§7 第 0 步），再把支柱 A 缩窄为"仅 Claude 侧补建索引并接到 handlers.go 热路径"，其余支柱据此重排。

---

## 1. 优点（先肯定做得对的地方）

- **三支柱分解本身是合理的**：有界增量索引 / cursor 分页 / 合并 RPC，确实对应了"无界载荷、冗余往返、重复扫描"三类真实问题。
- **向后兼容策略正确**：可选字段 + 新方法名 + 不动 `protocol.version`，与 [bridge-v1.md:177-178](../protocol/bridge-v1.md#L177-L178)「新字段须可选、旧客户端忽略」一致。
- **Phase 按 ROI 排序合理**：内部索引先行（零协议改动），再协议分页，再 RPC 合并。
- **后端隔离原则正确**：文件型（Claude/Codex）与代理型（OpenCode）区分对待，OpenCode 走 `ocProxy` 不在本方案范围。
- **"不做的事"边界清晰**，与 AGENTS.md（无 mock/fallback、不做 UI 自动化）一致。
- **风险表识别了真实风险**（mtime 误判、cursor 跨重启、滚动体验、OpenCode 回退），尽管部分对策不完整（见 §3）。

---

## 2. 🔴 Blocker 级问题

### B1. 支柱 A 的 Codex 半边已经存在且已测试——文档在提议重建已有代码

文档把 `agent/codex/session_index.go` 列为「← 新建」、`codex.go` 标为「ListSessions 改为读索引」，Phase 1 描述为「投入最小、可立即验证」。

但实际代码里 **Codex 已经有一套完整的 mtime 增量索引**：

- [agent/codex/list.go:42-140](../agent/codex/list.go#L42-L140) `sessionListCache`：
  - `fileEntry{mtime, info}` 缓存每个 JSONL 的解析结果 + mtime（与文档 A.1 的 `SessionIndexEntry.FileMtime` 同构）。
  - `list()` 是 **4 阶段增量刷新**：walk 收集 mtime → 对比缓存找 changed/deleted → 只重解析变化文件 → 重建排序快照。这正是文档 A.2 描述的算法。
  - 有界读取已落地：[list.go:21](../agent/codex/list.go#L21) `codexSessionListPrefixBytes = 256KB`（文档 A.3 的「~256KB」正是这个常量）。
- [agent/codex/codex.go:47](../agent/codex/codex.go#L47) `sessionCache sessionListCache` 字段，[codex.go:386-391](../agent/codex/codex.go#L386-L391) `ListSessions` 已经 `return a.sessionCache.list(codexHome)`。
- [agent/codex/list_cache_test.go](../agent/codex/list_cache_test.go) 已有 3 个测试：不重写 rollout、大文件有界前缀读取、返回副本不可污染。文档 A.6 的验收项「连续请求两次第二次 < 50ms 命中缓存」由现有缓存直接满足。

**结论**：支柱 A 的 Codex 部分 ~90% 已完成。文档把它当新建工作，会浪费投入，且暴露出**文档撰写时未阅读现有 Codex 实现**这一更深层的问题——这会让人怀疑其它论断是否同样未核对。

> 修正建议：把 Phase 1 改写为「**Codex 侧已完成**（核对 [list.go](../agent/codex/list.go)），本阶段仅针对 Claude 侧补建索引」。并据此重算 ROI 排序与性能基线（见 B2）。

### B2. 根因模型未经测量，且与 Codex 现状自相矛盾

文档声称「Codex 列表加载 5-15s」「瓶颈在 Bridge 端处理逻辑」，但 Codex 已是增量缓存（B1）。这意味着至少有一项成立：

1. **冷启动首扫**慢（可接受，二次请求应秒级）——但文档说"加载"持续 5-15s，未区分冷热；
2. **mtime 不断失效导致每次退化为全量扫描**——Codex 的 `source:exec→cli` 改写（[list.go:370 patchSessionSource](../agent/codex/list.go#L370)）和 app-server 访问是否会触达 rollout 文件、改变 mtime，文档完全没调查；
3. **瓶颈根本不在列表扫描**——而在 JSON 序列化大载荷、网络、或 iOS 侧处理。

文档第 8 节给出了基线数字，但**没有任何测量方法**（在哪台设备、用哪个时钟、量的是 handler 耗时还是端到端）。没有方法，「重构后对比」就是空话。

> 修正建议：动工前先**加一次实测量**。[handlers.go](../go-bridge/handlers.go) 已有大量 `slog.Info` 计时点（如 2441、2468），用现有日志或临时计时确认 5-15s 花在哪一段，再决定是否需要"系统性重构"。这直接决定了 B1 之后到底还要做什么。

### B3. `resume_session` 当前已是近乎空操作——支柱 C 的「4→1」收益被显著夸大

文档把「打开会话 4 步串行往返（resolve → get_session → resume → get_messages）」作为支柱 C 的动机，声称合并后消灭 3 次往返。

但 [handlers.go:2649-2677 handleResumeSession](../go-bridge/handlers.go#L2649-L2677) 实际只做三件事：订阅连接、解析 directory、返回——**明确不启动进程**，注释写明「实际 session 创建延迟到 send_message」。这是一次极廉价的 RPC（≈1 个 LAN RTT）。

而且文档自己第 1.2 节的止血表已列出「冗余 resume 移除」**已经做掉**——意味着客户端那条 resume 往返可能已经删了。

因此支柱 C 真正能省下的，是把 2 次**廉价**往返（resolve + resume）合并到 1 次 RPC，在 LAN 上约省 5–20ms。而用户感知的"长时间转圈后超时"是**单次 RPC 太慢**（历史解析），不是往返次数多——合并 RPC **不减少**那次历史解析的工作量。

> 修正建议：（a）在文档中明确：**支柱 C 解决的是往返延迟，支柱 B 才解决超时**；（b）重排叙事——支柱 B（限定 messageLimit）是打开超时的真正解药，C 只是锦上添花；（c）若实测（B2）显示往返延迟占比可忽略，考虑把 C 降级为可选/推迟。

### B4. 支柱 A 对 Claude 的接入点写错了——改 `claudecode.ListSessions` 对热路径无效

文档 A.5 写「`claudecode.go` ← ListSessions 改为读索引」。

但列表的**真正热路径不走 `agent.ListSessions()`**：[handlers.go:2185-2244 handleListSessions](../go-bridge/handlers.go#L2185-L2244) 在 [handlers.go:2189](../go-bridge/handlers.go#L2189) 用 `if agent.Name() != "claudecode"` 把 claudecode 分支单独拎出来，直接调用 `scanSessionsFromProjectDir` → `scanClaudeSessionSummary`（[handlers.go:2323](../go-bridge/handlers.go#L2323)、[handlers.go:2359](../go-bridge/handlers.go#L2359)）。

也就是说，[claudecode.go:436 ListSessions](../agent/claudecode/claudecode.go#L436) 在列表场景是**死代码**。只改它会建出一个**永远不被调用**的索引，缓存命中率永远是 0。

> 修正建议：把接入点改为 [handlers.go `scanSessionsFromProjectDir` / `scanClaudeSessionSummary`](../go-bridge/handlers.go#L2323)（或在 `handleListSessions` 的 claudecode 分支引入索引）。同时确认 [claudecode.go:436 ListSessions](../agent/claudecode/claudecode.go#L436) 是否还有其它调用方；若无，标注其为仅供回退。

---

## 3. 🟠 Major 级问题

### M-1. 消息反向读取（B.2）的分组边界问题被一笔带过

文档 B.4 提议「`rich_history_reverse.go` 新建：从文件尾部反向读取 N 行（支持 beforeCursor）」，B.2 说 cursor 编码「行号或 offset」。

但 rich history **不是 1 行 = 1 条消息**。Codex 的 `richHistoryBuilder`（[rich_history.go:219](../agent/codex/rich_history.go#L219)）与 Claude 的 `richHistoryMessageBuilder` 会把多条 JSONL（assistant text + reasoning + tool_use + 后续 tool_result）**合并成一个 entry**。从任意行号反向切窗，会把一对 `tool_use`/`tool_result` 劈成两半，产出残缺消息（有工具调用无结果，或反之）。

更关键：当前 `parseRichHistoryFromJSONL` **即使传了 limit 也是先全量解析再截尾**（[rich_history.go:182-185](../agent/codex/rich_history.go#L182-L185) `entries[len(entries)-limit:]`）。所以 500 条消息的会话打开超时，**真正的成本是全量解析**，而反向读取非但没解决它，还会引入正确性 bug。

> 修正建议：给出明确的分页策略。例如「整文件流式解析 + 滚动窗口内重排」，或维护一份（offset → message-index）轻量索引；并写一条专门测试：跨 tool_use/tool_result 边界翻页时消息不残缺。

### M-2. list cursor 用 `updatedAtMillis` 分页——时间戳碰撞会丢/重条目

B.1「cursor 编码 `updatedAtMillis` 偏移量，按时间降序分页」。但多条 session 极易共享同一毫秒值：

- Claude 侧 `updatedAt` 在文件过大时回退到 `fallbackTime`（文件 mtime，秒级精度，[handlers.go:2359-2431](../go-bridge/handlers.go#L2359-L2431)）；
- Codex 侧 `ModifiedAt` 直接用 `stat.ModTime()`（[list.go:244](../agent/codex/list.go#L244)），秒级。

严格按 `< cursor` 翻页，同时间戳的 session 会整组丢失或重复。

> 修正建议：cursor 必须带**确定性 tiebreaker**，例如 `{updatedAtMillis, sessionId}` 复合排序。在风险表中补这一条。

### M-3. 索引并发模型：刷新持写锁做文件 I/O，读者会阻塞而非"读旧快照"

风险表说「并发刷新竞争 → refresh 持锁，其他请求读旧索引快照」。但**提议的 `sync.RWMutex` 设计（A.1）和现有 Codex 缓存都做不到这点**：

- 现有 [list.go:55-56](../agent/codex/list.go#L55-L56) `c.mu.Lock(); defer c.mu.Unlock()` 在**整个 walk + 解析**期间持写锁；
- 提议的 `SessionIndex.refreshSessions`（A.2）要在持锁状态下 stat 每个文件、重扫变化文件。

一次慢刷新会**阻塞所有并发 list 请求**，而不是让它们读旧快照。"系统性重构"正该解决这个可扩展性缺陷。

> 修正建议：明确采用 copy-on-write：在锁外做文件 I/O 构建新快照，锁内只做指针交换；或读路径拷贝快照后立即放锁。把这个写进支柱 A 的设计，而不是留作未实现的风险对策。

### M-4. 风险对策"跟踪 size"既未进数据结构，现有缓存也没实现

风险表：「mtime 误判 → 除了 mtime 还跟踪文件 size；两者都没变才跳过」。但：

- 提议的 `SessionIndexEntry`（A.1）只有 `FileMtime`，**没有 size 字段**；
- 现有 Codex `fileEntry` 也只有 `mtime`（[list.go:42-45](../agent/codex/list.go#L42-L45)）。

 stated mitigation 在任何一处都未落地。

> 修正建议：在 A.1 加 `FileSize int64`，刷新时 `mtime.Equal && size.Equal` 才跳过；Codex 现有缓存一并补上。

### M-5. A.4 的「SSE 事件驱动刷新」对文件型后端很可能不成立

A.4 列出触发时机含「SSE 事件收到 `session.created` / `session.updated`」。但文件型后端（Claude/Codex）**不存在上游 server 推送这类事件**——bridge 自己就是被 spawn 出来的 agent 进程的事件源。没有证据表明 bridge 会发出 session 列表级事件。

> 修正建议：对文件型后端，把 30s 后台定时器从"兜底"提为**主触发**；SSE 触发仅适用于代理型后端（OpenCode），且需先确认上游 API 真有这些事件名。

### M-6. 能力检测机制其实已存在——文档另起炉灶且 register_ack 用错了

C.4 / §9 说「`hello_ack` / `register_ack` 的 `serverCapabilities` 新增 `open_session`」。但实际：

- [hello_handler.go:31](../go-bridge/hello_handler.go#L31) `HelloAckMessage.Capabilities map[string]bool`，[hello_handler.go:118-124](../go-bridge/hello_handler.go#L118-L124) 已返回 `remoteAccessConfig`/`trustedDevices`/`workspaceList`/`sessionMutation` 等键——**能力地图已存在**，加一个 `"openSession": true` 即可，无需新造 `serverCapabilities`；
- [types.go:33-40 RegisterAck](../go-bridge/types.go#L33-L40) **根本没有 capabilities 字段**（只有 Ok/Protocol/Backends/BridgeEpoch/Error）。文档说"register_ack 新增"是错的；
- 命名也不一致：现有键是 camelCase（`sessionMutation`），文档的 `open_session` 是 snake_case。

另外，`open_session` 本质是**按后端**有效（OpenCode 不支持），更该走 [agent_descriptor.go:88 deriveCapabilities](../go-bridge/agent_descriptor.go#L88) 的**每后端能力**（如加 `core.BatchOpenSession` 接口断言），而非 server 级布尔。

> 修正建议：复用现有每后端 `capabilities`（[agent_descriptor.go](../go-bridge/agent_descriptor.go)），新增一个接口断言（如 `BatchSessionOpener`），由 `deriveCapabilities` 产出 `"batch_open_session"`。客户端按 backend 能力判定回退，而非 server 级。

### M-7. 支柱 B 的 `limit` 参数在两条路径上都已存在——文档把它当成新增

- list：[handlers.go:2186](../go-bridge/handlers.go#L2186) 已用 `extractPositiveInt(msg, "limit")` + [handlers.go:2270 limitLatestSessions](../go-bridge/handlers.go#L2270)；
- messages：[types.go:97-101 GetSessionMessagesParams](../go-bridge/types.go#L97-L101) 已有 `Limit int`，并在 [handlers.go:2467](../go-bridge/handlers.go#L2467) 透传给 `GetRichSessionHistory`。

文档 B.1/B.2 把 `limit` 列为"新增可选"是错的。**真正新增的是 cursor / hasMore / nextCursor / oldestCursor / newestCursor**。

> 修正建议：在协议变更摘要里把 `limit` 标为"已存在"，仅声明 cursor 类字段新增，避免实施 agent 重复添加字段或与现有逻辑冲突。

---

## 4. 🟡 Minor 级问题

### m-1. `handlers.go` 行号整体过期（偏移 3–38 行）

文档第 7 节声称「让下一个 agent 不用重新定位」，但 `handlers.go` 的指针已失效：

| 文档给出 | 实际位置 | 偏移 |
| --- | --- | --- |
| `handleListSessions:2182` | [2185](../go-bridge/handlers.go#L2185) | +3 |
| `handleGetSessionMessages:2396` | [2434](../go-bridge/handlers.go#L2434) | +38 |
| `scanClaudeSessionSummary:2326` | [2359](../go-bridge/handlers.go#L2359) | +33 |
| `scanSessionsFromProjectDir:2286` | [2323](../go-bridge/handlers.go#L2323) | +37 |
| `handleResumeSession:2611` | [2649](../go-bridge/handlers.go#L2649) | +38 |

`agent/*` 行号准确（codex.go:386、claudecode.go:436/781、rich_history.go:18 均正确）。`handlers.go` 偏移与近期加入的止血日志/有界读取代码一致——文档是在这些改动之前取的行号。

> 建议：交付前用当前 HEAD 重新定位所有行号，或在文档顶部声明"行号对应 commit XXXX，使用时以符号搜索为准"。

### m-2. 验收标准与第 6 节"不做 UI 自动化"自相矛盾

第 6 节：「默认使用代码阅读、定向 build、定向 unit test 验证」。但验收标准里有「向上滚动加载更早消息 < 500ms」「打开响应时间 < 1s」「列表响应 < 200ms」——这些**端到端感知延迟 unit test 测不了**。

> 建议：要么把验收改成可单测的形态（如 handler 耗时阈值，用 [handlers.go](../go-bridge/handlers.go) 已有 `slog` 计时），要么明确标注为"手动验证目标"并给出操作步骤。

### m-3. iOS 引用无法在本仓库核对，且路径含 `...` 占位

iOS 文件在另一仓库 `opencodeIosNew`。文档用 `OpenCodeiOS/.../Services/Backend/UnifiedBridgeAdapter.swift:486` 这种带 `...` 的路径，本仓库无法验证行号，`...` 也让路径不可直接定位。

> 建议：补全真实相对路径；在文档顶部声明 iOS 行号来自另一仓库、可能漂移；关键论断（如客户端是否真的 per-directory 扇出）标注"待 iOS 侧确认"。

### m-4. 第 8 节性能基线缺测量方法

见 B2。所有数字都是断言，无方法、无设备、无计时口径。

> 建议：注明数据来源（用户体感 / 日志 / 计时），并给出重构后复测的同一口径。

### m-5. A.3「前 128 行」与现有 Codex 实现不一致

文档 A.3 说 Codex「读取前 128 行的 `response_item` 获取 title」。但现有 [list.go:180-230](../agent/codex/list.go#L180-L230) 是读 **256KB 前缀内全部行**，并取**最后一个**像真实 prompt 的 `input_text` 作为 summary（[list.go:217-224](../agent/codex/list.go#L217-L224)）。「前 128 行」既少于现有实现，取值策略（首 vs 末）也不同。

> 建议：以现有实现为准（取末个真实 prompt），或说明为何改成首 128 行。

### m-6. LRU@5000 出现在风险表却不在数据结构里，且可能无必要

风险表提「LRU 淘汰：超过 5000 条淘汰最久未访问」。但 A.1 的 `SessionIndex` 是裸 map，无 LRU；5000 条 × 小结构 ≈ 几 MB，LRU 增加复杂度却收益有限，现有 Codex 缓存也没有 LRU。

> 建议：删除该项，或给出明确的内存预算依据。

---

## 5. 已验证的代码位置对照表（供修订文档直接引用）

> 仅列经本评审核对正确的位置；`handlers.go` 行号已校正到当前 HEAD。

### Bridge（`cccode-macbridge`）

| 关注点 | 正确位置 |
| --- | --- |
| `handleListSessions`（含 claudecode 特殊分支 2189） | [handlers.go:2185](../go-bridge/handlers.go#L2185) |
| claudecode 列表热路径 `scanSessionsFromProjectDir` | [handlers.go:2323](../go-bridge/handlers.go#L2323) |
| claudecode 全量扫描 `scanClaudeSessionSummary`（含 256KB? 实为 `claudeSessionSummaryReadLimit` 有界读 2369-2372） | [handlers.go:2359](../go-bridge/handlers.go#L2359) |
| `handleGetSessionMessages`（已透传 `params.Limit` 到 rich history） | [handlers.go:2434](../go-bridge/handlers.go#L2434) |
| `handleResumeSession`（近空操作，延迟到 send_message） | [handlers.go:2649](../go-bridge/handlers.go#L2649) |
| 通用 RPC dispatch | [handlers.go:520-578](../go-bridge/handlers.go#L520-L578) |
| OpenCode(ocProxy) dispatch（list/resume/get_session 走代理） | [handlers.go:592-668](../go-bridge/handlers.go#L592-L668) |
| `GetSessionMessagesParams`（**已含 `Limit`**）/ `ResumeSessionParams` | [types.go:92-101](../go-bridge/types.go#L92-L101) |
| `RegisterAck`（**无 capabilities 字段**） | [types.go:33-40](../go-bridge/types.go#L33-L40) |
| hello_ack 能力地图 `Capabilities map[string]bool` | [hello_handler.go:31](../go-bridge/hello_handler.go#L31), [118-124](../go-bridge/hello_handler.go#L118-L124) |
| 每后端能力 `deriveCapabilities`（open_session 应挂这里） | [agent_descriptor.go:88-133](../go-bridge/agent_descriptor.go#L88-L133) |
| **Codex 已有增量索引** `sessionListCache`（4 阶段刷新 + 256KB 有界读） | [list.go:42-140](../agent/codex/list.go#L42-L140) |
| Codex `ListSessions` 已读索引 | [codex.go:386-391](../agent/codex/codex.go#L386-L391) |
| Codex 缓存已有测试 | [list_cache_test.go](../agent/codex/list_cache_test.go) |
| Codex rich history（limit 是**先全量解析再截尾**） | [rich_history.go:18, 182-186](../agent/codex/rich_history.go#L182-L186) |
| Claude `ListSessions`（**无缓存，且列表热路径不走它**） | [claudecode.go:436](../agent/claudecode/claudecode.go#L436) |
| Claude `loadClaudeRichHistory` 全量解析 | [claudecode.go:781](../agent/claudecode/claudecode.go#L781) |
| 协议方法表（需追加 `open_session`） | [docs/protocol/bridge-v1.md:74-110](../protocol/bridge-v1.md#L74-L110) |

### iOS（`opencodeIosNew`，**本仓库未核对**）

文档所列 iOS 位置无法在此验证。另注：MacBridge 本身是配对/状态/设置类伴随 App（[MacBridge/MacBridge/Views/](../MacBridge/MacBridge/Views/) 下无 ChatView/SessionView），**不含 session 列表/聊天 UI**——故方案聚焦 iOS 是对的，MacBridge 不在范围内，这一点文档可显式说明以消除歧义。

---

## 6. 缺失项

- **M1（最重要）缺乏性能测量与根因核实**。Codex 已有缓存却仍"5-15s"，必须先定位是真冷启动、mtime 失效、序列化、还是 iOS 端。否则整个"系统性重构"建在沙地上。
- **M2 未说明索引是"跨项目全局"还是"每项目"**。`handleListSessions` 在无 directory 时扫描**全部** `~/.claude/projects/`（[handlers.go:2228-2239](../go-bridge/handlers.go#L2228-L2239) 的 fan-out）。要让它快，索引必须跨项目全局；文档以 `sessionID` 为 key 未澄清这点。
- **M3 未处理 `delete_session` 时的索引失效**。否则只能靠 30s 定时器清理已删 session。
- **M4 未考虑载荷序列化体积**。即便有索引，一次返回 1000 session 的 JSON 在手机弱网下仍大。分页（B）能解，但文档没把 A（索引）与"无 cursor 首页载荷"关联起来。
- **M5 未提 OpenCode(ocProxy) 列表路径**不受本方案影响（[handlers.go:639 ocHandleListSessions](../go-bridge/handlers.go#L639)）。若 OpenCode 用户也慢，本方案不覆盖——应声明。
- **M6 Codex summary 在超 256KB 会话上会被冻结在前缀内的末个 prompt**，title 新鲜度有问题，文档 A.3 未触及。

---

## 7. 优先级修订建议（给下一个 agent）

**第 0 步（动工前，必做）**：性能实测 + 现状核对。
1. 用现有 `slog` 计时或临时埋点，量化 Codex/Claude 列表与打开的**分段耗时**（扫描 vs 解析 vs 序列化 vs 网络 vs iOS）。
2. 确认 Codex 慢是否源于 mtime 失效（加日志观察缓存命中率）。
3. 据此判定支柱 A/B/C 各自的实际收益。

**支柱 A（重写）**：
- 范围缩为「**仅 Claude 侧**补建增量索引」；Codex 侧标记为已完成并引用 [list.go](../agent/codex/list.go)。
- 接入点改为 [handlers.go `scanSessionsFromProjectDir`](../go-bridge/handlers.go#L2323)（而非 `claudecode.ListSessions`）——见 B4。
- 设计加：copy-on-write 并发（M-3）、mtime+size 双判（M-4）、跨项目全局索引（M2）、delete 失效（M3）。
- 把"SSE 触发"降级，30s 定时器为主（M-5）。

**支柱 B（保留，收窄）**：
- `limit` 标记为已存在，仅新增 cursor/hasMore 类字段（M-7）。
- list cursor 加 sessionId tiebreaker（M-2）。
- 消息分页给出**不破坏 tool_use/tool_result 分组**的具体算法与测试（M-1）；优先解决"全量解析再截尾"的真成本。

**支柱 C（降级/可选）**：
- 明确其只省往返延迟、不解决超时（B3）；是否实施取决于第 0 步实测。
- 能力检测改用每后端 `capabilities` + 接口断言（M-6），勿用 register_ack。

**文档交付前**：校正所有 `handlers.go` 行号（m-1），补 iOS 路径与"待核对"标注（m-3），统一验收口径与第 6 节约束（m-2），补性能基线测量方法（m-4）。

---

*评审完毕。总评：方案方向正确、结构清晰，但因未充分核对现有代码（尤其 Codex 已有索引、Claude 接入点错误）且缺乏实测支撑，当前版本不宜直接进入实施。按 §7 重排后可成为高质量的实施输入。*
