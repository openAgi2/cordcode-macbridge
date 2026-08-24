# codex-web 拓扑分裂提示与共享服务同步监视 —— 可行性分析（修订 v4）

- 日期：2026-08-24（下午，真机验证波次之后）
- 状态：**取证分析稿（修订 v4），非实施计划**。v3 经复审裁定向修订；本版通过后即可执行两组取证实验，实验结果再决定 implementation plan。
- 评审报告：
  - v1 评审：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-review.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-review.md)
  - v2 复审：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v2-review.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v2-review.md)
  - v3 复审：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v3-review.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v3-review.md)
- 涉及仓库：
  - Mac：`cordcode-macbridge-codex-web`（bridge/MacBridge app/官方 daemon 协作面）
  - iOS：`cordcode-ios-codex-web-backend`（本版除「未来 ACK 协议工作」标注外不承诺任何 iOS 改动，见 §3.4/§4.2）
- 约束：Session Sync v2 护栏（timeline 真相 owner = Projection Kernel；control-plane 例外只用于非 timeline 面）。诊断器不得写 timeline、不得加 writer、不得改变官方 daemon 生命周期；未来客户端 ACK 只衡量投递/接受状态，不得成为 timeline 终态或 projection revision 的 owner。

## 0. 修订说明（v3 → v4）

| 复审项 | v3 问题 | v4 处理 |
| --- | --- | --- |
| P1-1 | 「本地对象已物理清除」不成立（`fsck --unreachable --no-reflogs` 仍列得出）；`git log --grep <commit-id>` 验证命令无效且提交信息含哈希产生误导 | §5.2 重写：区分「不可达、普通 push 安全」与「对象物理清除」；验证命令改为 `git rev-list --all` / `git cat-file -e` / `git fsck --unreachable --no-reflogs`；本次已实际执行物理清除并记录验证输出；本版提交信息不再包含旧哈希 |
| P1-2 | 复用 `verify_shared_daemon_topology.sh` 粒度太粗，整段复用会把 exact-version 门（与 patch-skew 产品策略冲突）带进 topology 状态 | §3.3 明确复用边界：只复用 FD peer（`lsof` + `matching_peer_count`）与 PID 身份验证；不复用 exact-version 单一 PASS/FAIL；诊断按维度分开（topology/seatHealth/attachConfig/versionCompatibility/legacyProcess），topology 聚合不被版本字符串差异覆盖 |
| P1-3 | 双 corpus 要求 trigger/correlation，当前代码不存在；又禁止证据闭环前写代码——执行死锁 | §2.3 采纳方案 1：允许严格隔离的只读诊断 instrumentation（取证桩），列出桩的边界与验收；「不进入编码」限定为产品功能编码；并记录零代码降级（`unknown-full-trigger` 不能归因生产者） |
| P2-1 | `socket write completed` 按 direct/Relay 统一命名；enqueue 与 write 合并后仍叫写出成功；`[K4Patch] delivered` 与 `delivered` channel 锚点混写 | §4.2 按传输分开：direct `publisher_enqueued → websocket_write_result`；Relay 终点只有 `relay_writer_enqueued`；合并一律用弱名 `transport_accepted`；锚点修正（patch `tryEnqueue`@709 vs control `delivered` channel@395-401） |
| P2-2 | 五态模型没有映射到 UI 行为（同一文案只适用 `split_present`） | §3.4 补五态 → UI 行为映射表；防抖与自动清除按聚合状态定义，不再统一 N 秒 |

本版对复审意见**全部采纳**。P1-3 在两个方案中以方案 1 为准（方案 2 作为降级记录）：根因定位的核心正是区分 authoritative refresh 的生产者，且评审给出的桩边界（只读、不改指纹/generation/timeline/投递/writer、默认关闭或限取证窗口、不记完整 title/message、有定向单测）满足 Session Sync v2 控制面边界。

## 1. 结论摘要

1. **想法 A（提示「Mac Codex App 未连共享服务」）方向可行**；`remoteControl/status/changed` 排除正确（§3.1）。拓扑诊断主证据必须是实际 Unix socket FD/peer（设计文档拓扑不变量），进程树只是辅助；split/mixed 对照样本采到之前，任何判据不得标为高置信度或进入实施。
2. **想法 B（同步监视）支持做**，形态为 bridge 内嵌监视器（独立进程需要新 IPC 与状态共享，收益为负）。监视器是「围栏/哨兵」，不是治疗：风暴根因未定位前只量化现象，不做抑制/熔断。
3. **实施顺序**：先完成两组证据闭环（①shared/split/mixed 的 FD/peer 对照，用隔离 Desktop，不停 owner 的进程；②head/full 双 corpus 采样与 trigger 对账——允许只读取证桩作为数据源，见 §2.3），再决定风暴修复与监视器指标阈值。「证据闭环前不进入编码」仅指产品功能编码，不禁止取证桩。
4. iOS 展示**本版明确延后**：第一阶段只做 MacBridge；iOS 扩展需要正式 backend-global control-plane 协议（§3.4），完成前不承诺。

## 2. 事实基线

### 2.1 拓扑分裂机理（按当前宿主与 seat 实现）

官方 Desktop（证据基线见 §5.3 证据包：ChatGPT `26.818.31338`/bundle `6892`、内嵌 CLI `0.149.0-alpha.4`）每次 `transport.connect()`——包括断线重连——都会跑 `codex app-server daemon version`。control socket 不在或探测失败时，Desktop 把 transport `kind` 写成 `stdio` 且 `supportsReconnect()` 变 false，该 Desktop 进程不再尝试 websocket——**锁死**，只能完整退出 Desktop 一次恢复（think.md 2026-08-22 条目为补充记录，版本化证据以 §5.3 为准）。

**Mac 侧 daemon seat 当前实现（不再是 60s）**：`MacBridge/MacBridge/Services/RuntimeManager.swift` 的 `CodexSharedDaemonSeat.recoverIntervalSeconds = "0.25"`：每 250ms 幂等执行 `codex app-server daemon start` 并刷新 launchd attach 环境；其 LaunchAgent 配置 `RunAtLoad=true`、`KeepAlive=true`（RuntimeManager.swift:101/114/127/129）。**60s 是 `go-bridge/session_discovery.go:33` 的 catalog authoritative discovery 周期，不是 seat 恢复周期。**

**版本兼容策略（patch skew 允许）**：RuntimeManager.swift:1249-1251 对 patch skew（如 Desktop alpha.4.1 vs standalone alpha.4）**只记录日志，仍启动官方 daemon 并安装 seat**；是否 attach 由 Desktop 自己基于 `daemon version` 与 app-server compatibility 决定。exact string equality 比 Desktop 官方 attach probe 更严格——**拓扑判据不得使用 exact-version 门**（§3.3）。

分裂实际发生的失效条件（据此重写的威胁模型）：
- seat 尚未安装或 LaunchAgent 异常退出（250ms 补位不运行）；
- standalone 缺失或 daemon 启动持续失败；
- Desktop 在 attach 环境生效前已经锁入私有 stdio（重连窗口约 1s，刷新 attach env 与 Desktop 重新探测之间仍有竞速）；
- Desktop 版本/daemon 兼容探测不满足（attach 条件为 local-daemon 分支硬要求，见 §5.3）；
- 某次 daemon 故障恢复仍未赶在 Desktop 的 reconnect probe 前完成。

分裂后的实际损失（代码与真机证据）：
- Desktop 的 turn 不进共享 daemon → bridge 观察泵/relay 收不到，iOS 看不到实时流；
- iOS「停止」走 `CancelTurnForThread`：liveCodec 观测不到 → cold 基线 `thread/read` 也找不到 → "no active turn"——**停止对分裂后的 Desktop turn 无效**；
- 现有文案已区分 `-32600 already has an active writer` 的两种成因之一（Desktop 已脱离共享 daemon）与二（Desktop 共享但持有某会话 writer）。注意：writer 冲突本身不作为拓扑判据（§3.3）。

### 2.2 catalog 目录发现机制（当前代码，`f8a66aa` 之后）

- **3s head hint**（廉价「要不要跑一次权威全量刷新」）：`codexDiscoveryHintFingerprint`（`go-bridge/session_discovery.go:172`）→ `listOrderFingerprint`（`go-bridge/catalog_native_membership.go:112`）。只含 `index|id`；注释明确其动机：「updatedAt 在流式 turn 中随每个 text_delta 变化，语义指纹会让提示在长任务执行期间每 3s 误触发一次全量刷新（2026-08-23 真机：codex-web 的 sessions_changed generation 1→108 风暴）」。head probe 是**单页、最多约 25 条**。
- **权威全量快照 / 60s 兜底扫描**：`snapshotBackendSession`（`session_discovery.go:198`）→ `listSemanticFingerprint`（`catalog_native_membership.go:78`）。包含 `index|id|updatedAtMillis|规范化目录|projectId|title`；comment 声明 presentation-only 的 pin/running 覆盖层被刻意排除；请求为**完整有界分页**。
- **trigger 现状（P1-3 事实基础）**：`snapshotBackendSession` 只区分 `seed` 与 `poll`（`session_discovery.go:200` tag），**不接收 trigger fetch kind，也无 correlation ID**；60s ticker（:131）、`CatalogRefreshSignals()`（:122）与 head changed（:165）最终都调用同一个 `snapshotBackendSession(..., false, ...)`。head changed 有一条「running authoritative full refresh」日志可做近似时间关联，但 lifecycle refresh 与 60s tick 的 full request 形状相同，当前日志无法可靠区分。
- `wireFingerprint`（`go-bridge/catalog_wire_snapshot.go:131`）只用于 Claude 等兼容 source 与 legacy 路径（`session_discovery.go:273/308/335`），**不是 codex-web 当前指纹**。
- codex-web 目录刷新事件集中在生命周期变化：`thread/started`、`thread/name/updated`、`thread/archived`、`thread/deleted`（`agent/codex-web/events.go:55/648/661`）；text_delta 不直接触发 catalog refresh。

### 2.3 fingerprint 风暴：已知与未验证 + 双 corpus 取证流程（含取证桩）

- 有记录的风暴：2026-08-23 真机窗口（代码注释记 generation 1→108；此前会话观察记 1→125，sessionCount 恒 187，iOS 只发过 1 次 `list_sessions` 且无反应）。**哪个窗口对应哪次采样、风暴在 `f8a66aa` 之后是否仍复现，本版不断言。**
- 未验证假设：60s 权威扫描的 `listSemanticFingerprint` 仍含 `updatedAtMillis`，若官方对无关成员刷新该字段，权威路径仍可能产生「成员不变、指纹变化」的假阳性刷新（head 3s 面已排除）。这是**假设**，不是根因定案。
- **暂不实施**：v1 建议的「噪声字段忽略 / 连续 N 次抑制 / fence 变化率熔断」——在不知道具体字段与生产者之前，这类抑制可能吞掉合法的新建、重命名、归档、目录或 recency 变化。本版维持删除，不作为候选。

#### 取证桩（P1-3 方案 1：允许，但严格隔离）

根因定位的核心是区分 authoritative refresh 的生产者（60s tick / lifecycle / head changed / 人工）。当前代码无此信息（§2.2），因此**允许**为取证加入只读 instrumentation trace seam，边界：

- 只记录 `triggerKind`、`sampleID/correlationID`、请求类别与脱敏 tuple diff；**不改变控制流、指纹、generation、timeline、投递与 writer**；
- bounded、脱敏、**默认关闭**或明确限定取证窗口；不记录完整用户 title/message；
- 不进入 timeline 或 Projection Kernel；不成为新的 catalog 数据源，不改变 generation 推进规则（Session Sync v2 复审 §4）；
- 有针对该 seam 的定向单元测试，证明 trigger 传递不改变 discovery 行为；
- 取证完成后决定：保留为正式观测（走 §4.2 指标流）或删除。

**降级路线（方案 2）**：若不允许任何桩，则每次 authoritative 请求的 trigger 只能标记 `unknown-full-trigger`，无法归因生产者——storm 定位只能停留在现象量化，不能给修复方案。本版以方案 1 为准。

#### 取证流程（两个独立 corpus，不跨集合 diff）

1. 建立 **head corpus**：只保存 head probe 响应（单页 ≤25 条），只在同类 head 样本之间比 `index|id`。
2. 建立 **authoritative corpus**：只保存权威全量响应（完整有界分页），只在同类完整样本之间比 `index|id|updatedAtMillis|目录|projectId|title`。
3. **不跨集合比较**：head 的 25 条与 full catalog 数百条之间没有可比性，直接按时序 diff 会产生大量假新增/删除。
4. head→full 因果对：由取证桩的 `correlationID`（或时间/日志关联）组成一组；成员 diff 仍在各自 corpus 内发生。
5. 每条 authoritative 请求由桩标注 `triggerKind`（3s hint / 60s tick / lifecycle / manual）——仅凭相同 `thread/list` 请求形状无法区分生产者，必须带上游标识（无桩时降级为 `unknown-full-trigger`）。
6. 对账 `catalogGeneration` 与 `sessions_changed` 的实际推进点，确认哪些字段的变化真正触发了 fence。
7. 只有确定具体字段与生产者之后，才允许决定：修改指纹、修复上游生产者、或加防回归哨兵。

## 3. 想法 A：Mac 端分裂状态提示

### 3.1 信号源排除：`remoteControl/status/changed`

抓帧样本（2026-08-24 13:31，共享 daemon 正常态，字段值已脱敏）：

```json
{"method":"remoteControl/status/changed","params":{
  "status":"connected",
  "serverName":"<host>",
  "installationId":"<redacted>",
  "environmentId":"<redacted>"},
 "emittedAtMs":1787549873935}
```

官方源码结论（**绑定可复核版本**）：codex 仓库 `codex-rs/app-server-daemon/src/remote_control_client.rs` 的 remote-control 状态通知语义，核实于官方仓库 HEAD `536f86e5`（2026-08-21，「Support attaching to existing realtime calls」）；该文件内 remote-control 状态通知相关逻辑的最近一次改动为 `f4e6aa70`（2026-06-24，「add daemon pairing command」）。语义：`remoteControl/enable` 远程控制功能的连接状态机（Connecting/Connected/…），服务端名=本机名——**与 Desktop 连的是共享 daemon 还是私有 stdio 无关**，不能作为分裂判据。

### 3.2 进程形态现场：只能定位候选，不能单独下结论

2026-08-24 正常共享态下与 codex 相关的 app-server 进程（13:40 复核，个人路径已脱敏为 `$CODEX_HOME`）：

```
 29750  1  10:23AM  $CODEX_HOME/packages/standalone/current/codex app-server --listen unix://
 32073 32061  1:17PM  codex app-server
```

- **PID 29750**：带 `--listen unix://` 参数——共享 daemon 本体的**置信度较高**（监听 control socket 的形态），但仍是间接证据：官方实现可能改变参数形态，且它只能证明「daemon 存在」，不能证明「Desktop 连接的是它」。
- **PID 32073**（裸 `codex app-server`，PPID=bridge runtime）：**无法仅凭形态归类**，至少三种竞争解释：
  1. bridge 的 catalog stdio 单例——`agent/codex/catalog_client.go:158` `connectStdio` 单例裸 `codex app-server` 子进程（代码注释：「只杀直属子进程，漏 codex app-server fork 的孙子进程」）；PPID 落在 bridge runtime 之下与之吻合；
  2. `daemon start` 派生的守护本体——`agent/codex-web/lifecycle.go:213` `runDaemonStart` 只执行命令并等待返回，**无源码证明裸进程为其派生**；
  3. legacy codex stdio session——`agent/codex/appserver_session.go` 同样裸跑 `codex app-server`。
- 当前**不存在** ChatGPT.app/Codex 框架谱系的 app-server 进程——只能佐证「本机此刻无 Desktop 私有进程」，是单点快照，无 split 对照。

### 3.3 判据、复用粒度与状态模型（P1-3 修订：只复用 FD peer 算法，不整段搬 Phase 0 gate）

**复用边界（评审 P1-2 必改）**——`scripts/codex-web-phase0/verify_shared_daemon_topology.sh` 是 Phase 0 验收脚本，包含与当前产品策略冲突的旧 gate，**只能复用其中的具体方法**：

- ✅ 复用：`lsof` 获取 daemon socket object（`daemon_objects`）与 `matching_peer_count` 的 FD peer 匹配（脚本 36-51 行的 lsof/awk 方法）；
- ✅ 复用：PID 身份验证（`ps -p <pid> -o command=` 匹配 Desktop/CordCode 应用路径）作为辅助诊断；
- ✅ 复用：managed-loopback 进程数清零检查（`app-server --listen ws://127.0.0.1` 计数 = 0）作为 legacy cleanup 辅助；
- ❌ **不复用**：脚本 21-23 行 `standalone_version = desktop_version` exact 字符串相等 PASS/FAIL——与当前 patch skew 策略冲突（§2.1：RuntimeManager 对 patch skew 只记日志仍启动 daemon/seat，由 Desktop 自己决定 attach）。把它并入 topology 状态会把允许的 patch skew 误报为分裂。

**诊断维度分开（不再折叠成单一 PASS/FAIL）**：

| 维度 | 含义 | 判定来源 |
| --- | --- | --- |
| `topology` | Desktop/runtime FD 是否命中共享 daemon | 实例级 FD/private/unresolved 证据 |
| `seatHealth` | daemon/LaunchAgent 是否健康 | `daemon version` running、seat 脚本/agent 状态 |
| `attachConfig` | launchd env 是否配置 | `launchctl getenv CODEX_APP_SERVER_USE_LOCAL_DAEMON` |
| `versionCompatibility` | 官方 attach probe 是否兼容 | Desktop 自决；差异只记录日志，**不覆盖 topology** |
| `legacyProcess` | 是否仍有 managed-loopback/private runtime | 进程扫描 |

拓扑聚合只由实例级证据产生（见下），不能被版本字符串差异覆盖。

**证据优先级**：

1. **主证据：实际 Unix socket FD/peer**——Desktop 与 CordCode 是否连接同一 control socket/daemon identity（设计文档拓扑不变量；Gate 8「FD peer 与 daemon 监听 socket 相同」）。
2. **辅助证据：进程树与命令行**——只用于定位候选进程；裸 `codex app-server` 多来源已证明不可单独定论。
3. **writer conflict 不作为判据**：其出现/消失依赖当前选择的 session、活跃 writer 与测试时序，不能证明 transport 类型。

**状态模型（先逐实例，后聚合）**——Desktop 可能缺席、可能同时运行主实例与隔离实例、可能 shared 与 private 并存：

- 实例级分类：`shared`（FD/peer 命中共享 daemon control socket）/ `private`（自身 stdio 或私有 process）/ `unresolved`（存在但 FD 读取失败、权限或竞态）。
- 聚合状态：`desktop_absent`（未发现任何 Desktop 实例）/ `all_shared`（所有实例均 shared）/ `split_present`（存在 private 实例且无 shared 实例）/ `mixed`（shared 与 private 并存）/ `unknown`（实例未枚举或证据不足，不得默认健康/分裂）。
- 聚合规则要求：产品运行时能枚举全部 Desktop 实例；实例消失、PID 重用、FD 读取失败的聚合规则单独定义；防抖只作用于状态发布。
- **证据缺口**（采到前不标高置信度）：①split/mixed 状态的 FD/peer 对照样本；②产品运行时如何枚举与识别全部 Desktop 实例；③实例消失/PID 重用/FD 读取失败的聚合规则。

### 3.4 UI 落点（第一阶段仅 MacBridge，五态映射）

- **MacBridge**：`MacBridge/Views/WorkspaceView.swift` codex-web 行已有同款提示样式（`codexDaemonConfigChanged` → 橙色 `codexConfigChangedHint`，inline @426），按此样式承载下述状态提示；「重启共享 Codex 服务」按钮旁保留，文案说明「重启 daemon 不能解锁已锁死的 Desktop」。

**五态 → UI 行为映射（P2-2 补足，不再用统一文案）：**

| 聚合状态 | 严重级 | 是否显示 | 用户动作 | 自动清除条件 |
| --- | --- | --- | --- | --- |
| `all_shared` | — | 不显示警告 | 无 | — |
| `desktop_absent` | 中性 | 不显示 split 警告；可选中性状态（如「未检测到 Codex App」） | 无强制动作 | 检测到 Desktop 实例即转下一状态 |
| `split_present` | 高 | 显示警示横幅 | 「请完全退出并重新打开 Codex App，恢复连至共享服务」 | 全部实例 FD 命中 daemon 后按防抖清除 |
| `mixed` | 高 | 显示警示横幅（文案：**部分** Desktop 实例未连共享服务，列实例数量或可识别信息） | 重启**仍为 private 的实例** | 不再存在 private 实例后按防抖清除 |
| `unknown` | 低 | 不伪装成 split；显示「诊断不可用/无法判断」或仅记日志 | 无强制动作；可引导检查 daemon | 证据齐备即转真实状态 |

- **防抖与清除按聚合状态定义**：`split_present`/`mixed` 需要「持续 N 秒」才显示（防止瞬时探测失败误报，N 值建议 15-30s，待用户体感确认）；`desktop_absent`/`unknown` 的判定抖动容忍可在瞬态稳定后短暂延迟，不与 split 的 N 共用窗口；任何状态清除都只在**新的实例级证据**出现后发生，不因超时自动消失。
- **iOS**：**本版不承诺**。若扩展，必须走正式 backend-global diagnostic 协议：Mac canonical schema、iOS mirror/Swift 类型、capability/version negotiation、delivery 与 client acceptance 测试；不得塞进 session metadata（污染目录事实语义）或 turn runtime status（诊断与生成状态错误耦合）；该状态不得进入 timeline、Projection Kernel 或 catalog 指纹。

### 3.5 实施边界（硬约束）

1. topology 与 monitor health 仅属 control-plane，不进入 `IngestLive`、timeline 或 Projection Kernel。
2. 诊断器只观察状态，不执行 daemon 切换、不终止官方进程、不抢占 active writer、不修改 session。
3. iOS 只消费 Mac canonical control-plane 状态，不自行轮询或本地进程推断 topology。
4. catalog health 不得通过修改 session metadata 或 fingerprint 表达。
5. timeline 指标必须来自既有单一流水线的阶段观测，不为监控新增平行解析/投影路径。
6. 所有跨端新增字段必须 capability-gated，并有 canonical pack、mirror、delivery、client acceptance 测试。
7. 未来客户端 ACK（若引入）只衡量连接投递/客户端接受状态，不得成为 timeline 终态或 projection revision 的另一个 owner；Projection Kernel 仍由官方帧与既有 reducer 路径单向推进。
8. 取证桩（§2.3）只观测 discovery 请求因果：只读、不改指纹/generation/timeline/投递/writer、不成为 catalog 数据源、不改变 generation 推进规则；默认关闭或限取证窗口；完成后决定保留（进指标流）或删除。

## 4. 想法 B：共享 codex 同步监视

### 4.1 现有「监视家底」（避免重复建设）

- daemon seat：250ms 幂等 `daemon start` + launchd attach 环境刷新（`RuntimeManager.swift` `CodexSharedDaemonSeat`，RunAtLoad/KeepAlive）；`daemon version` 探测。
- 目录发现：3s head hint（`listOrderFingerprint`）/ 60s 权威快照（`listSemanticFingerprint`）双路径；生命周期事件驱动的即时刷新。
- `event-publisher: live event has zero online targets` WARN、`relay-router prekey exhausted`、relay outbox。
- K4Patch fence/回放缓冲、被动泵 1:1 健康自采（62 delta = 62 passive event 的验证法）。
- 会话 registry（markRunning/markIdle）、控制面 RPC 失败摘要。

### 4.2 指标（P2-1 修订：按 transport 分阶段，弱名合并）

形态：bridge 内嵌健康监视器（goroutine，随 bridge 生命周期），管理 API 暴露 + MacBridge badge；独立进程不推荐（需新 IPC，与现有家底重复）。

**投递语义（锚点已按 P2-1 修正）**：

- projection patch：`[K4Patch] delivered` 日志（`event_publisher.go:709`）出现在 `sink.tryEnqueue(...)` 成功后——**best-effort 入队**（溢出可经 pull 恢复），不等于 socket 写出，更不等于客户端应用。**不要把它与 `delivered` channel 混写**。
- `delivered` channel（`go-bridge/event_publisher.go:395-401`，`EnqueueControl` wait=true）用于**等待型 control/result 发送**：有界槽 + 队列入队后等待该 frame 的写尝试完成（write loop 在 `:173` 写尝试后 close）——是另一条路径，不是 patch 交付证明。
- direct 连接写路径：`SendJSONReport(any) error`（`relay_connection.go:42-45` 注释「Prefer error-returning write so write_post can prove wire success」）——direct 可观测写的成功/失败。
- Relay 路径：`SendJSONClassified(any, relayOutboundClass)`（`event_publisher.go:142-144`）返回 void，生产路径只把任务交给 unified relay writer——**调用返回 ≠ relay WebSocket 写完成**。

**第一阶段指标（全部 bridge 可观测，按 transport 分开）：**

- projection 流水线：①官方 timeline frame decoded；②reducer applied / revision advanced；③patch（`baseRev → syncRev`）generated；
  - **direct**：④`publisher_enqueued`（tryEnqueue 成功）→ ⑤`websocket_write_result`（`SendJSONReport` 返回/`write_post` 观测）；
  - **Relay**：④`publisher_enqueued` → ⑤`relay_writer_enqueued`（当前无 written completion seam 时，终点只能到此，**不得叫 socket write completed**；若未来有 completion seam 再升级为 `relay_envelope_write_result`）。
  - 任何阶段合并都必须使用较弱的名称（如 `transport_accepted`），不得提升为更强的交付保证。
- catalog 流水线：①authoritative snapshot sampled；②semantic fingerprint changed；③`catalogGeneration` advanced；④generation enqueued per connection（同 projection 的 transport 细分）；（第一阶段无 client applied——见未来协议）。
- 明确标注：`generated ≠ enqueued ≠ transport_accepted ≠ client applied`。
- 分区与标签约束：至少按 backend、transport、bridgeEpoch、connection generation、事件类别、观察范围分区；标签值必须有界（session/thread ID 不得直接作为长期指标标签）。
- 语义纠正：`sessions_changed:list_sessions` 非一一对应（iOS 可防抖/合并/后台/多客户端）；`zero-online` 在无观察者时合法；`prekey` 只适用于 Relay，不能与 LAN 统一计数。

**未来协议工作（另行立项，本版不含实施承诺）**：客户端 ACK/telemetry 协议——Mac canonical schema、iOS mirror、连接代际、重复 ACK 语义、超时语义、隐私/采样策略、双端测试；capability-gated 且只属 control-plane。**该工作明确需要 iOS 端改动**，不再宣称「第一阶段无需 iOS 改动」。

### 4.3 监视器生命周期与状态机（实施前必须补齐的清单）

- owner 与 start/stop 时机（bridge 进程生命周期绑定）；
- bridgeEpoch、reconnect、backend restart 后的计数清零规则；
- 聚合窗口、阈值、防抖与冷启动宽限期；
- `healthy` / `degraded` / `unknown` 状态机与恢复条件；
- 有界标签与内存上限；
- snapshot schema、管理 API、capability；
- 保存位置、保留期限、脱敏规则；
- MacBridge/iOS 各自消费范围；
- 单元测试、集成测试、故障注入边界（重连、无客户端、Relay/LAN 切换等）。

### 4.4 实施顺序（先证据，后方案）

1. **证据闭环 ①**：shared/split/mixed 状态的 FD/peer 对照（隔离 Desktop + 隔离 user-data-dir，不停 owner 进程；强制 stdio 仅取证、不入产品路径）。
2. **证据闭环 ②**：head/full 双 corpus 采样与 trigger 对账（§2.3 流程，数据源为只读取证桩；无桩时降级 `unknown-full-trigger`），决定风暴是修上游、改指纹还是加哨兵。
3. 风暴修复：仅在字段与生产者确认后按真实路径处理；**不得先上抑制/熔断**。
4. 防回归哨兵：把确认的异常指纹变化率指标化 + 阈值告警。
5. 拓扑诊断（主证据管线 + 五维诊断拆分）+ MacBridge 展示（按 §3.4 五态映射）；iOS 等协议设计完成后再动。

## 5. 证据附录

### 5.1 已被排除的信号源

`remoteControl/status/changed`：官方远程控制功能状态，非拓扑信号（§3.1 样本 + 绑定 commit 的源码锚点）。

### 5.2 敏感信息处置与 Git 验证（P1-1 修正：两个事实层次分开）

**事实层次 1：远端普通 push 安全性——成立。** v1（含真实 hostname/installation ID/environment ID/个人绝对路径）与 v2 为本地提交，从未被任何 ref 覆盖；v3 已将它们从分支历史移除。验证命令（推送前后均可）：

```text
git rev-list --all | grep -c '^<old-object-id>'   # 期望输出 0
```

**事实层次 2：本地对象物理清除——本次已实际执行并验证。** v3 提交时对象仍可通过 `git cat-file -e` 读取（评审 P1-1 属实），本轮已执行：

```text
git reflog expire --expire=now --all
git gc --prune=now
```

验证输出（v4 提交时）：

```text
git cat-file -e 93c2c70^{commit}          -> 失败（对象不存在）
git fsck --unreachable --no-reflogs       -> 不再列出旧提交
git rev-list --all | grep -c '^93c2c70'   -> 0
```

**注意事项**：

- `git log --grep <object-id>` **不能**用于验证：它搜索的是提交说明文本，不是对象可达性；且提交说明若含该字符串会返回反向误导结论（v3 提交信息曾含哈希，本版已去掉）。
- `fsck --unreachable` 默认考虑 reflog；验证本地对象清除须用 `--no-reflogs` 或先 `reflog expire --all`。
- 对象清除属于仓库维护操作，只在「本地物理清除」作为验收目标时执行；若仅要求远端不泄露，「事实层次 1」已足够。
- 若未来执行破坏性 gc 需保留他人/他分支的 reflog 恢复点，应评估 `--all` 范围后再执行。

### 5.3 版本化证据绑定（P2-2）

- Desktop transport 行为：以 `scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md` 为准（采集 2026-08-22）：Desktop ChatGPT `26.818.31338`（bundle `6892`）、内嵌 CLI `codex-cli 0.149.0-alpha.4`、standalone CLI `0.149.0-alpha.4`、`app.asar` SHA-256 `7db5508d…54`、内嵌 `codex` SHA-256 `10afbedd…19b`；local-daemon 分支的六项 attach 条件；活体验证：`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1` + 隔离 `--user-data-dir` 的隔离 Desktop 以 WebSocket（`app-server-control.sock`）连接。**本分析只对上述 build 声明**；build/SHA 变化后需重采证据。`think.md` 2026-08-22 条目仅作补充记录。
- 官方源码结论：§3.1 已绑定 codex 仓库 commit（HEAD `536f86e5` 2026-08-21；remote-control 通知最近改动 `f4e6aa70` 2026-06-24），不以可移动文件路径为准。

### 5.4 代码锚点（当前 HEAD）

- fingerprint：`go-bridge/session_discovery.go`（`codexDiscoveryHintFingerprint` @172、`snapshotBackendSession` @198，仅 seed/poll @200、wireFingerprint 仅兼容/legacy @273/308/335）；`go-bridge/catalog_native_membership.go`（`listSemanticFingerprint` @78、`listOrderFingerprint` @112）。
- 目录刷新事件与 trigger 现状：`agent/codex-web/events.go`（`signalCatalogRefresh` @55/648/661）；`session_discovery.go`（`CatalogRefreshSignals` @122、60s ticker @131、head changed @165 —— 全走同一 `snapshotBackendSession`，无 trigger/correlation）。
- 进程谱系：`agent/codex-web/lifecycle.go:213` `runDaemonStart`；`agent/codex/catalog_client.go:158`（单例 stdio）；`agent/codex/appserver_session.go`（legacy）。
- daemon seat 与版本策略：`MacBridge/MacBridge/Services/RuntimeManager.swift`（`recoverIntervalSeconds="0.25"` @101；RunAtLoad/KeepAlive @127-129；patch skew 只记日志仍启动 daemon/seat @1249-1251）。
- 投递语义：`go-bridge/event_publisher.go`（`[K4Patch] delivered` = patch `tryEnqueue` 成功 @709；`delivered` channel = `EnqueueControl` 等待型 control/result @395-401/173；`SendJSONClassified` void vs `SendJSONReport(error)` @142-150）；`go-bridge/relay_connection.go:42-45`（direct 写错误观测）。
- topology gate 复用方法：`scripts/codex-web-phase0/verify_shared_daemon_topology.sh`（exact-version @21-23 **不复用**；`lsof` socket objects + `matching_peer_count` @36-65 **复用**；managed-loopback @30-31 辅助）。
- 拓扑不变量：`docs/2026-08-21-codex-web-backend-design.md`（FD/peer 指向同一 control socket）；Gate 8 与 T0 宿主拓扑门。
- iOS 侧（仅引用，不在本仓）：`OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`（`sessionsChanged` 本地 generation 失效，无回报）。

### 5.5 评审验收对照

v3 复审 §6 验收门槛 8 条 → 本版对应：`rev-list --all` 不含旧敏感提交且本地对象已实际清除（→ §5.2，命令与验证输出）；不再使用 `git log --grep`（→ §5.2）；topology 状态不受 CLI exact-version 覆盖（→ §3.3）；明确复用脚本的具体函数/证据、不整段搬 Phase 0 gate（→ §3.3 复用边界）；双 corpus 存在现实可执行的 trigger/correlation 数据源（→ §2.3 取证桩方案 1）；诊断桩不改变 catalog 与 SSV2 真实路径（→ §3.5 硬约束 8）；direct/Relay 阶段名称与保证一致（→ §4.2）；五个聚合状态都有 UI 展示与恢复行为（→ §3.4 映射表）。

## 6. 待验证实验（v3 复审裁决：v4 通过后执行）

1. **FD/peer 对照（含安全约束）**：正常 shared 态与人为 split/mixed 态各采一组——用**隔离 Desktop（独立 `--user-data-dir`）与隔离环境**制造对照，**不终止 owner 正在使用的 Desktop/daemon**；实验性强制 stdio 仅用于取证，不进入任何产品路径。样本采齐前，拓扑判据不进编码。
2. **风暴再验证**：当前代码（`f8a66aa` 之后）按 §2.3 双 corpus 采样 1-2 分钟（trigger 由取证桩标注），确认风暴是否仍复现；若复现，逐字段 diff 定字段与生产者。
3. **防抖窗口定值（按聚合状态）**：`split_present`/`mixed` 的「持续 N 秒」建议 15-30s（桌面探测失败窗口 2.5s spawn timeout + 重连频率），待用户体感确认；`desktop_absent`/`unknown` 的容忍窗口单独定义，不共用 N。
4. **iOS 协议设计**（仅当第一阶段 MacBridge 被接受、且需要 iOS 时另立任务）：backend-global diagnostic schema + capability negotiation + 两端测试。
