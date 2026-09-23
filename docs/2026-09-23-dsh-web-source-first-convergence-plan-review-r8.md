# dsh-web source-first 收敛专项方案第八轮评审

- 日期：2026-09-23
- 对象：`docs/2026-09-23-dsh-web-source-first-convergence-plan.md` v8（文档头部 Date 行「2026-09-23（v8，同日第七轮修订）」），按未提交工作树内容评审
- **结论：通过。** 零阻塞级意见，5 条建议级意见（见下）。第七轮四条阻塞的修订（OD-5 影响范围收窄、A3 拆 A3a/A3b、A4b 条件门新增、OD-1 B 选项补全）经 Gate A×切片×OD 裁决表交叉对账全部自洽，无「同一事项既被允许又被禁止」；本轮亲核 30+ 处官方源码锚点（上游 `dsh-v0.1.7-alpha.2`）全部吻合；四个证据文件与文档声明一致、无凭据泄露；28 服务/123 @Remote = 底表 116 + 装配排除 7 的全仓对账经独立逐文件计数复核成立。S1 单独先行授权不受影响；S2～S5 在本文档通过后仍受各自 Gate A 退出条件与 OD 裁决规则约束。除保存本报告外，未修改任何文件，未提交，未向 owner 既有会话写入。

## P0 来源清单

| 来源 | 绝对工作树路径 | 分支 | 完整提交 | 未提交状态 | 任务预期与配套关系 |
| --- | --- | --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e` | 下列逐项清单 | 指定方案工作树；S1 已获 owner 单独批准先行并部署，S2～S5 须本方案通过 + Gate A + OD 裁决后实施 |
| 官方 dsh | `/Users/jacklee/Projects/deepseek-harness` | `master` | `00102833dfaee1da9f48a3a8eae9d34005a75218` | 干净（`git status --porcelain` 为空） | `git describe --tags --exact-match HEAD` 实得 `dsh-v0.1.7-alpha.2`，与方案 §上游锚点声明一致；官方源码唯一来源 |
| 运行座位 | `127.0.0.1:3080`（本机进程） | —（游离于两仓之外的运行实例） | — | 只读核验：`lsof`/`ps` 实得监听者 node PID 38778、2026-09-23 00:34:37 启动、命令行 `node /opt/homebrew/bin/dsh --profile web --host 127.0.0.1 --port 3080 --no-open`，与方案 §上游锚点及证据文件 `seat` 字段逐字一致 | 本轮未发起任何需 cookie 的 HTTP 探针、未读取 cookie 文件、未新建会话、未发送消息；CLI 版本 0.1.7-alpha.1 采信归档证据（`alpha1-commands-goals-wire.json`），未复测 |
| iOS（配套） | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `dd3911785868930dfbf45e681e0837e88333148f` | 干净 | 与 MacBridge 同名功能分支的配套工作树；本轮未引用 iOS 源码作任何结论。【补记：本行按 codex 审核结论（2026-09-23）补齐——跨仓评审来源门要求记录配套路径/分支/完整提交，即使评审未引用 iOS 源码；提交身份由 codex 审核亲核】 |

~~本轮未使用 iOS 仓源码，无结论依赖 iOS 侧，故不列 iOS 来源行。~~【更正（codex 审核结论 2026-09-23）：来源门要求无论是否引用都记录配套 iOS 工作树，已按上表补齐；原省略判断有误】

MacBridge 已修改（`git status` 亲核，与第七轮报告记录的状态一致，HEAD 未变）：
`CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`MacBridge/CordCodeLink.xcodeproj/project.pbxproj`、`MacBridge/MacBridge/Services/Localization.swift`、`MacBridge/MacBridge/Services/ManagementAPIClient.swift`、`MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`、`MacBridge/MacBridge/Views/WorkspaceView.swift`、`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`、`go-bridge/agent_descriptor.go`、`go-bridge/handlers.go`、`go-bridge/management_api.go`；`agent/dsh-web/` 下 `agent_presets.go`、`apitypes.go`、`approvals.go`、`approvals_test.go`、`background_tasks.go`、`background_tasks_test.go`、`codec.go`、`commands.go`、`commands_test.go`、`context_usage.go`、`context_usage_test.go`、`diagnostics.go`、`diagnostics_seat_test.go`、`dshweb.go`、`efforts_for_model_test.go`、`fakedsh_test.go`、`goal.go`、`goal_test.go`、`grace_wire_test.go`、`history.go`、`lifecycle_test.go`、`migrate_test.go`、`models.go`、`parity_s3_clean_detail_test.go`、`permission_mode.go`、`permission_mode_test.go`、`plan_review_test.go`、`proc_unix.go`、`proc_windows.go`、`projects.go`、`resolver.go`、`rpcmap_test.go`、`seat_lifecycle_test.go`、`session.go`、`session_models_selection_test.go`、`sessions.go`、`streams.go`、`streams_test.go`、`testdata/agentpreset_list_sanitized.json`、`testdata/session_list_g4_sanitized.json`、`testdata/session_list_sanitized.json`、`testdata_contract_test.go`、`userinput_history_test.go`、`wire.go`、`workflow_test.go`。

MacBridge 未跟踪：`MacBridge/MacBridgeTests/DeepSeekSeatActionTests.swift`；`agent/dsh-web/` 下 `auth.go`、`auth_test.go`、`binary.go`、`installer.go`、`installer_test.go`、`readiness.go`、`readiness_test.go`、`testdata/model_catalog_sanitized.json`；`docs/` 下 `2026-09-22-dsh-web-install-and-start-plan.md` 及其三份评审报告、`2026-09-23-dsh-web-auth-integration-plan.md`、`2026-09-23-dsh-web-source-first-convergence-plan.md` 及其 r1～r7 七份评审报告；`scripts/dshweb-phase0/` 下 `alpha1-commands-goals-wire.json`、`alpha1-web-assembly-mounts.json`、`s1-live-turn-frames.json`、`s1-runtime-log-excerpt.json`。本报告创建后自身亦为未跟踪文件。

## 阻塞意见

无。

## 建议级意见（不阻断，逐条可改）

1. **§2.3 证据清单与 §5.2 测试计划中「四个 goal 变更方法的错形/正形」枚举不精确。**
   `alpha1-commands-goals-wire.json` 实有 17 探针中，goal 四方法仅 pause/edit 兼有错形（`agent`，descriptor 拒绝 verbatim）与正形（`agentId`，"no current goal" 业务错误）探针；resume/clear 只有正形探针。核心结论「四方法请求形状全部被接受」由四条正形探针完整支撑，不受影响；错形拒绝的同构性由 pause/edit 及 commands/list、fileReferences、execute 的错形样本代表，且 A2 已要求 alpha.2 七面逐项补 `agentId` 被拒 verbatim。建议把枚举改写为「pause/edit 错形+正形；resume/clear 正形（错形拒绝由同 descriptor 机制样本代表）」，或在下次座位取证时补 resume/clear 两条错形探针；§5.2「含四 mutation 的错形/正形记录」同步修正。

2. **§4 OD-2b 选项 B「可见+管理（编辑/撤回，依赖 A3）」沿用 v8 拆分前的旧编号。**
   A3 已拆为 A3a（可见性，任一选项必需）与 A3b（管理，仅 B 启用）。建议改为「B 可见+管理（可见性证据依赖 A3a，管理证据依赖 A3b）」，A 选项也补「可见性证据依赖 A3a」。A3a 行本身已写「OD-2b 任一选项均必需」、§5.3 已按范围拆分，故现状不构成矛盾，仅 OD 表单元格与拆分后编号不同步。

3. **§3.1 逐项规定中 A4 条款未覆盖 v8 新增 A4b 的 `fileUploads.upload` 写入。**
   A4 条款现只写「图片 prompt 在隔离会话上发送；session/attachment 为只读取证」。A4b 的上传→receipt→prompt 准入链含服务端写入；通用红线「一切写入只允许发生在新建的一次性隔离会话上……禁止对 owner 既有会话/工作区做任何写入」在语义上已覆盖该上传，规则本身自洽，但逐项枚举与 Gate 编号拆分不同步。建议 A4 条款补一句「文件 receipts 上传→prompt 准入链同在隔离会话上完成，结束后归档」。

4. **§4 底表行 13（session/updateQueue）默认状态「future」与行 22–25 默认「切片（S5）」的默认方向相反，且 OD-2b=B 的升格无显式语句。**
   OD-1=B 有显式「行 22–25 的『切片』状态降级为 future」；OD-2b=B 将行 13 纳入 S3 管理范围却仅由影响列「S3（行 13）」隐含。两个默认方向均可辩护（未裁决前保守 fail-closed；行 13 说明「S3 先做可见；管理待 OD-2b」与 §5.3 一致，可见性经 inbox 投影不消费本面），不构成矛盾；建议在 OD-2b 验收列或行 13 说明中补显式升格语句（「OD-2b=B 时本行升格为切片（S3 管理侧）」），消除与 OD-1 行文的不对称。

5. **锚点路径缩写约定不统一。**
   例：「workspace/src/index.ts:385-405」实为 `packages/workspace/workspace/src/index.ts`；「ui-message-feedback/index.ts:60」实为 `packages/client/ui-message-feedback/src/client/index.ts:60`；「file-upload/client/runtime.ts:213-220」实为 `packages/client/file-upload/src/client/runtime.ts:213-220`。本轮核验中所有缩写均唯一解析、行号与内容精确吻合，不构成锚点错误；建议统一为可机械解析的完整路径（或显式声明缩写规则），降低后续实施与评审的定位成本。

## 已核实事项

### 1. 官方源码锚点抽查（评审要点 1，本轮亲核 30+ 处，全部吻合）

以下全部在 `/Users/jacklee/Projects/deepseek-harness`（`00102833dfaee1da9f48a3a8eae9d34005a75218`，`dsh-v0.1.7-alpha.2`）用 `sed -n`/`grep` 逐行核对：

- `packages/interaction/commands/src/index.ts:314-315`（`@Remote` + `list(agent: Agent)`）、`:360-366`（`execute(agent, line, submittedAttachments, signal)`）——§2.3 漂移表两行成立。
- `packages/goal/goal/src/index.ts:276-277`（`@Remote('get')` + `get(agent: Agent)`）、`:328-329`（edit 签名 `edit(agent, ref, request)`）、`:331-334`（`GOAL_INVALID_EDIT` throw 恰在 :334）、`:351-352`（pause）、`:363-364`（resume）、`:432-433`（clear）、`:390-391`（`@Remote('complete')`）、`:646-648`（`@Remote('create')`）——§2.3「alpha.2 多出 complete/create」与 A2 edit 负例锚点均成立。
- `packages/api/session-controller/src/file-references.ts:30-38`（`list(agent, query, signal)` 签名在引用区间内）。
- `packages/api/session-controller/src/types.ts:89-96`（image part `{type:'image', mediaType, data, name?}` 逐字段吻合）、`:485-541`（`SessionFollowRequest.assistantStream?: true` 与 start/chunk/end 帧 union，end.outcome 含 `committed` 的 `assistant/message|assistant/attempt` + seq 与 `abandoned`）。
- `packages/api/session-controller/src/commands.ts:344-363`（模型 image 支持校验 + `admitPromptContent`；`session/attachment-invalid` + `MODEL_DOES_NOT_SUPPORT_IMAGES` 在 :351-355，引用区间 346-353 覆盖校验与 throw 主体）、`:358-371`（`resolvePromptFileReceipts` :358-361 + `bindPrompt` :371）、`:372`（`if (request.mode === 'steer') agent.steer(message)` 恰在 :372）、`:380`（`session/agent-busy` `'prompt rejected'` 恰在 :380）、`:477-479`（`session/steer-unavailable` throw 在 :479，条件在 :478）。
- `packages/api/workspace-controller/src/commands.ts:174`（`'workspace/session-active'` 恰在 :174）、`:183-194`（unarchive 幂等，注释原文「An id that is not archived is not an error: the call is idempotent」在区间内）。
- `packages/workspace/workspace/src/index.ts:385-405`（unarchiveSession 无存在性检查；doc 注释明写「Unarchiving runs no session-existence check」）。
- `packages/workspace/workspace/tests/workspace.spec.ts:1086`（never-archived unarchive 成功断言）、`:1101`（已消失会话 unarchive 成功断言）——A5 幂等成功样本的官方测试引用成立。
- `packages/core/agent/src/types.ts:38-45`（`InboxTarget` + `InboxState` 两有序表）、`packages/core/agent-loop/src/inbox.ts:20-62`（投影只折叠 `agent/inbox/spliced`，:33）、`:32-55`（start/removedCount/重复 id 校验等内部不变量）、`packages/core/session/src/types.ts:309`（`'user/message': UserMessage` 恰在 :309）、`packages/core/agent/src/consumed-work.ts:83`（`case 'agent/inbox/spliced'` 恰在 :83）、`packages/core/agent/src/types.ts:101`（`outcome?: 'canceled'`）——§5.3 四锚点与 A3a 负例理由成立。
- S1 五锚点：`packages/api/session-controller/src/history.ts:165-175`（`assistantStream !== true` opt-in 订阅）、`src/client/transport.ts:179-215`（请求硬编码 `assistantStream: true`；快照缺基线即抛 `gateway/internal`；`expected = revision + 1` 逐帧校验）、`src/client/sessions/assistant-stream.ts:120-180`（start 注册、无匹配 start 的 chunk 返回 undefined、`frame.index !== nextIndex` rebaseline、end 释放 settlement）、`packages/core/agent/src/runtime-types.ts:355-363`（「Chunk frames are transient; the loop appends one final v2 assistant/message or assistant/attempt … before a committed end frame」）。
- file-upload 双锚点：`packages/client/file-upload/src/client/runtime.ts:213-220`（`remote.fileUploads.upload(sessionId, {data, name?}, signal)` 恰在 :213-220）、`packages/client/file-upload/src/index.ts:105-106`（`@Remote('upload')` + `upload(agent, request, signal)` 恰在 :105-106）——A4b 行两处引用行号精确。
- `packages/api/session-controller/src/index.ts:413-420`（`@Remote('attachment')` 读取已被会话日志引用的持久化图片，非上传端点）、`packages/api/workspace-controller/src/types.ts:122-132`（`WorkspaceArchiveSessionRequest{sessionId, stopActivity?}`）。
- 客户端调用点抽查：`packages/preset/agent-preset-registry/src/index.ts:174/:304`（agentPresets list/select）、`packages/llm/llm/src/index.ts:544`（`@Remote` listConfigurableProviders）、`packages/api/session-controller/src/skill-catalog.ts:19-40`（SessionSkillCatalog，namespace 'skills'）、`packages/api/settings-controller/src/credentials.ts:67-117`（CredentialsController 及 describe/set/unset 三 `@Remote`——行 40「仅为源码登记」的定性准确）、`packages/api/session-controller/src/client/sessions/session.ts:291/:350`（subagents.prompt / interruptByParent 调用恰在引用行）、`packages/client/ui-message-feedback/src/client/index.ts:60`（inject 含 `remote.messageFeedback`、`remote.sessionFeedback`）、`packages/client/ui-reference/src/client/index.ts:43`（inject `remote.sessionReferenceResolver`）、`packages/client/ui-permission-presets/src/client/catalog.ts:134`（`remote.permissionPresets.catalog()`）、`packages/client/ui-plugin-manager/src/client/manager-store.ts:526+/:586`（pluginManager 调用群 / `pluginRegistryProbe.fastest()` 恰在 :586，r5 更正成立）、`packages/extensions/ui-cordis/src/client/index.ts:38-58`（inject + stopFromPanel/undefineFromPanel/inventory）、`packages/extensions/cordis-client-runner/src/client/index.ts:182-273`（inject :182 + syncInspectManifest/resolveInspectQuery/invoke/reportRenderFailure）、`packages/client/ui-settings-plugin-inventory/src/client/index.ts:30-38`（inject + `pluginInventory.list()`）、`packages/client/ui-sidebar-documentpreview/src/client/office/index.ts:70-96`（:70 inject、:73 render、:96 generation）、`packages/api/workspace-controller/src/directory-picker.ts:54`（`@Remote('pick')`）。
- 本仓侧声明核实：`agent/dsh-web/commands.go:47-56` 确有「live-probed 2026-09-05 … `images: []` is the one shape both gateway generations accept」旧注释（rc.2 时点结论）——§5.2「实施时同步纠正旧注释与测试」的登记与本仓代码现状一致；`agent/dsh-web/auth.go:60`（`browserSessionRecordKey = "client-connection/browser-session"` 单记录）、`:70`（`~/.dsh/.credentials.yaml`）——行 40「认证只读一条」与代码一致；`go-bridge/checkpoint.go` 存在（行 43 声明）、`agent/dsh-web/background_tasks.go` 由 session/list subagent 行承接（行 48 声明，文件头注释印证）；`agent/dsh-web/streams_test.go:1128` `TestAssistantStreamContinuityDrops` 存在（A1 负例声明）。

### 2. 门控交叉一致性（评审要点 2）

r7 四条阻塞的修订逐一交叉对账，均自洽：

- **OD-5 收窄（r7 #1）**：§4 OD-5 影响列（「仅 S2 的跨版本防御部分（A2 门控）与 rc 兼容矩阵；不含 alpha.1 已证的 commands/execute 错参修复」）、§2.3 S2 实施门（「execute 现行 bug 修复……过审后即可实施」「不从 alpha.1 形状外推 alpha.2」）、§3 A2 缺项即停（「execute 现行 bug 修复不受此门约束」）三处一致；execute 修复既不被 A2 也不被未裁决的 OD-5 卡死，跨版本防御同时受 A2 样本与 OD-5 裁决约束，无「既允许又禁止」。
- **A3a/A3b 拆分（r7 #2）**：A3a「OD-2b 任一选项均必需」、A3b「仅 OD-2b=B 时启用；A 选项下本行不适用、不阻塞」、缺项即停按范围拆分（「可见性不受此门影响」）、§5.3「可见性与管理互不牵连」——OD-2b=A 时可见性不再被管理样本门卡死。
- **A4b 条件门（r7 #3）**：A4b「仅 OD-3=B 时启用」「不得靠 image 样本放行」与 §5.4、OD-3 选项 B（「行 100 随之进 S4 证据范围」）、底表行 100「future（绑 S4 OD-3）」一致；A4a 对 OD-3 任一选项必需。
- **OD-1 B 选项补全（r7 #4）**：B=S5 整体不实施、行 22–25 降级 future、独立验收（「置顶行为与现状一致；无新增归档/恢复面；不宣称双端一致；S5 不实施即满足」）；§5.5 按 A/B 分写；B 下「A5 样本不采集、不阻塞其他切片」与 A5 行「样本缺 → S5 阻塞」相容（S5 不实施时该阻塞无对象）。
- **其余交叉**：A6↔行 18（等价性未证实→维持轮询、行保持 future）；A7↔OD-2a（样本缺→悬置维持 queue，不阻塞其他切片；`session/steer-unavailable` 归 A3b、`session/agent-busy` 归 A7，两行互证且锚点已核）；OD-4 验收阈值建议值对 A/B 任一选项适用、B 另要求现状基准不回退；OD-2a 验收对 A/B 均可判定（A=next-step splice、B=next-turn 队列）；各 OD 验收标准与选项范围相符；底表行 11/12/13/18/22–25/68–76/100 与 §5.2–§5.5 切片档案逐行对账无矛盾（行 13 的默认方向问题见建议 4，属行文不对称而非矛盾）。
- **未裁决 OD 规则**：「未裁决的 OD 所影响切片保持阻塞」与各 OD 影响列（OD-1→S5、OD-2a→发送路径、OD-2b→S3 行 13、OD-3→S4 行 12/100、OD-4→S1 后续调优、OD-5→S2 跨版本防御）自洽；方案评审通过≠OD 裁决的边界在表头规则中明示。

### 3. 证据一致性（评审要点 3）

- `alpha1-commands-goals-wire.json`：17 探针逐项核对——commands/list 正反形（`agentId` ok / `agent` descriptor 拒）、commands/execute 四形状（`submittedAttachments:[]` 正向 ok、`images:[]` 被拒 verbatim、`agent` 双错、缺第三参被拒）、goals/get ok / goal/get 404、goal 四 mutation 正形均 "no current goal"（shape accepted 注记在档）、skills/list ok、fileReferences 正反形、座位身份（PID 38778 / 0.1.7-alpha.1 / 命令行）、`captured_at 2026-09-23T12:32:39+08:00`。`purpose`/`live_bug_finding`/`goal_mutations_status` 与 §2.3 收窄声明逐字一致：「形状被接受 ≠ 端到端可用」如实标注，端到端成功明确列为 S2 活体复测项，无结论超出证据范围。唯一枚举不精确处见建议 1。
- `alpha1-web-assembly-mounts.json`：11 namespace 错形探针挂载（descriptor 拒绝）+ agentTeams/speech 404 未挂载；pluginRegistryProbe 错误方法更正注记在档（r5 处置如实保留，`fastest` 正确方法重探已挂载）。
- `s1-live-turn-frames.json`：2 轮、frames [154,135]；`verification` 字段三条断言全绿（`revision_consecutive`/`chunk_index_consecutive`/`end_outcome_seq==assistant_message_seq==[17]`）+ `settlement_identity_aligned`；计数轮一 text_delta 42（82 字符）/reasoning_delta 86、轮二 44/65；45/94、41/80 两轮降级为摘要级历史记录的声明在 `historical_note`。
- `s1-runtime-log-excerpt.json`：180 行逐行脱敏日志；`passive_event_types` turn_started/text_delta/turn_completed = 4/172/4（四轮探针 turn 成立）；`runtime_identity` 含 ps 代际（PID 89580、2026-09-23 10:34:41、`/Applications/CordCodeLink.app` 内嵌 runtime）、runtime.json epoch `ce1754ab-…`、`binary_embedded_commit_b4e42d5c5a9a: true`（与本仓 HEAD `b4e42d5c5a9a…` 一致）。
- **凭据扫描**：四文件 cookie/bearer/password/token 关键词命中仅为脱敏声明本身（「cookie value never recorded」）与 LLM 用量计数器（inputTokens 等），无任何凭据值；本轮评审输出亦未出现 cookie 值。
- **全仓对账独立复核**：上游含 `@Remote` 的文件 28 个、装饰器共 123 个（`grep -rcE "^\s*@Remote"` 逐文件计数与 §4 各服务面数一一对应：SessionController 18、Terminal 10、Account 7、WorkspaceController 11、GoalService 7、dynamicCordisRunner 12、pluginManager 10、speech 6、Settings 5、workspaceFiles 5、credentials 3、job 3、directoryPicker 3、messageFeedback 3、llm 3、commands 2、agentPresets 2、subagents 2、其余各 1）；`extends TypertRemoteService` 30 处中 2 处为 `tool-cordis/src/api-catalog.ts` 内嵌声明字符串（非类定义），真实服务类 28；123 = 底表 116 + 装配排除 7（agentTeams 1 + speech 6）成立；底表行 1–116 编号连续无缺漏。

### 4. 纪律红线（评审要点 4）

- **不改 dsh 源码**：上游工作树 `git status --porcelain` 输出为空（干净），红线保持。
- **凭据只读一条且不外泄**：`agent/dsh-web/auth.go` 仅 `client-connection/browser-session` 单记录键；底表 account 7 面、credentials 3 面全部 deliberately unsupported 且 `hello_ack` 不广告；§2.4 statusMessage 只携带版本号与来源标识的约束在案，§7.4 发布门含 secret scan 复扫；本轮评审全程未读取 cookie 文件、未发起需鉴权请求。
- **未知版本 fail-closed**：§2.3 + §2.2（descriptor 校验错误如实上报、禁止猜测兼容/双发试探/递归搜 JSON）+ §2.3「不从 alpha.1 形状外推 alpha.2」——策略自洽，方案中无任何 fallback parser 或静默兼容条款。
- **§3.1 隔离规则**：通用红线（一切写入限新建一次性隔离会话/工作区、禁止对 owner 既有会话写入、结束 `workspace/archiveSession {stopActivity:true}` 归档并记录、无法满足→另座测试实例并保持阻塞）自洽；A2/A3/A5 逐项条款与各自 Gate 行对账一致，A3a 行显式引用 §3.1；A4 条款对 A4b 上传写入的覆盖缺口见建议 3（通用红线已覆盖，逐项枚举未同步，不构成规则违反）。
- **「待捕」「待裁决」为设计状态（评审要点 5）**：A2 阻塞（等 alpha.2 座位，样本取得路径已写明须 owner 升级或经 owner 同意另座）、A3a/A4a/A5/A6/A7 待捕、OD-1～OD-5 待裁决——其「缺项即停」「未裁决不自动实施」规则经交叉对账自洽，本轮不据此判缺陷。

### 5. 本轮执行说明

- 亲核命令：`git rev-parse HEAD` / `git branch --show-current` / `git status --porcelain`（两仓）、`git describe --tags --exact-match HEAD`（上游）、`sed -n`/`grep`/`find`（上游锚点 30+ 处）、`jq`（四个证据文件结构化读取，未打印任何凭据值）、`grep -rcE`（@Remote 逐文件计数）、`lsof -nP -iTCP:3080 -sTCP:LISTEN` + `ps -o pid,lstart,command -p 38778`（运行座位只读核验）。
- 未执行：需 cookie 的 HTTP 探针（本轮无检查项需要新活体样本，全部锚点与证据均为源码侧或已归档）；`dsh --version` 复测（版本声明采信归档证据）；任何写操作、任何文件修改（本报告除外）、任何 git 提交。
- 本报告不替代发布前 secret scan；S2～S5 各自实施前仍须按 §3 五要素取得同版本样本并逐编号判定。
