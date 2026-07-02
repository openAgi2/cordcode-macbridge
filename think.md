# OpenCode session 列表加载方案（实际实现）

本文记录 CordCode iOS OpenCode 模式 session 列表加载的真实修复路径。
设计文档 `docs/2026-07-02-opencode-project-first-session-list-plan.md` 的部分判断
（array-only/no-cursor、保守 limit=5）在真机验证后被推翻；本文以最终真机验证通过
的实现为准。

## 问题

iOS OpenCode 模式 session 列表存在三个叠加缺陷：

1. 每个项目只显示 1~3 条 session，远少于 OpenCode Desktop 的真实数量。
2. 没有「加载更多」入口，无法翻页。
3. 冷启动只加载字母序前 3 个项目，其余项目标题出现但 session 为空。

## 根因（三个独立缺陷叠加）

### 缺陷 1：MacBridge hasMore 逻辑对小项目是错的

OpenCode 路径原来没有全量列表，靠「返回数 >= limit」猜 hasMore。小项目返回 2~3 条
（< limit 5），hasMore=false，iOS 据此判定「已到末页」，「加载更多」入口永不出现。

对比 Codex/Claude 路径用 `paginateSessionList`：内存里有全量列表，用
`len(sessions) > limit` 算 hasMore 并返回可翻页的 nextCursor。

### 缺陷 2：rootsOnly 客户端丢弃把子 session 全砍了

MacBridge 原来在 Go 侧 `continue` 掉 parent_id 非空的 session。OpenCode 重度项目的
子 session（subagent、fork、compaction）比例高，砍完只剩 1~3 条 root。

### 缺陷 3：冷启动 .prefix(3) 只给前 3 个项目发请求

`loadSessions` 中 `.prefix(3)` 硬编码只加载前 3 个项目 bucket，其余靠 LazyVStack
视口懒加载，截图时多数项目没滚到。

## 最终修复方案

核心思路：让 OpenCode 路径走和 Codex/Claude 完全相同的 `paginateSessionList` 分页。

### MacBridge 侧（go-bridge）

`ocHandleListSessions`（handlers.go）重写：

- 对每个项目一次性从 OpenCode server 拉取 100 条 root session（常量
  `openCodeSessionFetchLimit = 100`），而不是之前的 limit=5。100 匹配 server 默认上限。
- `rootsOnly` 不再在 Go 侧客户端 `continue` 丢弃，而是作为 `roots=true` 查询参数
  发给 server，由 server 做 SQL `isNull(parent_id)` 过滤（和 OpenCode 源码一致）。
- 拉回后在内存按 `updatedAtMillis DESC` 排序，然后调用 `paginateSessionList(mapped,
  cursor, limit)`——与 Codex/Claude 走同一个函数。
- `hasMore` 和 `nextCursor` 由真实剩余数据量计算，不再瞎猜。
- `rootsOnly + cursor` 不再被拒绝。

`OpenCodeProxy.listSessions`（opencode-proxy.go）增加 `Roots bool` 字段，发 `roots=true`。

### iOS 侧（cordcode-ios）

`loadMoreOpenCodeSessions`（SessionsView.swift）改为 cursor 追加分页：

- 不再用「limit 加 5 重取」的旧方式。
- 用 bucket 已存的 `nextCursor` 发下一页请求，`append: true` 追加到已有 session 列表。
- 守卫条件改为 `bucket.hasMore && !bucket.isLoading && bucket.nextCursor 非空`。

侧栏（SidebarView.swift）上一轮已补的改动保持：项目区块进入视口自动触发
`loadOpenCodeBucketIfNeeded`；未加载项目显示「加载中」，加载完为空显示「暂无会话」。

### 为什么不直接用 OpenCode server 的 cursor

OpenCode server 的 `/api/session` 有 cursor（`packages/protocol/src/groups/session.ts`），
但 stable 1.17.13 的 `/session`（instance httpapi）是 array-only。MacBridge 连的是
instance httpapi。所以一次性拉 100 条再在内存分页是当前最正确的做法；未来上游
instance httpapi 支持 cursor 后可零改动切换到 server-side cursor。

## 验证

- `go test ./go-bridge/... -count=1` 全通过。
- `TestOpenCodeListSessionsFetchesLargePageAndPaginatesInMemory`：验证上游拉 100、
  roots=true、limit=2 切片返回 2 条且 hasMore=true，第二页 cursor 翻页返回剩余。
- iOS `SessionLoadOwnershipTests` 通过。
- Mac Release build + /Applications 覆盖安装 + runtime 8777 确认。
- iOS Debug build 安装到 iPhone。
- owner 真机验收通过（2026-07-03）：项目标题 basename、Chat 项目首页 5 条可翻页、
  小项目显示真实数量、go-bridge.log 无 ERROR。

## 设计文档中过时的部分

设计文档判断 OpenCode 为 array-only/no-cursor，因此采用保守 limit=5 + 客户端 rootsOnly
丢弃。实际读源码后发现 server 默认 limit 50/100 且支持 roots SQL 过滤，改为一口气拉 100
再内存分页更正确。完成情况文档 §4 的 Known Limits 中「无服务端加载更多是正确行为」
已被本方案推翻。
