# 评审报告：OpenCode Project-First Session List Redesign

Date: 2026-07-02

评审对象：[docs/2026-07-02-opencode-project-first-session-list-plan.md](../2026-07-02-opencode-project-first-session-list-plan.md)

评审依据：对照 `cordcode-macbridge` 源码（go-bridge / agent-opencode）、[GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md)、[CLAUDE.md](CLAUDE.md) 新 session bootstrap 约束，以及 `../cordcode-ios/` 现状。本报告只读核验，未修改任何运行链路代码。

---

## 0. 总体结论

**判定：方向正确，可作为实施基线，但存在一处重大事实性遗漏和若干必须在动工前厘清的技术边界，否则会重复造轮子并埋下游标语义冲突。**

- ✅ 产品方向（project-first、per-project bucket、冷启动有请求预算、global 降权）是对的，且与 iOS 现状（扁平 `[Session]` + 单次 global fetch + "查看更多"仅开 sheet）的差距判断准确。
- ✅ MacBridge 侧多数"已具备"的判断属实：`listProjects` 已映射 worktree→directory、`mapSession` 已携带 `projectId`、`x-opencode-directory` header 已存在。
- ❌ **致命遗漏**：方案把 `list_sessions` 的 `{sessions, nextCursor, hasMore}` 分页当成全新设计，但该信封在 generic 路径已实现并被 codex/claudecode 使用（[pagination.go:359](../../go-bridge/pagination.go)）。OpenCode 之所以没有，是因为被 `handleOpenCodeRPC` 路由到了无分页的 proxy 路径，而非"分页不存在"。
- ⚠️ 由此引出两条**游标语义不同**的分页路径共用同一个 RPC 字段 `cursor`，方案没有说明这一点，也没说明为什么 OpenCode 不能复用现有 `paginateSessionList`。
- ⚠️ 方案未覆盖：capability 信号、protocol pack 同步、`rootsOnly` 与分页的相互作用、opencode 双打包轨道（stable vs 2.0 preview）对响应形状的影响。

建议：**采纳产品模型，但在 §5/§9 重写 API 设计章节**，显式声明"复用已有信封、OpenCode 走 opaque 上游游标、不复用内存复合游标"，并补齐下文第 3 节的边界。

---

## 1. 方案论断逐条核验

| 方案论断 | 核验结果 | 证据 |
| --- | --- | --- |
| 当前 `ocProxy.listSessions` 只传 directory、无 limit/cursor | ✅ 准确 | [opencode-proxy.go:91-97](../../go-bridge/opencode-proxy.go) `listSessions(directory string)` 仅 `withDir(directory)` |
| MacBridge 已把 OpenCode `worktree` 映射为 `directory` | ✅ 准确 | [opencode-proxy.go:225-261](../../go-bridge/opencode-proxy.go) `listProjects` 按 `directory`→`path`→`worktree` 取值 |
| CordCode 现在看到 legacy/flattened 响应形状，需同时兼容数组与 `{data,cursor}` | ✅ 准确且必要 | [opencode-proxy.go:566-589](../../go-bridge/opencode-proxy.go) `unwrapArray` 的 generic 回退会从 map 里"任意取第一个 slice"，对 `{data,cursor}` 会拿到 `data` 但**丢弃 cursor** |
| `mapSession` 已暴露 `projectId` | ✅ 准确 | [opencode-proxy.go:289-292,329](../../go-bridge/opencode-proxy.go)（同时兼容 `projectId`/`projectID`） |
| iOS `sessions` 是单一扁平数组、无 per-project bucket | ✅ 准确 | `../cordcode-ios/.../Views/Session/SessionsView.swift` `@Published var sessions: [Session]` |
| iOS 侧"查看更多"只打开 sheet、无真正分页 | ✅ 准确 | `SidebarView.swift:259-277` 仅 `selectedProjectGroup = group`；`ProjectSessionsListView` 不发请求 |
| iOS `listSessions` 无 limit/cursor 参数 | ✅ 准确 | `CCCodeBridgeClient.swift:40-47` 仅 `backendId/workspaceId/directory/rootsOnly` |
| 上一轮 iOS "OpenCode = 单次全局 catalog" 是当前实际行为 | ✅ 准确 | 测试名 `testOpenCodePathUsesSingleGlobalSessionFetchAndNormalizesProjectDirectory` 直接坐实 |
| `list_sessions` 当前没有任何分页能力（方案 §0/§5 隐含前提） | ❌ **不成立** | generic 路径 [handlers.go:3183-3233](../../go-bridge/handlers.go) + [pagination.go:359](../../go-bridge/pagination.go) 已实现 `{sessions,nextCursor,hasMore}`，codex/claudecode 在用；OpenCode 只是没走到这条路径 |

---

## 2. 重大遗漏：`list_sessions` 分页信封已存在

这是本评审最关键的一条，方案全文没有提到。

**现状（源码真值）：**

- generic 分发 `case "list_sessions"` → `handleListSessions`（[handlers.go:706-707, 3183](../../go-bridge/handlers.go)）。
- 对非 claudecode backend：`agent.ListSessions(ctx)` 取**全量** → `sessionsToWire` → `paginateSessionList(wireSessions, cursor, limit)`（[handlers.go:3190-3206](../../go-bridge/handlers.go)）。
- `paginateSessionList`（[pagination.go:359-391](../../go-bridge/pagination.go)）输出正是方案 §5.1 提出的 `{sessions, hasMore, nextCursor}`，并对老客户端"多吐字段不破坏"。
- 已有定向测试 `TestListSessionsPagination`（[pagination_test.go:328](../../go-bridge/pagination_test.go)）证明该信封按 newest-first、跨页无重无漏工作。

**OpenCode 为什么没分页：**

OpenCode 的**所有**方法先经 `handleOpenCodeRPC`（[handlers.go:567,764](../../go-bridge/handlers.go)），其中 `list_sessions` 被路由到 `ocHandleListSessions`（[handlers.go:813-814, 925-947](../../go-bridge/handlers.go)），后者直接 `ocProxy.listSessions(dir)` 全量返回，**绕开了** generic 分页。也就是说，"OpenCode list_sessions 无分页"是路由结果，不是能力缺失。

**对方案的影响：**

1. 方案 §5.1 把 `{sessions, nextCursor, hasMore}` 当新信封设计，会让实施者重新发明一个已存在且已被测试的信封。**应改为"复用 `paginateSessionList` 的响应契约"**，仅讨论 OpenCode 如何填充它。
2. 方案 §1 Evidence / §2 Failure Mode 缺一句关键归因：问题不在"没有分页接口"，而在"OpenCode 走的是无分页 proxy 路径 + iOS 做了单次 global dump"。归因错了，实施顺序就会错。

---

## 3. 必须在动工前厘清的技术边界

### 3.1 两条游标语义不能混为一谈（高优先级）

| 路径 | 游标语义 | 来源 |
| --- | --- | --- |
| generic（codex/claudecode） | bridge 复合游标 `{v, ts(updatedAtMillis), sid}`，base64；对**已全量取回**的内存列表做切片 | [pagination.go:318-352, 359-391](../../go-bridge/pagination.go) |
| 方案为 OpenCode 提议 | opaque 上游游标（OpenCode `/session?cursor=` 原样透传） | 方案 §5.2 |

同一个 RPC 字段 `cursor`，两套含义。这本身**可接受**（iOS 只要把 cursor 当 opaque 透传，不解析），但方案必须显式写明：

- OpenCode **不应**复用 `paginateSessionList` 的复合游标，因为那要求 `ocProxy.listSessions` 先取**全量 global** 再切片——而这正是方案要消灭的"100 条 global dump"bug。
- OpenCode 需要的是**真·服务端游标分页**：每次只向 OpenCode 取一页。这是正确选择，但理由（"全量取回再切片 = 把 bug 搬进内存"）方案没写。
- iOS 端 `cursor` 必须按 opaque 处理、不得跨 backend 复用、不得持久化后跨进程使用（方案 §7 已提到"cursor 视为 memory-only"，正确，建议升级为硬约束）。

### 3.2 capability 信号缺失（高优先级）

[CLAUDE.md](CLAUDE.md) 与 [GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md) 明确：capability 由 `core/interfaces.go` 的可选接口推导，`hello_ack.backends[].capabilities` 下发，**不得维护脱离源码的手写能力表**。

方案引入 `list_sessions` 分页却不提 capability：

- 现状是没有 `session_list_pagination` 之类的 capability；iOS 也确实没用 limit/cursor。
- 方案落地后，codex/claudecode（generic 路径）和 opencode（proxy 路径）**都将**支持分页，所以 iOS 可以"默认所有 backend 支持"。但如果将来出现只支持部分 backend 的情况，没有信号就会变成按 backend 名猜——正是架构文档禁止的。
- **必须澄清的混淆**：[agent_descriptor.go:115-123](../../go-bridge/agent_descriptor.go) 里刻意关闭的 `session_pagination` capability 指的是**会话内消息历史分页**（`get_session_messages` + `paginate=true`，因 newest↔backward 振荡被关），与本方案的**会话列表分页**是两件事。方案全文没有区分二者，后续 reviewer 极易误读为"本方案触发了被禁的 session_pagination"。**建议方案专门加一段消歧**。

### 3.3 `rootsOnly` 与分页的相互作用会破坏分页数学（高优先级，方案完全没提）

`ocHandleListSessions` 现在是**先全量取、再客户端过滤** `rootsOnly`（[handlers.go:926,941-943](../../go-bridge/handlers.go)）：

```go
if rootsOnly && parentID != "" { continue }
```

一旦叠加服务端 `limit`/`cursor`，顺序变成"服务端返回 limit 条 → 客户端再过滤掉 parent 会话 → 实际可见 < limit → `hasMore` 误判 → 翻页漏项或空页"。这是真实 bug 风险。方案 §5.1 只说"`rootsOnly` 仍被支持"，没给语义。**必须明确**：要么 `rootsOnly` 在 OpenCode 目录分页下被禁用/报错，要么过滤必须在服务端完成（OpenCode 是否支持需 curl 核实），否则分页正确性无法保证。

### 3.4 OpenCode 双打包轨道决定响应形状（中优先级）

记忆 [[opencode-packaging-two-tracks]] 记录：`opencode-ai`（stable，无 service）与 `@opencode-ai/cli`（lildax，2.0 preview，有 service）命令面/协议面不同，"源码 daemon.ts ≠ stable 命令面"。

方案 §1 的 `DefaultSessionsLimit = 50`、`{data, cursor}` 形状来自**源码（疑似 2.0）**，而本机 curl 看到**数组形状**（疑似 stable）。因此 §5.2 的双形状解码不是"防御性兼容"，而是**两条轨道客观不同**。建议：

- 方案钉死 MacBridge 当前目标是哪条轨道（与 `external_http` resolved endpoint 实际跑的 server 对齐）。
- §9 测试计划必须**同时**用 httptest 模拟数组形状与 `{data,cursor}` 形状，不能只测一种。
- 注意方案 §1 的内部张力：源码默认 `limit=50`，但本机 bare `/session` 返回 100 条——要么 server 覆写了默认值，要么是另一条轨道。建议在 evidence 里记下实际 server 版本/commit，避免后续踩坑。

### 3.5 路径归一化要用 realpath，不是字符串比较（中优先级）

方案 §4.3 列了 `/private/tmp` vs `/tmp`、尾斜杠、`~`。在 Darwin 上 `/tmp` 是 `/private/tmp` 的 symlink，字符串比较会判不相等。`listProjects` 取到的 `worktree` 通常已是 realpath，而手动 pin 的目录可能是 symlink 形态。**应明确用 `filepath.Abs` + `filepath.EvalSymlinks` 归一再比**，并说明大小写策略仅限不区分大小写的卷。

### 3.6 global 会话按目录最长前缀重派（中优先级，有误归风险）

方案 §6.5 提议"`projectID=global` 但 directory 落在已知项目根下的会话，按最长前缀匹配重派回该项目"。OpenCode 的 `global` 项目本意是"无归属"，按 cwd 前缀猜项目可能把**有意 global** 的会话静默归到某个项目下，造成分组不可预期。建议：要么不做自动重派（global 就是 global），要么做成**带明确标识的启发式**（如显示"疑似属于 X"）并允许用户撤销，不要静默改分组。方案 §10.4 已意识到 session 可能在项目间移动，但这里的静默重派是另一个方向的同类风险。

---

## 4. 与架构约束的契合度

| 架构约束 | 方案契合情况 |
| --- | --- |
| OpenCode 是 hybrid 路由，`list_sessions` 当前走 HTTP proxy（[GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md) 路由矩阵） | ✅ 方案保持 proxy 路径，没有把 list_sessions 搬进 agent。正确 |
| 真实路径失败要暴露，不得加 mock/fallback（CLAUDE.md / 架构文档） | ✅ §12 "No production mock/fallback" 明确对齐 |
| 协议破坏性变更要升 major + 同步 iOS protocol pack（CLAUDE.md 新 session bootstrap） | ⚠️ 加 `limit/cursor/nextCursor/hasMore` 都是**可选字段**，非破坏，无需升 major；但**仍需**更新 `docs/protocol/` 权威 pack 与 iOS mirror。方案 §11 实施顺序里没有 protocol pack 同步这一步，需补 |
| `session_pagination`（消息历史）capability 关闭 | ⚠️ 与本方案无关，但方案未消歧，见 §3.2 |
| 改完 go-bridge/MacBridge 必须 Release 构建并覆盖安装（CLAUDE.md） | ✅ §11 step 6 已包含 |
| 发布前 secret scan（CLAUDE.md） | ⚠️ §11/§12 未提；OpenCode 不涉及 secret，但流程上应保留 |

---

## 5. 测试计划与实施顺序的修订建议

**Go 侧测试（§9.1）应增补：**

1. 已有 `TestListSessionsPagination` 覆盖 generic 信封——**不要重写**，新增的 OpenCode 测试只验证 proxy 路径透传 `limit/cursor`、发送 `x-opencode-directory`、并正确解码两种形状。
2. **httptest 双形状**：同一个 `ocProxy.listSessions` 调用，分别对 array 与 `{data,cursor}` 两种 server 响应断言，且后者必须保留 `nextCursor`（验证修了 `unwrapArray` 丢 cursor 的问题）。
3. **`rootsOnly` × `limit` 组合**：构造含 parent 会话的数据，验证翻页不漏项、不空页（见 §3.3）。
4. `hasMore` 契约：对 OpenCode 应为 `upstream cursor.next != null`，**不要**采用方案 §5.1"恰好等于 limit 就乐观置 hasMore=true"的兜底——那会触发一次空尾页请求。若上游确无 cursor，再考虑降级，并在日志里 `log()` 说明。

**实施顺序（§11）建议调整为：**

1. MacBridge：先决定 §3.1～§3.3 的语义（写进方案再动手）。
2. MacBridge：扩展 `ocProxy.listSessions` 支持 limit/cursor + 双形状解码，扩展 `ocHandleListSessions` 复用 `{sessions,nextCursor,hasMore}` 信封并解决 `rootsOnly` 顺序。
3. MacBridge：定向 Go 测试（含 httptest 双形状、rootsOnly×limit）。
4. **更新 `docs/protocol/` 权威 pack + iOS mirror/模型**（CLAUDE.md 要求，原方案漏列）。
5. iOS：引入 `ProjectSessionBucket` 与 OpenCode-only project-first 路径，`cursor` 按 opaque 透传。
6. iOS：sidebar 分组与 per-project load-more；补单测（请求预算、桶分页、global 排序、merge、empty state）。
7. 分别完成 MacBridge Release 与 iOS Debug 构建+覆盖安装。
8. owner 真机/共享 server 手动验收。

---

## 6. 验收标准的修订建议

方案 §12 的验收标准基本可用，建议补两条可证伪的硬指标，避免"看起来好了"：

- **冷启动请求预算可量化**：新增"OpenCode 冷启动 `list_sessions` 调用数 ≤ K（K = 首屏可见项目数 + 选中项目，典型 ≤ 4）"，并能在 go-bridge 日志里 grep 出调用次数作为证据（对应方案 §9.3 第 7 条，升级为验收项）。
- **分页正确性**：`Chat` 连续点 3 次"加载更多"，断言每次恰好 1 个目录作用域请求、累计会话无重复 id、最终页 `hasMore=false` 后按钮消失。这条把 §3.1/§3.3 的风险变成可验证项。
- 其余 §12 条款（global 不再占顶、Desktop 项目可见、Chat/cc-connect 出真实会话、每项目初始 ≤10、加载更多只影响当前项目、无 mock）保留。

---

## 7. 风险登记（在方案 §10 基础上补充）

| 编号 | 风险 | 来源/证据 | 建议 |
| --- | --- | --- | --- |
| R1 | 上游 cursor 查询参数名不确定（方案假设 `cursor`，可能是 `before/after`） | §10.1 | 动工前 curl 核实实际 server 的参数名与形状，钉死版本 |
| R2 | stable 与 2.0 preview 响应形状/默认 limit 不同 | 记忆 [[opencode-packaging-two-tracks]]、§1 内部张力（50 vs 100） | 双形状解码 + 双形状测试，evidence 记 server 版本 |
| R3 | `rootsOnly` 客户端过滤破坏服务端分页 | [handlers.go:941-943](../../go-bridge/handlers.go) | 见 §3.3，必须先定语义 |
| R4 | 把已有 generic 分页信封当新设计，重复实现 | [pagination.go:359](../../go-bridge/pagination.go) | 复用契约，见 §2 |
| R5 | `session_pagination`（消息历史）被误读为被本方案触发 | [agent_descriptor.go:115-123](../../go-bridge/agent_descriptor.go) | 方案加消歧段，见 §3.2 |
| R6 | global 会话静默重派到项目 | §6.5 | 改为显式启发式或不做，见 §3.6 |
| R7 | protocol pack 未同步 | CLAUDE.md | 实施顺序补一步，见 §5 |

---

## 8. 给方案作者的最小修订清单

1. **§0/§1/§2**：补一句"`list_sessions` 的 `{sessions,nextCursor,hasMore}` 信封在 generic 路径已存在（pagination.go），本方案只是让 OpenCode proxy 路径复用同一契约，并改用 opaque 上游游标"。
2. **§5.1**：删除"恰好等于 limit 乐观置 hasMore"兜底；改为 `hasMore = upstream cursor.next != null`；明确 `cursor` 为 opaque、memory-only。
3. **§5.1**：新增一段说明 `rootsOnly` 与 `limit/cursor` 的优先级（推荐：OpenCode 目录分页下禁用或服务端过滤）。
4. **§5.2**：把"两种形状解码"从防御性兼容升级为"双打包轨道客观差异"，并要求测试双形状。
5. **新增 §X（消歧）**：明确"会话列表分页 ≠ 被关闭的 `session_pagination`（消息历史分页）capability"。
6. **§6.5**：把 global→项目静默重派改为显式启发式或删除。
7. **§9**：加 httptest 双形状测试、rootsOnly×limit 测试、冷启动请求计数断言。
8. **§11**：插入"更新 `docs/protocol/` + iOS mirror"步骤；保留 Release/iOS 构建与 owner 真机验收。

完成上述修订后，方案可进入 exec-plan 执行阶段。
