# codex-web 拓扑分裂与同步监视分析 v4 复审报告

- 复审日期：2026-08-24
- 被评审文档：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md)
- 被评审提交：`d132a0f`
- 上一轮复审：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v3-review.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v3-review.md)
- 复审目标：尽可能发现遗漏，并回答“该文档现在能否直接交给开发 agent 使用”
- 复审结论：**不能直接交给开发 agent 做产品实现；也不应按当前 §6 原样开始取证。v4 已解决旧评审问题，但仍有 6 个 P1、3 个 P2。完成 v5 后可交给取证 agent；两组证据闭环并重写为 implementation plan 后，才可交给实现 agent。**

## 1. 已确认通过的事项

本轮复核确认 v4 对 v3 复审的五项整改均真实成立：

1. `93c2c70` 与 `6161968` 已不在任何 ref 的可达历史中。
2. 两个旧对象当前均无法通过 `git cat-file -e` 读取；`git fsck --unreachable --no-reflogs` 无输出，本地物理清除声明现在成立。
3. 四份文档均在 `d132a0f` 提交树中，正文链接可解析。
4. 分析文档及三份评审报告未检出此前真实 machine/environment 标识或个人绝对路径。
5. daemon seat 250ms、patch skew、topology gate 复用粒度的代码事实正确。
6. 取证桩已被限定为只读，不拥有 catalog/timeline/projection 真相。
7. direct/Relay 的投递阶段名称已分开，不再把 enqueue 冒充 client applied。
8. 五态 UI 映射已经补齐，`desktop_absent` 与 `unknown` 不再伪装成 split。
9. Session Sync v2 的单 writer、Projection Kernel owner 和 control-plane 边界没有被破坏。

所以本轮不是再次推翻路线，而是审核这条路线能否被另一个 agent 无歧义执行。

## 2. P1：阻止当前文档直接执行的问题

### P1-1：Desktop 版本化证据在 v4 提交时已经过期

文档 §2.1/§5.3 明确声明 Desktop transport 证据只适用于：

- ChatGPT `26.818.31338`
- bundle `6892`
- embedded CLI `0.149.0-alpha.4`
- 对应旧 `app.asar`/CLI hash

本轮对当前安装包做只读复核，实际现场已经是：

```text
ChatGPT              26.818.41509
bundle               6962
embedded CLI         codex-cli 0.149.0-alpha.4.1
standalone CLI       codex-cli 0.149.0-alpha.4
app.asar SHA-256     8eb91bd9efbf9a4dd04b9b0afdbfcb4e0bab5da18c1919ad74ca327c00c7e791
embedded CLI SHA-256 09db9560f6f9dec139d3324254fb3c8fdbad5ecce1d8c794113dc15294f6aefd
```

文档自己规定“build/SHA 变化后需重采证据”，因此当前 §6 的 topology 实验前置门尚未满足。旧 build 的 `app.asar` attach/fallback/force-CLI 条件不能自动外推到新 build。

本轮只读 FD 复核显示：当前 Desktop 仍有一个 Unix peer 命中官方 daemon socket object，CordCode runtime 仍有两个 peer 命中同一 daemon；这能证明“当前现场此刻为 shared”，但不能替代对新 `app.asar` 宿主逻辑和强制 stdio 实验入口的源码复核。

#### 必须修订

1. 在任何 split/mixed 实验前，先为当前 build 重跑 Desktop Attach Gate。
2. 更新证据包中的 build、bundle、CLI 版本与两个 SHA-256。
3. 对当前 `app.asar` 重新核实：
   - local-daemon attach 条件；
   - `daemon version` 失败后的 transport 选择；
   - `supportsReconnect` 行为；
   - 仅用于隔离取证的 force-CLI/stdio 入口。
4. 新证据入档前，§6 实验 1 状态应为 `blocked_by_stale_desktop_gate`，不能继续引用旧 build 的实验命令。

### P1-2：被引用的 Gate Attach 证据包仍包含个人绝对路径

v4 只扫描了分析文档和三份评审报告，但 §3.3/§5.3 直接引用的证据包：

[`scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md`](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md)

仍包含一个真实用户主目录下的 daemon socket 绝对路径。分析正文脱敏并不能使其引用的证据链自动脱敏；开发 agent 按链接打开证据时仍会接触和继续复制该路径。

#### 必须修订

- 将证据包中的个人路径替换为 `$CODEX_HOME/app-server-control/app-server-control.sock`。
- 对整个 Gate Attach 证据目录做同类扫描，而不是只扫描四份文档。
- 新版取证产物必须在提交前递归检查个人路径、hostname、installation/environment ID、session ID、workspace path 和 title。

### P1-3：取证桩尚不是可实现契约，开发 agent 可能通过第二次 `thread/list` 制造新真相路径

§2.3 只规定取证桩“记录 trigger、correlation 和脱敏 tuple diff”，但没有规定桩插入到哪一个现有数据对象上。当前实现存在关键约束：

- `codexDiscoveryHintFingerprint` 在同一次 head 响应上计算 `listOrderFingerprint`；
- `discoveryFingerprint`/`codexVisibleMembershipCounts` 在同一次 authoritative fetch 上计算 `listSemanticFingerprint`；
- `snapshotBackendSession` 只拿到最终 fingerprint/count，不再持有完整 tuple。

如果开发 agent 在 `snapshotBackendSession` 之外另发一次 `thread/list` 来采集 corpus，两个请求可能跨越官方状态变化，样本不再是“触发该 generation 的真实输入”；还会增加 daemon 负载并形成平行 catalog 读取路径。

#### 必须修订为明确契约

1. **禁止第二次 fetch**：取证必须观察“本次 fingerprint 实际使用的同一份 wire slice”。
2. head observer 插在 `FetchThreadListHead → filter/sessionsToWire → listOrderFingerprint` 的同一数据流上。
3. authoritative observer 插在 `FetchThreadList → filter visible membership → listSemanticFingerprint` 的同一数据流上。
4. `snapshotBackendSession` 接收显式 `triggerKind` 和 `sampleID`，但 fingerprint 输入仍由原函数唯一生产。
5. observer 失败只能丢取证样本并记录诊断错误；不得让 discovery 请求失败、不得改写 `seen`、不得阻止 fence/generation。
6. 取证关闭时不得增加额外 RPC、文件 I/O或无界内存。

建议在 v5 给出最小类型/调用图，例如：

```text
runBackendSessionDiscoveryLoop
  -> snapshotBackendSession(triggerKind, correlationID)
       -> discoveryFingerprint(..., observer)
            -> same fetched wire
            -> observer.Observe(redactedSample)   // best effort
            -> existing fingerprint               // sole behavior owner
```

没有这条硬约束，文档仍可能诱导开发 agent 为“方便抓样本”重新造一条 catalog 数据路径。

### P1-4：`lifecycle/manual` trigger 在当前 coalesced signal 中已经丢失，v4 没有决定是否改协议

当前 `CatalogRefreshSignals()` 返回容量为 1 的 `chan struct{}`：

- 官方 `thread/started`、rename、archive、delete 等生产者都调用同一个 `signalCatalogRefresh()`；
- channel 只携带空结构，且会合并多个 wake；
- discovery worker 最多只能知道“收到一个 catalog signal”，不能知道它来自 lifecycle notification 还是本地 RPC mutation。

v4 却要求取证桩将 authoritative trigger 标成 `lifecycle` 或 `manual`。仅在 `snapshotBackendSession` 增加参数无法恢复已经在 Agent 层丢失的信息。

#### 必须选择一个真实可实施口径

推荐第一阶段 trigger 枚举只包含：

```text
seed
periodic_tick
head_changed
catalog_signal_coalesced
```

这四类可以在 discovery worker 当前分支上准确产生。如果根因最后落在 `catalog_signal_coalesced`，再另立小任务把 `CatalogRefreshSignaler` 扩展为结构化、可合并的 reason set；该改动会影响 core interface 与 opencode-web，不能伪装成零影响日志桩。

若 v5 坚持第一轮就区分 lifecycle/manual，必须明确：

- 结构化 signal 类型；
- coalescing 时 reason set 的并集规则；
- opencode-web 等其他实现的迁移；
- 不改变 wake 次数与 catalog truth 的测试。

### P1-5：风暴实验的 1–2 分钟样本量不足，而且缺少必要运行前提和场景矩阵

authoritative cadence 是 60 秒。采样 1–2 分钟通常只能获得 1–2 个周期样本，无法可靠判断：

- 风暴是否已消失；
- semantic fingerprint 是否持续稳定；
- 某次变化是周期扫描还是合法生命周期变化；
- generation 变化率是否异常。

另一个未写出的前置条件是：3 秒 head probe 只有在 `broadcaster.HasConnections()` 为真时才运行。没有任何已连接客户端时，head corpus 会是空的。

当前实验还缺少 active turn。原问题恰好与流式 turn 中的 recency/updatedAt churn 有关；只观察空闲状态不能验证修复。

#### 必须改成计数和场景驱动的实验

不要只写“1–2 分钟”，至少定义：

1. 前置：一个 capability 正确的客户端持续连接，确认 `HasConnections=true`。
2. idle 场景：至少 30 个 head 样本、5 个 authoritative 周期样本。
3. active-turn 场景：一个持续跨越至少两个 authoritative interval 的真实长 turn；同时收集 head/full corpus。
4. 合法变化场景：新建、rename、archive/delete 各自验证一次，证明 observer 能区分合法 tuple 变化，且没有吞事件。
5. 停止条件：达到样本计数、无 observer 丢样、无额外 RPC、无敏感字段落盘。
6. 裁决：
   - 无业务变化时，semantic fingerprint/generation 不应反复推进；
   - 合法变化必须在 authoritative sample 中可解释；
   - 若仍出现异常，必须给出具体 hashed row、field mask、trigger 和 generation 的因果对。

按 60 秒 cadence，5 个周期样本意味着实际观察窗口至少约 5–6 分钟，而不是 1–2 分钟。

### P1-6：topology 实验和状态模型仍不完整——遗漏 CordCode 自身 attachment，且 `split_present` 与“不停 owner”存在冲突

#### 问题 A：只建模 Desktop，没有建模 CordCode runtime

§3.3 的 `topology` 维度写的是 Desktop/runtime FD，但实例分类和五态聚合只描述 Desktop。共享同步成立至少还要求：

- CordCode 主 connection 命中 daemon；
- observer connection 命中同一 daemon；
- 两者可能处于 reconnect/partial 状态。

如果 Desktop 为 `all_shared`，但 CordCode observer 已掉线，当前五态仍会显示健康，而 iOS 实际不会收到 Desktop turn。

必须增加独立状态，例如：

```text
bridgeAttachment = shared | partial | absent | unresolved
desktopAggregate = desktop_absent | all_shared | split_present | mixed | unknown
```

最终 sync health 由二者组合，不能只看 Desktop。

#### 问题 B：单个 Desktop 实例可能同时存在 shared FD 与 private 子进程

当前实例级 `shared/private/unresolved` 被写成互斥状态，但现实中一个实例可能在切换/残留窗口同时：

- 保有 shared daemon FD；
- 拥有 private stdio app-server 后代。

需要增加 `dual`，或把实例证据建模为两个独立布尔/三态维度，再由真值表分类。FD 命中不能覆盖 private-process 证据。

#### 问题 C：保持 owner shared Desktop 打开时无法制造聚合 `split_present`

如果 owner Desktop 一直保持 shared，同时启动一个强制 stdio 的隔离 Desktop，产品全局聚合结果只能是 `mixed`，不是“存在 private 且无 shared”的 `split_present`。

因此 §6 实验 1 不能同时承诺：

- shared/split/mixed 三个聚合状态都实测；
- 永不关闭当前 shared owner Desktop。

必须诚实拆分：

- shared：当前 owner/隔离 shared 实例可测；
- private instance：强制 stdio 的隔离实例可测；
- mixed aggregate：owner shared + 隔离 private 可测；
- split_present aggregate：只有在没有任何 shared Desktop 时才能测，需 owner 自愿关闭 Desktop，或标为人工/blocked；不能通过“测试时忽略 owner PID”冒充产品聚合结果。

#### 问题 D：private 不能仅靠“不命中 daemon FD”判定

无 shared FD 也可能表示启动中、连接重试或权限不足。`private` 至少需要以下正证据之一：

- Desktop 日志明确 `transport=stdio`；
- 递归父进程链确认该 Desktop 拥有私有 `codex app-server`，且 stdio pipe/FD 形态吻合；
- 当前 build 的官方宿主源码与隔离启动环境共同证明强制 stdio 分支命中。

否则必须归为 `unresolved`。

## 3. P2：转 implementation plan 前仍需补足的问题

### P2-1：取证产物没有 schema，且“保存响应”与“不记录 title/path”互相冲突

§2.3 一方面写“保存 head/full 响应”，另一方面禁止记录完整 title/message。authoritative wire 还含 session ID、workspace directory、project ID 等敏感字段。开发 agent 如果直接保存 raw JSON，会重演 v1 的泄露问题。

v5 应明确：**不得持久化 raw `thread/list` payload**。建议取证 JSONL 只包含：

```text
schemaVersion
runId
sampleId / correlationId
corpusKind
triggerKind
monotonicOffsetMs
rowCount / rawCount
fingerprint
catalogGenerationBefore/After
rowKeyHmac
fieldChangeMask
index
updatedAtDeltaMs
observerError
```

session ID、title、directory、project ID 使用每次 run 的内存 HMAC 或仅输出 changed/not-changed；不得输出可逆原文，run salt 不写入仓库。产物还需定义：

- 保存目录与文件命名；
- 最大样本数/字节数；
- schema version；
- 截断与 dropped sample 计数；
- commit 前递归脱敏扫描；
- 取证完成后的临时文件清理规则。

### P2-2：五维诊断表仍缺少机器可判定状态，`versionCompatibility = Desktop 自决` 不是数据源

§3.3 将 `versionCompatibility` 的判定来源写成“Desktop 自决”。这是行为描述，不是 monitor 可以读取的状态。

每个维度至少需要：

- 枚举值；
- 数据源；
- 采样失败值；
- freshness/采样时间；
- 是否影响 UI；
- 与 topology 的优先级。

例如 version compatibility 只能在以下证据下输出强结论：

- Desktop 已有 shared FD：当前连接事实证明本实例实际 compatible；
- 当前 embedded CLI 的官方 `daemon version` probe 明确成功/失败；
- 其余情况为 `unknown`，不能仅根据版本字符串猜测。

同理，`seatHealth` 应区分 daemon running 与 LaunchAgent healthy，`legacyProcess` 应区分 managed-loopback 遗留和 Desktop private process，不能共用一个布尔值。

### P2-3：server-only 指标仍需区分“当前已有观测点”与“未来实现点”

v4 的语义分类正确，但“第一阶段指标全部 bridge 可观测”容易被理解为代码已经具备全部指标：

- direct projection 的 `write_post` 已有；
- Relay `relay_writer_enqueued` 需要在 Relay writer 内部增加正式计数点；publisher 侧 `SendJSONClassified` 返回 void，无法证明内部 enqueue 成功；
- catalog generation 目前没有与 projection 同粒度的 per-transport completion 指标；
- management API、snapshot schema 与 badge 状态机仍只是 §4.3 的待设计清单。

文档应给指标逐项标注：`existing` / `needs instrumentation` / `future protocol`。否则开发 agent 可能把现有日志当成完整监控实现，或反过来重复实现已有的 direct probe。

## 4. 能否交给开发 agent

### 4.1 现在不能作为产品实现文档

答案是：**不能。** 文档自己仍标为“取证分析稿，非实施计划”，而且以下产品决策尚未完成：

- 当前 Desktop build 的宿主 Gate；
- topology 的 CordCode attachment 与 dual-instance 真值表；
- 五维诊断的机器 schema；
- monitor owner/API/persistence/threshold 的完整设计；
- 取证结论决定的风暴真实修复点；
- topology 状态管理 API 与 MacBridge 接线任务；
- 测试与回归 gate。

让实现 agent 直接使用当前文档，会迫使它在施工过程中自行决定这些架构问题，正是此前路线偏航的来源。

### 4.2 修订后可以交给“取证 agent”

v5 补齐本报告 P1 后，可以把文档交给取证 agent，且任务范围只能是：

1. 更新当前 Desktop Attach Gate；
2. 实现严格只读、同源、脱敏、有界的 discovery instrumentation；
3. 执行 topology 与 catalog 两组实验；
4. 输出证据包和裁决，不开发产品 monitor/UI。

### 4.3 什么时候可以交给“实现 agent”

两组实验完成后，必须另写或升级为 implementation plan，至少包含：

- 冻结的状态类型和 API schema；
- topology 与 monitor owner；
- 文件级实施拆分；
- 每项任务的输入/输出与禁止事项；
- 单元、集成、回归 gate；
- MacBridge UI 映射；
- 失败可见和回滚路径；
- Session Sync v2 护栏逐项测试；
- 真实证据引用与 completion report。

只有该 implementation plan 才适合交给开发 agent 全量施工。

## 5. 建议的 v5 修订顺序

1. 先更新当前 Desktop build 的 Gate Attach 证据，并脱敏被引用证据包。
2. 冻结取证桩的同源观察点、trigger 枚举、失败语义和禁止第二次 fetch。
3. 冻结脱敏 JSONL schema、enable 开关、bounded 规则与产物路径。
4. 将实验改为计数/场景驱动，写明在线客户端和 active-turn 前置。
5. 补 bridgeAttachment、dual instance 与聚合真值表。
6. 拆分 shared/private-instance/mixed/split-present 的可测条件与人工阻塞条件。
7. 为五维诊断和指标增加 machine-readable 状态及 `existing/needs instrumentation/future` 标记。
8. v5 复审通过后，仅执行取证；证据完成后另起 implementation plan。

## 6. v5 验收门槛

- 当前安装 Desktop build/hash 的 attach/fallback/force-stdio 证据已更新。
- 所有被引用证据文件均通过递归脱敏扫描。
- instrumentation 不产生第二次 `thread/list`，观察的是 fingerprint 的同一输入。
- trigger taxonomy 与当前 coalesced signal 能力一致；若改 core interface，有完整迁移和测试范围。
- raw session/title/path/project 数据不落盘，取证 schema 已冻结且有界。
- head corpus 明确要求在线连接；active turn 横跨至少两个 full interval。
- authoritative 样本数足以裁决，不能再以 1–2 分钟替代样本门槛。
- Desktop 实例支持 shared/private/dual/unresolved，聚合真值表无歧义。
- CordCode main/observer attachment 有独立状态，并参与最终 sync health。
- split_present 的实测前提与“不停 owner”不再互相矛盾。
- private 状态要求正证据，FD 不命中只能得到 unresolved。
- 五维诊断有机器可读枚举、数据源和 freshness。
- 指标逐项标注已有、需 instrumentation 或未来协议。
- 文档仍明确：取证 agent 不开发产品 monitor/UI。

## 7. 最终裁决

v4 已经是一份方向正确、护栏清楚的分析文档，但还不是“另一个 agent 拿到就能稳定执行”的取证 runbook，更不是 implementation plan。

最关键的新发现有三项：

1. 当前 Desktop 已升级，旧 build 的宿主证据按文档自己的规则已经失效；
2. discovery instrumentation 若不冻结同源观察点和脱敏 schema，极易产生第二次 fetch 或敏感 raw dump；
3. topology 状态只建模 Desktop，尚未覆盖 CordCode main/observer attachment，也无法在 owner shared Desktop 保持打开时实测聚合 `split_present`。

复审裁决：**v4 不通过“交给开发 agent”门；需要 v5。v5 通过后可交给取证 agent，取证完成并形成独立 implementation plan 后，才可交给实现 agent。**
