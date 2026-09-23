# dsh-web source-first 收敛专项方案第二轮评审

- 日期：2026-09-23
- 对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v2，按当前未提交工作树内容评审
- **结论：不通过。** v2 修正了 S4 方向并设立 S2/A2 阻塞门，但 Gate B 仍漏 Remote 表面，归档样本无法证明 S1 所写的完整帧断言，Gate A 的五要素也未逐项落实。S1 仍仅依 owner 原有单独授权；S2～S5 不得据此方案开始实施。
- 本轮只读核验方案、证据和官方源码；未修改产品代码、向既有会话发消息、运行构建或测试。

## P0 来源清单

| 仓库/工作树 | 绝对路径 | 分支与完整提交 | 未提交状态 | 任务预期、配套与产品特性 |
| --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline`；`b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 见下方逐项清单 | 用户指定工作树；dsh-web S1 流式输出及 S2～S5 官方 Web 收敛 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master`；`00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | 配套官方源码；`git describe`/`git log` 核对为 `dsh-v0.1.7-alpha.2` |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline`；`dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 与 MacBridge 同名功能分支配套；本评审未从 iOS 源码推出行为结论 |

MacBridge 中与本评审范围重叠的已修改路径：`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/{Localization.swift,ManagementAPIClient.swift}`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/{agent_descriptor.go,handlers.go,management_api.go}`，以及 `agent/dsh-web/` 下的：

`agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

未跟踪的相关路径：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/{auth.go,auth_test.go,binary.go,installer.go,installer_test.go,readiness.go,readiness_test.go,testdata/model_catalog_sanitized.json}`；`docs/{2026-09-22-dsh-web-install-and-start-plan-review-r2.md,2026-09-22-dsh-web-install-and-start-plan-review-r3.md,2026-09-22-dsh-web-install-and-start-plan-review.md,2026-09-22-dsh-web-install-and-start-plan.md,2026-09-23-dsh-web-auth-integration-plan.md,2026-09-23-dsh-web-source-first-convergence-plan-review.md,2026-09-23-dsh-web-source-first-convergence-plan.md}`；`scripts/dshweb-phase0/{alpha1-commands-goals-wire.json,s1-live-turn-frames.json,s1-runtime-log-excerpt.json}`。

## 复审意见

1. **阻塞｜§4、§9 第 2 项：所谓“7 个 controller 全 59 面”漏了 4 个已注册的 Remote namespace，共 8 个表面。** 上游 `packages/api/session-controller/src/skill-catalog.ts:18-35` 有 `skills/list`；`session-controller/src/file-references.ts:17-38` 有 `fileReferences/list`；`settings-controller/src/credentials.ts:67-117` 有 `credentials/describe|set|unset`；`workspace-controller/src/directory-picker.ts:41-88` 有 `directoryPicker/pick|list|createDirectory`。后两者还由主 controller 在 `settings-controller/src/index.ts:85-88`、`workspace-controller/src/index.ts:68-78` 显式挂载。这些包括用户可见的 skill、文件引用、配置凭据和目录选择。请从 `packages/api/` **所有**继承 `TypertRemoteService` 的类重新导出清单，再逐项作 disposition；若某子 namespace 在目标部署不可用，写明源码/运行态依据及排除原因。主 controller 的 59 项不能称作全部。
2. **阻塞｜§3 A1/A1b、§5.1、§9 第 6 项：证据文件与“完整两轮帧序列”不符。** `scripts/dshweb-phase0/s1-live-turn-frames.json` 只有一个 `PROBE`、一个 `SUMMARY` 和一行按事件类型压缩的 `FRAMESEQ`（149 个逗号分隔片段）；没有单帧 `revision`、`index`、`attemptId`、`outcome.seq` 结构，亦无所称第一轮 45/94 的记录。因此不能据该文件独立检查 revision 逐帧 +1、index 连续或 end 与 settlement 的身份关联。`s1-runtime-log-excerpt.json` 只有 86 行的计数及首尾两行，没有 86 条逐行记录或构建产物身份。请归档两轮经脱敏的**逐帧字段记录**及必要 journal 关联，分别证明计数、连续性、settlement；运行态证据应保留可复核的新代际和构建身份，或将当前文件与结论诚实降级为摘要而非完整证据。不得用摘要反证细节断言。
3. **阻塞｜§2.3、§3 A2、§9 第 3 项：alpha.1 的 execute 漂移行仍没有样本。** `alpha1-commands-goals-wire.json` 的四个探针仅覆盖 `commands/list {agentId}`、`commands/list {agent}`、`goals/get {agentId}`、`goal/get {agentId}`；没有 `commands/execute` 请求或 descriptor 拒绝记录。该文件支持 list/get 的参数差异，不支持表中 alpha.1 execute 使用 `{agentId,line,images}` 的断言。请补同版本真实请求/descriptor 证据，或把 execute 的 alpha.1 形状标为未验证并从“已实证”表移出。A2 的 alpha.2 `Agent` wire 阻塞标注正确，应保留。
4. **阻塞｜§3、§9 第 4 项：声称“每个编号五要素”与表格内容不一致。** A3/A4/A5/A6/A7 没有各自实际或预定的脱敏文件索引；A5 没有明确的缺项即停，A6 没有负例，A7 写“负例：无”且无缺项即停。A1b 也无负例。请给每个将支撑实施或切换的编号列实际文件名、同版本捕获、字段级断言、负例和缺项时的 disposition；不适用的负例要给出范围理由。S2～S5 的 Gate A 必须能独立判定通过/阻塞。
5. **建议｜§4、§5：把“逐行”落实到具体 Remote，或改称分组表。** 现表用斜杠合并多项，虽然主 controller 的 59 项可以逐项数出，却不便逐项核对 source、版本和实施归属。补齐遗漏的 8 项时建议每项一行，并把已支持项与本专项待支持项区分状态。OD-1～OD-5/OD-2a/2b 都属于真实产品裁决，当前选项表达清楚；S3 的 `UserMessage.id` 原位替换已明确以 A3 真实样本连续性为实施前提，这一点可保留。

## 已核实的修订

- §5.4 的图片方向已与 `packages/api/session-controller/src/types.ts:89-96,330-355`、`commands.ts:344-363`、`index.ts:413-420` 一致：prompt 内联发送，`session/attachment` 只读持久化图片。
- §5.1 的上游 S1 锚点仍准确：`history.ts:165-175`、`types.ts:485-541`、`client/transport.ts:179-215`、`client/sessions/assistant-stream.ts:120-180`、`packages/core/agent/src/runtime-types.ts:355-363`。abandoned 增量无法撤回和 revision 断档策略有显式登记；这不代替完成记录的原始证据。
- §5.3 的 Inbox 类型和折叠依据与 `packages/core/agent/src/types.ts:38-55`、`packages/core/agent-loop/src/inbox.ts:20-62`、`packages/core/session/src/types.ts:309` 相符；它们不单独证明占位与落定消息的 id 连续性，方案已把该点留给 A3 捕获。
- §2 的版本策略与发布门保留未知版本 fail-closed、不改 dsh 源码、凭据不进日志/诊断/`hello_ack`、不用猜测兼容或 fake fixture 反证协议的红线。§9 七条均写“采纳”，但以上四项尚未在文档与证据中实际闭合。
