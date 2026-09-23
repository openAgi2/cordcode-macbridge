# dsh-web source-first 收敛专项方案评审

- 评审日期：2026-09-23
- 评审对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md`，按未提交工作树内容评审
- 结论：**不通过**。S1 有单独先行授权；S2～S5 在方案修订并复审通过前不得实施。
- 评审范围：方案、指定来源的上游源码和只读来源核对；未修改产品代码、提交、构建、测试或向既有会话发送消息。

## P0 来源清单

| 来源 | 路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 见下方逐项清单 | 任务指定的评审工作树；预期产品特性为 dsh-web S1 流式输出及后续官方 Web 语义收敛 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净 | `git describe`、`git log`、tag 解析均确认 `dsh-v0.1.7-alpha.2`；配套上游源码 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 与本仓同名功能分支的唯一配套 iOS 工作树；本评审未据其源码作实现结论 |

本仓与 dsh-web 相关的已修改路径：

- `CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/{Localization.swift,ManagementAPIClient.swift}`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`；
- `agent/dsh-web/{agent_presets.go,apitypes.go,approvals.go,approvals_test.go,background_tasks.go,background_tasks_test.go,codec.go,commands.go,commands_test.go,context_usage.go,context_usage_test.go,diagnostics.go,diagnostics_seat_test.go,dshweb.go,efforts_for_model_test.go,fakedsh_test.go,goal.go,goal_test.go,grace_wire_test.go,history.go,lifecycle_test.go,migrate_test.go,models.go,parity_s3_clean_detail_test.go,permission_mode.go,permission_mode_test.go,plan_review_test.go,proc_unix.go,proc_windows.go,projects.go,resolver.go,rpcmap_test.go,seat_lifecycle_test.go,session.go,session_models_selection_test.go,sessions.go,streams.go,streams_test.go,testdata_contract_test.go,userinput_history_test.go,wire.go,workflow_test.go}`；
- `agent/dsh-web/testdata/{agentpreset_list_sanitized.json,session_list_g4_sanitized.json,session_list_sanitized.json}`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/{agent_descriptor.go,handlers.go,management_api.go}`。

本仓与 dsh-web 相关的未跟踪路径：

- `MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；
- `agent/dsh-web/{auth.go,auth_test.go,binary.go,installer.go,installer_test.go,readiness.go,readiness_test.go,testdata/model_catalog_sanitized.json}`；
- `docs/{2026-09-22-dsh-web-install-and-start-plan-review-r2.md,2026-09-22-dsh-web-install-and-start-plan-review-r3.md,2026-09-22-dsh-web-install-and-start-plan-review.md,2026-09-22-dsh-web-install-and-start-plan.md,2026-09-23-dsh-web-auth-integration-plan.md,2026-09-23-dsh-web-source-first-convergence-plan.md}`。

运行实例为 `127.0.0.1:3080`。未认证只读请求返回 401；本评审未使用 cookie。因此方案所称运行版 alpha.1 的 `agentId` 请求“200 ok”尚未由本次独立核实。评审输出不含 cookie 值。

## 上游锚点抽查

以下均在上述 dsh checkout 核对：

| 方案声明 | 实际锚点与结果 |
| --- | --- |
| follow 仅在 opt-in 时订阅 assistant stream | `packages/api/session-controller/src/history.ts:165-175`，相符 |
| 请求开关与 start/chunk/end 帧 union | `packages/api/session-controller/src/types.ts:485-541`，相符 |
| 官方客户端传 `assistantStream:true`、要求基线、逐帧检查 revision | `packages/api/session-controller/src/client/transport.ts:179-215`，相符 |
| 客户端按 start/chunk/end、index 和 settlement 折叠 | `packages/api/session-controller/src/client/sessions/assistant-stream.ts:120-180`，相符 |
| chunk 瞬态，提交后落持久事件 | `packages/core/agent/src/runtime-types.ts:355-363`，相符；方案应写完整路径 |
| commands `list(agent)` | `packages/interaction/commands/src/index.ts:314-315`，相符 |
| commands `execute(agent,line,submittedAttachments,signal)` | `packages/interaction/commands/src/index.ts:360-366`，相符 |
| goal `get(agent)`、`complete`、`create` | `packages/goal/goal/src/index.ts:276-277,390-391,646-648`，相符 |
| Inbox 两条有序列表 | `packages/core/agent/src/types.ts:38-55`，相符 |
| 归档可带 `stopActivity` | `packages/api/workspace-controller/src/types.ts:122-132`，相符 |

这证明了所列**源码代**的声明，不证明 alpha.1 或 alpha.2 的真实 wire 形状。尤其 `Agent` 跨 Remote 的形状，方案正确列为 A2 阻塞项。

## 意见清单

1. **阻塞｜§5.4、§3 A4：图片发送路径与源码冲突。** 方案将 `session/attachment` 写作上传步骤；官方 `packages/api/session-controller/src/index.ts:413-420` 明确是读取已被会话日志引用的图片。`types.ts:330-355` 的 prompt image part 直接包含 `mediaType`、`data`；`commands.ts:344-363` 在 prompt 准入时持久化。请重写 S4 调用顺序，A4 分别捕获发送请求和持久化后读取响应，并在样本前保持引用映射待定。
2. **阻塞｜§4 Gate B：disposition 表不完整。** 官方 `packages/api/workspace-controller/src/index.ts:86-201` 还有 workspace create/rename/delete、排序和 follow；`packages/api/session-controller/src/index.ts:282-502` 还有 rename/selectModel/cancel；`packages/api/settings-controller/src/index.ts:97-167`、`packages/api/workspace-files/src/index.ts:232-330`、job-controller 也有 Remote 表面。请以各 controller 的完整 `@Remote` 清单为底表，界定用户可见项并逐行给 supported/future/deliberately unsupported 及理由；合并的“会话功能”行不够。
3. **阻塞｜§2.3、§3 A2、§5.2：漂移两端证据不对等。** alpha.2 源码确有 `commands.list(agent)`、`execute(agent,line,submittedAttachments,signal)` 和 `goal.get(agent)`。alpha.1 `agentId` 的“探针 200 ok”缺请求、响应、实例版本身份的脱敏样本索引。请补可复核记录；继续明确 alpha.2 `Agent` wire 样本未取得时 S2 不得实施或广告，不从 alpha.1 外推。
4. **阻塞｜§3、§5、§8：Gate A 缺逐切片退出条件。** A1 还写“补归档成 fixture”；A3～A5 只列片段，未要求完整请求、响应、帧序列、重开结果和错误路径，也未规定样本缺失时 supported 行如何处理。请为 S2～S5 列同版本样本、脱敏文件索引、验证断言、负例及缺项即停的规则；不要留到实现期确认。
5. **阻塞｜§5.3、§4 OD-2：队列可见性的写入所有权未定。** `packages/core/agent/src/types.ts:38-55` 证明 Inbox 两表存在，未证明 `agent/inbox/spliced`、projection 与最终 `user/message` 的身份关系。A3 应捕获插入、编辑/撤回、消耗及最终消息的完整关系；S3 写清时间线唯一写入者、去重与收口。OD-2 的 steer/queue 产品选择应与队列可见性拆开。
6. **阻塞｜§5.1：S1 完成数字和部署声明缺证据索引。** 所列上游锚点成立；abandoned 增量无法撤回、revision 断档策略也如实登记。但 45/94 计数、完整帧序列、部署后 PID 和新版本特征日志仅为叙述。请给出脱敏证据位置，区分源码行为、组件验证和生产运行态验证。
7. **建议｜§4 OD-1～OD-5、§0：补齐裁决边界与编号。** 五项总体是产品行为或支持范围取舍，选项可理解。OD-1 只裁决置顶，S5 却还决定归档，应补归档/恢复语义；OD-4 写明窗口大小对断档恢复和资源的验收标准。文档把 S1/档案指向“§6.1/§6”，实际在 §5.1/§5。

方法论红线复扫：文档已禁止修改 dsh 源码、猜测兼容、递归 fallback parser 和用 fake fixture 反证协议；未知版本写为 fail-closed。凭据只读一条记录、不进入日志/诊断/`hello_ack` 的约束应继续保留，版本状态只应携带版本信息。现有方案尚未达到参照方案的 Gate A 样本先行、Gate B 完整能力地图和 supported 表面逐项立档放行标准。
