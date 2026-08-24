# codex-web 拓扑分裂与同步监视分析 v2 复审报告

- 复审日期：2026-08-24
- 被评审文档：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md)
- 被评审提交：`6161968`
- 上一轮评审：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-review.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-review.md)
- 复审范围：上一轮整改闭环、当前代码事实、Session Sync v2 边界、拓扑诊断可实施性、监控指标可观测性、提交历史安全
- 复审结论：**v2 已实质纠正上一轮路线错误，但仍有 4 个 P1、2 个 P2；可保留为取证分析稿，不能升级为实施计划。**

## 1. 上一轮整改确认

v2 对上一轮六项评审意见均作出了实质修订，不是文字性绕过：

1. catalog fingerprint 已按当前 `listOrderFingerprint` / `listSemanticFingerprint` 两条路径重写，旧 `wireFingerprint` 不再被误写成 codex-web 当前实现。
2. 裸 `codex app-server` 已恢复为多来源竞争解释，不再未经证据认定为 `daemon start` 派生本体。
3. topology 主判据已经从进程树改为实际 Unix socket FD/peer，进程树降为辅助证据。
4. iOS topology 展示已经延后，不再把 backend-global 状态塞入 session metadata 或 turn runtime status。
5. 监控指标已经从“raw 事件数必须等于 patch 数”改为 projection/catalog 分阶段流水线。
6. 监视器的 owner、epoch 重置、状态机、存储、API 和测试矩阵已明确列为实施前必须完成的设计项。

以上整改方向均可保留。本轮发现的是 v2 新增或遗留的事实与实施问题。

## 2. P1：推送前必须解决的问题

### P1-1：v1 的敏感信息仍会随 v2 的 Git 历史一起推送

文档 §5.2 写道：v1 提交 `93c2c70` 未推送，因此本修订直接替换 HEAD 后“不需要历史清理”。这项判断不成立。

`6161968` 是 `93c2c70` 的后代。Git 推送一个分支 tip 时会同时推送该 tip 可达但远端尚不存在的父提交。因此，即使 `93c2c70` 此前从未单独推送，未来推送 `6161968` 仍会把 v1 中的 hostname、installation ID、environment ID 和个人绝对路径带入远端历史。

#### 必须修订

1. 在推送当前分支前，把 v1/v2 重写或压缩成不包含敏感样本历史的提交。
2. 删除“v1 未推送，所以无需历史清理”的结论。
3. 历史修订完成前，不推送 `6161968` 或其后代。
4. 上一轮评审报告与本复审报告应在敏感历史处理后纳入最终文档提交，确保正文引用可以在干净检出中解析。

当前 `6161968` 只提交了分析正文，没有提交正文引用的上一轮评审报告；因此单独检出该提交时，文档开头的评审报告链接是断的。

### P1-2：daemon seat 恢复周期仍被错误写成 60 秒

文档 §2.1 和 §4.1 将 `ensure-codex-shared-daemon.sh` 描述成 60 秒周期，并据此认为它无法覆盖 Desktop 约 1 秒的首次 reconnect 窗口。

当前实现并非如此：

- [`MacBridge/MacBridge/Services/RuntimeManager.swift`](../MacBridge/MacBridge/Services/RuntimeManager.swift) 的 `CodexSharedDaemonSeat.recoverIntervalSeconds` 为 `0.25`。
- seat 脚本每 250ms 幂等执行 `codex app-server daemon start` 并刷新 launchd attach 环境。
- LaunchAgent 设置 `RunAtLoad=true` 和 `KeepAlive=true`。
- 60 秒是 `go-bridge/session_discovery.go` 的 catalog authoritative discovery 周期，不是 daemon seat 恢复周期。

因此，v2 对 topology split 发生窗口的描述仍建立在旧实现上。当前更准确的失效条件是：

- seat 尚未安装或 LaunchAgent 异常退出；
- standalone 缺失、daemon 启动持续失败；
- Desktop 在 attach 环境配置生效前已经锁入私有 stdio；
- Desktop 的版本或 daemon 兼容探测不满足；
- 某次 daemon 故障恢复仍未赶在 Desktop 的 reconnect probe 前完成。

#### 必须修订

将 §2.1 与 §4.1 的 60 秒座位描述改为当前 250ms KeepAlive seat，并按上述真实失效条件重写 topology split 威胁模型。60 秒只能保留在 catalog discovery 章节。

### P1-3：`shared/split/unknown` 三态不足以覆盖真实 Desktop 拓扑

文档 §3.3 按单一 Desktop 实例假设定义 `shared`、`split`、`unknown`。但官方 Desktop 可以：

- 完全没有运行；
- 同时运行主实例与隔离实例；
- 一个实例连接共享 daemon、另一个实例使用私有 stdio；
- 进程存在，但因权限或竞态无法读取 FD。

如果不区分这些情况：

- Desktop 未运行可能被错误显示为“未连接共享服务”；
- shared 与 private 实例并存时可能被整体误判为健康；
- 只检查某个 PID 会受到实例选择偏差影响。

#### 必须修订

先逐 Desktop 实例分类，再产生聚合状态。至少需要：

- `desktop_absent`
- `all_shared`
- `split_present`
- `mixed`
- `unknown`

现有 [`scripts/codex-web-phase0/verify_shared_daemon_topology.sh`](../scripts/codex-web-phase0/verify_shared_daemon_topology.sh) 已实现 daemon socket object 与指定 Desktop/runtime PID peer 的匹配；[`scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md`](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md) 已包含 shared 状态的 Desktop build、进程和 FD 证据。v2 应把这两项列为直接复用的基线，而不是重新设计同一套 peer 匹配逻辑。

新的证据缺口只有：

1. split/mixed 状态下的对照样本；
2. 产品运行时如何枚举和识别全部 Desktop 实例；
3. 实例消失、PID 重用和 FD 读取失败时的聚合规则。

### P1-4：监控流水线包含当前协议无法观测的客户端阶段

文档 §4.2 将以下阶段列入内嵌 bridge 监视器：

- projection：`client ack/applied`
- catalog：`client observed/applied generation`

当前协议没有这些确认信号：

- [`go-bridge/event_publisher.go`](../go-bridge/event_publisher.go) 的 `[K4Patch] delivered` 只表示 patch 已成功 enqueue 到连接 sink；不能证明 socket 已写出，更不能证明 iOS 已完成 reducer/render 应用。
- iOS 的 `sessionsChanged` 处理目前递增 `BackendCatalogGenerationStore` 的本地 generation，并发内部刷新通知；没有把 Mac 帧的 `catalogGeneration` 回报给 bridge。对应代码位于 iOS 仓库的 `OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`。

这与文档“第一阶段不承诺 iOS 改动”存在冲突：bridge 无法仅靠自身观察 client applied。

#### 必须修订

二选一：

1. 第一阶段只保留 server-observable 阶段，明确区分 `generated`、`enqueued`、`socket write completed`，并明确它们都不等于 client applied；或
2. 新建正式的 capability-gated acknowledgement/telemetry 协议，补齐 Mac canonical schema、iOS mirror、连接代际、重复 ACK 语义、超时语义、隐私/采样策略和双端测试。

若选择方案 2，它必须被明确列为 iOS 协议工作，不能继续宣称该阶段无需 iOS 改动。ACK 只能属于 control-plane，不能反向写 Projection Kernel 或制造第二个 timeline 状态源。

## 3. P2：取证与证据表达问题

### P2-1：不能把 head probe 与 authoritative snapshot 当成同一种相邻样本比较

文档 §2.3 要求连续保存 `thread/list` 响应并对相邻样本的完整 semantic tuple 做 diff，同时标记 3 秒 hint 和 60 秒 authoritative trigger。

两类请求的集合并不相同：

- 3 秒 head probe 是单页、最多 25 条，只服务于 `index|id` 变化提示；
- authoritative snapshot 使用完整有界分页，才拥有完整 semantic fingerprint。

如果直接按时间顺序比较两类响应，head 的 25 条与 full catalog 的数百条之间会产生大量假新增/删除。

#### 必须修订

建立两个独立 corpus：

1. **head corpus**：只在同类 head 样本之间比较 `index|id`；
2. **authoritative corpus**：只在同类完整样本之间比较 `index|id|updatedAtMillis|directory|projectId|title`。

当 head 变化触发 authoritative full refresh 时，用 correlation ID 或明确的时间/日志关联把两次请求组成一组，但不能跨请求类别直接做成员 diff。生命周期 refresh 与 60 秒 tick 都会触发 authoritative fetch，也需要独立 trigger 标识，否则仅凭相同 `thread/list` 请求无法区分生产者。

### P2-2：事实基线仍应绑定到可复核的版本化证据

文档 §2.1 的 Desktop transport 行为仍主要引用 `think.md`，而仓库已经有更强的版本化证据：

- Desktop build 与 bundle 版本；
- `app.asar` SHA-256；
- Desktop 内嵌 CLI 与 standalone CLI SHA/版本；
- Desktop local-daemon attach 条件；
- shared 状态的真实 FD peer；
- 可复跑的只读 topology gate。

这些证据位于 [`scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md`](../scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md)。v2 应直接引用该证据包并声明适用的 Desktop build，而不是只引用会继续演化的 `think.md`。

同理，`remote_control_client.rs` 的源码结论应记录所核实的 `<codex-repo>` commit 或对应官方版本，而不是只给出可移动的文件路径。否则未来官方源码更新后，文档中的“已核实”无法重放。

## 4. Session Sync v2 复审

v2 新增的硬约束总体符合 Session Sync v2：

- topology/health 均被限定在 control-plane；
- 不进入 `IngestLive`、timeline 或 Projection Kernel；
- 不增加 writer；
- 不自动终止或切换官方 daemon/Desktop；
- 不以 session metadata 或 fingerprint 携带诊断状态；
- 不为监控建立平行投影路径。

唯一需要补强的是 client acknowledgement：如果未来增加 ACK，ACK 只能衡量连接投递/客户端接受状态，不能成为 timeline 终态或 projection revision 的另一个 owner。Projection Kernel 仍由官方帧与既有 reducer 路径单向推进；客户端 ACK 不得反写或修改其内容状态。

## 5. 建议的 v3 修订顺序

1. 在推送前处理 `93c2c70 → 6161968` 的敏感 Git 历史。
2. 将上一轮评审和本轮复审报告纳入干净的最终文档提交，修复正文断链。
3. 把 daemon seat 周期修正为 250ms KeepAlive，并重写真实失效条件。
4. 将 topology 状态改为逐 Desktop 实例分类及聚合状态，直接复用现有 topology gate 的 FD peer 算法和 shared 样本。
5. 将 client applied 指标标为未来协议工作，或从第一阶段 server-only 监控中删除。
6. 把 fingerprint 实验拆成 head/full 两类 corpus，补 trigger correlation。
7. 将 Desktop 与官方源码结论绑定到具体 build、hash 和 commit。

## 6. v3 验收门槛

v3 满足以下条件后，可继续执行两组取证实验，但仍需实验结果才能升级为实施计划：

- 待推送历史中不包含 v1 的真实机器标识和个人绝对路径。
- 正文引用的两份评审报告均存在于提交树中。
- daemon seat 恢复周期与当前 `RuntimeManager.swift` 一致。
- topology 模型明确处理 Desktop absent、多实例和 mixed 状态。
- 复用已有 `verify_shared_daemon_topology.sh` 与 Desktop Attach Gate shared 证据。
- 第一阶段指标全部可由 bridge 当前协议观察；未来 client ACK 被明确标为协议扩展。
- head probe 与 authoritative snapshot 不跨集合直接 diff。
- Desktop transport 与官方源码结论均绑定可复核版本。
- 人为制造 split 的实验不终止 owner 正在使用的 Desktop/daemon，应使用隔离 Desktop 与隔离 user-data-dir，且明确实验性强制 stdio 仅用于取证、不得进入产品路径。

## 7. 最终裁决

v2 的主路线已经从“错误实现建议”恢复成“先取证、后设计”的正确方向，本轮不要求再次推翻整体结构。

但在上述 P1 修正前，文档仍不能作为仓库安全、事实准确的正式分析基线；尤其不能直接推送当前历史，也不能基于“60 秒 seat”或不存在的 client ACK 开始编码。

复审裁决：**需要 v3 定向修订；修订后继续 FD/peer split 对照与 head/full catalog 取证，证据闭环后再转实施计划。**
