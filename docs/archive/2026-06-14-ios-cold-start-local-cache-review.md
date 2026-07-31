# iOS 冷启动本地缓存实施计划评审

> 评审对象:`docs/2026-06-14-ios-cold-start-local-cache.md`
> 评审日期:2026-06-14
> 对照代码仓库:`/Users/jacklee/Projects/cccode-ios`
> 结论:**需修订后再交付实施**

## 1. 总体结论

文档对核心缺口的判断正确:

- `SessionSnapshotStore.saveSessionListSnapshot` 已在远端列表加载成功后写盘。
- `loadSessionListSnapshot` 当前没有生产调用点。
- 冷启动接入列表快照读取，确实能显著改善 Bridge 尚未就绪时的首屏体验。
- 消息快照已有完整读盘、展示和远端对账路径，本任务不应另接 `BridgeOfflineSnapshotAdapter`。

但当前版本还不能按“开发 agent 无需二次分析、照着改即可”的标准交付。主要原因不是核心方向错误，而是实施细节绕开了现有状态机，并遗漏了异步 scope 竞态、测试依赖注入和若干缓存展示语义。Phase 2 对消息门控的分析也与实际执行路径不符。

建议保留 Phase 1 的目标，重写具体实施步骤；Phase 2 从本计划移除或改成独立调查项；Phase 3 保持独立验证，不与列表秒开绑定。

## 2. 主要问题

### [P1] 缓存读取缺少 scope/current-request 校验，快速切换 backend 时可能展示错误列表

原计划在 `initialize` 的 scope-change 分支中直接执行:

```swift
let cached = await SessionSnapshotStore.shared.loadSessionListSnapshot(baseConfig: config)
sessions = cached
```

`loadSessionListSnapshot` 是 actor 隔离调用，`await` 会让出执行权。如果配置 A 的读取尚未返回，用户又切换到配置 B，A 的旧任务仍可能恢复执行并把 A 的列表写进当前 B 的 ViewModel。

现有远端加载通过 `loadRequestID` 防止旧请求覆盖新请求，但计划中的缓存读取发生在 `beginLoadRequest()` 之前，不受该机制保护。文档风险表声称 request ID 可以处理刷新竞争，也不能覆盖这条新增路径。

**必须修订:**

- 在读取前捕获 `requestedScopeKey`。
- 读取后检查 `Task.isCancelled`。
- 读取后检查 `currentServerConfig?.backendIdentity.cacheScopeKey == requestedScopeKey`。
- 最好让缓存恢复和远端刷新共享同一个 initialization generation，而不是各自建立半套防护。

建议结构:

```swift
let requestedScopeKey = config.backendIdentity.cacheScopeKey
self.currentServerConfig = config
// 先完成 client、directory service 和 scope 字段切换

let cached = await sessionSnapshotStore.loadSessionListSnapshot(baseConfig: config)
guard !Task.isCancelled,
      currentServerConfig?.backendIdentity.cacheScopeKey == requestedScopeKey else {
    return
}

applyCachedSessions(cached)
await loadSessions()
```

必须新增“配置 A 的缓存读取延迟返回，但配置 B 已初始化”测试，断言 A 的缓存不能覆盖 B。

### [P1] 不应新增第二套 `refreshSessionsInBackground` 状态机

文档建议复制现有 `loadSessions`，新增 `refreshSessionsInBackground(config:)`，只改变 loading 和 stale 状态。这会复制以下复杂行为:

- root-only 与 root/global/project-scoped 两条抓取路径；
- `loadRequestID` 去重；
- Bridge retryable error 分类；
- `isWaitingForBridge` 状态；
- project roots 推导；
- runtime state 同步；
- directory store prime；
- snapshot 保存；
- unauthorized 处理。

这类复制很容易在后续修复中发生行为漂移。

实际上现有 `loadSessions` 已经支持非阻塞刷新:

```swift
let shouldShowBlockingLoading = sessions.isEmpty
if shouldShowBlockingLoading {
    isLoading = true
}
```

只要缓存先填入 `sessions`，随后调用现有 `await loadSessions()` 就不会显示 blocking loading；SwiftUI 也会在 actor 让出执行权后立即渲染缓存。无需为了“后台”语义再造方法。

**必须修订:**

- 删除 Phase 1.2 的新方法设计。
- 继续使用唯一的 `loadSessions(forceRefresh:)`。
- 在 `loadSessions` 成功和失败出口维护缓存展示状态，或将其建模为更明确的 refresh state。

最小改动应是“initialize 先恢复缓存，再走原 `loadSessions`”，而不是复制加载实现。

### [P1] Phase 2 的“双门控”分析不成立，不应按文档方案修改

文档认为 `switchSession` 的:

```swift
let shouldUseSnapshotMode =
    !didChangeServerConfig && shouldOpenSessionFromSnapshot(sessionId: sessionId)
```

与 `initialize` 中传给 `loadMessages` 的门控不一致，并建议删除 `!didChangeServerConfig`。

实际执行路径是:

1. `switchSession` 根据该值决定是否在调用 `initialize` 前清空一次 `messages`。
2. `initialize` 随后无条件执行 `self.messages = []`。
3. `initialize` 接着无条件尝试 `messageCacheManager.getCachedMessages(...)`。
4. `loadMessages` 再根据 `shouldOpenSessionFromSnapshot` 读取磁盘并远端对账。

因此 `switchSession` 的局部门控并不控制最终是否读取快照。删除 `!didChangeServerConfig` 也不能实现文档宣称的行为，只会改变进入 `initialize` 前极短时间内是否保留旧消息。

此外，文档提出给 `shouldOpenSessionFromSnapshot` 增加 `baseConfig` 参数，但示例实现没有使用该参数，也没有增加任何 scope 判断，属于无效 API 扩张。

**必须修订:**

- 从本实施计划删除 Phase 2.1。
- 若要清理消息初始化逻辑，应另开任务，先定义 `switchSession` 到 `initialize` 之间是否允许保留旧 UI，以及 `initialize` 为什么无条件清空。
- 本次只为现有消息秒开路径增加回归测试，不修改门控。

### [P1] 自动化验收无法按计划稳定实现，缺少 snapshot store 依赖注入

`SessionsViewModel` 当前只允许注入 `backendClientFactory`，计划中的实现直接调用:

```swift
SessionSnapshotStore.shared
```

这会让单元测试依赖真实 Application Support 目录和全局共享状态。现有 `SessionSnapshotStore` 支持传入临时目录，测试已经使用该能力，但 `SessionsViewModel` 无法接收该实例。

**必须修订:**

- 给 `SessionsViewModel` 注入最小存储依赖。
- 可以直接注入 `SessionSnapshotStore`，无需为了一个调用新建大协议。
- 默认值仍为 `.shared`，测试传入基于临时目录的 store。

例如:

```swift
private let sessionSnapshotStore: SessionSnapshotStore

init(sessionSnapshotStore: SessionSnapshotStore = .shared) {
    self.sessionSnapshotStore = sessionSnapshotStore
    // existing observers
}
```

测试还需要能够制造延迟读取。如果 actor 本身不便控制，可以提取一个只有列表 load/save 的小协议，但只有确实需要竞态测试时才引入。

### [P2] “删除 session 后冷启动”的边界说明错误

文档称缓存中的已删除 session 会被 `isSessionSuppressed` 处理，但冷启动时:

- `recentlyDeletedSessions` 是纯内存字典；
- 新进程启动后该字典为空；
- `initialize` 还会在 scope change 时清空它。

因此离线冷启动时，已删除但仍留在快照中的 session 会重新出现，并可能一直存在到远端刷新成功。当前设计没有持久化 tombstone，过滤辅助方法无法解决此问题。

这不一定阻塞 Phase 1，但文档必须如实描述:

- 缓存是最后一次成功远端列表的快照；
- 离线时可能短暂或持续显示已在别处删除的 session；
- 点击该 session 后可能加载失败；
- 本任务不引入持久化删除 tombstone。

如果产品不接受该行为，必须把 tombstone 持久化纳入范围。

### [P2] 清空 `projectRoots` 会让缓存展示阶段的目录功能不完整

计划在恢复缓存后仍执行:

```swift
projectRoots = []
```

侧边栏分组能从 session 自身的 directory 临时构造，因此列表外观看起来基本正常；但 `directoryOptionsForNewSession` 和 `defaultDirectoryForNewSession` 依赖 `projectRoots`。Bridge 长时间离线时，用户虽然能看到缓存 session，却可能丢失新建 session 的目录选项。

**建议修订:**

- 从缓存 session 推导临时 `ProjectRoot`，复用现有 root-only 分支的推导规则。
- 再与 `manualProjectDirectories` 合并。
- 远端刷新成功后仍由现有逻辑整体替换。

### [P2] `isShowingStaleCache` 把两个维度混成一个布尔值

文档希望一个标记同时表达:

- 当前列表来自缓存；
- 当前正在刷新；
- 最近一次刷新失败；
- 数据可能陈旧。

这些状态的 UI 文案不同。尤其远端失败后，“正在刷新…”不能继续显示；非网络错误也不应统一显示“离线”。

最低限度需要明确状态转换:

| 状态 | cache visible | loading | waiting/error |
|------|---------------|---------|---------------|
| 缓存命中，刷新中 | true | false | none |
| 缓存命中，Bridge 未就绪 | true | false | waiting |
| 缓存命中，刷新失败 | true | false | error |
| 远端刷新成功 | false | false | none |
| 无缓存，刷新中 | false | true | none |

可以继续使用布尔值，但 UI 文案必须结合 `isWaitingForBridge` 和 `errorMessage`。更清晰的方案是私有 enum，不过本次不必为了类型纯度扩大改动。

### [P2] 空数组快照的语义未定义

`saveSessionListSnapshot` 会正常保存空数组。原计划用 `!cached.isEmpty` 判断缓存命中，因此“上次远端权威结果就是空列表”会被当作无缓存，继续显示 blocking loading。

需要明确产品语义:

- `nil` 表示 cache miss/解码失败；
- `[]` 表示存在一份权威的空列表快照。

建议将非 nil 都视为缓存命中。若担心旧错误曾写入空数组，应通过新鲜度或写入条件解决，不应把合法空列表和 cache miss 混为一谈。

### [P2] request ID 不能防止实时事件被稍后的全量响应覆盖

风险表称 `loadSessions` 的 request ID 已处理后台刷新与实时事件竞争。实际 `loadRequestID` 只用于区分多个 `loadSessions` 请求；`upsertSession` 等实时事件不会递增 request ID。

可能顺序:

1. 开始远端全量请求；
2. 收到 `sessionCreated`，实时插入新 session；
3. 较早开始的全量请求返回旧列表；
4. `self.sessions = displaySessions` 覆盖刚插入的事件数据。

这是现有行为，不必在本任务修复，但风险表不能声称已由 request ID 解决。应改为“沿用现有最终一致性行为，后续事件或自动 reload 会再次收敛”。

## 3. 建议的修订实施方案

### Phase 1:最小闭环

1. 为 `SessionsViewModel` 注入 `SessionSnapshotStore`。
2. scope 变化时先完成当前配置、client 和目录服务切换。
3. 读取当前 scope 的 session list snapshot。
4. 读取返回后校验 cancellation 和 current scope。
5. cache 非 nil 时立即应用，包括合法空数组。
6. 从缓存 session 推导临时 project roots，并同步 runtime states。
7. 设置“缓存已展示”状态，不进入 blocking loading。
8. 调用现有 `loadSessions()` 刷新。
9. 远端成功后清除缓存状态；失败时保留缓存和明确错误/等待状态。
10. 不新增第二套 fetch 方法。

### Phase 2:UI 状态

在 `SidebarView` 已有 loading/error 分支基础上增加轻量状态条:

- 刷新中:`显示本地记录，正在刷新…`
- Bridge 未就绪:`显示本地记录，正在连接…`
- 刷新失败:`无法刷新，当前为本地记录`

状态条不能遮挡列表，也不应把已有缓存时切回全屏 loading。

### Phase 3:独立验证项

- 验证同一 SavedBridge 在本地/relay 重连前后的 `cacheScopeKey`。
- 若 key 稳定，不改 identity。
- 若不稳定，另写迁移设计；不能直接把 scope 改成 bridgeId，否则现有缓存、非 Bridge server 和多 backend 隔离都需要迁移策略。
- 新鲜度 metadata 另起任务，避免本次修改列表 JSON 格式。

## 4. 必须补充的测试

建议在现有 `SessionsViewModelServerSwitchTests` 附近增加:

1. `testInitialize_cacheHitPublishesSessionsBeforeRemoteCompletes`
2. `testInitialize_emptyCacheIsAValidCachedResult`
3. `testInitialize_cacheMissKeepsBlockingLoading`
4. `testInitialize_remoteSuccessReplacesCachedSessions`
5. `testInitialize_remoteFailurePreservesCachedSessions`
6. `testInitialize_bridgeUnavailablePreservesCacheWithoutBlockingLoading`
7. `testInitialize_scopeSwitchLoadsOnlyNewScopeCache`
8. `testInitialize_staleCacheReadCannotOverwriteNewerScope`
9. `testInitialize_cachedSessionsPrimeTemporaryProjectRoots`
10. `testInitialize_corruptCacheFallsBackToRemoteLoading`

现有以下测试需要调整或保留回归:

- `testInitialize_serverSwitchClearsStaleSessionsBeforeReload`
  - 新语义应改成“切换后不保留旧 scope 列表；有新 scope 缓存则显示新缓存，否则为空并 loading”。
- Bridge ready retry 系列
  - 有缓存时断言 `isLoading == false`；
  - 无缓存时保持当前断言。
- root-only 与 non-root-only session 过滤测试
  - 确保缓存恢复使用与已保存 display snapshot 一致的可见性规则。

按项目约束，不需要 UI test 或 snapshot test。实施后应运行定向 Swift unit tests、定向 build；若当时连接了 iPhone，再安装到真机进行手动冷启动验证。

## 5. 文档层面的小问题

- “后台异步增量更新”措辞不准确，现有远端成功路径是整体替换 session 列表，不是 merge。
- `filterAndSortCachedSessions(_:config:)` 的 `config` 参数未使用。
- 缓存保存的本来就是过滤、排序后的 display sessions，再次过滤主要用于防御，不是恢复正确性的核心。
- `loadSessionListSnapshot` 解码失败只返回 nil，不会像消息快照一样删除损坏文件；文档不应描述成完整的格式恢复机制。
- 原文重复出现一次 `## 5. 边界情况` 标题。
- “每个 Phase 完成后装真机”应限定为修改 iOS 代码且设备已连接时；自动化验收仍不包含 UI automation。

## 6. 最终建议

核心改动值得做，而且实际最小实现比原计划更简单:

> 在 scope 安全和可测试的前提下，把 session list snapshot 恢复接到 `initialize`，然后继续复用现有 `loadSessions` 作为唯一远端刷新状态机。

原计划修订后可以直接交付开发 agent。修订前直接实施，最可能产生的问题是旧 scope 缓存覆盖新 backend、两套加载逻辑逐渐分叉，以及花时间修改一个并不控制消息快照恢复的 Phase 2 门控。
