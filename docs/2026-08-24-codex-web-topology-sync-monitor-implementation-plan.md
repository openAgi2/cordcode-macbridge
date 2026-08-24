# codex-web 拓扑与同步监视 —— 产品 implementation plan（v1）

日期：2026-08-24
依据：docs/2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md（v5）§4/§5/§7.4；
证据裁决：docs/2026-08-24-codex-web-catalog-forensics-evidence-verdict.md（二版）。
本计划只冻结设计、任务与门槛，**不包含产品施工**；施工须在本计划评审通过后另起执行。

## 0. 计划状态与前置证据

- 实验 B（catalog 双 corpus）：**已闭环**（verdict 二版，run2+run3 有效）。
- 实验 A（Desktop transport 拓扑人工实验）：**blocked_manual_owner_close**——按计划
  §7 作为**发布前人工门**由 owner 执行（隔离 Desktop + 进程级 force-stdio，见 v5 §6.1）；
  禁用目录过滤冒充 transport 证据；不终止 owner Desktop/daemon/CordCode，不使用 `pkill`。
- 因此本计划可以把 topology monitor 拆分为"证据采集/聚合（照 v5 §4.1–§4.3 规则）"与
  "私米样本验证（owner 人工门）"两段：采集逻辑可先行、人验门放在发布前。

## 1. 范围与边界

**做**：bridge 内嵌只读 monitor（v5 §4.4 维度采样）、管理 API snapshot、MacBridge 状态 UI
（badge/面板）、可选 capability-gated iOS 状态（Phase 2）、per-transport 完成指标（Phase 3）。
**不做**：新进程/IPC；新 timeline 解析或投影路径；第二 writer；合成/断言终态；推进
revision；管线级熔断；把 workspace 过滤信息当作 transport 证据；把 exact-version 比较
当作 topology 结论；把 `remoteControl/status/changed` / writer conflict 当 topology 证据。
**原则**（v5 §5.1/§7.1）：monitor 内嵌 bridge、绑定 bridge epoch、复用既有 daemon seat、
catalog discovery、event publisher、relay outbox、K4Patch fence、session registry；
`generated ≠ publisher_enqueued ≠ transport_accepted ≠ client_applied`，合并仅用较弱名称。

## 2. 冻结契约：状态与 API schema（v1）

### 2.1 Dimensions schema（§4.4 八维）

每个维度对象：`{ enum, sampledAtMonotonicMs, source, freshForMs, errorCode }`
（`errorCode` 枚举：`none` / `timeout` / `permission` / `rpc_failed` / `parse_failed` /
`process_missing` / `not_implemented`；过期后枚举转 `unresolved`/`unknown`，不得沿用旧值）。

| 维度 | 枚举值 | 数据源（冻结） |
|---|---|---|
| topology.bridgeAttachment | shared / partial / absent / unresolved | logical client registry + FD peer（按逻辑身份，非 peer 数猜身份） |
| topology.desktopAggregate | desktop_absent / all_shared / mixed / split_present / unknown | Desktop 实例枚举 + shared/private 正证据（§4.2 真值表） |
| seatHealth.daemon | running / stopped / unresolved | `codex app-server daemon version` + socket listener |
| seatHealth.launchAgent | healthy / missing / failed / unresolved | launchd job 状态 |
| attachConfig | enabled / disabled / unresolved | 用户 launchd domain env（仅提示下次启动配置） |
| versionCompatibility | effective_compatible / probe_compatible / probe_incompatible / unknown | shared FD；embedded CLI probe 或解析版本明确不兼容；无 probe 结果 = unknown |
| legacyProcess.managedLoopback | present / absent / unresolved | 参数 + PID/start-time 扫描（present = 遗留错误） |
| legacyProcess.desktopPrivate | present / absent / unresolved | §4.1 private 正证据（参与实例分类） |

### 2.2 syncHealth 派生（§4.3 表，全量实现）

`bridgeAttachment × desktopAggregate → syncHealth`：`healthy / not_applicable /
degraded / unknown`，UI 语义按 §4.3（degraded=高警示；unknown=中性"诊断失败"；
not_applicable=可选中性"未检测到 Codex App"；healthy=不显示）。

### 2.3 管理 API

`GET /internal/topology/snapshot`（Bearer token 同现有管理 API）返回：

```json
{
  "schemaVersion": "topology-monitor.v1",
  "bridgeEpoch": "…",
  "sampledAtMonotonicMs": 123456,
  "syncHealth": "healthy",
  "dimensions": { "<KEY>": { "enum": "…", "sampledAtMonotonicMs": 0, "source": "lsof_fd_peer", "freshForMs": 30000, "errorCode": "none" } },
  "instances": [ { "pid": 0, "startTime": "…", "classification": "shared_only", "evidence": { "sharedFd": true, "privateStdio": null } } ]
}
```

`schemaVersion=topology-monitor.v1`；`instances[].startTime` 要求 PID 重用防护；
任何采样失败只输出该维度 `errorCode` + `unresolved`，**不得**降级为 `split_present`。

### 2.4 owner 与生命周期

- 状态 owner：bridge 进程内单例 `MonitorState`，挂 `Handlers`，随 bridge 生灭；
- **无持久化**（内存重建，不写盘）；不修改 discovery 的 fingerprint/fence/generation；
- 读取现有 producer（daemon seat、discovery、event publisher、registry），不成为生产源；
- 采样循环独立 goroutine，cadence 见 §2.5；bridge epoch 变更（重启）即丢弃旧状态。

### 2.5 阈值、防抖与 freshness（冻结）

| 项目 | 值 |
|---|---|
| 采样 cadence | daemon/seat 30 s；FD peer 扫描 30 s；Desktop 实例枚举 60 s；launchd job 60 s |
| 展示防抖 | 连续 N=2 次采样一致才改变 snapshot 展示值；只延迟展示，不改证据 |
| freshness 上限 | FD/seat 30 s；实例/launchd 60 s；超限转 `unresolved`，恢复必须重新采到正证据（不按计时假定恢复） |

## 3. 文件级任务（Phase 1：monitor + API + MacBridge UI）

| 任务 | 文件（Mac 仓） | 内容 |
|---|---|---|
| T1 | `go-bridge/topology_monitor.go`（新） | 证据采集：实例枚举（复用 verify_shared_daemon_topology.sh 的 lsof FD-peer/匹配计数/PID 身份逻辑，**剔除 exact-version 单一 PASS/FAIL**）、private 正证据规则（§4.1 三选一）、各维度采样与 errorCode |
| T2 | `go-bridge/topology_aggregate.go`（新） | §4.2 实例聚合真值表（7 态）+ §4.3 syncHealth 派生表（全量）+ 防抖/freshness 状态机 |
| T3 | `go-bridge/topology_monitor_test.go`（新） | 真值表全量单测；防抖（N=2 一致才变化）；freshness 过期→unresolved；采样失败（permission/timeout）→ errorCode 且不产生 split；PID 重用防护测试 |
| T4 | `go-bridge/handlers.go` | 管理 API `GET /internal/topology/snapshot`（token 保护、schema 校验、bridge epoch 注入） |
| T5 | `go-bridge/main.go` | 挂载 monitor（`-topology-monitor=on|off` env/flag，默认 **on**） |
| T6 | `core/interfaces.go` | 状态类型定义（Dimensions/Instance/ErrorCode 枚举） |
| T7 | MacBridge/*（UI） | badge/面板：healthy=不显示；degraded=高警示（区分"Desktop 未接共享 daemon / 仅部分实例 / CordCode 未完整附着"文案）；unknown=中性"诊断失败"；not_applicable=可选"未检测到 Codex App" |

## 4. Phase 2/3（依赖门通过后另起执行，本计划先冻结接口）

- **Phase 2：capability-gated iOS 状态**——bridge 注册 capability `topology_status_v1`，
  canonical pack + mirror + client acceptance 测试（跨端字段必须 capability-gated，
  遵守 §7.1 不变量 6）；iOS 侧 UI 为另一仓库任务。
- **Phase 3：per-transport 完成指标**（v5 §5.2 needs_instrumentation 项）——
  `event_publisher.go` relay writer 入队/写出结果计数点；只加计数，不改变发送路径。

## 5. 门槛（每个 Phase 依次执行，全绿方可进入下一 Phase）

1. 定向单测（T3 全量 + API 契约）+ `go test ./go-bridge/` 全量 + `go vet`；
2. **Session Sync v2 回归**：现有 SSV2/投影测试套件全绿（监控不得触碰 timeline/revision）；
3. **direct/Relay 测试**：现有 direct/relay 回归全绿（publisher/K4Patch fence 不变）；
4. **失败可见检查**：人为制造采样失败 → 维度 unresolved + errorCode，UI 显示"诊断失败"
   而非 split/健康；禁用前必须 2 次一致恢复；
5. **owner 人工门（发布前）**：隔离 Desktop（独立 `--user-data-dir` + 进程级
   `CODEX_APP_SERVER_FORCE_CLI=1`）+ owner shared Desktop 并存 → 聚合判定与 UI 语义正确；
   private_only 单实例 → `split_present`；纯 shared → `all_shared`/healthy；
   **不使用 pkill；不终止 owner 进程；清理仅覆盖本次隔离实例**（v5 §6.1 安全约束）。

## 6. 回滚与失败可见

- monitor 由 flag/env 可整体关闭（默认 on）；关闭后管理 API 返回 501 + `not_implemented`，
  UI 不展示任何状态（不得把"关闭"伪装成 healthy）；
- 无持久化状态 → 进程重启即重建，无脏状态残留；
- snapshot 内每个维度独立 errorCode，UI 按 §2.5 过期规则展示，不把诊断失败当警报。

## 7. completion report 模板（交付时填写）

- 各 Phase 单测/回归/真机门结果（文件名 + 日期 + 结论）；
- §5 门槛清单逐项 通过/未通过 及证据锚点；
- 实验 A owner 人工门的分类结果与 evidence pack 路径；
- 回滚演练记录（关闭 flag → API 501 → UI 中性停用）；
- 对 v5 §4.4/§5.2 表项的勾稽（哪些维度/指标已由实现闭合、哪些保留 future）。
