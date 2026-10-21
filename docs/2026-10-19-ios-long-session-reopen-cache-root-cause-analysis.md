# iOS 长 session 二次打开全量重拉与缓存未复用：独立根因分析

日期：2026-10-19（Asia/Taipei）

范围：方案 v30 的 S-4 restore-then-increment 暖开链路。只做分析，不修改业务代码，不构建、不安装、不重启 App，不运行 UI/snapshot tests 或设备 UI automation。

## 结论

1. **直接阻断暖路径的是生产客户端缺少协议 conformance。** `CCCodeBridgeBackendClient` 实现了 anchor 方法，却没有声明遵循 `SessionProjectionAnchorNewerFetching`。动态协议转换先失败，capability 查询根本没有执行；日志统一写成 `anchor-echo-off`，不能据此认定 hello_ack 回显丢失。
2. **切走删除 ProjectionStore/replica 内存态属实，并且是 v30 明确保留的设计。** “切走十秒再回来直接复用内存”没有被这个设计实现；修改该策略需要独立设计论证，不能简单删除 clear。
3. **本次 10–30 秒等待的主要证据指向重复全量网络传输。** Mac enqueue 到 iPhone 收到完整大帧约 27 秒；没有证据支持把它归因于“20MB 固态写入需要几秒”。本轮 Mac 组件基准中，22.9MB 编码约 75–76ms、解码约 140–175ms、原子写约 43–45ms；这些不是 iPhone 实测成绩。
4. **79c2e394 的磁盘实际字节预算修复有效，但有边界缺口。** 真实样本从 48 行裁成 8 行、3,579,781B；巨型尾轮可能导致不保存、全量替换可能重复累计淘汰数，内存窗口仍使用粗估。
5. **修好 conformance 后，正常播种→磁盘恢复→anchor 对账的主要组件存在，但不能判定完整交付。** 还存在事务代际/epoch 隔离、重试、分页上限、复查集容量和 UI 标记接线缺口。本轮没有生产暖路径成功证据。

## 1. P0 来源清单

源码分析前、首次文档编辑前均重新执行分支、完整提交和工作区状态检查。两次结果一致。

| 字段 | Mac 仓 | iOS 仓 |
| --- | --- | --- |
| 仓库路径 | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` |
| 分支 | `feat/claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` |
| 完整提交 | `966c221e22c7550cf487e039072ef43f4879560c` | `79c2e39450bbb4d061a5f55f5adeb54702b1bb9d` |
| 分析前未提交状态 | 干净 | 干净 |
| 首次文档编辑前未提交状态 | 干净 | 干净 |
| 任务预期分支/工作树 | 本表 Mac 来源 | 本表 iOS 来源 |
| 配套来源 | 本表 iOS 路径、分支、完整提交 | 本表 Mac 路径、分支、完整提交 |
| 预期产品特性 | Claude SSV2、anchor-newer 协商与对账、full 播种 token | S-4 基底恢复、anchor-newer 消费、schema v2 实际字节预算 |

行号冻结于本表提交；下文 `iOS:`、`Mac:` 分别指本表工作树中的仓库相对路径。保存报告的后续文档提交不改变这些源码证据的基线。

背景文档：`Mac:docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，v30。

旧磁盘估算实现另读取了指定 iOS 提交的父提交 `17f336a598ab9fff8804595e73be9fd04a0cda18` 的 Git blob，未切换工作树。该来源只用于比较旧估算算法，不用于描述 79c2e394 的生产实现。

本题的能力协商、基底缓存和恢复事务属于 CordCode 私有 bridge/SSV2 语义，不由 Claude CLI 拥有。核验范围为双仓 transport、协议模型、store、replica、打开入口、Mac handler、现有定向测试及 v30；不以无关上游实现推断私有协议。

## 2. 证据范围与真实性分级

### 2.1 原始日志与文件

- 最新真机日志：`/tmp/fgtrace-pull/fg-trace3.log`。
- 较早复测日志入口：`/tmp/fgtrace-pull/fg-trace2.log`；本报告主要时间线使用 trace3，不把不同轮次拼成一次打开。
- Mac 日志：`~/Library/Application Support/CordCode Link/logs/go-bridge.log`。
- 原始基底：`/tmp/basecache-pull/CordCodeBaseCache/f45d018fc2a1c0292d3e05c8e1fc7fc1.json`。
- 独立 benchmark：`/tmp/cordcode-projection-analysis/bench.swift`。
- 两轮结果：`/tmp/cordcode-projection-analysis/bench.out`、`bench2.out`。

真机 fg-trace 使用 UTC；本报告本地时间为 UTC+8。NSLog/syslog 中缺少 `[ProjStore]` 不作为任何否定结论的依据。

上述 `/tmp` 文件属于本机临时证据入口，未随报告归档到 Git；文件不存在时不能声称可直接重跑。本报告保留了来源、关键读数、源码锚点与命令，但不是完整原始证据包。

### 2.2 结论等级

- **源码事实**：由冻结来源的声明、控制流或全仓调用点检索证明。
- **运行态事实**：由本次引用的真机/Mac 日志证明；日志本身不提供完整 iOS 二进制提交身份证明，不能据此把所有源码缺口都判为真机已发生。
- **组件实验证据**：真实缓存加生产 Codable 类型/磁盘 store 的隔离脚本结果；不能冒充 iPhone 或生产端到端验证。
- **待验证影响**：源码暴露的竞态、异常分支或性能目标，尚未通过对应真机/时序实验复现。

## 3. B：anchor-echo-off 的确切根因

### 3.1 生产类型转换先失败，capability 查询被短路

完整判定链：

1. `iOS:OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift:884` 把真实 `CCCodeBridgeBackendClient` 传给 `projectionStore.setFetcher`。
2. `iOS:OpenCodeiOS/OpenCodeiOS/Models/ProjectionRestoreThenIncrement.swift:40–41` 先做 `fetcher as? any SessionProjectionAnchorNewerFetching`，成功后才读取 `await fetcher.supportsProjectionWindowAnchorNewer`。
3. `iOS:OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift:34` 的类声明没有 `SessionProjectionAnchorNewerFetching`，也没有 `SessionProjectionWindowFetching`。全仓 Swift 检索未发现补充这两个 conformance 的 extension。
4. 该类虽在 `:1319` 实现 supports 属性、在 `:1347` 实现 anchor fetch 方法，Swift 不会因为成员形状相同而隐式遵循协议。
5. 因而第一项动态转换失败，第二项 capability 查询不执行；`ProjectionRestoreThenIncrement.swift:46` 输出 `reason=anchor-echo-off`，`:52` 调用 `pullFull`。

**判定：B 的日志属实，但“transport 查询返回 false”不是该路径的正确解释。日志合并了协议转换失败与 capability 未回显两个不同原因。**

此外，缺失父协议 `SessionProjectionWindowFetching` 也使旧 window 路径的转换失败，锚点为 `ProjectionStore.swift:778`。声明遵循 anchor 协议会同时要求其父协议契约；本轮没有实施任何修复。

### 3.2 hello 声明、ack 字段和清空/回填时机

| 环节 | 锚点 | 核验结果 |
| --- | --- | --- |
| iOS 声明 | `iOS:OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift:190–201` | anchor/scalar 在 `sessionSyncV2Enabled` 条件块内声明；模式默认 active（`:159`）。 |
| Mac 回显 | `Mac:go-bridge/server.go:512–518` | 声明及发布门满足时写入 ack map，并登记连接能力。 |
| Mac 发送/日志 | `Mac:go-bridge/server.go:998–1003` | 发送 ack 后记录 echoedCapabilities。20:03:06.189 日志确实包含 anchor/scalar。 |
| wire 字段 | `Mac:go-bridge/hello_handler.go:44–48`；`iOS:OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeModels.swift:154–163` | 两侧均使用 `capabilities`，Go map bool 对应 Swift `[String: Bool]?`。 |
| iOS 回填 | `iOS:OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift:629–659` | 成功解码和 ok 校验后，`:655` 填字典，`:656` 设 registered，再发布 connected。 |
| 查询 | 同文件 `:779–780` | `isRegistered && acknowledgedCapabilities[capability] == true`。本次打开 guard 未走到这里。 |
| receiveLoop 退出 | 同文件 `:1668–1682` | 清注册态、清字典；新握手成功重新经过 `:655` 回填。属于合理连接代际失效边界。 |

trace3 在 12:03:06Z 记录 connected，随后 12:03:48Z、12:03:56Z、12:03:58Z 的打开均输出 anchor-echo-off，见日志 `:6000`、`:6096`、`:6249`、`:6369`。

没有原始 ack 帧或 iOS 字典转储，因此不额外宣称“已直接观察客户端字典中的 true”。缺失 conformance 已足以确定解释现有控制流，不需要假设 ack 回填故障。

### 3.3 为什么现有单测未覆盖

`iOS:OpenCodeiOS/OpenCodeiOSTests/ProjectionBaseCacheTests.swift:141` 的 `testOpenWithBaseCacheReconciles` 在 `:155` 注入 fake。

`FakeAnchorFetcher` 在 `:276` 明确遵循 `SessionProjectionAnchorNewerFetching`，`:284–285` 直接返回两个 supports=true。测试覆盖 store 的理想输入路径，绕过生产客户端声明、transport 及 hello，所以不会暴露这次生产接线错误。

后续需要覆盖的测试边界是：真实生产类型的协议可转换性，以及当前连接 hello echo 的 true/false/重连失效与回填。不能仅增加更多直接返回 true 的 fake-store 测试。

## 4. A：内存清态与 v30 的设计符合性

### 4.1 已证明的清理行为

- `iOS:OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift:228–232`：离开 session 时停止 pull loop，调用 clear。
- `iOS:OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:376–385`：删除整个 State，取消在途 pull，删除 pending presentation，再调用 replica.clear。
- `iOS:OpenCodeiOS/OpenCodeiOS/Models/ProjectionReplica.swift:390–391`：删除 replica 中该 session 的 State。

因此切回来时，ProjectionStore/replica 已无可直接恢复的那份投影。其他呈现缓存即使仍持有派生数据，也没有参与 S-4 的基底打开路径。

### 4.2 v30 明文要求磁盘重开

`Mac:docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`：

- `:675`：内存加速限定为活跃期 patch 与前台恢复，切回必经磁盘反序列化。
- `:685–687`：BaseCacheHit 是重开唯一常规入口，内存态不参与打开状态机。
- `:718`：不得为打开路径改变 switchSession.clear 语义。
- `:727`：切走 clear 保留，切回走磁盘 BaseCacheHit。
- `:1057`：有效水位线重开的权威完成目标 ≤1 秒。

**判定：“内存不复用”是 v30 设计内行为，并非实施者漏接了方案已经要求的内存重开路径。**

### 4.3 架构与 UX 评估

有界磁盘恢复可以提供合理体验，前提是迅速显示基底、显式标识同步，并完成有界对账。十秒内切回仍等待 10–30 秒，不能作为该设计的正常体验解释。

同进程最近会话使用有界内存 LRU，是更直接的快速切换方案，磁盘承担进程退出后的恢复。但这属于对 v30 的设计调整。本轮仅评估，不实施。

更小的失效边界可以是：

- 保留按 backend/bridge/session 三元组隔离的不可变基底、token、provenance；
- 失效当前观察权、在途请求代际、恢复门、待回放事件和 ready 资格；
- 重开仍经唯一 replica writer 恢复及对账，不把缓存执行态当作已确认状态。

不能仅删除 clear 或仅保留整个现有 State：

- `ProjectionStore.swift:337–338` 的 isActive 实际等价于 ready；
- `ChatViewModel.swift:425–434` 的 sessionSyncV2Active 已由当前 session、backend capability 和 mode 决定，并非由缓存存在决定；旧注释不能证明只有删除全部内容才能防泄漏；
- `ProjectionStore.swift:265–281` 在重新观察时会从 replica 自动构造 ready State，保留 replica 后必须重新审计此入口；
- 现有 State 表以 sessionId 索引，不能把它直接当作跨 backend/bridge 的三元组缓存；
- 22.8MB JSON 不等于 22.8MB 解码后堆内存，保留策略需要容量限制和内存压力淘汰。

## 5. 真实成本测量与等待时间归因

### 5.1 组件 benchmark 方法

环境：Apple M4，arm64 macOS，Swift 6.4，`xcrun swift` 解释执行模式。未构建 iOS 或 Mac App。

脚本从冻结 iOS 来源原样提取真实 Codable 声明及生产 `ProjectionBaseCacheStore`，输入为原始缓存文件；临时写入限定在 `/tmp/cordcode-projection-analysis/cache`。没有使用简化 JSON 替代真实载荷，也没有向生产路径注入数据。

两轮各七次，计时区间不含脚本启动/编译准备时间。atomic write 项复用生产的 `.atomic` 写临时文件再 replace/move 过程；没有显式 fsync。

| 操作 | 22,894,080B / 48 行中位数 | 3,579,781B / 8 行中位数 |
| --- | ---: | ---: |
| typed JSONDecoder | 140–175ms | 24–26ms |
| JSONEncoder | 75–76ms | 12–13ms |
| Data 读取，OS 缓存命中 | 1.8–2.1ms | 0.1–0.3ms |
| atomic write + replace，无 fsync | 43–45ms | 4–6ms |

直接调用生产 `save(full)`，包含完整编码、比例裁剪、重编码和落盘，五次约 90–99ms。生产 `load` 有界文件约 24–25ms。

这些数值是 **Mac 组件实测，不是 iPhone 成绩**。不能按未经验证的倍率推算 iPhone。原子写返回也不能当成物理 NAND 已持久化；生产代码同样没有显式 fsync。

### 5.2 真机运行态时间线

第二次打开长 session：

| 本地时间 | 证据 |
| --- | --- |
| 20:03:58.586 | Mac projection_rpc mac_receive，全量请求。 |
| 20:03:58.649 | Mac projection_rpc response_enqueue，snapshot，headRev=5549。 |
| 20:04:13 | 真机 15 秒 open 硬帽超时，保留在途 RPC；trace3 `:6398–6399`。 |
| 20:04:26 | 真机完整帧进入 WS-RAW，22,790,672B；trace3 `:6409`。 |
| 20:04:27 | RPC ok、replica commit、FULL late=1、render/PIPE 提交；trace3 `:6445–6450`。 |

首次打开则在 20:03:49.582 enqueue，20:03:52 出现完整大帧，20:03:53 apply/呈现提交，见 trace3 `:6151–6162`。

**事实：第二次的主要等待发生于 Mac enqueue 至真机完整帧到达之间，约 27 秒。** 秒级日志不能拆出精确 iPhone 解码、restore、写盘或实际屏幕上屏时间，但不支持将这段时间归因于写盘。

### 5.3 iPhone 与 4MiB 暖开的诚实口径

本轮没有 iPhone 编码/写盘专用计时，不能给出设备实测毫秒值。已有日志也不足以把接收后的处理拆分成各阶段。

合理验收目标是：基底首屏争取亚秒级，良好网络且小响应时，对账完成采用方案 ≤1 秒目标；这不是本轮已经通过的真机成绩。

不能把所有暖请求都当作 KB 级：anchorRefresh 首包完整返回末轮，允许很大的 never-split turn；服务端还会对整个投影计算编码尺寸与 token（`Mac:go-bridge/projection_anchor_newer.go:157`、`:189–197`），缓存文件 ≤4MiB 不等于整个暖路径工作量也严格 ≤4MiB。

## 6. C：实际字节预算修复、收敛与账本

### 6.1 文件与漏算原因

原文件核验：22,894,080B、48 rows、schemaVersion=1、evictedOlderCount=0，session 前缀 7f389d08，身份匹配请求中的 Claude/bridge/session，48 行均有 token。

按旧 roughRowSize 原算法计算仅 2,094,739B，低于 4MiB，故不裁剪。

“旧算法对 reasoning 失明”的描述需要更正。父提交 `17f336a598ab9fff8804595e73be9fd04a0cda18` 的 `ProjectionBaseCacheStore.swift:161–174` 直接统计 `part.text`，没有 type==text 条件；reasoning 使用 text，因此会被计入。

真实样本部件的生产编码量约为：

| 部件类别 | 编码量 |
| --- | ---: |
| subagent | 13,121,460B |
| tool | 7,749,867B |
| reasoning | 1,876,637B |
| text | 130,848B |

这些是顶层部件按类别分别编码的统计，不应与完整快照字节数直接等同。主要漏算来自工具结构和递归 subagent 内容。

### 6.2 新实现实际有效

`iOS:OpenCodeiOS/OpenCodeiOS/Models/ProjectionBaseCacheStore.swift:123–160` 先限制行数，再编码，按实际 data.count 裁剪并重编码。

真实样本实验：

```text
48 rows / 22,894,080B
→ 删除最旧 40 rows
→ 8 rows / 3,579,781B / evictedOlderCount=40
```

只要实际完成新文件写入，字节判断不会被工具或子代理载荷绕过。

### 6.3 边界与不足

| 项目 | 判定与锚点 |
| --- | --- |
| 收敛性 | 每轮至少删除一行，有限步终止；行数先收敛至最多 256。不能把本样本一轮裁剪推广为所有分布都一轮。`ProjectionBaseCacheStore.swift:125–144`。 |
| 多删 | 比例算法不保证最大尾部窗口。本样本最大可容纳最后 10 行，共 3,716,846B；实际只保留 8 行。组件实验已复现。 |
| 巨型尾轮 | 最后一个 turn 自身超预算时可删空，`:145` 直接返回，既不新写也不删除旧文件。使用真实数据前 36 行、巨型尾轮的实验得到 oldFileUnchanged=true；没有既有文件时则不能播种。 |
| 单次账本 | 一致输入下，每删除一行累加一次，正确。`ProjectionBaseCacheStore.swift:127`、`:141`。 |
| full 后重复记账 | 恢复后已有 evicted=40，转 full 时 commit 保留 baseCache（`ProjectionStore.swift:1337–1342`），flush 带旧计数（`ProjectionRestoreThenIncrement.swift:422`），磁盘再删 40 得 80。模拟这一输入的组件实验为 rows=8、evicted=80、actualMissing=40。真实异常分支尚未真机复现。 |
| schema v2 | `load:103–109` 先读完整文件并解码，再检查版本；v1 被拒绝但不在该分支删除。首次仍支付旧大文件解码成本，成功保存后才替换。 |
| 编码成本 | 新 save 仍首先完整编码 22.9MB，再裁剪重编码。不能宣称完整编码成本已消除。 |

异步 flush 也没有变成写入完成屏障：`ProjectionRestoreThenIncrement.swift:432` 仍启动独立 Task。保存方法在同一 actor 上同步执行，已排在 save 后的 load 会等待；不能把“磁盘写入未完成”一概解释成 load 必然 miss。调度先后仍需实验，不能据此次预算修改宣称所有播种时序竞态已消除。

### 6.4 新增测试不复现旧漏算

`iOS:OpenCodeiOS/OpenCodeiOSTests/ProjectionBaseCacheTests.swift:102–136` 使用四个大 reasoning 字符串。旧 roughRowSize 本来就统计这些 text，所以旧实现也会裁到预算内。

因此该测试检查新实现的预算结果，但不能作为本次工具/subagent 漏算的有效负对照。回归 fixture 应覆盖真实结构载荷、递归 subagent、超预算尾轮和 full 后账本重置等边界；本轮未修改或运行 App 测试。

## 7. 修好 B 后是否已形成完整暖路径

### 7.1 正常路径主要组件存在，尚无生产成功证据

当前样本尾轮很小，修复生产 conformance 后，已有组件可以形成：

```text
首次 full 成功
→ merge full turnTokens
→ flush / v2 文件裁剪为尾部 8 行
→ 切走 clear
→ 切回 load
→ replica restore + store commit
→ anchor-newer 对账
→ 采用最终 head / epoch
→ flush
```

对应锚点：

- full token 并入：`iOS:OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift:1142–1144`。
- full 成功统一播种，含 late：同文件 `:1194–1200`。
- load/restore/对账：`iOS:OpenCodeiOS/OpenCodeiOS/Models/ProjectionRestoreThenIncrement.swift:58–135`。
- 生产 RPC 适配：`iOS:OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeClient.swift:542–560`。
- Mac 能力门、水合、fence、响应：`Mac:go-bridge/projection_anchor_newer_handler.go:45–186`。

这是源码可达性评估，不是已经完成修复或生产验证。

### 7.2 隐藏断点

| 断点 | 源码事实 | 实际影响验证状态 |
| --- | --- | --- |
| 对账事务缺 generation/epoch 中止检查 | Restore `:178–233` 没有捕获并核对事务 generation/起始 epoch，也没有取消检查；`:192–193` 每页只读当前 epoch。普通 full pull 则在 Store `:1121` 校验 fetcherGeneration。 | 源码缺口已证明；跨重连混代结果的具体时序待实验。 |
| hydrating 重试实际上无界 | Restore `:215–221` 每轮 pages+=1 后又 -=1，持续 hydrating 时 pages<0 不可达；sleep 的取消错误被 try? 吞掉。 | 控制流已证明；未运行无限失败实验。 |
| IncrementFailed 没有接回增量重试 | Restore `:143–147` 保留 ready 基底并返回 failed；ChatViewModel `:456–461` 不为 ready 启动重试；`:785–792` 手动重试仍走 pullOpenWindowed。 | 接线路径已证明；未真机触发增量失败。 |
| 两页上限实现不符 | Restore `:189` 先应用页面，`:196` 检查 pages<=2，允许执行第三页；且统计所有 correction 页面，而非只统计 turns 页。 | 控制流已证明；需分页测试覆盖差异。 |
| 全集复查集可能超过 256 | Restore `:203–204` 把新交付 token 并入全集；Mac handler `:61–66` 拒绝超过 256 项。满 256 行基底加新增 turn 后再续页即可触及。 | 边界组合由代码推出；当前 8 行样本不触发，尚未做对应请求实验。 |
| 内存窗口仍粗估 | Restore `:461–485` 的 roughTurnSize 仍只加 part.text 和常数，没有完整工具/subagent 大小。 | 实现事实；磁盘修复不能证明内存窗口也 ≤4MiB。 |
| 同步与截断标记未接 UI | Restore `:449`、`:455` 查询函数全仓无生产消费者；`:117` 清空 window coverage。ChatUIKitContainerView `:3600–3609` 读旧 coverage，未读淘汰账本。 | 调用点缺口已证明；“正在同步”“更早 40 轮未加载”的产品展示未证明。 |
| 巨型尾轮与账本 | 见 §6.3。 | 组件输入实验已复现；未冒充生产异常已发生。 |

方案要求的 epoch 中途变化中止，见 `Mac:docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md:70` 的 F-55 定案：iOS 比较事务开始 epoch 与重连 hello_ack epoch，变化时整组放弃。缓存 provenance 旧 epoch 可恢复，与事务中途跨 epoch 是两个不同场景。

**综合判定：修好 B 后有望使当前样本的正常暖开路径可达，但不能据此宣称 S-4 完整可用或已经通过真实路径验收。**

## 8. 可复验步骤

所有命令仅检查来源、读取证据或运行隔离组件脚本；不构建、不安装、不重启 App。

```bash
I=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
M=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading

# 来源身份：预期为 §1 的分支/完整提交；报告提交后 Mac HEAD 仅增加文档提交。
git -C "$I" branch --show-current
git -C "$I" rev-parse HEAD
git -C "$I" status --short
git -C "$M" branch --show-current
git -C "$M" rev-parse HEAD
git -C "$M" status --short

# B：生产声明、协议定义及动态转换。
rg -n 'SessionProjectionAnchorNewerFetching|SessionProjectionWindowFetching|final class CCCodeBridgeBackendClient' \
  "$I/OpenCodeiOS"

# hello 声明、字典回填、查询与失效。
rg -n 'helloCapabilities|sessionSyncV2Enabled|acknowledgedCapabilities|supportsAcknowledgedCapability' \
  "$I/OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift"

# 真机重复全量及迟到成功。
rg -n 'anchor-echo-off|WS-RAW.*22790672|FULL ok sid=7f389d08' \
  /tmp/fgtrace-pull/fg-trace3.log

# Mac echo 和同一 session 的 RPC 阶段；按本报告时间窗对齐，避免混轮次。
rg -n 'echoedCapabilities|projection_rpc.*7f389d08' \
  "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"

# A：clear、replica clear 与 v30 的明确设计约束。
rg -n 'projectionStore.clear|func clear|states\[sessionId\] = nil' \
  "$I/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift" \
  "$I/OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift" \
  "$I/OpenCodeiOS/OpenCodeiOS/Models/ProjectionReplica.swift"
rg -n '内存态不参与|不得为打开路径改动|内存层的价值' \
  "$M/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md"

# C：旧算法来源，不切换分支。
git -C "$I" show \
  17f336a598ab9fff8804595e73be9fd04a0cda18:OpenCodeiOS/OpenCodeiOS/Models/ProjectionBaseCacheStore.swift

# 独立真实样本 benchmark 和边界实验；要求原始 /tmp 输入、脚本仍存在。
# 写入只发生在 /tmp/cordcode-projection-analysis/cache。
xcrun swift /tmp/cordcode-projection-analysis/bench.swift

# 检查同步/截断查询是否存在生产消费者。
rg -n 'isReconcileSyncing|truncatedOlderTurnCount' \
  "$I/OpenCodeiOS/OpenCodeiOS"

# 对账事务中的代际、取消、分页和 epoch 使用。
rg -n 'epoch|isCancelled|fetcherGeneration|pendingPull|pages|correctionsPending' \
  "$I/OpenCodeiOS/OpenCodeiOS/Models/ProjectionRestoreThenIncrement.swift"
```

本轮仅完成源码分析、原始日志对齐和 Mac 隔离组件实验。未运行 App build、unit/UI/snapshot tests、真机安装或生产修复验证；没有把组件成功冒充端到端成功。
