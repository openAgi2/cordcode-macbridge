# Bridge 集成测试方案 — 基于 cc-connect 行为基准

> 本文档定义基于 cc-connect 核心事件行为基准的 Bridge 集成测试方案。
> 目标：用行为约束代替代码移植，确保 Bridge 和 cc-connect 对同一后端产生等价的状态转换。
> 生成日期: 2026-05-02
> 状态: 待开发

---

## 一、为什么需要集成测试

当前 Bridge 的测试（`bridge/src/backends/test-opencode.mjs`，171 个用例）以**单元测试**为主：验证单个函数的输入输出、SSE 事件解析、数据映射。但没有测试**跨多个事件的状态转换序列**——即"收到 A 事件后收到 B 事件，最终状态应该是 C"。

cc-connect 的 `core/engine_test.go`（12K 行，170+ 用例）大量测试这种状态序列。比如：
- 收到 `EventText` + `EventToolUse` + `EventResult(Done:true)` → session 关闭、回复发送
- 收到 `EventError` + `EventResult(Done:true)` → 错误消息发送、session 关闭
- 收到 `EventText` + 不再收到任何事件（进程退出）→ `EventResult` 自动触发

这类测试直接编码了 cc-connect 的行为语义。Bridge 需要等价的测试来保证行为一致。

---

## 二、测试框架选择

**使用现有框架**：Bridge 已有自定义测试框架（`test-opencode.mjs` 的 `section/assert` 模式），不引入新依赖。

**测试文件位置**：`bridge/src/backends/test-opencode-lifecycle.mjs`（新建）

**运行方式**：`node src/backends/test-opencode-lifecycle.mjs`

**与现有测试的关系**：独立文件，不修改 `test-opencode.mjs`。现有测试继续验证单元级行为，新文件验证生命周期级行为。

---

## 三、测试基础设施

### 3.1 Mock SSE 事件注入器

核心工具：一个能模拟 SSE 事件序列的 helper，替代真实的 SSE 连接。

```javascript
function createLifecycleDriver (opts = {}) {
  const driver = new OpenCodeHttpDriver({ logger: { log: () => {}, error: () => {}, warn: () => {} } })
  driver._fetch = opts.fetch || (async () => ({}))
  driver._emittedEvents = []
  driver._emit = (event) => { driver._emittedEvents.push(event) }
  driver._available = true
  driver._version = '1.1.53'

  // 注入 SSE 事件的便捷方法
  driver.injectSSE = (event) => {
    driver._onSSEEvent(event)
  }

  // 注入事件序列
  driver.injectSSESequence = (events) => {
    for (const evt of events) {
      driver._onSSEEvent(evt)
    }
  }

  // 获取特定类型的 normalized 事件
  driver.emittedByKind = (kind) => {
    return driver._emittedEvents.filter(e => {
      // normalized events 结构: { backendId, sessionId, event, data }
      // 需要检查 data 或 event 名来判断 kind
      return true // 实际实现需要根据 wire event 名映射
    })
  }

  // 清空已发出事件（用于隔离测试）
  driver.clearEmitted = () => { driver._emittedEvents = [] }

  return driver
}
```

### 3.2 事件构造 helpers

```javascript
// OpenCode SSE 事件构造
function sessionStatusEvent (sessionId, status) {
  return { type: 'session.status', properties: { sessionID: sessionId, type: status } }
}

function todoUpdatedEvent (sessionId, todos) {
  return { type: 'todo.updated', properties: { sessionID: sessionId, todos } }
}

function messageUpdatedEvent (sessionId, messageId, content, completed = false) {
  return {
    type: 'message.updated',
    properties: {
      sessionID: sessionId,
      message: { info: { id: messageId, role: 'assistant' }, parts: [{ type: 'text', text: content }] },
      time: completed ? { completed: Date.now() } : {}
    }
  }
}

function errorEvent (sessionId, message) {
  return { type: 'error', properties: { sessionID: sessionId, message } }
}
```

---

## 四、测试用例清单

每个用例标注对应的 cc-connect 行为基准。

### 4.1 任务完成信号

#### TC-1: todo.updated 全部完成 → 合成 session_state(idle)

**cc-connect 基准**：`EventResult(Done:true)` 发出后 session 关闭

```javascript
section('TC-1: todo.updated all finished → synthesize session_state(idle)')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'active'))  // session 开始运行

  // 所有 todo 完成
  driver.injectSSE(todoUpdatedEvent('s1', [
    { content: 'Task 1', status: 'completed', priority: 'normal' },
    { content: 'Task 2', status: 'completed', priority: 'normal' }
  ]))

  const events = driver._emittedEvents
  // 应该发出: todos_updated + session_state_changed(idle)
  assert(events.length >= 2, `event count: ${events.length}`)
  assert(events.some(e => e.event === 'todos_updated'), 'todos_updated emitted')
  assert(events.some(e => e.event === 'session_state_changed' && e.data?.state === 'idle'),
         'session_state_changed(idle) synthesized')
  // _runtimeStates 应该已清除
  assert(!driver._runtimeStates.has('s1'), 'runtimeStates cleaned up')
}
```

#### TC-2: todo.updated 部分完成 → 不合成 idle

```javascript
section('TC-2: todo.updated partial → no session_state(idle)')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'active'))

  driver.injectSSE(todoUpdatedEvent('s1', [
    { content: 'Task 1', status: 'completed', priority: 'normal' },
    { content: 'Task 2', status: 'pending', priority: 'normal' }
  ]))

  const idleEvents = driver._emittedEvents.filter(
    e => e.event === 'session_state_changed' && e.data?.state === 'idle'
  )
  assert(idleEvents.length === 0, 'no idle synthesized when todos still active')
  assert(driver._runtimeStates.get('s1') === 'running', 'runtimeStates still running')
}
```

#### TC-3: todo.updated 空 → 不合成 idle

```javascript
section('TC-3: todo.updated empty → no session_state(idle)')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'active'))

  driver.injectSSE(todoUpdatedEvent('s1', []))

  const idleEvents = driver._emittedEvents.filter(
    e => e.event === 'session_state_changed' && e.data?.state === 'idle'
  )
  assert(idleEvents.length === 0, 'no idle synthesized for empty todos')
}
```

### 4.2 Session 状态转换

#### TC-4: session.status idle → 正常转发

```javascript
section('TC-4: session.status idle → forward session_state(idle)')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'idle'))

  const stateEvents = driver._emittedEvents.filter(e => e.event === 'session_state_changed')
  assert(stateEvents.length === 1, 'one session_state_changed')
  assert(stateEvents[0].data?.state === 'idle', 'state is idle')
  assert(!driver._runtimeStates.has('s1'), 'runtimeStates cleaned')
}
```

#### TC-5: session.status active → 标记 running

```javascript
section('TC-5: session.status active → mark running')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'active'))

  assert(driver._runtimeStates.get('s1') === 'running', 'marked as running')
  const stateEvents = driver._emittedEvents.filter(e => e.event === 'session_state_changed')
  assert(stateEvents.length === 1, 'one session_state_changed')
  assert(stateEvents[0].data?.state === 'running', 'state is running')
}
```

#### TC-6: session.status active → idle → 正常完整周期

**cc-connect 基准**：`EventText` → `EventResult(Done:true)` 完整周期

```javascript
section('TC-6: active → idle full cycle')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'active'))
  driver.clearEmitted()
  driver.injectSSE(sessionStatusEvent('s1', 'idle'))

  assert(!driver._runtimeStates.has('s1'), 'runtimeStates cleaned after idle')
  const stateEvents = driver._emittedEvents.filter(e => e.event === 'session_state_changed')
  assert(stateEvents.length === 1, 'one session_state_changed')
  assert(stateEvents[0].data?.state === 'idle', 'final state is idle')
}
```

### 4.3 SSE 重连恢复（缺口 2）

#### TC-7: 重连后 reconcile 已完成的 session

**cc-connect 基准**：进程退出 → `EventResult(Done:true)` 不可遗漏

```javascript
section('TC-7: SSE reconnect reconcile — completed session')
{
  const driver = createLifecycleDriver()
  // 模拟 SSE 断开前的状态
  driver._runtimeStates.set('s1', 'running')
  driver._runtimeStates.set('s2', 'running')

  // Mock _fetch 返回 session 状态
  let fetchCallCount = 0
  driver._fetch = async (path) => {
    fetchCallCount++
    if (path.includes('/session/s1')) {
      return { id: 's1', status: 'idle', title: 'Done' }  // s1 已完成
    }
    if (path.includes('/session/s2')) {
      return { id: 's2', status: 'active', title: 'Running' }  // s2 仍在运行
    }
    return {}
  }

  // 触发 reconcile
  await driver._reconcileRuntimeStatesAfterReconnect('/project')

  assert(fetchCallCount === 2, `checked both sessions: ${fetchCallCount}`)
  assert(!driver._runtimeStates.has('s1'), 's1 cleaned (was idle)')
  assert(driver._runtimeStates.has('s2'), 's2 kept (still running)')

  // 应该合成了 s1 的 idle 事件
  const idleForS1 = driver._emittedEvents.filter(
    e => e.event === 'session_state_changed' && e.data?.state === 'idle' && e.sessionId === 's1'
  )
  assert(idleForS1.length === 1, 's1 idle synthesized')
}
```

#### TC-8: 重连后 reconcile 无残留 session

```javascript
section('TC-8: SSE reconnect — no pending sessions')
{
  const driver = createLifecycleDriver()
  // _runtimeStates 为空
  let fetchCalled = false
  driver._fetch = async () => { fetchCalled = true; return {} }

  await driver._reconcileRuntimeStatesAfterReconnect('/project')

  assert(!fetchCalled, 'no fetch when no pending sessions')
  assert(driver._emittedEvents.length === 0, 'no events emitted')
}
```

#### TC-9: 重连后 reconcile fetch 失败 → 跳过

```javascript
section('TC-9: SSE reconnect — fetch failure skips session')
{
  const driver = createLifecycleDriver()
  driver._runtimeStates.set('s1', 'running')

  driver._fetch = async () => { throw new Error('network error') }

  await driver._reconcileRuntimeStatesAfterReconnect('/project')

  // 保守策略：不确定就保留
  assert(driver._runtimeStates.has('s1'), 's1 kept when fetch failed')
  assert(driver._emittedEvents.length === 0, 'no events when fetch failed')
}
```

### 4.4 多事件序列

#### TC-10: active → todo updated → 全完成 → idle 的完整生命周期

**cc-connect 基准**：`EventText` → `EventToolUse` → `EventResult(Done:true)` 序列

```javascript
section('TC-10: full lifecycle — active → todos complete → idle')
{
  const driver = createLifecycleDriver()

  // 1. session 开始
  driver.injectSSE(sessionStatusEvent('s1', 'active'))
  assert(driver._runtimeStates.get('s1') === 'running', 'step 1: running')

  // 2. todo 更新（部分完成）
  driver.injectSSE(todoUpdatedEvent('s1', [
    { content: 'Task 1', status: 'completed', priority: 'normal' },
    { content: 'Task 2', status: 'in_progress', priority: 'normal' }
  ]))
  driver.clearEmitted()

  // 3. 所有 todo 完成
  driver.injectSSE(todoUpdatedEvent('s1', [
    { content: 'Task 1', status: 'completed', priority: 'normal' },
    { content: 'Task 2', status: 'completed', priority: 'normal' }
  ]))

  // 验证：应该合成 idle
  assert(!driver._runtimeStates.has('s1'), 'runtimeStates cleaned')
  const idleEvents = driver._emittedEvents.filter(
    e => e.event === 'session_state_changed' && e.data?.state === 'idle'
  )
  assert(idleEvents.length >= 1, 'idle synthesized at todo completion')
}
```

#### TC-11: 重复 idle 信号 — todo 完成先到，SSE idle 后到

```javascript
section('TC-11: duplicate idle — todo idle then SSE idle')
{
  const driver = createLifecycleDriver()
  driver.injectSSE(sessionStatusEvent('s1', 'active'))

  // todo 完成触发 idle
  driver.injectSSE(todoUpdatedEvent('s1', [
    { content: 'Task', status: 'completed', priority: 'normal' }
  ]))
  driver.clearEmitted()

  // SSE idle 随后到达（重复）
  driver.injectSSE(sessionStatusEvent('s1', 'idle'))

  // 不应该崩溃，runtimeStates 应该还是空的
  assert(!driver._runtimeStates.has('s1'), 'runtimeStates still clean')
  // SSE idle 应该正常转发（iOS 端会忽略重复的 .idle → .idle 转换）
  const idleEvents = driver._emittedEvents.filter(
    e => e.event === 'session_state_changed' && e.data?.state === 'idle'
  )
  assert(idleEvents.length === 1, 'SSE idle forwarded (iOS handles dedup)')
}
```

### 4.5 边界情况

#### TC-12: 多个 session 并行，只有部分完成

```javascript
section('TC-12: parallel sessions — partial completion')
{
  const driver = createLifecycleDriver()

  // 两个 session 同时运行
  driver.injectSSE(sessionStatusEvent('s1', 'active'))
  driver.injectSSE(sessionStatusEvent('s2', 'active'))

  // s1 的 todo 全完成
  driver.injectSSE(todoUpdatedEvent('s1', [
    { content: 'Task', status: 'completed', priority: 'normal' }
  ]))

  // s1 应该 idle，s2 仍 running
  assert(!driver._runtimeStates.has('s1'), 's1 cleaned')
  assert(driver._runtimeStates.get('s2') === 'running', 's2 still running')
}
```

#### TC-13: session 无 sessionId 的 SSE 事件

```javascript
section('TC-13: SSE event without sessionId → no crash')
{
  const driver = createLifecycleDriver()
  // 没有 sessionID 的 todo 事件
  driver.injectSSE({ type: 'todo.updated', properties: { todos: [] } })
  // 不应该崩溃
  assert(true, 'no crash on missing sessionId')
}
```

---

## 五、验收标准

测试文件 `bridge/src/backends/test-opencode-lifecycle.mjs` 应该：

1. 包含以上 13 个测试用例
2. 运行命令 `node src/backends/test-opencode-lifecycle.mjs` 全部通过
3. 不依赖外部服务（所有 HTTP 和 SSE 通过 mock）
4. 每个测试用例独立，不依赖其他用例的状态
5. 测试输出清晰标注每个用例的 pass/fail

## 六、执行计划

1. 创建 `bridge/src/backends/test-opencode-lifecycle.mjs`
2. 实现 `createLifecycleDriver` 和事件构造 helpers
3. 实现所有 13 个测试用例
4. 运行并确认全部通过
5. 在 `CLAUDE.md` 的 Quick-reference commands 中添加运行命令

## 七、后续扩展

当映射表中新增缺口时，同步补充对应的测试用例。每个缺口至少包含：
- 正常路径测试（happy path）
- 异常路径测试（error path）
- 边界情况测试（edge case）

长期目标：将 `test-opencode-lifecycle.mjs` 扩展为覆盖所有 5 种后端驱动（opencode、claudecode、codex、copilot、codex-cli）的通用生命周期测试。

---

## 附录：cc-connect 对应测试索引

| 本方案测试 | cc-connect 对应行为 | cc-connect 源码位置 |
|-----------|-------------------|-------------------|
| TC-1 | `EventResult(Done:true)` 触发 session 关闭 | `session.go:226-232`, `engine.go:2989` |
| TC-2 | 中间事件不触发终止 | `session.go:242-258` (switch 不发 EventResult) |
| TC-4 | 正常 session 生命周期结束 | `engine.go:2989-3010` |
| TC-6 | 完整 run → result 周期 | `engine.go:2989-3143` |
| TC-7 | 进程退出兜底（bridge 等效：reconcile） | `session.go:226-232` (readLoop EOF) |
| TC-9 | 错误不阻断后续完成 | `session.go:361-370` (handleError 不 return) |
| TC-10 | 完整事件序列 | `engine.go:2989-3143` (processInteractiveEvents) |
| TC-11 | 重复完成信号不崩溃 | `engine.go:2187-2200` (duplicate EventResult 处理) |
| TC-12 | 多 session 并行 | `engine.go` (per-session state 管理) |
