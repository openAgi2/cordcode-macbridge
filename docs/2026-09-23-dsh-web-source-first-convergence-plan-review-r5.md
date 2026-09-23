# dsh-web source-first 收敛专项方案第五轮评审

- 日期：2026-09-23
- 对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v5，按当前未提交工作树内容评审
- **结论：不通过。** 第四轮的三项已实质修正；本轮扩大到全仓 Remote、所有可见客户端调用点、探针目标方法以及 Gate A 运行边界做机械对账，发现下列阻塞。S1 的单独先行授权不受影响。除保存本评审文件外，未修改方案、证据或产品代码，未提交，未发送 owner 既有会话消息。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 见下列逐项清单 | 用户指定方案工作树；dsh-web S1 与 S2～S5 官方 Web 收敛 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | `git describe` 确认 tag `dsh-v0.1.7-alpha.2`；配套上游源码 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 与本仓同名功能分支配套；本轮未从 iOS 源码推断官方 wire |

MacBridge 范围内已修改路径：`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/Localization.swift`、`MacBridge/MacBridge/Services/ManagementAPIClient.swift`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/agent_descriptor.go`、`go-bridge/handlers.go`、`go-bridge/management_api.go`；`agent/dsh-web/` 下 `agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

未跟踪的相关路径：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/auth.go`、`auth_test.go`、`binary.go`、`installer.go`、`installer_test.go`、`readiness.go`、`readiness_test.go`、`testdata/model_catalog_sanitized.json`；`docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md`、`2026-09-22-dsh-web-install-and-start-plan-review-r3.md`、`2026-09-22-dsh-web-install-and-start-plan-review.md`、`2026-09-22-dsh-web-install-and-start-plan.md`、`2026-09-23-dsh-web-auth-integration-plan.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r3.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r4.md`、`2026-09-23-dsh-web-source-first-convergence-plan.md`；`scripts/dshweb-phase0/alpha1-commands-goals-wire.json`、`alpha1-web-assembly-mounts.json`、`s1-live-turn-frames.json`、`s1-runtime-log-excerpt.json`。

## 阻塞意见

1. **§4 `pluginRegistryProbe` 排除证据使用了不存在的方法。** `alpha1-web-assembly-mounts.json` 探的是 `pluginRegistryProbe/list`，404 只能证明这个 URL 不存在。官方类 `packages/client/ui-plugin-manager/src/index.ts:25-51` 的唯一 `@Remote` 是 `fastest()`；官方 Web 的 `packages/client/ui-plugin-manager/src/client/manager-store.ts:586` 确实调用 `remote.pluginRegistryProbe.fastest()`。所以“404 未挂载、无用户可见调用点”两项结论均不成立。请把 `pluginRegistryProbe/fastest` 作为实际表面核对：如需挂载证据，只对**正确方法**做安全错形探针；若目标版本未挂载，也应以正确方法的结果及 Web 装配关系说明排除。能力地图增加相应 disposition 或给出精确排除证据。
2. **§4 `dynamicCordisRunner` 的方法数、调用点与交叉引用均不符。** 文档称“4 面、`packages/client/` 无调用点、§4 主表和 S6 已登记”。官方 `packages/extensions/cordis-host-runner/src/index.ts` 实有 **12 个** `@Remote`：`undefineFromPanel`、`runHostHalf`、`getClientCode`、`resolveRequestRun`、`settleUserRun`、`stopFromPanel`、`syncInspectManifest`、`resolveInspectQuery`、`inventory`、`reportRenderFailure`、`reportClientGuardFailure`、`invoke`。官方客户端调用点在 `packages/extensions/ui-cordis/src/client/index.ts:38-58` 与 `packages/extensions/cordis-client-runner/src/client/index.ts:182-273`，不在 `packages/client/`；这些方法的目标 namespace 还被错形探针证明已挂载。§4 主表没有所称的 cordis/* 行，§5.6 S6 列表亦无 cordis。请将 12 面按官方客户端行为逐项 disposition，或逐项用装配证据排除，并改正不存在的交叉引用。机械对账显示全仓 **28 个真实 `TypertRemoteService`、123 个 `@Remote`**；当前 103 行之外正好是 agentTeams 1、speech 6、pluginRegistryProbe 1、dynamicCordisRunner 12。前两类用正确方法探到 404，后两类不能继续按现有理由排除。
3. **§3 A7 将 `updateQueue` 的错误码当作 direct prompt steer 的负例。** A7 要验证回合运行中 `session/prompt` 的 `mode:'steer'`，但所引 `session/steer-unavailable` 仅来自 `packages/api/session-controller/src/commands.ts:477-479` 的 `session/updateQueue` `action.kind==='steer'`。direct prompt 在同文件 `:372-380` 走 `agent.steer(message)`，其捕获错误映射为 `session/agent-busy`。请把 direct prompt 与待处理队列项的 steer mutation 分成两个取证对象；A7 的必需拒绝/收口样本应来自其实际调用路径，不能预定另一个 Remote 的错误码。若 OD-2a 仍不实施，A7 可保持 future，但实施门必须在方案中可执行。
4. **§3 Gate A 缺写操作的取证隔离规则。** A3 的 prompt/updateQueue、A4 的图片 prompt、A5 的 pin/archive 等都要改变真实服务端状态；当前通用门只规定样本五要素，没有指定在 `127.0.0.1:3080` 上如何避免触碰 owner 的既有会话和工作区。原评审边界只允许对既有会话做只读探针，真实 turn 须新建会话并结束后归档。请为每个需写入的 A3/A4/A5（以及 A2 若需实际 goal）规定新建隔离会话/工作区、允许的安全动作、结束后的归档和证据记录；无法满足则使用另座测试实例并保持阻塞。不要以“样本待捕”授权对 owner 会话写入。

## 非阻塞修订建议

- **§4 行 69 的状态要如实反映现行故障。** `commands/execute` 被标“已支持”，而同一行与 §2.3 已实证运行座位会拒绝桥发送的 `images`。建议标“现行受损，S2 第一修复项”，并明确修复/验证前不以该行证明能力可用。旧版 `agent/dsh-web/commands.go:47-56` 的注释和测试也仍称 `images:[]` 被座位接受，实施时应同步纠正；本轮不改代码。
- **A2 的 edit 源码锚点补到抛错行。** `GOAL_INVALID_EDIT` 的 `throw` 在 `packages/goal/goal/src/index.ts:334`，方案所引 `:331-333` 只覆盖前置判断。当前将业务负例标待捕是正确的。

## 已核实并可保留

- v5 对第四轮指出的 22 个表面已逐面 disposition；`alpha1-web-assembly-mounts.json` 的九个正确方法探针返回 descriptor 错误，可证明该方法在 alpha.1 座位挂载且方法未执行。`agentTeams/view`、`speech/transcribe` 的 404 使用正确方法，可作为当前座位未挂载证据。`pluginRegistryProbe/list` 是唯一方法名错误的 404。
- A2 七个桥消费面、两种 edit 负例已分开；空 request 的业务错误仍待真实样本确认。alpha.1 goal 四个探针只证明参数形状进入业务层，方案已收窄成功路径声明；现行 execute 错参仍有正反证据。
- S1 两轮逐帧、runtime 代际与特征日志、S4 附件读写方向及未知版本 fail-closed、凭据不进日志/诊断/`hello_ack` 等前轮核验结论未见回退。四个证据 JSON 的定点扫描未发现 cookie header、Bearer 值、launch token 或私钥标记；该扫描不代替发布前 secret scan。
