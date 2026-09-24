# 方案评审报告 R3: codex-remote 会话列表冷启动超时自动重试

- **评审日期**: 2026-09-24
- **方案**: `docs/2026-09-24-codex-remote-session-list-auto-retry.md` (v3)
- **方案 SHA-256**: `81706659ce7e20767c756e731dd9a4d21aff361e21de26ec90720cc66e334912`
- **评审员模式**: fresh（独立阅读后再核对 r1/r2 历史意见）
- **契约版本**: plan-contract-v1.1
- **复审范围**: 全量复审（r2 阻塞 F-6 闭合 + r2 8 条建议闭合 + v3 新增内容回归）

---

## 结论

- **verdict**: REVISION_REQUIRED
- **blockers**: 1（新发现 N-1：v3 catch 片段遗漏现有 generation/scopeKey 守卫与 cursor_stale 重建分支）
- **advisories**: 7（N-2, N-3, N-4 + F-9 部分 / F-11 部分 / F-13 未闭合 / F-15 未闭合）
- **implementation_readiness**:
  - Slice 1（Mac 侧重试预算）：可独立实施（F-14 已闭合，errors.Is 链路可复核）
  - Slice 2（iOS 自动重试）：**仍被 N-1 阻塞**；N-1 修订后即可开工
  - Slice 3（可选 UI）：依赖 Slice 2；N-3 的 UI 条件描述需一并修正

---

## 来源与覆盖

### 来源身份表

| 仓库 | 路径 | 分支 | 提交 | 未提交状态 | 与方案一致性 |
|------|------|------|------|------------|-------------|
| Mac | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `e4886849600e152abed1d4f62e854ebddd1cfa80` | 5 个 untracked docs | 方案称 4 个，实际 5 个（v3 撰写时 r2 报告已存在，见 F-15 未闭合） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `2934586efb4218b16c722fc5cb0562b27b2548d4` | 2 modified files | 与方案一致；SidebarView.swift 未提交修改位于 122-497、922-1152 行，与方案锚点 :511、:677 不重叠 |

### v2 意见闭合检查

| 意见 ID | 类型 | v3 处置 | 闭合判定 | 证据 |
|---------|------|---------|----------|------|
| F-6: iOS 自动重试无法触发 | 阻塞 | 引入 `isAutoRetrying` + guard 调整 | **闭合（但引入新缺陷 N-1）** | §4.2.2 明确 guard `!isLoading \|\| isAutoRetrying`；重试路径可进入；但 catch 片段遗漏 generation/scopeKey 守卫与 cursor_stale 分支 |
| F-7: autoRetryTask 值语义 bug | 建议 | 调整赋值顺序（先 Task → 再 state） | **闭合** | §4.2.2 片段：`let retryTask = Task {...}` → `newState.autoRetryTask = retryTask` → `recentPageState = newState` |
| F-8: scope 切换取消位置 | 建议 | 明确在 reset 前调用 cancel | **闭合** | §4.2.2 第 6 条：`recentPageState.autoRetryTask?.cancel()` 在 :877 的 reset 之前 |
| F-9: 等待时间计算 | 建议 | 更正计算明细 | **部分闭合** | §5.1.2 明细 `4 × 39.5s + 12s` 正确，但标题写 `≈ 162s` 与自身明细（170s）矛盾 |
| F-10: Mac 重试 vs iOS client 30s | 建议 | 补充交互表格 | **闭合** | §4.2.1 交互表覆盖四种场景，机制描述正确 |
| F-11: 延迟公式 | 建议 | 统一为等差 | **部分闭合** | §4.2.2 与 §5.1.2 一致（2/4/6s, 12s）；但 §6 Slice 2 与 §7 风险表仍写 `2s/4s/8s = 14s` |
| F-12: iOS 未提交状态 | 建议 | 更正为 2 modified files | **闭合** | 与 `git status` 一致；SidebarView 修改与方案锚点不重叠 |
| F-13: "Kimicode" 命名 | 建议 | 改为 "Kimi" | **未闭合（r2 自身证据有误）** | 实际 drivers 列表（`RuntimeManager.swift:205`）为 `["claude", "codex-remote", ...]`，**无 "Kimi"**；§5.2.3 "关闭 Kimi Code CLI 的 Remote Control relay" 引用不存在产品 |
| F-14: `h.currentBackendID()` | 建议 | 改为 `agentBackendID(agent)` | **闭合** | §4.2.1 引用 `agentBackendID(agent)`，与 `catalog_recent_view.go:170` 一致 |
| F-15: untracked docs 数量 | 建议 | 修正为 4 个 | **未闭合** | v3 撰写时（19:59）r2 报告（19:48）已存在；实际 5 个（2 前序任务 zcode-plan-agents-* + 3 本任务：方案 + r1 + r2） |

### 锚点核验表（本轮新增/复核）

| 方案原引用 | 实际来源 | 源码原文摘录 | 定位 / 语义 | 亲核/复用 |
|---|---|---|---|---|
| `catalog_recent_view.go:161` WithTimeout | 同文件 :161 | `ctx, cancel := context.WithTimeout(h.ctx, catalogRequestTimeout)` | 吻合 / 吻合 | 复用 r1/r2 |
| `catalog_recent_view.go:170` backendID | 同文件 :170 | `backendID := agentBackendID(agent)` | 吻合 / 吻合 | 亲核 |
| `agent/codex-appserver/rpc/client.go:289` %w wrap | 同文件 :289 | `fmt.Errorf("%s: request %s canceled: %w", ..., ctx.Err())` | 吻合 / 吻合 | 复用 r1（errors.Is 链路成立，Slice 1 可行） |
| `catalog_wire_snapshot.go:332` pageV2Context 错误返回 | 同文件 :332-337 | `if err != nil { return nil, nil, err }` | 吻合 / 吻合（raw return，errors.Is 链路保持） | 亲核 |
| `SessionsView.swift:2999` 函数签名 | 同文件 :2999 | `func loadRecentSessionsIfNeeded(force: Bool = false) async` | 吻合 / 吻合 | 亲核 |
| `SessionsView.swift:3014` guard | 同文件 :3014 | `guard !recentPageState.isLoading, !recentPageState.refreshInFlight else { return }` | 吻合 / 吻合 | 复用 r2（F-6 阻塞根因） |
| `SessionsView.swift:3046` catch generation guard | 同文件 :3046 | `guard recentPageState.generation == generation, recentPageState.scopeKey == scope else { return }` | **v3 片段遗漏** | 亲核 |
| `SessionsView.swift:3048-3053` cursor_stale 重建 | 同文件 :3048-3053 | `if let bridgeError = error as? CCCodeBridgeError, bridgeError.code == "cursor_stale", !recentPageState.didRebuildAfterStale { ... }` | **v3 片段遗漏** | 亲核 |
| `SessionsView.swift:3054-3055` 错误尾 | 同文件 :3054-3055 | `recentPageState.isLoading = false; recentPageState.errorMessage = localizedErrorDescription(error)` | 吻合 / 吻合 | 复用 r2 |
| `SessionsView.swift:877` reset | 同文件 :877 | `recentPageState = RecentSessionPageState()` | 吻合 / 吻合 | 复用 r2 |
| `SessionsView.swift:621-640` struct | 同文件 :621-640 | `isLoading, errorMessage, refreshInFlight, ...` | 吻合 / 吻合 | 复用 r2（新增字段 isAutoRetrying/autoRetryAttempt/autoRetryTask 待实施） |
| `SidebarView.swift:521` sentinel spinner | 同文件 :521 | `if state.refreshInFlight \|\| state.isLoading { ProgressView ... }` | 吻合 / 吻合（isLoading=true 期间已显示 spinner，无需改动 sentinel 条件） | 亲核 |
| `RuntimeManager.swift:205` drivers | 同文件 :205 | `drivers: [String] = ["claude", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]` | 吻合 / **r1/r2 证据均有误**（首元素为 "claude"，非 "Kimi"） | 亲核 |
| `catalog_wire_snapshot.go:245` singleflight | 同文件 :245-311 | `inFlight map[catalogWireScope]*catalogWireBuild` | 吻合 / 吻合（Mac 侧 page-0 单飞，对 iOS 重试有利） | 复用 r2 |

### 未核范围

- 方案引用的 think.md 复盘条目（Mac 2026-09-14、iOS 2026-09-20）复用 r1 核验
- 既有测试（Mac 3 个、iOS 3 个）复用 r1 核验
- CCCodeBridgeTransport 30s timeout（`CCCodeBridgeTransport.swift:97`）复用 r1
- 方案未涉及外部协议/wire 形状变更，audit-plan 专项不启用
- iOS 未提交修改（SidebarView.swift +129/-53，SessionListPresentationStore.swift +32）经 diff hunk 范围核对，与方案锚点 :511、:677 不重叠（复用 r2 结论）

---

## 意见

### [N-1][阻塞] v3 catch 片段遗漏现有 generation/scopeKey 守卫与 cursor_stale 重建分支

- **位置**: 方案 §4.2.2 第 3 条（catch 分支代码片段）
- **证据**:
  - 现有 catch 完整结构（`SessionsView.swift:3044-3056`）：
    ```swift
    } catch {
        guard recentPageState.generation == generation, recentPageState.scopeKey == scope else { return }
        if let bridgeError = error as? CCCodeBridgeError, bridgeError.code == "cursor_stale",
           !recentPageState.didRebuildAfterStale {
            recentPageState.isLoading = false
            recentPageState.nextCursor = nil
            recentPageState.sessions = []
            recentPageState.hasMore = true
            recentPageState.didRebuildAfterStale = true
            await loadRecentSessionsIfNeeded(force: true)
            return
        }
        recentPageState.isLoading = false
        recentPageState.errorMessage = localizedErrorDescription(error)
    }
    ```
  - v3 片段（§4.2.2 第 3 条）：
    ```swift
    } catch {
        if isRecentViewRetryableError(error), recentPageState.autoRetryAttempt < 3 {
            ... retry ...
            return
        }
        // 重试耗尽或不可重试
        ...
    }
    ```
  - 片段呈现为完整 catch 体（含自己的"重试耗尽"尾部，与现有 `isLoading=false; errorMessage=...` 尾部重复），但**遗漏**：
    - `guard recentPageState.generation == generation, recentPageState.scopeKey == scope else { return }`
    - `cursor_stale` 重建分支（`bridgeError.code == "cursor_stale", !didRebuildAfterStale`）
- **影响**:
  1. **scope-leak 回归**：遗漏 generation/scopeKey 守卫意味着迟到的旧 scope 失败会写入当前 scope 的 `recentPageState`（`newState = recentPageState; newState.errorMessage = ...`），污染新 scope 状态。方案自身 §2.4 引用 iOS think.md 2026-09-20 条目："切 backend 后 session 列表残留旧 backend 错误 — 已修（scope 泄漏）"。v3 片段恰好重新引入该 bug 类。
  2. **cursor_stale 恢复丢失**：`cursor_stale` 不在 `isRecentViewRetryableError` 白名单（`isBridgeReadyRetryableError + list_failed`），按 v3 片段会落入"重试耗尽"分支直接显示错误，cursor_stale 的 page-0 单次重建能力被删除。这是 §4.3 会话列表 parity 契约的既有行为（代码注释明确标注）。
  3. **回归测试覆盖不足**：方案 §5.2 列出的 iOS 回归测试未显式覆盖 cursor_stale；按片段实施后，现有回归测试可能无法捕获该功能丢失。
- **修订方向**:
  - 在 §4.2.2 第 3 条明确：retry 判断**插入**到现有 generation/scopeKey 守卫与 cursor_stale 分支之后（即保留两者）；或给出完整新片段包含全部三个分支（guard → cursor_stale → retryable check → 错误尾）
  - 明确 `cursor_stale` 与 `isRecentViewRetryableError` 的优先级（cursor_stale 先处理，因其有独立重建逻辑；retry 不覆盖 cursor_stale）
- **闭合标准**: 方案 §4.2.2 第 3 条的 catch 片段或文字描述明确保留 generation/scopeKey 守卫与 cursor_stale 分支，且 retry 判断的插入位置与两者不冲突

### [N-2][建议] 新 guard 在重试期间对所有调用者开放，弱化单请求所有权

- **位置**: 方案 §4.2.2 第 2 条 guard 调整
- **证据**:
  - 新 guard：`guard !recentPageState.refreshInFlight else { return }` / `guard !recentPageState.isLoading || recentPageState.isAutoRetrying else { return }`
  - 重试期间（Task 睡眠 + 重试加载全程）：`isLoading=true, isAutoRetrying=true` → 任何调用者（load-more sentinel、view reappear、sessions_changed 触发的 load）都通过 guard
- **影响**:
  - 现有 guard 的"单请求所有权"（代码注释 §4.3）在重试期间失效
  - 并发 cursor-chain 加载：dedupe 与 generation 守卫限制损害，但可能双计 `autoRetryAttempt`、覆盖 `autoRetryTask` 而不 cancel（孤儿 Task 延迟后触发额外加载）
- **修订方向**: 使用专用入口绕过 guard，例如 `loadRecentSessionsIfNeeded(force: true, isAutoRetry: true)` 参数化；retry Task 用 `isAutoRetry=true` 调用，其他调用者保持原单飞语义；`isAutoRetrying` 状态标志保留用于 UI 判断
- **闭合标准**: 方案明确说明重试 Task 之外的调用者在重试期间仍被 guard 阻挡

### [N-3][建议] item 7 与 §4.2.3 / Slice 3 的 UI 条件调整对象错误

- **位置**: 方案 §4.2.2 第 7 条 + §4.2.3 + §6 Slice 3
- **证据**:
  - 方案称"errorStateView 的检查条件从 isLoading 改为 isLoading || isAutoRetrying；侧栏 sentinel 同理"
  - 实际：
    - `errorStateView`（`SessionsView.swift:248-270`）由主列表状态机驱动，依赖 `viewModel.isLoading` / `viewModel.errorMessage`（标准路径），与 `recentPageState` 无关
    - 侧栏 recent sentinel（`SidebarView.swift:521`）条件 `refreshInFlight || isLoading`；方案保持 `isLoading=true` 贯穿重试，sentinel 已显示 spinner，无需改动
  - Slice 3 的触发条件 `recentPageState.autoRetryTask != nil` 与 §4.2.3 的 `isAutoRetrying == true` 不一致（虽在方案设计中二者同时为真，但表述不统一）
- **影响**: 实施者可能误改主列表 errorStateView 条件，引入与 recent 路径无关的状态耦合
- **修订方向**: item 7 改为"无需改动"或明确仅 sentinel 已满足（isLoading=true 自动生效）；Slice 3 统一用 `isAutoRetrying` 作为触发条件
- **闭合标准**: UI 条件调整的对象与真实代码组件对应，无跨状态机误耦合

### [N-4][建议] Slice 2 字段列表遗漏 isAutoRetrying

- **位置**: 方案 §6 Slice 2 改动
- **证据**:
  - §4.2.2 第 1 条新增 3 个字段：`isAutoRetrying`、`autoRetryAttempt`、`autoRetryTask`
  - §6 Slice 2 仅列 `autoRetryAttempt`、`autoRetryTask`
- **修订方向**: Slice 2 字段列表补齐 `isAutoRetrying`
- **闭合标准**: Slice 2 字段列表与 §4.2.2 一致

### [F-9 部分] §5.1.2 标题数字与明细矛盾

- **位置**: 方案 §5.1.2
- **证据**: 明细 `39.5 × 4 + 12 = 158 + 12 = 170s`；标题 `≈ 162s（近似值）`。170 与 162 差距 8s，非合理近似
- **修订方向**: 标题改为 `~170s` 或 `~160-175s`
- **闭合标准**: 标题与明细一致

### [F-11 部分] §6 Slice 2 与 §7 风险表仍写 2s/4s/8s

- **位置**: 方案 §6 Slice 2 改动 + §7 风险与缓解表
- **证据**:
  - §6 Slice 2："递增延迟 2s/4s/8s"
  - §7："3 次上限（2s + 4s + 8s = 14s 延迟）"
  - §4.2.2 与 §5.1.2 已统一为等差 2s/4s/6s（12s）
- **影响**: 实施者按 Slice 2 编码会得到 2/4/8s（等比），与 §4.2.2 公式 `attempt * 2s`（等差 2/4/6s）冲突
- **修订方向**: §6 Slice 2 与 §7 同步改为 2s/4s/6s（12s）
- **闭合标准**: 公式、文本描述、验收计算、Slice 描述、风险表五者一致

### [F-13 未闭合] "Kimi" backend 不存在；drivers 列表首元素实际为 "claude"

- **位置**: 方案 §5.1 第 3 条 + §5.2 第 3 条
- **证据**:
  - v3 将 §5.1 第 3 条改为 "其他 backend（opencode-web, dsh-web, Kimi）"
  - v3 §5.2 第 3 条："关闭 Kimi Code CLI 的 Remote Control relay（或断开网络）"
  - 实际 drivers 列表（`RuntimeManager.swift:205`）：`["claude", "codex-remote", "grokbuild", "dsh-web", "opencode-web"]`
  - Mac 仓 `agent/` 目录无 kimi 相关 backend（grep 无匹配）
  - iOS "Kimi" 出现位置仅为 `kimi-k3` 模型 ID（测试）与 `spkiMismatch`（字符串子串）—— 均与 backend 身份无关
  - r1 与 r2 的证据均误将 "claude" 引用为 "Kimi"；v3 采纳 r2 错误证据，导致"修复"本身仍错
- **影响**: 验收测试 §5.1 第 3 条枚举错误 backend；§5.2 第 3 条操作"关闭 Kimi Code CLI 的 Remote Control relay"不可执行（产品不存在）
- **修订方向**:
  - §5.1 第 3 条改为 "其他 backend（claude, grokbuild, dsh-web, opencode-web）"
  - §5.2 第 3 条改为 "关闭 ChatGPT Desktop（codex-remote 的 Remote Control 对端）或断开网络"
  - 注：§5.2 第 3 条已有 "或断开网络" 备选，可执行；但主操作仍指向不存在产品
- **闭合标准**: 验收标准中引用的 backend 名称与 drivers 列表一致；测试操作可执行

### [F-15 未闭合] untracked docs 实际 5 个

- **位置**: 方案 §1 来源清单
- **证据**:
  - 方案称 "4 个 untracked docs（2 个属前序任务，2 个属本任务：方案 + r1 评审报告）"
  - v3 撰写时间（19:59）晚于 r2 报告（19:48），r2 应计入
  - 实际 5 个：`plan + r1 + r2`（本任务）+ `zcode-plan-agents-acceptance + zcode-plan-agents-design`（前序任务）
- **修订方向**: 更正为 "5 个（2 前序 + 3 本任务：方案 + r1 + r2 评审报告）"
- **闭合标准**: 与 `git status` 一致

---

## 跨轮核验复用

| 命题 | 复用依据 | 本轮状态 |
|------|---------|---------|
| `catalogRequestTimeout = 8s`（:16） | r1 亲核，提交未变 | 复用 |
| codex-remote 不走 workspace catalog（handlers_codex_catalog.go:52-58） | r1 亲核 | 复用 |
| codex-remote 2 页分页 14-24s 实测 | r1 亲核 | 复用 |
| RPC client 60s per-request timeout（client.go:68） | r2 亲核，提交未变 | 复用 |
| snapshot cache TTL 10min（catalog_cursor_v2.go:31） | r1 亲核 | 复用 |
| 失败不 commit snapshot（catalog_wire_snapshot.go:289-294） | r1 亲核 | 复用 |
| iOS client 30s timeout（CCCodeBridgeTransport.swift:97） | r1 亲核 | 复用 |
| wire "list_failed" → CCCodeBridgeError.code="list_failed" | r1 亲核 | 复用 |
| isBridgeReadyRetryableError 白名单无 list_failed | r1 亲核 | 复用 |
| Mac singleflight inFlight map | r2 亲核 | 复用 |
| iOS 未提交修改与方案锚点不重叠 | r2 亲核 + 本轮 diff hunk 范围复核（122-497、922-1152 vs :511、:677） | 复用 |

---

## 复审处置与剩余门

### 仍待证据/OD/授权

- 无 pending 证据门
- 无 pending OD

### 阻塞意见列表

| F-ID | 严重性 | 标题 | 影响切片 |
|------|--------|------|----------|
| N-1 | 阻塞 | v3 catch 片段遗漏 generation/scopeKey 守卫与 cursor_stale 重建分支 | Slice 2 |

### 建议意见列表

| F-ID | 严重性 | 标题 | 影响切片 |
|------|--------|------|----------|
| N-2 | 建议 | guard 在重试期间对所有调用者开放 | Slice 2 |
| N-3 | 建议 | item 7 / Slice 3 UI 条件调整对象错误 | Slice 2/3 |
| N-4 | 建议 | Slice 2 字段列表遗漏 isAutoRetrying | Slice 2 |
| F-9 部分 | 建议 | §5.1.2 标题 162s 与明细 170s 矛盾 | 验收标准 |
| F-11 部分 | 建议 | §6 Slice 2 与 §7 仍写 2s/4s/8s | Slice 2 |
| F-13 | 建议 | "Kimi" backend 不存在；实际 drivers[0]="claude" | 验收标准 |
| F-15 | 建议 | untracked docs 实际 5 个 | 来源清单 |

---

## 附录：关键架构发现

1. **errors.Is 链路完整**：RPC client（client.go:289）用 `%w` wrap ctx.Err()=DeadlineExceeded → codex-remote ListSessions raw return → buildGlobal raw → FetchOrReuseContext raw → pageV2Context raw → recentHandleListSessions 可 `errors.Is(err, context.DeadlineExceeded)` 检测。Slice 1 机制可行。

2. **sidebar recent sentinel 已满足 UI 需求**：`SidebarView.swift:521` 条件 `refreshInFlight || isLoading` 已覆盖重试期间 spinner 展示；v3 保持 isLoading=true 贯穿重试，sentinel 无需改动。方案 item 7 的 UI 调整实为 no-op。

3. **"Kimi" 命名错误溯源**：r1 F-3 证据误将 `RuntimeManager.swift:205` 的 `"claude"` 引用为 `"Kimi"`；r2 F-13 沿用该错误并建议改为 "Kimi"；v3 采纳 r2 错误证据。实际 drivers 列表首元素为 `"claude"`（Claude Code CLI）。Mac 仓 `agent/` 目录无 kimi 相关 backend。

4. **scope-leak 历史事故**：iOS think.md 2026-09-20 条目明确记录"切 backend 后 session 列表残留旧 backend 错误 — 已修（scope 泄漏）"。方案 §2.4 引用该事故作为动机，但 v3 的 catch 片段遗漏 generation/scopeKey 守卫，恰好重新引入该 bug 类。N-1 阻塞根因。
