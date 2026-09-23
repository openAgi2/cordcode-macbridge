# dsh-web source-first 收敛专项方案第七轮评审

- 日期：2026-09-23
- 对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v7，按未提交工作树内容评审
- **结论：不通过。** 第六轮五条阻塞的直接修订均已核实；恢复的 OD 表与现有 Gate A、切片入口交叉对账后，还有四处会造成相反的实施结论。S1 的单独先行授权不受影响。除保存本报告外，未修改方案、证据或产品代码，未提交，未向 owner 既有会话写入。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 下列逐项清单 | 指定方案工作树；预期 dsh-web S1 已部署，S2～S5 须经方案、样本及产品裁决放行 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`；官方源码来源 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 本仓同名功能分支配套；本轮未以 iOS 源码推断官方 wire |

MacBridge 已修改：`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/Localization.swift`、`MacBridge/MacBridge/Services/ManagementAPIClient.swift`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/agent_descriptor.go`、`go-bridge/handlers.go`、`go-bridge/management_api.go`；`agent/dsh-web/` 下 `agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

MacBridge 未跟踪：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/` 下 `auth.go`、`auth_test.go`、`binary.go`、`installer.go`、`installer_test.go`、`readiness.go`、`readiness_test.go`、`testdata/model_catalog_sanitized.json`；`docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md`、`2026-09-22-dsh-web-install-and-start-plan-review-r3.md`、`2026-09-22-dsh-web-install-and-start-plan-review.md`、`2026-09-22-dsh-web-install-and-start-plan.md`、`2026-09-23-dsh-web-auth-integration-plan.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r3.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r4.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r5.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r6.md`、`2026-09-23-dsh-web-source-first-convergence-plan.md`；`scripts/dshweb-phase0/` 下 `alpha1-commands-goals-wire.json`、`alpha1-web-assembly-mounts.json`、`s1-live-turn-frames.json`、`s1-runtime-log-excerpt.json`。本报告创建后自身亦为未跟踪文件。

## 阻塞意见：恢复 OD 后的交叉门

1. **§2.3、§3 A2、§4 OD-5、§5.2：S2 第一修复项的放行条件互相冲突。** §2.3/§5.2 明说 `commands/execute` 的 alpha.1 错参修复“方案过审后即可实施”，A2 也明确该修复不依赖 alpha.2 样本；但 §4 的新硬规则规定未裁决 OD 所影响的切片保持阻塞，OD-5 状态待裁决且影响范围写“S2 与 §2 版本策略执行”。因此同一修复既被允许又被禁止。请将 OD-5 的阻塞范围精确到跨版本/rc 支持矩阵，明确 alpha.1 已证的 execute 修复是否排除在 OD-5 外；若不排除，就删除所有“过审后即可实施”的表述。不要让实施者自行选择较宽的一条。
2. **§3 A3、§4 OD-2b、§5.3：选择“只做排队可见”也会被管理操作的样本门卡死。** OD-2b 的 A 选项是 S3 只做可见；但 A3 单一编号仍要求 `updateQueue` 编辑、撤回、steer 变更的完整帧及拒绝样本，且写“id 连续性未证实 → S3 整体阻塞”。这些写操作属于 OD-2b 的 B 选项，不能成为 A 选项实施可见性的共同前提。请把 A3 拆成可见性必需的队列插入/消耗/id/重连证据，与仅在 OD-2b=B 时启用的管理操作证据；各自列文件、负例、断言和缺项即停。保留正常 Remote 无法提交非法 splice 的更正。
3. **§3 A4、§4 OD-3/行 100、§5.4：OD-3 选 B 后文件 receipts 没有可执行的 Gate A。** A4 只规定 image part、`session/attachment` 和图片拒绝；OD-3 的 B 选项却把 `fileUploads/upload` 纳入 S4。官方 Web `packages/client/file-upload/src/client/runtime.ts:213-220` 调 `fileUploads.upload(sessionId,{data,name?},signal)`，`packages/client/file-upload/src/index.ts:105-106` 声明 Remote；`packages/api/session-controller/src/commands.ts:358-371` 在 prompt 中解析、绑定 receipt。这是独立于图片的上传→receipt→prompt 准入→读取/重开链，当前 A4 没有其同版本请求/响应、负例、引用关系或缺项即停。请为 OD-3=B 增条件 Gate A 编号；若该证据未取得，B 选项对应范围必须保持阻塞，不得靠 image 样本放行。
4. **§4 OD-1、§5.5：OD-1 的 B 选项与统一验收标准不相容，归档/恢复行为未定义。** B 写“保持桥本地 pinstore（现状，双端各自为政）”，既未说 S5 的归档/恢复取消、维持什么现有行为，还是新增本地归档；但同一行的验收标准要求置顶双端一致、归档双端隐藏及恢复，与 B 相反。§5.5 又无条件写“归档行隐藏与恢复与 Mac web 一致”。请给 A/B 各自完整且可验收的置顶与归档/恢复范围；若 B 表示 S5 不实施，应明确其 disposition，不能用 A 的验收标准评价 B。

## 已核实的 v7 处置与来源抽查

- OD-1、2a、2b、3、4、5 的问题、选项、待裁决状态、影响切片与验收列确已恢复；所有待裁决项均有硬阻塞规则。上列意见针对不同门的组合效果。
- A5 的幂等成功与负例均和上游相符：`packages/api/workspace-controller/src/commands.ts:162-194`、`packages/workspace/workspace/src/index.ts:385-405`、`packages/workspace/workspace/tests/workspace.spec.ts:1086,1101`；A3 将非法 splice 降为内部定向测试与 `packages/core/agent-loop/src/inbox.ts:32-55` 相符。
- alpha.1 goal 证据的顶层 `purpose` 和 `goal_mutations_status` 均只称形状获接受，不再声称有 goal 时 mutation 成功；§4 行 71–74 明示端到端待测。direct steer 的三处核心锚点已改到 `packages/api/session-controller/src/commands.ts:372`，`:380` 和 updateQueue `:479` 对应错误路径准确。
- 本轮另逐处核了 `packages/api/session-controller/src/history.ts:165-175`、`client/transport.ts:181-215`、`index.ts:413-420`、`types.ts:89-96`、`packages/interaction/commands/src/index.ts:314-315,360-366`、`packages/core/agent/src/runtime-types.ts:355-363`、`packages/goal/goal/src/index.ts:328-334`，合计超过八处官方锚点；未见 S1 已知边界、附件方向、未知版本 fail-closed、不改 dsh 源码或凭据红线回退。四个证据 JSON 的定点扫描未发现 cookie header、Bearer 值、launch token 或私钥标记；这不替代发布前 secret scan。

## 非阻塞建议

- §4 OD-4 的验收列写“快照体积与解码耗时有上限”，但未给可测的阈值或制定阈值的责任人；在 OD-4 实际裁决前补数值，以便调优切片有客观出口。
