# iOS 冷启动本地缓存:Session 列表秒开

> 仓库:`/Users/jacklee/Projects/cccode-ios`
> 创建:2026-06-14(修订版 v4,已采纳三轮评审全部意见)
> 状态:待实施

## 0. 评审意见处置记录

### 第一轮(`docs/2026-06-14-ios-cold-start-local-cache-review.md`)— 全部采纳

| 评审项 | 级别 | 处置 |
|--------|------|------|
| scope 竞态:缓存读取缺少 cancellation/scope 校验 | P1 | ✅ 采纳 |
| 不应新增 `refreshSessionsInBackground` 第二套状态机 | P1 | ✅ 采纳 |
| Phase 2 双门控分析不成立 | P1 | ✅ 采纳(删除门控修改,只加回归测试) |
| 缺少 snapshot store 依赖注入 | P1 | ✅ 采纳(第一轮注入实例;第二轮升级为协议,见下) |
| 删除 session tombstone 边界描述错误 | P2 | ✅ 采纳 |
| 清空 `projectRoots` 导致目录功能不完整 | P2 | ✅ 采纳 |
| `isShowingStaleCache` 混合多维度 | P2 | ✅ 采纳 |
| 空数组快照语义未定义 | P2 | ✅ 采纳 |
| request ID 不防实时事件覆盖 | P2 | ✅ 采纳 |
| 措辞/config 参数/重复标题/真机限定 | 文档 | ✅ 采纳 |

### 第二轮(`docs/2026-06-14-ios-cold-start-local-cache-review-round-2.md`)— 全部采纳

| 评审项 | 级别 | 处置 | 说明 |
|--------|------|------|------|
| scope 切换时旧列表在缓存读取期间继续显示 + event observer 启动过早 | P1 | ✅ 采纳 | await 前立即清空旧 scope UI;sessions/projectRoots/stale/loading 全部重置;event observer 延后到缓存恢复后启动 |
| 空数组缓存仍被现有 ViewModel/UI 重新解释为 loading | P1 | ✅ 采纳 | `shouldShowBlockingLoading = sessions.isEmpty && !isShowingStaleCache`;Sidebar/SessionsView 空状态分支同步改;补充空缓存 UI 契约 |
| store 注入需覆盖读写 + 改用最小协议才能稳定测试延迟读取竞态 | P1 | ✅ 采纳 | 引入 `SessionListSnapshotStoring` 协议;现有两处 `saveSessionListSnapshot` 也改用注入实例;测试 double 用 continuation 精确暂停 |
| stale 状态维护应基于来源,不根据 `sessions.isEmpty` 推断 | P2 | ✅ 采纳 | `isShowingStaleCache` 只在远端成功/cache miss 时置 false,缓存命中时置 true,失败保持原值 |
| `inferProjectRoots` 应以过滤后的 sessions 为输入 | P2 | ✅ 采纳 | 统一用 `visibleCachedSessions`;空列表也推导(至少合并 manualProjectDirectories) |
| Phase 3.3 编号不一致 | P2 | ✅ 采纳 | 移出本次 Phase 编号,写成"后续独立任务" |

### 第三轮(`docs/2026-06-14-ios-cold-start-local-cache-review-round-3.md`)— 全部采纳

| 评审项 | 级别 | 处置 | 说明 |
|--------|------|------|------|
| 等待新缓存期间旧 event task 和旧 loadSessions 未失效,仍可能污染新 scope | P1 | ✅ 采纳 | scope 切换第 2 步:取消 globalEventTask/autoReloadTask/sessionListRefreshTask/bridgeReadyReloadTask + `invalidateLoadRequests()`(递增 loadRequestID),再做清旧 UI |
| Bridge retryable 判断错误依赖缓存是否为空,与状态矩阵冲突 | P1 | ✅ 采纳 | catch 结构改为:error 类型决定 `isWaitingForBridge`;cache provenance 只决定 `isLoading`;不再用 `sessions.isEmpty` 决定 Bridge waiting |
| continuation 测试 double 示例不可实现 | P2 | ✅ 采纳 | 改用 actor 版 `ControllableSnapshotStore`,scope key 索引 continuation;声明行为要求而非不可实现的 `let` |
| 远端权威空列表的 Sidebar 分支应给出明确优先级 | P2 | ✅ 采纳 | 给出 6 级分支优先级;必须删除/收窄泛化空状态 spinner 分支 |
| 协议文件归属应固定 | P2 | ✅ 采纳 | 放 `Services/Storage/SessionListSnapshotStoring.swift` 或 `SessionSnapshotStore.swift`,不放 View 文件 |

## 1. 目标

冷启动 iOS App 后,Session 列表立即显示上次缓存的本地数据(秒开),然后调用现有远端刷新获取最新列表。点进 Session 时,消息内容已有秒开机制(磁盘快照),本任务为其补充回归测试,不改门控。

对标 ChatGPT / Telegram 的离线优先体验:网络不好时仍能看到列表,联网后自动刷新。

## 2. 现状诊断

### 2.1 Session 列表:只写不读(主要缺口)

| 组件 | 文件 | 状态 |
|------|------|------|
| `SessionSnapshotStore.saveSessionListSnapshot` | `Services/Storage/SessionSnapshotStore.swift:223` | ✅ 每次 `loadSessions` 成功后异步保存过滤排序后的 display sessions |
| `SessionSnapshotStore.loadSessionListSnapshot` | `Services/Storage/SessionSnapshotStore.swift:239` | ⚠️ **已实现但零调用(死代码)** |

**冷启动流程(当前)**:
```
App 启动
  → SessionsViewModel.initialize(config:)     [SessionsView.swift:516]
    → sessions = []                            [SessionsView.swift:525]  ← 直接清空
    → isLoading = true                         [SessionsView.swift:529]
    → loadSessions()                           [SessionsView.swift:541]
      → shouldShowBlockingLoading = sessions.isEmpty  [SessionsView.swift:1001]
      → isLoading = true(sessions 为空时)      [SessionsView.swift:1002-1004]
      → backendClient.fetchSessions()          [SessionsView.swift:1009] ← 同步等远端
      → 成功后 sessions = result               [SessionsView.swift:1029]
      → saveSessionListSnapshot(硬编码 .shared)[SessionsView.swift:1033]
```

**问题**:磁盘上明明有上次保存的 `session-list-<scopeHash>.json`,但 `loadSessionListSnapshot` 没人调用。用户看到的是 loading 转圈,直到远端返回。

### 2.2 单 Session 消息:已有秒开机制(本任务不改)

| 组件 | 状态 |
|------|------|
| `MessageCacheManager`(内存 LRU,20 session) | ✅ 运行时有效,冷启动后空 |
| `SessionSnapshotStore.loadSnapshot`(磁盘) | ✅ 被 `ChatViewModel.loadMessages` 读取,冷启动秒开已能工作 |
| `BridgeOfflineSnapshotAdapter` | ⚠️ **死代码,生产代码 0 调用,本任务不接** |

点进 Session 时,`loadMessages` 的 disk-snapshot 分支(`ChatViewModel+MessageSync.swift:54-80`)会先读磁盘、秒开、再远端对账。本任务**不修改消息快照门控**,只补回归测试。

### 2.3 关键文件索引

| 文件 | 作用 |
|------|------|
| `Views/Session/SessionsView.swift` | `SessionsViewModel`、`loadSessions`、`initialize`、session 列表 UI |
| `Views/Components/SidebarView.swift` | `SessionsSidebarView`(实际 UI 入口) |
| `ViewModels/ChatViewModel+SessionManagement.swift` | `switchSession`、`initialize`、session 切换 |
| `ViewModels/ChatViewModel+MessageSync.swift` | `loadMessages`、`persistSnapshot`、缓存读写 |
| `Services/Storage/SessionSnapshotStore.swift` | 磁盘快照 actor(列表 + 消息) |
| `Services/MessageCacheManager.swift` | 内存 LRU + 磁盘转发 |
| `Services/Backend/BackendModels.swift:133-152` | `BackendServerIdentity.cacheScopeKey` scope 隔离 |
| `App/OpenCodeiOSApp.swift:415-442` | 冷启动入口(`autoConnectOnLaunch`) |
| `App/ContentView.swift:382-495` | 主视图(无独立 splash,loading 内联) |

### 2.4 scope 隔离机制

- `BackendServerIdentity.cacheScopeKey` = `"backendKind|baseURL|username"`(`BackendModels.swift:149`)
- 文件名用 SHA-256(scopeKey)做 hash:
  - 列表:`session-list-<SHA256(scopeKey)>.json`
  - 消息:`snapshot-<SHA256(scopeKey)>-<SHA256(sessionId)>.json`
- 存储目录:`<Application Support>/SessionSnapshots/`
- **Bridge 模式**:`ServerConfig` 只含 host/port/backendKind(不含 bridgeId),scope key 由这些字段决定。同一 Mac 的不同 backend(codex/claude)产生不同 scope,互不干扰。
- **注意**:Bridge 重连前后 `cacheScopeKey` 是否稳定(relay URL 是否变化导致 host/port 变)需在 Phase 3 验证。若不稳定,缓存命中率会低,需另起任务处理(不能简单改 scope key,会影响现有缓存和非 Bridge server 的隔离)。

## 3. 设计原则

1. **离线优先,复用现有刷新**:UI 先显示本地缓存(如果有),然后调用**现有** `loadSessions` 刷新。不新增第二套加载状态机。
2. **scope 切换立即清旧 UI**:scope 变化时,在 `await` 缓存读取**之前**立即清空旧 scope 的 sessions/projectRoots/stale 状态。缓存读取期间不展示旧 scope 数据。
3. **event observer 延后启动**:新 scope 的 event observer 在缓存恢复**之后**启动,避免实时事件被缓存赋值覆盖。
4. **不改缓存文件格式**:复用现有 `SessionSnapshotStore` 的 JSON 结构。
5. **scope 安全**:缓存读取前后校验 scope,防止快速切换 backend 时旧 scope 缓存覆盖新 scope。
6. **不掩盖错误**:远端刷新失败仍要如实提示。
7. **可测试**:通过最小协议注入,让单元测试能控制延迟读取竞态,不依赖全局 `.shared`。

## 4. 实施方案

### Phase 1:Session 列表冷启动秒开(核心)

#### 1.1 引入最小协议 + 注入 `SessionListSnapshotStoring`

**文件**:`Services/Storage/SessionListSnapshotStoring.swift`(独立文件)或 `SessionSnapshotStore.swift` 内(**不放 View 文件**)

```swift
/// Session 列表快照的最小存储协议,用于依赖注入和测试 double。
protocol SessionListSnapshotStoring: Sendable {
    func loadSessionListSnapshot(baseConfig: ServerConfig?) async -> [Session]?
    func saveSessionListSnapshot(baseConfig: ServerConfig?, sessions: [Session]) async throws
}

extension SessionSnapshotStore: SessionListSnapshotStoring {}
```

`SessionsViewModel` 注入:
```swift
private let sessionListSnapshotStore: any SessionListSnapshotStoring

init(sessionListSnapshotStore: any SessionListSnapshotStoring = SessionSnapshotStore.shared) {
    self.sessionListSnapshotStore = sessionListSnapshotStore
    // existing observer registration...
}
```

**现有两处 `saveSessionListSnapshot` 也改用注入实例**(:1033, :1114):
```swift
// 改前:
Task { try? await SessionSnapshotStore.shared.saveSessionListSnapshot(baseConfig: config, sessions: sessions) }
// 改后:
Task { try? await self.sessionListSnapshotStore.saveSessionListSnapshot(baseConfig: config, sessions: sessions) }
```

测试 double(actor 版,用 scope key 索引 continuation,精确暂停指定 scope 的读取):
```swift
actor ControllableSnapshotStore: SessionListSnapshotStoring {
    private var pendingLoads: [String: CheckedContinuation<[Session]?, Never>] = [:]
    private var savedSnapshots: [String: [Session]] = [:]

    func loadSessionListSnapshot(baseConfig: ServerConfig?) async -> [Session]? {
        let scope = BackendServerIdentity(baseConfig: baseConfig).cacheScopeKey
        return await withCheckedContinuation { continuation in
            pendingLoads[scope] = continuation
        }
    }

    func saveSessionListSnapshot(baseConfig: ServerConfig?, sessions: [Session]) async throws {
        let scope = BackendServerIdentity(baseConfig: baseConfig).cacheScopeKey
        savedSnapshots[scope] = sessions
    }

    /// 测试调用:释放指定 scope 的挂起读取,返回给定结果。
    func resumeLoad(scope: String, with sessions: [Session]?) {
        pendingLoads.removeValue(forKey: scope)?.resume(returning: sessions)
    }
}
```

**测试注意**:必须确保所有 continuation 都被 resume,否则测试挂死。`tearDown` 中检查并清理。

#### 1.2 `initialize` 执行顺序:取消旧 task → 清旧 UI → 切配置 → 读缓存 → 校验 scope → 应用缓存 → 启动事件流 → `loadSessions`

**文件**:`Views/Session/SessionsView.swift`,`initialize(config:schedulesBridgeUnavailableRetry:)`(:516)

**执行顺序(关键,共 10 步)**:

```swift
private func initialize(config: ServerConfig, schedulesBridgeUnavailableRetry: Bool) async {
    let previousScopeKey = currentServerConfig?.backendIdentity.cacheScopeKey
    let requestedScopeKey = config.backendIdentity.cacheScopeKey
    let scopeChanged = previousScopeKey != requestedScopeKey

    // 1. 计算 scope change(上面已做)

    if scopeChanged {
        // 2. 先取消旧 scope 的所有异步工作,防止旧 event/loadSessions/retry 污染新 scope。
        //    必须在清旧 UI 之前做,否则旧 task 在清空与缓存读取的窗口里仍可写入。
        globalEventTask?.cancel()
        globalEventTask = nil
        autoReloadTask?.cancel()
        sessionListRefreshTask?.cancel()
        bridgeReadyReloadTask?.cancel()
        invalidateLoadRequests()  // 递增 loadRequestID,使旧 loadSessions 的 isLoadRequestCurrent 返回 false

        // 3. await 前:立即清除旧 scope 的 UI(不能继续展示旧列表)
        if let previousScopeKey {
            SessionRuntimeStateStore.shared.clearScope(previousScopeKey)
        }
        sessions = []
        projectRoots = []
        isShowingStaleCache = false
        isLoading = true
        errorMessage = nil
        isWaitingForBridge = false
        recentlyDeletedSessions.removeAll()
    }

    // 4. 完成配置切换(在 await 之前,确保 currentServerConfig 已更新用于 scope 校验)
    self.backendClient = backendClientFactory(config)
    self.currentServerConfig = config
    self.directoryService = DirectoryResolutionService(baseConfig: config)
    self.activeServerScopeKey = "..."
    self.directoryScopeKey = config.backendIdentity.cacheScopeKey
    self.manualProjectDirectories = loadManualProjectDirectories(for: activeServerScopeKey).sorted()
    saveManualProjectDirectories(self.manualProjectDirectories, for: activeServerScopeKey)

    // 5. scope 变化时:读缓存(actor 隔离,会 await)
    if scopeChanged {
        let cached = await sessionListSnapshotStore.loadSessionListSnapshot(baseConfig: config)
        // 6. 校验:如果期间用户切换了 backend,丢弃这次读取结果
        guard !Task.isCancelled,
              currentServerConfig?.backendIdentity.cacheScopeKey == requestedScopeKey else {
            return
        }
        // 7. 应用 nil/空/非空 cache 结果
        applyCachedResult(cached)
    }

    // 8. 缓存恢复完成后才启动新 scope 事件流,避免实时事件被缓存赋值覆盖
    startGlobalEventObserver(config: config)

    // 9. 调用现有 loadSessions(唯一远端刷新状态机)
    await loadSessions()

    // 10. 按 retryable error 类型设置 waiting;按 cache provenance 设置 blocking loading
    //     (见 1.3 的 catch 结构)
    // ...existing bridge retry logic (isLoading 用 sessions.isEmpty && !isShowingStaleCache)...
}
```

**新增 `invalidateLoadRequests`**:
```swift
private func invalidateLoadRequests() {
    loadRequestID &+= 1
}
```
递增 `loadRequestID` 使旧 `loadSessions` 的 `isLoadRequestCurrent(requestID)` 返回 false,旧远端响应不会写入 sessions。

**注意**:取消 `bridgeReadyReloadTask` 后不会丢失恢复能力——新 `initialize` 在远端失败时仍会按现有逻辑重新安排 retry。

**`applyCachedResult` 区分 nil 和空数组**:
```swift
private func applyCachedResult(_ cached: [Session]?) {
    guard let cachedSessions = cached else {
        // cache miss(无文件或解码失败):保持 isLoading = true,走原流程
        return
    }
    // 缓存命中(包括合法空数组)
    let visibleCachedSessions = filterCachedSessions(cachedSessions)
    sessions = visibleCachedSessions
    projectRoots = inferProjectRoots(from: visibleCachedSessions)
    syncRuntimeStatesFromSessions(sessions)
    isLoading = false
    isShowingStaleCache = true
}
```

#### 1.3 修改 `loadSessions` 的 blocking loading 判断 + stale 状态维护

**文件**:`Views/Session/SessionsView.swift`

**blocking loading 判断改为**(所有出现 `sessions.isEmpty` 控制 loading 的地方):
```swift
// 改前:
let shouldShowBlockingLoading = sessions.isEmpty
// 改后(空缓存命中时不 blocking):
let shouldShowBlockingLoading = sessions.isEmpty && !isShowingStaleCache
```

**stale 状态维护(基于来源,不根据 `sessions.isEmpty` 推断)**:
- 缓存命中(`applyCachedResult`):`isShowingStaleCache = true`
- cache miss(`applyCachedResult` 收到 nil):`isShowingStaleCache = false`(保持)
- scope 切换开始:`isShowingStaleCache = false`(待新 scope cache 命中后再置 true)
- `loadSessions` 远端成功(当前 request 的结果被应用):`isShowingStaleCache = false`
- `loadSessions` 远端失败:保持 `isShowingStaleCache` 原值(不改)

**catch 结构:error 类型决定 Bridge waiting,cache provenance 只决定 blocking loading**:

cache provenance 只应决定是否显示 blocking loading,不应决定 error 是否属于 Bridge waiting。现有 catch 中 `isBridgeReadyRetryableError(error), sessions.isEmpty` 的 `sessions.isEmpty` 必须移除,改为:

```swift
catch {
    // ... isLoadRequestCurrent guard ...
    if case OpenCodeError.unauthorized = error {
        isWaitingForBridge = false
        // handleUnauthorized()
    } else if isBridgeReadyRetryableError(error) {
        isWaitingForBridge = true
        isLoading = sessions.isEmpty && !isShowingStaleCache  // cache provenance 只控制 blocking loading
        errorMessage = nil
    } else {
        isWaitingForBridge = false
        isLoading = sessions.isEmpty && !isShowingStaleCache
        errorMessage = localizedErrorDescription(error)
    }
    if shouldShowBlockingLoading {
        isLoading = false
    }
}
```

**`initialize` 在 `loadSessions` 返回后的 retry 调度分支也使用**:
```swift
isLoading = sessions.isEmpty && !isShowingStaleCache
```

#### 1.4 缓存过滤 + 临时 projectRoots 推导

**文件**:`Views/Session/SessionsView.swift`

```swift
/// 对缓存数据应用与 loadSessions 一致的过滤+排序(防御性,缓存已是 display sessions)
private func filterCachedSessions(_ sessions: [Session]) -> [Session] {
    return sessions
        .filter { !$0.isArchived && !$0.isChildSession && !isSessionSuppressed($0.id) }
        .sorted { $0.updatedAt > $1.updatedAt }
}

/// 从过滤后的缓存 session 推导临时 projectRoots。
/// 即使 session 为空也合并 manualProjectDirectories。
/// 远端刷新成功后由现有逻辑整体替换。
private func inferProjectRoots(from visibleSessions: [Session]) -> [ProjectRoot] {
    let projectIDPrefix = currentServerConfig?.backendKind.rawValue ?? "backend"
    let inferred = deduplicateProjects(
        visibleSessions.compactMap { session -> ProjectRoot? in
            guard let directory = session.directory, !directory.isEmpty else { return nil }
            return ProjectRoot(id: "\(projectIDPrefix):\(directory)", directory: directory, name: nil)
        }
    )
    let manual = manualProjectDirectories.map { ProjectRoot(id: "manual:\($0)", directory: $0, name: nil) }
    return deduplicateProjects(inferred + manual)
}
```

#### 1.5 stale 状态标记

```swift
/// true = 当前列表来自本地缓存,远端尚未成功刷新(刷新中/Bridge 未就绪/刷新失败)
@Published private(set) var isShowingStaleCache: Bool = false
```

**状态转换**(结合 `isWaitingForBridge` 和 `errorMessage` 决定 UI 文案):

| 场景 | `isShowingStaleCache` | `isLoading` | `isWaitingForBridge` | `errorMessage` |
|------|----------------------|-------------|---------------------|----------------|
| 有缓存,刷新中 | true | false | false | nil |
| 有缓存,Bridge 未就绪 | true | false | true | nil |
| 有缓存,刷新失败 | true | false | false | 有值 |
| 空缓存,刷新中 | true | false | false | nil |
| 空缓存,Bridge 未就绪 | true | false | true | nil |
| 空缓存,刷新失败 | true | false | false | 有值 |
| 远端刷新成功 | false | false | false | nil |
| 无缓存,刷新中 | false | true | false | nil |

### Phase 2:UI 状态条 + 空缓存 UI 契约

**文件**:`Views/Components/SidebarView.swift`、`Views/Session/SessionsView.swift`

#### 2.1 有缓存时的状态条

当 `viewModel.isShowingStaleCache == true && !viewModel.sessions.isEmpty` 时,显示轻量状态条(不遮挡列表,不切回全屏 loading):

- 刷新中(`!isWaitingForBridge && errorMessage == nil`):`显示本地记录,正在刷新…`
- Bridge 未就绪(`isWaitingForBridge`):`显示本地记录,正在连接…`
- 刷新失败(`errorMessage != nil`):`无法刷新,当前为本地记录`

#### 2.2 空缓存 UI 契约(Sidebar + SessionsView 两个入口)

现有 Sidebar 空状态分支(`SidebarView.swift` 附近):
```swift
// 现有(会错误地对远端权威空列表显示"正在连接 Mac…"):
else if !viewModel.isLoading && viewModel.sessions.isEmpty && viewModel.errorMessage == nil {
    Text("正在连接 Mac…")
}
```

**必须删除或收窄此泛化分支**,按以下**优先级顺序**排列空状态分支(从高到低):

1. `isLoading == true`(无缓存 blocking loading)→ 全屏 ProgressView
2. `sessions.isEmpty && isShowingStaleCache && errorMessage != nil` → 空状态 + 刷新失败提示
3. `sessions.isEmpty && isShowingStaleCache && isWaitingForBridge` → 空状态 + `正在连接…`(非 spinner)
4. `sessions.isEmpty && isShowingStaleCache` → 空状态 + `暂无本地会话,正在刷新…`(非 spinner)
5. `sessions.isEmpty && !isShowingStaleCache && errorMessage == nil && !isWaitingForBridge` → 普通 `暂无会话`(远端权威空列表)
6. `!sessions.isEmpty` → 正常列表

| 空缓存场景 | UI |
|-----------|-----|
| 空缓存 + 刷新中 | 空状态 + `暂无本地会话,正在刷新…`(不显示 blocking spinner) |
| 空缓存 + Bridge 未就绪 | 空状态 + `正在连接…`(不显示 blocking spinner) |
| 空缓存 + 刷新失败 | 空状态 + 刷新失败提示 |
| 远端权威空列表成功 | 普通 `暂无会话`(不显示连接 spinner) |
| 无缓存 + 刷新中 | blocking loading(现有行为) |

`SessionsView` 的非 Sidebar 入口也需检查,保证两个入口语义不冲突。

### Phase 3:独立验证项(不绑定 Phase 1)

#### 3.1 Bridge scope key 稳定性验证

验证同一 `SavedBridge` 在本地/relay 重连前后的 `cacheScopeKey` 是否一致:
- Bridge 连接成功后,记录 `ServerConfig` 的 host/port → 计算 `cacheScopeKey`
- 断开重连,再次记录,比对

**若 key 稳定**:不改 identity,缓存正常命中。
**若不稳定**:另写迁移设计,不能直接把 scope 改成 bridgeId(会影响现有缓存、非 Bridge server 和多 backend 隔离)。

#### 3.2 消息秒开回归测试

为现有消息快照路径(`loadMessages` disk-snapshot 分支)补充回归测试,确保冷启动后点进 session 仍能秒开。**不修改门控逻辑**。

### 后续独立任务(不纳入本次 Phase 编号)

- **缓存新鲜度 metadata**:扩展 `SessionSnapshotStore` 记录保存时间。本任务不修改列表 JSON 格式。
- **持久化删除 tombstone**:如产品不接受"离线时已删除 session 重新出现",需另起任务实现。

## 5. 边界情况

### 5.1 首次安装(无缓存)
`loadSessionListSnapshot` 返回 nil → cache miss → `isLoading = true` → 走原流程等远端。无变化。

### 5.2 切换 backend / bridge(不同 scope)
scope 切换时:
1. **await 前**立即清空旧 scope 的 sessions/projectRoots/stale/loading(不展示旧列表)
2. 读新 scope 缓存,期间显示 loading(短暂)
3. 缓存命中 → 秒开新 scope;cache miss → 继续 loading
4. 旧 scope 的 `await` 读取若延迟返回,被 `Task.isCancelled` + scope 校验丢弃

### 5.3 已删除 session 在离线缓存中重新出现(已知限制)
**本任务不引入持久化删除 tombstone。** 冷启动时:
- `recentlyDeletedSessions` 是纯内存字典,新进程为空,`initialize` 还会清空它
- 缓存中可能包含已在别处(如 Mac 端)删除的 session
- 离线时这些 session 会重新出现,点击后可能加载失败
- 远端刷新成功后会被最新数据整体替换,自动消失

如果产品不接受该行为,必须把 tombstone 持久化纳入范围(后续独立任务)。

### 5.4 缓存与远端数据冲突
远端刷新成功后,`sessions` 被远端数据**整体替换**(现有 `loadSessions` 行为)。不做 merge。实时事件(`upsertSession`)的增量逻辑不受影响。

**已知竞态**(现有行为,本任务不修复):全量请求进行中收到 `sessionCreated` 实时事件插入新 session 后,较早开始的全量请求可能返回旧列表覆盖。沿用现有最终一致性行为,后续事件或自动 reload 会再次收敛。

### 5.5 空数组缓存
`saveSessionListSnapshot` 会保存空数组(上次远端权威结果就是空列表)。`loadSessionListSnapshot` 返回 `[]`(非 nil)视为合法缓存命中:
- `isShowingStaleCache = true`,`isLoading = false`
- `loadSessions` 的 `shouldShowBlockingLoading = sessions.isEmpty && !isShowingStaleCache` = false
- UI 显示空状态 + 刷新提示(非 blocking spinner)
- `nil` 表示 cache miss(无文件或解码失败)

### 5.6 Bridge 未就绪(最受益场景)
冷启动时 Bridge 还在连接。此时:
- 读缓存 → 显示本地列表(秒开)
- 调用现有 `loadSessions` → `BridgeUnavailableBackendClient` 抛错 → `isWaitingForBridge = true` → 状态条显示"正在连接…"
- Bridge 连上后 → `bridgeBackendReady` 通知 → `scheduleBridgeReadyReload` → 再次 `loadSessions` 刷新

### 5.7 缓存解码失败
`loadSessionListSnapshot` 内部 try-catch 返回 nil(:247-250)。不会删除损坏文件(与消息快照不同)。nil 触发 cache miss 分支,走原流程 loading。如需修复损坏文件,另起任务。

## 6. 验收标准

### 6.1 功能验收(真机手动,仅修改 iOS 代码且设备已连接时)

| 场景 | 预期 |
|------|------|
| 冷启动(Bridge 未连上) | 立即显示上次缓存的 session 列表,状态条"正在连接…" |
| 冷启动(Bridge 已连上) | 立即显示缓存列表,状态条"正在刷新…",刷新后消失 |
| 首次安装(无缓存) | 显示 blocking loading,与当前行为一致 |
| 点进之前看过的 session | 秒开(磁盘快照,现有机制),后台刷新消息 |
| 网络断开冷启动 | 显示缓存列表,状态条"无法刷新,当前为本地记录" |
| 快速切换 backend | 切换瞬间旧列表消失(短暂 loading);新 scope 有缓存则秒开,旧 scope 延迟读取被丢弃 |
| 上次远端列表为空 | 空数组视为缓存命中,显示空状态 + 刷新提示(非 blocking spinner) |

### 6.2 自动化验收(定向 Swift unit test,不跑 UI test/snapshot test)

在现有 `SessionsViewModelServerSwitchTests` 附近增加:

1. `testInitialize_cacheHitPublishesSessionsBeforeRemoteCompletes` — 缓存命中时,远端完成前 sessions 已填充
2. `testInitialize_emptyCacheIsAValidCachedResult` — 空数组缓存不触发 blocking loading,`isShowingStaleCache = true`
3. `testInitialize_cacheMissKeepsBlockingLoading` — nil 缓存触发 blocking loading
4. `testInitialize_remoteSuccessReplacesCachedSessions` — 远端成功后替换缓存,`isShowingStaleCache = false`
5. `testInitialize_remoteFailurePreservesCachedSessions` — 远端失败后保留缓存,`isShowingStaleCache` 保持 true
6. `testInitialize_bridgeUnavailableWithCacheSetsWaitingNotLoading` — 非空缓存 + `bridgeUnavailable` → `isWaitingForBridge = true`、`isLoading = false`、`errorMessage = nil`
7. `testInitialize_bridgeUnavailableWithEmptyCacheSetsWaitingNotLoading` — 空缓存 + `bridgeUnavailable` → `isWaitingForBridge = true`、`isLoading = false`、`errorMessage = nil`
8. `testInitialize_bridgeUnavailableWithCacheMissSetsWaitingAndLoading` — cache miss + `bridgeUnavailable` → `isWaitingForBridge = true`、`isLoading = true`、`errorMessage = nil`
9. `testInitialize_nonRetryableErrorWithCacheShowsError` — 非 retryable error + 缓存 → `isWaitingForBridge = false`、`isLoading = false`、`errorMessage` 有值
10. `testInitialize_scopeSwitchLoadsOnlyNewScopeCache` — 切换 backend 只读新 scope 缓存
11. `testInitialize_staleCacheReadCannotOverwriteNewerScope` — 配置 A 的缓存读取延迟返回(用 `ControllableSnapshotStore` continuation 控制),不覆盖已切换到 B 的 ViewModel。**断言:配置 B 初始化开始后,即使 B 的缓存读取尚未返回,也绝不能继续看到 A 的 sessions**
12. `testInitialize_staleRemoteLoadCannotOverwriteNewerScope` — 配置 A 的远端 `loadSessions` 延迟返回,切换到 B 并暂停 B 的缓存读取;释放 A 的远端响应,断言不能写入 B
13. `testInitialize_staleEventCannotModifyNewerScope` — 配置 A 的 event stream 在切换 B 后发送事件,断言不能修改 B 的列表
14. `testInitialize_cachedSessionsPrimeTemporaryProjectRoots` — 缓存命中后 projectRoots 从过滤后的 session directory 推导
15. `testInitialize_corruptCacheFallsBackToRemoteLoading` — 损坏文件返回 nil,走 loading

现有测试调整:
- `testInitialize_serverSwitchClearsStaleSessionsBeforeReload` — 新语义:切换后立即不保留旧 scope 列表(即使在缓存读取期间);有新 scope 缓存则显示新缓存,否则为空并 loading
- Bridge ready retry 系列 — 有缓存时断言 `isLoading == false`;无缓存时保持当前断言
- root-only 与 non-root-only session 过滤测试 — 确保缓存恢复使用与已保存 display snapshot 一致的可见性规则

## 7. 实施顺序

1. **Phase 1.1**:引入 `SessionListSnapshotStoring` 协议(放 `Services/Storage/`),注入 ViewModel,现有两处 save 也改用注入实例
2. **Phase 1.2**:`initialize` 执行顺序改为:取消旧 task + `invalidateLoadRequests` → 清旧 UI → 切配置 → 读缓存 + scope 校验 → 应用缓存 → 启动事件流 → `loadSessions`
3. **Phase 1.3**:`loadSessions` blocking 判断改为 `sessions.isEmpty && !isShowingStaleCache`;catch 结构改为 error 类型决定 Bridge waiting、cache provenance 只决定 blocking loading;stale 状态维护基于来源
4. **Phase 1.4-1.5**:过滤辅助 + 临时 projectRoots + stale 标记
5. **Phase 2**:UI 状态条 + 空缓存 UI 契约(Sidebar + SessionsView 两个入口,6 级分支优先级)
6. **Phase 3.1-3.2**:scope 稳定性验证 + 消息秒开回归测试

每个 Phase 完成后跑定向 Swift unit test + 定向 build。修改 iOS 代码且设备已连接时,装真机做冷启动手动验证。

## 8. 不做的事

- **不引入新缓存框架**(CoreData / Realm / SQLite)。现有 JSON 文件够用。
- **不新增 `refreshSessionsInBackground` 方法**。复用现有 `loadSessions`。
- **不修改消息快照门控**(`switchSession` / `initialize` 的 `shouldUseSnapshotMode`)。现有消息秒开已能工作,只补回归测试。
- **不接 `BridgeOfflineSnapshotAdapter`**。死代码,且 `ChatViewModel` 已通过真实 config 走正确 scope。
- **不改 `loadSessions` 的整体替换为 merge**。整体替换简单可靠。
- **不引入持久化删除 tombstone**。如实描述已知限制(后续独立任务)。
- **不修改列表 JSON 格式 / 加新鲜度 metadata**(后续独立任务)。
- **不加 push notification / 后台静默刷新(BGTaskScheduler)**。本次只做冷启动时的本地缓存读取。

## 9. 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| Bridge scope key 不稳定 → 缓存命中率低 | Phase 1 收益打折 | Phase 3.1 验证;若不稳定另起任务,不直接改 scope key |
| 缓存数据过期严重(很久没打开) | 用户看到很旧的列表 | 远端刷新成功后整体替换;新鲜度提示为后续独立任务 |
| `loadSessionListSnapshot` 解码失败 | 回退到 blocking loading | 返回 nil,走原流程;不删除损坏文件 |
| 快速切换 backend,旧 scope 缓存/event/loadSessions 延迟返回覆盖新 scope | 显示错误列表 | scope 切换时取消旧 event/retry/refresh task + `invalidateLoadRequests()`(递增 loadRequestID);await 前立即清旧 UI;`requestedScopeKey` + `Task.isCancelled` + scope 比对;event observer 延后到缓存恢复后启动 |
| 全量请求与实时事件竞争覆盖 | 可能短暂列表闪烁/数据回退 | 沿用现有最终一致性行为(本任务不修复) |
| 离线时已删除 session 重新出现 | 用户困惑 | 如实描述;远端刷新后消失;tombstone 为后续独立任务 |

## 10. 相关文档

- 第一轮评审:`docs/2026-06-14-ios-cold-start-local-cache-review.md`
- 第二轮评审:`docs/2026-06-14-ios-cold-start-local-cache-review-round-2.md`
- 第三轮评审:`docs/2026-06-14-ios-cold-start-local-cache-review-round-3.md`
- Session loading 系统性重设计(分页/索引):`docs/2026-06-13-session-loading-systemic-redesign.md`
- 锚点跳底修复复盘:`handoffs/anchor-fix-final-state.md`
