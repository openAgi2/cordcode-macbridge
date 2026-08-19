# dsh-web 单实例 3080 收敛方案(实例分裂根因 + 端口即身份重设计)

- 日期:2026-08-19(**v4:三轮评审收口稿(定稿)**。一轮 M1–M6/S1–S10 见 `…-review.md`、二轮 R2-1/R2-S1…S7 见 `…-review-r2.md`(均「修改后通过」)、三轮见 `…-review-r3.md`(**APPROVE,收口**);三轮采纳账目见 §11/§12/§13)
- 状态:**定稿,可交开发者**——按 §12 五件切口 + §12.1 工程注记实施;单测至少覆盖 §8.1 与 §8.5(§12 末)。三轮判定:不必再改设计、不必先动 iOS(现行客户端不按 hello status 收起入口,通用错误气泡足够)、不再开方向评审。本文档代码零改动。
- 背景:真机现象——iOS 端 DeepSeek Harness(wire kind `deepseek-web`)收发正常,Mac 端用户自己的 `dsh web`(默认 3080)UI 不同步。根因为**实例分裂**:bridge 翻粘到 managed 3096 孤儿实例,与用户 3080 是两个进程、两套内存投影。评审当晚本机即处于该分裂态,活体取证见 §0.3。现行行为的设计出处为 `2026-08-16-dsh-web-backend-design.md` §4.2(S3 双实例策略),本文是对该节的方向性修订。
- 不变约束:零迁移、双向接力、不代装(npm 不代装)不变;原「未启动(不代拉)」按 owner 2026-08-19 裁决修订为「**缺位则补拉到权威端口**」(v1 文首「永不自托管」措辞与此矛盾,系沿袭 08-16 之前旧初衷未更新,评审 S4 指正)。新增并立死一条不变量:**任何时刻本机最多一个 dsh web 实例,且坐在权威端口上(= 探测列表首位,默认 3080;用户显式配置则为其配置值,见 §9)**。
- 代码锚点:**按 `opencode/web` 分支树核对(2026-08-19;二轮评审核对树 `9796432`)**。与 main `5f9237b` 存在 diff 的文件完整清单(二轮 R2-S3 修正):`agent/dsh-web/approvals.go`、`background_tasks_test.go`、`go-bridge/agent_descriptor.go`、`go-bridge/main.go`(opencode-web 分支逻辑,不影响 dsh-web 订阅判定——默认分支 `return true` 本树 `:810`)、`MacBridge/MacBridge/Services/RuntimeManager.swift`(不触及 `:489` 的 120min 默认)。本案引用的全部锚点行(resolver/streams/dshweb/sessions/diagnostics/session_discovery/hello_handler/agent_descriptor)已在本树逐一复核成立;本文行号一律指 `opencode/web` 树。

## 0. 事故回顾与取证链(前因)

### 0.1 现象与判别

- iPhone 用 DeepSeek Harness 发消息,流式回复正常、turn 正常收口;
- Mac 上用户自己的 3080 web UI 对这些 turn 无感(实时流不到;列表项的 turns/updatedAt 也停在本宇宙的旧值,见 §0.5);
- 重启 3080 实例不解决问题(3096 孤儿活着,resolver 永不回头看 3080)。

**判别器分只读与突变两类(评审 S2,取证时注意)**:

| 类别 | 手段 | 说明 |
|---|---|---|
| 只读(第一步用这些) | `lsof -nP -iTCP:3080,3096 -sTCP:LISTEN`;读 `~/Library/Application Support/CordCode Link/dsh-web-managed-server.json`;`POST /internal/agents/dsh-web/test`(读活 `InstanceStatus()`/`Current()`) | 不触碰 resolver |
| **会突变** | `run_diagnostics`(`agent/dsh-web/diagnostics.go:61` 内部走 `Resolve()`)、任何触发 `clientFor` 的 RPC | 判别动作本身可能造成翻转/收养/补拉,不得当第一步 |

### 0.2 现行机制与触发面(代码事实)

resolver 三步 ladder(`agent/dsh-web/resolver.go:321`):

1. 缓存实例探活(`probeTimeout` 2s,`resolver.go:88`),应答即原样返回——**粘滞**:只要缓存活着,永不重探权威端口;
2. 探外部 `127.0.0.1:3080`,命中 → external;
3. `loadState()` 收养 state 文件指向的旧 managed 实例(`resolver.go:358` 收养注释),或 spawn 到 **3096–3196**(`resolver.go:47`;`managedBootTimeout` 30s,`resolver.go:91`),并缓存整个实例生命周期(§4.2 S3 原文:"probing happens once per instance lifetime…a user starting 3080 later coexists with managed")。

**Resolve 的真实调用面是三层(评审 M1 修正,v1 只写了 watcher 一层)**:

| 触发面 | 代码锚点 | 频率 |
|---|---|---|
| RPC:每次 session 目录/流操作经 `clientFor()` | `agent/dsh-web/sessions.go:22`(`ListSessions` 等) | 按操作 |
| **流重连:mux/host 常驻流断线后 2 秒重开** | `agent/dsh-web/streams.go:28-29`(`streamReconnectBackoff = 2s`)、`runStreamLoop`(`streams.go:98`);`shouldStartPassiveSubscription("dsh-web")` 恒 true(`go-bridge/main.go:796` 函数头,默认分支 `return true` 在本树 `:810`,二轮 R2-S5 校正)——iOS 连着时双流常驻 | 断线后每 ~2s |
| 兜底:逐 backend discovery watcher | `go-bridge/session_discovery.go:33`(`sessionDiscoveryInterval = 60s`),调 `ListSessions` → `clientFor` → `Resolve` | 每 60s |

### 0.3 根因:主触发器是 2s 流重连,17 秒停机足够(评审当晚活体)

当晚本机分裂态活体:

```
3080  LISTEN  pid 21312  node dsh web                     STARTED 2026-08-19 20:00:03(用户)
3096  LISTEN  pid 1406   node dsh --profile web --host 127.0.0.1 --port 3096
                                          STARTED 2026-08-18 11:37:05(33h+ 前的 managed 孤儿)
state 文件 dsh-web-managed-server.json:url=http://127.0.0.1:3096, pid=1406
```

翻转时间线(日志):

```
19:38:54  instance resolved  source=external  baseURL=http://127.0.0.1:3080
19:38:54  stream open mux + host
19:59:46  stream ended mux/host  close 1006 unexpected EOF   ← 用户重启 3080
19:59:48  stream open host / mux                              ← 2.1s 后重开 → clientFor → Resolve
20:00:03  新 3080 进程启动(pid 21312)                       ← 比重连晚 15s
```

19:59:48 那一刻:缓存 3080 已死、新 3080 还没起来、state 指向的 3096 孤儿活着 → 第 3 步 `loadState` 收养 → 粘死。**没有新 spawn;17 秒停机足够;2 秒流重连就是触发器。**

由此修正 v1 的窗口论断:

- **已接线(iOS 连着、双流常驻)⇒ 3080 停机超过 ~2s 且新实例未及应答 `host.describe`,即翻**;
- **无接线(无客户端无流)⇒ 停机超过 ~60s,watcher tick 必落在窗口内,必翻**(v1 原论证,对 watcher 成立,是充分条件而非主路径);
- 60s 论证不因修正作废——它仍是「无流状态」下的保底翻转路径,验收矩阵两行都要(§8.1/§8.2)。

### 0.4 静默面的准确形状(评审 M2 修正)

v1 写「hello_ack 的 InstanceStatus 只镜像启动时结果」归因过窄。代码事实:

- `InstanceStatus()` 读的是**活的** `Current()`(`agent/dsh-web/dshweb.go:164`);hello 每次都重建 descriptor(`go-bridge/hello_handler.go:164`),重连的 hello 能看到翻转后的 source 字符串。
- 真正冻住/缺失的是四张面:**① 已连着的 iOS 不再 hello**,拿不到新 reason;**② `GET /internal/agents` 的 `cachedAgentDescriptors`** 是独立缓存(当晚 GET=external 3080、`/test`=managed 3096,同一时刻两套说法);**③ 翻转后 status 仍是 `available`**,`AgentStatus` 枚举(`go-bridge/agent_descriptor.go:20-32`)没有拓扑/重连语义,不产生任何事件;**④ `Resolve` 本身不打日志**(只有启动时的 `backgroundResolve` 打 source),翻转在日志里也静默。

### 0.5 衍生事实与残留

- **「列表也不出现」的归因修正(评审 S3)**:当晚两边 `session.list` 是**同一 20 个 id**(同一磁盘 catalog、两套内存投影);实际分歧在 turns/updatedAt(如 `session-6d807c35…` 3080 侧 31 turns、3096 侧 32;`session-d8ac5e0c…` 5 vs 1)。v1 把「列表不出现」推给 workspace.json/坑 5 过满——坑 5 原义是「未分组」不是「不在列表」。正确表述:3080 在跑时看不见对方内存里的新 turn;冷启后 list 应能从磁盘冒出;仍缺再查归组(未分组 ≠ 不在列表)。双实例写 workspace.json 是否损坏维持原设计 S3 挂账(当晚未做写实验)。
- **孤儿累积**:3096 孤儿已活过多次 Link 重启(收养实例 `Stop()` 不杀,`resolver.go` Stop 语义);若再发生一轮翻转,`pickFreePort` 会跳过被占端口、spawn 新实例并覆盖 state 文件——旧实例从此无人管理。state 文件至今仍指向 33h+ 前的 pid 1406。

## 1. 现行设计评估:它防御什么、糟在哪

**粘滞本身不是错的。** dsh v1 上游没有实例协调原语(无固定 socket/锁文件、无跨进程事件总线、无 `since` 续传、store 无锁)。mux/host 两条事件流按实例持有,会话内存态亦然;若 resolver 每次 RPC 重挑,两端实例间反复横跳会造成更糟的裂脑。粘滞是对横跳的合理防御。

真实缺陷四条:

| # | 缺陷 | 说明 |
|---|---|---|
| 1 | **触发器平凡且快** | 「用户重启自己的实例」是最日常的运维动作;已接线时 **2 秒流重连**即翻(§0.3),无接线时 60s watcher 兜底必翻。原设计只按「用户后来才启动 3080」评估共现风险,漏了「重启 bridge 正连着的实例」这条更常见的边 |
| 2 | **翻转完全静默** | 四张面俱全:已连客户端不再 hello;Management GET 有独立缓存;status 恒 `available` 无拓扑事件;Resolve 不打日志(§0.4)。与「失败路径必须产生可见终态」红线相抵触——非违反字面(红线对 turn/session 失败而立),而是红线未覆盖「实例拓扑静默变更」类别 |
| 3 | **无回流路径** | 翻到 managed 后只要它活着永不回权威端口;恢复只有「权威端口实例在跑时重启 Link」或「手杀 managed 进程」,普通用户不可发现 |
| 4 | **可用性换一致性且不暴露** | 若无 spawn,实例不在 = 如实 not_configured(可见失败);spawn 兜底买来的可用性,代价是一致性静默劣化——把可见失败换成不可见分裂 |

## 2. 方案总述:端口即身份

一句话:**把「实例身份」从「谁 spawn 的」改成「权威端口这个位子」——权威端口(探测列表首位,默认 3080;用户显式配置则为其配置值)是唯一实例的位子;bridge 缺位时把服务补拉到位子上,而不是另立山头;实例失联后先给重启量级的宽限期持续重探,再考虑补拉。**

同步在此架构里的唯一载体是同一个 dsh web 实例(web UI 看它、bridge 转发它、事件流从它出)。守住单实例不变量后:

- **「同步」从需要维护的功能变成单实例的自然结果**:Mac→iOS 已有接线(events.host 流 + discovery watcher)与 iOS→Mac(web UI 作为同一服务的浏览器客户端)自动对齐;
- **「收养错实例」「孤儿累积」「让位迁移」这一族问题被结构性删除**(不是修好,是不存在):只有一个位子;
- 现行 `dsh-web-managed-server.json` 的端口+PID 身份记录,本质是给「多实例可能并存」打补丁;单位子世界里几乎失去必要。

## 3. 解析状态机

### 3.1 状态与转移

现行「缓存探活 → 探权威端口 → 收养/新拉」折叠为以权威端口为中心的状态机:

1. **每次解析:探权威端口。** 应答 → 用它(source 标签规则见 §4 竞态 1,只影响诊断与关停语义)。
2. **无应答,且本 bridge 进程生命周期内从未有过实例**(冷启动)→ 直接在权威端口上 spawn。**进程重启 = 冷启动**(评审 S1):Link 重启、120 分钟自动重启(`MacBridge/MacBridge/Services/RuntimeManager.swift:489`,`autoRestartIntervalMinutes` 默认 120)后新进程无内存,权威端口在则收养、不在则立刻 spawn——与用户恰在同时重启实例的交错走进 §4 竞态 1(EADDRINUSE 认端点),**不进宽限**。
3. **无应答,但本进程此前有过实例**(= 失联,典型触发即用户重启)→ 进入**宽限期(90–120 秒,可调)**:宽限内每次解析重探权威端口,对调用方按 §3.2 的 wire 契约如实返回,**不收养别的端口、不 spawn**;到期仍无应答 → 在权威端口上补拉。

宽限期时长不再锚在「用户手搓实例冷启动分布」上(评审 §5:未测,30s 只是我们自己的 spawn 上限;2s 触发器使该数字不再承重),锚在:盖住 60s watcher 间隔 + 一次人手重启 + 30s 启动预算,留余量。

对 §0.3 的事故走一遍:已接线、流 2s 重连 → 缓存探活失败 → 进入宽限,持续重探 → 用户实例 15 秒后回来 → 重新绑上 → **全程无翻转、无第二个实例、不收养 3096 孤儿**;宽限内的代价按 §3.2 呈现。

### 3.2 宽限的 wire 契约(P0,一轮 M3;二轮 R2-1 收敛为单一默认路径)

宽限不是「不阻塞地等」,必须定义每个调用面看到什么。二轮收敛后不再留二选一 gate:

- **`InstanceStatus` / hello(默认路径)**:宽限内 `InstanceStatus()` 保持 **`available=true`**,detail 标明 `instance reconnecting (grace until <t>)`;hello_ack 仍是 `available`,入口在。失败只经下面两条面(RPC 错误 + turn 终态)暴露。
  - **为什么不能走 `available=false`(映射层事实,二轮 R2-1)**:hello 不直接消费 `(available, detail)` 二元组,而是经 `detectInstanceStatusProber`(`go-bridge/agent_descriptor.go:246-258`):`available=false` **一律**映射为 `AgentStatusNotConfigured`,不看 detail——「available=false + reconnecting 文案、不加枚举」打到 hello_ack 上**就是 `not_configured`**,与「backend 保持可见」直接冲突。除非同时改 detector,不走这条。
  - **iOS 侧实测(二轮 Swift 读取;撤回 v2 的错误引证)**:v2 曾以 `agent_descriptor.go` 注释断言「iOS 对 not_configured 收起入口」——证错,该注释(`:29-31`)说的是 OpenCode URL 未配置,与 iOS 侧栏无关。iOS 实测:入列只按 kind——`BridgeProvider.swift:887`(重连 `:1131` 同)`:backends.filter { BackendKind.fromWireKind($0.kind) != nil }`;`BackendModels.swift:34` 认 `deepseek-web`;descriptor 的 status/reason(`CCCodeBridgeModels.swift:182-183`)**没有「按 status 藏入口」的消费点**。结论:现行 iOS 不会因 hello status 收起 DeepSeek Harness 入口。新增 `AgentStatus` 枚举(`reconnecting`)从「实施第一步实测二选一」**降级为「仅当未来 iOS 开始按 status 过滤入口时再考虑」**。
  - 可见性如实注记:reconnecting 的 detail 主要在 Mac 侧(management/诊断/§3.4 日志)可见;iOS 用户侧的 fail-visibly 由下两条承担。
- **RPC(`send_message`、`list_sessions` 等)**:返回协议错误码 **`backend_unavailable`**(`docs/protocol/unified-bridge-protocol.md:169`,现行错误表已有,语义「Backend 进程不可达」与宽限吻合)+ 稳定 message(如 `dsh web instance reconnecting (grace)`)。**禁止 `not_configured` 作宽限错误码**——理由收窄为**协议语义**(not_configured 意为「未配置」,不是「重启中」;不再以「iOS 藏入口」为据,该断言已撤回)。不新造 iOS 不认识的错误码。
  - **send 路径须显式映射(二轮 R2-S1;三轮补 list 面与精确锚点)**:已打开会话的 `Send` 走建连时绑定的 client(`agent/dsh-web/session.go:132-145`,`s.client.Call`),**不经 `clientFor`/`Resolve`**;现行 send 失败在 handler 落 `send_failed`(`go-bridge/handlers.go:2284/2306/2319` 三处),`list_sessions` 失败落 `list_failed`(`handlers.go:1541/1606/2808/2939` 四处),且 go-bridge 今天**零处**发射 `backend_unavailable`——它是**新接线**,不是复用现成路径。实施时宽限错误须在 handler 显式映射成 `backend_unavailable`(**含已打开会话的 send 与 list_sessions**,经类型化错误识别,见 §12.1 第 1 条),不得落成现行 `send_failed`/`list_failed`。协议表 `recoverBy=reconfigure_backend` iOS 不消费,不得据此设计「去设置页」引导。
  - **冷启动补拉期间同理(二轮 R2-S6)**:冷启动 spawn in-flight 时并发 RPC 立即返回 `backend_unavailable`(或现行 in-flight 文案),不阻塞 30s——single-flight 的「上次已知」在冷启动为空,直接走错误返回。
- **进行中 turn(坑 8 红线)**:实例失联(缓存探活失败/流死亡)时,该 backend 上 running 会话必须收**终态错误事件**(turn_error 类,透传「dsh web 实例失联」原文),不得卡「执行中」。实例回来后由 history 重拉/投影 forceCold 补齐真实状态(既有断线语义,设计 §4.3.3)。**终态生产者须显式实现(二轮 R2-S2)**:流 1006 断开后 `runStreamLoop` 只重连、不发 turn_error——进入宽限/缓存探活失败时,须对 registry 中 running 的 dsh-web 会话推终态;此项入 §11 实施切口,漏了会再踩坑 8。
- **会话列表**:宽限内 `ListSessions` 失败时**保留 last-good fingerprint**——这是现行行为(`go-bridge/session_discovery.go` ~:205-217,枚举出错不清 `seen`),写进方案防实施者「出错就清 catalog」。

### 3.3 锁与非阻塞(P0,评审 M4)

现行 `Resolve` 一进门 `r.mu.Lock()`,`spawnManaged` 的 30s boot-wait 也在锁内——一次补拉卡住所有 `clientFor`(mux、host、ListSessions、发消息)最多 30s。实施约束:

- `r.mu` **只护缓存实例与 `lostAt` 的读写**;探活(`probeTimeout` 2s)、spawn boot-wait(`managedBootTimeout` 30s)一律在锁外;
- 并发解析走 single-flight:同一时刻一个探测者,其余调用方拿「上次已知状态 + §3.2 契约」立即返回,不得每人各付 2s 探活;
- 宽限内对权威端口的反复 `host.describe` 设 **≤1s 负缓存**,避免 mux+host+RPC 每流每秒各付 2s;
- 补拉异步或在锁外等端点;§4 竞态 1 的「无错误暴露到 iOS」只有在 spawn 不持锁时才成立。

### 3.4 观测(评审 S5)

`Resolve` 每次 source 变迁打 INFO 日志(from→to + 原因:cache-dead / adopt / spawn / grace-expiry / rebind)。当晚翻转在日志里静默,唯一 source 日志来自启动时的 `backgroundResolve`——此坑一并封掉。

## 4. 权威端口之争的竞态

1. **双方同时抢权威端口**(bridge spawn 时用户实例恰在启动中):bridge 子进程 bind 失败死掉,但 boot-wait 循环探的是**端点**(现行 `spawnManaged` 即 `probeInstance(baseURL)`,不看 PID——此语义保持),用户实例随后应答 → 认它。**source 标签规则(评审 M5,拆成两句)**:探测=端点;**标签=本进程孩子仍活着(`processIsAlive`,`resolver.go:473`,已写好未接线——接线)且仍占该端口才是 `managed`,否则 `external`;永不显示尸体 PID**(现行 boot-wait 命中后恒打 `SourceManaged` + 自己孩子 PID,`resolver.go:391-396`,孩子已死时标签与 PID 双错)。
2. **bridge 的权威端口实例在跑,用户手动 `dsh web`(未配置自定义端口)**:得到 EADDRINUSE。**接受并文档化**:「CordCode Link 运行时权威端口已由它拉起,直接开浏览器即可」。dsh v1 无监听器移交原语;且严格好于现状——现状用户实例「启动成功」实为静默平行宇宙。
3. **权威端口被非 dsh 服务占用**:探测不认、spawn bind 不上 → 如实 not_configured,诊断区分「端口被非 dsh 进程占用」。与现行失败类别相同。

安全红线不变:spawn 恒为 `--host 127.0.0.1 --port <权威端口>`,永不用 `--host 0.0.0.0`/`--trusted-host`(原设计 §4.4)。

## 5. 关停语义:推荐「不杀 + 下次收养」

先澄清现状(评审 S10,防实施者误判):现行 `Stop()` 只杀**本进程 spawn 的** managed;收养实例(`cmd==nil`)**已经不杀**——3096 孤儿活过多次 Link 重启即证。本推荐是把「本进程 spawn 的权威端口实例」也改成与收养相同的不杀:

| 选项 | 代价 | 评价 |
|---|---|---|
| 杀(现行对本进程 spawn 的语义) | 用户正开浏览器看会话时,退出 Link 把 web UI 一起带走 | 简单无滞留,体验突兀 |
| **不杀,留任,下次探权威端口自然收养(推荐)** | 升级 dsh 后旧进程滞留跑旧代码 | 端口即身份使这绝对安全:只有一个位子,不存在收养错或累积;浏览器不因 Link 退出而断;bridge 重启对浏览器侧无感 |

推荐后者,与初衷同向:**会话世界的连续性优先于进程所有权洁癖**。滞留代价的补偿:诊断输出当前实例 PID + 进程启动时间(`host.describe` 无启动时间字段,评审 S9——用 `ps` 取,写一句即可),升级后可见地提示手动重启。

## 6. 旧状态迁移与残留核验(一次性)

- 升级到单端口方案时,旧版可能留下 3096–3196 上的 managed 实例与 state 文件。**PID 安全清理(评审 S6)**:按 state 文件 PID 杀前,核对其 cmdline 含 `dsh` 且仍监听在记录端口;对不上(PID 复用)只删 state 文件 + 告警,不杀。会话数据已在共享磁盘 store,杀掉无损。随后删除 state 文件、**退役 3096–3196 端口区间与 `managedPortMin/Max`**。
- **旧会话核验**:当晚两边 `session.list` 同一 20 个 id(§0.5)——磁盘 catalog 共享无恙;分歧在内存投影的 turns/updatedAt。升级后以权威端口实例的 history 重拉为准即可对齐;若个别会话在 web 侧栏**不出现**且冷启后仍不出现,才是归组/workspace.json 层问题(未分组 ≠ 不在列表),单独立案,不混修。

## 7. 排除的替代路线

| 路线 | 排除理由 |
|---|---|
| 保留多端口,给两实例做同步桥 | 死路:v1 无跨实例通知面,live turn 无法注入另一进程内存态;轮询 store 只能同步列表同步不了会话;且迫使 bridge 持有自己推导的 dsh 事实,违反设计前提 |
| 不 spawn,权威端口不在即 not_configured | 诚实简单,但用户手动起服务前 iOS 完全不可用,违背 owner「应该尝试启动」裁决 |
| 维持双端口,仅加「分裂告警」 | 治标:静默分裂变吵闹分裂,两个宇宙的问题还在 |

## 8. 验收矩阵增补行

1. **流重连翻转回归行(本次事故的回归行,评审 M1/S7)**:已接线双流常驻 → 用户重启权威端口实例、停机 ~17s → 现行 2s 内粘 3096 孤儿;修复后:宽限内不收养不 spawn、RPC 得 `backend_unavailable`、进行中 turn 收终态错误 → 实例回来自动重绑,无双实例、日志有 source 变迁。
2. **watcher 兜底行**:无接线(无客户端无流)→ 停机 >60s → 宽限接管,同上,不翻。
3. **冷启动行**:权威端口从未有实例 → bridge 拉起 → 浏览器打开权威端口(默认 3080)实时看到 iOS 消息与回复(事件帧传播,非轮询);补拉期间并发 RPC 立即 `backend_unavailable`,不阻塞(二轮 R2-S6/S7)。
4. **进程重启 = 冷启动行(评审 S1)**:Link 重启/120min 自动重启 → 权威端口在则收养(不进宽限)、不在则立刻 spawn;与用户同时重启实例 → EADDRINUSE 认端点、标签 external。
5. **宽限 wire 契约行(评审 M3 + 二轮 R2-1/R2-S1)**:宽限内 send(含已打开会话)→ `backend_unavailable` + 稳定 message(非 not_configured、非现行 `send_failed`);hello 仍 `available` + reconnecting detail;进行中 turn → 终态错误事件;`ListSessions` 失败 → catalog 保留 last-good 不清空。
6. **锁纪律行(评审 M4 + 二轮 R2-S6)**:补拉 30s 窗口内并发 RPC 不被串行阻塞(single-flight + 锁外 I/O);宽限内探活有 ≤1s 负缓存;冷启动补拉期间并发 RPC 立即错误返回,不阻塞。
7. **权威端口可配置行(评审 M6)**:`dsh_web_url` 配 4000 → 位子=4000,spawn 绑 4000,3080 上另起的实例属用户自己的非权威用法,诊断如实标注。
8. **升级清孤儿行(评审 S6)**:升级后杀 3096 旧孤儿——cmdline+端口核对通过才杀;对不上只删 state + 告警。
9. **共存认知行**:bridge 的权威端口实例在跑时,手动 `dsh web` 得端口占用提示,文档化预期行为。
10. **留任行**:退出 Link 浏览器不断;重启 Link 无缝收养同一实例。

## 9. 固有边界与上游根治

- **权威端口的定义(评审 M6 对齐;现行语义按二轮 R2-S4 如实修正)**:位子 = **探测列表的首位**——默认 3080;用户显式配置 `dsh_web_url`(`agent/dsh-web/dshweb.go:109-110`)则**配置值就是位子**,spawn 只绑这个位子。**现行行为如实**:dshweb.go:94 注释写「显式 URL,跳过探测」过满——代码实际是 `WithProbeURLs` 替换探测列表后**照样探**,未命中仍 spawn 到 3096–3196(即配置非 3080 的用户今天同样可能分裂);本案改为「配置端口就是位子,补拉也绑它」。用户配了 4000 又在 3080 起实例,身份是 4000,3080 属用户自担的非权威用法——显式配置的特例语义,不是「再分裂一次」。
- **上游根治**:向 dsh 提需求——权威实例发现原语(锁文件/固定 socket/实例注册面)或跨进程失效通知。bridge 在 v1 上只能选择暴露哪种失败;本方案把失败暴露为「端口争用/如实的不可用错误」,而非「静默平行宇宙」。

## 10. 修复落地前的临时处置(现行部署态)

1. **确认(只读,勿用会突变的手段)**:`lsof -nP -iTCP:3080,3096 -sTCP:LISTEN`;`POST /internal/agents/dsh-web/test` 含 `managed`/`3096` 即坐实。不要拿 `run_diagnostics` 当第一步(它内部 `Resolve`,判别本身可造成翻转,评审 S2)。
2. **恢复**:3080 已在听的话,杀掉 3096 上的 `dsh`(当晚 pid 1406)——进行中 iOS turn 随 3096 死;流还连着则约 **2s** 重连即重绑 3080(评审 M1 连带修正:v1 写「等下一个 60s tick」偏慢,实际是任一 Resolve——流/RPC/tick——先到先重解析)。或先保证 3080 起来再重启 CordCode Link(新进程第一步探权威端口)。
3. **不要先重启 3080 当修复**:3096 孤儿活着,resolver 永不回头。

## 11. 一轮评审采纳记录(v2 对照 `2026-08-19-dsh-web-canonical-3080-instance-design-review.md`)

结论「修改后通过」;必改 M1–M6、建议 S1–S10 **全部采纳,无不采纳项**。逐项落点:

| 项 | 处置 | 落点 |
|---|---|---|
| M1 主触发器=2s 流重连,v1 写成 60s watcher | 采纳 | §0.2 三层触发面表、§0.3 时间线与双情形论断、§8.1 回归行、§10.2 恢复等待修正 |
| M2 「InstanceStatus 只镜像启动时」归因过窄 | 采纳 | §0.4 重写为四张静默面(①不重 hello ②Management 独立缓存 ③status 恒 available ④Resolve 无日志);§1 缺陷 2 同步改 |
| M3 宽限无 wire 形状 | 采纳 | §3.2 专节:错误码选型 + 禁 not_configured + turn 终态 + last-good 保留 + InstanceStatus/hello 可见性 + 实施 gate(该 gate 已被二轮 R2-1 关闭,见 §12) |
| M4 Resolve 持锁跨探活/spawn | 采纳 | §3.3 专节:锁只护缓存/lostAt、single-flight、≤1s 负缓存、spawn 锁外;§8.6 验收行 |
| M5 端点探测与 source 标签混为一谈 | 采纳 | §4 竞态 1 拆两句:探测=端点;标签=孩子活且占端口才 managed(`processIsAlive` 接线);永不显示尸体 PID |
| M6 「永远 3080」与 `WithProbeURLs` 打架 | 采纳 | §2/§9:位子=探测列表首位(默认 3080,配置则配置值);§8.7 验收行 |
| S1 进程重启=冷启动 | 采纳 | §3.1 第 2 条 + §8.4 |
| S2 判别器只读/突变二分 | 采纳 | §0.1 表、§10.1 |
| S3 「列表不出现」归因过满 | 采纳 | §0.5 重写(当晚两边同 20 id;坑 5 原义=未分组)、§6 |
| S4 文首初衷措辞与 spawn 矛盾 | 采纳 | 文首不变约束改写:零迁移/双向接力/不代装不变,「未启动」按 owner 裁决改为「缺位则补到权威端口」 |
| S5 翻转无日志 | 采纳 | §3.4(source 变迁 INFO)、§8.1 |
| S6 按 PID 杀孤儿不安全 | 采纳 | §6(cmdline+端口核对)、§8.8 |
| S7 验收缺行 | 采纳 | §8 扩至 10 行 |
| S8 活文档仍写 3096–3196 | 采纳 | 实施注记:实施 PR 同步改 `GO_BRIDGE_ARCHITECTURE.md:238`,不阻塞本提案 |
| S9 host.describe 无启动时间 | 采纳 | §5(ps 取进程启动时间) |
| S10 现行 Stop 对收养本就不杀 | 采纳 | §5 先澄清现状再给推荐,防实施者误判 |
| 评审 §7 最小实施切口 | 采纳 | 见下「实施切口」 |

**采纳中带选择/限定的两处,理由如下**:

1. **M3 错误码二选一 → 选 `backend_unavailable`**:协议错误表现行已有(`unified-bridge-protocol.md:169`,语义「Backend 进程不可达」),与宽限吻合;`service_not_running` 是 `AgentStatus` 枚举值(`agent_descriptor.go:25`)且语义偏「服务未运行」,与「正在回来的路上」不合,不用作 RPC 错误码。(二轮复核:选型维持成立,且该码在 go-bridge 今天零发射点——是 R2-S1 的新接线,不是复用现成路径。)
2. **M3 「InstanceStatus 增加 reconnecting 文案」→ 限定为 detail 文案层面,不预设新增枚举值**:~~以「iOS 实测二选一」为实施 gate~~ **已被二轮 R2-1 取代并结案**——gate 关闭,收敛为 §3.2 的单一默认路径(宽限内 `available=true` + reconnecting detail;`available=false` 经现行 detector 必落 `not_configured`,不可走)。

## 12. 二轮评审采纳记录(v3 对照 `…-review-r2.md`)

结论「修改后通过」;必改 R2-1、建议 R2-S1…S7 **全部采纳,无不采纳项**。逐项落点:

| 项 | 处置 | 落点 |
|---|---|---|
| R2-1 hello 段自相矛盾 + 「iOS 藏入口」证错 | 采纳 | §3.2 重写:收敛单一默认路径(宽限 `available=true` + reconnecting detail);`detectInstanceStatusProber` 映射入文(`agent_descriptor.go:246-258`,available=false 一律 not_configured);iOS 实测入文(`BridgeProvider.swift:887`/`:1131` 只按 kind 过滤、`BackendModels.swift:34`、`CCCodeBridgeModels.swift:182-183` 无按 status 藏入口消费点),v2 错误引证撤回;「禁 not_configured」收窄为 RPC 错误码层面、理由改协议语义;新枚举降级为「未来 iOS 按 status 过滤时再考虑」 |
| R2-S1 已打开会话 Send 不经 Resolve;现行落 `send_failed`;`backend_unavailable` 零发射点 | 采纳 | §3.2 send 路径段(`session.go:132-145`/`handlers.go:2268-2319` 锚点 + handler 显式映射要求 + recoverBy 不消费注记)、§8.5、§11 实施切口第 2 条 |
| R2-S2 turn 终态无生产者 | 采纳 | §3.2 turn 段(runStreamLoop 只重连不发 turn_error 的事实 + registry running 会话推终态要求)、§11 实施切口第 3 条 |
| R2-S3 文首两树 diff 清单不全 | 采纳 | 文首补全(main.go 43 行、RuntimeManager.swift 21 行入清单;行为结论经本树复核仍成立,「完全一致」过强表述删除) |
| R2-S4 「跳过探测」注释过满 | 采纳 | §9(现行=替换列表后照样探、miss 仍 spawn 3096–3196;本案=配置端口就是位子) |
| R2-S5 main.go 锚点 | 采纳 | §0.2(函数头 `:796`,`return true` 本树 `:810`) |
| R2-S6 冷启动并发 RPC 未定义 | 采纳 | §3.2 RPC 段、§8.3/§8.6 |
| R2-S7 §8.3 写死 3080 | 采纳 | §8.3(「权威端口(默认 3080)」) |

**一处补充注记(非不采纳)**:R2-1 建议的默认路径使 reconnecting 文案在 iOS 侧无直接消费点(入列不看 status)——已在 §3.2 如实注记:该 detail 的可见性在 Mac 侧(management/诊断/日志),iOS 用户侧 fail-visibly 由 RPC `backend_unavailable` + turn 终态承担。不据此改选 `available=false`(会触发 detector 落 not_configured,违背可见性目标)。

**实施切口(一轮 §7 + 二轮 R2-S1/S2 增补,共五件)**:

1. `resolver.go` 状态机:宽限(90–120s,不收养不 spawn)/锁纪律(mu 只护缓存与 lostAt、single-flight、≤1s 负缓存、spawn 锁外)/source 标签(`processIsAlive` 接线,端点探测≠标签)/变迁 INFO 日志;
2. handler 层宽限错误显式映射 `backend_unavailable`(**含已打开会话的 send 与 list_sessions**——今天分别落 `send_failed`(`handlers.go:2284/2306/2319`)与 `list_failed`(`handlers.go:1541/1606/2808/2939`);经 §12.1 第 1 条的类型化错误识别,**不得只改 `handleSendMessage` 三处**);
3. 宽限进入/缓存探活失败时,对 registry 中 running 的 dsh-web 会话推 turn 终态错误事件(幂等,见 §12.1 第 3 条);
4. 诊断与 `InstanceStatus` 文案(reconnecting detail、ps 取进程启动时间;特判见 §12.1 第 4 条);
5. 生命周期单测(粘滞、宽限不 spawn 不收养、到期 spawn 权威端口、EADDRINUSE 认端点、权威端口可配置、single-flight 不阻塞、turn 终态)+ 退役 3096–3196 + 同步活文档(S8,`GO_BRIDGE_ARCHITECTURE.md:237-241`)。

### 12.1 工程注记(三轮 §3,不挡开工但必读)

评审原判「不必回写设计也能做对」;为交接口径统一,并入本文,开发者开工时按此执行:

1. **类型化宽限错误**:`list_sessions` 今天失败码是 `list_failed`、send 是 `send_failed`——要让「含已打开会话」的 RPC 都变成 `backend_unavailable`,resolver 应返回**可 `errors.As` 的类型化错误**(如 `ErrInstanceReconnecting`),handler 各入口识别后改码;不要只改 `handleSendMessage` 三处。
2. **已打开 session 如何感知宽限**:`dshSession.Send` 不经 `Resolve`(`session.go:132-145`)。实现二选一(文档不锁死):`Send` 先查 resolver 宽限态并返回上述类型错误,或 handler 在 dsh-web send 前查询宽限态。
3. **turn 终态只推一次**:进入宽限与流 1006 断开可能先后发生——对同一 session 的终态必须**幂等**,避免双发 `turn_error`。
4. **`InstanceStatus` 必须特判宽限**:现行 `Current()==nil` → `available=false` → 经 detector 落 `not_configured`(`agent_descriptor.go:247-255`)。宽限期间(`lostAt` 置位)要报 `available=true` + reconnecting detail,否则 §3.2 默认路径被现行映射击穿。

**必测下限(三轮 §4)**:单测至少覆盖 **§8.1**(流重连 ~17s 停机,不收养 3096 孤儿)与 **§8.5**(已打开会话 send → `backend_unavailable`,非 `send_failed`)两行;其余 §8 行随切口第 5 件补齐。

**不必先动 iOS**——二轮已证明现行客户端不按 hello status 收起入口;`backend_unavailable` 在 iOS 走通用错误气泡(send 失败本就有气泡兜底,`session.go:131-133` 注释自证)。

**未核验项清单更新(一轮 §5 × 二轮 §4)**:① iOS 渲染——**结案**(hello 不按 status 藏入口;`backend_unavailable` 无专用 UI、走通用错误气泡);② 用户手搓 `dsh web` 冷启动分布——维持挂账,2s 触发器下不再承重;③ workspace.json 双写——维持挂账,升级清理后补观察;④ 19:59:48 重连目标 URL 铁证——维持,§3.4 日志落地后复现即自动留证。

## 13. 三轮评审收口记录(v4 对照 `…-review-r3.md`)

**APPROVE,收口**——二轮 R2-1 与七条建议的落点经抽查全部属实(detector 映射、Send 不经 Resolve、三处 `send_failed`、iOS 只按 kind 入列);无新必改,不再开方向评审。判定:不改设计、不动 iOS,开发者按 §12 五件 + §12.1 注记实施。

| 项 | 处置 | 落点 |
|---|---|---|
| 工程注记 1:类型化宽限错误(send + list 一起改码,不只三处) | 采纳(评审称「不必回写」,为交接口径统一仍并入) | §12.1 第 1 条、§12 切口第 2 条、§3.2 send 映射段 |
| 工程注记 2:已打开会话感知宽限的两种实现 | 采纳(不锁死) | §12.1 第 2 条 |
| 工程注记 3:turn 终态幂等(宽限进入与流 1006 防双发) | 采纳 | §12.1 第 3 条、§12 切口第 3 条 |
| 工程注记 4:`InstanceStatus` 特判宽限(防 `Current()==nil` 落 not_configured) | 采纳 | §12.1 第 4 条、§12 切口第 4 条 |
| 必测下限(§8.1 流重连不收养 3096、§8.5 send → backend_unavailable) | 采纳 | §12 末「必测下限」 |
| 三轮 §2 复核:二轮条款落点全部属实 | 无需处置 | — |
| §11 一轮表 M3 行残留「实施 gate」字样 | 顺手清理(评审判定不构成歧义) | §11 M3 行补注「该 gate 已被二轮 R2-1 关闭」 |
| `list_failed` 锚点 | 补全:评审引 `handlers.go:2939` 一处,本树实测共四处(`:1541/1606/2808/2939`),全部入文 | §3.2、§12 切口第 2 条 |

**无不采纳项。** 本节为终轮记录;后续变更走实施 PR 与回归验收(§8),不再回改本设计。
