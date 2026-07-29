# CCCode MacBridge 深度运行期 Code Review

审查日期：2026-06-18

审查范围：`MacBridge/`、`go-bridge/`、`core/`、`config/`、`agent/`、`transcriptindex/`、`relay-server/`、协议文档与测试

审查重点：安全增量、并发与 goroutine 生命周期、agent 进程治理、Swift↔Go 契约、Relay 资源边界、协议兼容、测试质量与可维护性

基线文档：

- `docs/2026-06-18-code-review-prompt.md`
- `docs/code-review-2026-06-18.md`（v4）
- `CLAUDE.md`
- `AGENTS.md`
- `CONTRIBUTING.md`
- `SECURITY.md`

## 1. 与 v4 的关系声明

本轮已完整阅读 v4，并以当前工作树而不是 v4 当时的代码状态重新核查。当前代码已经落地了 v4 的多项整改，包括 `read_file` workspace/symlink 边界、Bridge 帧上限、Relay limiter 有界化、设备存储事务提交、TLS fail-closed、配对限流、日志滚动与 management bind fail-fast；这些不重复列为发现。

本报告只记录 v4 未覆盖的问题，或在 v4 已覆盖主题上发现的新增运行期子问题。核心增量集中在控制面凭据继承、runtime 关停时的子进程回收、事件 channel 关闭竞态、Relay 跨设备队头阻塞和 Swift 重启任务竞态。

## 2. 总体评估

当前代码在协议契约测试、Relay mailbox 事务、WebSocket 单 writer 封装、直连帧限制和离线 outbox 上限方面值得信任，明显不是“无边界的原型服务”。尤其是 v4 指出的多数安全问题已经有对应实现和回归测试，说明整改链路有效。

v4 之外最危险的三个隐患是：

1. Mac App 注入给 go-bridge 的 management/Relay 控制面凭据继续被 agent 子进程继承，远程设备可借 agent 工具执行读取并使用这些凭据。
2. runtime 的统一 shutdown 不关闭已登记 session；这些 session 又由 `context.Background()` 创建，Go 父进程退出后可能留下孤儿 agent 进程。
3. Relay 将 bridge→device 写操作同步放在单一 bridge 读循环中，一个慢设备可以阻塞同 route 下所有设备的数据转发最长 120 秒。

此外，OpenCode/Codex 的若干 `Close()` 路径在等待超时后直接关闭事件 channel，生产 goroutine 若稍后恢复会触发 `send on closed channel`，可把单个 session 的退出异常放大成整个 bridge 进程崩溃。

## 3. 发现清单

### 3.1 Agent 子进程继承 management/Relay 控制面凭据，且失败 stderr 原样进入日志和远端错误

[v4 状态] v4 未覆盖

[严重度] 🔴高

[类别] P0-A 安全增量复核 / P0-C agent 进程治理

[位置]

- `MacBridge/MacBridge/Services/RuntimeManager.swift:258-287`
- `go-bridge/main.go:608-611`
- `agent/claudecode/session.go:149-158`
- `agent/codex/session.go:123-128`
- `agent/codex/appserver_session.go:243-253`
- `agent/opencode/session.go:86-92`
- `agent/claudecode/session.go:272-284`
- `agent/codex/session.go:241-255`
- `agent/opencode/session.go:215-226`

[问题] go-bridge 从 Mac App 环境取得 management token、Relay route credential 等控制面秘密后，只清除了 OpenCode server 用户名/密码；默认 agent spawn 继续复制整个 `os.Environ()`，并会把 agent stderr 原样写日志和包装成客户端可见错误。

[证据]

```swift
environment["CCCODE_MANAGEMENT_TOKEN"] = token
environment["CCCODE_RELAY_ROUTE_ID"] = config.relayRouteID
environment["CCCODE_RELAY_CREDENTIAL"] = config.relayCredential
process.environment = environment
```

```go
func clearOpenCodeServerAuthEnv() {
	_ = os.Unsetenv("OPENCODE_SERVER_USERNAME")
	_ = os.Unsetenv("OPENCODE_SERVER_PASSWORD")
}
```

```go
env := filterEnv(os.Environ(), "CLAUDECODE")
env = core.MergeEnv(env, extraEnv)
cmd.Env = env
```

调用链：

```text
RuntimeManager.launchBridgeProcess
  -> go-bridge 读取 CCCODE_MANAGEMENT_TOKEN / CCCODE_RELAY_CREDENTIAL
  -> agent.StartSession(context.Background())
  -> cmd.Env = os.Environ() + provider env
  -> Claude/Codex/OpenCode 及其工具子进程可读取控制面秘密
```

[影响] 攻击者 C（已配对但不可信设备）若能发起允许执行 shell/tool 的 agent turn，可让 agent 输出环境变量，或者直接在 Mac 上携带 management token 调用 loopback `/internal/shutdown`、设备吊销、配对审批等接口。这绕过了 Bridge capability policy：远端不需要直接访问 `127.0.0.1`，agent 进程本身就是本地执行代理。第三方 agent/plugin 崩溃时若把环境写入 stderr，秘密还可能进入 `go-bridge.log` 或被作为 `EventError` 返回客户端。

[修复建议]

1. 在 go-bridge 解析完控制面配置后立即 `Unsetenv`：
   `CCCODE_MANAGEMENT_TOKEN`、`CCCODE_RELAY_CREDENTIAL`、`CCCODE_RELAY_ROUTE_ID`、`CCCODE_RELAY_ENDPOINT`，而不只清 OpenCode 凭据。
2. 新增 `core.BuildAgentEnv(base, providerEnv, sessionEnv)`，采用显式允许列表/拒绝列表构造 agent 环境；控制面变量必须无条件拒绝，provider API key 仅按目标 agent 注入。注意 `base` **不应再是 `os.Environ()`**——那正是本 bug 的根因（全量继承才会泄漏 `CCCODE_*`）；`base` 应是受控的最小启动环境（PATH、HOME、LANG 等），其余一律按 allowlist 显式加入。
3. Claude、Codex exec、Codex stdio app-server、OpenCode 四条 spawn 路径必须共用同一环境构造函数。
4. agent stderr 进入日志或 wire error 前走内容级 redactor；默认只记录退出码、长度和稳定错误码，完整 stderr 仅在显式诊断模式写入受保护文件。
5. 增加回归测试：向父进程设置上述四个 `CCCODE_*` 变量，断言每种 agent 的 `cmd.Env` 均不包含它们；构造含 token 的 stderr，断言日志和 `EventError` 不含原文。

### 3.2 Runtime shutdown 没有关闭 session，父进程退出后可能遗留孤儿 agent 进程

[v4 状态] v4 未覆盖

[严重度] 🔴高

[类别] P0-C agent 进程治理 / P0-B goroutine 生命周期

[位置]

- `go-bridge/handlers.go:358`
- `go-bridge/handlers.go:1534`
- `go-bridge/handlers.go:1604`
- `go-bridge/main.go:371-391`
- `agent/claudecode/claudecode.go:1098`
- `agent/codex/codex.go:435`
- `agent/opencode/opencode.go:503`

[问题] 所有活跃 session 都从 `context.Background()` 派生，而统一 shutdown 只取消 main context、关闭 WebSocket/HTTP/Relay，没有遍历 session registry 调用 `AgentSession.Close()`；三个 `Agent.Stop()` 还是空实现。

[证据]

```go
sess, err := agent.StartSession(context.Background(), resumeID)
```

```go
shutdown := func() {
	cancel()
	server.CloseAllConnections("bridge shutting down")
	relayBridgeClient.Close()
	_ = httpServer.Shutdown(shutdownCtx)
	mgmtSrv.Shutdown()
}
```

```go
func (a *Agent) Stop() error { return nil }
```

调用链：

```text
Mac restart/quit/SIGTERM
  -> go-bridge shutdown()
  -> main context cancel（只覆盖 passive subscriber/relay client）
  -> HTTP Serve 返回，Go 进程退出
  -> Background 派生的 Claude/Codex/OpenCode session 未 Close
  -> Unix 不保证父进程退出时杀死普通子进程
```

[影响] 配置热更新、120 分钟定时重启、Mac App 退出或 go-bridge 崩溃恢复后，旧 Claude/Codex/OpenCode 进程可能继续运行并持有 cwd、文件、网络连接和 stdout pipe。多次重启会累积孤儿进程、重复执行中的 turn、CPU/内存占用，甚至让旧进程继续修改 workspace。Mac 睡眠/唤醒后也可能出现 UI 只认识新 runtime、旧 agent 仍在后台运行的“幽灵 session”。

[问题边界] 需区分两类 session：`handlers.go:191-217` 的 `cleanupIdleSessions()`（由 `StartCleanupLoop` 周期调用）已会按 backend TTL 回收**空闲** session 并调 `sess.Close()`，因此长时间不活跃的 session 有兜底、不会无限累积。本发现针对的是 runtime shutdown 时仍处于 **active（执行中 turn）** 的 session——它们既不在 shutdown 的 registry 遍历范围内（shutdown 根本没做遍历），也不满足 idle 条件（`state != sessionStateIdle`），因此不会被 idle cleanup 处理，这些 agent 子进程才是真正的孤儿来源。修复重点应放在"shutdown 必须遍历并关闭 active session"。

[修复建议]

1. 给 `Handlers` 增加幂等 `Shutdown(ctx)`：
   停止 cleanup ticker/observation loop，锁内快照并清空 session registry，锁外并发或有界串行调用每个 `AgentSession.Close()`。
2. session 必须从 runtime 根 context 派生：把 `ctx` 注入 `Handlers`，替换三个 `context.Background()`；连接断开不应自动取消 session，但 runtime shutdown 必须取消。
3. `shutdown()` 顺序改为：停止接收新 RPC → 广播 shutdown → 关闭 active session/agent → 关闭 Relay/WebSocket → Shutdown HTTP。
4. 为每个 backend 设置明确 grace period；超时后杀进程组，而不是只杀直接 PID。Claude 当前只向直接进程发 SIGTERM/SIGKILL，也应采用 Codex 的 process-group 策略。
5. 增加真实子进程集成测试：启动会阻塞的 helper 子进程，调用 runtime shutdown，断言父 PID 和孙进程 PID 均在期限内消失。

### 3.3 多个 Close 路径在 producer 未退出时关闭 events channel，可触发进程级 panic

[v4 状态] v4 未覆盖

[严重度] 🔴高

[类别] P0-B 并发与 goroutine 生命周期 / P0-C agent 进程治理

[位置]

- `agent/opencode/session.go:480-494`（**高危**：`close(s.events)` 无 `closeOnce`，超时后直接关）
- `agent/codex/passive_subscriber.go:532-555`（**高危**：`closeOnce` 只包了 conn 关闭，未包 :554 的 `close(s.events)`）
- `agent/codex/appserver_session.go:697-733`（**中危**：已有 `closeOnce.Do(close)`，但超时分支后仍执行 close，producer 此后发送仍会 panic）
- `agent/opencode/sse_subscriber.go:730-754`（**低危**：`close(s.events)` 已在 `closeOnce` 内，且 `emit()` 有 `default` 非阻塞兜底；理论 panic 面仍在但触发条件苛刻）
- 对照正确实现：`agent/codex/session.go:850-890`

[问题] OpenCode session 与 Codex passive subscriber 的 `Close()` 在等待 `wg` 超时后仍立即 `close(events)`；超时本身已经证明 producer goroutine 可能还活着，因此其后任何一次发送都会 panic。Codex app-server 已用 `closeOnce` 防重复关闭，但仍在超时分支后执行 close，同样无法阻止 producer 此后发送。sse_subscriber 因 close 已包在 `closeOnce` 内、且 emit 非阻塞，风险显著低于前三者，单列仅供统一治理参考。

[证据]

OpenCode session（无任何保护，超时后直接关）：

```go
select {
case <-done:
case <-time.After(8 * time.Second):
	slog.Warn("opencodeSession: close timed out, abandoning wg.Wait")
}
close(s.events)   // 无 closeOnce，超时即关
```

Codex app-server（有 closeOnce，但超时分支后仍走到 close）：

```go
select {
case <-done:
case <-time.After(2 * time.Second):
}
s.closeOnce.Do(func() {   // closeOnce 只防重复关，不防 producer 此后发送
	close(s.events)
})
```

Codex exec session 的正确模式（超时后不直接关，等 producer 退出再关）：

```go
case <-time.After(codexSessionForceKillWait):
	// Do not close(cs.events) here: readLoop may still ... panic on send.
	go func() {
		<-done
		close(cs.events)
	}()
```

Codex exec session 已明确实现“producer 退出后再关闭”，说明同仓库已经认识到该不变量；OpenCode session、passive subscriber、app-server 三条路径没有遵守。

[影响] 弱网、SSE body 不响应取消、agent 孙进程继续持有 stdout、app-server reader 卡住时，session abort/idle cleanup/runtime restart 会进入 timeout 分支。producer 稍后解析到一条消息或错误并执行 `events <- ev`，整个 go-bridge 因 `send on closed channel` 崩溃，随后 Swift 自动重启；反复触发可形成重启风暴。其中 OpenCode session（无 closeOnce）与 passive subscriber（closeOnce 未覆盖 events）触发最直接、风险最高。

[修复建议]

1. 统一不变量：只有 producer owner 可以关闭 `events`，或者 `Close()` 必须等所有 producer 退出后再关。
2. 复用 `codexSession.Close()` 的延迟关闭模式：OpenCode session 与 passive subscriber 需新增/修正 `closeOnce`，且 `close(events)` 必须推迟到 producer（`wg`）确认退出之后，而不是超时即关；app-server 需把 close 从超时分支移出，改为等 producer 退出的延迟关闭。sse_subscriber 风险已低，按同一模式统一即可，不必单独大改。
3. timeout 后主动关闭底层 pipe/body/socket 并杀进程组；若仍未退出，返回明确错误但不要关闭 channel。
4. `emit` 的 `default`/`select` 不能被当成 closed-channel 安全网；`select` 中向已关闭 channel 发送仍会 panic。
5. 增加“producer 故意晚于 Close timeout 发送”的定向测试，并用 `go test -race` 运行。

### 3.4 Relay 的同步跨设备写造成 route 级队头阻塞

[v4 状态] v4 已覆盖（仅增量）

[严重度] 🔴高

[类别] P1-E relay-server 深度 / P1-F 资源边界与背压

[位置]

- `relay-server/internal/relay/server.go:521-565`
- `relay-server/internal/relay/server.go:613-620`
- `relay-server/internal/relay/server.go:23-30`

[问题] v4 验证了单连接 writer mutex 和 write deadline，但 bridge socket 的唯一读循环会同步等待目标 device 的 `WriteMessage`；一个慢设备的背压因此传播到同 route 的全部设备。

[证据]

```go
_, payload, err := peer.conn.ReadMessage()
...
if target := s.device(routeID, envelope.DestinationID); target != nil {
	if err := target.write(payload); err == nil {
		continue
	}
}
```

```go
p.writeMu.Lock()
defer p.writeMu.Unlock()
_ = p.conn.SetWriteDeadline(time.Now().Add(relayWriteDeadline))
return p.conn.WriteMessage(websocket.TextMessage, payload)
```

`relayWriteDeadline` 当前为 120 秒。

[影响] 攻击者 C 可连接 Relay device socket 后停止读取，再诱导 Mac 向该设备发送接近 32 MiB 的合法大帧。Relay 的 bridge read loop 最长阻塞 120 秒，期间同 route 下其他设备的握手、事件和 RPC 响应都不能从 bridge socket 被读取。正常弱网设备也会造成同样的跨设备故障，表现为“一台慢手机拖死整个家庭/团队 route”。

[修复建议]

1. 每个 `socketPeer` 增加有界发送队列和唯一 writer goroutine；bridge read loop 只做非阻塞/短超时 enqueue。
2. 队列达到帧数或字节上限时断开该慢 device，并将当前 envelope 写 mailbox；不得为每帧启动无界 goroutine。
3. bridge→device 在线投递和 mailbox append 需要定义幂等语义，避免“已在线写成功又入 mailbox”重复。
4. 增加 route 级测试：device A 不读，device B 正常读；向 A 写满缓冲时，B 仍应在小于 1 秒内收到消息。
5. 记录 per-device queue bytes、drop/disconnect 次数和 write latency，避免只能从 route 全局超时推断。

### 3.5 Swift restart 使用无代次的延迟 Task，并发配置更新会启动多轮互相接管的 runtime

[v4 状态] v4 未覆盖

[严重度] 🟠中

[类别] P0-D Swift↔Go 跨进程契约

[位置]

- `MacBridge/MacBridge/Services/RuntimeManager.swift:187-199`
- `MacBridge/MacBridge/Services/RuntimeManager.swift:563-600`
- `MacBridge/MacBridge/App/AppDependencies.swift:132-159`
- `MacBridge/MacBridge/App/AppDependencies.swift:163-197`
- `MacBridge/MacBridge/App/AppDependencies.swift:200-210`

[问题] 每次 `restart()` 都创建一个不可取消、无 generation 校验的 1.5 秒延迟 Task；短时间内 Relay provisioning、remote URL、凭据或用户操作连续触发 restart 时，所有 Task 都会执行 launch。

[证据]

```swift
func restart() {
    userStopped = true
    terminateProcess()
    setStatus(.starting, "正在重启 Bridge...")
    Task {
        try? await Task.sleep(nanoseconds: 1_500_000_000)
        self.userStopped = false
        self.launchBridgeProcess()
    }
}
```

```swift
runtimeManager.restart()
...
self.runtimeManager.config.relayCredential = relay.credential
self.runtimeManager.restart()
```

[影响] 多个延迟 Task 依次启动 A/B/C runtime。后一个 launch 通过 `prepareRuntimeOwnershipForLaunch()` 识别同路径进程并 SIGTERM/SIGKILL 前一个，导致端口反复接管、session 丢失、日志/ready frame 抖动和额外 crash 回调。用户看到的是配置变更后多次断连或短暂 ready 后再次重启。

[修复建议]

1. 保存 `restartTask` 并在新 restart 到来时 cancel 旧任务。
2. 增加单调 `launchGeneration`；Task 醒来后必须验证 generation 仍是当前值。
3. 把多项配置更新收敛为一次 `applyConfigAndRestart(newConfig)`，先原子替换配置，再只调度一次 restart。
4. `launchBridgeProcess()` 开头增加“已有当前 generation 进程正在启动/运行”守卫。
5. 增加 unit test：100ms 内连续调用三次 restart，断言只执行一次 process launch。

### 3.6 `runtime.json` 和 management-token 写失败后仍发布 ready，启动契约可能出现“假就绪”

[v4 状态] v4 已覆盖（仅增量）

[严重度] 🟠中

[类别] P0-D Swift↔Go 跨进程契约

[位置]

- `go-bridge/main.go:406-422`
- `go-bridge/runtime_startup.go:43-64`
- `MacBridge/MacBridge/Services/RuntimeManager.swift:438-466`

[问题] management token 和 `runtime.json` 是 Swift 发现管理面的必需文件，但 Go 对两者写失败只记日志，仍继续运行并输出 stdout ready；Swift 实际不消费该 ready frame，而只轮询文件。

[证据]

```go
if err := core.AtomicWriteFile(..."/management-token", ...); err != nil {
	slog.Error("management-token 写入失败", "error", err)
}
WriteReadyFrame(...)
```

```go
if err := core.AtomicWriteFile(runtimePath, data, 0o600); err != nil {
	slog.Error("runtime.json 写入失败", "error", err)
}
```

调用链：

```text
磁盘满/权限错误
  -> management-token 或 runtime.json 写失败
  -> Bridge 端口和 management server 已监听
  -> Go 仍认为 ready
  -> Swift pollManagementAPI 永远拿不到完整 bootstrap
  -> 60 秒后按“卡住”重启，重复失败
```

[影响] 磁盘满、Application Support 权限异常或原子 rename 失败时，会产生一个对网络已开放但 UI 永远认为未就绪的 runtime，并每 60 秒重启。旧客户端可能仍能连接，新 UI 却无法管理，状态恢复不一致。

[修复建议]

1. `WriteReadyFrame` 改为返回 error；product mode 下 `runtime.json` 写失败必须写 `runtime_error.bootstrap_persist_failed` 并退出。
2. management-token 写失败同样 fail-fast，不能发布 ready。
3. Swift 同时解析 stdout 的结构化 `runtime_ready/runtime_error`，文件轮询作为持久 bootstrap 交叉校验，而不是唯一信号。
4. ready frame 增加并校验 `bridgeEpoch`；Swift 应同时匹配 PID、port、epoch，防止同 PID 生命周期之外的旧文件误判。
5. 增加只读目录/磁盘写失败测试，断言不会进入 ready。

### 3.7 Management API 客户端无短超时，健康轮询可能被单次请求阻塞一分钟以上

[v4 状态] v4 未覆盖

[严重度] 🟠中

[类别] P0-D Swift↔Go 跨进程契约

[位置]

- `MacBridge/MacBridge/Services/ManagementAPIClient.swift:128-145`
- `MacBridge/MacBridge/Services/RuntimeManager.swift:381-390`
- `MacBridge/MacBridge/Services/RuntimeManager.swift:477-492`

[问题] management 请求使用 `URLSession.shared` 默认超时；监控循环串行 await `getStatus()` 和 `getAgents()`，请求挂起期间不会执行三秒轮询和自动重启判定。

[证据]

```swift
let (data, response) = try await URLSession.shared.data(for: req)
```

```swift
while let self, !Task.isCancelled {
    if self.bridgeProcess?.isRunning == true {
        await self.pollManagementAPI()
        self.evaluateAutoRestart()
    }
    try? await Task.sleep(nanoseconds: 3_000_000_000)
}
```

[影响] management handler 死锁、loopback socket 半开或进程卡顿时，一次 status 请求可阻塞到系统默认请求超时；随后 agents 请求还可能再阻塞一次。`managementFailureCount` 和 stuck restart 在 await 返回前都不更新，实际故障发现时间远大于代码表面上的 3×3 秒。

[修复建议]

1. 使用专用 ephemeral `URLSessionConfiguration`，`timeoutIntervalForRequest` 设为 2–3 秒，resource timeout 设为 5 秒。
2. status 与 agents 不应串行决定 liveness；status 成功后更新状态，agents 刷新可独立低优先级执行。
3. 每轮 poll 加 generation/PID 校验，旧请求返回后不得覆盖新 runtime 状态。
4. 增加 management server 接受连接但不返回响应的测试，断言 supervisor 在预期时间内进入恢复流程。

### 3.8 配对限流 bucket 按任意 pairingId/IP 永久留存，可被低速制造进程级 map 增长

[v4 状态] v4 已覆盖（仅增量）

[严重度] 🟡低

[类别] P0-A 安全增量复核 / P1-F 资源边界

[位置]

- `go-bridge/pairing_hardening.go:30-42`
- `go-bridge/pairing_hardening.go:70-91`
- `go-bridge/pairing_hardening.go:103-121`
- `go-bridge/pairing_handler.go:196-201`

[问题] 配对暴力破解保护已经落地，但 `sourceBuckets` 和针对不存在 session 的 `pairFails` 没有 TTL 清理或总容量上限；任意 pairingId 会创建永久条目。

[证据]

```go
sourceBuckets map[string]*slidingBucket
pairFails     map[string]*slidingBucket
```

```go
b := g.pairFails[pairingID]
if b == nil {
	b = &slidingBucket{...}
	g.pairFails[pairingID] = b
}
```

[影响] 攻击者 B 可持续提交随机高熵 pairingId；每个来源每分钟虽然只能尝试 10 次，但条目在五分钟窗口后也不会删除。长时间运行或多个来源/IPv6 地址下，map 会单调增长。该问题不会绕过配对认证，主要是低速内存消耗和全局测试状态污染。

[修复建议]

1. 为 gate 增加定期/惰性 TTL 清理，删除窗口内无计数的 bucket。
2. 设置全局最大 bucket 数；容量满时对新 key fail closed。
3. 未找到 session 时不要为任意 pairingId 建立独立长期 bucket，可使用有界哈希分片或只依赖来源限流。
4. 将 `globalPairingGate` 注入 server 实例，避免测试共享进程级状态。

### 3.9 Handler 内部生命周期组件不可整体关闭，测试和未来进程内重建会泄漏 goroutine

[v4 状态] v4 已覆盖（仅增量）

[严重度] 🟡低

[类别] P0-B 并发与 goroutine 生命周期 / P1-G 可维护性

[位置]

- `go-bridge/handlers.go:65-86`
- `go-bridge/handlers.go:183-188`
- `go-bridge/relay_observation.go:53-62`
- `go-bridge/relay_observation.go:183-197`
- `go-bridge/main.go:146`

[问题] `NewHandlers()` 自动启动 observation goroutine，`StartCleanupLoop()` 使用不可停止的 `time.Tick`；虽然 `ObservationManager.Stop()` 存在，但 production shutdown 没有统一调用点，cleanup loop 完全没有 stop API。

[证据]

```go
func (h *Handlers) StartCleanupLoop(interval time.Duration) {
	go func() {
		for range time.Tick(interval) {
			h.cleanupIdleSessions()
		}
	}()
}
```

```go
om.leaseTimer = time.NewTicker(5 * time.Second)
go om.leaseCheckLoop()
```

[影响] 当前独立 runtime 进程退出时 OS 会回收 goroutine，因此不是线上永久泄漏；但单元测试多次创建 `Handlers`、未来若支持进程内重载，旧 ticker/goroutine 会继续持有完整 handler 对象和状态，造成测试间干扰、内存保留和难以解释的后台访问。

[修复建议]

1. `Handlers` 持有 cleanup ticker/cancel，并提供幂等 `Close()`。
2. `NewHandlers()` 不隐式启动后台 goroutine；由 runtime composition root 显式 `Start(ctx)`。
3. 所有测试 `t.Cleanup(handlers.Close)`，不直接依赖全局默认实例。
4. 与 3.2 的 session shutdown 合并为一个完整生命周期接口。

### 3.10 三个 god-object 的共享状态边界仍不适合隔离测试，拆分应围绕 owner 而不是行数

[v4 状态] v4 已覆盖（仅增量）

[严重度] 🟡低

[类别] P1-G 可维护性与 god-object

[位置]

- `go-bridge/handlers.go:29-58`
- `config/config.go:79-83`
- `MacBridge/MacBridge/Services/RuntimeManager.swift:118-167`
- `go-bridge/pairing_handler.go:16-17`
- `go-bridge/device_conn_registry.go:15`

[问题] v4 已指出文件过大；新增结论是阻碍测试隔离的根因不是行数，而是每个文件都同时拥有多个生命周期和共享可变状态，且部分状态仍通过包级全局变量隐式注入。

[证据]

`Handlers` 同时拥有：

```text
agents/sessions/opencodeSessionOptions/contentRefs/seq
broadcaster/relayRunning/pendingNotifications
prekeys/observation/outbox/presentation/relay router
trustedDevices/relayIdentity/transcriptIndex/capabilityPolicy
```

`config` 使用：

```go
var configMu sync.Mutex
var ConfigPath string
```

Swift `RuntimeManager` 同时负责 process、bootstrap、port takeover、日志、sleep/wake、token、OpenCode desktop 配置和状态机。

[影响] 多实例测试无法独立配置 pairing/device registry 和 ConfigPath；并发测试会互相串行或污染全局状态。`RuntimeManager` 的无结构 Task 与 process generation 共存于 `@MainActor`，使时序 bug 很难用纯状态机测试复现。任何“大文件拆分”若继续共享同一批 map/mutex，只会移动代码，不会降低风险。

[修复建议]

具体拆分边界：

1. `go-bridge/handlers.go`
   - `RPCDispatcher`：method→handler 路由与 capability policy。
   - `SessionSupervisor`：session registry、状态迁移、idle cleanup、shutdown。
   - `SessionEventPump`：event channel、rebind、seq、broadcast、pending notification。
   - `HistoryService`：list/get/pagination/transcript index。
   - `FileAccessService`：authorized root、read_file、content refs。
   - `RelayDeliveryCoordinator`：observation/outbox/prekey/offline milestone。
   - 首要风险点：`sessions`、`relayRunning`、`seq` 和 `opencodeSessionOptions` 必须各有唯一 owner，不能被多个拆分类型直接持有 map 指针。
2. `config/config.go`
   - `schema.go`、`validate.go`、`load.go`、`provider_resolution.go`。
   - `repository.go`：`ConfigRepository{path, mu, fs}`，替代 `ConfigPath/configMu`。
   - `mutation_project.go`、`mutation_provider.go`、`mutation_platform.go`。
   - `toml_patch.go`：保留 surgical editing 原语。
   - 首要风险点：路径和锁必须实例化，否则拆文件后测试仍共享全局 ConfigPath。
3. `RuntimeManager.swift`
   - `BridgeProcessSupervisor` actor：launch generation、terminate、crash budget。
   - `RuntimeBootstrapReader`：runtime.json/token/ready frame 校验。
   - `PortOwnershipService`：lsof、takeover policy。
   - `RuntimeLogSink`：权限、滚动、pipe drain。
   - `SleepWakeCoordinator`：只发状态事件，不直接启动进程。
   - `OpenCodeDesktopConfigurator` 与 `OfficialRelayProvisioner` 独立文件。
   - 首要风险点：`bridgeProcess`、`lastLaunchedPID`、`userStopped`、restart Task 必须归同一个 actor，不能拆成跨对象可写属性。
4. 将 `globalPairingStore`、`globalDeviceStore`、`globalPairingRegistry`、`globalPairingGate`、`globalDeviceConnRegistry` 放入 `RuntimeDependencies`，由 `Main()` 创建并显式注入。

### 3.11 Codex 重连测试自身存在数据竞争，race 门禁当前不能通过

[v4 状态] v4 未覆盖

[严重度] 🟡低

[类别] P2-I 测试质量与 flaky

[位置]

- `agent/codex/passive_subscriber_test.go:282-312`

[问题] `TestPassiveSubscribe_ReconnectAfterServerClose` 的 HTTP/WebSocket handler 可能并发执行，却无同步地读写闭包变量 `closeCount`；测试对“第一次/第二次连接”的判断因此既有 data race，也依赖不稳定的 handler 调度顺序。

[证据]

```go
closeCount := 0
fake.onConnect = func(conn *websocket.Conn) {
	closeCount++
	...
	if closeCount == 1 {
		return
	}
}
```

本轮执行 `go test -race ./go-bridge ./agent/... -count=1` 得到：

```text
WARNING: DATA RACE
Write: passive_subscriber_test.go:290
Read:  passive_subscriber_test.go:311
--- FAIL: TestPassiveSubscribe_ReconnectAfterServerClose
```

[影响] CI 若加入 race detector 会稳定失败；普通测试下，两个连接 handler 的调度可能让“第一连接关闭、第二连接发事件”的假设反转，形成偶发 flaky。它也会掩盖 passive subscriber 本身真正的关闭/重连 race。

[修复建议]

1. 使用 `atomic.Int32.Add(1)` 获取本次连接序号，后续只比较局部 `connectionNumber`。
2. 更稳妥地用 channel 明确协调“第一连接已完成握手并关闭”后再发起第二次 Subscribe。
3. 修复后将 `go test -race ./agent/codex -run TestPassiveSubscribe_ReconnectAfterServerClose -count=20` 纳入定向门禁。

## 4. P0 优先级建议

如果只能修 5 件事，按当前工作树排序：

1. 剥离 agent 子进程中的 management/Relay 控制面环境变量，并统一 stderr redaction。
2. 实现 runtime 级 session/agent shutdown，确保进程组被回收。
3. 修复所有 events channel 的关闭所有权，消除 `send on closed channel`。
4. 将 Relay device 写改为 per-device 有界队列，消除 route 级队头阻塞。
5. 给 Swift restart 增加 cancel/generation，保证配置合并后只启动一次 runtime。

与 v4 P0/P1 的排序关系：

- 若生产部署尚未包含当前工作树已经实现的 v4 修复，v4 的 `read_file` 越权仍应排总优先级第 1；直连帧上限、配对限流、设备存储事务、TLS fail-closed 继续是发布门禁。
- 若部署基线就是当前工作树，则上述 v4 项已有代码与测试证据，本轮新增的“控制面凭据进入 agent 环境”成为新的最高优先级安全问题。
- `runtime.json` 假就绪和 management timeout 应与 Swift restart generation 同一批修复，以形成完整跨进程状态机，而不是三个零散补丁。

## 5. 需要进一步验证的疑点

以下项目仅凭静态阅读不能诚实地下确定结论，需要运行期验证：

1. **孤儿进程实际范围**：分别用 Claude CLI、Codex exec、Codex stdio app-server、OpenCode CLI 启动长 turn，向 go-bridge 发 SIGTERM，记录父/子/孙 PID 是否存活。重点验证 shell wrapper、sudo `run_as_user` 和 agent 自身插件子进程。
2. **events channel panic**：用 helper process/自定义 `io.ReadCloser` 故意在 Close timeout 后恢复并发送事件；应在修复前稳定复现、修复后通过。普通 race detector 不一定捕获 closed-channel panic。
3. **Relay 队头阻塞**：需要真实 TCP 缓冲而非只用内存 fake。A 设备连接后不读，连续发送大 envelope，同时测 B 设备端到端延迟。
4. **Swift restart 竞态**：用注入的 process launcher/clock 写纯 unit test；无需 UI automation。连续配置通知、Relay provisioning 回调和用户 restart 应收敛为一个 generation。
5. **睡眠/唤醒长跑**：当前 wake 只轮询，进程活着就不主动重建 Relay/session。需要真机 Mac sleep/wake 长跑确认 socket、agent stdin/stdout 和 Relay reconnect 的真实行为。
6. **sessionRegistry 并发模型**：`get()` 返回内部 `*trackedSession` 指针后解锁，当前若未来新增无锁字段写入可能出现 race。建议用 `go test -race` 配合并发 send/abort/cleanup/rebind 压测，而不是仅凭结构下 race 结论。
7. **transcript 超大数据集**：应生成数万/数十万消息 JSONL，测首次索引时间、增量重建、磁盘大小和并发分页；当前测试证明边界正确，不等于资源曲线已知。
8. **管理 API 半开**：用本地 server 接受请求但不返回，验证当前 supervisor 实际卡住时长和修复后的恢复上限。

按项目约束，本轮没有运行 UI tests、snapshot tests、Simulator automation 或真机自动化。

## 6. 验证记录

本轮实际执行：

```text
go test ./... -count=1                       PASS
go vet ./...                                 PASS
(cd relay-server && go test ./... -count=1) PASS
(cd relay-server && go vet ./...)           PASS
go test -race ./go-bridge ./agent/...       FAIL
(cd relay-server && go test -race ./internal/relay)
                                                PASS
```

普通单元测试与 vet 全部通过。race 运行在 `agent/codex/passive_subscriber_test.go:290/311` 检出测试代码自身的 `closeCount` 数据竞争，详见 3.11；其余本次执行到的包未报告 race。现有测试没有覆盖本报告最关键的 shutdown 子进程回收、Close timeout 后 producer 恢复、Relay 慢消费者跨设备隔离和 Swift restart generation，因此“普通测试通过”不能反证这些发现。

## 7. 结论

当前代码比 v4 审查时的安全基线明显更强，主要安全边界整改已经进入实现层；本轮没有发现需要推翻 v4 结论的新密码学或存储一致性漏洞。

发布前最需要补齐的是“控制面秘密不得进入 agent 数据面”和“runtime 是所有子进程的真正 supervisor”这两条系统不变量。完成凭据环境隔离、session shutdown、channel 关闭所有权、Relay per-device 背压隔离和 Swift launch generation 后，运行期稳定性才与现有协议/存储安全水平相匹配。
