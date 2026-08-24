# codex-web topology/sync monitor implementation plan v1 评审报告

- 日期：2026-08-24
- 被评审文档：[`2026-08-24-codex-web-topology-sync-monitor-implementation-plan.md`](./2026-08-24-codex-web-topology-sync-monitor-implementation-plan.md)
- 被评审提交：`38becfb`
- 证据补洞提交：`d9d2a32`、`4f56738`
- 评审方式：代码锚点复核、证据 verdict 对照、当前 HEAD 定向/全量 Go 测试与 vet
- 结论：**DONE / SUPERSEDED。** 本报告对 v1 的 NO-GO 已由 v2 计划全量吸收；不得再把本文档当成当前开发阻断或实现队列

> 2026-08-24 晚间复核：v2 已冻结 provider seam、Darwin collector、strict DTO/always-200 disabled 语义、采样 freshness/防抖、Mac 解码/轮询/UI、non-Darwin 与 iOS 脱敏边界；对应 Phase 0/1 代码已实施。本评审的作用到此结束，剩余状态只看 v2 计划的收尾表。

## 1. 已确认通过

1. `d9d2a32` 已把字节预算改为逐事件写前检查，并为唯一 `run_summary` 预留空间；新增 `TestForensicsByteCapAtomic`。
2. verdict 二版已将 run1 明确判无效；run3 为单轮有效样本：118 head、6 个 periodic authoritative、active turn 跨 4 个 full interval、无 drop/error/limit，656,762 B 小于 1 MiB。
3. Experiment A 已改回 Desktop transport topology 定义，不再以 workspace filter 冒充。
4. storm 判词已收敛为“无成员变化风暴；存在单行 updatedAt churn；旧 3 秒级风暴未复现”。
5. 独立复跑：`go test ./go-bridge -run TestForensics -count=1`、`go test ./go-bridge -count=1`、`go vet ./go-bridge` 全部通过；`git diff --check` 通过。
6. 当前工作树仅有既存 `.zcode/` 与 handoff 未跟踪项，三个补洞/计划提交未夹带它们。

因此 catalog 取证阶段可以正式收口；下面的问题都在**产品计划**，不是继续质疑 run3。

## 2. P1：进入开发前必须修订

### P1-1：`bridgeAttachment` 没有可实现的数据提供接口

计划要求用“logical client registry + FD peer”区分 codex-web main 与 observer，但当前：

- main/pump 与 observer 由 `agent/codex-web` 内部持有；
- 两者处于同一 bridge PID，进程级 `lsof` 只能看见多个 peer，不能给 peer 标注 main/observer 身份；
- T1/T6 没有定义 `agent/codex-web → go-bridge monitor` 的只读 provider、连接 identity、endpoint/epoch 或断线状态；
- 仅凭“匹配 peer 数 >= 2”会重犯 v5 已禁止的数量猜身份。

v2 必须冻结一个只读 provider seam：由 codex-web Agent 暴露 main/observer 的逻辑角色、连接状态、epoch、固定 UDS endpoint，以及能与实际 transport/FD 关联的 identity；monitor 只消费快照。必须有 main-only、observer-only、两者 shared、断线重连、stale epoch 测试。若现有 transport 无法暴露 FD identity，计划应先列一个 source seam 任务，不得让 T1 临场发明。

### P1-2：Desktop `private_only/dual` 采集算法仍是口号

T1 写“private 正证据三选一”，但没有冻结：

- 如何枚举每个 Desktop 主实例并绑定 PID/start-time；
- 如何递归归属 app-server 后代，而不是只查 direct child；
- 日志证据的文件、事件字段与实例关联方式；
- 进程级 force-stdio 环境如何只用于隔离实验、不得成为生产猜测；
- shared FD 与 private 正证据同时出现时如何保留 `dual`，而不是先到者覆盖；
- 任一步权限/竞态失败如何得到 `unresolved`。

v2 必须写成逐实例证据流水线与优先级，并明确生产 monitor 不读取用户会话内容、不终止进程、不修改 launchctl 环境。

### P1-3：cadence 与 freshness 相等，状态会在采样抖动时周期性过期

计划冻结 FD/seat `cadence=30s, freshness=30s`，实例/launchd `60s/60s`。任何调度抖动或命令耗时都会让旧样本在下一样本到达前变 stale，UI 可能在健康与 unresolved 间闪烁。

同时统一 `N=2` 没有区分 degraded、recovery、desktop_absent、unknown；60 秒实例采样下会引入最长约两分钟的未论证延迟。

v2 必须：

- 让 `staleAfter` 严格大于 cadence + 最坏采样耗时 + jitter，或由完成时间计算 age；
- 分开 degrade/recovery/unknown 的防抖规则；
- 写清首次样本、采样中、过期、恢复的边界测试；
- 给出数值依据，禁止冻结无证据 magic number。

### P1-4：负态证据尚未完成时，monitor 不能默认 on

计划 T5 令 `-topology-monitor` 默认 on，但 private/mixed/dual 活体门仍 blocked，UI 同阶段就会消费判定。这样开发/安装包会在负态分类尚未验证时主动展示产品结论。

v2 应分两次启用：

1. 开发与证据期默认 off，仅显式诊断包开启；API 如实报告 disabled；
2. owner 完成 mixed/private 人工门后，再用独立、可审查的启用提交切换默认值。

这不是产品 fallback；是防止未验证诊断进入默认路径的发布门。

### P1-5：API schema 仍不足以生成严格 Swift mirror

当前示例存在以下歧义：

- `bridgeEpoch` 示例是字符串，但现有 Management v1 runtime identity 使用数值 epoch；
- `dimensions` 是动态 `<KEY>` map，缺少固定字段与每个 enum 的严格 tagged shape；
- `instances[].evidence` 用 `true/null`，没有 unknown/error 的类型语义；
- `sampledAtMonotonicMs` 属于 bridge 进程时钟，MacBridge 不能拿自己的 monotonic clock直接比较；`freshForMs` 是阈值还是剩余寿命未定义；
- disabled 时 API 返回 501，但现有 `ManagementAPIClient.performRequest` 会把所有非 2xx 转成泛化 `httpError`，UI 无法区分“功能关闭”和“诊断故障”。

v2 必须冻结完整 JSON fixture：数值 epoch、固定 dimensions 对象、`ageMs/stale` 或 server 已裁决的 freshness、instance evidence tagged union、disabled 响应和 Swift decode 行为。加入 Go observed fixture 与 Swift mirror fixture；未知字段/未知 enum 的 fail-closed 规则也要写明。

### P1-6：文件级任务与当前代码实际入口不一致

- 管理路由实际 owner 是 `go-bridge/management_api.go` 的 `ManagementServer.ServeHTTP`，不是 T4 所写的 `handlers.go`。
- API 还需要 `ManagementConfig`/provider 注入与 `management_api_test.go`/fixture，计划未列。
- MacBridge 不是 `MacBridge/*` 一个可执行文件级任务；至少涉及 `Services/ManagementAPIClient.swift`、状态 owner（`RuntimeManager.swift` 或明确的 ViewModel）、`Views/WorkspaceView.swift` 以及对应 tests。
- 把纯 management DTO 放入 `core/interfaces.go` 的理由未说明；只有跨 agent 的 optional provider interface 才应进入 core，HTTP DTO 应留在 go-bridge 管理层。

v2 必须把 T1–T7 改成真实文件与 owner，补齐 provider 注入、codec/fixture、Mac polling 生命周期和测试文件。

### P1-7：计划题为 topology + sync monitor，但 Phase 1 没有 catalog/sync health 任务

T1–T7 实际只实现 topology。run3 得到的 updatedAt churn、generation rate、authoritative sample/semantic change 等没有进入任何 monitor 状态；Phase 3 只写 per-transport delivery，也未覆盖 catalog health。

v2 必须二选一并明确裁决：

1. **推荐**：Phase 1 明确改名为 topology monitor；基于 run3 判定“当前不修改 catalog fingerprint、不把低频 updatedAt churn 当故障”，catalog/per-transport 监视另立证据和计划；或
2. 在本计划中补完整 catalog health owner、指标、窗口、阈值、API 与测试。

禁止标题承诺 sync monitor、任务却只交付 topology badge。

### P1-8：临时 forensics observer 的去留没有裁决

v5 要求证据完成后决定删除，或提升为正式观测。计划既没有删除 `catalog_forensics.go`/env/extractor 的任务，也没有把它正式纳入 catalog health schema、生命周期和隐私策略。

v2 必须明确：

- 若 Phase 1 仅 topology：产品发布前删除运行时 observer/env seam，保留测试工具/已脱敏 verdict；或
- 若正式保留：把它改成有 owner、默认策略、版本、资源预算、隐私和回归门的正式诊断能力。

临时取证代码不能无限期停留在“以后再决定”。

## 3. P2：计划应同时补齐

### P2-1：人工 topology 门的四类场景必须拆开

owner shared Desktop 保持打开 + 隔离 force-stdio 只能得到 `mixed`。要得到全局 `split_present/private_only`，所有 shared Desktop 必须不存在，需要 owner 自愿关闭 shared Desktop。计划 §5 同时要求“不终止 owner”与 `private_only → split_present`，执行条件仍冲突。

v2 应分别列：shared、isolated private instance、mixed aggregate、split_present aggregate；最后一项允许标 `blocked_manual_owner_close`，不得忽略 owner PID冒充。

### P2-2：iOS Phase 2 必须只接收脱敏聚合，不得镜像本地进程证据

本地管理 snapshot 含 PID/start-time/进程证据。未来 `topology_status_v1` 不能直接把该 schema mirror 到 iOS。v2 应冻结 iOS 只接收 `syncHealth + bounded reason codes + freshness`，不发送 PID、路径、命令行、FD 或 Desktop instance evidence。

### P2-3：非 macOS 与缺少系统命令的行为未定义

Go bridge 可在非 macOS 环境构建/测试。v2 应规定 Darwin-only collector；非 Darwin 或 `lsof/ps/launchctl` 不可用时输出 `not_implemented/unresolved`，不启动循环命令、不报 split，并有测试。

### P2-4：回滚的 501 语义与 UI“中性停用”尚未闭环

若保留 501，Swift client 必须显式识别 topology endpoint 的 501；网络失败、401、decode failure 与 disabled 不能混为一类。更简单的方案是 endpoint 始终 200，返回 `state=disabled`。v2 需要选择一个，不要把决定留给实现 agent。

## 4. 开发准入裁决

当前可以做的只有：**修订 implementation plan v2**。不应开始 T1–T7 产品代码。

v2 通过后，开发顺序建议冻结为：

1. provider/DTO/fixture 与纯聚合状态机；
2. Darwin collector（显式开启、默认 off）与失败可见；
3. management API + MacBridge decode/polling；
4. Mac UI；
5. owner mixed/private 人工门；
6. 独立提交切默认 on；
7. 再决定 iOS 与 per-transport/catalog health 的后续计划。

最终裁决：**取证阶段 PASS；产品开发阶段 NO-GO，等待 implementation plan v2。**
