# codex-web 拓扑分裂与同步监视分析 v3 复审报告

- 复审日期：2026-08-24
- 被评审文档：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md)
- 被评审提交：`fd9d7b1`
- 上一轮复审：[`2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v2-review.md`](./2026-08-24-codex-web-topology-split-and-sync-monitor-analysis-v2-review.md)
- 复审范围：v2 整改闭环、历史清理可重放性、topology gate 复用边界、双 corpus 取证可执行性、server-only 指标口径、Session Sync v2 护栏
- 复审结论：**v3 已完成上一轮要求的结构性整改，但仍有 3 个 P1、2 个 P2；修订后可认定为可执行取证稿，仍不能直接升级为实施计划。**

## 1. 已确认通过的整改

以下事项已经通过复核：

1. 三份文档均已纳入 `fd9d7b1` 的提交树，分析正文中的两份评审链接可在干净检出中解析。
2. 三份文档未再检出个人绝对路径及 v1 中已知的真实 machine/environment 标识值。
3. `93c2c70` 与 `6161968` 均已从 `git rev-list --all` 的可达历史移除；普通分支 push 不会发送不可达对象。
4. daemon seat 已正确改为 250ms、`RunAtLoad`、`KeepAlive`，60 秒只保留在 catalog authoritative discovery 语境。
5. topology 状态已改为逐 Desktop 实例分类，并覆盖 absent、多实例、private/shared 混合与 unresolved。
6. 第一阶段已删除 client applied/ACK 指标；未来 ACK 被明确列为需要 iOS 改动的 capability-gated control-plane 协议项目。
7. head probe 与 authoritative snapshot 已拆成两个 corpus，不再跨集合做成员 diff。
8. Desktop transport 证据已绑定 ChatGPT build、bundle、CLI 版本与 hash；remote-control 源码结论已绑定官方 codex commit。
9. Session Sync v2 的 writer、timeline owner、Projection Kernel 与 control-plane 边界保持正确。

v3 的主路线已经稳定。本轮问题集中在“证明是否真实完成”和“按文档能否直接执行取证”。

## 2. P1：必须修订的问题

### P1-1：旧提交已不可达，但“对象已物理清除”的验收事实不成立，且文档给出的复核命令无效

本轮只读核验结果：

```text
93c2c70: not reachable from --all, object still present
6161968: not reachable from --all, object still present

git fsck --unreachable --no-reflogs:
  unreachable commit 93c2c70...
  unreachable commit 6161968...
```

因此应区分两个结论：

- **远端普通 push 安全性**：成立。两个旧提交已经不可达，普通 push 不会发送它们。
- **本地对象物理清除**：尚未成立。`git cat-file -e` 仍能读取两个对象，`git fsck --unreachable --no-reflogs` 仍能列出它们。

分析文档 §5.2 建议用 `git log --grep 93c2c70` 验证历史清理，这个命令也不能完成目标：`--grep` 搜索的是提交说明，不是对象 ID 或可达性；当前 `fd9d7b1` 的提交说明本身含有字符串 `93c2c70`，所以该命令返回的是 `fd9d7b1`，会产生相反的误导。

#### 必须修订

1. 文档明确区分“不可达、不会被普通 push 发送”与“本地 object 已物理删除”。
2. 将 push 前可达性检查改为等价于：

   ```text
   git rev-list --all | grep '^<old-object-id>'
   ```

   预期无输出。
3. 如果验收目标还包括本地物理清除，则独立检查：

   ```text
   git cat-file -e '<old-object-id>^{commit}'
   git fsck --unreachable --no-reflogs
   ```

   当前现场仍不满足这一目标。是否再次执行对象清理属于破坏性仓库维护操作，应单独处理，不应在分析文档里写成已完成事实。
4. 若需求仅为确保远端不泄露，当前“旧提交不可达”已经满足；不要把本地对象仍存在误写成普通 push 会泄露。

### P1-2：现有 topology gate 只能复用 FD peer 算法，不能整段作为产品诊断基线

文档 §3.3 写“直接复用 `verify_shared_daemon_topology.sh`，不重新设计 peer 匹配”。复用 peer 匹配方向正确，但现有脚本还包含与当前产品策略冲突的旧 gate：

- [`scripts/codex-web-phase0/verify_shared_daemon_topology.sh`](../scripts/codex-web-phase0/verify_shared_daemon_topology.sh) 第 21–23 行要求 Desktop 内嵌 CLI 与 standalone 的 `--version` 字符串完全相等，不相等立即失败。
- [`MacBridge/MacBridge/Services/RuntimeManager.swift`](../MacBridge/MacBridge/Services/RuntimeManager.swift) 的当前产品逻辑明确说明 exact string equality 比 Desktop 官方 attach probe 更严格；patch skew 只记录日志，仍启动官方 daemon/seat，由 Desktop 自己基于 `daemon version` 和 app-server compatibility 决定是否 attach。
- 脚本还把 attach env、managed-loopback 清零、CLI exact version、daemon running 和 FD peer 合并成单一 PASS/FAIL；这些分别属于 configuration、legacy cleanup、availability 与 actual topology，不应被折叠成一个 topology 状态。

如果产品监视器直接执行整段脚本，允许的 patch skew 会被误报为 topology failure；即使 Desktop 已经通过 FD peer 证明处于 `shared`，脚本也会在检查 peer 前提前失败。

#### 必须修订

明确“复用”的粒度：

1. 复用 `lsof` 获取 daemon socket object 和 `matching_peer_count` 的 FD peer 方法。
2. 复用 PID 身份验证与零 managed-loopback 检查作为辅助诊断。
3. 不复用 exact-version PASS/FAIL 作为 topology 判据。
4. 将诊断维度分开：
   - `topology`: Desktop/runtime FD 是否命中共享 daemon；
   - `seatHealth`: daemon/LaunchAgent 是否健康；
   - `attachConfig`: launchd env 是否配置；
   - `versionCompatibility`: 官方 attach probe 是否兼容；
   - `legacyProcess`: 是否仍有 managed-loopback/private runtime。
5. topology 聚合只能由实例级 FD/private/unresolved 证据产生，不能被版本字符串差异覆盖。

这不是重新造 peer 算法，而是避免把 Phase 0 验收脚本中的其他历史 gate 错当成产品运行时状态机。

### P1-3：双 corpus 实验要求当前代码不存在的 trigger/correlation，但文档同时禁止在证据闭环前写代码

文档 §2.3 要求：

- head→full 使用 correlation ID；
- 每条 authoritative 请求标记 60s tick、生命周期事件或人工 refresh trigger。

当前 [`go-bridge/session_discovery.go`](../go-bridge/session_discovery.go) 不具备这些信息：

- 60s ticker、`CatalogRefreshSignals()` 与 head changed 最终都调用相同的 `snapshotBackendSession(...)`。
- `snapshotBackendSession` 只区分 `seed` 与 `poll`，不接收 trigger，也没有 correlation ID。
- head changed 有一条“running authoritative full refresh”日志，可以做近似时间关联；但 lifecycle refresh 与 60s tick 的 full request 形状相同，当前日志无法可靠区分二者。

与此同时，文档状态和实施顺序写的是“两组证据闭环前不进入编码”。因此按当前文字，实验既要求不存在的观测字段，又不允许加入观测字段，形成执行死锁。

#### 必须修订

二选一：

1. **允许严格隔离的诊断 instrumentation**：在产品功能编码前，可以加入不改变 fingerprint、generation、timeline、投递和 writer 的只读 trace seam，仅记录 `triggerKind`、`sampleID/correlationID`、请求类别与脱敏 tuple diff；取证完成后决定保留为观测还是删除。
2. **保持零代码取证**：放弃“每次请求都有精确 trigger/correlation”的要求，只使用现有 head 日志、请求 limit/cursor 和时间窗口，并明确 lifecycle 与 60s tick 只能得到 `unknown-full-trigger`，不能据此归因生产者。

推荐方案 1，因为根因定位的核心正是区分 authoritative refresh 的生产者。该 instrumentation 必须满足：

- 只读，不改变控制流与 fingerprint；
- bounded、脱敏、默认关闭或明确限定取证窗口；
- 不记录完整用户 title/message；
- 不进入 timeline 或 Projection Kernel；
- 有定向单元测试证明 trigger 传递不改变 discovery 行为。

文档应把“未进入产品实施”与“允许诊断桩取证”区分开，避免再次用时间猜测代替因果证据。

## 3. P2：需要在转实施计划前补足的问题

### P2-1：`socket write completed` 必须按 direct/Relay 分开，不能与 enqueue 合并后仍使用同一语义

文档 §4.2 将第一阶段定义为 server-observable，并列出 `socket write completed`；同时允许在当前无法观察时合并 enqueued 与 socket write 阶段。

当前代码的传输事实并不一致：

- direct connection 可以通过 `SendJSONReport` 得到 WebSocket `WriteJSON` 返回值，因此可以观测 direct write success/failure。
- Relay `SendJSONClassified` 返回 `void`；生产路径通常只把任务 enqueue 到 unified relay writer，调用返回不等于 relay WebSocket write completed。
- `[K4Patch] delivered` 日志只表示 `eventOutboundSink.tryEnqueue` 成功。

因此“全部 bridge 可观测”可以成立，但必须按 transport 使用不同的阶段名和观测点；把阶段 ④⑤合并后仍叫 socket write completed 会制造假成功。

#### 建议修订

- direct：`publisher_enqueued → websocket_write_result`；
- Relay：`publisher_enqueued → relay_writer_enqueued → relay_envelope_write_result/mailbox_state`；
- 若第一阶段暂时没有 Relay writer completion seam，终点只能叫 `relay_writer_enqueued`，不能叫 socket write completed；
- 任何阶段合并都必须使用较弱的名称，例如 `transport_accepted`，不得提升为更强的交付保证。

另外，文档把 `[K4Patch] delivered` 与 `eventOutboundFrame.delivered` channel 的代码锚点写在一起并不准确：projection patch 的 delivered 日志来自 patch enqueue 分支，`delivered` channel 主要用于等待型 control/result 发送。语义结论“只是入队”正确，但源码锚点应改为 projection patch 的 `tryEnqueue` 与对应日志位置。

### P2-2：五态模型尚缺到 UI 行为的确定映射

文档 §3.3 已定义：

- `desktop_absent`
- `all_shared`
- `split_present`
- `mixed`
- `unknown`

但 §3.4 仍只有统一文案“Codex App 当前未连共享服务（拓扑分裂）”。该文案只适用于 `split_present`，对其他状态不成立：

- `mixed`：必须指出部分 Desktop 实例仍为 private，否则用户可能只重启了错误实例；
- `desktop_absent`：不是 topology split，通常不应显示错误横幅；
- `unknown`：应显示“无法判断/诊断不可用”，不能要求用户重启 Desktop；
- `all_shared`：不显示警告。

#### 建议修订

在分析稿中补一个最小映射表：聚合状态、严重级、是否显示、用户动作、自动清除条件。至少明确：

| 状态 | UI 行为 |
| --- | --- |
| `all_shared` | 不显示警告 |
| `desktop_absent` | 不显示 split 警告；可选中性状态 |
| `split_present` | 显示重启全部 private Desktop 实例的警告 |
| `mixed` | 显示“部分实例未连接共享服务”，列实例数量或可识别信息 |
| `unknown` | 显示诊断不可用或仅记录日志，不伪装成 split |

防抖和状态自动清除也应按聚合状态定义，而不是给所有状态使用同一个 N 秒窗口。

## 4. Session Sync v2 复审

v3 没有新增 Session Sync v2 架构违规：

- topology、monitor health 与未来 ACK 均被限定在 control-plane；
- 不写 timeline，不进入 `IngestLive` 或 Projection Kernel；
- 不增加 writer，不终止/切换官方 runtime；
- iOS 不自行推断 Mac 进程拓扑；
- catalog diagnostic 不进入 session metadata 或 fingerprint；
- ACK 不拥有 projection revision 或 turn terminal state。

若采纳 P1-3 的诊断 instrumentation，它也必须保持上述边界。trigger/correlation 只能观测 discovery 请求因果，不能成为新的 catalog 数据源或改变 generation 推进规则。

## 5. 建议的 v4 修订顺序

1. 修正文档中的 Git 验证命令，区分“不可达、普通 push 安全”与“本地对象物理清除”。
2. 将 topology gate 的复用范围限定为 FD peer/PID 识别，不复用 exact-version 单一 PASS/FAIL。
3. 明确允许只读诊断 instrumentation，或诚实降低 trigger 取证精度；推荐前者。
4. 按 direct/Relay 分开 server-observable 交付阶段，删除 enqueue/socket-write 合并后的强语义。
5. 补齐五态到 MacBridge UI 的行为映射。
6. 完成后执行 split/mixed FD 对照与 head/full 双 corpus 实验；只有证据闭环后才转实施计划。

## 6. v4 验收门槛

- `git rev-list --all` 不包含旧敏感提交；若声称本地对象已清除，则 `git cat-file` 与 `git fsck --unreachable` 也必须支持该声明。
- 不再使用 `git log --grep <object-id>` 验证提交可达性。
- topology 状态不受 CLI exact-version 字符串差异直接覆盖。
- 明确复用 topology script 的具体函数/证据，不把整个 Phase 0 gate 直接搬入产品监视器。
- 双 corpus 实验存在现实可执行的 trigger/correlation 数据源。
- 诊断 instrumentation 不改变 catalog 或 Session Sync v2 真实路径。
- direct 与 Relay 的完成阶段名称和保证与当前传输实现一致。
- 五个聚合状态都有确定的 UI 展示与恢复行为。

## 7. 最终裁决

v3 已完成从错误方案到正确取证路线的主要收敛，旧评审的架构问题已经解决。本轮不要求改动主路线，也不要求提前开发 topology monitor。

但当前仍不能开始两组正式取证：trigger/correlation 的观测来源尚未解决；也不能把现有 Phase 0 topology gate 整体搬进产品，因为 exact-version gate 会与当前 patch-skew 策略冲突。

复审裁决：**需要 v4 小范围但实质性的可执行性修订；v4 通过后即可执行取证实验，实验结果再决定 implementation plan。**
