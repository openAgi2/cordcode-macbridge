# codex-web topology monitor —— 产品 implementation plan（v2）

日期：2026-08-24（v2，按 [v1 评审报告](./2026-08-24-codex-web-topology-sync-monitor-implementation-plan-review.md) 修订）
依据：v5 分析 §4/§5/§7.4；证据 verdict 二版；v1 评审 8×P1 + 4×P2。
v2 裁决（先冻结，避免实现 agent 临场决定）：
- **Phase 1 仅实现 topology monitor**（P1-7 选优：改名，标题不再承诺 sync monitor）；catalog/per-transport health 依据 verdict run3 判定（低频 updatedAt churn 不为故障、不改 fingerprint）→ **另立证据与计划**，本计划不含其任务。
- **临时 catalog forensics observer：产品发布前删除运行时路径**（P1-8 选优：保留提取器/测试工具与已脱敏 verdict，删除 observer 代码、env seam 与插点）。
- **禁用语义采用 always-200 + `state=disabled`**（P2-4 选优，拒绝 501：避免管理客户端把非 2xx 一律泛化）。
- 本计划只冻结设计与任务；**不施工**。评审通过后按 §5 开发顺序执行，不得提前。

## 1. 范围与边界（Phase 1 = topology monitor）

**做**：含 provider、聚合状态机、Darwin 证据采集器、管理 API snapshot、MacBridge 解码/轮询/UI 的只读 topology monitor。
**不做（本 Phase）**：catalog health（fingerprint/generation/updatedAt churn 的监视指标）、per-transport delivery 指标、iOS capability 协议；新进程/IPC；timeline/投影路径；第二 writer；合成终态；推进 revision；熔断；把 workspace 过滤当 transport 证据；exact-version 单一 PASS/FAIL；`remoteControl/status/changed` 或 writer conflict 当 topology 依据。
**原则**（v5 §5.1/§7.1）：monitor 内嵌 bridge、绑定 bridge 数值 epoch、只读消费既有 producer；`generated ≠ publisher_enqueued ≠ transport_accepted ≠ client_applied`。

## 2. 冻结契约

### 2.1 Provider seam（评审 P1-1；Phase 0 优先任务 S0）

**现状锚点**：`agent/codex-web` 已有 `ConnectionEpoch int64`（codexweb.go:48）、`Client.Epoch()`、passive observer 连接（events.go:680）；但**不存在**把 main/pump 与 observer 的角色、连接状态与 FD/endpoint 关联暴露给 monitor 的只读接口。先立 S0，T1 不得临场发明。

`core/interfaces.go`（跨 agent 的 optional provider；HTTP DTO 不进 core）：

```go
// CodexWebTransportIdentityProvider 只读暴露 codex-web 后端的 main/pump 与 observer
// 传输身份快照。monitor 只消费快照，不构造连接。
type CodexWebTransportIdentityProvider interface {
	TransportIdentitySnapshot(ctx context.Context) (CodexWebTransportIdentity, error)
}

type CodexWebTransportIdentity struct {
	Epoch       int64  // 数值 epoch（与 Management v1 runtime identity 同形）
	Endpoint    string // UDS endpoint（实际 transport 的连接对象身份）
	Main        CodexWebTransportRoleState
	Observer    CodexWebTransportRoleState
	SampledAtMs int64 // 完成采样时刻（monotonic ms）
}

type CodexWebTransportRoleState struct {
	Attached  bool   // provider 侧确认在连
	Epoch     int64  // 该角色连接代际；0=从未建立
	PeerKey   string // 可关联 FD/peer 的身份（如 epoch+endpoint 的组合键）；空=不可用
	ErrorCode string // none|timeout|rpc_failed|unknown
}
```

**FD 语义**：main 与 observer 同处 bridge PID，进程级 lsof 只给出 peer 集合；角色- FD 对应（哪个 peer 属于谁）由 provider 的 `PeerKey` + lsof 连接的 socket object 共同判定——**数量 ≥2 永远不作身份判定**（v5 §4.3）。若两者对不上：`Attached=true 但 lsof 无匹配 peer` → 该角色 `unresolved`。
**S0 测试**：main-only、observer-only、两者 shared、断线重连（epoch 递增）、stale epoch、provider 错误 → unresolved。
**S0 交付文件**：`agent/codex-web/transport_identity.go`（新，只读实现）、`core/interfaces.go`（provider 接口）、`agent/codex-web/transport_identity_test.go`。

### 2.2 Desktop 实例证据流水线（评审 P1-2；逐实例、可执行）

每实例（枚举→证据→分类）严格按序，任一步失败进 `unresolved`+errorCode，**不得先到者覆盖**：

1. **枚举**：以 `ps -axo pid,lstart,command` 找 Desktop 主进程（按可执行路径 + `--user-data-dir` 归属）；候选记录 `pid/startTime/cmdline-hash`（cmdline 仅作候选；不落盘敏感内容）。
2. **绑定**：start-time 与 PID 组合作为一次性身份（防火 PID 重用）；每采样重新验证。
3. **后代归属**：递归（非 direct-child）展开进程树，归入该 Desktop 实例的 `codex app-server` 后代集合。
4. **shared 正证据**：`lsof` 该实例后代 PID 集 → 命中 `$CODEX_HOME/app-server-control/app-server-control.sock` 的 Peer/object inode（复用 verify_shared_daemon_topology.sh 的 lsof 匹配与 PID 身份检查；剔除 exact-version 判定）。
5. **private 正证据（三选一，均须与实例关联）**：(a) Desktop 日志明确 `transport=stdio`（文件、事件字段、实例关联方式冻结于实现说明——按 user-data-dir 的日志目录归属）；(b) 递归父进程链证明其后代 app-server 是 private stdio（父链从 app-server 一路向上含该 Desktop 主进程 + stdio pipe/FD 形态）；(c) 静态分支 + 进程级环境共同证明 **隔离实验实例** 命中 `CODEX_APP_SERVER_FORCE_CLI=1`——**此路径仅限取证/隔离实验**，生产 monitor 对用户环境里的 force env 一律不作 private 正证据（只计入 `attachConfig=enabled/disabled` 提示，v5 §2.3）。
6. **分类**：shared_only / private_only / **dual**（shared 与 private 同时成立时保留 dual，绝不先到者覆盖）/ unresolved。
7. **聚合**：§4.2 真值表 7 态（无实例=desktop_absent；全 shared_only=all_shared；任意 dual 或 shared+private 并存=mixed；≥1 private_only 且无 shared_only/dual=split_present；否则 unknown）。
8. **安全**：全程只读；不读会话内容；不终止/不修改任何进程；不改 launchctl 环境；pkill 禁用。隔离实例仅在 owner 人工门创建（§5 门 5）。

### 2.3 Dimensions 与 syncHealth（枚举沿用 v5 §4.4；输出字段修正）

每维度对象（固定 keys，全量 8 个）：

```json
{ "enum": "<枚举>", "ageMs": 12345, "stale": false,
  "source": "lsof_fd_peer|launchd_probe|version_probe|process_tree|provider_shapshot",
  "errorCode": "none|timeout|permission|rpc_failed|parse_failed|process_missing|not_implemented|unknown" }
```

- `ageMs` 由 **service 侧在生成 snapshot 时**从完成时刻计算；`stale = ageMs > staleAfter`。客户端**不拿本地时钟比较**，只需展示（P1-5）。
- 采样完成即记 `SampledAtMs`（按完成时间而非开始时间——P1-3 的 age 修正）。
- `syncHealth` 按 §4.3 表全量派生（healthy/not_applicable/degraded/unknown），由 `bridgeAttachment × desktopAggregate` 得出。

### 2.4 管理 API schema（评审 P1-5；完整 fixture 冻结）

`GET /internal/topology/snapshot`（Bearer token；**始终 200**）：

```json
{
  "schemaVersion": "topology-monitor.v1",
  "state": "enabled",
  "bridgeEpoch": 1710893634113558,
  "sampledAtMs": 1710893634113558,
  "syncHealth": "healthy",
  "dimensions": {
    "topologyBridgeAttachment": { "enum": "shared", "ageMs": 1200, "stale": false, "source": "provider_snapshot", "errorCode": "none" },
    "topologyDesktopAggregate":  { "enum": "all_shared", "ageMs": 1200, "stale": false, "source": "process_tree", "errorCode": "none" },
    "seatHealthDaemon":         { "enum": "running", "ageMs": 1200, "stale": false, "source": "version_probe", "errorCode": "none" },
    "seatHealthLaunchAgent":    { "enum": "healthy", "ageMs": 1200, "stale": false, "source": "launchd_probe", "errorCode": "none" },
    "attachConfig":             { "enum": "enabled", "ageMs": 1200, "stale": false, "source": "launchd_probe", "errorCode": "none" },
    "versionCompatibility":     { "enum": "effective_compatible", "ageMs": 1200, "stale": false, "source": "version_probe", "errorCode": "none" },
    "legacyManagedLoopback":    { "enum": "absent", "ageMs": 1200, "stale": false, "source": "process_tree", "errorCode": "none" },
    "legacyDesktopPrivate":     { "enum": "absent", "ageMs": 1200, "stale": false, "source": "process_tree", "errorCode": "none" }
  },
  "instances": [
    { "pid": 4242, "startTime": "2026-08-24T17:00:00Z", "classification": "shared_only",
      "evidence": [ { "kind": "shared_fd", "state": "confirmed" }, { "kind": "private_stdio", "state": "unavailable" } ] }
  ]
}
```

- `bridgeEpoch`：**数值**（与 `Management RuntimeIdentity.BridgeEpoch` 同源，management_api.go:118 已用数值 >0 判空）。
- `instances[].evidence` 为 **tagged union**：`{kind: shared_fd|private_stdio|force_stdio_experiment, state: confirmed|absent|unavailable}`；`unavailable`=采样失败（含权限/竞态），绝不作 negatives 资产。
- `state=disabled` 时仅返回 `{schemaVersion, state:"disabled", bridgeEpoch, sampledAtMs}`（其余省略），**仍 200**。
- **Swift mirror**：固定 `dimensions` 解构为 8 个 typed 可选字段；未知 enum 值 → decode fail-closed（视为 unknown/诊断失败，绝不默认 healthy）；未知字段忽略（Go 侧 fixture 与 Swift 解码 fixture 成对冻结：`management_api_test.go` 持有 observed fixture；`MacBridgeTests` 持有 mirror fixture）。
- 测试：disabled fixture、stale 维度、instances tagged unions、未知 enum fail-closed、401/token 拒绝。

### 2.5 阈值、防抖与过期（评审 P1-3；含数值依据与边界测试）

| 项目 | 值 | 依据 |
|---|---|---|
| cadence：FD/seat | 30 s | 与 discovery 3s 探测解耦；UI 秒级延迟可接受 |
| cadence：实例/launchd | 60 s | 进程枚举成本高于 FD 探测 |
| probe timeout（单次） | ≤5 s | 系统命令最坏耗时 |
| staleAfter：FD/seat | 60 s | = cadence(30) + probe 最坏(≤2×5s，两探针并行) + slack(≥15s)；保证抖动一个完整空窗内不 stale（P1-3 关键：**age 按完成时刻计算，staleAfter 严格大于 cadence+最坏耗时+jitter**） |
| staleAfter：实例/launchd | 120 s | = cadence(60) + 最坏耗时(≤2×10s) + slack(≥30s) |
| 防抖 degrade 展示 | N=2 连续同样 | 防抖只延迟展示，不改证据 |
| 防抖 recovery 展示 | N=3 连续正证据 | 恢复需要比降级更多的确定性（评审要求区分） |
| unknown/unresolved 展示 | **立即**（不防抖） | 证据不足必须立刻可见，不能靠防抖掩盖（防抖≠隐瞒） |
| 首次样本 | 完成首个采样前显示 `unresolved`+`sample_pending` | 健康态必须先有完整正证据（评审禁“无证据=healthy”） |
| desktop_absent | 立即（正负态证据） | 实例枚举本身是正结果 |

**边界测试（冻结）**：首次样本前、采样进行中、age 超 staleAfter 转 stale、恢复采样失败保持 unresolved、N=2/N=3 防抖计数、desktop_absent 立即展示。

### 2.6 生命周期与安全（评审 P1-4 + 边界）

- `-topology-monitor`（env：`CODEX_TOPOLOGY_MONITOR`）**默认 off**——负态分类（private_only/dual/mixed）经 owner 人工门验证前，产品默认路径不展示诊断结论；开发/诊断构建显式开启；API 在 off 时如实返回 `state=disabled`。
- owner 人工门 + 独立、可审查提交后才切默认 on（§5 开发顺序第 6 步）。
- 无持久化（内存、随 bridge 生灭）；不修改 discovery 的 fingerprint/fence/generation；不写用户目录；不读取会话内容；不终止进程；不改 launchctl 环境。

## 3. 文件级任务（P1-6；真实文件与 owner）

**Phase 0（provider/DTO/fixture/纯聚合状态机）**
| 任务 | 文件 | 内容 |
|---|---|---|
| S0 | `agent/codex-web/transport_identity.go`（新）、`core/interfaces.go`、`agent/codex-web/transport_identity_test.go` | §2.1 provider（roles/epoch/endpoint/PeerKey） |
| A1 | `go-bridge/topology_aggregate.go`（新）+ `topology_aggregate_test.go` | §4.2 真值表纯聚合 + syncHealth 派生 + 防抖/freshness 状态机（纯函数、无 I/O） |
| A2 | `go-bridge/topology_dto.go`（新）+ fixture 常驻 `go-bridge/testdata/topology_snapshot.json` | §2.4 DTO/编解码/tagged union（HTTP DTO 留 go-bridge，不进 core） |

**Phase 1（Darwin collector + API + MacBridge）**
| 任务 | 文件 | 内容 |
|---|---|---|
| C1 | `go-bridge/topology_collector_darwin.go`（新，`//go:build darwin`）+ `topology_collector_test.go` | lsof/ps/launchctl 采集；**错误码全覆盖**：permission/timeout/parse/process_missing/not_implemented；非 Darwin 或命令缺失 → `not_implemented`/`unresolved`，**不启动循环命令、不报 split**（P2-3） |
| C2 | `go-bridge/topology_collector_stub.go`（`!darwin`） | non-darwin 恒 not_implemented |
| API | `go-bridge/management_api.go`（新增 route；真实 owner=ManagementServer.ServeHTTP）+ `management_api_test.go` | `GET /internal/topology/snapshot`；注入 `ManagementConfig{TopologyProvider}`；**始终 200 + state**；401 检验 |
| UI-1 | `MacBridge/MacBridge/Services/ManagementAPIClient.swift` | snapshot 请求 + `TopologyMonitorStatus` Codable（fail-closed 未知 enum → .unknown）+ disabled 分支 |
| UI-2 | `MacBridge/MacBridge/Services/TopologyMonitorStatusStore.swift`（新） | 轮询生命周期 owner：cadence、退避、epoch 新鲜度、app 前后台暂停 |
| UI-3 | `MacBridge/MacBridge/Views/WorkspaceView.swift` | badge/面板语义：healthy 不显示；degraded 高警示（三种文案区分）；unknown 中性“诊断失败”；not_applicable 可选“未检测到 Codex App” |
| UI-tests | `MacBridge/MacBridgeTests/TopologyMonitorDecodeTests.swift`（新，+mirror fixture）、`TopologyMonitorStatusStoreTests.swift`（新）、`WorkspaceViewTests.swift`（追加） | mirror decode、store 生命周期、UI 状态渲染 |
| 清理 | `scripts/codex-web-phase0/`（保留 extractor + verdict）；删除 `go-bridge/catalog_forensics.go`、`catalog_forensics_test.go`、session_discovery.go/handlers.go 中的 capture/commit 插点、`GO_BRIDGE_CODEX_CATALOG_TRACE*` env seam（P1-8；作为发布门任务，与默认-on 提交同批） | 移除运行时 observer；保留证据包与提取工具（已脱敏、git-ignored） |

## 4. Phase 2/3 决议（P1-7/P2-2；另行计划）

- **catalog/per-transport health**：不在本计划；依据 verdict run3（updatedAt churn 低频且可归因；gen 会被合法低幅变化推进，不列为故障信号；监控不得修改 catalog fingerprint）→ 另立证据与 implementation plan（§5 顺序第 7 步）。
- **iOS capability `topology_status_v1`**：只接收 **syncHealth + 有界 reason code + age/stale**；**不发送 PID、start-time、路径、命令行、FD、Desktop instance evidence**；canonical pack + mirror + client acceptance 测试（§7.1 不变量 6）；本地管理 snapshot 原样仅留 Mac 侧。

## 5. 门与开发顺序（冻结；评审 §4）

开发顺序（评审建议冻结，逐项门全绿方可前进）：
1. provider/DTO/fixture 与纯聚合状态机（S0/A1/A2；含非 Darwin 与缺命令测试）；
2. Darwin collector，**显式开启、默认 off**，失败可见全测试（C1/C2）；
3. management API + MacBridge 解码/轮询（API/UI-1/UI-2/UI-tests；`go test ./go-bridge/` 全量 + `go vet` + Swift `xcodebuild test`）；
4. Mac UI（UI-3 + WorkspaceViewTests）；
5. **owner 人工门（拆分四场景，P2-1）**：
   - S1 shared-only：owner 保持 shared Desktop → `all_shared/healthy`；
   - S2 isolated private：仅隔离 force-stdio 实例（独立 `--user-data-dir` + 进程级 env），无任何 shared Desktop → `split_present/degraded`；
   - S3 mixed：owner shared Desktop 保持 + 隔离 private → `mixed/degraded`；
   - S4 dual/过渡窗：仅真实过渡记录，不注入不修改；
   - S5 split_present 聚合：需 owner 自愿完全退出所有 shared Desktop——若 owner 不与，标 `blocked_manual_owner_close`；**不得忽略 owner PID 冒充**；全程无 pkill、不终止 owner 进程、清理仅覆盖隔离实例（v5 §6.1）。
6. **独立提交切默认 on**：仅当 S1–S5（或诚实 blocked 记录）+ 清理任务 + UI 全绿；提交信息注明启用依据与审查；
7. 再另行规划 iOS（§4 脱敏约束）与 catalog/per-transport health。

本阶段门槛：SSV2 回归全绿（monitor 不触碰 timeline/revision）；direct/Relay 回归全绿；失败可见检查（人为制造采样失败 → unresolved+errorCode，UI 显示诊断失败而非 split）；`git diff --check`；评审文档与 completion report 提交。

## 6. 回滚与失败可见（P2-4 闭环）

- endpoint 恒 200；`state=disabled` 语义：Swift 侧独立枚举（.disabled），与解码失败/网络失败/401 严格分开（fail-closed：这三者→ .unknown 诊断失败，**不得展示为 healthy 或 disabled**）；
- 开关整体关闭 → 面板不显示（不冒充 healthy）；无持久化 → 重启即重建；
- 采样失败维度独立 errorCode；恢复必须重新采到正证据（不按计时假定）。

## 7. completion report 模板（交付时填写）

- Phase 0/1 门逐项：单测、全量、vet、Swift 测试、失败可见演练；
- owner 人工门 S1–S5 结果（含 blocked_manual_owner_close 的诚实记录）；
- 清理任务完成证明（observer 代码与 env seam 已删除；dumps 未入库）；
- 默认 on 提交的启用依据与审查记录；
- 对 v5 §4.4/§5.2 的勾稽（哪些已闭合、哪些保留 future 及原因）。
