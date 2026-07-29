# iOS 冷启动本地缓存实施计划第三轮评审

> 评审对象:`docs/2026-06-14-ios-cold-start-local-cache.md` 修订版 v3
> 评审日期:2026-06-14
> 对照代码仓库:`/Users/jacklee/Projects/cccode-ios`
> 结论:**前两轮意见已全部落实，但仍有 2 个新增 P1；修正后可直接实施**

## 1. 总体结论

v3 已经实质关闭前两轮提出的全部问题:

- scope 切换会在缓存读取前清除旧 UI；
- event observer 被移动到缓存恢复之后启动；
- nil 与合法空缓存贯穿 ViewModel 和 UI 设计；
- `loadSessions` 保持唯一远端状态机；
- snapshot store 读写统一走最小协议注入；
- stale 状态基于来源维护；
- project roots 使用过滤后的 sessions；
- 独立任务和本次 Phase 已明确拆分。

文档主体已经达到可执行水平。第三轮按真实并发时序检查时，发现两个此前被“延后 event observer”和“空缓存 provenance”改动新引入或暴露的问题。它们都可以局部修正，不影响总体方案。

## 2. 阻塞问题

### [P1] 新 scope 等待缓存期间，旧 event task 和旧 `loadSessions` 仍可能写入新状态

v3 在 scope 切换时立即清空 UI，然后更新 `currentServerConfig`，等待新 scope 缓存，最后才调用:

```swift
startGlobalEventObserver(config: config)
await loadSessions()
```

问题在于，现有 `startGlobalEventObserver` 才会执行:

```swift
globalEventTask?.cancel()
```

把它延后意味着缓存读取等待期间，旧 scope 的 event task 尚未被显式取消。旧 task 的外层循环会检查 scope，但 `for await event in stream` 内处理事件前只检查 `Task.isCancelled`，不会再次比较 scope。因此旧 stream 在这段窗口收到事件时，仍可能调用 `handleBackendLiveEvent` 修改已经切到新 scope 的 `sessions`。

同样，旧 scope 已经在执行的 `loadSessions` 只通过 `loadRequestID` 判断自己是否仍为当前请求。新 scope 的 `loadSessions` 要等缓存读取结束后才调用 `beginLoadRequest()`。在此之前，旧请求仍持有当前 request ID，返回后可以把旧 scope 结果写进新 scope UI。

因此，“清空旧 UI + 缓存恢复后再启动新 observer”还不够。scope 切换开始时必须先让旧异步工作失效。

**必须在第 1 步补充:**

```swift
if scopeChanged {
    globalEventTask?.cancel()
    globalEventTask = nil

    autoReloadTask?.cancel()
    sessionListRefreshTask?.cancel()
    bridgeReadyReloadTask?.cancel()

    invalidateLoadRequests()

    // 然后再清旧 UI
    sessions = []
    projectRoots = []
    // ...
}
```

其中 `invalidateLoadRequests` 可以是:

```swift
private func invalidateLoadRequests() {
    loadRequestID &+= 1
}
```

也可以统一引入 initialization generation。最小改动是递增现有 request ID。

需要注意，取消 `bridgeReadyReloadTask` 后，新初始化仍会在远端失败时按现有逻辑重新安排 retry，不会丢失恢复能力。

**必须补充测试:**

1. A 的远端 `loadSessions` 延迟返回，切换到 B 并暂停 B 的缓存读取；释放 A 的远端响应，断言不能写入 B。
2. A 的 event stream 在切换 B 后发送事件，断言不能修改 B 的列表。
3. 原测试 8 继续覆盖 A 的缓存读取延迟返回。

只测缓存读取竞态无法证明整个 scope 切换链路安全。

### [P1] Bridge retryable catch 条件与状态矩阵冲突

v3 建议把现有条件:

```swift
isBridgeReadyRetryableError(error), sessions.isEmpty
```

改成:

```swift
isBridgeReadyRetryableError(error),
sessions.isEmpty && !isShowingStaleCache
```

但这会让以下两种缓存命中场景无法进入 Bridge waiting 分支:

- 有缓存 sessions；
- 合法空缓存，`isShowingStaleCache == true`。

它们都会落入普通 error 分支，得到:

```swift
isWaitingForBridge = false
errorMessage = localizedErrorDescription(error)
```

这与文档状态矩阵要求的:

```text
有缓存/空缓存 + Bridge 未就绪
isWaitingForBridge = true
errorMessage = nil
```

直接矛盾。

cache provenance 只应决定是否显示 blocking loading，不应决定错误是否属于 Bridge waiting。

**建议将 catch 结构改为:**

```swift
if case OpenCodeError.unauthorized = error {
    isWaitingForBridge = false
    handleUnauthorized()
} else if isBridgeReadyRetryableError(error) {
    isWaitingForBridge = true
    isLoading = sessions.isEmpty && !isShowingStaleCache
    errorMessage = nil
} else {
    isWaitingForBridge = false
    errorMessage = localizedErrorDescription(error)
}
```

`initialize` 在 `loadSessions` 返回后的 retry 调度分支也应使用:

```swift
isLoading = sessions.isEmpty && !isShowingStaleCache
```

而不是现有的 `sessions.isEmpty`。

**必须扩展测试:**

- 非空缓存 + `bridgeUnavailable` → waiting true、loading false、error nil。
- 空缓存 + `bridgeUnavailable` → waiting true、loading false、error nil。
- cache miss + `bridgeUnavailable` → waiting true、loading true、error nil。
- 非 retryable error + 缓存 → waiting false、loading false、error 有值。

## 3. 非阻塞修订

### [P2] continuation 测试 double 示例目前不是可实现代码

文档示例:

```swift
final class ControllableSnapshotStore: SessionListSnapshotStoring, @unchecked Sendable {
    private let loadContinuation: CheckedContinuation<[Session]?, Never>?
}
```

不可在异步读取发生后给不可变 `let` continuation 赋值，也没有并发保护。开发 agent 仍需自行设计。

建议文档只声明行为要求，或给出 actor 版本:

```swift
actor ControllableSnapshotStore: SessionListSnapshotStoring {
    private var pendingLoads: [String: CheckedContinuation<[Session]?, Never>] = [:]

    func loadSessionListSnapshot(baseConfig: ServerConfig?) async -> [Session]? {
        let scope = BackendServerIdentity(baseConfig: baseConfig).cacheScopeKey
        return await withCheckedContinuation { continuation in
            pendingLoads[scope] = continuation
        }
    }

    func resumeLoad(scope: String, with sessions: [Session]?) {
        pendingLoads.removeValue(forKey: scope)?.resume(returning: sessions)
    }
}
```

实际测试还应防止 continuation 未恢复导致测试挂死。

### [P2] 远端权威空列表的 Sidebar 分支应给出明确条件

文档已经给出正确 UI 契约，但现有 Sidebar 的:

```swift
!isLoading && sessions.isEmpty && errorMessage == nil
```

会显示“正在连接 Mac…”，即使远端已经成功返回权威空列表。实施时必须删除这个泛化分支，或至少增加 `isWaitingForBridge` 条件。

推荐分支优先级:

1. blocking loading；
2. 空列表 + stale cache；
3. 空列表 + error；
4. 空列表 + waiting；
5. 权威空列表；
6. 非空列表。

这不是新架构问题，但明确顺序可以避免 agent 只加一个状态条却留下旧 spinner 分支。

### [P2] 协议的文件归属应固定

文档写“`SessionsView.swift` 或 `Services/Storage/`”。既然协议由存储 actor 实现，并同时服务生产和测试，建议直接放在:

```text
Services/Storage/SessionSnapshotStore.swift
```

或独立的:

```text
Services/Storage/SessionListSnapshotStoring.swift
```

避免把基础存储契约定义在 SwiftUI view 文件中。

## 4. 已通过核验的实现约束

以下事项第三轮确认无问题:

- actor 的同步隔离方法可以通过跨 actor `await` 满足该异步使用方式。
- 协议标记 `Sendable` 与 `Session`、`ServerConfig` 的现有 `Sendable` 定义相容。
- 两处 snapshot save 统一改用注入 store 是正确范围。
- 使用过滤后的 sessions 推导 project roots 正确。
- 空缓存使用 `isShowingStaleCache` 表达 provenance 可行。
- 不需要新增 `refreshSessionsInBackground`。
- 不需要修改消息快照门控。
- 不需要 UI test 或 snapshot test。

## 5. 最终实施顺序修正版

建议把 Phase 1.2 的顺序最终固定为:

1. 计算 scope change。
2. scope change 时取消旧 event/retry/refresh task，并使旧 load request 失效。
3. 清除旧 scope UI。
4. 切换 config/client/directory scope。
5. 读取新 scope cache。
6. 校验 cancellation 和 requested scope。
7. 应用 nil/空/非空 cache 结果。
8. 启动新 scope event observer。
9. 调用唯一的 `loadSessions`。
10. 按 retryable error 类型设置 waiting；按 cache provenance 设置 blocking loading。

## 6. 最终结论

v3 已经正确吸收前两轮全部意见，但“延后启动新 observer”必须配套“提前取消旧 observer/旧 request”，而 Bridge waiting 分类不能由列表是否为空决定。

修正这两个 P1 后，实施计划可以直接交给开发 agent，不需要第四轮架构评审。
