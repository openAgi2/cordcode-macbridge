# 方案评审报告 R2: codex-remote 会话列表冷启动超时自动重试

- **评审日期**: 2026-09-24
- **方案**: `docs/2026-09-24-codex-remote-session-list-auto-retry.md` (v2)
- **方案 SHA-256**: `90c308bfe43cc6e263461801f95680cb2d7cfe6582478712a62fc3934d3ce22f`
- **评审员模式**: fresh（新评审员独立阅读）
- **契约版本**: plan-contract-v1.1
- **复审范围**: 全量复审（v1 阻塞闭合 + v2 新增内容回归）

---

## 结论

- **verdict**: REVISION_REQUIRED
- **blockers**: 1
- **advisories**: 8
- **implementation_readiness**: Slice 1（Mac 侧重试预算）可独立实施；Slice 2（iOS 自动重试）需修订后方可开工；Slice 3（错误态 UI）依赖 Slice 2 修订

---

## 来源与覆盖

### 来源身份表

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 与方案一致性 |
|------|------|------|------|------------|-------------|
| Mac | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `e4886849600e152abed1d4f62e854ebddd1cfa80` | 4 untracked docs | 方案称 3 个，实际 4 个（含 r1 报告，见 F-5 回归） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `2934586efb4218b16c722fc5cb0562b27b2548d4` | **2 modified files** | 方案称"干净"，实际有未提交修改（见 F-12） |

**iOS 未提交修改详情**（属其他任务，与本方案无功能重叠）：
- `OpenCodeiOS/OpenCodeiOS/Views/Components/SidebarView.swift`（+129/-53，section collapse 功能，行 119-200）
- `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionListPresentationStore.swift`（+32，collapsedSectionKeys 状态）

方案引用的 iOS 锚点（SessionsView.swift、CCCodeBridgeTransport.swift、CCCodeBridgeClient.swift、BackendModels.swift）均在干净文件中，不受未提交修改影响。SidebarView.swift 的未提交修改位于行 119-200，与方案引用的 :511、:677 不重叠。

### v1 意见闭合检查

| 意见 ID | 类型 | v2 处置 | 闭合判定 | 证据 |
|---------|------|---------|----------|------|
| F-1: Mac 侧重试预算不足 | 阻塞 | 改为 30s 独立预算 | **闭合** | §4.2.1 明确 30s，依据充分（14-24s 实测 + 6s 余量 < 30s < 60s RPC timeout，client.go:68 已核） |
| F-2: bridgeReadyReload 不覆盖 recent view | 阻塞 | 新增 recentAutoRetry 机制 | **未闭合** | 机制设计有缺陷，见 F-6（阻塞） |
| F-3: "Kimi Code" 命名 | 建议 | 改为 "Kimicode" | **回归** | "Kimicode" 不存在，实际为 "Kimi"（Mac driver ID）/ KimiCode（iOS enum），见 F-13 |
| F-4: 伪代码不匹配 | 建议 | 改用真实函数签名 | **基本闭合** | §4.2.1/§4.2.2 用真实调用链描述，但 `h.currentBackendID()` 不存在（应为 `agentBackendID(agent)`），见 F-14 |
| F-5: untracked docs 数量 | 建议 | 修正为 3 个 | **回归** | 实际 4 个（含 r1 报告），见 F-15 |

### 锚点核验表（本轮亲核）

| 方案原引用 | 实际来源及位置 | 源码原文摘录 | 定位 / 语义 | 亲核/复用 |
|---|---|---|---|---|
| `catalog_native_membership.go:16` — 8s timeout | 同文件 :16 | `const catalogRequestTimeout = 8 * time.Second` | 吻合 / 吻合 | 复用 r1 |
| `catalog_recent_view.go:161` — WithTimeout | 同文件 :161 | `ctx, cancel := context.WithTimeout(h.ctx, catalogRequestTimeout)` | 吻合 / 吻合 | 复用 r1 |
| `catalog_recent_view.go:170` — backendID | 同文件 :170 | `backendID := agentBackendID(agent)` | 吻合 / 吻合 | 亲核（F-14 相关） |
| `agent/codex-appserver/rpc/client.go:67-68` — 60s timeout | 同文件 :68 | `defaultRequestTimeout = 60 * time.Second` | 吻合 / 吻合 | 亲核 |
| `SessionsView.swift:621-640` — RecentSessionPageState | 同文件 :621-640 | 字段列表与方案一致 | 吻合 / 吻合 | 亲核 |
| `SessionsView.swift:2999` — loadRecentSessionsIfNeeded | 同文件 :2999 | 函数签名一致 | 吻合 / 吻合 | 亲核 |
| `SessionsView.swift:3014` — isLoading guard | 同文件 :3014 | `guard !recentPageState.isLoading, !recentPageState.refreshInFlight else { return }` | 吻合 / **语义冲突**（F-6 阻塞） | 亲核 |
| `SessionsView.swift:3055` — errorMessage 赋值 | 同文件 :3055 | `recentPageState.errorMessage = localizedErrorDescription(error)` | 方案称 :3066，实际 :3055（11 行漂移） | 亲核 |
| `SessionsView.swift:877` — recentPageState reset | 同文件 :877 | `recentPageState = RecentSessionPageState()` | 方案称 :933+，实际 :877 | 亲核 |
| `SessionsView.swift:1054-1145` — bridgeReadyReload | 同文件 :1054-1145 | 7 delays [0, 500ms, 1s, 2s, 4s, 8s, 15s] | 吻合 / 吻合 | 复用 r1 |
| `RuntimeManager.swift:205` — drivers | 同文件 :205 | `["Kimi", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]` | 吻合 / **r1 证据有误**（F-13 相关） | 亲核 |
| `BackendModels.swift:42-65` — BackendKind | 同文件 :42-65 | 无 "Kimi" 或 "KimiCode" case | 吻合 / **r1 证据有误**（F-13 相关） | 亲核 |

### 未核范围

- 方案引用的 think.md 复盘条目（Mac 2026-09-14、iOS 2026-09-20）已复用 r1 核验
- 既有测试（Mac 3 个、iOS 3 个）已复用 r1 核验
- 方案未涉及外部协议/wire 形状变更，audit-plan 专项不启用

---

## 意见

### [F-6][阻塞] iOS 自动重试机制无法触发：isLoading guard 阻止重试进入

- **位置**: 方案 §4.2.2 节代码片段 + `SessionsView.swift:3014`
- **证据**:
  - 方案 catch 分支在自动重试路径**保持 `isLoading = true`**（"不设置 errorMessage，保持 isLoading"）
  - `loadRecentSessionsIfNeeded()` 的 :3014 行有 guard：`guard !recentPageState.isLoading, !recentPageState.refreshInFlight else { return }`
  - 重试 Task 延迟后调用 `loadRecentSessionsIfNeeded(force: true)`，但此时 `recentPageState.isLoading == true` → guard 命中 → **函数立即返回，重试从未执行**
- **影响**: 方案核心机制（recentAutoRetry）按当前规格无法实现。验收标准 1（"等待 ~25s 后直接出现列表，不显示错误态"）无法达成——首次失败后 iOS 不会自动重试，错误态仍会暴露给用户。
- **修订方向**（三选一或组合）：
  1. **重试前清除 isLoading**：在重试 Task 中调用 `loadRecentSessionsIfNeeded` 前，先设置 `recentPageState.isLoading = false`（但会导致 UI 从 spinner 闪到 empty state）
  2. **绕过 guard**：为自动重试路径添加专用入口（如 `loadRecentSessionsForAutoRetry()`）或参数（如 `force: true, allowWhileLoading: true`），跳过 :3014 的 isLoading 检查
  3. **引入专用状态标志**：新增 `isAutoRetrying: Bool` 字段，与 `isLoading` 分离；UI 检查 `isLoading || isAutoRetrying` 显示 spinner，guard 只检查 `isLoading`
- **闭合标准**: 方案明确说明重试路径如何通过 :3014 guard，且 UI 状态（spinner 持续显示）与代码逻辑一致

### [F-7][建议] 代码片段值语义 bug：autoRetryTask 未存入 recentPageState

- **位置**: 方案 §4.2.2 节代码片段
- **证据**:
  ```swift
  state.autoRetryAttempt = attempt
  recentPageState = state        // ← 先存入
  let delayNanoseconds: UInt64 = UInt64(attempt) * 2_000_000_000
  state.autoRetryTask = Task { ... }  // ← 后修改 local state，recentPageState 未更新
  ```
  Swift struct 是值类型，`recentPageState = state` 后修改 `state.autoRetryTask` 不会影响 `recentPageState.autoRetryTask`。
- **影响**: `recentPageState.autoRetryTask` 永远为 nil → scope 切换时的 `autoRetryTask?.cancel()` 无效 → 旧 scope 的重试 Task 成为孤儿继续运行；Slice 3 的 `recentPageState.autoRetryTask != nil` 判断永远为 false → "正在重试…" UI 不显示。
- **修订方向**: 调整顺序：先创建 Task 并赋值给 `state.autoRetryTask`，再 `recentPageState = state`。
- **闭合标准**: 方案代码片段或描述能正确反映值语义的赋值顺序。

### [F-8][建议] scope 切换取消重试的位置错误

- **位置**: 方案 §4.2.2 节第 5 条
- **证据**:
  - 方案称 "initialize 的 scopeChanged 清理块（SessionsView.swift:933+）中，增加 recentPageState.autoRetryTask?.cancel()"
  - 实际 `recentPageState = RecentSessionPageState()` 在 :877 行，:933+ 是缓存恢复后的代码
  - 若在 :877 之后调用 `recentPageState.autoRetryTask?.cancel()`，`recentPageState` 已是新实例，`autoRetryTask` 为 nil，cancel 无效；旧实例的 Task 成为孤儿
- **影响**: scope 切换后旧 scope 的重试 Task 继续运行，延迟后调用 `loadRecentSessionsIfNeeded(force: true)` 加载新 scope 的数据（功能上无害但浪费资源，且违反方案自身设计）。
- **修订方向**: 明确在 :877 的 `recentPageState = RecentSessionPageState()` **之前**调用 `self.recentPageState.autoRetryTask?.cancel()`。
- **闭合标准**: 方案明确 cancel 调用的位置在 recentPageState 替换之前。

### [F-9][建议] 验收标准等待时间计算不一致

- **位置**: 方案 §4.2.2 节 "重试上限 3 次的依据" + §5.1.2 节
- **证据**:
  - 方案称 "3 次 × 30s Mac 预算 + 14s iOS 延迟 = 104s 总等待"
  - 实际计算：初始尝试 1 次（8s + 1.5s + 30s = 39.5s）+ iOS 重试 3 次（每次 39.5s）+ iOS 延迟 14s = **172s**
  - 若考虑 iOS client 30s timeout（见 F-10），实际为 4 × 30s + 14s = **134s**
  - 方案漏算了初始尝试，且将 Mac 预算误当作 30s（实际为 8s + 1.5s + 30s = 39.5s）
- **影响**: 验收测试按 104s 等待会误判为失败（实际错误态在 134s 或 172s 才出现）。
- **修订方向**: 更正计算为 4 次尝试 × 每次实际耗时 + 延迟，或明确说明 "104s" 是近似值并给出精确范围。
- **闭合标准**: 验收标准的等待时间与实际机制一致。

### [F-10][建议] Mac 重试最坏情况（39.5s）超过 iOS client timeout（30s）

- **位置**: 方案 §4.2.1 节 + §5.1.1 节
- **证据**:
  - Mac 侧重试流程：8s 首次 timeout + 1.5s 等待 + 30s 重试预算 = 最坏 39.5s
  - iOS client `list_sessions` 超时为 30s（CCCodeBridgeTransport.swift:97，r1 已核）
  - 若 Mac 重试在 30s 后完成（无论成功或失败），iOS client 已超时返回 `requestTimedOut`
  - 方案未讨论此交互；机制仍工作（iOS 见 requestTimedOut → 自动重试 → 命中 snapshot cache 或再次失败），但错误类型链描述不完整
- **影响**: 方案 §5.1.1 称 "等待 ~25s 后直接出现列表"，实际若 Mac 重试在 30-39.5s 完成，iOS 会先超时（30s）→ 自动重试（2s 延迟）→ 命中 cache → 列表在 ~32-42s 出现。验收标准的时间预期不准确。
- **修订方向**: 补充说明 Mac 重试与 iOS client timeout 的交互，以及成功路径的实际时间范围（23.5-33.5s 直接成功，或 30s 超时 + 2s 延迟 + cache 命中 ≈ 32-42s）。
- **闭合标准**: 方案明确描述 Mac 重试超时与 iOS client timeout 的相对关系及实际等待时间范围。

### [F-11][建议] 延迟公式与文本描述不一致

- **位置**: 方案 §4.2.2 节代码片段 + "重试上限 3 次的依据"
- **证据**:
  - 代码片段：`let delayNanoseconds: UInt64 = UInt64(attempt) * 2_000_000_000` → attempt 1=2s, 2=4s, 3=6s（等差，总 12s）
  - 文本描述："递增延迟: 2s, 4s, 8s"（等比，总 14s）
  - "3 次 × 30s Mac 预算 + 14s iOS 延迟 = 104s" 使用 14s
- **影响**: 实现者按公式编码会得到 2/4/6s，与文本的 2/4/8s 不符；验收计算基于 14s。
- **修订方向**: 统一为等差（公式 `attempt * 2s`，总 12s）或等比（公式 `(1 << attempt) * 1s` 或 `pow(2, attempt)`，总 14s），并更新验收计算。
- **闭合标准**: 公式、文本描述、验收计算三者一致。

### [F-12][建议] 来源清单 iOS 状态不准确

- **位置**: 方案 §1 节来源清单
- **证据**:
  - 方案称 "iOS 未提交状态=干净"
  - 实际 `git status` 显示 2 个 modified files（SidebarView.swift +129/-53，SessionListPresentationStore.swift +32）
  - 修改属其他任务（section collapse 功能），与本方案无功能重叠
- **影响**: P0 来源门要求准确记录未提交状态。虽然修改不影响本方案的锚点（SessionsView.swift 等干净），但来源清单应反映实际状态。
- **修订方向**: 更正为 "2 modified files（属其他任务，与本方案无重叠）"。
- **闭合标准**: 来源清单与实际 `git status` 一致。

### [F-13][建议] "Kimicode" 命名回归：实际 backend 为 "Kimi"

- **位置**: 方案 §5.1 节第 3 条 + §5.2 节第 3 条
- **证据**:
  - v2 采纳 r1 建议改为 "Kimicode"，但 r1 的 F-3 证据有误
  - Mac 产品 lineup（RuntimeManager.swift:205）为 `["Kimi", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]`，**无 "Kimi"**
  - iOS BackendKind enum（BackendModels.swift:42-65）无 "Kimi" 或 "KimiCode" case
  - "Kimi" 是 Kimi Code CLI 的产品名（Mac driver ID），"Kimi Desktop" 不存在（Kimi 是 CLI，非 Desktop app）
  - §5.2 第 3 条 "关闭 Kimi Desktop" 应为 "关闭 Kimi Desktop"（codex-remote 的依赖）或 "断开网络"
- **影响**: 验收标准引用不存在的 backend，测试时无法执行 "关闭 Kimi Desktop" 操作。
- **修订方向**: §5.1 第 3 条改为 "Kimi"（或 "Kimi Code"）；§5.2 第 3 条改为 "关闭 Kimi Desktop" 或 "断开网络"。
- **闭合标准**: 验收标准中引用的 backend 名称与代码一致，测试操作可执行。

### [F-14][建议] `h.currentBackendID()` 不存在

- **位置**: 方案 §4.2.1 节 "改动" 第 1 条
- **证据**:
  - 方案称 "当 `h.currentBackendID() == "codex-remote"`"
  - 实际代码（catalog_recent_view.go:170）为 `backendID := agentBackendID(agent)`
  - `h.currentBackendID()` 方法在 go-bridge 中不存在
- **影响**: 实现者按方案编码会编译失败。
- **修订方向**: 改为 "当 `agentBackendID(agent) == "codex-remote"`" 或 "当局部变量 `backendID == "codex-remote"`"。
- **闭合标准**: 方案引用的函数/方法名与实际代码一致。

### [F-15][建议] untracked docs 数量回归：实际 4 个

- **位置**: 方案 §1 节来源清单
- **证据**:
  - 方案称 "3 个 untracked docs（属前序任务，本任务不动）"
  - 实际 `git status` 显示 4 个 untracked files（含本方案文档、r1 评审报告、2 个 zcode-plan-agents docs）
  - r1 报告（19:26）早于 v2（19:37）生成，v2 应计入
- **影响**: 轻微，不影响方案正确性，但来源清单应准确。
- **修订方向**: 更正为 "4 个 untracked docs（2 个属前序任务，2 个属本任务：方案 + r1 报告）"。
- **闭合标准**: 来源清单与实际 `git status` 一致。

---

## 复审处置与剩余门

### 仍待证据/OD/授权列表

- 无 pending 证据门（方案未声明待捕项）
- 无 pending OD（方案未列未决事项）

### 阻塞意见列表

| F-ID | 严重性 | 标题 | 影响切片 |
|------|--------|------|----------|
| F-6 | 阻塞 | iOS 自动重试机制无法触发：isLoading guard 阻止重试进入 | Slice 2 |

### 建议意见列表

| F-ID | 严重性 | 标题 | 影响切片 |
|------|--------|------|----------|
| F-7 | 建议 | 代码片段值语义 bug：autoRetryTask 未存入 recentPageState | Slice 2 |
| F-8 | 建议 | scope 切换取消重试的位置错误 | Slice 2 |
| F-9 | 建议 | 验收标准等待时间计算不一致 | 验收标准 |
| F-10 | 建议 | Mac 重试最坏情况超过 iOS client timeout | 验收标准 |
| F-11 | 建议 | 延迟公式与文本描述不一致 | Slice 2 |
| F-12 | 建议 | 来源清单 iOS 状态不准确 | 来源清单 |
| F-13 | 建议 | "Kimicode" 命名回归：实际 backend 为 "Kimi" | 验收标准 |
| F-14 | 建议 | `h.currentBackendID()` 不存在 | Slice 1 |
| F-15 | 建议 | untracked docs 数量回归：实际 4 个 | 来源清单 |

---

## 附录：关键架构发现

1. **Single-flight 机制**：`catalog_wire_snapshot.go` 的 `inFlight map[catalogWireScope]*catalogWireBuild` 提供 page-0 singleflight。当 iOS 在 Mac 重试期间（30-39.5s）发起重试，新请求会等待同一 in-flight build，不会触发重复 fetch。此机制对方案有利（避免并发 RPC），但方案未提及。

2. **iOS client timeout 与 Mac 重试的交互**：Mac 重试最坏 39.5s > iOS client 30s timeout。成功路径：若 Mac 在 30s 内完成，iOS 直接收到结果；若在 30-39.5s 完成，iOS 先超时 → 自动重试 → 命中 snapshot cache → 快速返回。失败路径：iOS 见 `requestTimedOut`（已在 isBridgeReadyRetryableError 白名单）→ 自动重试。机制健壮，但方案描述不完整。

3. **r1 F-3 证据错误**：r1 称产品 lineup 为 `["Kimi", ...]`，实际为 `["Kimi", ...]`。"Kimi" 是 Kimi Code CLI 的产品名。v2 采纳 r1 建议改为 "Kimicode" 是回归。

4. **RecentSessionPageState reset 位置**：`recentPageState = RecentSessionPageState()` 在 :877（scopeChanged 清理块），非方案所称 :933+。cancel 必须在 reset 之前调用。
