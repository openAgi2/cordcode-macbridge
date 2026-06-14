# Session Loading 系统性修复实施方案

> 日期：2026-06-13  
> 状态：终审通过，可进入 Phase 0；Phase 1-3 仍受 Phase 0 门禁约束  
> 评审依据：`docs/2026-06-13-session-loading-review.md`、`docs/2026-06-13-session-loading-review-r2.md`、`docs/2026-06-13-session-loading-review-r3.md`  
> Bridge 基准 commit：`04f4615`；工作区另有未提交改动，定位时以符号搜索为准  
> 涉及仓库：`cccode-macbridge`（Bridge/后端，权威实现）、`cccode-ios`（iOS App，拆仓后正牌仓库）
>
> ⚠️ 仓库校准（2026-06-13）：本文档 iOS 部分最初按拆仓前的 `/Users/jacklee/Projects/opencodeIosNew`（`UnifiedBridge*` 架构）编写。2026-06-01 仓库拆分（审核 `proved-complete`）后，iOS 正牌仓库为 `/Users/jacklee/Projects/cccode-ios`（`CCCodeBridge*` 架构），`opencodeIosNew` 已废弃。§2.2 / §4.3 / §6.8 / §12 的 iOS 路径已据此校准。iOS 章节的**功能需求与约束仍然有效**；过期的只是文件路径与架构命名。close-1009 根因（后端单帧过大）与 iOS 仓库无关，Phase 0/1 与 Phase 2 后端工作不受影响。

## 1. 结论

当前问题不能继续依靠增加 hard limit 或减少一两个 RPC 来处理。系统性修复的主线应当是：

1. 先用分段计时确认耗时和闪退发生在 Bridge 扫描、历史解析、序列化、传输、iOS 解码还是 UI 应用阶段。
2. 保留并审计已经存在的 Codex 列表增量缓存，只为 Claude Code 的真实列表热路径补建跨项目索引。
3. 为历史建立不破坏逻辑消息边界的 transcript 索引，再实现 cursor 分页，避免“全量解析后截尾”。
4. `open_session` 仅用于减少已被测量证明有价值的往返，不作为打开超时的主要解法。

原方案的方向仍然成立，但实施顺序和部分设计已经按评审报告纠正。

## 2. 当前事实

### 2.1 已确认的 Bridge 状态

- Codex 已有增量列表缓存：
  - `agent/codex/list.go` 中的 `sessionListCache`
  - 四阶段刷新：walk、差异检测、变化文件重解析、排序快照重建
  - 单文件列表摘要最多读取 256KB
  - `agent/codex/codex.go` 的 `ListSessions` 已接入
  - `agent/codex/list_cache_test.go` 已覆盖主要缓存行为
- Claude Code 的 Bridge 列表热路径不走 `claudecode.Agent.ListSessions`，而是：
  - `go-bridge/handlers.go` 的 `handleListSessions`
  - `scanSessionsFromProjectDir`
  - `scanClaudeSessionSummary`
- `list_sessions.limit` 已存在并在 handler 中生效。
- `GetSessionMessagesParams.Limit` 已存在并透传给 rich history provider。
- Codex 和 Claude 的历史 limit 仍是“完整解析后截取最后 N 条”，没有限制解析成本。
- `resume_session` 当前只做订阅、目录解析和返回，不启动 agent 进程；iOS 的 Codex/Claude 打开路径也已跳过该调用。
- `RegisterAck` 没有 capabilities 字段；每后端能力由 `deriveCapabilities` 产生。

### 2.2 已确认的 iOS 状态

以下位置在正牌仓库 `/Users/jacklee/Projects/cccode-ios`（`CCCodeBridge*` 架构）核对：

| 关注点 | 位置 |
| --- | --- |
| Bridge 注册/传输 | `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift`；连接生命周期见 `CCCodeBridgeBackendClient.swift` |
| Session 列表/历史请求 | `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift`（`fetchSessions` / `fetchMessages`），底层 RPC 在 `CCCodeBridgeClient.swift` |
| 能力（capability）来源 | `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeModels.swift`（`CCCodeAgentDescriptor.capabilities`），由 `BridgeProvider` 从 `hello_ack` 注入 |
| Session 列表加载 | `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift` |
| 打开会话初始化 | `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift` |
| 历史加载与应用 | `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift`，`loadMessages` |
| 消息渲染（web-based） | `OpenCodeiOS/OpenCodeiOS/App/MessageWeb/`（WKWebView），容器 `App/ChatUIKitContainerView.swift` |

行号容易随当前未提交改动漂移，因此本方案不把 iOS 行号作为稳定定位依据。

### 2.3 仍未确认的根因

用户观测到：

- Codex / Claude Code 列表加载经常超过 10 秒。
- Codex 会话打开经常超时或空白。
- iOS 偶发闪退。
- 即使处于局域网，连接质量仍然较差。

这些是现象，不是已测量的性能基线。尤其 Codex 已有缓存，因此不得继续假设列表扫描就是唯一瓶颈。可能原因包括：

- Codex 冷启动首扫；
- rollout mtime 持续变化导致缓存反复失效；
- Bridge JSON 序列化或 Relay 帧传输；
- iOS WebSocket 排队、解码、模型映射或主线程应用；
- 实际连接走 Relay 而非 LAN direct；
- 超大历史全量解析、全量解码或渲染导致内存峰值和闪退。

## 3. 设计原则

1. **先测量后重构**：任何优化必须对应已量化的阶段耗时或资源峰值。
2. **有界工作量**：不仅限制返回条数，还要限制磁盘读取、JSON 解析和内存驻留。
3. **逻辑消息完整**：分页不能拆开 assistant、reasoning、tool_use 和 tool_result 的组合关系。
4. **稳定分页**：cursor 必须有确定性排序和 tie-breaker，不能只依赖时间戳。
5. **真实热路径接入**：Claude 索引必须接到 handlers 的特殊分支，不能只改无人调用的 `Agent.ListSessions`。
6. **后端隔离**：文件型后端与 OpenCode 代理路径分别处理。
7. **兼容演进**：复用现有每后端 capability；旧方法保留。
8. **不制造假成功**：索引只保存定位元数据，不作为历史正文替代品。

## 4. Phase 0：性能实测与代码现状核对

Phase 0 是后续开发的强制门禁。未完成测量报告，不得开始分页或 batched RPC。

### 4.1 测量场景

每个场景至少执行冷、热各 5 次：

1. Codex `list_sessions`
2. Claude Code `list_sessions`
3. 打开小会话（少于 50 条逻辑消息）
4. 打开大 Codex 会话
5. 打开大 Claude 会话
6. direct LAN 与 Relay 各执行一组；如果当前只走 Relay，必须先记录该事实

数据集必须记录：

- session 文件数量；
- 总体积；
-最大单文件体积；
-返回 session 数量；
-返回消息数量；
-响应字节数。

### 4.2 Bridge 分段计时

为请求生成统一 trace ID，并记录：

```text
request_total_ms
enumerate_ms
stat_compare_ms
metadata_parse_ms
history_parse_ms
wire_mapping_ms
json_encode_ms
socket_send_ms
response_bytes
cache_total_files
cache_changed_files
cache_deleted_files
cache_hit
transport_route = direct | relay
```

计时应放在真实生产路径，可使用现有 `slog`；完成诊断后保留低成本指标，移除高噪声日志。

### 4.3 iOS 分段计时

使用现有 `PerformanceTracer` 或 `os_signpost` 记录：

```text
connect_or_reuse_ms
register_ms
rpc_wait_ms
websocket_receive_bytes
decode_ms
map_models_ms
main_actor_apply_ms
first_content_visible_ms
memory_before_mb
memory_peak_mb
```

同时收集：

- iOS crash report；
- memory warning；
- jetsam 记录；
- WebSocket close code；
- 当前服务器 endpoint 与 direct/relay 路由。

> 仓库校准：Phase 0 的 iOS 埋点（`SessionLoadingTracer`）当时实现于拆仓前的 `opencodeIosNew`；正牌仓库 `cccode-ios` 的 `Utilities/PerformanceTracer.swift` 当前**不含**该埋点，需在新仓库补建后才能支撑 Phase 2 的 iOS 端精细测量回归（见 exec-plan `phase0-ios-instrumentation-*` 的仓库差异说明）。

### 4.4 Codex 缓存专项核对

必须回答：

1. 冷启动首扫耗时多少？
2. 热请求是否真正命中 `sessionListCache`？
3. 哪些文件每次被判为 changed？
4. mtime 变化是否由 app-server、source patch 或其它写操作引起？
5. handler、序列化、网络和 iOS 各占多少时间？

### 4.5 Phase 0 交付物

新增：

```text
docs/2026-06-13-session-loading-baseline.md
```

报告必须包含原始命令、环境、样本规模、分段结果和 Phase 1-3 的 go/no-go 判断。

## 5. Phase 1：Claude 全局会话索引与 Codex 缓存加固

### 5.1 范围

- Claude：新建跨项目全局索引并接入 handlers 热路径。
- Codex：不重建索引，只根据 Phase 0 修补命中率、锁粒度或指纹问题。
- 不改协议。

### 5.2 Claude 索引结构

索引 key 必须包含项目身份，不能只用 session ID：

```go
type claudeSessionKey struct {
    ProjectKey string
    SessionID  string
}

type claudeSessionFingerprint struct {
    ModTimeUnixNano int64
    SizeBytes       int64
}

type claudeSessionIndexEntry struct {
    Key          claudeSessionKey
    FilePath     string
    Directory    string
    Title        string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    MessageCount int
    Fingerprint  claudeSessionFingerprint
}

type claudeSessionSnapshot struct {
    ByKey  map[claudeSessionKey]claudeSessionIndexEntry
    Sorted []claudeSessionIndexEntry
}
```

### 5.3 刷新算法

1. 枚举 `~/.claude/projects/*/*.jsonl`，获取 path、project key、mtime(ns)、size。
2. 与当前 snapshot 的 fingerprint 比较。
3. 新增或 fingerprint 改变的文件在锁外解析摘要。
4. 文件消失的 entry 从新 snapshot 删除。
5. 按 Claude 唯一稳定键 `updatedAt DESC, projectKey ASC, sessionID ASC` 排序。
6. 锁内只交换 snapshot 指针，不在写锁内做文件 I/O。

并发要求：

- 同一时刻最多一个 refresh 执行，其它 refresh 请求复用同一个 in-flight 结果。
- 已存在 snapshot 时，读者不得因另一个请求正在解析文件而持锁阻塞。
- 首次无 snapshot 时，请求等待首次构建完成。

### 5.4 热路径接入

Claude 索引必须接入：

```text
go-bridge/handlers.go
  handleListSessions
  scanSessionsFromProjectDir
```

建议由 `Handlers` 或独立 catalog service 持有全局 Claude 索引。`claudecode.Agent.ListSessions` 可以复用同一索引，但不能作为唯一接入点。

无 directory 时从全局 snapshot 返回；有 directory 时在 snapshot 上过滤 project/directory，不再重新读取对应项目全部 JSONL。

### 5.5 失效触发

主触发：

- `list_sessions` 请求时执行轻量 fingerprint 核对；
- `delete_session` 成功后立即删除或标记对应 entry dirty；
- Bridge 发起的 rename、archive、delete 成功后显式失效；
- 外部 Claude CLI 造成的新建或改动由下一次请求时的 fingerprint 核对捕获。

不默认启用每 30 秒全局扫描。后台 timer 只有在 Phase 0/Phase 1 测量证明请求时核对影响用户延迟，且产品确实要求无人访问时保持索引新鲜，才允许加入。

理由：文件型后端没有可靠的上游 session SSE；无条件 timer 会带来持续 I/O，并不能替代请求时一致性核对。

### 5.6 Codex 加固

保留现有 `sessionListCache`，按测量结果决定：

- fingerprint 从 mtime 扩展为 mtime(ns) + size；
- 文件 I/O 移出互斥锁并改为 copy-on-write snapshot；
- 记录 changed file 原因；
- 将 `sessionListCache.sorted` 的排序固定为 `ModifiedAt DESC, SessionID ASC`，消除当前 map 遍历造成的同时间戳顺序漂移；
- 调查 256KB 前缀导致 title 停留在旧 prompt 的问题。

Codex title 新鲜度与列表性能是两个独立问题，不应为了更新 title 恢复全文件扫描。

### 5.7 测试

必须新增：

- Claude 首次全局构建；
- 第二次零变化命中；
- 单文件变化只重解析该文件；
- 同 mtime 不同 size 被识别；
- 文件删除立即移除；
- 相同 session ID 位于不同 project 时不冲突；
- refresh 期间并发读不等待文件解析锁；
- directory filter 不触发重复扫描。

## 6. Phase 2：边界安全的历史索引与 cursor 分页

Phase 2 是解决大会话超时和内存峰值的主要工作。

### 6.1 当前问题

`limit` 已存在，但 Codex 和 Claude 都是：

```text
读取完整 JSONL
→ JSON 解码全部行
→ 合并成逻辑 RichHistoryEntry
→ 最后截取 N 条
```

因此 limit 限制了响应条数，却没有限制磁盘和解析成本。

### 6.2 禁止方案

不得从任意 JSONL 行号或 byte offset 直接反向截取 N 行后解析。原因：

- 一条逻辑消息可能横跨多行；
- reasoning、tool_use、tool_result 会合并进同一 entry；
- 任意切窗会生成缺失调用或缺失结果的残缺工具消息。

### 6.3 TranscriptPageIndex

为每个 session 建立只含定位元数据的索引，不缓存正文：

```go
type transcriptFingerprint struct {
    SizeBytes       int64
    ModTimeUnixNano int64
}

type logicalMessageSpan struct {
    Ordinal      int64
    StableID     string
    ReplayStart  int64
    EndOffset    int64
    OpenToolUses int
}

type transcriptPageIndex struct {
    Fingerprint      transcriptFingerprint
    ParseOffset      int64
    PrefixGeneration string
    GenerationLineage []prefixGenerationRecord
    Spans            []logicalMessageSpan
}

type prefixGenerationRecord struct {
    ID             string
    ParentID       string
    IndexedThrough int64
    AnchorHash     string
}
```

含义：

- `ReplayStart` 是能够完整重建该逻辑消息的最早 byte offset。
- `EndOffset` 是该逻辑消息包含的最后一条相关 JSONL 记录之后的位置。
- tool_result 更新此前 assistant entry 时，该 entry 的 `EndOffset` 必须扩展到 result 所在位置。
- `OpenToolUses` 表示该 entry 中尚未匹配 tool_result 的工具调用数；tool_use 增加，所有能闭合调用的 backend result 记录都必须递减。
- Codex 至少覆盖 `function_call_output`、`custom_tool_call_output` 和 `event_msg/patch_apply_end`；Claude 必须覆盖普通及 pending `tool_result` 的最终配对。
- 活跃文件追加时，从最后一个 `OpenToolUses > 0` 的 entry 的 `ReplayStart` 重放；若不存在未闭合调用，则从最后一个 entry 的 `ReplayStart` 重放，以允许尾部 assistant entry 继续合并。
- 一个 entry 的区间可能跨过后继 entry。重建一页时必须读取该页所有 span 的字节区间并集，即 `min(ReplayStart)..max(EndOffset)`，整体运行原 grouping builder，再按 ordinal/stable ID 抽取目标页；不得逐 span 独立解析。
- tool result 与 tool use 相距很远时，该并集可能较大。Phase 0/Phase 2 基线必须记录 `page_replay_bytes` 和最坏页重建耗时，再决定是否需要更细粒度 checkpoint。

索引可以持久化到 Bridge data directory，按 backend、文件路径、fingerprint 校验。持久化内容只有 offset、ID、指纹和 generation lineage，不包含消息正文，因此不是缓存快照或假数据。持久化必须采用单写者和临时文件原子替换；读取到截断、校验失败或版本不兼容的索引文件时直接丢弃并从真实 transcript 重建。

Transcript index 的构建和增量刷新必须按 session identity singleflight。相同 session 的并发 `get_session_messages` 复用同一 in-flight index task；不同 session 可以在全局 I/O/内存预算内并行。不得让两个请求同时扫描和覆盖同一个索引文件。

### 6.4 首次建索引与预热

首次遇到未索引的大文件仍需要完整扫描一次。为避免用户打开时承担全部成本：

1. list_sessions 返回后，按最近更新时间后台预热最近 N 个 session 的 transcript index；
2. 用户打开目标 session 时，该 session 的索引任务提升优先级；
3. 预热必须限并发和 I/O，不得与列表请求争用全局锁；
4. N 和并发数由 Phase 0 数据决定，不预先硬编码进协议。

预热还必须设置进程级内存和在途解析字节预算。Phase 0 需要观测预热开启前后的 RSS、峰值内存、磁盘吞吐和前台请求 P95；预热不得以制造新的内存峰值换取首开速度。

### 6.5 消息分页协议

`limit` 已存在；新增字段：

```json
{
  "sessionId": "abc",
  "directory": "/path",
  "limit": 50,
  "beforeCursor": "opaque-v1-cursor"
}
```

响应新增：

```json
{
  "messages": [],
  "oldestCursor": "opaque-v1-cursor",
  "newestCursor": "opaque-v1-cursor",
  "hasMore": true
}
```

cursor 使用版本化不透明编码，至少包含：

```text
version
session identity
logical message ordinal
prefix generation
```

#### 6.5.1 Cursor append 语义

消息 backward cursor 表示“该 ordinal 所属的已索引前缀”，不绑定整个文件的当前 mtime/size：

- 纯 tail append 只扩展索引并产生新的 `newestCursor`，旧的 `oldestCursor` 和 backward cursor 保持有效。
- generation 粒度固定为每次成功提交的索引刷新，而不是每条 JSONL append 行；同一刷新批次只产生一个 generation。
- 索引为每次已验证的 append 建立可继承的 `PrefixGeneration` 谱系；新 generation 以旧 generation 为祖先。
- append 判定至少要求文件未截短、文件身份（device/inode）未变化，并验证旧 EOF 前固定窗口的 continuity anchor；验证后只解析新增尾部及未闭合 entry 的 replay 区间。
- continuity anchor 使用固定窗口的强 128-bit 校验和，而不是逐字节临时比较。候选实现为 `xxHash3-128` 或截断 BLAKE3，最终选择由 Phase 0 微基准决定；算法名、窗口大小和 hash 值必须进入持久化索引版本。
- 文件截短、原位改写、替换或索引无法证明新内容是旧前缀的连续追加时，建立新的 generation 谱系。
- cursor 所引用的 generation 是当前谱系祖先时继续分页；不是祖先时返回 `cursor_stale`。

因此活跃会话持续输出不会使向后分页失效。只有 cursor 所引用的前缀被改写或无法验证连续性时才返回 `cursor_stale`。客户端收到该错误后重新加载第一页，不得静默拼接不同谱系的历史。

#### 6.5.2 跨重启与谱系边界

- 当前 generation 及其有界祖先谱系随 transcript index 原子持久化，Bridge 正常重启后仍可验证旧 backward cursor。
- 谱系以刷新次数计数并设置固定上限；达到上限时，把仍可证明共享同一不可变前缀的连续祖先折叠为一个 root checkpoint，祖先判断保持 O(1) 或有界 O(K)。
- root checkpoint 至少保存 `IndexedThrough`、continuity anchor 和 generation ID，不能只保留父 ID。
- cursor 早于已折叠且无法由 root checkpoint 证明的范围时返回 `cursor_stale`。
- 索引文件缺失、损坏、版本不兼容或 lineage 校验失败时，重建索引并让旧 cursor stale。这是跨异常重启的安全降级，不允许猜测祖先关系。

### 6.6 列表分页协议

`list_sessions.limit` 已存在；新增 `cursor`、`nextCursor`、`hasMore`。

列表 cursor 是 backend-specific opaque cursor。snapshot 排序与 cursor 内复合键必须逐字段同序：

```text
Claude Code:
  updatedAtMillis DESC
  projectKey ASC
  sessionID ASC

Codex:
  modifiedAtMillis DESC
  sessionID ASC
```

Claude cursor 编码三元组，Codex cursor 编码二元组。Codex 不引入不存在的 projectKey。任何 backend 都不能只编码时间戳。

### 6.7 兼容策略

- Bridge 继续接受没有 cursor 的旧请求。
- 新 iOS 仅在 backend capability 包含 `session_pagination` 时发送 cursor。
- 旧方法和旧 response 字段保留。
- 是否改变“未传 limit”的旧语义必须在 Phase 0 后单独裁决，不能在实现分页时顺带改变。

### 6.8 iOS 适配

- Session 列表保留第一页结果，接近底部时预取下一页。
- 历史接近顶部时预取上一页。
- 页面请求按 cursor 去重；同一 cursor 不允许并发重复加载。
- 新页按 message stable ID 合并，保持当前滚动锚点。
- `cursor_stale` 时清空分页链并重新加载第一页，界面显示真实加载状态。
- 解码和消息映射在后台执行，主线程只应用已转换的轻量 view state。

> 实现注记（cccode-ios）：该仓库消息 UI 为 web-based（`MessageWeb` / WKWebView），"历史接近顶部预取"需通过 JS↔Swift 桥接感知滚动位置，而非原生 SwiftUI `.onAppear`；精简落地路径见 exec-plan `phase2-ios-pagination-impl` notes（Stage 2）。`cursor` 去重、stable ID 合并、`cursor_stale` 重置等需求与本节一致。

### 6.9 边界测试

必须覆盖：

- tool_use 在页尾、tool_result 在后续 JSONL 行；
- assistant text + reasoning + 多个工具调用；
- Claude pending tool result 先于 owner 被解析；
- Codex patch result 更新已有调用；
- 页间无重复、无缺失；
- 纯 tail append 后旧 backward cursor 继续有效；
- 文件截短、替换或已索引前缀改写后旧 cursor 返回 `cursor_stale`；
- continuity anchor 验证失败时不得把变更误判为 append；
- continuity anchor 算法或窗口版本变化时旧索引安全失效；
- page span 重叠时按区间并集 replay，tool result 不丢失；
- 未闭合 tool_use 跨 append 后可从正确的 `ReplayStart` 重建；
- Codex 三种结果记录都能闭合对应 `OpenToolUses`；
- 同 session 并发索引请求只执行一次扫描，不同 session 受全局预算约束；
- Bridge 重启后持久化 lineage 内的旧 backward cursor 继续有效；
- lineage 折叠后 root checkpoint 可证明范围内的 cursor 继续有效，范围外明确 stale；
- 500+ 逻辑消息首次只解码一页正文；
- response bytes 和峰值内存受限。

## 7. Phase 3：可选的 batched open_session

### 7.1 定位

`open_session` 不是大会话超时的主要解法。它只减少目录解析、订阅和历史请求之间的往返与重复查找。

只有 Phase 0 显示打开链路中的额外 RPC 或目录探索占据显著时间，才实施本阶段。建议门槛：

```text
非历史解析/传输的串行前置耗时 > 打开总耗时的 15%
或 > 150ms
```

### 7.2 能力检测

不得修改 `RegisterAck` 添加 server capabilities。

新增 core 可选接口，例如：

```go
type BatchSessionOpener interface {
    OpenSession(ctx context.Context, request OpenSessionRequest) (*OpenSessionResult, error)
}
```

`deriveCapabilities` 根据接口断言为对应 backend 添加：

```text
batch_open_session
```

iOS 按当前 backend descriptor 的 capability 决定是否调用，不能使用 server 级全局布尔。

### 7.3 RPC 范围

如果实施，单个 RPC 可以组合：

- session metadata / directory；
- connection subscription；
- 第一页历史及 cursor。

它必须复用 Phase 2 的分页读取，不能另写一条全量历史解析路径。

OpenCode `ocProxy` 是否支持该能力单独评估；不支持时不声明 capability。

## 8. 实施顺序与门禁

### Phase 0：测量

门禁：

- 有可复现的分段基线；
- 已确认 direct/relay 路由；
- 已确认闪退是 crash、jetsam 还是 UI 状态错误；
- 对 A/B/C 分别给出 go/no-go。

### Phase 1：列表索引

默认 go：

- Claude 全局索引接入真实 handlers 热路径。

条件 go：

- Codex 缓存加固项目由 Phase 0 数据决定。

### Phase 2：历史索引与分页

默认 go：

- 只要大会话仍存在全量解析和内存峰值，即进入本阶段。

门禁：

- 逻辑消息边界模型先通过测试；
- cursor 稳定性和失效语义冻结；
- Bridge 与 iOS 协议样例更新。

### Phase 3：batched open

默认 no-go，达到第 7.1 节阈值后才实施。

## 9. 验收方法

### 9.1 自动化

默认运行：

- Go 单元测试；
- Bridge handler 协议测试；
- Swift 定向 unit test；
- macOS 和 iOS 定向 build；
- JSON fixture / cursor compatibility test。

不主动运行 UI test、snapshot test 或 simulator automation。

### 9.2 手工真机性能验证

端到端目标由手工真机测试验证，不冒充 unit test：

1. direct LAN 和 Relay 分别测试；
2. 每个场景冷、热各 5 次；
3. 使用 Phase 0 相同数据集和计时口径；
4. 保存 Bridge 日志、iOS signpost、响应字节和内存峰值。

性能目标必须在 Phase 0 基线生成后写入 baseline 文档。当前用户体感“5-15 秒”只作为问题描述，不作为可审计基线。

## 10. 范围边界

- 本方案不修改 OpenCode 上游 API；`ocProxy` 的列表和历史能力单独评估。
- Mac SwiftUI 伴随 App 不包含 session/chat UI，不是本次列表和历史加载改造对象。
- 不引入正文快照代替真实历史。
- 不以 timeout 重试掩盖 parser、transport 或 cursor 错误。
- 不默认加入周期性全盘扫描。
- 不因预计存在 5000 个 session 就实现 LRU；当前元数据规模不足以证明其收益。
- 不改 Bridge protocol major version；新增字段和方法保持兼容。

## 11. 评审意见处置

### 11.1 Blocker

| ID | 处置 | 说明 |
| --- | --- | --- |
| B1 Codex 索引已存在 | 采纳 | Phase 1 改为 Claude 为主，Codex 仅测量和加固。 |
| B2 根因未经测量 | 采纳 | 新增强制 Phase 0 和分段指标。 |
| B3 resume 近空操作 | 采纳 | `open_session` 降为可选，历史分页成为打开超时主线。 |
| B4 Claude 接入点错误 | 采纳 | 索引直接接入 handlers 特殊分支。 |

### 11.2 Major

| ID | 处置 | 说明 |
| --- | --- | --- |
| M-1 反向读取破坏分组 | 采纳 | 删除反向 N 行方案，改为逻辑消息 span 索引和 tail replay。 |
| M-2 时间戳 cursor 碰撞 | 采纳 | 改为 backend-specific 复合键：Claude 使用时间/project/session，Codex 使用时间/session。 |
| M-3 写锁内 I/O | 采纳 | 使用锁外构建、锁内 snapshot 交换和 refresh singleflight。 |
| M-4 size 未进入指纹 | 采纳 | fingerprint 明确包含 mtime(ns) + size。 |
| M-5 30s timer 应为主触发 | 调整采纳 | 接受“文件型 SSE 不可靠”，但不接受无条件 timer 为主触发。请求时 fingerprint 核对和显式失效能提供一致性且避免持续 I/O；timer 仅在测量证明必要时加入。 |
| M-6 能力检测机制 | 采纳 | 使用每后端 capability + core 接口断言，不修改 RegisterAck。 |
| M-7 limit 已存在 | 采纳 | 文档仅把 cursor 和 page metadata 列为新增。 |

### 11.3 Minor

| ID | 处置 | 说明 |
| --- | --- | --- |
| m-1 行号漂移 | 采纳 | Bridge 位置按评审校正，并强调以符号搜索为准。 |
| m-2 验收与 UI 自动化冲突 | 采纳 | 自动测试与手工真机性能验证分开。 |
| m-3 iOS 引用不可核对 | 采纳 | 已在另一仓库核对并使用完整路径，不依赖易漂移行号。 |
| m-4 基线无测量方法 | 采纳 | Phase 0 定义环境、样本、指标和重复次数。 |
| m-5 Codex 前 128 行误述 | 采纳 | 删除该描述，以现有 256KB 前缀实现为事实基线。 |
| m-6 LRU@5000 无依据 | 采纳 | 删除 LRU 设计，除非后续内存测量证明需要。 |

### 11.4 评审缺失项

| 项目 | 处置 |
| --- | --- |
| M1 性能实测 | 纳入 Phase 0。 |
| M2 Claude 索引必须跨项目 | 索引 key 和 snapshot 明确包含 project key。 |
| M3 delete 后失效 | 纳入 Phase 1 显式失效触发。 |
| M4 响应序列化体积 | Phase 0 记录 encode/send/bytes，Phase 2 通过分页约束。 |
| M5 OpenCode 路径不覆盖 | 在范围边界明确声明。 |
| M6 Codex title 新鲜度 | 纳入 Codex 条件加固项，与性能问题分开处理。 |

### 11.5 第二轮评审

| ID | 处置 | 说明 |
| --- | --- | --- |
| 新-N1 列表排序键冲突、Codex 无 tie-breaker | 采纳 | Claude 固定为 `(updatedAt, projectKey, sessionID)`；Codex 固定为 `(modifiedAt, sessionID)`；snapshot 与 cursor 逐字段同序。 |
| 新-N2 append 导致 cursor 失效 | 采纳 | 冻结为 prefix generation 谱系；纯 tail append 继承旧 generation，backward cursor 继续有效。 |
| 新-n1 span 缺未闭合工具状态 | 采纳 | `logicalMessageSpan` 增加 `OpenToolUses`，并定义 tail replay 起点。 |
| 新-n2 span 重叠回放未定义 | 采纳 | 页面按所有 span 的字节区间并集整体 replay，再按 ordinal/stable ID 抽取。 |
| 新-n3 types.go 行号 | 采纳 | 修正为 `types.go:97-101`。 |
| 新-n4 Claude 创建措辞 | 采纳 | 区分 Bridge 发起的 mutation 与外部 CLI 写入。 |
| 新-n5 持久化与预热边界 | 采纳 | 增加单写者原子替换、损坏重建，以及预热内存/在途字节预算。 |
| R1 capability casing 自我更正 | 记录 | 每后端 capability 继续使用现有 snake_case；无需修改当前方案。 |

### 11.6 终审

终审结论：通过，可进入 Phase 0；无 Blocker、无 Major。

| 实现期澄清 | 处置 |
| --- | --- |
| continuity anchor 强校验 | 已冻结为版本化固定窗口 128-bit checksum，具体算法由 Phase 0 微基准选择。 |
| generation 跨重启行为 | 持久化有界 lineage；正常重启保持 cursor，可疑或损坏状态安全 stale。 |
| transcript index 并发 | 按 session singleflight，不同 session 受全局资源预算约束。 |
| Codex OpenToolUses 闭合类型 | 明确覆盖 `function_call_output`、`custom_tool_call_output`、`patch_apply_end`。 |
| generation 粒度和深度 | 每次索引刷新一个 generation，并使用有界 lineage 与 root checkpoint 扁平化。 |

## 12. 当前代码位置

位置对应 `04f4615` 附近工作区；实施时先用 `rg` 搜索符号。

### Bridge

| 关注点 | 位置 |
| --- | --- |
| `handleListSessions` | `go-bridge/handlers.go:2185` |
| Claude 列表热路径 | `go-bridge/handlers.go:2323` |
| Claude 摘要扫描 | `go-bridge/handlers.go:2359` |
| `handleGetSessionMessages` | `go-bridge/handlers.go:2434` |
| `handleDeleteSession` | `go-bridge/handlers.go:2630` |
| `handleResumeSession` | `go-bridge/handlers.go:2649` |
| RPC dispatch | `go-bridge/handlers.go:520` |
| OpenCode dispatch | `go-bridge/handlers.go:592` |
| message limit 参数 | `go-bridge/types.go:97-101` |
| RegisterAck | `go-bridge/types.go:33` |
| hello server capability map | `go-bridge/hello_handler.go:31` |
| backend capability 推导 | `go-bridge/agent_descriptor.go:88` |
| Codex 列表缓存 | `agent/codex/list.go:42` |
| Codex 列表接入 | `agent/codex/codex.go:386` |
| Codex 历史解析 | `agent/codex/rich_history.go:18` |
| Claude fallback ListSessions | `agent/claudecode/claudecode.go:436` |
| Claude 全量历史解析 | `agent/claudecode/claudecode.go:781` |

### iOS

正牌仓库根目录（拆仓后；原 `opencodeIosNew` 已废弃）：

```text
/Users/jacklee/Projects/cccode-ios
```

关键文件（`CCCodeBridge*` 架构）：

```text
OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeClient.swift
OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift
OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift
OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeModels.swift
OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendClient.swift
OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift
OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift
OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift
OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift
OpenCodeiOS/OpenCodeiOS/App/MessageWeb/                # web-based 消息渲染
OpenCodeiOS/OpenCodeiOSTests/CodexSeamTests.swift
```

## 13. 协议文档更新要求

Phase 2 开始实施时同步更新：

```text
docs/protocol/bridge-v1.md
docs/protocol/schema/bridge-v1.types.ts
docs/protocol/samples/
```

应记录：

- `limit` 已存在；
- `list_sessions.cursor/nextCursor/hasMore` 为新增；
- `get_session_messages.beforeCursor/oldestCursor/newestCursor/hasMore` 为新增；
- backend-specific cursor 排序、prefix generation、append 容忍和 stale 语义；
- 若 Phase 3 go，新增 `open_session` 方法及 backend capability `batch_open_session`。

在 Phase 0 未完成前，不冻结新的 cursor schema，也不开始 `open_session` 实现。
