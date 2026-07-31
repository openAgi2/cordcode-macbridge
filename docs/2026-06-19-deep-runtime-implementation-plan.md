# CCCode MacBridge 深度运行期 Code Review 修复执行规格

> **命名说明（2026-06-24）：** 本文档写于 repo rename 之前。文中 cccode-macbridge/cccode-ios 指 GitHub 旧仓库名(现为 cordcode-*);Go module path 已从 github.com/openAgi2/cccode-macbridge 重命名为 …/cordcode-macbridge。本文为历史记录。


> 日期：2026-06-19
> 代码基线：`c6be451`（main）
> 证据来源：`docs/2026-06-18-deep-runtime-code-review.md`（11 项发现，4 高 / 3 中 / 4 低）
> 评审依据：`docs/2026-06-18-deep-runtime-code-review-评审意见.md`（评审已确认全部发现成立，含 3 处描述修正，本规格已采纳）
> 状态：全部任务可直接开发；T10 为架构债，本轮只做最小治理，不做大文件拆分。
> 目标：把 11 项评审发现转换为固定方案、原子任务、精确锚点、测试要求和验收条件，供开发 agent 无需再做产品或架构选型即可执行。

---

## 1. 执行规则

1. 按任务编号顺序执行。每个任务必须形成独立、可 review 的变更单元；只有用户明确要求时才创建 git commit。不得把多个任务混成一次大重构。
2. 不引入 production fallback、mock 数据、placeholder 或“先跑起来”的降级路径。
3. 不运行 UI tests、snapshot tests 或 simulator automation。默认验证为代码阅读、定向 unit test、Go test/vet/race、macOS build。
4. 修改 Go 代码后（root module `github.com/openAgi2/cccode-macbridge`）：

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1
go test ./go-bridge/... -run <TestName> -count=1      # 单测
go vet ./...
go test -race ./go-bridge ./agent/... -count=1        # 涉及并发的任务必须跑 race
```

5. 修改 `relay-server/` 后（**独立 module `cccode-relay`，必须 cd 进去**；提交代码不会更新线上 relay）：

```bash
(cd relay-server && go test ./... -count=1)
(cd relay-server && go vet ./...)
(cd relay-server && go test -race ./internal/relay -count=1)   # T04 必须
```

6. 修改 Swift 代码后（工程由 XcodeGen 从 `MacBridge/project.yml` 生成，不得只手改 `.xcodeproj`）：

```bash
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge \
  -configuration Debug -destination 'platform=macOS' build
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge \
  -configuration Debug -destination 'platform=macOS' test
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge \
  -configuration Debug -destination 'platform=macOS' test \
  -only-testing:CCCodeBridgeTests/<TestClassName>/<testMethod>   # 单测
```

7. `relay-server/` 是独立部署链：本仓库的修复**不会自动上线**，relay 修复（T04）在代码与测试通过后，由开发 agent 按 `CLAUDE.md`「Deploying relay-server to the VPS」段落执行部署（见下方 T04 部署步骤）。凭据来自 `~/.zshrc` 的环境变量与 `~/.ssh/config` 的 `cccode-relay-prod` 别名，脚本非交互式，**不得提交任何密码/VPS 凭据**。
8. 每个任务完成报告必须包含：改动文件、行为变化、测试命令及结果、未覆盖的运行期验证。禁止只写“已修复”。
9. 涉及 agent 子进程环境的任务（T01）必须额外验证：控制面变量不得出现在任何 agent 的 `cmd.Env`。
10. 不得把项目的有意设计批成缺陷：120 分钟定时重启、relay 独立 module、capability opt-in interface、TLS fail-closed 均是既定约束。

## 2. 全局完成标准

全部非阻塞任务完成后必须满足：

- agent 子进程（Claude/Codex/OpenCode 及其 stdio app-server）的 `cmd.Env` 不含任何 `CCCODE_*` 控制面变量；agent stderr 不原样进入日志或 `EventError`。
- runtime 统一 shutdown 关闭所有 active session/agent 子进程（进程组回收，非仅 SIGTERM 直接 PID）。
- 所有 events channel 的关闭只发生在 producer（`wg`）确认退出之后；任何 `Close()` 路径的 timeout 分支不再直接 `close(events)`。
- Relay 的 bridge→device 投递改为 per-device 有界发送队列；单个慢设备不阻塞同 route 下其他设备。
- Swift `restart()` 用 launch generation + 可取消 Task 收敛；100ms 内连续三次 restart 只启动一次进程。
- management-token / runtime.json 写失败时 fail-fast，不发布 ready。
- management API 客户端有 2–3s 请求超时，慢响应不阻塞监控循环。
- 配对限流 bucket 有 TTL 清理与全局容量上限。
- Go test / vet、relay-server test / vet / race、agent race、macOS build 全部通过。

## 3. 任务队列

### T01 剥离 agent 子进程中的控制面凭据 + stderr redaction

对应发现：3.1（🔴 高，本轮最高优先级安全问题）
依赖：无

#### 不变量

agent 数据面（Claude CLI / Codex / OpenCode 及其 stdio app-server、以及它们的工具子进程）**永远**不得接触 go-bridge 的控制面秘密：`CCCODE_MANAGEMENT_TOKEN`、`CCCODE_RELAY_CREDENTIAL`、`CCCODE_RELAY_ROUTE_ID`、`CCCODE_RELAY_ENDPOINT`。agent 进程本身就是远端设备的本地执行代理，这些变量一旦泄漏，远端可借 agent 工具读出后调 loopback `/internal/shutdown`、设备吊销、配对审批等接口，绕过 capability policy。

#### 解阻前提：go-bridge 解析后立即 Unsetenv

go-bridge 从环境读取控制面配置后，立即清除进程自身环境（`go-bridge/main.go` 解析完配置处），与现有 `clearOpenCodeServerAuthEnv()`（`main.go:608-611`）合并：

```go
func clearControlPlaneEnv() {
	for _, k := range []string{
		"CCCODE_MANAGEMENT_TOKEN",
		"CCCODE_RELAY_CREDENTIAL",
		"CCCODE_RELAY_ROUTE_ID",
		"CCCODE_RELAY_ENDPOINT",
		"OPENCODE_SERVER_USERNAME",
		"OPENCODE_SERVER_PASSWORD",
	} {
		_ = os.Unsetenv(k)
	}
}
```

在 `main.go` 读完这些变量、构造完配置对象之后、`NewHandlers` 之前调用一次。注意：这只防“go-bridge 进程后续 fork 时不再带”，不防“agent 显式 `os.Environ()`”——后者由下面的统一构造函数根治。

#### 固定实现：统一 agent 环境构造函数

新增 `core.BuildAgentEnv(base, providerEnv, sessionEnv []string) []string`（放 `core/message.go` 旁，与 `MergeEnv` 同包）：

- **`base` 不再是 `os.Environ()`**——那正是本 bug 根因（全量继承才会泄漏 `CCCODE_*`）。`base` 应是受控的最小启动环境（`PATH`、`HOME`、`USER`、`LANG`/`LC_*`、`TMPDIR` 等运行必需项），由调用方显式传入或由 helper 从 `os.Environ()` 过滤出一个最小 allowlist。
- 先用拒绝列表无条件剔除：`CCCODE_*` 前缀、`OPENCODE_SERVER_USERNAME`、`OPENCODE_SERVER_PASSWORD`，无论来自 base 还是 extra。
- 再 `MergeEnv` 合并 providerEnv（按目标 agent 注入的 API key 等）与 sessionEnv。
- 合并后再扫一遍拒绝列表做 belt-and-braces（防止 extra 里夹带）。

四条 spawn 路径必须共用此函数，替换当前各自的写法：

| 路径 | 当前写法 | 行号 |
| --- | --- | --- |
| Claude exec | `filterEnv(os.Environ(), "CLAUDECODE")` + `MergeEnv` + `FilterEnvForSpawn` | `agent/claudecode/session.go:149-158` |
| Codex exec | `core.MergeEnv(os.Environ(), cs.extraEnv)` | `agent/codex/session.go:127` |
| Codex stdio app-server | 见 `appserver_session.go` 的 spawn 段 | `agent/codex/appserver_session.go:243-253` |
| OpenCode CLI | `env := os.Environ()` + `MergeEnv` | `agent/opencode/session.go:86-92` |

Claude 的 `filterEnv(env, "CLAUDECODE")`（`session.go:865`，防 nested session 检测）逻辑并入 `BuildAgentEnv` 的拒绝列表（仍剔除 `CLAUDECODE` 前缀）。Claude 现有的 `FilterEnvForSpawn`（`session.go:157`，run_as_user allowlist）继续在最外层调用，顺序：`BuildAgentEnv(...)` → `FilterEnvForSpawn(env, spawnOpts)`。

#### 固定实现：stderr redaction

agent stderr 进入日志/wire error 前走内容级 redactor。涉及三处（当前都是原样进日志 + 包成 `EventError`）：

- `agent/claudecode/session.go:272-284`（`finishReadLoop`）
- `agent/codex/session.go:241-255`（`readLoop`）
- OpenCode/Codex app-server 对应的错误构造处（grep `EventError` + `stderr`）

新增 `core.RedactStderr(s string) string`（`core/redact.go` 旁）：

- 默认只保留：退出码、稳定错误分类、长度。`fmt.Errorf("%s", stderrMsg)` 改为 `fmt.Errorf("%s", core.RedactStderr(stderrMsg))`。
- 正则剔除疑似 token / 长 base64 / `CCCODE_*` / `Bearer ` 前缀片段。
- 完整 stderr 仅在显式诊断模式（环境变量 `CCCODE_DEBUG_STDERR=1`）写入 `0600` 受保护文件，不进 `EventError`、不进 `slog`。

#### 测试

- 新增 `core` 包测试：父进程环境注入四个 `CCCODE_*` + OpenCode 凭据，断言 `BuildAgentEnv` 输出不含任一。
- 新增定向测试：断言三条路径的 `cmd.Env` 均不含 `CCCODE_*`——通过把 env 构造提取为可注入函数或读取 `cmd.Env` 的测试 hook。
- 新增 `RedactStderr` 测试：输入含 `CCCODE_MANAGEMENT_TOKEN=secret` 与长 base64 的 stderr，断言输出不含原值、稳定可重现。
- 已有测试不得回归：`go test ./go-bridge/... ./agent/... -count=1`。

#### 验收

```bash
rg -n 'CCCODE_MANAGEMENT_TOKEN|CCCODE_RELAY_CREDENTIAL|CCCODE_RELAY_ROUTE_ID|CCCODE_RELAY_ENDPOINT' \
  agent/ --glob '!*_test.go'
```
上述命令允许命中 redactor 的拒绝列表定义（白名单），但不得命中把 `os.Environ()` 直接赋给 `cmd.Env` 的路径。

---

### T02 runtime 级 session/agent shutdown + 生命周期组件统一关闭

对应发现：3.2（🔴 高）+ 3.9（🟡 低，并入同一生命周期接口）
依赖：无（但与 T03 配合形成完整进程治理，建议相邻执行）

#### 问题边界（评审修正）

`handlers.go:191-217` 的 `cleanupIdleSessions()`（由 `StartCleanupLoop` 周期调用）已会按 backend TTL 回收**空闲** session 并 `sess.Close()`。本任务针对的是 runtime shutdown 时仍处于 **active（`state != sessionStateIdle`，执行中 turn）** 的 session——它们既不在 shutdown 的遍历范围（shutdown 根本没遍历 registry），也不满足 idle 条件，因此是孤儿来源。

#### 固定实现：Handlers 持有 root ctx + 注入 StartSession

1. `Handlers` 新增字段 `ctx context.Context`（`handlers.go:29-58` 结构体内）。`NewHandlers()` 不再启动后台 goroutine（见下方第 4 点），改为 `NewHandlers(ctx)` 接收 root ctx；`main.go:84` 调用处改为 `handlers := NewHandlers(ctx)`。
2. 把两处 `agent.StartSession(context.Background(), ...)`（`handlers.go:358` 与 `:1604`）改为 `agent.StartSession(h.ctx, ...)`。**注意**：连接断开不得自动取消 session（当前行为正确，保持），只有 runtime shutdown 取消 root ctx 时才传播。
3. `Handlers` 新增幂等 `Shutdown(ctx context.Context) error`：
   - 停止 cleanup ticker（T09 同步处理，见下）。
   - 调 `observation.Stop()`（`relay_observation.go` 的 `ObservationManager` 已有 `stopCh`，`NewHandlers` 隐式 `go om.leaseCheckLoop()`，改为显式 Stop）。
   - 锁内快照 `sessions` registry 并清空 map；锁外**并发或有界串行**调用每个 `core.AgentSession.Close()`，受传入 `ctx` 的 deadline 约束。
4. `StartCleanupLoop`（`handlers.go:183-189`）改用 `time.NewTicker` + `select` 监听 `ctx.Done()`/stop channel，提供 stop；不再用不可停止的 `time.Tick`。`NewHandlers()` 不再隐式起 observation goroutine——observation loop 改由显式 `Start(ctx)` 启动，`Shutdown` 里 Stop。`main.go:146` `handlers.StartCleanupLoop(60 * time.Second)` 保持，但内部可停。

#### 固定实现：shutdown 顺序 + 进程组回收

`main.go:371-391` 的 `shutdown()`（当前只 `cancel()` + 关 WS/HTTP/Relay/mgmt）顺序改为：

```text
停止接收新 RPC（HTTP Server.Shutdown，graceful）
→ handlers.Shutdown(ctx)（关闭 active session/agent，进程组回收）
→ 广播 shutdown / 关闭 active WS 连接（server.CloseAllConnections）
→ relayBridgeClient.Close() + relay/tls/mgmt Server.Shutdown
```

- 三个 `Agent.Stop()`（`claudecode.go:1098`、`codex.go:435`、`opencode.go:503`）当前 `return nil`。本轮**不要求**实现完整 Stop 语义（session 级 Close 已覆盖单进程回收），但保留为 `// TODO: process-group stop` 占位，不得声称已实现。
- Claude 的 `Close()` 当前只向直接进程发 SIGTERM/SIGKILL（见 `claudecode` 的 kill 路径），Codex 已用 process-group（`forceKillAllCmds`，`codex/session.go:870`）。本轮把 Claude 的 kill 也改为杀进程组（`Setpgid` + 负 PID），与 Codex 对齐。

#### 测试

- 新增集成测试：启动一个会阻塞的 helper 子进程作为 agent session，调用 `handlers.Shutdown(ctx)`，断言父 PID 与孙进程 PID 均在 deadline 内消失（`syscall.Kill(-pgid, 0)` 探活）。
- 新增测试：`Shutdown` 幂等（调用两次不 panic、不重复 Close）。
- 新增测试：`StartCleanupLoop` 在 `ctx.Done()` 后 goroutine 退出（用 `runtime.NumGoroutine()` 或 channel 同步断言，不用 `time.Sleep`）。
- race：`go test -race ./go-bridge ./agent/... -count=1`。

#### 验收

- `rg -n 'StartSession\(context\.Background\(' go-bridge/` 无命中。
- `Handlers` 有 `Shutdown(ctx)` 且 `main.go` shutdown 顺序在 HTTP Shutdown 之后、WS close 之前调用它。
- 真机子进程实测（睡眠/唤醒长跑）列为运行期验证，不阻塞本任务代码完成。

---

### T03 修复所有 events channel 的关闭所有权

对应发现：3.3（🔴 高，评审已修正各路径分层）
依赖：无（与 T02 相邻执行效果最佳）

#### 不变量

**只有 producer owner 可以关闭 `events`，或 `Close()` 必须等所有 producer（`wg`）退出后再关。** timeout 分支绝不直接 `close(events)`。这是同仓库 `codexSession.Close()`（`agent/codex/session.go:850-893`）已确立的正确模式，本轮把其余路径对齐。

#### 范本：codexSession.Close()（照此复用）

```go
done := make(chan struct{})
go func() { cs.wg.Wait(); close(done) }()
select {
case <-done:
    cs.closeOnce.Do(func() { close(cs.events) })
case <-time.After(closeTimeout):
    // 强杀进程组；仍不直接 close，改为延迟 goroutine 等 done
    forceKillAllCmds(...)
    go func() { <-done; cs.closeOnce.Do(func() { close(cs.events) }) }()
}
```

#### 各路径固定动作

| 路径 | 当前问题 | 动作 |
| --- | --- | --- |
| `opencode/session.go:480-494` | **无 `closeOnce` 字段，超时后直接 `close(s.events)`** | 新增 `closeOnce sync.Once` 字段；`close(s.events)` 全部包进 `closeOnce.Do`；超时分支改为延迟 goroutine 等 `wg` 退出后再关（不复用直接关） |
| `codex/passive_subscriber.go:532-555` | `closeOnce`（:534）只包了 conn 关闭，**未包** :554 的 `close(s.events)` | 把 `close(s.events)` 移入 `closeOnce.Do`；超时分支改为延迟关闭模式 |
| `codex/appserver_session.go:697-733` | 有 `closeOnce.Do(close)`（:731），但超时分支（:728）后仍走到 close，producer 此后发送仍 panic | 把 close 从超时分支移出，超时后改为延迟 goroutine 等 `wg` 退出；复用 codex exec 模式 |
| `opencode/sse_subscriber.go:740-754` | close 已在 `closeOnce` 内 + `emit()` 有 `default` 兜底，**风险已低** | 按同一延迟关闭模式统一即可，不必单独大改；确认 emit 不会在 close 后阻塞即可 |

timeout 后必须主动关闭底层 pipe/body/socket 并杀进程组；若仍未退出，返回明确错误但**不要**关闭 channel。

#### 关于 emit 的安全网误区

`emit` 的 `select { case ch <- ev: ... default: }` **不能**防 closed-channel panic——向已关闭 channel 发送即使走 default 分支也会 panic。修复以"close 只在 producer 退出后发生"为唯一保证，不得依赖 emit 兜底。

#### 测试

- 新增定向测试：自定义 `io.ReadCloser`/helper process 故意在 `Close()` timeout 后才向 events 发送一条事件；修复前应稳定 panic（可 recover 断言），修复后 producer 正常退出、不 panic。
- 对四条路径各跑一次该模式测试。
- race：`go test -race ./agent/... -count=1`，重点跑 `codex` / `opencode`。

#### 验收

- `rg -n 'close\(.*\.events\)' agent/` 所有命中均在 `closeOnce.Do` 或 `done` 退出分支内，无 timeout 分支直接 close。
- opencode session 结构体含 `closeOnce sync.Once`。

---

### T04 Relay per-device 有界发送队列（消除 route 级队头阻塞）

对应发现：3.4（🔴 高）
依赖：无
注意：`relay-server` 是独立 module + 独立部署链。代码与测试通过后，由开发 agent 按 `CLAUDE.md`「Deploying relay-server to the VPS」流程执行部署（步骤见下）。

#### 问题

`relay-server/internal/relay/server.go:521-566` 的 `readBridgeFrames` 是 bridge socket 唯一读循环，对每个 envelope 同步调 `target.write(payload)`（:553）。`socketPeer.write`（:613-619）持 `writeMu` 设 120s 写 deadline。慢设备 TCP 接收窗口满时，单次 `WriteMessage` 阻塞最长 120s（持续灌帧可远超），整个 for 循环停住，同 route 其他 device 读不出来。

#### 固定实现：per-device 有界队列 + writer goroutine

`socketPeer`（`server.go:66-69`，当前只有 `conn` + `writeMu`）新增：

```go
type socketPeer struct {
    conn    *websocket.Conn
    writeMu sync.Mutex   // 仅保护 control frame / close 等直写
    sendCh  chan []byte  // 有界发送队列
    done    chan struct{}
    // metrics: queueBytes int64, drops int64, writeLatency（atomic）
}
```

- 每个 `socketPeer` 启动唯一 writer goroutine：从 `sendCh` 取帧 → 持 `writeMu` 设 deadline → `WriteMessage`。
- `readBridgeFrames`（:552-557）改为**非阻塞 enqueue**：`select { case target.sendCh <- payload: case default: /* 队列满 */ }`，不再同步等写完成。
- 队列满（帧数或字节上限）时断开该慢 device：关闭 conn、从 device map 移除（`removeDevice`，:680），并把当前 envelope 写 mailbox（`AppendFrame`，:559）保证不丢。
- bridge→device 在线投递与 mailbox append 的幂等语义：在线 enqueue 成功即 continue；enqueue 失败（队列满→断开）才 append mailbox。避免"在线写成功又入 mailbox"重复。
- **不得**为每帧启动无界 goroutine（那是另一种 OOM）。

#### 边界常量

```go
const (
    perDeviceSendQueueFrames = 256      // 单 device 发送队列帧数上限
    perDeviceSendQueueBytes  = 8 << 20  // 8 MiB 字节上限，二者先到先断
)
```

enqueue 前同时检查帧数与累计字节；任一超限即断开该 device。

#### 测试

- route 级隔离测试：device A 连接后不读（用真实 TCP 缓冲，非内存 fake），向 A 灌满缓冲；同时 device B 正常读。断言 B 在 < 1s 内收到消息，A 被断开。
- 队列满时当前 envelope 正确入 mailbox，B 重连后能收到。
- writer goroutine 在 peer 关闭后退出（无泄漏）。
- race：`(cd relay-server && go test -race ./internal/relay -count=1)`。
- metrics：断言 per-device queue bytes / drop 次数可观测（logger 或 atomic 字段）。

#### 验收

- `readBridgeFrames` 内无同步 `target.write(payload)` 调用（全部走 `sendCh`）。
- route 级隔离测试通过，单 device 阻塞不影响其他 device。
- **不修改 `relayWriteDeadline`（120s）的值**来绕过问题——它是为容纳大帧慢链路传输而设的有意值，背压改在队列层解决。

#### 部署（开发 agent 执行）

代码与测试通过后，按 `CLAUDE.md`「Routine binary update」流程部署到 VPS：

1. 交叉编译 linux/amd64：

```bash
(cd relay-server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o /tmp/cccode-relay-server ./cmd/relay-server)
```

2. 运行非交互式安全部署脚本（只读核查 → 备份 → 上传 → SHA 校验 → 原子替换 → 重启 → 健康检查）：

```bash
scripts/deploy-relay-vps.sh
```

部署前置条件（开发 agent 需先确认，缺失则记 blocker 不强行部署）：
- `~/.zshrc` 含 `CCCODE_RELAY_VPS_HOST` / `CCCODE_RELAY_VPS_USER` / `CCCODE_RELAY_VPS_PASS`（先 `source ~/.zshrc`）；`~/.ssh/config` 含 `cccode-relay-prod` 别名。
- 该 VPS sshd 有慢 banner exchange（UseDNS 反查 + 间歇性网络），脚本已自动用 `ConnectTimeout`/`ConnectionAttempts` 重试；若手动 ssh 需多试几次。
- 部署后 Mac 的 `RelayBridgeClient` 自动重连，iOS 客户端会有短暂「连接中」抖动，属正常。
- **绝不提交密码或任何 VPS 凭据**；脚本输出的 rollback 命令（指向 `/opt/cccode-relay/bin/relay-server.bak.<UTC>`）需在完成报告中记录。
- 不得触碰同 VPS 上的旧 frp tunnel（nginx `:9090`）。

---

### T05 Swift restart 用 launch generation + 可取消 Task 收敛

对应发现：3.5（🟠 中）
依赖：无

#### 问题

`RuntimeManager.swift:187-200` 的 `restart()` 每次创建一个不可取消、无 generation 校验的 1.5s 延迟 Task。`AppDependencies.swift:164-198` 的 `handleRemoteURLChange()` 末尾（:181）调一次 `restart()`，紧接着 Task 内 Relay provisioning 完成（:192）又调一次——真实存在的连续双 restart。多个延迟 Task 依次 launch，靠 `prepareRuntimeOwnershipForLaunch()`（:563-584）SIGTERM/SIGKILL 同路径旧进程互相接管，导致端口反复接管、session 丢失、ready frame 抖动。

#### 固定实现

1. 保存 `restartTask: Task<Void, Never>?`（`RuntimeManager` 字段，`bridgeProcess`/`lastLaunchedPID`/`userStopped` 同区）。新 `restart()` 到来时先 `restartTask?.cancel()`。
2. 新增单调 `launchGeneration: Int`（`@MainActor` 存储属性，初始 0）。`restart()` 入口 `launchGeneration += 1` 并捕获局部 `let gen = launchGeneration`。Task 醒来后必须验证 `gen == launchGeneration`，不等则直接 return。
3. 收敛配置更新：新增 `applyConfigAndRestart(_ apply: (inout RuntimeConfig) -> Void)`，先原子改 config 再只调度一次 restart。`AppDependencies` 的 `handleRemoteURLChange()` / Relay provisioning 回调改为先 `applyConfigAndRestart { c in ... }` 合并所有字段变更，再只 restart 一次。
4. `launchBridgeProcess()` 开头增加守卫：若已有当前 generation 的进程正在 starting/running，return（防重入）。

```swift
func restart() {
    userStopped = true
    crashCount = 0
    launchGeneration += 1
    let gen = launchGeneration
    restartTask?.cancel()
    terminateProcess()
    setStatus(.starting, "正在重启 Bridge...")
    restartTask = Task { [weak self] in
        try? await Task.sleep(nanoseconds: 1_500_000_000)
        guard let self, gen == self.launchGeneration, !Task.isCancelled else { return }
        self.userStopped = false
        self.launchBridgeProcess()
    }
}
```

#### 测试

- 纯 unit test（注入 process launcher / clock，无需 UI automation）：100ms 内连续调用三次 `restart()`，断言只执行一次 process launch。
- 配置更新合并：连续 remoteURL 变更 + Relay provisioning 回调，断言只 restart 一次、config 为最终合并值。
- generation 回退：旧 Task 醒来时 generation 已变，断言不 launch。

#### 验收

- `restart()` 保存并 cancel 旧 `restartTask`；存在 `launchGeneration` 守卫。
- `applyConfigAndRestart` 被配置更新路径采用，无双 restart。

---

### T06 runtime.json / management-token 写失败 fail-fast

对应发现：3.6（🟠 中）
依赖：无（与 T05 同批，形成跨进程状态机）

#### 问题

`WriteReadyFrame`（`go-bridge/runtime_startup.go:44-64`）无返回值；`management-token` 写失败（`main.go:410-412`）和 `runtime.json` 写失败（`runtime_startup.go:61-63`）只 `slog.Error` 后继续输出 stdout ready。Swift 实际只轮询文件、不消费 ready frame，因此磁盘满/权限错误时产生"对网络已开放但 UI 永远未就绪"的 runtime，每 60s 重启。

#### 固定实现

1. `WriteReadyFrame` 改为 `func WriteReadyFrame(...) error`，把 `runtime.json` 的 `AtomicWriteFile` 错误返回。
2. `management-token` 写失败（`main.go:408-413`）和 `WriteReadyFrame` 返回的 error：product 模式下写 `runtime_error.bootstrap_persist_failed`（`WriteErrorFrame`）并 `os.Exit(1)`，不发布 ready。
3. Swift 侧（`RuntimeManager.swift:438-466`）同时解析 stdout 的结构化 `runtime_ready` / `runtime_error`，文件轮询作为持久 bootstrap 交叉校验，不再唯一信号。
4. ready frame 增加并校验 `bridgeEpoch`（`runtime_startup.go:48` 已生成）；Swift 同时匹配 PID、port、epoch，防同 PID 生命周期外的旧文件误判。

#### 测试

- 只读目录/磁盘写失败测试：mock 或临时只读 dataDir，断言不进入 ready、退出码非 0。
- `WriteReadyFrame` 返回 error 时上层 fail-fast。

#### 验收

- `WriteReadyFrame` 签名含 `error`；写失败路径走 `WriteErrorFrame` + exit。
- `rg -n 'WriteReadyFrame\(' go-bridge/` 所有调用检查返回值。

---

### T07 Management API 客户端短超时 + 轮询解耦

对应发现：3.7（🟠 中）
依赖：无

#### 问题

`ManagementAPIClient.swift:140` 用 `URLSession.shared`（默认超时）；`RuntimeManager.startMonitoring()`（:381-392）串行 `await pollManagementAPI()` + `evaluateAutoRestart()`，请求挂起期间不执行 3s 轮询与自动重启判定。

#### 固定实现

1. 用专用 ephemeral `URLSessionConfiguration`：`timeoutIntervalForRequest = 2`，`timeoutIntervalForResource = 5`。在 `ManagementAPIClient` 内持有 `URLSession`（`init` 创建），替换 `URLSession.shared`。
2. status 与 agents 不串行决定 liveness：status 成功即更新状态；agents 刷新独立低优先级执行（`async let` 或独立 task），不阻塞 status 轮询周期。
3. 每轮 poll 加 generation/PID 校验（与 T05 的 `launchGeneration` / `lastLaunchedPID` 协同）：旧请求返回后不得覆盖新 runtime 状态。

#### 测试

- management server 接受连接但不返回响应（`URLSession` 配置测试）：断言 supervisor 在 ≤ 5s 内进入恢复流程，而非等系统默认超时。
- status 成功但 agents 慢：status 状态更新不被 agents 阻塞。

#### 验收

- `ManagementAPIClient` 无 `URLSession.shared`，用专用 configuration。
- 监控循环 status/agents 解耦。

---

### T08 配对限流 bucket TTL 清理 + 全局容量上限

对应发现：3.8（🟡 低）
依赖：无

#### 问题

`pairing_hardening.go:34-37` 的 `sourceBuckets` / `pairFails`（`map[string]*slidingBucket`）只增不删；任意 pairingId 会创建永久条目（`recordPairingFailure` :108-114）。

#### 固定实现

1. 为 `pairingAttemptGate` 增加惰性 TTL 清理：`allow` / `recordPairingFailure` 入口清理窗口内无计数的 bucket；或新增定期清理（并入 T09 的可停 ticker）。
2. 全局最大 bucket 数上限（如 `maxBuckets = 4096`）；容量满时对新 key fail closed（返回 `pairing.rate_limited`）。
3. 未找到 session 时不要为任意 pairingId 建立独立长期 bucket：用有界哈希分片或只依赖来源限流。
4. 将 `globalPairingGate`（`pairing_hardening.go:39-42`）注入 server 实例，避免测试共享进程级状态（与 T10 的 `RuntimeDependencies` 注入方向一致，但本轮只做注入，不做完整 god-object 拆分）。

#### 测试

- 大量随机 pairingId 提交后，map 大小收敛到上限，不单调增长。
- TTL 过期后 bucket 被清理。
- 容量满时新 key fail closed。

#### 验收

- `pairingAttemptGate` 有清理路径与容量上限；`globalPairingGate` 注入实例。

---

### T09 Handler 生命周期组件可整体关闭（并入 T02 接口）

对应发现：3.9（🟡 低）
依赖：T02（共用 `Handlers.Shutdown` / `Start(ctx)`）

#### 问题

`NewHandlers()`（`handlers.go:65-87`）隐式启动 observation goroutine（`NewObservationManager()` 内 `go om.leaseCheckLoop()`，`relay_observation.go:60-61`）；`StartCleanupLoop`（:183-189）用不可停止的 `time.Tick`；production shutdown 无统一调用点。

#### 固定实现

本任务的动作已并入 T02 的"`NewHandlers(ctx)` + 可停 cleanup ticker + `ObservationManager.Start(ctx)/Stop()`"。本任务仅做以下补充：

- `ObservationManager` 拆出显式 `Start(ctx)`（不在构造函数起 goroutine）；`Handlers.Shutdown` 调 `observation.Stop()`（已有 `stopCh`，:50）。
- 所有测试 `t.Cleanup(handlers.Shutdown)` 或 `Close()`，不依赖全局默认实例。

#### 测试

- 多次创建 `Handlers` + `Shutdown` 后，旧 ticker / goroutine 退出（无泄漏）。

#### 验收

- `NewHandlers()` 构造函数内无 `go ...` 启动语句；observation 由显式 `Start` 启动。
- 与 T02 验收合并。

---

### T10 god-object 治理（本轮最小治理，不拆文件）

对应发现：3.10（🟡 低，架构债）
依赖：无
状态：本轮**不做**大文件拆分（handlers.go 3200 行 / config.go 3200 行 / RuntimeManager.swift 1100 行），只做最小治理以降低多实例测试污染。

#### 固定实现（最小）

1. `config/config.go` 的包级全局 `var configMu sync.Mutex` / `var ConfigPath string`（:79-83）本轮**不删**（删动会牵连全包），但新增一个 `ConfigRepository{path, mu, fs}` 类型供新代码使用，旧全局标记 `// Deprecated:`，新代码不得再引用全局。
2. 包级全局 device/pairing store（`globalPairingStore` / `globalDeviceStore` / `globalPairingRegistry` / `globalPairingGate` / `globalDeviceConnRegistry`）按 T08 方向注入 server 实例；本轮注入 pairing gate（T08），其余 store 标注后续任务。
3. 本任务不产出拆分后的新文件结构（评审 §3.10 的拆分边界作为后续大重构的输入，不在本轮执行范围）。

#### 验收

- `config` 新代码不引用 `Deprecated` 全局；pairing gate 注入实例。
- 不得在本任务顺便拆 handlers.go（会破坏 T02–T09 的 review 边界）。

---

### T11 修复 Codex 重连测试自身数据竞争

对应发现：3.11（🟡 低，测试 flaky / race 门禁阻断）
依赖：无

#### 问题（已实跑复现）

`agent/codex/passive_subscriber_test.go:287` 的 `closeCount := 0`（普通 int）在 `fake.onConnect` 闭包里 `closeCount++`（:290，Write）又在 `if closeCount == 1`（:311，Read）。WebSocket onConnect 由多个连接 goroutine 并发执行，无同步。

实跑确认（`go test -race`）：

```text
WARNING: DATA RACE
Write: passive_subscriber_test.go:290
Read:  passive_subscriber_test.go:311
--- FAIL: TestPassiveSubscribe_ReconnectAfterServerClose
```

#### 固定实现

1. `closeCount` 改为 `atomic.Int32`；连接内 `n := closeCount.Add(1)` 取本次连接序号，后续只比较局部 `n`。
2. 更稳妥：用 channel 明确协调"第一连接已完成握手并关闭"后再发起第二次 Subscribe，避免依赖 handler 调度顺序。

#### 测试

- 修复后 `go test -race ./agent/codex -run TestPassiveSubscribe_ReconnectAfterServerClose -count=20` 稳定通过。
- 纳入定向 race 门禁。

#### 验收

- `passive_subscriber_test.go` 无裸 int 的 `closeCount` 并发读写。
- `go test -race ./go-bridge ./agent/... -count=1` 通过（无 race）。

## 4. 推荐执行顺序

```text
第一批（安全 + 进程治理核心，4 项高风险）：
  T01 控制面凭据隔离（最高优先级安全问题）
  T03 events channel 关闭所有权
  T02 runtime session/agent shutdown（含 T09 生命周期）
  T04 Relay per-device 有界队列

第二批（跨进程契约，3 项中风险，形成完整状态机）：
  T05 Swift restart generation
  T06 runtime.json 假就绪 fail-fast
  T07 management 短超时

第三批（稳定性 / 债务 / 测试，4 项低风险）：
  T08 配对 bucket TTL
  T10 god-object 最小治理
  T11 测试 race 修复

部署（开发 agent 执行，T04 代码与测试通过后）：
  T04 relay-server 按 CLAUDE.md「Deploying relay-server to the VPS」流程部署
```

T01–T04 是第一批，完成后先提交一次完整 review；T05–T07 第二批；T08/T10/T11 第三批。禁止一个 PR 同时修改安全边界、进程生命周期、Relay 背压和 Swift 状态机。

## 5. 与 v4 整改（`code-review-2026-06-18.md`）的优先级关系

- 若生产部署**尚未**落地 v4 已实现的修复，v4 的 `read_file` 越权仍排总优先级第 1；直连帧上限、配对限流、设备存储事务、TLS fail-closed 继续是发布门禁。
- 若部署基线就是当前工作树（`c6be451`），v4 项已有代码与测试证据，则本轮 T01（控制面凭据进入 agent 环境）成为新的最高优先级安全问题。
- 本规格所有任务均在 v4 已覆盖安全边界**之外**（运行期时序 / 进程治理 / 并发 / 跨进程契约），与 v4 不冲突、不重复。

## 6. 需运行期验证（不阻塞代码完成）

以下仅凭静态阅读 + unit test 不能下定论，需 owner 授权的运行期/真机验证：

1. **孤儿进程实测**：Claude CLI / Codex exec / Codex stdio app-server / OpenCode CLI 启动长 turn，向 go-bridge 发 SIGTERM，记录父/子/孙 PID。重点 shell wrapper、sudo `run_as_user`、agent 插件子进程。（T02）
2. **events channel panic**：helper process 故意在 Close timeout 后发送事件，修复前复现、修复后通过。普通 race detector 不一定捕获 closed-channel panic。（T03）
3. **Relay 队头阻塞**：真实 TCP 缓冲，A device 不读连续灌大 envelope，测 B device 端到端延迟。（T04，relay-server 部署后）
4. **睡眠/唤醒长跑**：Mac sleep/wake 长跑确认 socket、agent stdin/stdout、Relay reconnect 真实行为。（T02/T05）
5. **management 半开**：本地 server 接受请求不返回，验证 supervisor 实际卡住时长与修复后上限。（T07）

## 7. 开发 Agent 启动指令

可直接把下面内容交给开发 agent：

```text
读取 AGENTS.md、CLAUDE.md、docs/2026-06-18-deep-runtime-code-review.md、
docs/2026-06-18-deep-runtime-code-review-评审意见.md 和本文件
docs/2026-06-19-deep-runtime-implementation-plan.md。

以 implementation plan 为唯一实现规格，按 T01→T11 的推荐顺序执行。
每个任务单独实现、定向测试并形成可 review 变更单元；不要合并任务，
不要添加 fallback/mock/placeholder。

禁止自动运行 UI tests、snapshot tests 或 simulator automation。
默认验证为代码阅读 + 定向 unit test + Go test/vet/race + macOS build。

修改 Go（root module）后：go build ./go-bridge; go test ./go-bridge/... -count=1; go vet ./...
涉及并发的任务（T02/T03/T04/T11）必须额外：go test -race ./go-bridge ./agent/...
（T11 含: -run TestPassiveSubscribe_ReconnectAfterServerClose -count=20）
修改 relay-server 后（独立 module）：cd relay-server && go test ./... && go vet ./...
（T04 必须: go test -race ./internal/relay）
修改 Swift 后：xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge
  -configuration Debug -destination 'platform=macOS' build
  以及对应的定向 test。

relay-server 是独立部署链：T04 代码与测试通过后，由你（开发 agent）按 CLAUDE.md
「Deploying relay-server to the VPS」流程部署——先交叉编译，再跑 scripts/deploy-relay-vps.sh。
部署前置条件（~/.zshrc 的 CCCODE_RELAY_VPS_* 与 ~/.ssh/config 的 cccode-relay-prod）缺失时
记 blocker，不强行部署；绝不提交密码/VPS 凭据；不要碰同 VPS 的旧 frp tunnel (:9090)。

只有本任务测试、相关 build、race 全部通过，才能标记任务完成；
完成报告必须含改动文件、行为变化、测试命令及结果、未覆盖的运行期验证。
```
