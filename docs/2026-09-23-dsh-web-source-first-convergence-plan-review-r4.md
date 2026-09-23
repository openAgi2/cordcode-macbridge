# dsh-web source-first 收敛专项方案第四轮评审

- 日期：2026-09-23
- 对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v4（当前未提交工作树）
- **结论：不通过。** A2 的七面范围已补齐，但 Gate B 对已存在的官方 Web Remote 调用仍作未核实排除，A2 的 edit 负例与源码不符，alpha.1 goal 探针被解释成了尚未验证的业务成功。S1 的单独先行授权不受本结论改变。除保存本评审报告外，未修改方案、证据或产品代码，未提交，也未向既有会话发消息。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 见下列逐项清单 | 用户指定方案工作树；dsh-web S1 与 S2～S5 官方 Web 收敛 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | `git describe` 核实为 tag `dsh-v0.1.7-alpha.2`；配套上游源码 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 与 MacBridge 同名功能分支配套；本轮未从 iOS 源码推出官方协议结论 |

MacBridge 范围内已修改路径：`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/Localization.swift`、`MacBridge/MacBridge/Services/ManagementAPIClient.swift`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/agent_descriptor.go`、`go-bridge/handlers.go`、`go-bridge/management_api.go`；`agent/dsh-web/` 下 `agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

未跟踪的相关路径：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/auth.go`、`auth_test.go`、`binary.go`、`installer.go`、`installer_test.go`、`readiness.go`、`readiness_test.go`、`testdata/model_catalog_sanitized.json`；`docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md`、`2026-09-22-dsh-web-install-and-start-plan-review-r3.md`、`2026-09-22-dsh-web-install-and-start-plan-review.md`、`2026-09-22-dsh-web-install-and-start-plan.md`、`2026-09-23-dsh-web-auth-integration-plan.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r3.md`、`2026-09-23-dsh-web-source-first-convergence-plan.md`；`scripts/dshweb-phase0/alpha1-commands-goals-wire.json`、`s1-live-turn-frames.json`、`s1-runtime-log-excerpt.json`。

## 意见清单

1. **阻塞｜§4 观察清单、§9.3 主动补缺：不能将已被官方 Web 客户端调用的 Remote 留在“挂载未核，不属底表”。** 上游 `packages/client/ui-plugin-manager/src/client/manager-store.ts:526-539,577,651-652,728-775` 直接调用 `remote.pluginManager`；`packages/client/ui-settings-plugin-inventory/src/client/index.ts:30-38` 调用 `remote.pluginInventory.list`；`packages/client/ui-permission-presets/src/client/catalog.ts:134` 调用 `remote.permissionPresets.catalog`；`packages/client/ui-message-feedback/src/client/index.ts:60` 与 `surface.ts:27` 消费 `remote.messageFeedback`；`packages/api/session-controller/src/client/sessions/session.ts:291,350` 消费 `remote.subagents.prompt/interruptByParent`。这些服务都在 §4 的“其余 13”中。请以这些官方调用点和目标部署的挂载证据核对范围，为确定用户可见的每一面给出 disposition；若某调用点未在目标 Web 装配中启用，请给装配证据再排除。`FileUploads.upload` 也有官方客户端调用点（`packages/client/file-upload/src/client/runtime.ts:213-220`），应按实际传输/挂载形态判定。81 行只能称当前已列底表，不能据此宣称覆盖全部官方用户可见表面。
2. **阻塞｜§3 A2：edit 负例把“缺 request”错误地等同于 `GOAL_INVALID_EDIT`。** 官方 `packages/goal/goal/src/index.ts:328-335` 的 edit 签名要求 `request: EditGoalRequest`；只有已进入方法、取得当前 goal，并且 `request` 对象的 `objective` 与 `maxGoalRounds` **均缺失**时才显式抛 `GOAL_INVALID_EDIT`。整个 request 参数缺失属于另一种网关 descriptor/参数错误，不能从源码推出 `GOAL_INVALID_EDIT`。请拆成“缺 request 的网关负例”和“空 request 对象、存在 goal 的业务负例”，并在 alpha.2 样本中分别确认；不具备安全业务样本时将后一项标为待捕，不能预定错误码。
3. **阻塞｜§2.3、§9.3：goal 按钮“今天可用、没有新 bug”超出探针证明范围。** `alpha1-commands-goals-wire.json` 的 pause/resume/clear/edit 正形探针均返回 `ok:false, error:"no current goal"`。这证明请求越过 descriptor 校验并进入业务层，**没有证明任一按钮在有 goal 时成功改变状态、响应可解码、投影可收敛**。请将结论准确限定为“四个请求形状在 alpha.1 被接受；实际 mutation 成功路径未由本证据验证”，或用隔离会话捕获成功 mutation 及状态变化后再声称按钮可用。不要将“无当前 goal”的业务错误当作端到端成功。

## 已闭合的第三轮意见

- A2 从三面扩到桥实际消费的七面；goal pause/resume/clear/edit 各自要求 alpha.2 样本与契约测试，complete/create 标 future。上游 `packages/goal/goal/src/index.ts:276-277,328-329,351-352,363-364,432-433` 与新增锚点相符。跨版本部分仍因缺 alpha.2 样本而阻塞；已证实的 alpha.1 execute 错参可作为独立修复项留在方案中。
- §4 的 `packages/api/` 67 面与四个新增 namespace 的 14 面逐行编号为 1～81；terminal/account 已逐面展开。但上述官方 Web 调用点表明观察清单的排除结论还没有成立。
- A7 已将“暂不适用”限制为本轮不实施切换，并要求启用 steer 前补拒绝路径样本。`packages/api/session-controller/src/types.ts:222` 与 `commands.ts:477-479` 锚点属实。
- S1 的两轮逐帧记录、运行态 180 行日志、alpha.1 execute descriptor 正反样本维持第三轮核验结果。红线仍写明不改 dsh 源码、未知版本 fail-closed、禁猜测兼容和 fallback、凭据不入日志/诊断/`hello_ack`。
