# 评审：go-bridge "iOS 卡连接中、重启即恢复" 根因报告

> 评审日期：2026-06-17
> 被评审文档：[docs/2026-06-17-bridge-connecting-hang-root-cause.md](2026-06-17-bridge-connecting-hang-root-cause.md)
> 评审方法：逐条复核报告引用的代码位置 + 架构归属核实

## 总评

**结论方向正确，核心根因（A/B）是实锤，P0 修复路径成立；但存在一处影响修复有效性的重大定位错误（relay_hub.go 误归因），以及若干遗漏点。建议修订后再作为实施依据。**

| 维度 | 评价 |
|------|------|
| 核心结论（缺保活 + 持锁无 deadline 写） | ✅ 成立 |
| 根因 A（server.go SendJSON） | ✅ 完全准确，最高优先级实锤 |
| 根因 B（relay_bridge_client 无 ping） | ✅ 完全准确，本次 5 分钟假死的直接来源 |
| 根因 C（pairing 部分） | ✅ 准确 |
| **根因 C（relay_hub.go 部分）** | ❌ **定位错误：不在本次故障路径上** |
| 修复方案（P0-A / P0-B） | ✅ 技术正确，建议采纳 |
| 修复方案（文件清单） | ⚠️ 含误归因文件，需删除 relay_hub.go |
| 日志实证 | ✅ 有说服力，但仅单次采样窗口 |

---

## 一、证据逐条复核

### 根因 A — server.go 持锁无 deadline 写 ✅ 完全准确

| 报告引用 | 复核结果 |
|----------|----------|
| [server.go:94-116](../go-bridge/server.go#L94-L116) `SendJSON` 持锁 `WriteJSON` 无 deadline | ✅ 与源码一致 |
| [server.go:293](../go-bridge/server.go#L293) ping ticker 抢同一把 `conn.mu` → 保活被卡 | ✅ 准确。ping goroutine 在 [server.go:293](../go-bridge/server.go#L293) 和 [server.go:306](../go-bridge/server.go#L306) 两次抢 `conn.mu`；`SendJSON` 持锁阻塞期间 ticker 醒来也拿不到锁 |
| [server.go:103-112](../go-bridge/server.go#L103-L112) 连续 5 次写错误才关 | ✅ 准确 |
| [server.go:281-312](../go-bridge/server.go#L281-L312) 直接 `/` 路径其实有 ping/pong + 90s 读 deadline | ✅ 准确。但被 deadline-less 写抵消这一判断成立 |

**补充（强化报告结论）**：[types.go:417-469](../go-bridge/types.go#L417-L469) `Broadcaster.Send` 在 [types.go:465](../go-bridge/types.go#L465) 就释放了 `b.mu`，之后 [types.go:467-469](../go-bridge/types.go#L467-L469) 对每个 target **串行**调用 `conn.SendJSON`。加上 [types.go:447-463](../go-bridge/types.go#L447-L463) 的 fallback——无订阅者时广播给 `allConns` **所有连接**——意味着只要有一个慢连接还挂在 `allConns` 里没被 cleanup，**每一条事件**都会去戳它一次。这是"一个卡连接拖垮全员投递"的放大器，报告结论成立。

> 小订正：报告表述"Broadcaster.Send 给所有连接串行发事件"——`b.mu` 已释放，阻塞来自 `SendJSON` 的串行调用，不是 broadcaster 锁。结论不变。

### 根因 B — relay_bridge_client 无保活 ✅ 完全准确，本次故障主因

| 报告引用 | 复核结果 |
|----------|----------|
| [relay_bridge_client.go:80-121](../go-bridge/relay_bridge_client.go#L80-L121) 只有重连 backoff | ✅ 准确 |
| [relay_bridge_client.go:189-199](../go-bridge/relay_bridge_client.go#L189-L199) `SendEnvelope` 持 `writeMu` 写无 deadline | ✅ 准确 |
| [relay_bridge_client.go:203-242](../go-bridge/relay_bridge_client.go#L203-L242) `readLoop` 无 `SetReadDeadline` | ✅ 准确 |
| 全文无 ping / `SetReadDeadline` / `SetWriteDeadline` | ✅ 已通读全文（452 行）确认 |

日志中"01:00:16 最后活动 → 01:05:34 EOF（5 分 18 秒）"与"无保活、靠 OS TCP 兜底"完全吻合。这条是本次"卡连接中"的直接来源，修复优先级判断正确。

### 根因 C — pairing 部分 ✅ 准确

- [pairing_handler.go:221-222](../go-bridge/pairing_handler.go#L221-L222) 注释"不设 read deadline，靠 iOS ping + OS TCP keepalive" — ✅ 一字不差，设计缺陷自证。
- [pairing_handler.go:86](../go-bridge/pairing_handler.go#L86) `NotifyComplete` 持 `writeMu` 写无 deadline — ✅
- [pairing_handler.go:266](../go-bridge/pairing_handler.go#L266) `sendPairingResult` 写无 deadline — ✅

### 根因 C — relay_hub.go 部分 ❌ 重大定位错误

**问题：报告自述"全程走 `wss://relay.byteseek.uk:8443`（外部 relay）"，却把 [relay_hub.go:255-257](../go-bridge/relay_hub.go#L255-L257) / [relay_hub.go:287-290](../go-bridge/relay_hub.go#L287-L290) 的持锁无 deadline 写列为 Mac 端根因。但这条代码不在本次故障路径上。**

核实依据：

1. `go-bridge/relay_hub.go` 的 `RelayHub` 是 **go-bridge 进程内嵌的本地 relay 服务**，仅在 [main.go:311](../go-bridge/main.go#L311) `if strings.TrimSpace(*relayServiceAddr) != ""` 时才 `NewRelayHub()` 并起本地 loopback listener（[main.go:320-323](../go-bridge/main.go#L320-L323)）——这是**进程内/本地联调**路径。
2. 生产场景 MacBridge 连的是**外部 relay**（`relay.byteseek.uk:8443`）。外部 relay 服务器是**独立 Go 模块** `relay-server`（`module cccode-relay`，见 `relay-server/go.mod`），其转发实现是 `relay-server/internal/relay/server.go`，**不是** `go-bridge/relay_hub.go`。
3. [main.go:341-343](../go-bridge/main.go#L341-L343) 进一步印证：只有当 `sharedRelayHub != nil`（即本地联调模式）时，`bridgeWSURL` 才指向 `localRelayEndpoint`；否则连 `*relayEndpoint`（外部地址）。

**后果**：按报告 P0 修复表给 `go-bridge/relay_hub.go` 加 `SetWriteDeadline`，**对本次外部 relay 故障完全无效**——那份代码在生产时根本不在这条连接上跑。修复要么落到 Mac 端真正在跑的 `RelayBridgeClient`（即根因 B，已覆盖），要么得到 `relay-server` 独立模块去改（不归本仓库 go-bridge 管，且需 relay 运维方介入）。

**建议修订**：
- 把根因 C 里的 relay_hub.go 段落改标为"仅本地联调路径，非本次故障"；
- P0/P1 修复表删除 `relay_hub.go`，或单列一条"relay-server 模块侧转发代码需独立排查"指向 `relay-server/internal/relay/server.go`。

---

## 二、报告遗漏的点（建议补充）

### 1. relay_bridge_client 还有两处无 deadline 写未列入

除报告已列的 `SendEnvelope`([relay_bridge_client.go:198](../go-bridge/relay_bridge_client.go#L198))外：

- [relay_bridge_client.go:325](../go-bridge/relay_bridge_client.go#L325) `handleClientHello` 发 server hello
- [relay_bridge_client.go:431](../go-bridge/relay_bridge_client.go#L431) `sendServerHelloError`

这两处同样持 `writeMu` 写、无 deadline。握手阶段频率低、影响小，但 P0-A 实施时应一并加，否则半开连接上发 server hello 仍会卡住 `writeMu`，阻塞后续 `SendEnvelope`。

### 2. `closed` 标志置位后并未真正关底层 ws

[server.go:103-112](../go-bridge/server.go#L103-L112) 连续 5 次写错误后只置 `c.closed = true` 并跑 `onCleanup`，**没有调用 `ws.Close()`**。`onCleanup` 确实从 broadcaster / registry 摘除了业务引用，但 [server.go:316-327](../go-bridge/server.go#L316-L327) 的读循环仍在 `ReadMessage` 上挂着，goroutine 和 TCP fd 要等 OS 超时才真正回收。加了 P0-A 写 deadline 后，建议让"写超时"也直接触发 `CloseWithControl` 走完整关闭路径，否则换了一种 goroutine 悬挂方式。

### 3. "阻塞数分钟"的机制需表述更准

报告把写阻塞与"OS TCP keepalive 约 2 小时"挂钩。两者其实是不同机制：单次 `WriteJSON` 的阻塞时长取决于** TCP 写缓冲填满后的重传超时**，半开连接上通常是数分钟级（与日志 5 分 18 秒吻合）；2 小时是空闲 keepalive 探测的首次间隔。建议在报告里把这两层分开表述，避免实施者误以为加 TCP keepalive 就能解决（真正解决的是应用层 ping + 读写 deadline）。

---

## 三、修复方案评审

| 项 | 评价 | 说明 |
|----|------|------|
| **P0-A** 所有写加 `SetWriteDeadline`(5–10s) | ✅ 正确且必要 | gorilla/websocket 写不自带 deadline，方向对；10s 合理。**实施时务必覆盖 `SendEnvelope` + relay_bridge_client.go:325/431 + pairing NotifyComplete/Result**，不要漏握手路径 |
| **P0-B** relay-bridge-client 加 30s ping + 60s 读 deadline | ✅ 根治本次 5 分钟假死 | 参照 [server.go:281-312](../go-bridge/server.go#L281-L312) 的直接路径实现是对的；pong handler 重置读 deadline、ping 写失败即返回触发现有 backoff，思路正确 |
| **P1** `/pairing` 加读 deadline + ping | ✅ 合理 | 防配对中途进后台卡死 |
| **P1** `relayEvents` 加 `<-connClosed` | ⚠️ 优化项，非必须 | conn 断开后 `broadcaster.UnsubscribeAll` 已摘订阅；`relayRunning` 有 60s idle 兜底（[handlers.go:1803](../go-bridge/handlers.go#L1803)）。加 `connClosed` 是更快释放，但不会"永久泄漏"。报告把它列为次要是对的，但描述"新连接也无法 relay"偏重——实际有超时兜底 |
| **P2** mailbox 压缩 / `ObservationManager.RemoveDevice` | ✅ 长期优化 | 与本故障无直接因果，独立处理 |
| 修复文件清单含 `relay_hub.go` | ❌ 见上文定位错误 | 对外部 relay 故障无效，应删除或改指 relay-server 模块 |

**关于"P0 两条做完大概率根治"**：判断成立。P0-A 让坏连接在写侧被快速识别，P0-B 在 relay 空闲侧主动探测半开，两者覆盖了日志里两种失败模式（持锁写卡死、空闲假活）。加完后 MacBridge 侧已有的"卡住自动重启"保险丝可保留但不应再频繁触发——可把"自动重启触发频率"作为验证指标。

---

## 四、证据强度建议

报告基于单次日志采样（00:59–01:05）。建议在实施 P0 后补充：

1. **复现验证**：iOS 连 relay 后主动进后台 5+ 分钟，确认修复后 `relay-bridge-client` 在 ~30s 内重连（而非 5 分钟）。
2. **指标对比**：采集修复前后 `socket_send_ms` 分布，确认 P0-A 后 99 线回落到个位数 ms（修复前 max 1319ms）。
3. **回归测试**：为"写 deadline 超时 → 连接关闭 → broadcaster 摘除"路径加单元测试，避免后续回退。
4. ** relay-server 侧**：若 P0 后仍偶发，再到 `relay-server/internal/relay/server.go` 排查服务器端是否同样持锁无 deadline（独立模块、独立部署，本报告未覆盖）。

---

## 五、给实施者的行动清单

1. ✅ 采纳 P0-A：在 `SendJSON`、`relay_bridge_client.go` 的三处写（`SendEnvelope` / 325 / 431）、`pairing_handler.go` NotifyComplete/Result 加 `SetWriteDeadline`。写超时走 `CloseWithControl` 完整关闭（而非只置 `closed` 标志）。
2. ✅ 采纳 P0-B：`relay_bridge_client.go` `Connect` 后起 ping ticker + `SetReadDeadline(60s)` + `SetPongHandler` 重置读 deadline，参照 [server.go:281-312](../go-bridge/server.go#L281-L312)。
3. ❌ **不要**在 `go-bridge/relay_hub.go` 上花时间修本次故障——那是本地联调 hub，不在外部 relay 路径。
4. ⚠️ 报告 P1 的 `relayEvents` 加 `connClosed` 可做，但优先级低于 P0，且需说明现有 60s idle 已兜底。
5. 📝 修订原报告：纠正 relay_hub.go 归属，补全 relay_bridge_client.go 遗漏的两处写、`closed` 标志不关 ws 的细节、以及写阻塞机制（TCP 重传 vs keepalive）的表述。

---

## 六、第二轮评审（修订复核）

> 复核对象：报告 v2（已据第一轮评审修订）
> 结论：**修订全部准确落地，可靠性显著提升，可进入实施。** 仅余 3 处文档一致性瑕疵和 1 个值得显式化的根治前提，均为收尾级别。

### 6.1 修订逐条复核 ✅

| 修订项 | 落地位置 | 复核 |
|--------|----------|------|
| 撤销 relay_hub.go 根因定位 | 根因 C 勘误（[L111-121](2026-06-17-bridge-connecting-hang-root-cause.md#L111-L121)） | ✅ 定位纠正准确。`main.go:307` 启用条件、"only loopback test listeners are allowed" 错误信息核实一致；外部 relay → 独立模块 `relay-server` 的归属正确 |
| 修复表删除 relay_hub.go | [L156-165](2026-06-17-bridge-connecting-hang-root-cause.md#L156-L165) 勘误 | ✅ P0 文件清单已删 relay_hub.go |
| 补 L325/L431 两处写 | [L163-165](2026-06-17-bridge-connecting-hang-root-cause.md#L163-L165)、[L186-188](2026-06-17-bridge-connecting-hang-root-cause.md#L186-L188) | ✅ 两处均为持 `writeMu` 写、无 deadline，与 L198 并列正确 |
| closed=true 不调 ws.Close() | [L190-193](2026-06-17-bridge-connecting-hang-root-cause.md#L190-L193) 配套修订 | ✅ 准确，且给出"应走 CloseWithControl"的明确指引 |
| RTO vs keepalive 分清 | 根因 A 正文 [L73-78](2026-06-17-bridge-connecting-hang-root-cause.md#L73-L78) | ✅ 两套机制分述清楚，因果归因正确 |
| 次要问题 mailbox 条目标注归属 | [L131-135](2026-06-17-bridge-connecting-hang-root-cause.md#L131-L135) | ✅ 已注明"联调场景，与生产无关" |

修订者"先亲自核实再动笔"的说法与代码实测一致——行号、机制描述均经得起回查。

### 6.2 修订引入/遗留的文档瑕疵（建议定稿前收尾）

1. **一句话结论措辞未同步**：[L16](2026-06-17-bridge-connecting-hang-root-cause.md#L16) 仍写"写操作阻塞数分钟"，而根因 A 正文已精确为"数十秒至数分钟（RTO）"。建议结论段同步措辞，保持全文一致。
2. **mailbox 在"次要问题"列了但修复表无对应行**：[L131-135](2026-06-17-bridge-connecting-hang-root-cause.md#L131-L135) 仍列 mailbox 退化，修复表（[L148-154](2026-06-17-bridge-connecting-hang-root-cause.md#L148-L154)）已删 P2 mailbox 行。建议次要问题里那条直接标注"联调场景，不单列修复"，消除"有症状无修复项"的错位。
3. **勘误块密度偏高**：全文现 4 处勘误/配套修订块，穿插正文打断阅读。内部技术报告不必永久保留修订痕迹，建议定稿时把勘误吸收进正文，或集中到文末"修订历史"小节。

### 6.3 修订后浮现的技术边界（增量）

**根治前提需显式化**：根因 B [L98-101](2026-06-17-bridge-connecting-hang-root-cause.md#L98-L101) 自己指出"5 分钟断连是 relay 服务器侧先超时断的"。这意味着 P0-B 之所以能让 Mac 在 30s 重连、绕开 5 分钟等待，**依赖一个本次未核实的前提：relay 服务器侧（`relay-server/internal/relay/server.go`）的读超时/转发写不会反过来被半开连接卡死**。

- 日志里 relay 侧 5 分 18 秒能断、回 EOF，说明它有读超时，P0-B 确实成立。
- 但 relay-server 模块是否也有 deadline-less 转发写、是否会在 Mac 突然不发数据时同样迟钝，**本次排查未深入**（报告 [L217](2026-06-17-bridge-connecting-hang-root-cause.md#L217) 已诚实标注）。

所以 [L167](2026-06-17-bridge-connecting-hang-root-cause.md#L167)/[L223](2026-06-17-bridge-connecting-hang-root-cause.md#L223) "大概率根治"的措辞**保留"大概率"是对的**。建议在备注补一句边界："前提是 relay 服务器侧转发无独立阻塞；若 P0 上线后仍偶发长卡顿，下一步必查 `relay-server/internal/relay/server.go` 的转发写与读 deadline。"——把假设写在明处，避免团队把"大概率"当"确定"。

**relayEvents P1 表述仍偏重**：[L125-128](2026-06-17-bridge-connecting-hang-root-cause.md#L125-L128) "relayRunning 不释放，新连接也无法 relay"未提兜底。实际上 [handlers.go:1803](../go-bridge/handlers.go#L1803) 的 `relayActiveTimeout=60s` idle 超时会清掉。建议补"(有 60s idle 兜底，非永久泄漏)"，免得实施者误判为高优泄漏而抢排在 P0 前。

### 6.4 第二轮结论

- 修订质量：高，错误定位纠正到位，无新引入的技术性错误。
- 可否实施：**可以**。6.2 的 3 处是文档打磨，6.3 是边界说明，都不阻塞 P0 动工。
- 实施顺序仍按第一轮 5.1/5.2：先 P0-A（写 deadline，含 L198/L325/L431 三处），再 P0-B（relay-bridge-client ping）。relay_hub.go 不碰。
