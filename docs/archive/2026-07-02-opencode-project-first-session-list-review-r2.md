# 第二轮评审：OpenCode Project-First Session List Redesign

Date: 2026-07-02

评审对象：[docs/2026-07-02-opencode-project-first-session-list-plan.md](../2026-07-02-opencode-project-first-session-list-plan.md)（已按第一轮 [review](2026-07-02-opencode-project-first-session-list-review.md) 修订）

方法：重新通读修订稿，逐条核验 7 项声称的修订是否准确落地，再做 adversarial pass，并补足第一轮未深挖的可观测性与排序/方向假设。只读核验，未改运行链路。

---

## 0. 总体结论

**判定：7 项声称修订全部准确落地，事实核验无误，方案已显著成熟，达到"可进 exec-plan"门槛。但第二轮发现两处被埋没为"普通 risk"的关键依赖，实则是二值可行性门，必须在 step 1 之前就以决策树对待：**

1. **上游 cursor 是否存在，决定"加载更多"这个 headline 功能能不能上线**——不是风险注脚，是可行性门。
2. **§12 要"从 `go-bridge.log` 证明"冷启动请求预算，但 OpenCode 路径当前不产生该日志信号**——验收标准无法证伪。
3. **结果排序与游标方向（newest-first、cursor→older）是未验证假设**，错了则"首屏 10 条 = 最新 10 条"不成立。

这三条之外都是次要打磨。修订质量本身没有引入新的事实错误。

**另（防返工）**：第二轮架构考古确认，OpenCode 独立于 generic 分页是 [2026-06-13 重设计文档](../2026-06-13-session-loading-systemic-redesign.md) 风险 M5 的刻意边界，不是疏忽。本方案"复用已有信封"只能停在**响应形状**，绝不能把 OpenCode 并回 generic handler 或机制——否则会丢字段、坏路由、重演 global dump。详见 §1.5。

---

## 1. 修订核验（7 项声称修订）

| # | 声称修订 | 落地位置 | 核验 |
| --- | --- | --- | --- |
| 1 | generic list_sessions 分页信封已存在，OpenCode 绕到 proxy 路径没用上 | §0 L26、§1 L50-52 | ✅ 准确。与源码 [handlers.go:3183](../../go-bridge/handlers.go) + [pagination.go:359](../../go-bridge/pagination.go) + 路由 [handlers.go:813](../../go-bridge/handlers.go) 一致 |
| 2 | OpenCode 不能复用 paginateSessionList 做内存切片，必须走上游目录分页 | §5.0 L160、§5.2 L258-259 | ✅ 推理正确：全量取回再切片 = 把 global 100 条 dump 搬进内存 |
| 3 | 区分两套 cursor 语义 | §5.0 L154-167、§10.6 | ✅ 准确。bridge 复合游标 [pagination.go:318-352](../../go-bridge/pagination.go) vs upstream opaque |
| 4 | session_list_pagination 与已禁用消息历史 session_pagination 消歧 | §5.0 L169、§10.7 | ✅ 准确。禁用点在 [agent_descriptor.go:115-123](../../go-bridge/agent_descriptor.go)，确为 get_session_messages 历史分页 |
| 5 | rootsOnly + limit/cursor 分页数学风险 | §5.1 L211-212、§9.1 L388、§11.3 | ✅ 风险真实：当前先全量取再客户端过滤 parent（[handlers.go:941-943](../../go-bridge/handlers.go)），叠加服务端 limit 会漏项/空页 |
| 6 | global 最长前缀静默重派 → 不默认，仅显式启发式 | §6.5 L342 | ✅ 与第一轮建议一致，措辞稳妥 |
| 7 | docs/protocol + iOS mirror 同步、双响应形状测试、请求预算验收 | §10.8、§11.5、§9.1、§12 L468 | ✅ 全部补齐 |

**结论：修订无事实错误、无过度采纳。** 第一轮的 8 条最小修订清单已全部满足。

---

## 1.5 防过度统一：OpenCode 必须独立于 generic 分页机制（避免返工）

> 背景：本节源自第二轮架构考古（git 历史 + [2026-06-13-session-loading-systemic-redesign.md](../2026-06-13-session-loading-systemic-redesign.md)）。目的是防止实施者读到"复用已有信封"后过度发挥，把 OpenCode 强行并回 generic 路径，触发字段丢失 / 路由破坏 / global dump 重演而来回返工。

**事实：OpenCode 独立于 generic 分页是刻意设计，不是疏忽。**

- 时间线：OpenCode proxy 路径随 `81d0a36`（2026-06-01 初始导入）就存在；generic `paginateSessionList` 由 `ec674f0`（2026-06-14）引入，且该提交**只改了 codex/claudecode，未碰 `opencode-proxy.go` 或 `ocHandleListSessions`**。
- 设计文档把这件事写成了明文边界：原则 6「后端隔离：文件型后端与 OpenCode 代理路径分别处理」、§10「本方案不修改 OpenCode 上游 API；`ocProxy` 的列表和历史能力单独评估」、风险 M5「OpenCode 路径不覆盖」。

**三个结构性原因，决定了"统一"只能停在信封形状：**

1. **路由层拦截**：[handlers.go:565-568](../../go-bridge/handlers.go) 在 `backendID=="opencode" && isOC()`（[handlers.go:348](../../go-bridge/handlers.go) `ocProxy != nil`）时把所有方法转给 `handleOpenCodeRPC`，generic `handleListSessions` 在 HTTP server 已配置时**根本到不了**。
2. **游标模型不匹配**：generic `listCursor`（[pagination.go:318](../../go-bridge/pagination.go)）是 `{ts,sid}` 复合游标，要求 bridge 握有完整排序列表才能切片。codex/claudecode 靠本地文件枚举廉价拿全量；OpenCode 要"握全量"就得从 HTTP 拉全部会话 = global dump 重演。
3. **字段保真**：proxy 的 `mapSession`（[opencode-proxy.go:272](../../go-bridge/opencode-proxy.go)）保留 OpenCode 专属字段（`projectId`/`parentId`/`time.{created,updated,archived}`/`effectiveModelId`/`effectiveProviderId`/`messageCount`）；generic 路径 `agent.ListSessions() → AgentSessionInfo → sessionsToWire` 会丢掉这些。

**实施红线（do / don't）：**

- ✅ 复用 generic 的**响应信封形状** `{sessions, nextCursor, hasMore}`。
- ✅ 在 `ocHandleListSessions` **内部**，用上游 opaque 游标填充该信封。
- ❌ 不要为了"统一"把 `ocHandleListSessions` 并入 generic `handleListSessions`，或把 OpenCode 路由改走 generic——会触发上述 2、3 两类问题。
- ❌ 不要复用 `paginateSessionList`（复合游标 + 全量切片）。
- ❌ 不要用 `agent.ListSessions()`（[opencode.go:504](../../agent/opencode/opencode.go)，实为 CLI `opencode session list`）替代 proxy——它是降级路径、无 directory 作用域、无 project 元数据；仅在 `isOC()==false`（无 HTTP server）时才经 generic 路径使用。

**建议方案文档**在 §5 加一小节「OpenCode 不并入 generic 分页机制」，把上述 do/don't 写进去，使实施者直接看到红线，而不必从评审报告反推。

---

## 2. 第二轮新发现的关键问题

### 2.1 【P1 / 可行性门】cursor 存在性是二值门，不是 risk 注脚

方案目前的处理：§5.2 L259 承认"若上游只回数组，则该轨道只能首页分页"；§10.1 列为 risk #1；§11.1 把 curl-verify 放成 step 1。**问题是它把一个决定半数功能（"加载更多"）能否上线的二值前提，降级成了散落在三处的 risk 注脚。**

关键链条：

- §1 自己的 evidence 显示本机 curl `/session` 拿到的是**数组形状**（L33、L43-46），而 `{data, cursor}` 只来自**源码/v2**（L40）。
- §5.2 L259 已承认：数组轨道下，没有 `nextCursor` 就**无法翻第二页**。
- 而"加载更多"（§6.3）、"反复 load-more 不重复、`hasMore=false` 收手"（§12 L469）是本方案的 headline 价值。

因此 §11.1 的 curl 验证结果会**二分整个方案**：

- **结果 A（stable 轨道确有 cursor）**：完整方案可上线，含加载更多。
- **结果 B（stable 轨道只回数组、无 cursor）**：加载更多**不可上线**；只能交付"每项目首页 10 条"的项目桶（这本身已修复 global dump bug，是净改进），加载更多需 deferred 到 server/轨道升级。

**建议**：把这条提升为 §0 或 §11 顶部的显式决策树，并让 §12 验收标准对 A/B 两种结果分别给出可证伪条款（结果 B 下，"加载更多"相关条款改为 deferred/需 server 升级）。否则实施者可能在结果 B 下仍按 A 实现，产出一个"首页对、点加载更多永远转圈或空"的半成品。

### 2.2 【P1 / 可观测性缺口】"从 go-bridge.log 证明请求预算"在 OpenCode 路径无信号

§12 L468 要求"OpenCode 冷启动 `list_sessions` 计数……从 `go-bridge.log` 证明"，§9.3.7 要求"日志里不再有 list_sessions 爆发"。但：

- 结构化日志 `"go-bridge: session loading metrics"`（[session_load_metrics.go:88](../../go-bridge/session_load_metrics.go)）**只**在 generic `handleListSessions`（[handlers.go:3185](../../go-bridge/handlers.go)）和 paginated messages 路径（[handlers.go:3578](../../go-bridge/handlers.go)）经 `newSessionLoadRequestMetrics` 触发。
- OpenCode 走的是 `ocHandleListSessions`（[handlers.go:925-947](../../go-bridge/handlers.go)），用**裸 `conn.SendResult`**（L946），**不发**该 metrics 行。

也就是说，方案依赖来作证的日志信号，在它选择的代码路径上**当前并不存在**。验收标准无法证伪。

**建议**：§11 增加一步——给 OpenCode paginated 路径补上与 generic 等价的 `sessionLoadRequestMetrics`（至少记 `method/backend_id/transport_route/result_count`），或明确写出验收时要 grep 的**具体日志行**（如 ocProxy 自身的请求日志）。否则"从 log 证明"是空头条款。

### 2.3 【P1 / 未验证假设】结果排序与游标方向

方案通篇隐含"每个项目首屏 10 条 = 最新 10 条""加载更多 = 更旧的会话"。但从未验证：

- OpenCode `/session` 返回的**排序**（newest-first？还是 created/oldest？）。generic 路径的 newest-first 是 bridge 在内存里 `sortSessionsByUpdatedAt` 做的（[handlers.go:3250](../../go-bridge/handlers.go)），**不适用于** OpenCode 上游分页——上游给什么序就是什么序。
- `cursor.next` 的**方向**（向更旧翻？还是向更新翻？）。§5.2 的信封同时有 `{next, previous}`，方案只保留 `.next`（L258），但没说清 `.next` 对应的是"更旧"。

若上游实际是 oldest-first 或 `.next` 向更新翻，则"首屏 10 条"会是最旧 10 条、"加载更多"方向反了，产品语义全错。

**建议**：把这两点并入 §11.1 的 curl 验证清单，明确要确认三件事——(a) cursor 参数名、(b) **stable 轨道是否真的返回 cursor**、(c) **返回排序与 cursor 方向**。三条任一不满足都要回 §2.1 的决策树。

---

## 3. 次要问题（打磨级，不阻塞 exec-plan）

1. **§9.1 L387 负向断言不可测**："OpenCode proxy path does not use generic paginateSessionList over a bare global dump" 是"没做 X"，难以单测。改为正向可测："OpenCode list_sessions 带目录时只返回该目录会话、不触达其他目录。"
2. **§5.2 丢弃 `cursor.previous`**：信封含 `{next, previous}` 但方案只保 `.next`，等于只支持向前（更旧）翻页。对"加载更旧"UX 没问题，但 bucket 的"下拉刷新看新会话"无 cursor 故事——需明确"刷新 = 不带 cursor 重取首页"。补一句即可。
3. **capability 网关 vs backend 名（前瞻债）**：§6.1 L292 用 `backendKind == .openCode` 门控 bucket 路径，§10.7 才讨论 capability。架构规则禁止"按 backend 名猜能力"。OpenCode-only 首版用 backend 身份门控可接受，但应显式记为技术债：**当分页扩展到 Claude/Codex 时，门控必须迁到 `session_list_pagination` capability**，否则违反 [GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md) 的能力推导原则。
4. **冷启动并发度未定**：§6.2 首屏最多加载 ~4 个项目页，未说是并行还是串行。建议显式（如"最多 2 路并发"），避免冷启动对共享 server 瞬时 4 个 `/session` 请求。
5. **`limit` 上限与上游默认对齐**：§5.1 L208 cap `1...50`，§1 L40 上游 `DefaultSessionsLimit=50`，一致；但产品用 10。注意 §1 内部张力仍存（源码默认 50 vs 本机 bare 返回 100），evidence 最好记下实际 server 版本/commit。

---

## 4. 建议的 §11.1 决策树（替换当前的一句话 step 1）

```text
curl-verify stable OpenCode 共享 server，必须确认三件并记录：
  (a) cursor 查询参数名（cursor / before / after …）
  (b) stable 轨道是否真的返回 cursor 信封（还是只回数组）
  (c) 返回排序（newest-first?）与 cursor 方向（.next → 更旧?）

  ├─ (a)(b)(c) 全满足 → 完整方案（含加载更多）
  ├─ (b) 不满足（数组 only） → 交付"每项目首页 10 条"项目桶；
  │                              加载更多 deferred，需 server/轨道升级
  └─ (c) 不满足（非 newest-first 或方向反） → 调整 bucket 首屏/翻页语义后再实现
```

把它放在 §11 顶部，方案的实施风险就前置可见了。

---

## 5. 结论

- 修订质量高，第一轮全部关键建议准确落地，无新事实错误。
- 阻塞 exec-plan 的只剩 §2.1 的决策树化 + §2.2 的可观测性补齐——两处都是"改文档措辞与实施步骤"，不涉及重做设计。
- §2.3 的排序/方向并入 §2.1 的 curl 验证即可。
- 次要问题可在实施中顺手解决。

**建议作者做完 §2.1～§2.3 的文档调整后，即可进入 exec-plan 执行阶段**；进入后第一步（§11.1 curl 验证）的结果决定是走完整方案还是首页-only 降级版，这一步必须在写任何 Swift/Go 业务代码之前完成并记录。
