# Codex Web 连续性架构：同一 daemon 上的观察、执行态与 Desktop 附着

- 日期：2026-08-22
- 状态：**施工前合同 / 整体设计草案。禁止按现象单修。**
- 适用仓库：MacBridge 本文件为权威；iOS 仓 `docs/` 下同名文件是给 iOS workspace agent 的工作镜像，冲突以本文件为准
- 前置合同：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)（v2.0 topology-first）
- 问题来源：[2026-08-22-codex-web-desktop-bidirectional-handoff-gap.md](2026-08-22-codex-web-desktop-bidirectional-handoff-gap.md)
- 活体复盘：MacBridge `think.md` 2026-08-22 条目
- 不变约束：CordCode 初衷 + SSV2 十二条 + 官方 Desktop 不可改 asar

> [!IMPORTANT]
> v2.0 证明了「Desktop 与 CordCode 必须连接同一个官方 daemon」。本文不推翻那条拓扑硬门。
> owner 真机已经证明：**拓扑 PASS ≠ 连续性 PASS**。缺的是 Link 进程生命周期、Desktop
> 不可逆 stdio 锁、iOS 观察面、投影执行相位之间的整体合同。后续 agent 必须按本文四层
> 一起设计、一起验收，不得把「列表闪一下」「卡执行中」「切 session 才同步」「CPU 高」
> 拆成互不相关的 hotfix。

> [!WARNING]
> 本文授权的是整体设计与按层施工。未完成本文 §6 的层合同之前，禁止再开「发现一个修一个」
> 的任务。已经落地的座位 / AttachLiveThread / relay 常驻只是 L0–L2 的局部补丁，不能当成
> 连续性已完成。

## 0. 一句话

CordCode 是用户官方 Mac Codex 工作流的 iOS 延伸。官方 `codex app-server` daemon 是唯一
运行时；MacBridge / go-bridge 是第二连接和观察者，不是 runtime 主人。产品要过的不是
「曾经连上过同一 socket」，而是：

1. Link 退出或重启时，daemon 和 Desktop 的 websocket 附着都还在；
2. Link 回来后，iPhone **立刻**继续旁观当前打开的 session，不必切走再切回；
3. Desktop 上的长回合在 iPhone 上有唯一权威终态，不会正文到了还停在「执行中」；
4. 正在跑的长任务不会因为 Link 进出而被切到 Desktop 私有 stdio，更不能指望 stdio 热接那一轮。

这四条必须同时成立。只修其中一条，其余会在下一次真机里换一张皮再出现。

## 1. 产品不变量

下列条件缺一即停止产品扩面。它们是同一合同，不是可选菜单。

| ID | 不变量 | 反例（禁止当解决方案） |
|---|---|---|
| C1 | 唯一 runtime 是官方 local daemon（control socket + `daemon start` alreadyRunning） | managed-loopback、每 session stdio、抢 Desktop 子进程 |
| C2 | MacBridge 不拥有 daemon。退出 / 停后台 / 杀 runtime **不得** `daemon stop`、不得杀掉 Desktop | 把 daemon 当 go-bridge 子进程；退出 Link 当日常「重启 Desktop」 |
| C3 | Desktop 一旦 `daemon version` 失败会锁死 `kind=stdio`，进程内不可远程翻回 | 用 kill/steal stdio 子进程、SQLite 假同步、第二 app-server 继续工作 |
| C4 | 活跃 thread 的 writer 在 daemon 进程内。第二连接只能观察或在无 writer 时 resume | iOS 对 Desktop 正在写的 thread 抢锁；stdio 热接 daemon 上的 in-flight turn |
| C5 | iPhone `isGenerating` 只认投影 `execution.phase`。正文到齐不等于回合收口 | timer、输入框、Stop 本地态、字数/静默时间猜完成 |
| C6 | iPhone 正在看的 session，在 **新的 go-bridge 进程** 上必须从 t=0 就有观察范围和 observer attach | 等 20s 续约；靠切 session 当重连修复；hello 成功冒充已在听 |
| C7 | 长任务的执行点在 daemon，不在 Link。Link 进出不得中断 daemon 上的 turn | 把「列表闪一下」当成服务死了；用重启 Desktop 续跑 |

C3 是官方 Desktop 宿主事实，本产品改不了 asar。合同只能是：**永不制造 Desktop 的探测失败窗口**；已经锁死的 Desktop 只能完整退出一次，且这不是退出 Link 的正常步骤。

## 2. 官方 Desktop 不可改的宿主事实

当前 ChatGPT Desktop（`app.asar` 只读核验，bundle `26.818.31338` 一代）在本地、
`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`、无 `FORCE_CLI` / `CODEX_CLI_PATH` 覆盖时：

1. 每次 `transport.connect()`（含 reconnect）都再跑 `codex app-server daemon version`，
   spawn timeout **2500ms**。
2. control socket 不在时该命令通常 **立刻失败**，不会等满 2.5s。
3. 失败就把 `kind` 写成 `stdio`，`supportsReconnect()` 变为 false。这个 Desktop 进程
   没有远程翻回 websocket 的 API。
4. 首次 reconnect 大约在 websocket 断开后 **1s**（`reconnectDelayMs` 初值 1000）。
5. Desktop 自己的版本门是 `appServerVersion >= 0.141.0`，**不是** `codex --version`
   字符串全等。本机曾出现 Desktop `0.149.0-alpha.4.1` vs standalone `0.149.0-alpha.4`，
   官方探测过得去，exact CLI 门却把座位安装整段 fail-closed 掉。
6. 私有 stdio 是另一个 app-server 进程。它不能接管 daemon 上已持有 writer 的 in-flight
   turn（`-32600 already has an active writer`）。官方也没有 stdio ↔ daemon 热迁移。
7. 空闲 thread 内存卸载延迟 30 分钟，那是卸载计时，不是崩溃恢复，也不是跨进程续跑。

推论：对 Desktop 来说，**探测失败 = 永久私有 runtime**，直到用户完整退出 App。
CordCode 能做的只有让探测不要失败，以及在已经失败时诚实告诉用户「请退出 Desktop」，
而不是再造第二服务。

## 3. 失败地图：表面现象是一层，根是合同缺口

owner 真机看到的不是六个独立 bug。它们是 C1–C7 某一层缺失时的不同皮肤。

| 表面现象 | 当时容易误判 | 实际缺失层 | 活体要点 |
|---|---|---|---|
| 先重启 Link 再重启 Desktop，Mac→iOS 能跟 | 拓扑已完成 | 只证明冷启动附着，不证明 Link 进出连续性 | 两边都在 daemon 上，观察面也挂上了 |
| 退出 Link（含后台），Desktop 列表闪一下 | 共享服务被带走 | **L1 误报**。列表刷新 ≠ daemon 死 | daemon `setsid`、ppid=1；Link 退出路径不停 daemon；Desktop 仍有 UDS 打在同一 control socket |
| 再打开 Link，Mac 发、iOS 不同步；切 session 再切回才好 | Desktop 掉到 stdio | **L2 观察连续性** | 新 go-bridge 进程观察范围为空白；`resubscribeObservationSessions` 无 scope 可绑；`AttachLiveThread` 只挂在 `set_observation_scope` 上；iOS 续约最多 20s 且捕获旧 client |
| 长故事正文到了仍「执行中」；停止无效；再发短的才收口 | iOS 渲染/停止坏了 | **L3 执行连续性** | `isGenerating` 只跟 `execution.phase`；观察通道 `chan` 容量 256，满则丢后续事件，完成帧可被挤掉；Stop 本地 aborted 会被仍为 running 的投影盖回去 |
| 长任务中途退 Link，担心任务中断、切私有后续不上 | daemon 是 Link 的孩子 | **C2+C4** | 执行点在 daemon。Link 走了只少一个观察者。stdio **不能**热接 in-flight turn |
| 发消息时 CordCode Link CPU 36% | runtime 吃流把机器拖死 | **观测面**，不是拓扑失败 | 同屏 `cordcode-bridge-runtime` 仅 3%；热的是 Swift GUI。3s 管理面轮询解释不了爆发。需 Instruments，禁止先改 daemon 生命周期「优化 CPU」 |

禁止把上表每一行开成一个互不相关的 PR。修 L2 却不处理 L3，长回合仍会卡执行中；修 L3 队列却不处理 L2，重启 Link 后根本收不到完成帧。

## 4. 目标架构：四层连续性

四层串行。下层失败时上层必须诚实暴露，不得用更上层的 UI 技巧补。

```text
L0  登录级 daemon 座位
    LaunchAgent KeepAlive 只跑幂等 `daemon start` + attach env
    补位必须短于 Desktop 首次 reconnect（约 1s）
            │
L1  Desktop 附着
    登录域 `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`
    永不制造 ≥1s 的 socket 空窗；CLI patch 偏差不得挡住座位
    已锁 stdio 的 Desktop = 异常恢复（完整退出），不是退出 Link 的步骤
            │
L2  观察连续性（iOS 正在看的 session）
    观察范围的权威在「当前打开的 session」，必须能在新 go-bridge 进程
    的第一条业务路径上重建：Subscribe + observer `thread/resume`
    hello/重连成功 ≠ 已在听
            │
L3  执行连续性（投影相位）
    观察路径上的 `turn/started` / delta / `turn/completed` 不得因队列满丢失
    iPhone 执行态 = Kernel `execution.phase`
    外部回合的 Stop 要么官方中止成功并投影 idle，要么失败可见，不得假收口
```

MacBridge 在这张图里只出现在 L0 的「探测/补位」和 L2/L3 的「第二连接」。它不是 L0 的进程主人，也不是 L3 的真相源。

### 4.1 L0 座位

已部分落地，仍属本层合同而不是「已完成可遗忘」：

- 用户 LaunchAgent `org.openagi.cordcode.codex-app-server-daemon`
- 脚本只循环官方 `daemon start`（alreadyRunning 不换 PID）+ `launchctl setenv`
- **不得** `daemon stop` / `daemon restart`（restart 是 ccswitch 的显式杠杆，见 §5）
- KeepAlive + 亚秒级补位，因为 Desktop reconnect 约 1s，60s 周期盖不住
- 只对真实用户 home 写 plist，测试假 home 不得安装
- Link `shutdownForExit` / `stop` 只停 runtime 与托管 OpenCode，不动座位、不动 daemon、不动 Desktop

官方 daemon 用 `setsid` 脱离父进程。bootout 座位 bash **不应**带走 daemon；若未来发现 process group 仍能杀掉 daemon，那是 L0 缺陷，修座位隔离，而不是让 Link 拥有 daemon。

### 4.2 L1 Desktop 附着

- 附着入口只有官方 local daemon UDS，禁止再引入 loopback URL 产品路径
- Desktop 是否附着由 **它自己的** `daemon version` + `appServerVersion >= 0.141.0` 决定
- CordCode 的 exact `codex --version` 字符串门已经证明有害：挡住座位，又挡不住 Desktop 自己去附着或锁 stdio
- 退出 Link 时 Desktop 列表闪一下，应视为「少了一个 client 的队列通知」，直到活体证明 daemon PID 变了或 Desktop 出现私有 `codex app-server` 子进程（且父进程是 ChatGPT，不是 `cordcode-bridge-runtime`）
- 旧 `codex` driver 在 runtime 下拉起的 stdio `codex app-server` **不是** Desktop 私有服务，取证时必须看 PPID

### 4.3 L2 观察连续性

这是当前真机「重启 Link 后不同步、切 session 才好」的合同缺口。

今天的断裂点：

1. 观察范围活在 **go-bridge 进程内存**。进程一死，范围清空。
2. `registerConnection` / `resubscribeObservationSessions` 只能从还活着的 scope 重绑。新进程上 scope 为空，重绑是空操作。
3. `AttachLiveThread`（observer `thread/resume`）只挂在 `set_observation_scope` 处理函数上。没有 scope RPC，observer 不会去 resume 当前 thread。官方边界：订阅前的 turn 事件不重放。
4. iOS 续约循环最多 20s 一次，并且 `Task` 捕获了创建时的 `bridgeClient`。Link 重启后这条循环既可能还在睡，也可能对着已失效的 client 静默失败，然后一直等到用户切 session 才 `setObservationForeground`。

目标形态（必须整体设计，不要只改 20s 为 1s）：

- **权威**：iPhone 当前打开的 `(backendId, sessionId)` 是观察意图的唯一来源。
- **t=0**：新 go-bridge 进程上，hello 成功后的第一条与该 session 有关的路径（显式 scope RPC 或等价的「当前可见 session」声明）必须同时做到：
  - device observation scope 非空；
  - broadcaster Subscribe 当前 connection；
  - observer 连接对该 thread `resume`（无 writer 冲突的只读订阅）；
  - 之后 Desktop 的 `turn/started` / delta / `turn/completed` 才能进 Kernel。
- **hello 成功、Relay 已连、列表能拉，一律不算「正在旁观」。**
- 切走再切回可以当作回归对照，**不得**当作正常重连步骤写进验收。

冷校准（thread/read、projection hydrate）补的是缺口历史，不能替代 live 观察。没有 L2，L3 的完成帧根本不会到。

### 4.4 L3 执行连续性

iPhone 输入框「执行中」= `projection.execution.phase.isExecuting`。SSV2 不允许用本地 Stop、静默时间或「正文看起来写完了」收口。

今天的断裂点：

1. 观察/session listener 通道容量 256，满时 `dispatchEvent` **直接丢事件**。一千字故事的 delta 可以挤掉 `turn/completed`。表现：正文几乎都在，相位仍 running。
2. 下一条短消息事件少，完成帧能进 Kernel，相位 idle，于是「再发一条才好」。
3. Stop 把本地态打成 `aborted`；若投影仍 running，`applyPresentationChangeSet` 会再次把 `isGenerating` 拉回 true。对外部 Desktop 回合，iPhone 本来也不是 writer。

目标形态：

- 观察路径上的终态帧是 **不可丢的**。实现可以是有界队列 + 终态优先、或阻塞背压、或独立完成通道；不允许「delta 挤掉 completed」作为可接受行为。
- Kernel 必须能从官方 `turn/completed`（以及官方失败/中断语义）落到 `execution.phase=idle|failed`，且 iPhone 只跟这一相位。
- 外部回合点 Stop：要么官方中止成功且投影 idle，要么明确「无法中止另一端的回合」。禁止假完成。
- 禁止用「再发一条短消息」当收口手段，那是当前缺陷的用户绕过，不是设计。

## 5. 明确不做什么

1. 不改官方 Desktop asar，不注入、不 hook、不杀它的 stdio 子进程来「翻回」websocket。
2. 不把「退出 Link 后重启 Desktop」写成正常工作流。
3. 不为了 ccswitch 而停掉登录 daemon。ccswitch 写的是 `~/.codex/config.toml`；跑着的 daemon 持有内存配置。正确杠杆是用户显式 `codex app-server daemon restart`，且必须快于 Desktop 的 1s reconnect 探测。restart 本身会制造空窗，属于 L1 风险，要单独设计，不得顺便写进座位脚本。
4. 不恢复 `managed-loopback-ws` 或第二 app-server 来回避 stdio 锁。
5. 不用 history / raw event / 字数比较补洞（SSV2 第 4 条）。
6. 不把 CordCode Link 的 CPU 爆发当成改 daemon 生命周期的理由。先证明是 Swift GUI、runtime 还是座位脚本。
7. 不把旧 `codex` driver 的 stdio 子进程当成 Desktop 私有服务。
8. 不在 L2 未重建时用冷投影冒充「已经在 live 旁观」。

## 6. 施工切面（按层，禁止按 bug 开 PR）

后续 agent 只允许按层提交。每一层必须带：不变量、唯一 writer/观察者、失败怎么暴露、定向测试、owner 矩阵行。层与层可以并行 **设计**，但 L2 的产品验收依赖 L0/L1 仍成立。

### 6.1 L0/L1 收口（座位 + 附着）

目标：Link 退出/重启不是 Desktop 锁 stdio 的原因；座位不被 CLI patch 偏差挡住。

必须留下的证据：

- Link `stop` / `shutdownForExit` 源码路径无 `daemon stop`、无座位 bootout（安装时的 bootout 只替换 LaunchAgent 作业，不得杀死 daemon）
- 退出 Link 后 daemon PID 不变、`daemon version` 仍 running、Desktop 若原先 websocket 则仍无 ChatGPT 子进程 `codex app-server`
- Desktop 列表闪一下可以发生，但 **不得**伴随 daemon PID 更换或 Desktop 私有 stdio 子进程

未完成项（设计时就要写进这一层，不要散落）：

- ccswitch → 官方 `daemon restart` 的产品入口与空窗预算
- 座位 process group 与 daemon `setsid` 的隔离测试
- 已锁 stdio 的诚实文案（现有 ownership 错误已部分覆盖）

### 6.2 L2 收口（观察在新进程上的 t=0）

目标：iPhone 开着 session 时重启 Link，重连成功后 **不必切 session**，Mac 再发就能进执行态并流式。

必须整体处理的三个点，缺一不算 L2 完成：

1. iOS 在 `bridgeDidConnect` / `bridgeTransportDidReconnect` 时立即按 **当前** `currentSessionId` 声明 observation（新 client，不得用已捕获的旧 client）。
2. 新 go-bridge 在第一条 scope（或等价声明）上 Subscribe + `AttachLiveThread`；`resubscribeObservationSessions` 在 scope 为空时不得假装成功。
3. observer 对已 loaded 的当前 thread resume；与 Desktop 同时订阅不抢 writer。

定向测试至少覆盖：go-bridge 进程重启、observation 内存为空、iOS 重连、当前 session 未 switch、随后外部 `turn/started` 能进 Kernel。禁止只测「切 session 之后」。

### 6.3 L3 收口（完成帧不可丢）

目标：Desktop 长回合结束，iPhone 相位变为 idle；Stop 对外部回合诚实。

必须整体处理：

1. 观察路径终态不可被 delta 挤掉（容量、背压或终态优先，选一种写进实现合同并测试百万级 delta 仍能 completed）。
2. Kernel 对 observer 的 `turn/completed` / failed / interrupted 落到 `execution.phase`。
3. iPhone Stop：writer 是 Desktop 时失败可见；投影 running 时不得被本地 aborted 骗成完成，也不得在 Stop 后被 running 投影无说明地拉回执行中而不告知「另一端仍在跑或投影未收口」。

定向测试：合成超过队列容量的 delta + 最后一帧 completed，投影必须 idle。禁止用「再发短消息」当测试通过条件。

### 6.4 CPU（独立观测，不阻塞 L0–L3 合同）

CordCode Link 36% vs runtime 3% 说明热路径在 Swift GUI。本层只要求：

- 用 Instruments / `sample` 在「Desktop 长流式」时采 CordCode Link，指出主线程栈
- 未采样前禁止改座位间隔、禁止停 daemon、禁止把 CPU 当 L0 回归理由

## 7. owner 验收矩阵

后续任何「连续性完成」报告必须一次性给出下表，不得跑完一行再要下一行。owner 只回报步骤号 + ✅/❌ + 现象。

| # | 前提 | 动作 | 应看到 |
|---|---|---|---|
| 1 | Desktop 已附着共享 daemon（无 ChatGPT 子进程 `codex app-server`） | 只退出 Link（含后台），不要动 Desktop | 列表最多闪一下；daemon PID 不变；Desktop 长任务若在跑应继续出字 |
| 2 | 仍是 1 | 再打开 Link，iPhone **不要**切 session | 重连成功后，Mac 再发，iPhone 立刻执行中 + 流式，不必切走切回 |
| 3 | 两边都在 daemon | Mac 发「讲个故事 1000 字」，等 Desktop 完成 | iPhone 正文跟上后输入框变为完成态；点停止若回合已结束应保持完成，不得假执行中 |
| 4 | 3 刚结束 | 不要再靠「发一条短的」救状态；若已完成则直接停在完成态 | 后续 Mac 短消息正常开跑、正常收口 |
| 5 | iPhone 开着该 session | 重启 Link，Mac 在重连成功后发 | 与 #2 相同；切 session 只作为对照，不是必做步骤 |
| 6 | 对照：任务中途 **完整退出 Codex App** | 再打开 Desktop | 允许换 runtime；正在飞的那一轮接不上，只能看到已写下的历史。这不是退出 Link 的对照失败 |

#1 失败才去查 L0/L1。#2/#5 失败是 L2。#3/#4 失败是 L3。#6 是官方宿主边界，用来防止把「退出 Desktop」误写成产品主路径。

## 8. 给后续 agent 的阅读顺序与禁令

阅读顺序：

1. 本文全文（合同）
2. v2.0 设计 §0 与 §6.1（拓扑，不可退回 managed-loopback）
3. `think.md` 2026-08-22 Desktop stdio 锁与 CLI patch 偏差
4. 再读源码：`RuntimeManager.swift` 座位、`handlers.go` `set_observation_scope` / `resubscribeObservationSessions`、`agent/codex-web/events.go` 观察通道与 `AttachLiveThread`、iOS `setObservationForeground` / `applyPresentationChangeSet`

禁令：

- 不得只修 iOS 或只修 Mac 来掩盖另一侧缺口。L2 是双仓：iOS 立刻声明当前 session，Mac 新进程立刻 Subscribe+attach。
- 不得把切 session、再发短消息、重启 Desktop 写进正常用户步骤。
- 不得在未跑 §7 矩阵时声称「双向 live 已完成」。自动拓扑脚本 PASS 仍然不够。
- 发现新现象时，先在 §3 表里归层；归不进四层，先改本文，再改代码。

## 9. 与已落地补丁的关系

下列改动是本架构的局部实现，不是完成证明：

- `codex-web` 列入 relay 跨回合常驻、`set_observation_scope` 调 `AttachLiveThread`
- 登录 LaunchAgent 座位、0.25s KeepAlive 循环
- 取消 exact CLI 版本 fail-closed，避免挡住座位
- dsh-web `--no-open`
- ownership 文案改为要求完整退出 Desktop，而不是抢锁

它们可以保留，但必须被 §4 四层重新解释。若与本文冲突，改代码或改本文，禁止再叠第四个特例。
