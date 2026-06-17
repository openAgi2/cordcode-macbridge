# go-bridge "iOS 卡连接中、重启即恢复" 根因报告

> 日期：2026-06-17
> 状态：根因已定位，修复方案待实施
> 排查方法：代码级走读 + `/tmp/go-bridge.log` 实证比对

## 现象

iOS 端长时间显示"连接中"，手动到 MacBridge 界面点重启按钮就恢复。
用户怀疑缓存满 / 网络问题，实际两者都不是。

## 结论（一句话）

go-bridge 的 WebSocket 连接缺少保活（ping/pong + 读写 deadline），
叠加"持锁无 deadline 的网络写"。半开连接（iOS 进后台 / TCP 半开）
无法被及时探测，写操作阻塞数十秒至数分钟（TCP 重传超时 RTO）并卡住
连接锁，拖垮保活机制与广播路径，表现为全员卡"连接中"。重启进程
清空一切 goroutine/锁/TCP 状态，故重启即恢复 —— 治标不治本，问题会反复。

## 日志实证（`/tmp/go-bridge.log`，采样窗口 00:59–01:05）

### 1. relay WebSocket 静默断连 —— "连接中"的直接来源

```
00:59:21  relay-bridge-client: connected          ← 连上 relay
01:00:16  hello_ack sent via relay                 ← 最后一次活动
01:05:34  ERROR read error: unexpected EOF         ← 5分18秒后才报错
01:05:37  connected                                ← 重连恢复
```

中间约 5 分钟 iOS 端就是卡"连接中"。Mac↔relay 的 WebSocket
在 01:00 后无任何动静，直到 TCP 层超时抛 EOF；**期间没有任何
保活探测发现连接已死**。

### 2. 写延迟飙升 —— 连接已变慢的信号

`socket_send_ms` 字段（163 个 relay 路径样本）：

| 指标 | 值 |
|------|-----|
| 平均 | 44.9 ms |
| 最大 | 1319.6 ms |
| <10ms（正常） | 99 (61%) |
| 10–50ms（偏慢） | 44 (27%) |
| 50–200ms（慢） | 10 (6%) |
| ≥200ms（严重卡顿） | 10 (6%) |

正常应为 1–5ms。说明写路径已开始堆积。

### 3. 全程 `transport_route=relay`

当前走的就是 relay 路径（`wss://relay.byteseek.uk:8443`），
正好命中下列 relay 侧问题。

## 代码根因（按严重度排序）

### 🔴 根因 A：所有 WebSocket 写都没有 deadline + 持锁写（最高优先级）

`go-bridge/server.go:94-116`

```go
func (c *Conn) SendJSON(v interface{}) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if c.closed { return }
    if err := c.conn.WriteJSON(v); err != nil {   // 无 deadline，持锁写
        ...
    }
}
```

- `WriteJSON` 在持锁状态下写网络，无超时。
- 对端半开时（iOS 进后台 / TCP 半开），该写会**阻塞数十秒至数分钟**，
  直到 TCP 重传超时（RTO，Retransmission Timeout）耗尽、内核报错返回。
  注意：这是 TCP 层的**重传超时**机制，与根因 B 提到的 OS TCP
  **keepalive**（空闲探测，macOS 默认约 2 小时）是两套独立机制，
  不要混淆——RTO 负责处理"已发出但收不到 ACK 的数据"，keepalive
  负责处理"连接长时间无任何流量"。本次持锁写卡住走的是 RTO 那条。
- 锁被占住期间：
  - 检测死连接的 ping ticker 也要抢同一把锁（`server.go:293`）
    → **保活机制本身被卡住**。
  - `Broadcaster.Send` 给所有连接串行发事件（`types.go:417-469`）
    → **一个卡住的连接拖垮全员事件投递**。
- 这正是"大家同时卡连接中"的机制。
- 注意：直接 `/` WebSocket 路径其实**有** ping/pong + 90s 读 deadline
  （`server.go:281-312`），但被这个 deadline-less 写抵消了。

**只有连续 5 次写错误才关连接**（`server.go:103-112`），
对半开连接的恢复太慢。

### 🔴 根因 B：relay-bridge-client 完全没有 ping/keepalive

`go-bridge/relay_bridge_client.go:80-121, 189-199, 203-242`

- 重连逻辑有（exponential backoff，初始 2s 最大 60s，`relay_bridge_client.go:80-121`）。
- 但 **没有任何 `SetReadDeadline` / `SetWriteDeadline` / ping ticker**
  （grep 全文确认，命中的全是 backoff，无保活）。
- Mac↔relay 的 WebSocket 空闲时只靠 OS TCP 默认 keepalive 兜底，
  macOS 默认要约 **2 小时**才探测半开连接。
- 所以 iOS 一进后台，这条连接能"假活"很久，期间 iOS 端就是"连接中"。
- 日志里那次 5 分钟断连，是 relay 服务器侧先超时断的，**不是 Mac 主动发现的**。

### 🟠 根因 C：`/pairing` 路径同样无 deadline

`go-bridge/pairing_handler.go:223-245`

- `pairing_handler.go:221-222` 注释明确写"不设 read deadline，
  靠 iOS 端 ping 保活 + OS TCP keepalive 检测"——和根因 B 同一设计缺陷。
- iOS 进后台中断配对时，读 goroutine 阻塞在 `ReadMessage` 直到 TCP 层超时。

> **勘误（据 2026-06-17 评审修订）**：本报告初版曾把
> `go-bridge/relay_hub.go:255-257, 287-290` 列为 relay 设备路径根因，
> **这是定位错误，已撤销**。`relay_hub.go` 是 `relayServiceAddr` 非空时
> 才启用的进程内 **loopback 联调 hub**（见 `main.go:307-329`，错误信息
> 明写 "only loopback test listeners are allowed"）；本次故障走的是
> 外部 relay `wss://relay.byteseek.uk:8443`，其转发实现位于**独立模块
> `relay-server`（module `cccode-relay`）**，不在 go-bridge 内。
> 因此给 `relay_hub.go` 加 `SetWriteDeadline` 对本次外部 relay 故障
> 完全无效——那代码生产时不在这条连接上跑。
> 外部 relay 路径上 Mac 侧的修复点是 `RelayBridgeClient`（根因 B 已覆盖）；
> relay 服务器侧的修复需到 `relay-server` 模块另开。

## 次要问题（非主因，影响长期稳定性）

- **`relayEvents` goroutine 未耦合连接关闭**（`handlers.go:1680-1831`）：
  select 只看 `<-events` 和 `<-idleTimer.C`，无 `<-connClosed`。
  连接断后若 agent 仍发事件，goroutine 继续向死连接写（经根因 A 阻塞），
  且 `relayRunning[sessionID]` 在此期间不释放，新连接也无法 relay。
  **有兜底**：收到首个事件后有 60s 空闲超时（`relayActiveTimeout`，
  `handlers.go:1711, 1803`），无事件时 goroutine 最多再活 60s 即退出。
  故此项**不是高优泄漏**，不必抢排在 P0 前面——但加 `<-connClosed`
  能把最坏延迟从 60s 降到即时，建议在 P1 做。
- **`ObservationManager.devices` 无界增长**（`relay_observation.go`）：
  `RemoveDevice`/`Stop` 定义了但生产代码从不调用。慢泄漏。
- **`relay_hub.go` mailbox 切片从不压缩**（`relay_hub.go:316-339, 404-435`）：
  `AckMailbox` 只标记 `acked=true`，不删除元素；`FetchMailbox` 每次
  O(n) 全扫。注意 `relay_hub.go` 是进程内 **loopback 联调 hub**
  （见上文勘误），不在本次外部 relay 故障路径上；此项属联调场景的
  长期退化，与生产"卡连接中"无关，**不单列修复**。

## 已验证 NOT 是问题

- `contentRefs` / `contentRefOrder`：200 上限 FIFO 淘汰（`handlers.go:3009-3012`）✅
- `opencodeSessionOptions`：多处 prune ✅
- `PendingNotificationStore`：每设备 50 上限 ✅
- `sessionRegistry` / `relayRunning`：`cleanupIdleSessions` 清理 ✅（前提是 session 真空闲）
- `PairingSessionStore`：有 `DeleteExpired`/`CleanupAll` ✅
- `ActiveConnRegistry` / `DeviceConnRegistry`：注销在 `onCleanup` / cleanup ✅

## 修复方案

| 优先级 | 修复 | 文件 | 效果 |
|--------|------|------|------|
| **P0** | 所有客户端写加 `SetWriteDeadline`（建议 5–10s） | `server.go`、`relay_bridge_client.go`、`pairing_handler.go` | **单点根治**：写超时立即失败，不卡死锁，坏连接被快速标记并清理 |
| **P0** | relay-bridge-client 加 ping ticker（30s ping + 60s 读 deadline） | `relay_bridge_client.go` | **根治日志里的 5 分钟假死**：主动探测半开连接，30s 内重连 |
| **P1** | `/pairing` 加读 deadline + ping | `pairing_handler.go` | 防配对中途 iOS 进后台卡死 |
| **P1** | `relayEvents` 加 `<-connClosed` 退出路径 | `handlers.go` | 防连接断后 goroutine 泄漏堆积 |
| **P2** | `ObservationManager` 接 `RemoveDevice`/`Stop` | `relay_observation.go` | 治长期内存泄漏 |

> **勘误（据 2026-06-17 评审修订）**：上表初版曾把 `relay_hub.go`
> 列入 P0 写 deadline 范围，**已撤销**。`relay_hub.go` 是进程内
> loopback 联调 hub，不在本次外部 relay 故障路径上（详见上文根因 C 勘误）。
> P0 写 deadline 在外部 relay 路径上的落点是 `RelayBridgeClient`
> （根因 B 已覆盖）；relay 服务器侧的转发写超时需到 `relay-server`
> 模块另开。
>
> 另，relay-bridge-client 除已列的 `SendEnvelope`（L189-199）外，
> **还有两处无 deadline 写评审已补入**：`L325`（server hello）、
> `L431`（hello error），P0 写 deadline 一并覆盖。

**P0 两条做完，"卡连接中"大概率根治。**

## 参考实现要点（给后续实施者）

### 写 deadline（P0-A）

gorilla/websocket 的 `WriteMessage`/`WriteJSON` 不会自带 deadline。
在所有持锁写之前加：

```go
_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
if err := c.conn.WriteJSON(v); err != nil {
    // 失败处理：连续错误计数 / 关连接
}
```

注意 gorilla 限制：同一 conn 的写不能并发，必须保证仍在同一把锁内。
现有 `c.mu` / `writeMu` 已经满足，只是缺 deadline 那一行。

可考虑在 `SendJSON`、`relay_bridge_client.go` 的三处写
（`SendEnvelope` L198、server hello L325、hello error L431）、
`pairing_handler.go:86/266` 统一加。

> **配套修订（据 2026-06-17 评审）**：`server.go:103-112` 在置 `closed=true`
> 后只调 `onCleanup`，**未调 `ws.Close()`**，读循环仍挂在 `ReadMessage`。
> 加写 deadline 后应让它走 `CloseWithControl` 完整关闭，否则旧连接的
> 读 goroutine 不会及时退出。

### relay-bridge-client 保活（P0-B）

参考直接路径的 `server.go:281-312` 实现：
- 连接建立后 `SetReadDeadline(60s)`，每次收到 pong 重置。
- 起 ticker 每 30s 发一次 `WriteControl(PingMessage)`（带短 deadline）。
- pong handler `SetPongHandler` 里重置读 deadline。
- 读超时 / ping 写失败 → 返回，触发现有重连 backoff。

> **前提与边界（据 2026-06-17 第二轮评审，并经实测修正）**：P0-B 让 Mac 端在 90s 内
> 主动探测并重连，**绕开日志里那 5 分 18 秒的等待**。第二轮评审推断"relay 侧
> 5 分 18 秒断回 EOF 说明它有读超时"——**经实测需修正**：`relay-server/internal/relay/server.go`
> 生产代码**也无任何 SetReadDeadline/SetWriteDeadline/ping**（唯一一处 SetReadDeadline
> 在测试里），所以那 5 分多钟断开更可能是 gorilla 默认初始读 deadline 或对端 TCP RTO，
> 而非 relay 主动保活。**这意味着 relay-server 自身同样会卡，必须一并补齐**
> （见实施规格文档 `2026-06-17-bridge-hang-implementation-spec.md`）。
>
> 故 P0-B 单独做能让 **Mac 侧** 不卡，但若 relay-server 不补，**relay 服务器侧**
> 仍可能成为瓶颈。本次实施范围已定为 go-bridge + relay-server 一起改。若上线后仍偶发
> 长卡顿，再深挖两端交互。报告保留"大概率"措辞即为此，不当"确定"。

### relayEvents goroutine 耦合关闭（P1）

`Conn.CloseWithControl` / `onCleanup` 里 close 一个 `connClosed` channel，
`relayEvents` 的 select 加 `case <-connClosed: return`。

## 相关文件（绝对路径）

- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/server.go`（根因 A）
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/handlers.go`（次要：relayEvents 泄漏）
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/pairing_handler.go`（根因 C）
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/relay_bridge_client.go`（根因 B；含 L198/L325/L431 三处无 deadline 写）
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/relay_observation.go`（次要：泄漏）
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/types.go`（根因 A：Broadcaster.Send）
- `/Users/jacklee/Projects/cccode-macbridge/go-bridge/main.go:307-349`（relay 启用条件：证明 `relay_hub.go` 仅 loopback 联调，不在外部 relay 路径上）
- `/Users/jacklee/Projects/cccode-macbridge/relay-server/`（独立模块 `cccode-relay`：外部 relay 服务器转发实现；relay 服务器侧的写超时修复在此模块，本次排查未深入）

## 备注

- 本次排查已在 MacBridge 侧加了"卡住自动重启 + 定时兜底重启"作为保险丝
  （`RuntimeManager.swift` + `SettingsView.swift`），但那是缓解，不是根治。
- 真正的根治在 go-bridge，按本报告 P0 两条实施即可。

## 修订历史

正文各处的 `勘误` 块是就近提示；全局性改动统一归档于此。

### 2026-06-17 第一轮评审（已落地）

1. **撤销 `relay_hub.go` 根因定位**：核实 `main.go:307-329`，`relay_hub.go`
   是 `relayServiceAddr` 非空时才启用的进程内 loopback 联调 hub，不在本次
   外部 relay（`wss://relay.byteseek.uk:8443`）故障路径上。外部 relay 转发
   实现在独立模块 `relay-server`（module `cccode-relay`）。修复表 P0 行、
   文件清单、根因 C 同步更正。
2. **补入遗漏的两处无 deadline 写**：`relay_bridge_client.go:325`
   （server hello）、`L431`（hello error），连同 `SendEnvelope`(L198)
   一并纳入 P0 写 deadline 范围。
3. **`server.go:103-112` 关闭不彻底**：置 `closed=true` 后未调
   `ws.Close()`，读循环仍挂；加写 deadline 后应走 `CloseWithControl`
   完整关闭。
4. **措辞校准**：根因 A 的"阻塞数分钟"明确为 TCP 重传超时（RTO），
   与根因 B 的 OS keepalive（空闲探测，~2h）分清为两套独立机制。

### 2026-06-17 第二轮评审（已落地）

5. **一句话结论措辞同步**：L16 由"阻塞数分钟"改为"阻塞数十秒至数分钟
   （TCP 重传超时 RTO）"，与正文精度一致。
6. **`relayEvents` 加兜底说明**：核实存在 60s 空闲超时兜底
   （`relayActiveTimeout`，`handlers.go:1711, 1803`），故"relayRunning
   不释放"**不是高优泄漏**，不应抢排在 P0 前；加 `<-connClosed` 仅把
   最坏延迟从 60s 降到即时，归 P1。
7. **mailbox 条目标注归属**：属联调场景长期退化，与生产"卡连接中"
   无关，**不单列修复**，消除次要问题列与修复表的错位。
8. **P0-B 前提亮在明处（并经实测修正）**：第二轮评审推断"relay 侧 5 分 18 秒
   断回 EOF 说明它有读超时"——**实测 `relay-server/internal/relay/server.go`
   生产代码也无任何 SetReadDeadline/SetWriteDeadline/ping**，故那断开更可能
   是 gorilla 默认读 deadline 或对端 TCP RTO。结论：relay-server **自身也会卡**，
   必须随 go-bridge 一起补齐读写 deadline + ping（见实施规格文档
   `2026-06-17-bridge-hang-implementation-spec.md`）。报告保留"大概率"措辞，
   不当"确定"。
