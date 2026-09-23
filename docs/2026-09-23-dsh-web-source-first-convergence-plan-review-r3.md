# dsh-web source-first 收敛专项方案第三轮评审

- 日期：2026-09-23
- 评审对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v3（未提交工作树内容）
- **结论：不通过。** 第二轮的四项证据缺口已有实质修复；仍有一项 Gate A 覆盖缺口，影响 S2 的 goal 管理跨版本实施。S1 的既有单独授权不受本结论改变。未修改方案、证据或产品代码，未提交，也未向 owner 既有会话发送消息。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期及配套特性 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 见下方逐项清单 | 用户指定方案工作树；dsh-web S1 与 S2～S5 source-first 收敛 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | `git describe`、`git log` 证实 tag `dsh-v0.1.7-alpha.2`；配套上游源码 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 与本仓同名功能分支配套；本轮未以 iOS 源码推断外部协议 |

MacBridge 范围内已修改路径：`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/Localization.swift`、`MacBridge/MacBridge/Services/ManagementAPIClient.swift`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/agent_descriptor.go`、`go-bridge/handlers.go`、`go-bridge/management_api.go`，及 `agent/dsh-web/` 下 `agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

未跟踪的相关路径：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/auth.go`、`auth_test.go`、`binary.go`、`installer.go`、`installer_test.go`、`readiness.go`、`readiness_test.go`、`testdata/model_catalog_sanitized.json`；`docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md`、`2026-09-22-dsh-web-install-and-start-plan-review-r3.md`、`2026-09-22-dsh-web-install-and-start-plan-review.md`、`2026-09-22-dsh-web-install-and-start-plan.md`、`2026-09-23-dsh-web-auth-integration-plan.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md`、`2026-09-23-dsh-web-source-first-convergence-plan.md`；`scripts/dshweb-phase0/alpha1-commands-goals-wire.json`、`s1-live-turn-frames.json`、`s1-runtime-log-excerpt.json`。

## 意见清单

1. **阻塞｜§3 A2、§5.2：alpha.2 的 goal 管理样本范围不足。** A2 只要求 `commands/list`、`goals/get`、`commands/execute`，§5.2 却将“goal 操作”作为跨版本防御和活体复测范围。本仓 `agent/dsh-web/goal.go:27-37,62-93` 当前实际发送 `goals/pause|resume|clear|edit`，其中 edit 带 `ref` 和 `request`；官方 alpha.2 `packages/goal/goal/src/index.ts:328-329,351-352,363-364,432-433` 对应这些 Remote，且参数已是 `agent: Agent`。只捕获 get 不能证明四个 mutation 的 namespace、`Agent` wire、`ref`/`request` 形状或响应/错误。请把当前桥支持的 pause/resume/clear/edit 逐项纳入 A2 的同版本请求、响应、负例及契约测试；若某项不继续支持，应在 S2 和能力广告中明确降级。`complete/create` 若不在本轮范围，也应标为未来项。现行 execute 错参的独立修复已有 alpha.1 证据，不依赖这些 alpha.2 样本；此意见不否定该 bug 结论。
2. **建议｜§4：terminal/account 仍是合并行。** 11 个真实 `TypertRemoteService`、67 个 `@Remote` 的总数经独立静态计数相符；新增八项也已有 disposition。但 #51–60、#61–67 分别合并在一行，严格说不是“每 Remote 单行”。由于同组处置一致、不影响实施门，可在下版展开或将“67 面逐行”改为“67 面全量登记”。
3. **建议｜§3 A7：负例不适用理由过宽。** `steer`/`queue` 是二值模式，不代表无失败行为；上游 `packages/api/session-controller/src/types.ts:222` 已声明 `session/steer-unavailable`，`commands.ts:477-479` 有队列 steer 拒绝路径。A7 如将来支持发送模式切换，应捕获官方 Web 运行中/状态变化时的拒绝或收口路径；该 future 裁决目前未授权实施，因此本轮不将此列为阻塞。

## 已闭合的第二轮事项及独立核验

- **Gate B 范围**：独立枚举 `packages/api/` 所有 `extends TypertRemoteService`，得到 11 个服务、67 个 Remote；v3 的 `skills`、`fileReferences`、`credentials`、`directoryPicker` 八项与上游源码一致。
- **S1 逐帧证据**：`s1-live-turn-frames.json` 有两轮、154/135 条记录。独立遍历得到各 136/117 个 stream 帧，revision 分别 1～136、1～117 连续；chunk index 分别 0～133、0～114 连续；两轮 end `outcomeSeq=17` 都对应 journal `assistant/message seq=17`。text-delta 42/44、reasoning-delta 86/65 与方案一致。旧两轮计数已如实标为摘要级历史。
- **S1 运行态证据**：`s1-runtime-log-excerpt.json` 含 180 条脱敏行，独立计数为 turn_started 4、text_delta 172、turn_completed 4；运行 PID 89580 仍监听 8777，启动于 10:34:41；`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime -version` 输出 `b4e42d5c5a9a` 和 10:33:44 构建时间。该命令证明包身份，进程及日志分别提供运行代际与特征行为证据。
- **alpha.1 execute 漂移**：`alpha1-commands-goals-wire.json` 的 `submittedAttachments:[]` 请求被接受；带 `images:[]` 的请求返回 `missing "submittedAttachments"; unexpected "images"`。本仓 `agent/dsh-web/commands.go:62-65,126-129` 确实仍发送 `images`，现行 bug 结论成立。正向样本使用未知命令名，证明 descriptor 准入形状，不宣称真实斜杠命令副作用已通过。
- **Gate A 结构**：A1～A7 已列文件索引、字段断言、负例或理由和缺项后的状态；A2～A5 的未取样状态保持阻塞。上述意见 1 是 A2 覆盖范围问题，不是缺少门制字段。
- **锚点与红线**：复核了 S1 的 `history.ts:165-175`、`types.ts:485-541`、`client/transport.ts:179-215`、`client/sessions/assistant-stream.ts:120-180`、`core/agent/src/runtime-types.ts:355-363`，以及 S4 的 `types.ts:89-96`、`commands.ts:344-363`、`index.ts:413-420`；内容与方案一致。方案继续禁止改 dsh 源码、猜测兼容、fallback parser、fake fixture 反证协议、凭据进入日志/诊断/`hello_ack`，未知版本 fail-closed。
