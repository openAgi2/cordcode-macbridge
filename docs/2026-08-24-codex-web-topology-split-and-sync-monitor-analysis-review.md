# codex-web 拓扑分裂与同步监视分析文档评审报告

- 评审日期：2026-08-24
- 被评审文档：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md)
- 被评审提交：`93c2c70`
- 评审范围：事实与源码一致性、Session Sync v2 架构护栏、诊断信号可靠性、监控指标可实施性、仓库信息安全
- 结论：**需要重大修订，当前版本不应直接进入实施**

## 1. 总体结论

文档有三项正确的方向判断，可以保留：

1. `remoteControl/status/changed` 是官方远程控制功能状态，不是 Desktop 与 CordCode 是否连接同一 daemon 的拓扑信号，排除正确。
2. 同步健康监视器以内嵌 bridge 组件实现，比新增独立常驻进程更符合当前宿主拓扑。
3. topology diagnostic 必须停留在 control-plane，不能写入 timeline 或 Projection Kernel；监控是围栏，不是治疗。

但是，文档当前的两个核心实施前提不成立：

- fingerprint 风暴被归因于旧的 `wireFingerprint=id|updatedAtMillis` 路径，但在文档提交之前，codex/codex-web discovery 已经改为 `listOrderFingerprint` 与 `listSemanticFingerprint` 两条路径。
- 进程树中的裸 `codex app-server` 被直接解释成共享 daemon 本体，但相同进程形态也会由 legacy session 和 catalog stdio fallback 产生，进程树无法证明实际 socket 拓扑。

如果按当前文档直接实施，最可能出现的结果是：误诊 topology split、对正常 catalog 变化做熔断，以及产生长期误报的同步监控指标。

## 2. 阻塞实施的问题

### P1-1：fingerprint 风暴归因与提交时的代码不符

原文 §1.2 断言：

> `wireFingerprint` 摘要为 `id|updatedAtMillis`，官方 `updatedAtMillis` 每 2–4 秒变化导致 generation 和 `sessions_changed` 风暴。

但在被评审提交 `93c2c70` 之前，提交 `f8a66aa` 已经完成以下修改：

- 3 秒 head hint 使用 `listOrderFingerprint`，只包含顺序与 session ID，明确排除了流式 turn 引起的 `updatedAtMillis` 抖动。
- codex/codex-web authoritative snapshot 使用 `listSemanticFingerprint`。
- `listSemanticFingerprint` 包含顺序、ID、`updatedAtMillis`、规范化目录、project ID 和 title，并非原文描述的旧 `wireFingerprint`。
- codex-web 的 text delta 不直接触发 catalog refresh；刷新事件集中在 thread started/name updated/archived/deleted 等生命周期变化。

代码锚点：

- [`go-bridge/session_discovery.go`](../go-bridge/session_discovery.go)：`codexDiscoveryHintFingerprint` 与 `snapshotBackendSession`
- [`go-bridge/catalog_native_membership.go`](../go-bridge/catalog_native_membership.go)：`listSemanticFingerprint`
- [`agent/codex-web/events.go`](../agent/codex-web/events.go)：codex-web lifecycle refresh 事件

因此，“风暴根因是 `updatedAtMillis`”目前只是未证实假设。原文建议的“噪声字段忽略”或“连续 N 次抑制”会把症状压下去，却可能同时吞掉合法的新建、重命名、归档、目录变化或最近活动顺序变化，违反真实路径失败应被暴露的开发约束。

#### 必须修订

将 §1.2、§3.2 和 §5 实验 2 改成证据驱动的定位流程：

1. 连续保存 `thread/list` 原始响应，保留采样时间与请求触发源。
2. 对相邻样本的完整语义 tuple 做逐字段 diff：顺序、ID、`updatedAtMillis`、目录、project ID、title。
3. 区分 3 秒 head hint、60 秒 authoritative tick、thread 生命周期事件和人工刷新。
4. 对照 `catalogGeneration`、`sessions_changed` 的实际推进点。
5. 只有在确定具体字段和生产者之后，才能决定修改 fingerprint、修复上游生产者或增加防回归哨兵。

在完成上述实验之前，应删除“updatedAtMillis 已确定是根因”和任何准备实施的熔断方案。

### P1-2：进程树不能作为首选 topology split 判据

原文 §2.3 将某个 bridge 子代的裸 `codex app-server` 解释成 `daemon start` 派生的共享 daemon body，并据此建议通过 Desktop 谱系是否存在 app-server 判断拓扑分裂。

这项解释没有源码支持：

- [`agent/codex-web/lifecycle.go`](../agent/codex-web/lifecycle.go) 的 `runDaemonStart` 只是执行 `codex app-server daemon start` 并等待返回，没有证明观察到的裸进程是其派生 daemon body。
- [`agent/codex/appserver_session.go`](../agent/codex/appserver_session.go) 的 legacy stdio session 同样执行裸 `codex app-server`。
- [`agent/codex/catalog_client.go`](../agent/codex/catalog_client.go) 的 catalog stdio fallback 也执行裸 `codex app-server`。

因此，单凭 PID、PPID 和命令行形态，无法区分：

- 共享 daemon 本体；
- legacy codex stdio session；
- catalog fallback；
- Desktop 私有 stdio app-server。

新版 [`2026-08-21-codex-web-backend-design.md`](./2026-08-21-codex-web-backend-design.md) 的真正拓扑不变量是：Desktop 与 CordCode 的实际 FD/peer 是否连接同一个 daemon control socket。进程树是易受重启、reparent 和官方版本实现变化影响的间接信号，只能作为辅助证据。

“writer conflict 突然消失”也不能作为可靠的负向佐证：它依赖当前选择的 session、是否存在活跃 writer 以及测试时序，不能证明 Desktop transport 类型。

#### 必须修订

拓扑诊断应采用以下证据优先级：

1. **主证据：实际 Unix socket FD/peer**。确认 Desktop 与 CordCode 是否连接同一 control socket/daemon identity。
2. **辅助证据：进程树与命令行**。用于定位候选进程，不能单独下结论。
3. **状态分类：`shared` / `split` / `unknown`**。证据不足必须返回 `unknown`，不得猜测成健康或分裂。
4. **防抖只作用于诊断状态发布**，不能终止官方进程、切换 transport、增加 writer 或修改 timeline。

文档还缺少真正发生 topology split 时的 FD/peer 与进程树成对样本。该实验完成前，不应把进程树判据标为“高置信度”或“推荐实施”。

### P1-3：iOS topology 提示缺少正式的 control-plane 协议落点

原文 §2.4 提出通过“既有 backend/runtime status 或 sessions metadata”携带状态，并强调“不新增通道”。这里混淆了三种不同作用域：

- topology health 是 backend-global diagnostic 状态；
- runtime status 是某次执行或 backend 运行态；
- session metadata 是单个 session 的目录事实。

把 topology 状态塞进 session metadata 会污染 session 数据语义；塞进 turn runtime status 则会让诊断状态与生成状态错误耦合。即使只增加一个字段，也仍然是跨 Mac/iOS 的协议变更，不能用“不新增通道”规避协议设计。

#### 必须修订

二选一：

1. 定义 backend-global control-plane diagnostic status，补齐 Mac canonical schema、iOS mirror/Swift 类型、capability/version negotiation、delivery/client tests；或
2. 第一阶段只在 MacBridge 展示，不向 iOS 传输，等协议设计完成后再扩展。

无论选择哪条路线，该状态都不得进入 timeline、Projection Kernel 或 session catalog fingerprint。

### P1-4：文档包含应脱敏的本机信息

原文真实样本包含 hostname、installation ID、environment ID 和个人绝对路径。这些值并非论证所必需，而且违反仓库关于个人绝对路径的约束。

#### 必须修订

- 将 hostname、installation ID、environment ID 替换为明确标注的脱敏示例。
- 将个人绝对路径替换为仓库相对路径、`$CODEX_HOME` 或 `<repo-root>`。
- 如果提交已经进入共享远端，根据仓库暴露范围决定是否需要清理 Git 历史；至少应立即提交 HEAD 脱敏修订。

## 3. 监控方案的问题

### P2-1：多个指标跨越了不可直接比较的计数域

原文 §3.2 提议监控“被动事件数与 K4Patch flush 数的差额”，并认为正常情况下差额应为零。该假设不成立：

- control-plane 帧不会写入 Projection Kernel；
- 重复帧或 reducer no-op 不推进 revision；
- 多个官方帧可能合并到一个 projection revision 或 patch；
- patch 生成、连接投递、客户端应用是不同阶段；
- 重连 realign/snapshot 不能按普通增量 patch 计数。

同样，`sessions_changed:list_sessions` 也不是一一对应关系。iOS 可以防抖、合并刷新、处于后台；还可能存在多个客户端。`zero-online` 在没有观察者时是合法状态，`prekey` 只适用于 Relay，不能与 LAN 统一计数。

#### 建议改为分阶段指标

针对 timeline/projection：

1. 官方 timeline frame decoded；
2. reducer applied / revision advanced；
3. patch `baseRev → syncRev` generated；
4. patch delivered per connection；
5. client ack/applied；
6. reconnect realign/snapshot。

针对 catalog：

1. authoritative snapshot sampled；
2. semantic fingerprint changed；
3. `catalogGeneration` advanced；
4. generation delivered per foreground connection；
5. client observed/applied generation；
6. client-triggered list completion。

所有指标至少应按 backend、transport、bridgeEpoch、connection generation、事件类别和观察范围分区；标签值必须有界，不能把 session/thread ID 直接作为长期指标标签。

### P2-2：内嵌监视器的生命周期和状态机未定义

“与 `catalogProcessRegistry` 同型”不能构成监视器设计。Process registry 管理进程身份和生命周期，而健康监视器管理采样窗口、聚合状态与告警恢复，两者职责不同。

实施前应补齐：

- owner 和 start/stop 时机；
- bridgeEpoch、reconnect、backend restart 后的计数清零规则；
- 聚合窗口、阈值、防抖和冷启动宽限期；
- `healthy` / `degraded` / `unknown` 状态机及恢复条件；
- 有界标签和内存上限；
- snapshot schema、管理 API、capability；
- 保存位置、保留期限和脱敏规则；
- MacBridge/iOS 各自消费范围；
- 单元测试、集成测试和故障注入边界。

否则，监视器自身可能成为新的高频状态源，并在重连、无客户端或 Relay/LAN 切换时持续误报。

## 4. Session Sync v2 护栏审计

文档当前没有直接要求增加第二个 timeline writer，也没有要求 iOS 绕过 Projection Kernel 直接解释官方帧，因此概念上没有立即违反 Session Sync v2 的核心 owner 约束。

但以下实施边界必须写成硬约束：

1. topology 与 monitor health 仅属于 control-plane，不进入 `IngestLive`、timeline 或 Projection Kernel。
2. 诊断器只观察状态，不得执行 daemon 切换、终止官方进程、抢占 active writer 或修改 session。
3. iOS 只消费 Mac canonical control-plane 状态，不自行通过轮询或本地进程推断 topology。
4. catalog health 不得通过修改 session metadata 或 fingerprint 表达。
5. timeline 指标必须来自既有单一流水线的阶段观测，不能为了监控新增平行解析/投影路径。
6. 所有跨端新增字段必须 capability-gated，并有 canonical pack、mirror、delivery 和 client acceptance 测试。

只要按上述边界修订，内嵌诊断监视器可以符合 Session Sync v2；按当前模糊的“runtime status 或 sessions metadata”方案直接实施则存在架构漂移风险。

## 5. 建议的文档重写顺序

1. 脱敏所有机器标识和个人绝对路径。
2. 将 topology 首选判据从进程树改为实际 socket FD/peer；进程树降为辅助证据。
3. 重新分类现场进程，补采 split 状态与 shared 状态的成对证据。
4. 删除旧 `wireFingerprint` 的既定根因描述，基于当前代码重写 catalog fingerprint 机制。
5. 完成连续 `thread/list` raw tuple diff 与触发源对账，再确定风暴修复方案。
6. 为 topology health 定义 backend-global control-plane schema，或者明确第一阶段仅 Mac 展示。
7. 将监控指标改成 projection/catalog 两条分阶段流水线，按 transport 和 connection generation 分区。
8. 补齐监视器 owner、生命周期、状态机、存储、脱敏、API 和测试矩阵。
9. 完成以上修改后，再把文档状态从“分析稿”升级为实施计划。

## 6. 验收门槛

修订版至少应满足以下条件，才能进入编码：

- 不再把旧 `wireFingerprint` 描述成当前 codex-web discovery 实现。
- 每个 catalog 风暴根因结论都有对应的 raw 样本和字段级 diff。
- topology 判定有 actual socket peer 证据，且明确 `unknown` 分支。
- 不依赖 writer conflict 的出现或消失判断 transport。
- 不包含真实机器标识和个人绝对路径。
- iOS 提示具有明确的 backend-global control-plane 协议定义，或明确延后。
- 指标比较发生在语义相容的阶段，不要求 raw event 数等于 patch 数。
- 明确 bridgeEpoch/reconnect 后的监控重置与告警恢复规则。
- 明确诊断面不写 timeline、不增加 writer、不改变官方 daemon 生命周期。

## 7. 最终评审意见

评审结论为：**方向可保留，事实层和实施层必须重写；当前版本拒绝作为 implementation-ready 文档。**

最优先的工作不是编写监视器，而是完成两组证据闭环：

1. shared/split 两种状态下 Desktop 与 CordCode 的实际 socket FD/peer 对照；
2. 当前 discovery 实现下连续 `thread/list` 的完整语义 tuple 与 refresh trigger 对照。

这两组证据会直接决定拓扑诊断和风暴修复的真实方案，也能避免再次把适配器表象当成宿主拓扑不变量。
