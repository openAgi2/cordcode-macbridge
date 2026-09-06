# dsh-web 并行子代理 workflow 卡：消息正文对齐官方 3080 Web（2026-09-06）

## 背景与目标

owner 报障（2026-09-06 15:40）：Mac 3080 web 版在会话正文里渲染并行 subagent 运行卡
（`plan109-subagent-rewrite · 4 个成员`，可展开、可点成员进子会话、完成后自动收起），
同一 session 同一位置 CordCode iOS 只有「正在处理任务」的活动行文案，卡片整段缺失。

目标：把官方 `tool-workflow/*` 四事件折叠成会话正文里的 workflow 卡投影
（live + 冷拉同形），iOS 按官方 `WorkflowRunPanel` 排版渲染，运行中成员可点开
既有 §13 子任务详情。

**前置 P0 级 bug（本设计顺带修复）**：`agent/dsh-web/codec.go` apply() 没有
`tool-workflow/*` 分支 → default → `resetf("unknown required event type")` →
**每个 tool-workflow 事件都重置 live codec**（丢 openBlocks 增量、下一帧重复
turn_started）。官方把这四类注册在 `KNOWN_SESSION_EVENT_TYPES` 且为 REQUIRED
（不可 ignorable），任何 subagent 并行运行期间直播流都会触发。冷拉
`mapHistoryEvents` 无 default 分支静默跳过——这就是为什么冷打开不炸但也没有卡。

## 上游源码锚点（source-first，全部已读）

仓库 `/Users/jacklee/Projects/deepseek-harness`，commit `d347e703908d0406b7a7ef80e3a0e594d86b2215`
（2026-09-04，release/dsh-0.1.3-alpha.1）：

| 官方符号 | 语义 |
| --- | --- |
| `packages/workflow/tool-workflow/src/types.ts` | 四事件 data 形状：run-start `{runId,name}`；agent-start `{runId,seq,label,phase?,childId}`；agent-end `{runId,seq,outcome}`；run-end `{runId,stopReason}`。`WorkflowAgentOutcome = completed|failed|cancelled`；`WorkflowStopReason = completed|cancelled|error` |
| `packages/workflow/tool-workflow/src/invariant.ts` | journal 级不变量（append 时官方服务端已保证）：run-start runId/name 非空且不重复；agent-start 需开放 run、seq 正整数、label string、phase 存在时必须 string、childId 非空、seq 不重复；agent-end 需成员存在未结算、outcome 枚举；run-end 需 stopReason 枚举且无未结算成员 |
| `packages/workflow/tool-workflow/src/index.ts` | 四事件都是 **log-only**（append 进 journal，不进模型上下文）；`workflow/agent-start` 发 `phase === undefined ? {} : {phase}`（缺席≠null≠""） |
| `packages/client/ui-workflow-run/src/client/workflow-definition.ts` | 折叠真值：按 runId 聚成一个 keyed chat 节点；`projectWorkflow` 视图投影——成员按 phase 身份分组（`workflowPhaseKey`: `undefined→null→"missing"`，`""→value:0:` 独立身份），成员状态 = outcome 映射或（locationClosed ? interrupted : running），run 状态 = stopReason 映射或（locationClosed ? interrupted : running）；`locationClosed` = 锚点 turn/step 已闭合 |
| `packages/client/ui-workflow-run/src/client/WorkflowRunPanel.tsx` | 渲染器：RunHeader（名称 + N 个成员 + StateDot 状态文案）→ PhaseSection（阶段名/未分阶段 + N 个成员 + 状态计数摘要）→ MemberRow（dot + label + 状态文案）；disclosure 规则 running/abnormal 自动展开、clean 自动收起（焦点在内延迟收起）；成员行**仅 running 且在普通会话列表且 origin=subagent 且 parentId 匹配才可点**（`navigableMembers`）→ `openSession(childId)` |
| `packages/client/ui-workflow-run/src/client/locales.ts` | zh 文案真值：运行中/已完成/失败/已取消/已中断；N 个成员；未分阶段/空阶段名；计数 `已完成 3 · 失败 1`（" · " 连接，interrupted 且有 completed 时 completed 进列表） |
| `packages/core/session/src/known-event-types.ts` | 四类型 REQUIRED 注册（不是 ignorable） |

真实 journal 样本（owner 事故会话 `session-3eacd40e-40ff-410f-8924-4964f7563740`，
含 6 个 run）：plan109-subagent-rewrite（4 成员全 completed，未带 phase）；
plan110-subagent-four（seq2 写武大郎篇 **failed** → 触发重试 run
plan110-subagent-wudalang-retry）；run-start 紧跟 `tool/call` name="workflow"
之后、全部落在开放 turn 内；全部样本 agent-start 无 phase 字段。
fixtrue 归档：`agent/dsh-web/testdata/goal_round_seat_windows.jsonl` 同目录新增样本文件。

## CordCode 有意保留的差异（记录）

1. **interrupted 推断粒度**：官方按 step/turn location（step 级）；CordCode 投影是
   turn 粒度——turn 终态（turn_completed/aborted/error）时仍 running 的 run/成员
   → interrupted。语义相同（锚点闭合即中断），粒度更粗。
2. **折叠层**：官方折叠（workflow-definition.ts）+ 视图投影（projectWorkflow）在
   客户端两步走；CordCode 在 Mac 侧 agent/dsh-web 折叠成整值快照（live codec 与
   冷拉共用 `workflow_fold.go`，与 command_fold.go 同构），reducer/iOS 是纯镜像。
   interrupted 由 reducer turn 终态 fixup 注入（折叠层不知 turn 闭合）。
3. **成员导航**：官方 `openSession(childId)` 打开子会话视图；iOS 复用 §13
   `BackgroundTaskDetailView`（taskID=child session id）。可点门控与官方一致
   （仅 running）；已完成成员的详情仍从既有 §13 任务面板进。

## 交互流程（四拍）

1. **打开输入的地方**：iPhone 打开 dsh 会话 → 正文 assistant 气泡里、workflow
   工具行的下一位，出现 workflow 卡（折叠行：run 名称 + 「N 个成员」 + 状态点与
   状态文案）。直播中 Mac 发起 subagent 并行任务时，卡随 run-start 实时出现。
2. **输入**：点卡头展开/收起（run 级、phase 级各自独立）；展开后每个成员行
   （状态点 + 标签 + 状态文案）可读。无文本输入。
3. **发出去**：点**运行中**的成员行 → 发 `subagentMemberTap {childSessionId}`
   bridge 事件 → native 弹出 §13 子任务详情 sheet（getBackgroundTaskDetail，
   taskID=childSessionId）；非运行中成员行不可点（官方 navigableMembers 同门控）。
4. **过程展示**：成员随 agent-start 逐个出现、状态点随 agent-end 变色
   （运行中→已完成/失败/已取消）；run 结束后整卡状态切换并**自动收起**
   （running/abnormal 自动展开、clean 自动收起，官方 disclosure 同规则）；
   中断（turn 结束仍未收 run-end）显示「已中断」。冷重开会话时同卡从
   journal 历史折叠重建，形状与直播一致。

## 实现（Mac 侧）

### 1. core/message.go — 事件与域模型

- `EventWorkflowRun EventType = "workflow_run"`；`Event.WorkflowRun *WorkflowRunEvent`。
- `WorkflowRunEvent`（整值快照，折叠后发射）：`RunID/Name/Status/Phases`。
- `WorkflowRunPhase{Phase *string, Members []WorkflowRunMember}`（`Phase nil`=
  未分阶段=官方 missing；非 nil `""`=空阶段名，独立身份）；
  `WorkflowRunMember{Seq, Label, ChildSessionID, Status}`。
- Status 枚举 `running|completed|failed|cancelled|interrupted`（官方
  WorkflowRunStatus）。折叠层只产前四种 + running；interrupted 由 reducer 注入。

### 2. agent/dsh-web/workflow_fold.go — 单一折叠真值（新文件）

镜像官方 workflow-definition.ts 的 fold（state：`name/stopReason?/members[]`，
member `seq/label/phase?/childId/outcome?`）+ invariant.ts 校验（违规返回
error → codec resetf）+ projectWorkflow 快照（**无 interrupted**——折叠层不知
turn 闭合；分组按首现顺序，phase 身份 `workflowPhaseKey` 同式）。live codec 与
history 冷拉共用（command_fold.go 先例）。

### 3. agent/dsh-web/codec.go — 四 case（P0 修复）

- apply() switch 增 `tool-workflow/run-start|agent-start|agent-end|run-end` 四
  case → 解码 → `workflowFold.onX`（违规 resetf）→ `snapshot(runId)`。
- 可归属（`activeTurnID != ""`）→ 发射
  `core.Event{EventWorkflowRun, TurnID: activeTurnID, WorkflowRun: &快照}`；
  无 active turn（mux 附着落在 run 内的窗口）→ **折叠继续、事件不发射**
  （fail-open；后续事件首次可归属时整卡补齐——官方「无 start 不渲染节点」对位）。
- 校验门：runId/name 非空、seq 正整数、outcome/stopReason 枚举、label string、
  phase 存在时 string、childId 非空、重复 run/重复 seq（官方 invariant 镜像）。

### 4. go-bridge/events.go — 逻辑事件

`case core.EventWorkflowRun` → `"workflow_run"` data
`{turnId, workflowId, workflowName, workflowStatus, workflowPhases:[{phase:string|null,
members:[{seq,label,childSessionId,status}]}]}`（phases 经 helper 转 wire map）。

### 5. go-bridge/projection_types.go — part 与 op

- `ProjectionPart` 增 workflow 变体：`WorkflowID/WorkflowName/WorkflowStatus/
  WorkflowPhases []WorkflowPhaseProjection`；
  `WorkflowPhaseProjection{Phase *string, Members []WorkflowMemberProjection{Seq,
  Label, ChildSessionID, Status}}`（JSON 键 `workflowId/workflowName/
  workflowStatus/workflowPhases/phase/members/seq/label/childSessionId/status`）。
- `PartOp.Op` 增 `"upsert_workflow"`（按 workflowId 原地 upsert，同 upsert_user_input
  契约：owning turn 必须已在 iOS 侧存在 → staging 防线）。

### 6. go-bridge/projection_reducer.go — 折叠入 part + 终态 fixup

- `projectionSession` 增 `workflowRuns map[string]workflowPending`（同 userInputs
  模式：`{turnID, part}`，按 runId 续接归属）；clone/Restore（从基线 part 重建索引）
  /flush 空判全同 userInputs。
- `case "workflow_run"`：workflowId 必填；turnId 先取事件、再被既有 pending 归属
  覆盖（跨源身份）；turn 必须已存在（user_message/text_delta 已建）否则丢弃
  （fail-closed，不造幻影 turn）；upsert part（`Type=="workflow" && WorkflowID`，
  整值替换=subagent_part 模式）→ 记 pending → `stageTurnForFlush`。
- `flushLocked`：pending workflows → `PartOp{Op:"upsert_workflow", TurnID/MessageID:
  owning turnID, Part}`；`stageOwningTurnsForPendingParts` 增 workflow 归属 staging。
- **终态 fixup（官方 locationClosed 镜像）**：`turn_completed`/`turn_aborted`/
  `turn_error` 与 `settleOtherOpenTurns` 收口时，遍历该 turn assistant parts 中
  `Type=="workflow" && WorkflowStatus=="running"`：成员 running→interrupted、
  run→interrupted，并同步 ps.workflowRuns pending（flush op 带上 fixup）。
  幂等：迟到的 run_end 整值覆盖为真实结局。
- `isSessionSyncV2RawTimelineEvent`（projection_delivery.go）增 `"workflow_run"`
  （K4 seal：投影是唯一内容写者）。

### 7. agent/dsh-web/history.go — 冷拉折叠

- `dshTurnAccumulator` 增 `workflows map[string]int`（runId → parts 下标）；
  walk 增四 case：run-start 且 turn 开 → append part map
  `{"type":"workflow","workflowId","workflowName","workflowStatus",
  "workflowPhases":[…]}`（wire 键与 ProjectionPart JSON 一致）；后续事件原地改
  parts[i]（同一 fold 快照重建）；turn 未开（附着前残留形）→ 整 run 跳过。
- `flush`（turn/end 或 torn tail）时对仍 running 的 run 应用 interrupted fixup
  （torn tail 经 converter 的 turn_completed 由 reducer fixup 兜底，双保险幂等）。

### 8. go-bridge/handlers_projection.go — 冷拉转换

`openCodeRichHistoryEntryToProjectionEvents` assistant 分支增 `case "workflow"`：
把 part map 整值转成一个 `workflow_run` hydrate 事件（turnId=该 entry 的 turnID，
data 键与 live wire 相同）→ reducer 同一 case 折叠。冷热同形。

### 9. 协议文档

- `docs/protocol/schema/bridge-v1.types.ts`：`BridgeProjectionPart` 增 workflow
  变体、`BridgePartOp` 增 `upsert_workflow`；iOS mirror 同步。
- `docs/protocol/bridge-v1.md`：Part vocabulary 增「workflow」节（官方对位、状态
  枚举、phase 身份、upsert 语义、interrupted 注入点、subagentMemberTap）。
- `BridgeProtocolSchemaRevision` → `"2026-09-06"`。

### 10. Mac 测试

- codec：四事件折叠发射（plan109 样本回放→整卡）、无 turn 丢弃但折叠保持、
  违规形状 resetf（重复 run/seq、坏 outcome/stopReason、空 runId/name）。
- fold：真实 journal 样本（plan109 全 completed 无 phase；plan110 seq2 failed；
  未结算 torn run → running）。
- reducer：live 折叠（run-start 入 active turn、agent-end 更新、run-end 收口）；
  turn_completed fixup（running→interrupted + pending 同步）；patch op
  （upsert_workflow + owning turn staging）；Restore 重建索引后续接归属。
- history：journal 样本回放（含 turn 内嵌位、卡在 tool 行后）；goal 轮嵌位不回归。
- converter：part map → workflow_run 事件形状与 live wire 一致。

## 实现（iOS 侧，`../cordcode-ios-plan-approval`）

1. `Models/SessionProjection.swift`：`SessionProjectionPart` 增
   `workflowId/workflowName/workflowStatus/workflowPhases`（CodingKeys + init）；
   `applyingPartOps` 增 `case "upsert_workflow"`（按 workflowId 原地 upsert，
   upsert_user_input 同式）。
2. `SessionProjectionMapping.swift`：`mapPart` 增 `case "workflow"` →
   `.workflow(MessageWorkflowRun)`；workflowId 空 → fail-closed 丢弃（context_injection
   同式）。`MessageWorkflowRun{workflowId,name,status,phases:[{phase:String?,
   members:[{seq,label,childSessionId,status}]}]}`（Models/Message.swift 新类型）。
3. `AssistantTimelineRenderSpec.swift`：`buildBlocksFromParts` 增 `case .workflow`
   （先 flush 文本/reasoning 再入块，同 command/contextInjection 位）；新
   `AssistantWorkflowBlock`（official-mirror 文档注释）。
4. `MessageWebSnapshotBuilder.swift`：块 → `WebWorkflowRunBlock`
   `{kind:"workflow_run", workflowId, name, status, phases:[{phase,members:[…]}]}`；
   `WebAssistantBlock` union 增该 kind（message-web/src/types.ts）。
5. message-web：新 `WorkflowRunBlock.tsx` 镜像 WorkflowRunPanel——RunHeader 折叠行
   （名称+成员数+dot+状态文案）、PhaseSection（阶段名/未分阶段+成员数+计数摘要
   「已完成 3 · 失败 1」）、MemberRow（dot+label+状态）；disclosure：
   running/abnormal 展开、clean 收起（无键盘焦点，简化官方 focus 逻辑为
   用户手动 toggle 后不自动收起）；running 成员可点 → `subagentMemberTap
   {childSessionId}`（postToNative）；zh 文案逐字取官方 locales。
6. `MessageWebBridge.swift`：event enum + decode 增 `subagentMemberTap
   (MessageWebSubagentMemberTapPayload{childSessionId})`；
   `ChatUIKitContainerView` 处理 → `presentDetailSheet(BackgroundTaskDetailView…)`
   （复用 §13 loadDetail 闭包，taskID=childSessionId）。
7. iOS 协议 mirror（docs/protocol）同步 Mac 权威包；npm build message-web；
   新 .swift 文件后 `xcodegen generate`；定向 build。

## 验证

- Mac：`go build ./go-bridge && go test ./agent/dsh-web/... ./go-bridge/... -count=1`
  （定向新增测试类）。
- iOS：定向 build + 既有投影测试类回归。
- 真机验收（owner）：Mac 3080 web 发起并行 subagent run → iPhone 同会话正文出现
  卡片、成员实时变化、运行中成员可点开详情、完成后自动收起；重开 App 冷拉同形。

## 交付完成情况（2026-09-06 17:4x，self-attested）

**Mac 侧**（本工作树 `plan/approval-layer`，未提交改动）：

- `core/message.go` WorkflowRunEvent；`agent/dsh-web/workflow_fold.go`（单一折叠真值）；
  `codec.go` 四 case（P0 修复：tool-workflow 事件不再 reset live codec）；
  `go-bridge/events.go`/`projection_types.go`/`projection_reducer.go`（upsert_workflow
  op + turn 终态 interrupted fixup）/`handlers_projection.go`（冷拉转换）；
  `agent/dsh-web/history.go`（冷拉折叠 + torn tail fixup）；协议 pack
  （bridge-v1.md / bridge-v1.types.ts / schemaRevision 2026-09-06）+ iOS mirror。
- 定向测试 12 项全绿（`go test ./agent/dsh-web/... ./go-bridge/... -run 'Workflow'`）
  + `go vet` 干净。
- Release 构建（runtime commit 6197da5b8f89）已覆盖安装 /Applications 并重启：
  8777 监听者为内嵌 runtime（PID 96858，启动 17:28:48 晚于构建 17:26:38）；
  无 /Applications 之外的违规进程；已安装二进制与构建产物 SHA-256 一致且含
  `upsert_workflow`/`workflow_run` 特征符号。

**iOS + message-web 侧**（`../cordcode-ios-plan-approval`，未提交改动）：

- 投影链：`SessionProjectionPart` workflow 字段 + `upsert_workflow` op →
  `mapPart`（fail-closed：空 workflowId/未知 run status 丢弃，成员 status 缺失降级
  running）→ `MessageWorkflowRun` → render spec `AssistantWorkflowBlock` →
  web `WebWorkflowRunBlock`（canonicalItemIDs 带锚定）。
- 双渲染路径：AssistantTurn 非行路径 + 虚拟化行路径（renderRows.ts 增 `workflowRun`
  row kind + SplitAssistantTurnRows 渲染分支 + hideDetailProcessRows 豁免）。
- 成员点击：`subagentMemberTap {childSessionId}` web→native 事件 →
  `presentSubagentMemberDetail`（§13 BackgroundTaskDetailView 复用：getBackgroundTaskDetail
  + 可选 cancel + 失败态「无法加载子任务详情」）；仅 running 成员可点（官方
  navigableMembers 同门控，native 侧详情接口 fail-closed）。
- 契约门：`contract.json` 增 WebWorkflowMember/Phase/RunBlock + subagentMemberTap；
  fixtures `web-events.json` 补样本（13 事件全覆盖）→ `contract:generate` 重生
  Swift/TS mirror；shared-message-renderer 平行类型副本同步。
- 定向测试：WorkflowTimelineTests 7 + AssistantTimelineRenderSpec 30 +
  MessageWebBridgeCodec 22 + ContextInjection 5 = 64/64 绿；
  MessageWebContractParity 8 绿；MessageWebContractFixture 2/2 绿（预期清单已补
  subagentMemberTap）；message-web vitest 323 绿；shared vitest 280 绿。
- 真机安装（owner 授权设备 00008140-001E69503453001C）：`scripts/run.sh device` 已执行。

**验收待 owner**：重开并行 subagent 会话（如 plan109 那条），核对正文卡
（名称/N 个成员/状态/自动展开收起）、running 成员点击弹子任务详情、冷重开同形。
