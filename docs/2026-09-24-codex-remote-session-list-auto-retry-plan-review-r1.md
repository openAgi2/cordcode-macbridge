# 方案评审报告 R1: codex-remote 会话列表冷启动超时自动重试

- **评审日期**: 2026-09-24
- **方案**: `docs/2026-09-24-codex-remote-session-list-auto-retry.md` (v1)
- **方案 SHA-256**: `a2d51513d23a9ce799e56f710874650f6be2f068f7dbe0f8069640a437f8aae5`
- **评审员模式**: fresh（新评审员独立阅读）
- **契约版本**: plan-contract-v1.1

---

## 结论

- **verdict**: REVISION_REQUIRED
- **blockers**: 2
- **advisories**: 3
- **implementation_readiness**: Slice 2（iOS 白名单）和 Slice 3（错误态 UI）可独立实施；Slice 1（Mac 侧重试）需修订重试预算后方可开工

---

## 来源与覆盖

### 来源身份表

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 与方案一致性 |
|------|------|------|------|------------|-------------|
| Mac | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `e4886849600e152abed1d4f62e854ebddd1cfa80` | 3 untracked docs | 方案称 2 个，实际 3 个（轻微偏差，见 F-5） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `2934586efb4218b16c722fc5cb0562b27b2548d4` | 干净 | 一致 |

### 锚点核验表

| 方案原引用 | 实际来源及位置 | 源码原文摘录 | 定位 / 语义 | 亲核/复用 |
|---|---|---|---|---|
| `catalog_native_membership.go:16` — `catalogRequestTimeout = 8s` | 同文件 :16 | `const catalogRequestTimeout = 8 * time.Second` | 定位吻合 / 语义吻合 | 亲核 |
| `catalog_recent_view.go:161` — recent view WithTimeout | 同文件 :161 | `ctx, cancel := context.WithTimeout(h.ctx, catalogRequestTimeout)` | 定位吻合 / 语义吻合 | 亲核 |
| `handlers_codex_catalog.go:101` — codex workspace catalog 8s | 同文件 :101 | `ctx, cancel := context.WithTimeout(h.ctx, catalogRequestTimeout)` | 定位吻合 / 语义吻合 | 亲核 |
| `handlers_codex_catalog.go:52-58` — codex-remote 返回 false | 同文件 :52-58 | `if agent == nil \|\| agent.Name() == "codex-remote" { return false }` | 定位吻合 / 语义吻合 | 亲核 |
| `handlers.go:3931` — generic standard 无 timeout | 同文件 :3931 | `ctx := core.WithSessionLoadMetrics(h.ctx, metrics.context())` | 定位吻合 / 语义吻合（h.ctx 无 deadline） | 亲核 |
| `agent/codex-remote/catalog.go:63-113` — listThreads 分页 | 同文件 :63-113 | for 循环 + cursor 翻页 | 定位吻合 / 语义吻合 | 亲核 |
| catalogListPageSize = 100 | `catalog.go:22` | `catalogListPageSize = 100` | 定位吻合 / 语义吻合 | 亲核 |
| `agent/codex-appserver/rpc/client.go:287-289` — 错误字符串 | 同文件 :289 | `fmt.Errorf("%s: request %s canceled: %w", c.errorPrefix, method, ctx.Err())` | 定位吻合（:289） / 语义吻合 | 亲核 |
| `catalog_wire_snapshot.go:245-311` — snapshot cache | 同文件 :245-311 | FetchOrReuseContext 完整实现 | 定位吻合 / 语义吻合 | 亲核 |
| :289-294 — 失败不 commit snapshot | 同文件 :289-294 | `if err == nil { build.snapshot = ... } else { build.err = err }` | 定位吻合 / 语义吻合 | 亲核 |
| `catalog_cursor_v2.go:31` — TTL 10min | 同文件 :31 | `const catalogSnapshotTTL = 10 * time.Minute` | 定位吻合 / 语义吻合 | 亲核 |
| `SessionsView.swift:1054-1145` — bridgeReadyReload | 同文件 :1054-1145 | scheduleBridgeReadyReload + 7 delays [0, 500ms, 1s, 2s, 4s, 8s, 15s] | 定位吻合 / 语义吻合 | 亲核 |
| `SessionsView.swift:1168-1192` — isBridgeReadyRetryableError | 同文件 :1168-1192 | CCCodeBridgeError.code switch 无 list_failed | 定位吻合 / 语义吻合 | 亲核 |
| `CCCodeBridgeTransport.swift:97` — 30s timeout | 同文件 :97 | `static let defaultRequestTimeoutNanoseconds: UInt64 = 30_000_000_000` | 定位吻合 / 语义吻合 | 亲核 |
| `CCCodeBridgeTransport.swift:105-113` — list_sessions default | 同文件 :105-113 | `default: return defaultRequestTimeoutNanoseconds` | 定位吻合 / 语义吻合 | 亲核 |
| `SessionsView.swift:410-431` — errorStateView | 同文件 :410-431 | wifi.exclamationmark + "会话加载失败" + "重试" button | 定位吻合 / 语义吻合 | 亲核 |
| `SidebarView.swift:511, 518` — sentinel error | 同文件 :511, 518 | "加载失败：%@" + "重试" | 定位吻合 / 语义吻合 | 亲核 |
| `SidebarView.swift:677, 690` — project sentinel | 同文件 :677, 690 | "加载失败：%@" + "重试" | 定位吻合 / 语义吻合 | 亲核 |
| `SessionsView.swift:239-277` — 状态机 | 同文件 :239-277 | isLoading → spinner; empty+stale+error → staleEmpty; empty+error → errorState; empty → empty; else → list | 定位吻合 / 语义吻合 | 亲核 |
| `SessionsView.swift:1132-1136` — 重试耗尽 | 同文件 :1132-1136 | `self.isWaitingForBridge = false; self.isLoading = false; errorMessage = ...` | 定位吻合 / 语义吻合 | 亲核 |
| `SupportsRecentCatalog() == true` (codex-remote) | `catalog.go:128` | `func (a *Agent) SupportsRecentCatalog() bool { return true }` | 方案未显式引用但逻辑依赖 | 亲核 |
| `listWireError` → `WireError{Code: "list_failed"}` | `handlers.go:53` | `func listWireError(err error) *WireError { return wireErrorWithReconnect(err, "list_failed") }` | 方案未显式引用但逻辑依赖 | 亲核 |
| iOS `CCCodeBridgeError` 解码 | `CCCodeBridgeTransport.swift:1789-1795` | `CCCodeBridgeError(code: error.code ?? "unknown", ...)` | 确认 wire "list_failed" → iOS CCCodeBridgeError.code="list_failed" | 亲核 |

### 未核范围

- 方案引用的 think.md 复盘条目（Mac 2026-09-14、iOS 2026-09-20）已亲核存在且内容一致
- 既有测试（Mac 3 个、iOS 3 个）已验证存在
- 方案未涉及外部协议/wire 形状变更，audit-plan 专项不启用

---

## 意见

### [F-1][阻塞] Mac 侧重试预算不足：8s 重试超时 < codex-remote 数据传输时间

- **位置**: 方案 4.2.1 节伪代码 + "重试使用相同的 8s timeout"
- **证据**:
  - 方案自身取证：codex-remote 2 页分页 × 7-12s/页 = 14-24s（方案 2.2 节）
  - 伪代码：`context.WithTimeout(h.ctx, catalogRequestTimeout)` — `catalogRequestTimeout = 8s`
  - 首次请求 8s 超时后 ctx 取消，snapshot 未提交（`catalog_wire_snapshot.go:289-294`）
  - 重试使用相同 8s timeout → 即使连接已预热（省去 handshake/auth/dial ~5s），纯数据传输仍需 14-24s > 8s → 重试必然失败
- **影响**: 方案验收标准 1（"等待 ~20s 后直接出现列表，不显示错误态"）无法达成。Mac 侧重试失败后返回 list_failed，iOS 侧 recent view 无自动重试机制（`loadRecentSessionsIfNeeded` 错误写入 `recentPageState.errorMessage`，不触发 `bridgeReadyReload`），侧栏时间模式仍显示错误 + 手动重试按钮
- **修订方向**: 三种可选路径（择一或组合）：
  1. **渐进超时**：首次 8s，重试用独立 budget（如 25s）。需修改 `pageV2Context` 或在其外层包 retry 逻辑，重试 ctx 用 `context.WithTimeout(h.ctx, 25*time.Second)`
  2. **codex-remote 专属首次超时**：在 `recentHandleListSessions` 的 builder 闭包内，当 `backendID == "codex-remote"` 时用更大 timeout（如 30s）。不影响其他 backend 的 8s budget
  3. **提供证据**：如作者有实测数据证明 warm-connection 下 codex-remote 2 页分页 <8s，补充数据即可闭合
- **闭合标准**: 方案修订后重试总预算 >= codex-remote 实际数据传输时间（方案自身取 14-24s，建议 >=25s），或有实测证据证明 8s 重试足够

### [F-2][阻塞] iOS 侧 bridgeReadyReload 不覆盖 recent view 路径

- **位置**: 方案 4.2.2 节 + 方案 9 节评审重点
- **证据**:
  - `bridgeReadyReload` 由 `scheduleBridgeReadyReload` 触发（`SessionsView.swift:1054`），其调用 `initialize(config:)` → `loadSessions()`（`SessionsView.swift:933`）
  - `loadSessions()` 走 `fetchSessionPage` → `listSessionsPage`（`CCCodeBridgeClient.swift:279-293`）— **标准路径，无 `catalogView:"recent"`**
  - 侧栏时间模式走 `loadRecentSessionsIfNeeded()`（`SessionsView.swift:2999`）→ `fetchRecentSessionPage` → `listSessionsRecentPage`（`CCCodeBridgeClient.swift:298-311`）— **recent 路径，带 `catalogView:"recent"`**
  - `loadRecentSessionsIfNeeded` 的错误处理（`SessionsView.swift:3042-3056`）写入 `recentPageState.errorMessage`，**不写 `lastLoadError`**
  - `isBridgeReadyRetryableError` 检查的是 `lastLoadError`（`SessionsView.swift:1118`），而 `lastLoadError` 由 `loadSessions()` 设置（`SessionsView.swift:2391`）
  - 结论：recent view 的 `list_failed` 错误**不触发** `bridgeReadyReload`，添加 `list_failed` 到白名单对侧栏时间模式无效
- **影响**: 方案的核心问题是侧栏时间模式 recent view 的 8s 超时，但 iOS 侧修复（`list_failed` 加入白名单）只能帮助 `loadSessions()`（标准路径）的 `list_failed` 错误。侧栏时间模式的错误仍无自动重试。方案 4.2.2 节的"所有 backend 受益"声明对侧栏时间模式不成立
- **修订方向**:
  1. **补充侧栏时间模式的自动重试**：在 `loadRecentSessionsIfNeeded` 的 error handler 中，当错误为 `list_failed` 且 backend 为 codex-remote 时，自动延迟重试（如 1-2s 后重试 1 次）
  2. **或明确声明范围**：如果方案只修复主视图（项目模式）的 `list_failed` 问题，应明确说明侧栏时间模式不在本轮范围内，并登记为后续任务
- **闭合标准**: 方案明确说明 `list_failed` 白名单变更对 recent view 路径的影响（或无影响的原因），并补充 recent view 路径的自动重试方案或显式排除声明

### [F-3][建议] "Kimi Code" 不存在

- **位置**: 方案 5.1 节第 3 条
- **证据**: 当前产品 lineup（`RuntimeManager.swift:205`）为 `["Kimi", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]`。iOS 侧 `BackendKind` 枚举（`BackendModels.swift:52`）无 "Kimi Code" 变体。`grep -rn "Kimi\|kimi"` 在双端源码中均无匹配
- **影响**: 验收标准引用不存在的 backend，可能导致实施时遗漏实际 backend 或产生混淆
- **修订方向**: 替换为实际 backend 名称（如 `Kimicode`, `grokbuild`）或删除该括号
- **闭合标准**: 验收标准中引用的 backend 名称与代码一致

### [F-4][建议] 伪代码与实际代码结构不匹配

- **位置**: 方案 4.2.1 节伪代码
- **证据**:
  - 伪代码使用 `h.backendID`，实际无此字段；应使用 `agentBackendID(agent)` 或局部变量 `backendID`（`catalog_recent_view.go:170`）
  - 伪代码直接调用 `h.agent.ListSessions(ctx)`，实际代码调用 `cache.pageV2Context(ctx, scope, cursor, limit, builder)` 其中 builder 是闭包（`catalog_recent_view.go:208`），`agent.ListSessions(mctx)` 在闭包内（`:192`）
  - 重试需要包装 `pageV2Context` 调用或在其外层实现，不能直接替换 `ListSessions` 调用
- **影响**: 实施者按伪代码编码会编译失败或逻辑错误。需重新理解代码结构后设计重试位置
- **修订方向**: 修订伪代码以匹配实际代码结构，或改为文字描述重试策略（在 `pageV2Context` 外层包装 retry loop，重试时创建新 ctx 和新 builder 闭包）
- **闭合标准**: 伪代码或描述能直接映射到实际代码的调用链

### [F-5][建议] 来源清单未提交状态数量不准确

- **位置**: 方案 1 节来源清单
- **证据**: 方案称 "2 个 untracked docs"，实际 `git status` 显示 3 个 untracked docs（`2026-09-24-codex-remote-session-list-auto-retry.md`, `2026-09-24-zcode-plan-agents-acceptance.md`, `2026-09-24-zcode-plan-agents-design.md`）
- **影响**: 轻微，不影响方案正确性。但来源清单是 P0 门要求，应准确
- **修订方向**: 更正为 "3 个 untracked docs"
- **闭合标准**: 来源清单与实际 `git status` 一致

---

## 复审处置与剩余门

### 仍待证据/OD/授权列表

- 无 pending 证据门（方案未声明待捕项）
- 无 pending OD（方案未列未决事项）

### 阻塞意见列表

| F-ID | 严重性 | 标题 | 影响切片 |
|------|--------|------|----------|
| F-1 | 阻塞 | Mac 侧重试预算不足 | Slice 1 |
| F-2 | 阻塞 | iOS 侧 bridgeReadyReload 不覆盖 recent view | Slice 2 |

### 建议意见列表

| F-ID | 严重性 | 标题 | 影响切片 |
|------|--------|------|----------|
| F-3 | 建议 | "Kimi Code" 不存在 | 验收标准 |
| F-4 | 建议 | 伪代码与实际代码结构不匹配 | Slice 1 |
| F-5 | 建议 | 来源清单数量不准确 | 来源清单 |

---

## 附录：关键架构发现

评审过程中发现以下架构事实，方案未显式提及但影响设计正确性：

1. **iOS 双路径加载**：主视图（项目模式）走 `loadSessions()` → 标准路径（无 Mac 侧 timeout）；侧栏时间模式走 `loadRecentSessionsIfNeeded()` → recent 路径（Mac 侧 8s timeout）。两者错误处理路径独立，`recentPageState.errorMessage` 不触发 `bridgeReadyReload`。

2. **Wire 错误传播链**：Mac `listWireError` → `WireError{Code: "list_failed"}` → iOS `CCCodeBridgeTransport.handleResult` → `CCCodeBridgeError(code: "list_failed")` → `isBridgeReadyRetryableError` 检查。链路完整，但只覆盖 `lastLoadError`（`loadSessions` 路径），不覆盖 `recentPageState.errorMessage`。

3. **Snapshot cache 失败不提交**：`FetchOrReuseContext` 在 builder 返回 error 时不 commit snapshot（`catalog_wire_snapshot.go:289-294`），重试必须重新 fetch。这意味着 Mac 侧重试无法利用首次请求的部分进度。

4. **owner 真机重试路径**：方案称 owner 手动重试 23s 后成功。根据代码分析，23s > 8s，说明重试走的是标准路径（`loadSessions` → 无 Mac 侧 timeout），而非 recent 路径（8s timeout 会再次失败）。这进一步印证 recent 路径需要独立的重试机制。
