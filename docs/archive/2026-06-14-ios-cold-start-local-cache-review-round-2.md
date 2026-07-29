# iOS 冷启动本地缓存实施计划第二轮评审

> 评审对象:`docs/2026-06-14-ios-cold-start-local-cache.md` 修订版 v2
> 评审日期:2026-06-14
> 对照代码仓库:`/Users/jacklee/Projects/cccode-ios`
> 首轮评审:`docs/2026-06-14-ios-cold-start-local-cache-review.md`
> 结论:**首轮意见已实质采纳，但仍有 3 个 P1 需要修订，之后才适合直接交付实施**

## 1. 总体评价

第二版相较首版有明显改善。首轮指出的以下问题已经正确处理:

- 不再复制 `loadSessions` 状态机。
- 明确加入 scope/cancellation 校验。
- 删除了不成立的消息“双门控”修改。
- 明确依赖注入方向。
- 如实记录 tombstone 限制。
- 增加缓存 project roots 推导。
- 补充 stale 状态矩阵。
- 区分 nil cache miss 与合法空数组。
- 修正实时事件与全量响应的风险描述。
- 验收测试和不做事项已经足够具体。

核心实施路线现在是正确的。但逐句模拟伪代码后，仍有三个会直接导致错误行为或不可稳定测试的问题。它们都集中在 Phase 1 的执行细节，不需要重新设计架构。

## 2. 阻塞问题

### [P1] scope 切换时旧列表会在缓存读取期间继续显示，event observer 也启动得过早

修订稿要求在缓存读取前完成:

```swift
self.backendClient = backendClientFactory(config)
self.currentServerConfig = config
// ...
startGlobalEventObserver(config: config)
let cached = await sessionSnapshotStore.loadSessionListSnapshot(baseConfig: config)
```

但旧 scope 的 `sessions` 和 `projectRoots` 直到缓存读取返回后才会被替换或清空。因为读取是 `await`，在这段时间里 UI 已经处于新 config，却仍展示旧 backend 的 session 列表。这违反“切换后不能串 scope”的核心要求。

同时，新的 global event observer 在缓存读取前启动。如果新 scope 的 `sessionCreated` 事件先到，事件插入的数据随后可能被缓存赋值整体覆盖:

1. 配置切换到 B；
2. 启动 B 的 event stream；
3. 等待 B 的磁盘缓存；
4. B 的实时事件插入 session；
5. 缓存读取返回；
6. `sessions = cachedSessions` 覆盖刚收到的事件。

**必须修订执行顺序:**

```swift
let scopeChanged = previousScopeKey != requestedScopeKey

if scopeChanged {
    if let previousScopeKey {
        SessionRuntimeStateStore.shared.clearScope(previousScopeKey)
    }
    // await 前立即移除旧 scope UI，不能继续展示旧列表。
    sessions = []
    projectRoots = []
    isShowingStaleCache = false
    isLoading = true
    errorMessage = nil
    isWaitingForBridge = false
    recentlyDeletedSessions.removeAll()
}

self.backendClient = backendClientFactory(config)
self.currentServerConfig = config
self.directoryService = DirectoryResolutionService(baseConfig: config)
// 设置 scope 和 manual directories

if scopeChanged {
    let cached = await sessionListSnapshotStore.loadSessionListSnapshot(baseConfig: config)
    guard !Task.isCancelled,
          currentServerConfig?.backendIdentity.cacheScopeKey == requestedScopeKey else {
        return
    }
    applyCacheResult(cached)
}

// 缓存恢复完成后再启动新 scope 的事件流，避免事件被缓存覆盖。
startGlobalEventObserver(config: config)
await loadSessions()
```

如果产品希望缓存读取期间完全没有 loading 闪烁，应该优化 store 读取速度或引入预加载，而不能继续展示旧 scope 数据。

对应测试应增加断言:配置 B 初始化开始后，即使 B 的缓存读取尚未返回，也绝不能继续看到 A 的 sessions。

### [P1] “空数组是缓存命中且不 blocking loading”与现有代码和 UI 冲突

文档正确规定:

```text
[] = 合法缓存命中
nil = cache miss
```

但伪代码把 `sessions = []` 后设为:

```swift
isLoading = false
isShowingStaleCache = true
```

随后调用现有 `loadSessions()`。现有实现会重新计算:

```swift
let shouldShowBlockingLoading = sessions.isEmpty
if shouldShowBlockingLoading {
    isLoading = true
}
```

因此空数组缓存仍会立刻回到 blocking loading，和文档的验收标准相反。仅复用现有方法并不能天然处理空缓存命中，必须让 blocking 判断考虑缓存是否已经恢复:

```swift
let shouldShowBlockingLoading = sessions.isEmpty && !isShowingStaleCache
```

失败分支中的 Bridge 判断也存在同一问题:

```swift
isBridgeReadyRetryableError(error), sessions.isEmpty
```

它不能区分“无缓存”与“合法空缓存”。相关 loading/waiting 判断应使用 cache provenance，而不是 `sessions.isEmpty`。

UI 也必须同步修改。当前 Sidebar 分支是:

```swift
else if !viewModel.isLoading
    && viewModel.sessions.isEmpty
    && viewModel.errorMessage == nil {
    Text("正在连接 Mac…")
}
```

所以即使 ViewModel 保持 `isLoading = false`，合法空缓存仍会显示无限连接 spinner。文档中的状态条又限定:

```swift
isShowingStaleCache == true && !sessions.isEmpty
```

空缓存永远看不到该状态条。

**必须补充空缓存 UI 契约:**

- 空缓存 + 刷新中:显示“暂无本地会话，正在刷新…”或普通空状态附刷新提示，不显示 blocking spinner。
- 空缓存 + Bridge 未就绪:显示空状态和“正在连接…”提示。
- 空缓存 + 刷新失败:显示空状态和刷新失败提示。
- 远端权威空列表成功:显示普通“暂无会话”，不显示连接 spinner。

`SessionsView` 的非 Sidebar 入口也应检查，因为它根据 `isLoading`/空列表显示 empty state；至少要保证两个入口语义不冲突。

### [P1] store 注入方案不完整，且无法实现计划中的延迟读取竞态测试

文档只写了:

```swift
private let sessionSnapshotStore: SessionSnapshotStore
```

并计划用它读取缓存。但 `loadSessions` 当前两个保存调用仍然硬编码:

```swift
SessionSnapshotStore.shared.saveSessionListSnapshot(...)
```

如果不一起替换:

- 测试从临时 store 读取，却把远端结果写入真实 Application Support；
- 测试会污染开发机全局缓存；
- 无法验证远端成功后写回同一注入 store；
- “依赖注入、不依赖 `.shared`”的目标没有真正完成。

**必须明确:**现有两处 `saveSessionListSnapshot` 也改用注入实例。

更重要的是，测试 8 要求:

> 配置 A 的缓存读取延迟返回，不覆盖已切换到 B 的 ViewModel。

具体的 `SessionSnapshotStore` actor 不能被继承，也没有延迟 hook。仅注入一个临时目录实例无法确定性地控制读取完成顺序。靠大文件或调度时机制造竞态会产生不稳定测试。

应采用一个最小协议:

```swift
protocol SessionListSnapshotStoring: Sendable {
    func loadSessionListSnapshot(baseConfig: ServerConfig?) async -> [Session]?
    func saveSessionListSnapshot(baseConfig: ServerConfig?, sessions: [Session]) async throws
}

extension SessionSnapshotStore: SessionListSnapshotStoring {}
```

然后 `SessionsViewModel` 注入 `any SessionListSnapshotStoring`。测试 double 可以用 continuation 精确暂停 A 的读取，先完成 B，再释放 A。

如果不希望增加协议，则必须删除“延迟读取顺序”的自动化验收，改成无法证明竞态防护的较弱测试。鉴于 scope 串数据属于高风险问题，建议保留测试并采用小协议。

## 3. 非阻塞修订

### [P2] stale 状态维护应基于来源，不应再用 `sessions.isEmpty` 推断

Phase 1.3 写道:

> 失败出口:如果有缓存数据(`!sessions.isEmpty`)，保留 `isShowingStaleCache = true`

这再次排除了合法空缓存。`isShowingStaleCache` 本身已经是“是否展示过缓存”的来源标记，失败时不应根据列表数量重新推断。

建议规则:

- 只有远端成功应用当前 request 的结果时置 `false`。
- cache miss 初始化时置 `false`。
- 缓存命中后置 `true`，任何远端失败都保持原值。
- scope 切换开始时先置 `false`，待新 scope cache 命中后再置 `true`。

### [P2] `inferProjectRoots` 应以过滤后的 sessions 为输入

伪代码当前执行:

```swift
sessions = filterCachedSessions(cachedSessions)
projectRoots = inferProjectRoots(from: cachedSessions, config: config)
```

这会从随后被过滤掉的 archived/child session 中推导 project root。应统一使用:

```swift
let visibleCachedSessions = filterCachedSessions(cachedSessions)
sessions = visibleCachedSessions
projectRoots = inferProjectRoots(from: visibleCachedSessions, config: config)
```

同时不需要用 `if !cachedSessions.isEmpty` 包住 project roots 推导；即使 session 为空，也应至少合并 `manualProjectDirectories`。

### [P2] Phase 编号和风险表有一处小的不一致

风险表写“Phase 3.3 新鲜度提示”，正文对应的是 `3.3`，实施顺序只列到 `3.1-3.2`，而正文标题称“另起任务”。建议直接从本次 Phase 编号中移除，写成“后续独立任务”，避免开发 agent 把它误当作本轮遗漏工作。

## 4. 已通过的部分

以下内容第二轮无需再改:

- 根因“列表快照只写不读”成立。
- 复用唯一 `loadSessions` 的方向正确。
- 不修改消息快照门控正确。
- 不接 `BridgeOfflineSnapshotAdapter` 正确。
- scope key 稳定性独立验证正确。
- tombstone 限制描述准确。
- 远端成功整体替换、不做 merge 的范围正确。
- 10 项测试覆盖面基本合理。
- 不运行 UI/snapshot automation 的约束符合项目要求。

## 5. 建议的最终实施骨架

```swift
if scopeChanged {
    clearOldScopePresentationImmediately()
}

configureForRequestedScope(config)

if scopeChanged {
    let cached = await sessionListSnapshotStore.loadSessionListSnapshot(baseConfig: config)
    guard requestStillTargets(config) else { return }
    applyCachedResult(cached) // nil 和 [] 必须分开
}

startGlobalEventObserver(config: config)
await loadSessions()
```

`loadSessions` 保持唯一远端状态机，但其 blocking 条件应调整为:

```swift
let shouldShowBlockingLoading = sessions.isEmpty && !isShowingStaleCache
```

并且所有 session-list snapshot 的读写都必须经过同一个注入的 `SessionListSnapshotStoring`。

## 6. 最终结论

第二版已经从“需要重写实施路径”进展到“只需修正执行细节”。剩余三个 P1 都是局部改动:

1. scope change 时先清旧 UI，缓存恢复后再启动事件流；
2. 让合法空缓存贯穿 ViewModel 和 Sidebar 状态判断；
3. 完整注入读写 store，并提供可控制延迟的测试 double。

完成这三点后，文档可以直接交给开发 agent 实施，不需要再次做架构分析。
