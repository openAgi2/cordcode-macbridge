# dsh-web 单实例 3080 收敛方案 v2 第二轮评审报告

- 评审对象：`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md` v2（主仓，未 commit）
- 对照：v1 评审 `docs/2026-08-19-dsh-web-canonical-3080-instance-design-review.md`
- 评审日期：2026-08-19
- 评审方法：逐项对照第一轮 M1–M6 / S1–S10，不采信 §11 自述；对 v2 新增断言（错误码选型、hello 映射、锁纪律、两树一致、iOS 藏入口）回源码。核验树 = `opencode/web` `9796432`，并对照 main `5f9237b`。
- 纪律：iOS 行为以本轮 Swift 读取为准，不用 Go 注释代替。

## 1. 结论

**修改后通过。** 第一轮六条必改、十条建议全部有正文落点，路线不再动摇。P0 三块（2s 主触发器、宽限 wire、锁纪律）写进了可实施的形状。

还欠 **1 项文档必改**：§3.2 把「hello 不落 `not_configured`」和「`available=false` + 不新增枚举」并列为可选项，但现行 `detectInstanceStatusProber` 会把任何 `available=false` **写成 hello_ack `not_configured`**；同时「iOS 对 `not_configured` 收起入口」被 Go 注释误证，本轮 iOS 源码显示 **并不按 status 过滤**。不改这一段，实施者会在「禁止 not_configured」和「gate 选项 A」之间撞车，或去加并不需要的枚举。

改完这一段即可开写，不必再开一轮方向评审。

## 2. 第一轮条款逐项复核

| 项 | 落点 | 二轮判定 |
|---|---|---|
| M1 主触发=2s 流重连 | §0.2 三层表、§0.3 今晚时间线、双情形论断、§8.1、§10.2 | ✅ 正确。`streams.go:28-29` backoff=2s；`shouldStartPassiveSubscription` 对 dsh-web 走默认 `return true`（本树 `:810`，函数头 `:796`） |
| M2 静默面归因 | §0.4 四张面 + §1 缺陷 2 | ✅ `InstanceStatus` 读活 `Current()`（`dshweb.go:164`）；hello 每次 `BuildAllAgentDescriptors`（`hello_handler.go:164`） |
| M3 宽限 wire | §3.2 | ✅ 主体成立。错误码选 `backend_unavailable` 合理。**hello 映射与 iOS 藏入口见 R2-1** |
| M4 锁 | §3.3 + §8.6 | ✅ 约束完整（锁范围 / single-flight / ≤1s 负缓存 / spawn 锁外） |
| M5 探测≠标签 | §4 竞态 1 | ✅ `processIsAlive` 本树只有定义（`resolver.go:473-475`）无调用；`:391-396` 仍恒打 managed+孩子 PID |
| M6 权威端口=探测首位 | §2 / §9 / §8.7 | ✅ 新语义清楚。现行「跳过探测」措辞略过满，见 R2-S4 |
| S1 进程重启=冷启动 | §3.1 + §8.4 | ✅ `RuntimeManager.swift:489` 默认 120 分钟 |
| S2 判别器只读/突变 | §0.1 / §10.1 | ✅ `diagnostics.go:61` 确走 `Resolve()` |
| S3 列表归因 | §0.5 / §6 | ✅ 与一轮活体（同 20 id、turns 分叉）一致 |
| S4 初衷措辞 | 文首 | ✅ |
| S5 变迁日志 | §3.4 / §8.1 | ✅ |
| S6 PID 安全杀 | §6 / §8.8 | ✅ |
| S7 验收扩行 | §8 十行 | ✅ 覆盖一轮缺口 |
| S8 活文档 | 实施注记 | ✅ 不阻塞提案；`GO_BRIDGE_ARCHITECTURE.md:237-241` 仍写 3096–3196 |
| S9 ps 取启动时间 | §5 | ✅ |
| S10 Stop 现状 | §5 先澄清再推荐 | ✅ |

采纳账「全部采纳、无不采纳」与正文一致。两处带选择：

1. RPC 码选 `backend_unavailable`（协议表 `:169`，「Backend 进程不可达」）而不用 `service_not_running`（那是 `AgentStatus`，不是 RPC 码）——选型成立。
2. 不预设新枚举、把 hello 可见性留作实施 gate——意图对；**选项集合与现行映射打架，见 R2-1**。

## 3. 二轮新发现

### 必改

**R2-1. §3.2 hello / `InstanceStatus` 段内部矛盾，且「iOS 藏入口」为假**

三件叠在一起：

1. **映射层（v2 未写）**。`InstanceStatus()` 返回 `(available bool, detail)`。hello 不直接用这个二元组，而是经 `detectInstanceStatusProber`（本树 `agent_descriptor.go:247-255`；main 上等价的 `detectDSHWebInstance`）：

   ```
   available == true  → AgentStatusAvailable
   available == false → AgentStatusNotConfigured   // 一律，不看 detail
   ```

   因此 gate 选项 A（`available=false` + reconnecting 文案、不新增枚举）打到 hello_ack 上 **就是 `status=not_configured`**，与同节「禁止 not_configured / backend 必须保持可见」直接冲突。

2. **「iOS 对 not_configured 收起入口」证错**。v2 写「`agent_descriptor.go:26-28` 注释自证」。本树 `:26-28` 是 `port_conflict` / `version_unsupported` / `permission_denied`；`not_configured` 注释在 `:29-31`，说的是 **OpenCode URL 未配置**，不是 iOS 侧栏。

   本轮读 iOS：`CCCodeAgentDescriptor` 有 `status`/`reason`（`CCCodeBridgeModels.swift:178-186`），但入列只按 kind：

   `BridgeProvider.swift:887`（重连 `:1131` 同样）

   ```
   connectedBackends = backends.filter { BackendKind.fromWireKind($0.kind) != nil }
   ```

   `deepseek-web` 能认出（`BackendModels.swift:34`）。**没有按 `status == not_configured` 过滤。** `recoverBy` 只在 `BridgeWireError` 解码，无消费点。一轮 §5「iOS 渲染未核验」现在可以结案：现行 iOS **不会**因 hello status 把 DeepSeek Harness 收起。

3. **后果**。继续把「藏入口」当前提，实施者要么误加 `reconnecting` 枚举（wire 变更，v2 自己说能免则免），要么走选项 A 把禁止的 `not_configured` 打上 hello。

**修改建议**（收敛成一条默认路径，关掉二选一 gate）：

- 宽限内 `InstanceStatus()` 保持 **`available=true`** + detail 标明 `instance reconnecting (grace until <t>)`。hello 仍是 `available`，入口在；RPC / 进行中 turn 按已写的 `backend_unavailable` 与终态事件暴露失败。
- 写明：若把 `available=false` 交给现行 detector，hello **必然**变成 `not_configured`。除非同时改 `detectInstanceStatusProber`，不要走这条。
- 新增 `AgentStatus` 枚举从「实施第一步实测二选一」降为「仅当未来 iOS 开始按 status 藏入口时再考虑」。
- 「禁止 `not_configured`」收窄为 **RPC 错误码**（语义是未配置，不是重启中）。理由改为协议语义，不要再写「iOS 会藏入口」。
- `agent_descriptor.go:26-28` 那处引用删掉。

### 建议

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| R2-S1 | 已绑定 session 的 `Send` 不走 `Resolve`/`clientFor` | `session.go:132-144` 用 `s.client.Call`；`handlers.go:2268-2319` 失败码是 **`send_failed`**。go-bridge **零处**发出 `backend_unavailable` | §3.2 / 实施切口补一句：宽限错误要在 handler 显式映射成 `backend_unavailable`（含已打开 session 的 send）；不要落成现行 `send_failed`。协议表 `recoverBy=reconfigure_backend` 目前 iOS 不消费，不要据此做「去设置页」 |
| R2-S2 | 进行中 turn 终态是 §3.2 硬要求，实施切口没写生产者 | 流 1006 之后 `runStreamLoop` 只重连，不发 `turn_error` | 实施切口加上：进入宽限 / 缓存探活失败时，对 registry 里 running 的 dsh-web session 推终态。漏了会再踩坑 8 |
| R2-S3 | 文首「两树 diff 仅 approvals / agent_descriptor / background_tasks_test」不完整 | `git diff 5f9237b` 还有 `go-bridge/main.go`、`RuntimeManager.swift` | 补进例外清单。行为结论仍成立（dsh-web 默认仍 `return true`；`:489` 仍是 120min），但「完全一致」字面不成立 |
| R2-S4 | `dsh_web_url` 写成现行「跳过探测」 | `dshweb.go:94` 注释确实这么写；代码是 `WithProbeURLs` 替换列表后 **照样探**，未命中仍 spawn **3096–3196** | §9 改成「现行=只探配置 URL，miss 仍去 3096；本案改为配置端口就是位子，spawn 也绑它」 |
| R2-S5 | `main.go:796` 标成「默认分支」 | 本树 `:796` 是函数头，`return true` 在 `:810`；main 上函数在 `:778`、`return true` 在 `:787` | 锚到 `return true` 那一行 |
| R2-S6 | 冷启动 30s spawn 期间并发 RPC 看到什么 | §3.2 只定义了宽限；§3.3 single-flight「上次已知」在冷启动是空 | 补一句：冷启动补拉中并发 RPC 立即 `backend_unavailable`（或沿用 in-flight 文案），不要阻塞 30s |
| R2-S7 | §8.3 冷启动行仍写死 localhost:3080 | 与 §9「位子可以是 4000」不完全一致 | 改成「权威端口（默认 3080）」 |

## 4. 一轮 §5 未核验项

| # | 原状 | 本轮 |
|---|---|---|
| ① iOS 对 `backend_unavailable` / `available=false` 的渲染 | 未核 | **已核。** hello 列表不按 status 藏 backend。RPC 侧 go-bridge 今天发的是 `send_failed`，iOS 走通用错误气泡；`backend_unavailable` 尚无专用 UI。足以关掉「必须新枚举」gate（R2-1） |
| ② 用户手搓 `dsh web` 冷启动分布 | 未测 | 仍挂账。2s 触发器下不再承重 |
| ③ workspace.json 双写是否损坏 | 挂账 | 维持 |
| ④ 19:59:48 重连目标 URL 铁证 | 反推 | 维持；§3.4 日志落地后自动消 |

## 5. 脚本 / 跨策略核对

- `backend_unavailable`：协议表有、go-bridge 无发射点、iOS 无专用分支——三处一致，说明这是**新接线**，不是复用现成路径（R2-S1）。
- `not_configured` 藏入口：Go 注释策略 vs iOS `fromWireKind` 策略**不一致**；信 iOS。
- 两树：本案 `resolver.go` / `streams.go` / `session_discovery.go` / `hello_handler.go` / `dsh-web` 主体与 main 无 diff；`main.go` 多了 opencode-web 分支，不影响 dsh-web 默认 true。

## 6. 修订优先级

**P0（v2.1 改完再动代码）**

1. R2-1：收敛 §3.2 hello 默认路径，删掉错误的 iOS 藏入口断言，写明 detector 映射。

**P1（同一稿顺手，避免实施漏项）**

2. R2-S1 handler 映射 `backend_unavailable`（含已打开 session）
3. R2-S2 turn 终态写入实施切口
4. R2-S6 冷启动并发 RPC

**P2**

5. R2-S3/S4/S5/S7 锚点与措辞

改完 R2-1 即可按 §11 切口开工：`resolver` 状态机 + 文案 + 生命周期单测 + 退役 3096–3196 + 活文档。**仍不必先动 iOS**——本轮已证明现行客户端不会因 hello status 把入口收走。
