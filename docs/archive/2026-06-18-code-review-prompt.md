# Code Review Prompt（供 GPT / 其他 LLM 深度 review cccode-macbridge）

> 用法：把下面「Prompt 正文」整段复制给 GPT-5.5（或其他模型），并附上本仓库代码访问权限。
>
> 本 prompt 在 `docs/code-review-2026-06-18.md`（v4）基础上**扩展 review 维度**。
> v4 已充分覆盖「安全 / 权限边界 / 威胁模型 / read_file 绕过 / relay 限流 / 配对暴力破解 /
> TLS 降级 / 受信设备存储原子性」，并经四轮评审验证零幻觉。**本轮重点不是重做安全审查**，
> 而是补齐 v4 未深入的运行期时序、并发、进程治理、跨进程契约和资源边界等维度，
> 同时对 v4 已覆盖的安全项做**增量复核**（只报新增或 v4 漏判的问题，不重复）。

---

## Prompt 正文

你是一位资深系统/平台架构师,正在对 **CCCode MacBridge** 代码库做一次深度 code review。

### 1. 项目背景(你必须先建立的认知)

CCCode MacBridge 是 **macOS 伴侣程序**,把本机已安装的 AI coding agent 后端(Claude Code CLI、OpenCode server、Codex app-server)通过**直连 LAN WebSocket**或**端到端加密的公共 Relay** 暴露给 iPhone/iPad 客户端。本仓库是 **Mac 侧**;iOS 客户端在另一仓库。

务必先读 `CLAUDE.md` / `AGENTS.md` / `CONTRIBUTING.md` / `SECURITY.md`,它们定义了项目的**铁律**:
- 不得在 production 运行期代码里加 fallback/mock 路径掩盖真实失败。
- 永不提交凭据/route id/provisioning token/password/私钥/Apple Team ID。
- `relay-server` 是**独立 Go module + 独立部署链**,提交代码不会更新线上 relay。
- runtime 逻辑属 `core/config/agent/`,wire 协议适配属 `go-bridge/`。
- review 必须遵守这些既定约束,不要把项目的**有意设计**(如 relay 独立模块、capability opt-in interface、120min 定时重启)当成缺陷来批。

**组件地图**(必读对应入口):
- `MacBridge/` — SwiftUI macOS app,拥有 go-bridge 进程生命周期、UI、设置、配对。`RuntimeManager.swift`(1100+ 行 `@MainActor`)是 Swift↔Go handoff 核心。
- `go-bridge/` — Go WebSocket runtime,真正的 bridge。入口 `go-bridge/cmd/.../main.go` → `gobridge.Main()`。三大网络面:`:8777` Bridge WS、`127.0.0.1:<random>` Management API、`cccode-relay` v1 E2EE relay。
- `core/` `config/` — agent 抽象 + 配置。`config/config.go` 3200+ 行。
- `agent/{claudecode,codex,opencode}` — agent 后端,各自 `init()` 注册到 `core.RegisterAgent`。
- `transcriptindex/` — 边界安全的 transcript 分页索引。
- `relay-server/` — **独立 Go module**(`module cccode-relay`),公共加密 relay。

### 2. 与既有 review 的关系(关键约束)

`docs/code-review-2026-06-18.md`(v4)已用四轮迭代深度覆盖安全/权限边界,并验证零幻觉。**你必须先读 v4 全文**。本轮的产出要求:

1. **不重复 v4 已覆盖的发现**。对 v4 已覆盖的项(read_file 绕过、WS 帧上限、relay 限流、受信设备原子写、TLS 降级、配对暴力破解、capability policy 缺失等),只在发现**新增子问题、新的攻击路径或 v4 定级明显偏差**时才报。
2. **重点放在 v4 未深入的维度**(见下方 §3)。这些是「不太全面」的具体所指。
3. 每条发现要说明它「是否已被 v4 覆盖」——已覆盖的给增量,未覆盖的标 [v4 未覆盖]。

### 3. Review 维度(本轮重点,按优先级)

v4 强在安全与权限边界,本轮补的是**运行期时序、进程治理、并发、跨进程契约、资源边界**这条线。按「本代码库特有高危 × 出事影响大」排序。

#### 🔴 P0-A 安全增量复核(只报新增/漏判)
对 v4 的安全结论做增量复核,特别查 v4 可能没追到的地方:
- **agent 进程 spawn 的环境/凭据泄漏**:`SECURITY.md` 列了「Agent process spawning and environment redaction」为敏感区——v4 是否验证了 Claude Code CLI / Codex / OpenCode 的子进程环境变量、argv、临时文件里是否泄漏 OpenCode credential / API key / relay token?子进程 `cmd.Env` 是否做了脱敏?agent stderr/stdout 是否原样进日志?
- **Relay 非速率限制面的滥用**:v4 查了 limiter,但 relay 的 mailbox 存储、envelope 路由、prekey 管理、`route_*`/`br_route_*` 全局 gitleaks 放行是否还有其他可被公网攻击者(A 类)利用的资源/逻辑漏洞?
- **Management API 越权面**:`127.0.0.1` loopback 但 token 一旦泄漏 = 本机管理员能力。token 的生成强度、存储位置、日志可见性、`/internal/*` 是否有需要二次确认的高危操作(如 shutdown/revoke-all)?
- **配对 QR/手动码的生命周期**:v4 查了暴力破解,但 QR 的有效期、单次性、claim 后的失效、并发 claim 竞态是否完整?

#### 🔴 P0-B 并发与 goroutine 生命周期(v4 未覆盖)
go-bridge 有大量共享状态(`sync.Mutex`/`RWMutex` 117 处,`go func` 10 处)。这是 Go 服务头号线上故障源:
- **锁顺序与死锁**:多把 mutex(device store / pairing store / 连接 registry / session registry)是否有交叉持锁?写锁里是否调用了可能再次加锁的方法?
- **goroutine 泄漏**:`go func` 启动的工作 goroutine(agent 事件转发、relay 投递、心跳)在连接断开/session 切换/进程退出时是否被正确取消?有没有「只启动不等待、用 channel 但无人关闭」的模式?
- **context 取消传播**:Go runtime 各层是否一致用 `context.Context` 串联取消信号?有没有用 `context.Background()` 切断取消链导致父取消传不下来的地方?
- **`sync.Map` vs 普通 map+锁** 的选用是否合理;map 并发读写风险。
- **WebSocket 并发写**:gorilla/websocket 的「一个连接同时只允许一个 writer」约束是否在所有写路径(事件推送、RPC 响应、心跳、relay 投递)都被遵守?v4 提到「已封装」——核实是否**所有**写路径都走了封装。

#### 🔴 P0-C agent 进程治理(v4 未覆盖)
agent 子进程(Claude Code CLI / Codex app-server / OpenCode)的生命周期是 MacBridge 的核心职责,也是最容易出僵尸进程/资源泄漏的地方:
- **进程生命周期**:agent 进程在 session 结束、client 断开、Mac 睡眠/唤醒、bridge 重启时是否被正确回收?有没有僵尸进程累积(defunct)的风险?
- **Stdin/Stdout/Stderr 管道**:管道是否在 agent 退出后被关闭并 drain?未 drain 的管道会阻塞 agent 进程退出或导致 broken pipe。
- **流式输出背压**:agent 的流式 token(LLM 输出)生产速度 vs WS/relay 投递消费速度——中间缓冲是否有上限?慢客户端会不会让缓冲无限增长导致 OOM?
- **信号处理**:SIGTERM/SIGINT 时 agent 子进程是否优雅退出?KILL 前 grace period?
- **runas / 权限**:`core/runas*.go` 的提权逻辑是否安全?

#### 🔴 P0-D Swift↔Go 跨进程契约(v4 未覆盖)
MacBridge Swift app 与 go-bridge 是**两个进程**,靠 `runtime.json` + ready frame + Management API 通信,这个契约面 v4 没碰:
- **ready/runtime.json 握手**:`RuntimeManager` 轮询 `runtime.json` + `management-token` 文件——竞态(文件半写、端口被旧进程占用、token 文件权限)如何处理?v4 虽提 stale-port-takeover 但没深入。
- **进程崩溃恢复**:go-bridge 崩溃后 Swift 侧的自动重启策略——重启风暴保护、状态恢复一致性、旧 session/连接的清理。
- **配置热更新**:remote URL/OpenCode creds/relay route 变更通过 `restart()` 应用——restart 期间正在进行的 session/RPC 如何被通知和清理?有没有「restart 后旧连接幽灵」?
- **睡眠/唤醒**:`RuntimeManager` 的 sleep/wake 处理——唤醒后端口是否还有效、agent 进程是否被系统挂起、relay 重连。
- **Management API 客户端**:`ManagementAPIClient.swift` 的重试/超时/错误处理。

#### 🟠 P1-E relay-server 深度(v4 未充分覆盖)
v4 主要查了 limiter。relay 是公网暴露面,需更全面:
- **store/mailbox 一致性**:SQLite WAL、外键、容量、cursor 的边界场景(并发写、容量满、cursor 失效)。
- **envelope 路由**:relay 对 envelope 是「不透明路由」——但路由逻辑本身有没有 DoS 面(路由表膨胀、无效 route 的资源占用)?
- **deadline/超时**:`deadline_test.go` 提到有 deadline——是否所有路径都覆盖?慢客户端连接是否会占满连接池?
- **prekey 池管理**:relay prekey 的预生成、补充、耗尽处理。

#### 🟠 P1-F 资源边界与背压(v4 未充分覆盖)
v4 触及了 WS 帧 OOM,但资源面更广:
- **事件/消息队列上限**:Bridge 事件推送到 iOS 客户端时,慢客户端或断连期间事件是否无限缓冲?relay 投递同理。
- **transcript 分页**:大 session(几万条消息)的分页索引在内存/磁盘的占用与加载性能。
- **日志滚动**:CLAUDE.md 说 8MiB × 3 代——高流量下是否会占满磁盘或丢关键日志?
- **历史加载性能**:`transcriptindex/` 的索引构建/查询在超大 session 下的表现。

#### 🟠 P1-G 可维护性与 god-object(v4 提及但未给可执行结构)
v4 点了 `handlers.go`(3200 行)、`config.go`(3200 行)、`RuntimeManager.swift`(1100 行)是债务,但没给可执行的拆分边界。本轮要求:
- 对这三个大文件,给出**具体的拆分建议**(按职责切到哪些新类型/文件),并指出拆分的**首要风险点**(哪些共享可变状态最阻碍测试隔离)。
- 包级全局变量(device store / pairing store / 连接 registry)对**多实例测试**和**初始化顺序**的具体影响——有没有「测试间状态泄漏」的 flaky 风险?

#### 🟡 P2-H 协议版本协商与兼容(v4 未覆盖)
- `hello.protocol.version` 的向后/向前兼容:旧 client 连新 bridge、新 client 连旧 bridge 的降级行为是否有测试覆盖?
- capability 协商失败时的 fallback 是否会「静默降级到不安全状态」?
- `docs/protocol/` 是权威——代码实现的版本号/字段是否与文档一致?

#### 🟡 P2-I 测试质量与 flaky(v4 未覆盖)
- 时序类测试(并发、deadline、重连、relay)是否 flaky?有没有 `time.Sleep` 代替同步的隐患?
- `httptest` 起的 server 是否有 goroutine 泄漏?
- 集成测试 vs 单元测试的边界是否清晰?

### 4. 评审输出格式(强制,否则评审无效)

对**每一个**发现,必须给出:

```
[v4 状态] v4 已覆盖(仅增量) / v4 未覆盖 / v4 定级偏差
[严重度]   🔴高 / 🟠中 / 🟡低
[类别]     见上方维度编号(如「P0-C agent 进程治理」)
[位置]     文件路径:行号(或精确符号/函数名)。多处用多个 location。
[问题]     一句话说清「这里假设了什么 / 做错了什么」。
[证据]     引用真实代码片段(≤10 行)或调用链,证明这不是臆测。
[影响]     在「弱网/并发/Mac 睡眠/进程崩溃/慢客户端」等运行期条件下会怎样坏掉;哪个攻击者画像(A-E,见 v4 §2)可达。
[修复建议] 具体到「改哪个方法 / 加什么守卫 / 用哪个 API」,而不是「建议加强」。
```

**禁止输出**:
- 重复 v4 已完整覆盖的发现(除非有明确增量/偏差)。
- 没有行号/符号定位的泛泛建议。
- 没有调用链证据的猜测性结论。
- 风格类吹毛求疵,除非掩盖真实 bug。
- 不要为凑数量堆低价值发现。宁缺毋滥。

### 5. 评审交付结构

1. **与 v4 的关系声明**(2-3 句):确认已读 v4,说明本轮的增量范围。
2. **总体评估**(3-5 句):v4 之外最值得信任的部分、v4 未覆盖的最危险 3 个隐患(应落在运行期时序/进程治理/并发这条线)。
3. **发现清单**:按「强制输出格式」逐条列出,按严重度降序。每条标 `[v4 状态]`。
4. **P0 优先级建议**:如果只能修 5 件事,按顺序是哪 5 件,与 v4 的 P0(P0-1 read_file 等)如何排序。
5. **需要进一步验证的疑点**:无法仅凭静态阅读确认、需运行期/测试验证的假设(诚实标注,不硬装确定)。尤其注明哪些并发/进程问题需 TSan/race detector 或真机长跑验证。

### 6. 行为约束

- **先验证再下结论**:任何「这里有 race / goroutine 泄漏 / 进程不回收」的断言,必须先追调用链或引用代码证明。无法证明的列入「需进一步验证」。
- **区分设计与缺陷**:项目刻意约束(见 `CLAUDE.md` 铁律、120min 定时重启、relay 独立模块)不要批成 bug。
- **诚实**:不确定就说不确定。宁少勿编。
- **聚焦**:这是 macOS 伴侣 + Go bridge + agent 进程治理 + 公网 relay。不要套用无关的 Web 后端或纯客户端 review 套话。
- **对 v4 既不盲从也不为反对而反对**:v4 经过四轮验证,其安全结论可信;本轮是补 v4 的盲区,不是推翻 v4。
