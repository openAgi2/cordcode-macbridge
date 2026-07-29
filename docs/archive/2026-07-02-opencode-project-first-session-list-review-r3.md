# 第三轮评审：OpenCode Project-First Session List Redesign

Date: 2026-07-02

评审对象：[docs/2026-07-02-opencode-project-first-session-list-plan.md](../2026-07-02-opencode-project-first-session-list-plan.md)（已按第二轮 [review-r2](2026-07-02-opencode-project-first-session-list-review-r2.md) 修订，含 §5.0.1 防返工边界、cursor 决策门、双实施分支、可观测性要求）

方法：不盲信文档。逐条对照源码核验本轮新增论断（§5.0.1 路由/游标/字段/CLI、决策门、可观测性），并主动复查第二轮自己下的结论。只读核验，未改运行链路。

---

## 0. 总体结论

**判定：方案已达到可进 exec-plan 的成熟度。本轮新增的防返工边界（§5.0.1）、cursor 决策门（§10.1/§11.1-2）、双分支（§12）经代码核验全部准确。只剩两处"精度"需要微调，其中一处是纠正我自己第二轮的过头判断。**

- ✅ §5.0.1 的四条 do/don't 经源码逐条核实，**CLI fallback 论断不仅成立、证据比方案写的更强**（见 §1.4）。
- ✅ 决策门与双分支内部自洽；排序/方向验证正确地对两条分支都适用。
- 🔧 **P2 修正（可观测性）**：请求预算计数**今天就能**从 `go-bridge.log` 证明（入口已有通用 RPC 日志），方案的 §12/§11 把"首发"绑死到一条新日志线，过强了——这是我第二轮 §2.2 说过了头，方案继承了这个过头判断，本轮纠正。
- 🔧 **P2 小缺口（handler 接线）**：§5.2 写了 `ocProxy` 要收 limit/cursor，但没显式说 `ocHandleListSessions` 必须**新提取** limit/cursor 并透传——容易漏接线。
- 🟡 两处一致性打磨（§9.3 不分支、§11.3 "where verified" 歧义）。

做完这两处 P2 微调，即可进入 exec-plan，第一步仍是 §11.1 的 curl 决策门。

---

## 1. 本轮新增论断逐条代码核验

| 论断 | 位置 | 核验结果 | 证据 |
| --- | --- | --- | --- |
| `isOC()` 把 OpenCode HTTP 模式路由到 `handleOpenCodeRPC`，generic `handleListSessions` 被刻意绕过 | §5.0.1 L177,187,190 | ✅ 准确 | [handlers.go:565-568](../../go-bridge/handlers.go) + `isOC()` [handlers.go:348](../../go-bridge/handlers.go) (`ocProxy != nil`) |
| 不要复用 `paginateSessionList`（需全量内存列表，会重演 global dump） | §5.0.1 L188 | ✅ 准确 | [pagination.go:359-391](../../go-bridge/pagination.go) 确为"对已排序列表切片"，游标 [pagination.go:318-352](../../go-bridge/pagination.go) |
| proxy `mapSession` 保留 projectId/parentId/timestamps/model/provider/messageCount | §5.0.1 L183 | ✅ 准确 | [opencode-proxy.go:322-346](../../go-bridge/opencode-proxy.go) 输出含 `projectId`/`parentId`/`createdAtMillis`/`updatedAtMillis`/`effectiveModelId`/`effectiveProviderId`/`messageCount` |
| 不能用 CLI fallback `agent.ListSessions()` 替代 proxy（缺 directory 作用域与 project 元数据） | §5.0.1 L189 | ✅ 准确，**且证据更强**（见 §1.4） | [opencode.go:641-668](../../agent/opencode/opencode.go) |
| generic `session loading metrics` 日志由 generic `handleListSessions` 发，OpenCode 路径不发 | §10.9 L478 | ✅ 结构准确，**但"无法证明"过强**（见 §2） | `newSessionLoadRequestMetrics` 仅 [handlers.go:3185,3578](../../go-bridge/handlers.go)；`ocHandleListSessions` 用裸 `SendResult` |
| 决策门要求先验证 cursor 参数/响应形状/排序/next 方向/版本 | §11.1 L484-489 | ✅ 合理且自洽 | 排序/方向对两条分支都适用（§10.1 L454） |
| 双分支：full vs first-page-only | §11.2, §12 L513 | ✅ 自洽 | first-page-only 时 `hasMore` 恒假 → 加载更多自然不展示（§6.3 L347） |

### 1.4 CLI fallback 论断——证据比方案写的更强

§5.0.1 说 CLI fallback "lacks the HTTP directory scope and OpenCode project metadata"。源码核实：CLI 入口结构 `opencodeSessionEntry`（[opencode.go:634-639](../../agent/opencode/opencode.go)）**只有 `{ID, Title, Updated, Created}` 四个字段**，转成 `core.AgentSessionInfo` 时（[opencode.go:659-664](../../agent/opencode/opencode.go)）只填 `{ID, Summary, MessageCount, ModifiedAt}`——**没有 projectId、没有 directory、没有 parentId、没有 provider/model**。而且 CLI 以 `c.Dir = workDir`（[opencode.go:643](../../agent/opencode/opencode.go)）跑，天生单 workDir，无法做多项目/目录作用域枚举。所以 §5.0.1 这条 do-not 不仅成立，理由比"缺元数据"更硬：CLI 路径根本无法表达"某个项目的会话"。✅

---

## 2. 关键修正：可观测性——计数今天就能证明，别把首发绑死新日志

**这是我第二轮 §2.2 说过了头、方案 §10.9/§11.4/§12 继承了过头判断的一处。本轮纠正。**

第二轮我说"OpenCode 路径无 metrics 信号、验收无法证伪"。本轮复查发现：[handlers.go:529](../../go-bridge/handlers.go) 的入口日志

```go
slog.Info("go-bridge: RPC request", "method", msg.Method, "backendId", msg.BackendID, "requestId", msg.RequestID)
```

**对每个 RPC 都打**，包括 OpenCode 的 `list_sessions`（ocProxy.fetch 内部确实无日志，但入口这条覆盖了所有方法）。所以 §12 那条"冷启动 `list_sessions` 计数 ≤4"的验收，**今天就能**用

```bash
grep 'go-bridge: RPC request' go-bridge.log | grep method=list_sessions | grep backendId=opencode | wc -l
```

证明，不需要等新日志线。

因此分层澄清：

- **请求计数（请求预算验收）**：✅ 现有入口日志已足够，**不阻塞首发**。
- **富诊断（cursor-present / result_count / next-cursor-present / duration）**：❌ 确实缺（ocProxy.fetch 无日志、ocHandleListSessions 无 metrics），值得按 §10.9 补，但属**并行增强**，不该卡住 first-page-only 上线。

**建议改两处措辞：**

- §12 L515："proven from the new OpenCode proxy list log line" → 改为"proven from `go-bridge.log`（请求计数用现有 `RPC request` 入口日志即可；富诊断字段由 §10.9 的新日志线补充）"。
- §11.4（add observability）不应排在"暴露给 iOS / 构建 Release"之前作为硬门禁；它是并行的诊断增强，可与业务实现同步或之后做。

（顺带：第二轮 [review-r2 §2.2](2026-07-02-opencode-project-first-session-list-review-r2.md) 那条"验收无法证伪"应据此降级——计数可证，只有富诊断缺失。我那轮说重了。）

---

## 3. 小缺口：handler 层 limit/cursor 提取未显式写出

§5.2 给出了 `ocProxy` 侧的 `OpenCodeSessionListOptions{Directory, Limit, Cursor}` 和 `GET /session?limit=&cursor=`，但**没显式说 `ocHandleListSessions` 要新增提取逻辑**。复查确认：当前 [handlers.go:925-947](../../go-bridge/handlers.go) 的 `ocHandleListSessions` **只提取 `rootsOnly`**（L926），不提取 limit/cursor。generic 路径用的是 `extractPositiveInt(msg,"limit")`（[handlers.go:3184](../../go-bridge/handlers.go)）和 `extractStringParam(msg,"cursor")`（[handlers.go:3200](../../go-bridge/handlers.go)）。

**风险**：实施者按 §5.2 扩了 `ocProxy`，却忘了在 handler 里加提取并透传，导致 iOS 发来的 limit/cursor 被静默丢弃（请求看起来"成功"但分页不生效）——这正是架构文档反复警告的"看起来好了"。

**建议**：§5.2 增一行显式要求——"`ocHandleListSessions` must newly extract `limit` (via `extractPositiveInt`) and `cursor` (via `extractStringParam`) and pass them through to `ocProxy.listSessions(options)`; today it extracts only `rootsOnly`." 并在 §9.1 加一条测试：带 limit/cursor 的 list 请求，断言发往上游的 URL/header 含这些值（可通过 httptest 捕获）。

---

## 4. 次要一致性打磨

1. **§9.3 手动验证不分支**：§12 已按 full / first-page-only 分支，但 §9.3 的 step 4（tap load more）和 step 8（重复 load-more 不重复、`hasMore=false` 收按钮）是 **full-path 专属**，first-page-only 下无意义。建议像 §12 那样标注"仅 full 分支执行"。
2. **§11.3 "where verified" 有歧义**：`support directory-scoped limit/cursor where verified` 可被误读为"first-page-only 不动 proxy"。实际**两条分支都要加 limit 提取 + 信封形状 + mapSession + 入口日志**，只有 cursor 处理不同。建议改为"两条分支都扩展 proxy 的 limit + 信封；仅 cursor 透传按 §11.2 决策结果启用"。
3. **加载更多按钮其实是 `hasMore` 驱动，不必依赖 backend 名**：§6.3 已是"bucket 有 hasMore 才显示加载更多"，所以 first-page-only（`hasMore` 恒假）下按钮自然消失——这条很干净。§6.1 的 `backendKind == .openCode` 门控只用于**桶布局**（project-first），不用于加载更多。这是合理拆分，无需改，仅提示：将来若把桶布局推广到 Codex/Claude，门控迁 capability（§6.1 L316 已记为债）。

---

## 5. 结论与进 exec-plan 前的微调清单

方案三轮下来已从"方向对但有遗漏"演进到"边界清晰、防返工到位、决策门前置"。进 exec-plan 前只需：

1. **§12 L515 + §11.4**：解耦——请求预算计数用现有入口日志即可，新富诊断日志线为并行增强，不卡首发。（纠正我第二轮的过头判断）
2. **§5.2**：显式写 `ocHandleListSessions` 必须新提取 limit/cursor 并透传；§9.1 加对应 httptest 断言。
3. **§9.3 / §11.3**：补分支标注与措辞澄清（打磨级）。

这三条都是文档措辞调整，不涉及重做设计。做完即可进 exec-plan；exec-plan 第一步必须是 §11.1 的 curl 决策门，其结果决定走 full 还是 first-page-only——这步在写任何 Swift/Go 业务代码之前完成并记录。

三轮评审累计确认的核心不变量（实施时不得违反）：

- OpenCode 留在 proxy 路径，不并入 generic（§5.0.1，已代码核实）。
- 复用信封**形状**，不复用 generic handler / `paginateSessionList` / CLI fallback。
- `cursor` 对 iOS 恒为 opaque、memory-only、不跨 backend/项目/进程复用。
- `hasMore` 仅由上游真实 next cursor 决定，不做"恰好等于 limit"推断。
- 真实失败要暴露，不加 mock/fallback（[CLAUDE.md](../../CLAUDE.md)）。
