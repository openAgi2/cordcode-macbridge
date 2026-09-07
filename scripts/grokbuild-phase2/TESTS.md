# Phase 2（模式方向·Mac）禁用+诊断交付证据

- 工作树：`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（plan/approval-layer）
- 方案：`docs/2026-09-07-grok-build-slash-command-panel-implementation.md` v1.5 §5.1/§5.3 + §8 行 2 停止点
- 日期：2026-09-07
- 测试日志：`test-run.log`（定向 exit=0）；三包全量回归（grokbuild 30.2s / core cached-ok / go-bridge 78.3s 全 ok，BUILD_OK + VET_OK）

## 范围裁决（为什么本单元现在可交付）

方案 §8 行 2 前置 =「P4/P7/P8 有效恢复与归属全部通过；**失败则只交付明确禁用与诊断，不称模式功能完成**」。
P7 已在 phase0 得到决定性失败结论（1.0.13 官方恢复把 Pending→Inactive，无 CMU、无 plan 注入、文件不回写——短命 mode-only 路径阻断），失败分支被触发。该分支不依赖 P6 真实 turn 取证（P6 属执行方向与准入终表）。
**本单元只交付读侧 typed 状态 + 明确禁用 + 诊断；模式切换（set_mode 写入面、§5.2 事务、§5.3 完整读循环/watch/周期重验）未交付且明确阻断**——不是完成。

## 交付物（Mac 文件）

| 文件 | 内容 |
|---|---|
| `core/message.go` | `EventSessionMode` + `SessionModeEvent{status: confirmed\|pending\|unknown, mode?: plan\|default, canSet, reason?}`（typed，不重用 dsh planMode {active,pending}） |
| `core/interfaces.go` | `SessionModeReader` 可选接口（权威读约束写进契约注释：单一权威源+官方恢复映射，CanSet=false 必须如实） |
| `agent/grokbuild/session_mode.go` | P8 权威读：`findSessionDir` → `plan_mode.json` → 官方恢复映射（Active→plan；Pending/ExitPending/Inactive→default；未知/缺失/损坏→unknown 值非错误）；CanSet 恒 false + 稳定原因码 `grok_mode_switch_blocked_official_recovery`；modeSideState 缓存 + CMU dirty 重读 + invalidateSession/All |
| `agent/grokbuild/acp_codec.go` | `parseCurrentModeUpdate`（1.0.13 实测形状 `{sessionId, update:{sessionUpdate:"current_mode_update", currentModeId}}`，phase0 p5 样本） |
| `agent/grokbuild/session.go` | stdout CMU → `modeSide.markDirty`（§5.3：通知只 dirty，不携带目标值） |
| `agent/grokbuild/leader_subscriber.go` + `grokbuild.go` | leader rail `onModeDirty` 回调接线 |
| `agent/grokbuild/grokbuild.go` | **六键 legacy ModeSwitcher 拆除**（SetMode/GetMode/PermissionModes/mode 字段/normalizePermissionMode/接口断言全删；`permission_mode` capability 随断言消失——绝不留六键空转）；`SetProviders`→modeSide.invalidateAll |
| `agent/grokbuild/session_admin.go` | `DeleteSession` → modeSide.invalidateSession |
| `go-bridge/projection_types.go` | `SessionModeView` + snapshot `sessionMode` 字段 + patch `sessionMode` 字段（additive，nil=后端无 mode reader→chip 缺席） |
| `go-bridge/projection_reducer.go` | `session_mode` reduce case（整值 last-wins、指针不敏感去重、未知 status fail-closed 丢弃）；pending 字段/双 clone（Mode 指针深拷贝）/空 patch 检查/flush 拷贝/flush+DropPending 清理 |
| `go-bridge/events.go` | `core.EventSessionMode` → 逻辑事件 `session_mode`（nil payload 丢弃；unknown 是值） |
| `go-bridge/handlers_projection.go` | hydrate 事务内一次性权威读注入（agent 满足 SessionModeReader 时 ApplyHydrateEvent("session_mode")——快照零历史行也带 sessionMode） |
| 测试 | `agent/grokbuild/session_mode_test.go`（5）+ `go-bridge/projection_reducer_mode_test.go`（6）+ grokbuild_test 断言改向 |

## 测试矩阵（12/12 绿）

grokbuild：官方恢复映射表驱动（Active/Pending/ExitPending/Inactive/未知/缺失 × status/mode/canSet/reason）；权威文件读（多会话隔离、目录缺失=unknown 值非错误）；缓存 + 外部改写 + CMU dirty 重读（改文件不 dirty 不生效——通知才是失效信号）；invalidateSession 单删/All 全清；六键拆除断言（不满足 ModeSwitcher、满足 SessionModeReader）。
go-bridge：整值快照+去重（同值不 bump SyncRev）；unknown 是值不是缺席；垃圾 status fail-closed；patch 携带变化/无变化不携带（flush 清理修复）；clone 深拷贝保真（测试抓到并修复两真 bug：结构体含指针时 == 比较指针身份致去重永不命中、clone 浅拷贝共享 Mode 指针）；core 事件映射。

## §9 模式恢复关闭条件对照（本单元覆盖部分）

- Pending 文件存在+关闭后默认不算 Plan 成功 → GetSessionMode 映射 Pending→default(confirmed)——读侧永不把 Pending 当 plan ✓（实测样本形状守护在映射测试）
- Active/Inactive/ExitPending 按官方恢复 ✓；无 `_meta.mode`（不读它）✓；外部归属未知禁切 → CanSet 恒 false ✓
- 未覆盖（明确不声称）：idle actor 复用恢复门（属切换面）、§5.3 watch/周期重验/live CMU→patch 推送（本单元 CMU 只做 dirty，快照拉取时重读）、SetSessionMode 接口（不存在——写入面禁用）

## 未含 / 后续

- **模式切换写入面：不交付**（P7 阻断；待官方生命周期方案，届时按 §5.2 事务/operation lease 重建）。本单元不是模式功能完成。
- 权限 RPC（list/set_permission_modes）sessionId 化与 Grok 两键菜单：随 p3c/iOS 侧需要时再做——当前 Grok 无 ModeSwitcher，两 RPC 对 grok 返回 not_supported（诚实）。
- iOS sessionMode 消费（chip 三态、禁用原因展示）= p3c。
