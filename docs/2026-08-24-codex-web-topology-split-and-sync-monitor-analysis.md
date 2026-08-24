# codex-web 拓扑分裂与同步监视 —— 取证分析与执行约束（修订 v5）

- 日期：2026-08-24
- 状态：**DONE（取证阶段已收口；不再是执行队列）**。产品实现以 v2 implementation plan 为唯一队列
- 使用范围：可以交给取证 agent 实现受限 instrumentation、执行证据实验并输出裁决；**不得据此直接开发产品 monitor、管理 API、MacBridge/iOS UI 或同步协议**。
- 涉及仓库：Mac `cordcode-macbridge-codex-web`；iOS `cordcode-ios-codex-web-backend` 仅作协议边界引用，本阶段不改。
- 架构护栏：Session Sync v2。timeline 真相 owner 仍是 Projection Kernel；诊断只属于 control plane，不得写 timeline、增加 writer、改变官方 daemon 生命周期或推进 projection revision。
- 评审链：
  - [v1 评审](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-review.md)
  - [v2 复审](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v2-review.md)
  - [v3 复审](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v3-review.md)
  - [v4 复审](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v4-review.md)

## 0. v5 修订裁决

> 2026-08-24 晚间收尾：catalog 取证已由 evidence verdict 二版判定 PASS；本文要求的临时 observer 也已在产品发布门中移除。未执行的 Experiment A 活体 private/mixed/split 样本已转入 v2 计划的 owner 人工门，排在所有可自动化任务之后；禁止从本分析重复开发 monitor/API/UI。

v5 对 v4 复审的 6 个 P1、3 个 P2 全部落实：

| 复审项 | v5 决策 |
| --- | --- |
| 当前 Desktop Gate 过期 | §2.1 和 Gate 证据包更新到 ChatGPT `26.818.41509` / bundle `6962`，重验 attach、fallback、reconnect、force-stdio 静态逻辑和 shared FD 现场 |
| 引用证据仍有个人路径 | Gate README 改为 `$CODEX_HOME`；§7.2 要求递归扫描整个证据目录 |
| instrumentation 可能二次 fetch | §3.2 冻结为同一 wire slice 上的旁路 observer，明确禁止第二次 `thread/list` |
| lifecycle/manual trigger 已丢失 | §3.3 第一阶段只允许 `seed / periodic_tick / head_changed / catalog_signal_coalesced` |
| 1–2 分钟样本不足 | §6.2 改为计数与场景门：idle 至少 30 head + 5 authoritative，active turn 横跨至少两个 full interval |
| topology 漏建模 bridge/dual | §4 增加 `bridgeAttachment`、Desktop `dual` 与无歧义聚合真值表 |
| raw 响应与脱敏冲突 | §3.4 禁止持久化 raw payload，冻结有界 redacted JSONL schema 与 HMAC 规则 |
| 五维诊断不可机器判定 | §4.4 冻结枚举、数据源、失败值、freshness 与 UI 影响 |
| 指标现状混写 | §5.2 每项标注 `existing / needs_instrumentation / future_protocol` |

本版的交付门是“另一个取证 agent 能无歧义执行”，不是“另一个产品开发 agent 能直接施工”。两组证据闭环后必须另写 implementation plan。

## 1. 结论与问题层级

1. **拓扑提示方向成立**，但必须由实际 Unix socket FD/peer 与 private 正证据共同裁决。`remoteControl/status/changed` 是官方远程控制功能状态，与 Desktop 使用 shared daemon 还是 private stdio 无关，继续排除。
2. **同步监视方向成立**，合理形态是 bridge 内嵌监视器；它是围栏，不是治疗。catalog 风暴的具体字段和生产者尚未证实，不得先加忽略字段、连续 N 次抑制或熔断。
3. 当前最先做的不是产品 UI，而是两组证据闭环：
   - 当前 Desktop build 的 shared/private/mixed 拓扑证据；
   - head/authoritative 双 corpus 的同源、脱敏、带 trigger 取证。
4. 第一阶段只允许只读取证 instrumentation。产品 monitor、API、badge、iOS 协议都要等待取证结论与独立 implementation plan。

## 2. 已核实事实基线

### 2.1 当前 Desktop Attach Gate（2026-08-24）

当前安装现场：

```text
ChatGPT              26.818.41509
bundle               6962
embedded CLI         codex-cli 0.149.0-alpha.4.1
standalone CLI       codex-cli 0.149.0-alpha.4
app.asar SHA-256     8eb91bd9efbf9a4dd04b9b0afdbfcb4e0bab5da18c1919ad74ca327c00c7e791
embedded CLI SHA-256 09db9560f6f9dec139d3324254fb3c8fdbad5ecce1d8c794113dc15294f6aefd
```

对当前 `app.asar` 本地只读解包并核实 `.vite/build/src-CLzQUgbV.js`：

- local、非 Windows host，`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`，未设置 `CODEX_APP_SERVER_FORCE_CLI=1`，没有 CLI override，且 `codex app-server daemon version` 在 2500ms 内通过 Desktop 自身兼容检查时，transport 改为 `websocket` 并连接 `$CODEX_HOME/app-server-control/app-server-control.sock`；
- 任一条件不满足或 probe 抛错时，代码落入 private `stdio` transport；
- transport 的 `supportsReconnect()` 仅在 `kind=websocket` 时为 true；stdio 被选中后不会依靠该 transport 自动切回 shared；
- `CODEX_APP_SERVER_FORCE_CLI=1` 仍能阻断 local-daemon 分支，是当前 build 可用于**隔离取证**的 force-stdio 入口，不得成为产品 fallback。

同日只读 FD 现场：Desktop 主进程有 1 个 peer、CordCode runtime 有 2 个 peer 命中同一 daemon socket object；launchd attach env 为 `1`，daemon 报 `status=running`。这证明当前 Desktop 和 runtime 进程均连接 shared daemon，也证明 patch skew `alpha.4.1` vs `alpha.4` 未阻止实际 attach；两个 runtime peer 与 main+observer 的预期形态一致，但数量本身不能代替逻辑 client 身份识别。该样本也不替代当前 build 的 private/mixed 活体样本。

完整版本化证据见 [Gate Desktop Attach](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md)。旧 2026-08-22 样本仅作历史基线。

### 2.2 daemon seat 与分裂机理

- `MacBridge/MacBridge/Services/RuntimeManager.swift` 的 `CodexSharedDaemonSeat.recoverIntervalSeconds = "0.25"`；LaunchAgent 为 `RunAtLoad=true`、`KeepAlive=true`。60 秒是 catalog authoritative discovery 周期，不是 seat 恢复周期。
- RuntimeManager 对 CLI patch skew 只记日志，仍启动 daemon/seat；拓扑判据不能使用 exact string equality。Desktop 官方 probe 与实际 FD 才是兼容事实。
- 分裂可能发生于 seat 未安装/退出、standalone 缺失、daemon 持续失败、Desktop 在 attach env 生效前锁入 stdio、官方兼容 probe 失败或故障恢复竞速。
- 分裂后 Desktop turn 不进入 shared daemon，bridge observer 无法接收实时帧；iOS 对该 turn 的取消与实时同步都会失效。writer conflict 本身依赖 session/时序，不能作为拓扑判据。

### 2.3 catalog discovery 当前实现

- **3 秒 head hint**：`codexDiscoveryHintFingerprint` → `FetchThreadListHead` → filter/`sessionsToWire` → `listOrderFingerprint`。单页最多约 25 条，只含 `index|id`。仅当 `broadcaster.HasConnections()` 为 true 时运行。
- **authoritative full**：`snapshotBackendSession` → `FetchThreadList` 完整有界分页 → visible membership → `listSemanticFingerprint`。包含 `index|id|updatedAtMillis|规范化目录|projectId|title`。
- `snapshotBackendSession` 当前只区分 seed/poll；60 秒 ticker、`CatalogRefreshSignals()` 和 head changed 最终进入相同 poll 入口。
- `CatalogRefreshSignals()` 是容量 1 的 `chan struct{}`。thread started、rename、archive、delete 等原因已被合并，consumer 只能知道“收到 catalog signal”。
- `wireFingerprint` 只用于兼容/legacy 路径，不是 codex-web 当前指纹。

有记录的 2026-08-23 generation 风暴促成了 head 指纹从 semantic 改为 order-only；`f8a66aa` 后是否仍有 authoritative 风暴、若有由哪个字段/trigger 造成，仍是待证假设。

## 3. discovery 取证 instrumentation 冻结契约

### 3.1 允许范围

取证桩是临时、bridge 内部、只读的 control-plane observer：

- 默认关闭；仅在显式取证窗口开启；
- 不写 timeline/Projection Kernel，不增加 writer，不成为 catalog 数据源；
- 不修改 fingerprint、`seen`、fence、generation、投递、轮询 cadence 或 coalescing；
- observer 错误只能丢失诊断样本并产生有界错误码，不能使 discovery 失败；
- 取证结束后根据证据决定删除，或在独立 implementation plan 中提升为正式指标。

### 3.2 同源观察点：禁止第二次 fetch

**硬约束：任何 corpus 样本必须来自“本次 fingerprint 实际使用的同一份 wire slice”；不得为取证额外调用 `thread/list`。**

```text
runBackendSessionDiscoveryLoop
  ├─ head probe
  │    FetchThreadListHead
  │      → filter / sessionsToWire
  │      → evidence = observer.Capture(head, sameWire) # 只在内存生成脱敏 diff
  │      → listOrderFingerprint(sameWire)              # 唯一行为 owner
  │      → observer.Commit(evidence, fingerprint, generationBefore/After)
  └─ snapshotBackendSession(triggerKind, correlationID)
       FetchThreadList (bounded pagination)
         → visible membership
         → evidence = observer.Capture(authoritative, sameWire)
         → listSemanticFingerprint(sameWire)           # 唯一行为 owner
         → existing seen/fence/generation logic
         → observer.Commit(evidence, fingerprint, generationBefore/After)
```

实现约束：

1. observer 接口由 fingerprint 数据流接收只读 view，不能自行持有 client/fetcher；`Capture` 只生成有界脱敏内存对象，`Commit` 在原有状态推进完成后补齐 fingerprint/generation 并 best-effort 输出。
2. head observer 插在 `FetchThreadListHead → filter/sessionsToWire → listOrderFingerprint` 的同一路径；authoritative observer 插在 `FetchThreadList → visible membership → listSemanticFingerprint` 的同一路径。
3. `snapshotBackendSession` 新增 `triggerKind`、`correlationID` 只用于观测；返回值、错误和状态推进保持现有语义。
4. `Capture`/`Commit` 的 panic/error 都必须被边界捕获并转成 `observerError`，不得阻止 fingerprint 与 generation 的原路径执行；head 不推进 generation 时 before/after 使用同一当前值。
5. disabled 时不得增加 RPC、文件 I/O、goroutine、timer 或无界内存；允许的额外成本仅是一次 nil/disabled 分支。

定向单元测试至少证明：fetch 调用数不变；observer 看见的 slice 与 fingerprint 输入同源；observer 失败不改变结果与 generation；disabled 不产生 I/O；同一输入在 observer 开关两侧产生相同 fingerprint。

### 3.3 trigger 与 correlation

第一轮只允许当前 worker 能准确产生的枚举：

```text
seed
periodic_tick
head_changed
catalog_signal_coalesced
```

- head sample 自带独立 `sampleId`；当其变化触发 full refresh 时，full sample 复用同一 `correlationId`。
- 3 秒 head probe 与 60 秒 authoritative ticker 都使用 `periodic_tick`，由 `corpusKind` 无歧义区分；signal channel 使用 `catalog_signal_coalesced`；初始快照使用 `seed`；head 变化触发的 authoritative sample 使用 `head_changed`。
- **不得伪造 `lifecycle` 或 `manual`**。两者在容量 1 的空 signal 中已丢失。
- 如果根因落在 `catalog_signal_coalesced` 内部，再另立 core interface 变更：结构化 reason set、coalescing 并集、opencode-web 等实现迁移、wake 次数与 catalog truth 不变测试。本轮不做。

### 3.4 持久化、脱敏与上限

**禁止持久化 raw `thread/list` payload。** 运行时仅向现有 bridge 结构化日志写入 redacted 事件；本轮冻结的显式开关为：

```text
GO_BRIDGE_CODEX_CATALOG_TRACE=1
GO_BRIDGE_CODEX_CATALOG_TRACE_MAX_SAMPLES=<n>
```

- 默认 `maxSamples=256`，配置值超过 `512` 时钳制为 `512`；结构化取证总量硬上限 `1 MiB`，先到者停止并记录单个 `run_summary`。
- 不另开生产热路径 dump 文件。离线提取入口固定为 `scripts/codex-web-phase0/extract_catalog_forensics.sh`：只从 bridge 日志选择 `catalog_forensics` 事件并验证 schema，然后写入 `scripts/codex-web-phase0/dumps/catalog-forensics/<run-id>/catalog-forensics.v1.jsonl` 与 manifest；提取器不得复制其他日志行。
- `runId` 随机；row key 使用每次 run 的内存 HMAC key。key/salt 不写日志、不写证据包，因此不同 run 不可关联。
- title、session/thread ID、directory、project ID、hostname、installation/environment ID 和消息内容不得以原文或可逆编码输出。
- `observerError` 只允许有界枚举，不记录可能携带 payload/path 的原始 error string。

冻结的 JSONL schema：

```text
schemaVersion              # 固定 catalog-forensics.v1
runId
sampleId
correlationId              # 无因果关联时为 null
corpusKind                 # head | authoritative
triggerKind                # §3.3 四态
recordKind                 # sample_summary | row_diff | run_summary
monotonicOffsetMs          # 不写绝对墙钟时间
rowCount
rawCount
fingerprint
catalogGenerationBefore
catalogGenerationAfter
rowKeyHmac                 # row_diff 记录；由稳定 raw ID 计算，其他记录为 null
fieldChangeMask            # added/removed/index/updatedAt/directory/project/title bitset
index                      # 仅保留非敏感序位
updatedAtDeltaMs           # 相邻同 corpus 样本的差值，不写绝对时间
observerError              # none/encode_failed/limit_reached/write_failed/dropped
droppedCount               # 仅 run_summary 使用
```

每个 sample 先写 `sample_summary`，再只写相对上一份**同 corpus**样本的 `row_diff`；不得跨 head/full 比较。由于 row key 由稳定 ID 生成，ID 更换表示旧 row `removed` + 新 row `added`，不得虚构 `id changed`。超过上限时增加内存 dropped counter，最终只输出一个 `run_summary`。提交证据前必须递归扫描原始个人路径、主机名、session/thread ID、workspace/title、installation/environment ID；不通过则证据不得入 Git。

schema 测试必须覆盖：无 raw 字段、HMAC run 间不可关联、field mask 正确、上限/截断/dropped 计数、error 枚举、导出目录只含 redacted JSONL/manifest。

## 4. topology 诊断状态模型

### 4.1 证据优先级

1. shared 正证据：已识别进程的 Unix FD peer 命中 daemon control socket object。
2. private 正证据至少一个：Desktop 日志明确 `transport=stdio`；递归父进程链证明该 Desktop 拥有 private `codex app-server` 且 stdio pipe/FD 形态吻合；或当前 build 静态分支与该隔离实例的进程级环境共同证明 force-stdio 命中。
3. 无 shared FD 不是 private 正证据；启动中、权限失败、PID 竞态、FD 读取失败一律 `unresolved`。
4. 进程命令行仅用于候选枚举。裸 `codex app-server` 还可能是 catalog stdio 单例或 legacy session，不能单独裁决。
5. writer conflict、`remoteControl/status/changed`、CLI exact version 均不得覆盖 topology。

可以复用 `scripts/codex-web-phase0/verify_shared_daemon_topology.sh` 的 `lsof` socket-object / `matching_peer_count` 与 PID 身份检查；**不得复用**其中的 exact-version 单一 PASS/FAIL 作为产品 topology 结论。

### 4.2 Desktop 实例与聚合

实例级分类：

```text
shared_only   shared FD 正证据存在，private 正证据不存在
private_only  private 正证据存在，shared FD 正证据不存在
dual          shared FD 与 private 正证据同时存在
unresolved    实例存在，但两类证据不足或采样失败
```

聚合真值表：

| 全部实例证据 | `desktopAggregate` |
| --- | --- |
| 没有 Desktop 实例 | `desktop_absent` |
| 所有实例都是 `shared_only` | `all_shared` |
| 任一 `dual`，或 `shared_only` 与 `private_only` 并存 | `mixed` |
| 至少一个 `private_only`，没有 `shared_only`/`dual`（即使另有 unresolved） | `split_present` |
| 只有 `shared_only` + `unresolved`，或只有 `unresolved` | `unknown` |

`split_present` 表示已经有 private 正证据且不存在 shared Desktop；unresolved 不抹掉已经确认的 split。`all_shared` 必须要求全部实例可解析，不能把未知实例默认健康。

### 4.3 CordCode attachment 与最终 sync health

CordCode 主 connection 与 observer connection 必须按逻辑 client identity 分别采样，不能只用“peer 数量 >= 2”猜测身份：

```text
bridgeAttachment = shared | partial | absent | unresolved
```

- `shared`：main 与 observer 均被识别且命中同一 daemon；
- `partial`：两者中恰有一个已确认命中，另一个明确未附着/断开；
- `absent`：两者均明确未附着；
- `unresolved`：身份枚举或 FD 采样不足，不能下结论。

最终展示保留两个原始维度，并派生：

| bridgeAttachment | desktopAggregate | `syncHealth` | UI 语义 |
| --- | --- | --- | --- |
| `shared` | `all_shared` | `healthy` | 不显示警告 |
| `shared` | `desktop_absent` | `not_applicable` | 可选中性“未检测到 Codex App” |
| `shared` | `split_present` | `degraded` | 高警示：Desktop 未接共享 daemon |
| `shared` | `mixed` | `degraded` | 高警示：仅部分 Desktop 实例同步 |
| `partial` / `absent` | 任意 | `degraded` | 高警示：CordCode observer/main 未完整附着；不得归咎 Desktop |
| `unresolved` | 任意 | `unknown` | 中性诊断失败，不伪装成 split |
| `shared` | `unknown` | `unknown` | 中性诊断失败 |

状态发布需要实例 start-time 防 PID 重用、连续采样防抖与 freshness；防抖只延迟展示，不修改底层证据。恢复条件必须重新采到正证据，不能靠计时假定恢复。

### 4.4 五维机器诊断 schema

所有维度都携带 `sampledAtMonotonicMs`、`source`、`freshForMs`、`errorCode`；过期后转 `unknown/unresolved`。

| 维度 | 枚举 | 强证据/数据源 | UI 影响 |
| --- | --- | --- | --- |
| `topology.bridgeAttachment` | §4.3 四态 | logical client registry + FD peer | 参与 `syncHealth` |
| `topology.desktopAggregate` | §4.2 五态 | Desktop 实例枚举 + shared/private 正证据 | 参与 `syncHealth` |
| `seatHealth.daemon` | `running/stopped/unresolved` | `daemon version` + socket listener | 解释 bridge failure，不覆盖 topology |
| `seatHealth.launchAgent` | `healthy/missing/failed/unresolved` | launchd job 状态 | 解释恢复能力 |
| `attachConfig` | `enabled/disabled/unresolved` | 用户 launchd domain env | 仅提示下次启动配置，不证明当前实例 transport |
| `versionCompatibility` | `effective_compatible/probe_compatible/probe_incompatible/unknown` | shared FD；当前 embedded CLI probe 成功，或解析后的版本明确不兼容 | I/O/timeout/parse 失败为 unknown；不用 exact string 猜测，不覆盖 topology |
| `legacyProcess.managedLoopback` | `present/absent/unresolved` | 参数 + PID/start-time 扫描 | present 为遗留错误 |
| `legacyProcess.desktopPrivate` | `present/absent/unresolved` | §4.1 private 正证据 | 参与 Desktop 实例分类 |

版本字符串与 build/hash只作为 metadata。实际 shared FD 优先产生 `effective_compatible`；没有 probe 结果时必须 `unknown`。

## 5. 同步监视候选与实现现状

### 5.1 架构边界

未来监视器应内嵌 bridge、绑定 bridge epoch，复用既有 daemon seat、catalog discovery、event publisher、relay outbox、K4Patch fence 与 session registry。不得新建独立进程/IPC，不得新建 timeline 解析或投影路径。

严格区分：`generated ≠ publisher_enqueued ≠ transport_accepted ≠ client_applied`。direct 与 Relay 保留不同强度的终点；合并只能使用较弱名称。

### 5.2 指标库存

| 流水线/阶段 | 当前状态 | 现有/需要的 seam | 可声明语义 |
| --- | --- | --- | --- |
| official frame decoded → reducer applied → patch generated | `existing` | Projection Kernel/K4Patch 现有观测 | server projection progress |
| direct patch `publisher_enqueued` | `existing` | `event_publisher.go` `tryEnqueue` | best-effort connection queue accepted |
| direct `websocket_write_result` | `existing` | `SendJSONReport` / `write_post` | direct socket write attempt result |
| Relay `publisher_enqueued` | `existing` | publisher 调用 | 只证明交给 relay path |
| Relay `relay_writer_enqueued` | `needs_instrumentation` | unified relay writer 内正式计数点 | writer queue accepted；不是 socket written |
| Relay envelope write result | `needs_instrumentation` | 当前没有 completion seam | relay socket write attempt result |
| authoritative sampled → semantic changed → generation advanced | `existing` | discovery/fence 状态 | catalog server progress |
| catalog per-transport completion | `needs_instrumentation` | direct/Relay 分阶段计数 | transport-specific accepted/write result |
| client applied / ACK | `future_protocol` | capability-gated Mac canonical + iOS mirror | 仅客户端接受状态，不拥有 timeline/revision |
| monitor snapshot/API/badge | `future implementation plan` | owner/state machine/API 未冻结 | 不得在取证阶段实现 |

所有长期标签必须有界；thread/session ID 不能作为指标标签。`sessions_changed` 与 iOS `list_sessions` 非一一对应；zero-online 在无客户端时合法；prekey 只适用于 Relay。

## 6. 取证实验协议

### 6.1 实验 A：当前 build 拓扑对照

已完成：当前 build 静态 Attach Gate、shared Desktop 1 peer、CordCode runtime 2 个 matching peers。后者只证明 runtime 进程连接 shared daemon；按 §4.3 对 main/observer 做逻辑身份归属仍属于待补证据。

待补样本严格拆分：

1. `private_only` 实例：仅在用户批准启动隔离 Desktop 后，使用独立 `--user-data-dir` 与**进程级** `CODEX_APP_SERVER_FORCE_CLI=1`；不得修改全局 launchctl env。
2. `mixed` 聚合：owner shared Desktop 保持运行 + 上述隔离 private 实例。
3. `dual`：只在真实过渡/残留窗口出现时记录，不为制造样本注入或改 Desktop。
4. `split_present` 聚合：要求不存在任何 shared Desktop。若 owner 不主动退出，标为 `blocked_manual_owner_close`；不得忽略 owner PID 冒充 split。

安全约束：不使用 `pkill`；不终止 owner Desktop/daemon/CordCode；启动前记录隔离实例可执行路径、PID、start time、user-data-dir；清理仅覆盖该次明确拥有的隔离实例与目录。实验属于人工 UI 运行，执行 agent 必须遵守仓库的 UI 测试授权规则；本分析修订本身不运行该实验。

每个实例采集：PID/start time、父进程链、进程级 attach/force env、shared socket peer、private 正证据、最终实例分类。每个时点同时采 `bridgeAttachment` 与 `desktopAggregate`，不得只截单个进程。

### 6.2 实验 B：catalog 双 corpus

前置门：

- 当前 build Gate 为最新且通过；
- 受限 instrumentation 的定向测试通过；
- 一个 capability 正确的客户端持续连接，确认 `broadcaster.HasConnections()=true`；
- trace 默认关闭、显式开启，run 上限与脱敏自检生效；
- head 与 authoritative 各自从同源 wire 采样，不发生额外 RPC。

场景矩阵：

1. **idle**：至少 30 个 head 样本、5 个 `periodic_tick` authoritative 样本。按 60 秒 cadence，观察窗口至少约 5–6 分钟。
2. **active turn**：一个真实长 turn 持续跨越至少两个 authoritative interval；同时满足 head/full 样本并记录 generation。
3. **合法变化**：新建、rename、archive、delete 各执行一次；它们可先统一标成 `catalog_signal_coalesced`，但 authoritative tuple diff 必须能解释变化。

停止条件：样本计数达标；fetch 调用数与关闭 observer 时一致；无 observer drop/error；无敏感原文；没有越过 512 samples/1 MiB。任何一项失败，该 run 无效并保留失败码，不得补假样本。

裁决：

- idle/无业务变化时，semantic fingerprint 与 generation 不应反复推进；
- active turn 中，head order 应稳定；authoritative 若变化，必须定位到具体 `rowKeyHmac + fieldChangeMask + trigger + generation`；
- 合法变化必须在 authoritative corpus 内可解释；head/full 不跨 corpus diff；
- 若异常只落在 `catalog_signal_coalesced`，结论只能到该粒度，不能猜 lifecycle/manual；
- 证据决定后续是修上游生产者、调整 semantic fingerprint，还是仅增加防回归哨兵。证据前禁止抑制/熔断。

## 7. 安全、SSV2 与交付门

### 7.1 Session Sync v2 不变量

1. Projection Kernel 是 timeline、turn phase、plan/dock 和 revision 的唯一 owner。
2. 诊断、topology、catalog health 属于 control plane，不得合成 timeline 终态或推进 revision。
3. 不增加第二 writer，不终止官方进程，不接管 daemon 生命周期。
4. discovery observer 不成为第二 catalog source，不改变 fingerprint/fence/generation。
5. 指标只观测既有单一流水线；未来 ACK 只证明接受状态。
6. 任何跨端字段必须 capability-gated，并有 canonical pack、mirror、delivery、client acceptance 测试。

### 7.2 隐私与证据检查

- 被引用的 Gate 目录与未来 corpus 目录必须整体递归扫描，而不是只扫正文。
- 禁止个人绝对路径、hostname、installation/environment ID、raw session/thread ID、workspace path、title/message 入库。
- 旧敏感提交已从 refs 与本地对象库清除；普通 push 前仍以 `git rev-list --all`、递归内容扫描为准。不要用 `git log --grep <object-id>` 代替可达性检查。
- 临时解包/隔离 user-data-dir 不得提交；清理由执行者仅针对自己创建且已验证的目录。

### 7.3 取证 agent 完成标准

取证 agent 只在以下全部满足时交付：

- instrumentation 同源、只读、无第二次 fetch，单测通过；
- schema、上限、HMAC、错误与 dropped 语义符合 §3；
- 当前 build 的 private instance 与 mixed 样本完成，或按协议诚实标 blocked；
- catalog 三场景样本门达标并有字段/trigger/generation 因果裁决；
- 证据包递归脱敏通过；
- 未实现产品 monitor/API/UI/iOS 协议；
- 输出一份 evidence verdict，明确哪些假设被证实/排除/仍未知。

### 7.4 产品实现门

本文件**不能直接交给产品开发 agent 全量施工**。证据完成后必须另写 implementation plan，冻结：状态/API schema、owner、文件级任务、阈值/防抖、MacBridge UI、capability、direct/Relay 测试、Session Sync v2 回归、失败可见与回滚、completion report。

因此本版最终裁决是：**可以交给取证 agent；不能交给产品实现 agent。**

## 8. 代码与证据锚点

- Desktop 当前 Gate：[Gate Desktop Attach README](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md)
- discovery：`go-bridge/session_discovery.go`、`go-bridge/catalog_native_membership.go`、`go-bridge/catalog_wire_snapshot.go`
- catalog signal：`agent/codex-web/events.go` 与 core `CatalogRefreshSignals()`
- daemon seat：`MacBridge/MacBridge/Services/RuntimeManager.swift`
- delivery：`go-bridge/event_publisher.go`、`go-bridge/relay_connection.go`
- topology helper：`scripts/codex-web-phase0/verify_shared_daemon_topology.sh`（只复用 FD/PID 方法，不复用 exact-version 总门）
- 架构路线：`docs/2026-08-21-codex-web-backend-design.md` 的 shared official daemon、单 writer、Projection Kernel 与 Session Sync v2 护栏
