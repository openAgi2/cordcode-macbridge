# dsh-web source-first 收敛专项方案第六轮评审

- 日期：2026-09-23
- 对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v6，按未提交工作树内容评审
- **结论：不通过。** 第五轮四条阻塞与两条建议的直接处置已核实，但对整份实施契约再次交叉检查后发现以下五条阻塞。S1 的单独先行授权不受影响。除本评审文件外，未修改方案、证据或产品代码，未提交，未对 owner 既有会话发送消息。

## P0 来源清单

| 来源 | 工作树绝对路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 下列逐项清单 | 指定的方案工作树；预期 dsh-web S1 已部署、S2～S5 尚待方案放行和 Gate A |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`；官方源码锚点 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 本仓同名功能分支的配套工作树；本轮不以 iOS 源码推断官方 wire |

MacBridge 已修改：`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/Localization.swift`、`MacBridge/MacBridge/Services/ManagementAPIClient.swift`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/agent_descriptor.go`、`go-bridge/handlers.go`、`go-bridge/management_api.go`；`agent/dsh-web/` 下 `agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

MacBridge 未跟踪：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/` 下 `auth.go`、`auth_test.go`、`binary.go`、`installer.go`、`installer_test.go`、`readiness.go`、`readiness_test.go`、`testdata/model_catalog_sanitized.json`；`docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md`、`2026-09-22-dsh-web-install-and-start-plan-review-r3.md`、`2026-09-22-dsh-web-install-and-start-plan-review.md`、`2026-09-22-dsh-web-install-and-start-plan.md`、`2026-09-23-dsh-web-auth-integration-plan.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r2.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r3.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r4.md`、`2026-09-23-dsh-web-source-first-convergence-plan-review-r5.md`、`2026-09-23-dsh-web-source-first-convergence-plan.md`；`scripts/dshweb-phase0/` 下 `alpha1-commands-goals-wire.json`、`alpha1-web-assembly-mounts.json`、`s1-live-turn-frames.json`、`s1-runtime-log-excerpt.json`。本报告创建后自身亦为未跟踪文件。

## 阻塞意见

1. **§4、§5：OD-1～OD-5 的实际裁决表从 canonical 文档消失。** 全文检索仅找到 OD 引用，没有任何选项、owner 决定状态或验收标准；此前第一、二轮报告明确核过五项选项，现 v6 只剩 §9.1 的“OD 边界/编号已采纳”历史文字。S3 的 OD-2b、S4 的 OD-3、S5 的 OD-1 因而没有可执行的产品取舍；OD-4/OD-5 也无法复审。请在本 canonical 文档恢复 OD-1、OD-2a/2b、OD-3、OD-4、OD-5 的问题、互斥选项、当前决定或待裁决状态、影响切片及验收标准；没有决定的切片不得因方案通过而自动实施。不能只引用旧评审报告，因为 §0 已规定本方案为唯一实施权威。
2. **§3 A5：预定“unarchive 不存在会话的错误”与官方源码相反，Gate A 负例不可达。** 官方 `packages/api/workspace-controller/src/commands.ts:183-194` 明说未归档 id 是幂等 no-op；`packages/workspace/workspace/src/index.ts:385-405` 不检查会话存在，不在归档集合就直接返回；`packages/workspace/workspace/tests/workspace.spec.ts:1086,1101` 对 `never-archived` 和已消失会话均验证成功。请把 A5 负例改为实际可达且与 S5 相关的错误，并将不存在/未归档 id 明确列为**成功幂等样本**；对应字段断言、文件索引和缺项即停规则一起校正。不要在正式座位上反复追逐源码否定的错误。
3. **§3 A3：要求活体制造“非法 splice 的投影错误形状”，但它是本地持久化折叠不变量，不是官方写入 API 的合法负例。** `packages/core/agent-loop/src/inbox.ts:32-55` 在折叠已有 journal 事件时检查越界、重复 id，抛出 `invalid persisted inbox splice at session seq`；方案 A3 却把“活体确认错误形状”列为实施前必捕负例。正常 `session/prompt`/`session/updateQueue` 不会让探针提交任意 splice。请将其降为源码支持的内部 reducer 负例，由定向测试验证；A3 的真实负例改用可通过隔离会话安全触发的 Remote 拒绝（例如已列的 `updateQueue` steer 拒绝），并明确其请求/响应样本。否则 S3 的 Gate A 出口实际上无法完成。
4. **§2.3、证据包：goal 的“仅证明形状”修订仍有两处反向声称“按钮可用”。** 方案第 130–131 行称 `goal_mutations_status` 登记“四方法今日可用”；`scripts/dshweb-phase0/alpha1-commands-goals-wire.json` 顶层 `purpose` 仍写 `goal buttons work on alpha.1`。同文件 `goal_mutations_status` 和方案第 123 行已经正确写明：无 goal 的业务错误只证明 descriptor 放行，真实 mutation 与投影收敛尚未验证。请同步删除两处成功断言，统一为“形状被接受，端到端成功未证”，再核对 §4 行 71–74 的“已支持”是否需加上待 S2 活体复测的限定，避免把现有广告状态误当验收证据。
5. **§3 A7、§9.5 与 v6 改动声明：新的核心源码锚点仍错一行。** `packages/api/session-controller/src/commands.ts:372` 才是 `if (request.mode === 'steer') agent.steer(message)`；方案三处均引用 `commands.ts:374`，该行实际是 `binding.commit()`。`:380` 的 `session/agent-busy` 锚点正确。原评审的 P0 source-first 门规定任一官方行为锚点对不上即不得通过；请三处改为 `:372`，并复核 v6 新增的所有文件/行号。

## 已核实的 v6 处置和复扫范围

- 正确方法 `pluginRegistryProbe/fastest` 的错形样本返回 descriptor 错误；官方 `packages/client/ui-plugin-manager/src/index.ts:50-51` 声明该 Remote，`src/client/manager-store.ts:586` 调用；底表行 104 正确入表。旧 `list` 探针已从 JSON 删除并留更正注记。
- `packages/extensions/cordis-host-runner/src/index.ts` 有 12 个 `@Remote`（:231、329、388、417、442、484、502、515、529、688、722、745），底表行 105–116 逐面列出，扩展客户端调用点与 §5.6 future 入口已补。`agentTeams/view`、`speech/transcribe` 的 404 使用真实方法。底表 116 个编号连续，7 面装配排除使 123 面账目闭合。
- direct steer 源码在 `commands.ts:372-380`，`updateQueue` steer 拒绝在 `:477-479`；v6 已把两条错误路径分开。§3.1 已规定 A2～A5 仅对新建隔离会话/工作区写入并归档，满足上轮隔离意见。
- 另抽核了 `goal/goal/src/index.ts:328-334` 的 edit 参数与抛错、`workspace-controller/src/types.ts:123-132` 的归档参数、`session-controller/src/commands.ts:344-355` 的图片拒绝、`agent-loop/src/inbox.ts:32-55` 的 splice 检查；加上上述 fastest、cordis、direct steer、updateQueue、unarchive，共超过八处上游锚点核对。S1 逐帧证据、附件读取方向、未知版本 fail-closed 与凭据红线未见 v6 回退。定点扫描四个证据 JSON 未发现 cookie header、Bearer 值、launch token 或私钥标记；这不代替发布前 secret scan。
