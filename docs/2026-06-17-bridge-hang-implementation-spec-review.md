# 评审：Bridge "卡连接中" 根治实施规格

> 评审日期：2026-06-17
> 被评审文档：[docs/2026-06-17-bridge-hang-implementation-spec.md](2026-06-17-bridge-hang-implementation-spec.md)
> 评审方法：通读方案 + 核实全部"待核实"点 + 核对源码行号与锁状态

## 总评

**方案质量高，可直接进入实施。** 它把根因结论转成了精确到行号的指令，补全了 gorilla 并发约束、deadline 取值依据、验证链，且做了一件值得肯定的事——**用实测纠正了我第二轮评审的一个错误判断**。

> ⚠️ **我第二轮 6.3 判断有误，此处更正**：我曾基于日志"relay 侧 5 分 18 秒断回 EOF"推断"relay-server 有读超时，所以 P0-B 成立"。本轮核实 `relay-server/internal/relay/server.go` 全文（745 行），`SetReadDeadline`/`SetWriteDeadline`/`PingMessage`/`SetPongHandler` **全部无匹配**。relay-server 应用层没有任何保活，那 5 分 18 秒的断开是对端 TCP RTO 或 Cloudflare 空闲超时（代码 `clientIP` 读 `CF-Connecting-IP`，说明部署在 CF 后），不是 relay 主动发现。方案把 relay-server 纳入本次范围**是正确的**，我第二轮"relay 侧有兜底"的乐观判断收回。

## 一、给方案"待核实"点的直接答案

方案多处标"先核实/不要假设"，这是好习惯。以下是我核实后的确切答案，实施者可直接用，不必再查：

### 1. pairing_handler.go 锁状态（方案 P0-A 表 L107-108 标"核实"）

| 位置 | 锁状态 | 结论 |
|------|--------|------|
| [pairing_handler.go:86](../go-bridge/pairing_handler.go#L86) `NotifyComplete` 内 | 调用方持 `conn.writeMu`（[L82-83](../go-bridge/pairing_handler.go#L82-L83)） | ✅ 持 `writeMu` |
| [pairing_handler.go:266](../go-bridge/pairing_handler.go#L266) `sendPairingResult` 内 | **看调用点**：[L216](../go-bridge/pairing_handler.go#L216) 调用持 `writeMu`；[L152/158/176/181/186/193/211](../go-bridge/pairing_handler.go#L152) 调用**不持锁** | 见下 |

**关键结论**：`SetWriteDeadline` 本身在 gorilla 内部是并发安全的（独立 mutex 保护），所以**无论调用点持不持锁，都能安全调用**。在 sendPairingResult 内 `conn.SetWriteDeadline(...)` 紧贴 `conn.WriteMessage(...)` 即可，不必额外加锁。现状下那几个无锁调用点都在读 goroutine（[L224](../go-bridge/pairing_handler.go#L224)）启动之前，无写并发；加 deadline 不改变此结论。

> 补充提醒（既有瑕疵，非本次引入）：sendPairingResult 在不同调用点锁状态不一致，是既有事实。本次只加 deadline，**不要顺手重构锁结构**——那会扩大回归面。

### 2. server.go 字段名（方案 P0-A 配套段标"核实字段名"）

[server.go:23](../go-bridge/server.go#L23) 字段定义 `conn *websocket.Conn`，访问即 `c.conn`。方案的犹豫是多余的——**就是 `c.conn`**，直接用。

### 3. relay-server 读循环函数名（方案标"readPeerMessages 或同名，先 grep"）

实际函数名已定位，不必 grep：

| 方案描述 | 实际位置 | 内容 |
|----------|----------|------|
| bridge 读循环 | [relay-server/internal/relay/server.go:486](../relay-server/internal/relay/server.go#L486) `readBridgeFrames` | 持续 `peer.conn.ReadMessage()`，无 deadline |
| device 读循环 | [server.go:529](../relay-server/internal/relay/server.go#L529) `readDeviceFrames` | 同上 |
| 转发写 | [server.go:568-572](../relay-server/internal/relay/server.go#L568-L572) `socketPeer.write` | 持 `p.writeMu`、`WriteMessage` **无 deadline** |
| peer 结构 | [server.go:53-56](../relay-server/internal/relay/server.go#L53-L56) `socketPeer` | 只有 `conn` + `writeMu`，**无 `lastPong`/ping 字段** |

`socketPeer.write` 持 `writeMu` 无 deadline，与 go-bridge 根因 A 是**同一个坑、对称存在**。方案对它的处理方向正确。

## 二、几个值得改进的点

### 1. ⚠️ SendJSON 失败关闭路径的死锁陷阱（方案 P0-A 配套段）

方案的改法在 SendJSON 失败分支手写 `c.conn.Close()` + 解锁 cleanup。这是可行的，但**埋了一个陷阱没说**：

**不能在持 `c.mu` 时直接调用 `c.CloseWithControl(...)`**——[server.go:148](../go-bridge/server.go#L148) `CloseWithControl` 自己要 `c.mu.Lock()`，而 SendJSON 已持有 `c.mu`，会**死锁**。

方案没有调 CloseWithControl、而是手写 conn.Close + 手动 unlock/cleanup，恰好绕开了这个死锁，是对的。但建议在方案里**显式写出这个死锁约束**，否则实施者"重构得更优雅"时很可能踩进去（把 conn.Close 换成 CloseWithControl）。

更稳妥的等价写法（避免与 CloseWithControl 逻辑分叉）：
```go
if c.consecutiveWriteErrors >= 5 {
    c.closed = true
    ws := c.conn          // 持锁时取出引用
    cleanup := c.onCleanup
    c.onCleanup = nil
    c.mu.Unlock()         // 先放手，再走完整关闭
    _ = ws.Close()
    if cleanup != nil { cleanup() }
    c.mu.Lock()           // defer 的 Unlock 配对
    return
}
```
（与现有 CloseWithControl 的"先取 ws/cleanup 再 unlock"结构一致，只是少了 close frame——半开连接本来就发不出去 close frame。）

### 2. 坑 1 与坑 3 的 WriteControl 写锁表述自相矛盾

- 坑 1（L36-39）说："`WriteControl` 是 gorilla 唯一允许与 WriteMessage 并发的调用……**不要**套写锁"。
- 坑 3 的 pingLoop 示例（L87-89）却又：`c.writeMu.Lock(); WriteControl(PingMessage...); c.writeMu.Unlock()`，注释说"保证不和同连接其他控制帧并发"。

gorilla 的 `WriteControl` 内部用独立锁，多个 WriteControl 并发、以及与 WriteMessage 并发**都是安全的**。所以坑 1 的"不要套写锁"是对的，坑 3 的示例是过度保守。两者冲突会让实施者困惑。

**建议**：坑 3 的 pingLoop 改为不持 writeMu（遵循坑 1）。即便 `Close()`（[relay_bridge_client.go:161](../go-bridge/relay_bridge_client.go#L161)）也用 writeMu 发 CloseMessage，ping 的 WriteControl 与它并发也是 gorilla 允许的。不套锁的好处是 ping 不被数据写阻塞——否则写卡住时 ping 也发不出，正好违背加 ping 的初衷。

（这是优化项，非正确性问题；坑 3 现写法能跑。但既然坑 1 已经讲对，坑 3 应与之统一。）

### 3. relay-server 范围：建议与 go-bridge 解耦交付

方案把 go-bridge P0-A/P0-B 和 relay-server 补齐并列写进同一个 Definition of Done（L228-235）。但两者部署链完全不同：

- **go-bridge**：随 app 构建分发，用户更新 app 即生效。
- **relay-server**：独立部署在 `relay.byteseek.uk`（VPS + Cloudflare），改完代码需**单独运维部署**，app 更新覆盖不到（方案 L243-246 自己说了）。

关键点：**go-bridge 的 P0-B 即使没有 relay-server 配合，Mac 侧也能主动 ping 探测半开**——gorilla 的默认 PingHandler 会让 relay 自动回 pong，Mac 的 pong handler 重置读 deadline。所以"卡连接中"这个用户症状**主要由 Mac 侧 P0-B 解决，不阻塞于 relay-server**。relay-server 补 deadline 是防止"relay 侧转发写给半开 device 时卡住 writeMu"的健壮性增强。

**建议**：方案明确分两个交付包——
- **PR-1（解症状，必交付）**：go-bridge P0-A + P0-B，随 app 上线。
- **PR-2（健壮性，紧随）**：relay-server 写/读 deadline + ping，独立部署，不阻塞 PR-1。

DoD 也拆成两组，避免实施者把两个部署链绑死、互相阻塞。

### 4. relay-server 的修复优先级：写 deadline > 读 deadline > ping

方案 L180-185 把三者并列。结合 gorilla 行为，优先级应是：

1. **写 deadline（`socketPeer.write`，最高）**：对称于 go-bridge 根因 A，防转发写卡死 writeMu，这是 relay 侧最可能放大故障的点。
2. **读 deadline（`readBridgeFrames`/`readDeviceFrames`）**：让 relay 主动判死半开，不再纯靠对端 RST/CF 超时。
3. **ping ticker（最低，甚至可不做）**：因为 Mac 侧 P0-B 会主动 ping，relay 的 gorilla 默认 PingHandler 自动回 pong。relay 自己再加 ping 是双保险，收益递减。若要加，注意 relay-server 的 socketPeer 需补 `lastPong` 字段 + pong handler 重置读 deadline，改动比前两项大。

建议方案把"写+读 deadline"列为 relay-server 必做，ping 标可选。

### 5. 测试要用可注入的短超时，否则 go test 要等几十秒

方案 L209-216 要求新增测试断言"~10s 内写报错""90s 内重连"。用真实常量（10s/30s/90s）跑测试，单条用例就要等数十秒，不可行。

**建议**：方案补一条——把 `relayPingPeriod`/`relayReadTimeout`/`relayWriteTimeout`（以及 server.go 的 `bridgeReadTimeout`）做成**可注入的变量或包级变量**，测试时覆盖为短值（如 ping 100ms / 读超时 300ms / 写 deadline 200ms），断言在 1s 内触发。现有 `TestPingTickerClosesOnTimeout`（[events_test.go:917](../go-bridge/events_test.go#L917)）若也用真实 90s，本身就是慢测试——实施时可一并审视。

## 三、确认准确的点（无需改动）

- **坑 1（gorilla 写并发约束）**：方向正确，"一个写者 + 锁串行"是 gorilla 的硬约束。
- **坑 2（deadline 值对齐直接路径）**：明智。统一 30s ping / 90s 读 / 10s 写，对齐 `bridgeReadTimeout`，比照抄根因报告示意值更严谨。写 deadline 10s 的依据（远大于正常 <5ms、远小于 RTO）成立。
- **坑 3（ping 随 c.done 退出）**：关键判断正确——RelayBridgeClient 是重连循环，ping 必须挂在 Connect 内随 c.done 生命周期退出，否则泄漏或写已关 conn。
- **构建链（L239-248）**：`project.yml:47` 的 preBuildScript 确实是 `go build ./go-bridge/cmd/cccode-bridge-runtime`，改 go-bridge 后 Xcode build 自动编译，描述准确。
- **测试位置**：`TestPingTickerClosesOnTimeout` 在 [events_test.go:917](../go-bridge/events_test.go#L917)，针对直接路径（server.go ping ticker），不覆盖 relay 路径——方案"必须新增 relay 路径测试"的判断成立。
- **不要做的事（L261-267）**：不碰 relay_hub.go、不改 backoff、不顺手做 P1/P2——都对，控制回归面。

## 四、结论与行动清单

**方案在修订上述 5 点后可实施。** 其中：

- **阻塞实施（必须先改方案）**：第 1 点（死锁陷阱说明）、第 3 点（交付解耦）——影响正确性和交付节奏。
- **建议改进（实施时可顺带处理）**：第 2 点（WriteControl 统一）、第 4 点（relay-server 优先级）、第 5 点（测试超时注入）。

实施顺序：
1. **go-bridge P0-A**：6 处数据写加 10s deadline（含 L198/L325/L431 + pairing L86/L266），SendJSON 失败走完整关闭（注意死锁约束）。
2. **go-bridge P0-B**：RelayBridgeClient 加 30s ping + 90s 读 deadline + pong handler，pingLoop 随 c.done 退出。新增 relay 路径 ping 测试（用短超时）。
3. **随 app 上线 PR-1**，观察"卡连接中"是否消失、自动重启触发频率是否下降。
4. **relay-server PR-2（独立部署）**：先写 deadline（socketPeer.write），再读 deadline，ping 可选。

> 一句话：这份方案比根因报告更接近"实施 agent 能直接照做"的形态，最大的价值是用实测把 relay-server 拉进了修复范围——这个范围扩张是对的，但要和 go-bridge 解耦交付，别让 relay 部署节奏拖住 app 侧的根治。

---

## 五、终审：修订后最后一轮复核

> 复核对象：实施规格 v2（已据第三轮 2 阻塞项 + 3 改进项修订）
> 结论：**5 项修订全部正确落地，方案接近可交付。终审发现 1 处修订内部矛盾必须先修（const vs var），另有几处遗留瑕疵建议一并清理。**

### 5.1 修订逐条复核 ✅

| 修订项 | 落地位置 | 复核 |
|--------|----------|------|
| 阻塞项 1：死锁陷阱 | 警示块 [L119-128](2026-06-17-bridge-hang-implementation-spec.md#L119-L128)、代码注释 [L135-137](2026-06-17-bridge-hang-implementation-spec.md#L135-L137)、DoD [L262](2026-06-17-bridge-hang-implementation-spec.md#L262) | ✅ 三处写明。核实 [server.go:148](../go-bridge/server.go#L148) `CloseWithControl` 首行即 `c.mu.Lock()`、[server.go:143-145](../go-bridge/server.go#L143-L145) `Close()` 确实转调 `CloseWithControl`——"`Close()` 也死锁"的论断成立 |
| 阻塞项 2：交付解耦 | [L251-276](2026-06-17-bridge-hang-implementation-spec.md#L251-L276) 拆 PR-1/PR-2 + 论据 | ✅ DoD 拆分清晰，"Mac 侧 P0-B 靠 gorilla 默认 pong 工作、不阻塞 relay-server"论据正确 |
| 改进项 1：坑1/坑3 矛盾 | pingLoop [L85-88](2026-06-17-bridge-hang-implementation-spec.md#L85-L88) 去掉 writeMu + 注释 | ✅ 与坑1统一为"WriteControl 不套写锁"。注释解释了"套锁会让 ping 被卡住的数据写阻塞"，判断正确 |
| 改进项 2：relay-server 优先级 | [L201-208](2026-06-17-bridge-hang-implementation-spec.md#L201-L208) 写>读>ping | ✅ 落点用核实函数名（socketPeer.write:568 / readBridgeFrames:486 / readDeviceFrames:529），ping 标可缓做 |
| 改进项 3：测试可注入短超时 | [L238-241](2026-06-17-bridge-hang-implementation-spec.md#L238-L241) + DoD 标注 | ✅ 要求"包级变量或字段、测试覆盖 50ms"——**但与常量定义冲突，见 5.2** |

### 5.2 🔴 终审必改：const vs var 内部矛盾（会让改进项 3 落空）

这是本轮唯一**必须先修**的问题，它是修订自己引入的内部不一致：

- 常量定义（[L170-174](2026-06-17-bridge-hang-implementation-spec.md#L170-L174)）写的是 `const ( relayPingPeriod = ...; relayReadTimeout = ...; relayWriteTimeout = ... )`，落点表 [L48](2026-06-17-bridge-hang-implementation-spec.md#L48) 也写"新增**常量**"。
- 但改进项 3（[L238-241](2026-06-17-bridge-hang-implementation-spec.md#L238-L241)）要求"做成包级变量或字段、**可被测试覆盖**（测试里改成 50ms）"。

**Go 的 `const` 是编译期常量，测试里无法重新赋值。** 实施者若照 L170-174 写成 `const`，改进项 3 的"测试覆盖成 50ms"就做不到，测试又退回到等真实 10s/90s——改进项 3 白做。

**改法**：把 L170-174 和 L48 的"`const`/常量"统一改为**包级 `var`**：
```go
var (
    relayPingPeriod   = 30 * time.Second
    relayReadTimeout  = 90 * time.Second
    relayWriteTimeout = 10 * time.Second
)
```
注意现有 [server.go:18](../go-bridge/server.go#L18) 的 `bridgeReadTimeout` 也是 `const`——PR-1 若要让直接路径的 ping/读超时同样可测，需一并把它从 `const` 改 `var`（或新测试只覆盖 relay 路径的新变量，不动 `bridgeReadTimeout`，二选一，方案里写明）。

> 这个矛盾不影响 PR-2（relay-server）的代码正确性，但会让两边 deadline 测试都卡在长等待，必须修。

### 5.3 遗留瑕疵（建议清理，非阻塞）

1. **[L146-148](2026-06-17-bridge-hang-implementation-spec.md#L146-L148) 旧犹豫未删**：配套代码后的括号还留着"但要确认 `c.conn` 字段名——核实 Conn 结构体里底层 ws 的字段名，可能是 `c.conn` 或别名"。但字段名第三轮已核实、落点表 [L102](2026-06-17-bridge-hang-implementation-spec.md#L102) 已明确写 `c.conn`。这段旧文字与已核实状态矛盾，应删除。
2. **[L136](2026-06-17-bridge-hang-implementation-spec.md#L136) "不能用 CloseWithControl/Close" 的"Close"裸写易混**：读者可能误读成"任何 Close 都不能用"（包括下一行正确的 `c.conn.Close()`）。建议改成"不能用 `CloseWithControl` 或 `c.Close()`"，明确指 `Conn` 的方法、与 `c.conn.Close()`（gorilla 方法）区分。
3. **[L262](2026-06-17-bridge-hang-implementation-spec.md#L262) DoD 只提了 CloseWithControl**：警示块已说清 `c.Close()` 也死锁，DoD 这条补上"，不可用 CloseWithControl 或 c.Close()"更完整。
4. **[L184-189](2026-06-17-bridge-hang-implementation-spec.md#L184-L189) "gorilla 默认初始读 deadline" 表述不准**：gorilla `Upgrader.Upgrade` 出来的 conn 默认 read deadline 是零值（**无** deadline），不是"有默认读 deadline"。那 5 分 18 秒断开应归因于对端 TCP RTO 或 Cloudflare 空闲超时（代码读 `CF-Connecting-IP`）。不影响落点（落点已明确要加 `SetReadDeadline`），但背景说明应改准。

### 5.4 终审结论

- **5 项修订**：全部正确落地，技术判断经得起回查（尤其死锁论断、Close 转发链、gorilla WriteControl 并发性）。
- **必改 1 处**：5.2 的 const→var，否则改进项 3 的测试可注入性失效。
- **建议清理 4 处**：5.3，均为文字/一致性，不动代码逻辑。

**改掉 5.2 这一条，方案即可交付实施 agent。** 5.3 可在实施者动笔时顺手修正，不阻塞。

实施顺序维持第三轮结论：go-bridge P0-A（6 处写 deadline + 死锁安全的关闭）→ P0-B（RelayBridgeClient ping，pingLoop 不套 writeMu、随 c.done 退出）→ PR-1 随 app 上线验证症状消失 → relay-server PR-2 独立部署（写 deadline 优先）。