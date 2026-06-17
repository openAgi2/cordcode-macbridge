# Bridge "卡连接中" 根治实施规格（给实施 agent）

> 日期：2026-06-17
> 配套根因报告：`docs/2026-06-17-bridge-connecting-hang-root-cause.md`
> 本文档是把根因结论转成**可直接执行的实施指令**，补全了根因报告里
> 未展开的技术细节（写 deadline 落点、gorilla 并发约束、RelayBridgeClient
> 改造结构、deadline 值依据、验证方法、构建链）。
> 实施前请先通读根因报告的"根因 A / B"和"修订历史"。

## 实施范围

按用户确认：**go-bridge（P0-A + P0-B）+ relay-server（读写 deadline 核实/补齐）**。
`relay_hub.go` 不碰（loopback 联调 hub，不在故障路径上）。

## ⚠️ 实施前必读：几个会做错的坑

### 坑 1：gorilla/websocket 的写并发约束

**同一 `*websocket.Conn`，多个 goroutine 不能同时调用 `WriteMessage`/`WriteJSON`**
（会 panic 或数据错乱）。gorilla 只保证"一个写者 + 多个读者"，或"用锁串行化写"。

因此 `SetWriteDeadline` **必须在已持写锁的情况下调用**，且必须紧贴写调用：
```go
// ✅ 正确：deadline + 写 在同一把锁内，原子
c.mu.Lock()
defer c.mu.Unlock()
_ = c.conn.SetWriteDeadline(time.Now().Add(10*time.Second))
err := c.conn.WriteJSON(v)

// ❌ 错误：deadline 在锁外，另一个写者可能插进来
c.conn.SetWriteDeadline(...)
c.mu.Lock()
c.conn.WriteJSON(v)
```

**注意 `WriteControl`（ping/pong/close）是 gorilla 唯一允许与 WriteMessage 并发的调用**
（gorilla 内部用单独的 channel）。所以 ping 的 `WriteControl(PingMessage,...)`
（server.go:308）**不要**套写锁，也不要给它加数据写 deadline——它自带短 deadline
（第 3 参）。只给 `WriteJSON`/`WriteMessage` 这类**数据写**加 deadline。

### 坑 2：deadline 值不要照抄根因报告的数字

根因报告给的"5-10s 写 / 30s ping / 60s 读"是**示意**。实施时统一遵循
**go-bridge 直接路径现有值**（server.go:289/301/317），保持两条路径一致：

| 参数 | 直接路径现状 | 实施采用 | 定义位置 |
|------|-------------|---------|---------|
| ping 周期 | 30s（L289） | 30s | 新增包级变量 `var`（便于测试覆盖，见改进项 3） |
| 读超时（无 pong） | 90s（L301，`elapsed>90s`） | 90s | 复用/对齐 `bridgeReadTimeout` |
| 写 deadline | 无 | **10s** | 新增包级变量 `var` |

写 deadline 用 10s：远大于正常单帧写（<5ms，见日志 socket_send_ms），
远小于 TCP RTO（数十秒），既能快速发现坏连接，又不会误杀慢写。

### 坑 3：RelayBridgeClient 的 ping goroutine 生命周期

`RelayBridgeClient` 的结构和直接路径不同——它是**重连循环**：
- `Run()`（relay_bridge_client.go:81-123）是外层循环，每次重连调 `Connect()`
- `Connect()`（:58-76）建立新 conn、新建 `c.done`、起 `readLoop()`
- 连接断时 `readLoop` 退出并 `close(c.done)`，`Run` 收到信号后重连

**ping goroutine 必须挂在 `Connect()` 内、随 `c.done` 退出**，和 `readLoop`
同生命周期。否则重连后会泄漏上一个 ping goroutine，或写已关闭的旧 conn。
参考模式：
```go
func (c *RelayBridgeClient) Connect(relayBridgeURL string) error {
    // ... dial, 设 c.conn, c.done = make(...)
    go c.readLoop()
    go c.pingLoop(c.done)   // ← 新增：传入当前连接的 done
    return nil
}

func (c *RelayBridgeClient) pingLoop(done chan struct{}) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-done:
            return
        case <-ticker.C:
            c.mu.Lock()
            conn := c.conn
            c.mu.Unlock()
            if conn == nil { return }
            // WriteControl 不套 writeMu（见坑 1）：gorilla 允许 WriteControl 与
            // WriteMessage 并发。若套 writeMu，ping 会被数据写阻塞，违背加 ping
            // 的初衷——半开连接上数据写正在卡 writeMu 时，ping 也要等。
            err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
            if err != nil { return }
        }
    }
}
```
读超时通过 `SetReadDeadline` + `SetPongHandler` 实现，见 P0-B 详述。

## P0-A：所有数据写加写 deadline

### 落点清单（逐个文件、逐个函数，锁名已核实）

| 文件:行 | 函数 | 写锁 | 底层 ws 字段 | 改法 |
|---------|------|------|------------|------|
| `go-bridge/server.go:100` | `Conn.SendJSON` | `c.mu` | `c.conn` | WriteJSON 前 `c.conn.SetWriteDeadline(10s)` |
| `go-bridge/relay_bridge_client.go:198` | `SendEnvelope` | `c.writeMu` | `conn` | WriteMessage 前 `conn.SetWriteDeadline(10s)` |
| `go-bridge/relay_bridge_client.go:325` | server hello（handleClientHello 内） | `c.writeMu` | `conn` | 同上 |
| `go-bridge/relay_bridge_client.go:431` | `sendServerHelloError` | `c.writeMu` | `conn` | 同上 |
| `go-bridge/pairing_handler.go:86` | NotifyComplete | `conn.writeMu` | `conn.conn`（套一层） | WriteMessage 前 `conn.conn.SetWriteDeadline(10s)` |
| `go-bridge/pairing_handler.go:266` | 配对推送写 | `pending.writeMu` | `conn` | WriteMessage 前 `conn.SetWriteDeadline(10s)` |

> 锁名、字段名、套层结构均为第三轮评审核实结果，可直接用。
> `SetWriteDeadline` 本身并发安全，但**仍须在写锁内调用并紧贴写**，
> 否则 deadline 与实际写之间可能被另一个写者插入（见坑 1）。

### 配套：让 server.go 的关闭真正生效（⚠️ 含死锁陷阱）

根因报告修订项 3：`server.go:103-112` 连续 5 次写错误后置 `closed=true`，
只调 `onCleanup`，**没调 `ws.Close()`**，读 goroutine 仍挂在 ReadMessage。
加写 deadline 后，写失败会更快到来，必须让连接真正关闭。

> **🚫 死锁陷阱（第三轮评审阻塞项，必读）**：`CloseWithControl`
> （server.go:148）**第一行就 `c.mu.Lock()`**。而 `SendJSON` 调用时
> **已持有 `c.mu`**。所以**绝不能在 `SendJSON` 的失败分支里调
> `CloseWithControl`（或 `Close()`，它内部转调 `CloseWithControl`）**，
> 否则当场死锁。
>
> 正确做法：失败分支里调**底层 `c.conn.Close()`**（`*websocket.Conn.Close`，
> 不经 `c.mu`，是 gorilla 自己的方法）。这恰好绕开了重入 `c.mu`。
> 原方案手写的 `conn.Close()` 正确，但务必在注释里写明"不要改成
> CloseWithControl，会死锁"。

改 `SendJSON` 的失败分支：
```go
if c.consecutiveWriteErrors >= 5 {
    slog.Warn(...)
    c.closed = true
    // 关闭底层 ws，让读循环退出。注意：必须是 c.conn.Close()（gorilla 的方法），
    // 不能用 CloseWithControl 或 c.Close() —— 那两个会重入 c.mu 造成死锁。
    _ = c.conn.Close()
    if cleanup := c.onCleanup; cleanup != nil {
        c.onCleanup = nil
        c.mu.Unlock()
        cleanup()
        c.mu.Lock()
    }
}
```
（`c.conn` 是 `*websocket.Conn`（server.go:23 已核实，无套层），`Close()` 在持
`c.mu` 时调用安全——关闭后其他写会因 `closed` 检查提前返回。）

## P0-B：relay-bridge-client 加 ping/pong 保活

目标：Mac↔relay 这条 WebSocket 不再"假活"数分钟，30s ping + 90s 无 pong
判死，触发现有重连 backoff，绕开日志里 5 分 18 秒的等待。

### 改造步骤（基于 relay_bridge_client.go 现状）

1. **`Connect()`（:58-76）**：dial 成功后
   - `conn.SetReadDeadline(time.Now().Add(relayReadTimeout))`（90s）
   - `conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(relayReadTimeout)) })`
   - 起 `go c.pingLoop(c.done)`（见坑 3）

2. **`readLoop()`（:203-242）**：`ReadMessage` 报错时，区分"读超时"和"正常关闭"。
   读超时（`os.IsTimeout(err)` 或 gorilla 的 deadline）当作连接死，return 触发重连
   ——现有逻辑 `err != nil { return }` 已满足，确认即可。

3. **新增 `pingLoop(done)`**：见坑 3 代码。30s 发一次 ping，失败 return。

4. **包级变量**（文件顶部，对齐直接路径）——⚠️**必须用 `var` 不能用 `const`**：
   ```go
   // 用 var 而非 const：测试需要覆盖成短值（50ms）以避免等真实 10s/90s（见改进项 3）。
   // Go 的 const 是编译期常量，测试里无法重新赋值。
   var (
       relayPingPeriod   = 30 * time.Second
       relayReadTimeout  = 90 * time.Second
       relayWriteTimeout = 10 * time.Second
   )
   ```
   注意现有 `server.go:18` 的 `bridgeReadTimeout` 也是 `const`——若要让直接路径的
   ping/读超时同样可测，需一并改 `var`；或新测试只覆盖 relay 路径的上述新变量、
   不动 `bridgeReadTimeout`。二选一，实施时写明。

### 注意：现有重连逻辑保留不动

`Run()`（:81-123）的 exponential backoff（2s→60s，抖动±25%）是好的，**不要改**。
P0-B 只让"连接死"这件事在 90s 内被发现，重连策略沿用。

## relay-server 模块：核实并补齐读写 deadline

> 这部分**修正了根因报告 P0-B 前提的措辞**：评审说"relay 侧 5 分 18 秒
> 断回 EOF 说明它有读超时"。实测 `relay-server/internal/relay/server.go`
> **生产代码无任何 SetReadDeadline/SetWriteDeadline/ping**（唯一一处
> SetReadDeadline 在测试 server_test.go:368/427）。gorilla 升级后的 conn 默认
> **无**读 deadline（零值），所以日志里那 5 分 18 秒断开，更可能是**对端 TCP RTO
> 或 Cloudflare 空闲超时**（代码 `clientIP` 读 `CF-Connecting-IP`，部署在 CF 后），
> 而非 relay 主动保活。**这意味着 relay-server 自身同样会卡**，必须在本次一并补齐。

### 落点（函数名第三轮评审已核实）

`relay-server/internal/relay/server.go`：
- **`:568` `socketPeer.write`**（转发写，持 `writeMu`，`*websocket.Conn` 字段 `p.conn`）：
  WriteMessage 前 `p.conn.SetWriteDeadline(10s)`。
- **`:486` `readBridgeFrames`**（bridge 侧读循环）：加 `SetReadDeadline(90s)`
  让半开 bridge 连接被判死。
- **`:529` `readDeviceFrames`**（device 侧读循环）：同上加 `SetReadDeadline(90s)`。
- `socketPeer` 结构体（:55 附近）**无 lastPong 字段**——若加 ping/pong 需新增字段。

### 实施优先级（第三轮评审改进项 2）

**写 deadline > 读 deadline > ping**，按这个顺序做：
1. **写 deadline**（`socketPeer.write`）：最高收益，转发写不再被半开 peer 卡死。
2. **读 deadline**（`readBridgeFrames`/`readDeviceFrames`）：让 relay 侧能主动判死半开连接。
3. **ping/pong**：**收益最低，可缓做**——因为 Mac 侧（P0-B）已经主动发 ping，
   relay 只需被动回 pong（gorilla 默认行为），双向保活已由 Mac 侧驱动。
   relay 侧再加 ping 是冗余兜底，不阻塞 PR-2 交付。

relay-server 的 deadline 值与 go-bridge 保持一致（10s 写 / 90s 读）。

> relay-server 模块独立（`module cccode-relay`），有自己的 `_test.go`。
> 改完单独 `cd relay-server && go test ./...`。

## 验证方法（必须做，否则无法自证修好）

### 单元/集成测试

```bash
# go-bridge
cd go-bridge && go test ./... -run 'Ping|Deadline|Relay|Close' -v

# relay-server
cd relay-server && go test ./...
```

**新增测试（实施者必须补）**：
1. **写 deadline 测试**（go-bridge）：模拟对端不读（半开），断言写方在 ~10s
   内报错、连接被关闭，而不是挂死。参考现有 `TestPingTickerClosesOnTimeout`
   （events_test.go）的 httptest + Server 模式。
2. **relay-bridge-client ping 测试**（go-bridge）：起一个本地 relay server
   （或 mock），让对端停止读，断言 client 触发重连。
   - 现有 `TestPingTickerClosesOnTimeout` **只覆盖直接路径，不覆盖 relay 路径**，
     必须新增。
3. **relay-server 写/读 deadline 测试**（relay-server）：断言半开 peer 转发写在
   deadline 内失败、半开读循环在 deadline 内判死。

> **测试用可注入短超时（第三轮评审改进项 3）**：deadline 值（10s/90s）要做成
> **包级变量或结构体字段、可被测试覆盖**（如 `var writeTimeout = 10*time.Second`，
> 测试里改成 50ms）。否则断言"deadline 内重连/关闭"的用例要等几十秒，
> 测试又慢又 flaky。**不要把 deadline 写成函数内字面量**。

### 行为级验证（最能反映原症状）

复现原症状"iOS 进后台 → Mac 卡连接中数分钟 → 重启才恢复"，改后应变成
"iOS 进后台 → 90s 内 Mac 自动重连恢复"。具体：
1. 用 `go test` 的 deadline 测试覆盖（上面 3 条）。
2. 真机/模拟器：让 iOS 端连上、然后切到后台或断 Wi-Fi，观察 Mac 端日志
   `relay-bridge-client: connected` 是否在 ~90s 内重新出现（而非原 5 分多）。

## 交付：拆两个独立 PR（第三轮评审阻塞项 2）

> **为什么拆**：go-bridge 随 app 分发（用户更新 app 即生效），relay-server
> 要单独部署到 VPS（需运维介入）。两者生命周期不同，绑在一个 DoD 会互相阻塞。
> 而且 **Mac 侧 P0-B 靠 gorilla 默认 pong 就能工作，不阻塞于 relay-server 修复**——
> Mac 主动发 ping，relay 只需回 pong（gorilla 自动行为）。所以 PR-1 单独上线就能
> 解掉"卡连接中"的症状，PR-2 是健壮性增强。

### PR-1：go-bridge（解症状，随 app 分发）

- [ ] go-bridge 所有数据写（落点表 6 处）有 10s 写 deadline
- [ ] `server.go:103-112` 关闭时调底层 `c.conn.Close()`（注意死锁陷阱：不可用 `CloseWithControl` 或 `c.Close()`，会重入 `c.mu`）
- [ ] relay-bridge-client 有 30s ping + 90s 读超时 + pong handler
- [ ] 新增测试：写 deadline 测试 + relay-bridge-client ping 测试（可注入短超时）
- [ ] `cd go-bridge && go test ./...` 全绿
- [ ] 现有 `TestPingTickerClosesOnTimeout` 不回归
- [ ] 行为验证：iOS 进后台后 Mac 端 ~90s 内 `relay-bridge-client: connected` 重现

### PR-2：relay-server（健壮性，需 VPS 单独部署）

- [ ] `socketPeer.write` 加写 deadline
- [ ] `readBridgeFrames`/`readDeviceFrames` 加读 deadline
- [ ] ping/pong（收益最低，可缓做，见 relay-server 落点优先级）
- [ ] 新增测试：写/读 deadline 测试（可注入短超时）
- [ ] `cd relay-server && go test ./...` 全绿
- [ ] 提醒用户：此 PR 需部署到 relay VPS 才生效

## 构建与交付链（不要漏）

1. **go-bridge 改完，不需要手动编译 runtime**：Swift app 的 Xcode 构建脚本
   （`MacBridge/project.yml:47`）会在 build app 时自动
   `go build ./go-bridge/cmd/cccode-bridge-runtime`，产物打进 app 资源。
   所以流程是：改 go-bridge → Xcode build MacBridge app → 装到 /Applications。
2. **relay-server 是独立部署的服务**（跑在 VPS 上的 relay.byteseek.uk）。
   改完 relay-server 后，**需要重新部署到 relay 服务器**才生效——这不是
   app 更新能覆盖的。实施者改完代码后要提醒用户：relay-server 的修复
   需要单独部署。
3. 本地联调：`relay_hub.go`（loopback 联调 hub）可用，但与本次故障无关，
   **不要在那里加 deadline**。

## 风险与回滚

- **风险**：写 deadline 误判慢写为坏连接。10s 阈值远大于正常帧（<5ms），
  误杀概率低；若担心，可先设 15s 观察日志。
- **风险**：relay-bridge-client ping goroutine 泄漏。务必随 `c.done` 退出
  （见坑 3）。
- **回滚**：纯加 deadline + ping，无数据迁移，git revert 即可。
- **MacBridge 侧的自动重启保留**：作为兜底，P0 上线后若仍偶发卡顿，
  自动重启保证可用性，同时按根因报告修订项 8 去 relay-server 排查。

## 不要做的事

- ❌ 不要碰 `relay_hub.go`（loopback 联调，不在故障路径）
- ❌ 不要给 `WriteControl`(ping/pong) 加写锁或数据写 deadline（gorilla 允许并发）
- ❌ 不要改 `Run()` 的重连 backoff 策略（它是好的）
- ❌ 不要为追求"彻底"顺手做 P1/P2（pairing deadline、relayEvents connClosed、
  mailbox 压缩）——P0 足以根治卡连接中，P1/P2 另行排期，避免本次回归面过大
- ❌ 不要在没跑 `go test ./...` 的情况下声称完成
