# 监工指令 10 号完成情况（audit-009 尾项修复）

> **指令**：directive-010-question-recovery-strict-tail-and-ios-install.md（hole-fill，依据 audit-009 partial）
> **提交**：Mac `1e9a714`（实现+测试）、`5b11ba3`（真实 serve reload 回归 + 证据门控 agent description）、本报告提交；iOS 仓无产品代码改动（仍为 `71007a4`）
> **声明**：exec-plan proven ≠ supervisor verified ≠ owner 真机 UI done。提交本报告后停止，等待 audit-010；不进入 owner UI 矩阵。

## 1. audit-009 剩余缺口逐条处置

| # | audit-009 剩余缺口 | 处置 | 证据 |
|---|---|---|---|
| 1 | Question 只检查 messageID 非空，TurnID 直接取 activeTurn；不验证属于该 session/turn，callID 未要求非空 | `handleQuestionAsked` 重写：`tool.messageID` 与 `tool.callID` 双非空；`messageIDs[msgID]==session`（subscriber 观测事实）+ role==assistant + `assistantTurns[msgID]`（帧携带的 `info.parentID`）与 armed turn 交叉验证。`activeTurn` 仅是验证项，不再是归属来源；stale/other-session/unknown 全部 fail closed | `events.go` provenQuestionTurn；`TestAudit010_QuestionAskedSourceCorrelationDestructive`（6 子用例）；fullpath `TestAudit010_QuestionUnprovenIdentityZeroProjection` |
| 2 | GET /question 只用于 ResolveUserInput lookup，重启/漏帧后无 Dock 冷恢复 | StartSession resume 与 SSE 断线重连后跑 `recoverPendingQuestions`：GET /question 只处理目标 session 行；owning turn 先查 live facts，否则一次权威 `GET /session/{id}/message` 事务（assistant `parentID`，且 parent 必须是同事务真实 user 行）；经现有唯一 Kernel route 投影一次；无 ActiveTurnID fallback、无 phantom turn、无 raw second path | `questions.go` recoverPendingQuestions/historyProvenQuestionTurn；`TestAudit010_ColdRecoveryFromGETQuestion` + 破坏矩阵 + `TestAudit010_RecoveryFiltersOtherSessions` + `TestAudit010_RecoveryFailsClosedOnServeErrors`；fullpath 冷重载/断线重连；真实 serve `TestSandboxC6C7/question-reload` |
| 3 | GET+live 去重 | agent 级 `claimQuestionProjection`（questionMu 下 test-and-set）：live asked 与 recovery 对同一 interactionID 恰好一方投影；resolved 时清除 | `TestAudit010_GetLiveRaceConvergesToOneProjection`；fullpath `TestAudit010_QuestionGetLiveRaceSinglePart` |
| 4 | reply/reject/external resolution/reconnect/cold reload 无 full-path 覆盖 | fullpath（真实 adapter+Handlers+relay+deltaBatcher+EventPublisher+Kernel）补齐：reply 原位、reject 原位（source=other_client）、冷重载、断线重连（实测二次拨号后恢复） | `go-bridge/audit010_fullpath_test.go` 全部 6 个测试 |
| 5 | Rename/archive/delete 未限制 exact 200；archive 未比较 echoed timestamp | 三者均改为 `code != 200` 即失败；archive 响应 `time.archived` 必须等于请求发送的 epoch-ms（缺失/零/不同/分数值全失败） | `TestAudit010_MutationsRejectNon200`（201/202/204 + success-shaped body + 无 catalog signal）；`TestAudit010_ArchiveTimestampConfirmation`；真实 serve mutations 子测试 |
| 6 | /agent 只要求 name；缺 mode 默认 primary；description/native 未验证 | name/mode 必须显式非空 string；native 必须显式 boolean；mode→primary 默认已删除。**证据门控偏差**（见 §3.1）：description 在场必须 string、缺席仅允许 hidden 行 | `TestAudit010_AgentRegistryExplicitFields`（10 破坏用例 + hidden 行放行）；真实 serve /agent 7 行全过 |
| 7 | /provider 缺失/null default、connected 与合法 empty 混同 | 顶层 all/default/connected 三键必须显式存在且类型正确（missing/null 是 shape 失败；`[]`/`{}` 合法 empty 通过）；行级 required id 维持 fail closed | `TestAudit010_ProviderTopLevelExplicit`（7 破坏用例 + legal-empty 放行） |
| 8 | Todo 行未拒绝"required 齐全 + 额外 alias key" | `decodeTodoRows` 要求行恰为 `{content,status,priority}` 三键；`{content,…,text:…}` 整体失败且 lastTodos 不被污染（endpoint 与 SSE 双路径） | `TestAudit010_TodoExtraAliasKeyFailsWholeList` |

## 2. 真实测试与输出

**Mac（全部自跑、可独立复验）**

- `go test ./... -count=1 -timeout 15m`：全 PASS（12 包）。
- `go test -race ./agent/opencode-web -count=1`：PASS（5.799s，无 race）。
- `go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- 新增 owning tests：
  - agent 级 `TestAudit010_*`：question 关联破坏矩阵（missing callID / unknown / other-session / stale previous-turn / parentless / correct）、冷恢复 + 5 例破坏矩阵 + 跨 session 过滤 + serve 错误 fail-closed、GET/live 收敛、resolved 事实 turn、agent/provider/todo/mutation 严格尾。
  - fullpath `TestAudit010_Question*`（6 个）：零投影、reply/reject 原位、冷重载（0.02s 恢复）、断线重连（1.03s，二次拨号后 `recovered pending question through the Kernel route session=ses_ocw1 question=que_a7 turn=msg_u1`）、GET+live 单 part。

**HTTP status 破坏矩阵（mutation）**

| 操作 | 200 | 201 | 202 | 204 | 404 |
|---|---|---|---|---|---|
| rename | ✅（echo 校验后） | ❌ HTTP 200 only | ❌ | ❌ | ❌ |
| archive | ✅（timestamp 等值后） | ❌ | ❌ | ❌ | ❌ |
| delete | ✅（true+404+list 收敛后） | ❌（body=true 仍失败且不 signal catalog） | ❌ | ❌ | ❌ |

**Question GET/live race 的 Kernel/part 结果**（fullpath，真实 Kernel 栈）：`TestAudit010_QuestionGetLiveRaceSinglePart` 结束态为恰 **1 个** `user_input` part（interactionId `que_a7`、turn `msg_u1`、status pending）；reducer `userInputs` map 每 interaction 恰一条（upsert 语义），reply/reject 后同一 part 原位迁移为 answered/rejected、source=other_client，part 计数不变。live 侧因 claim 已被 recovery 持有而零发射（agent 级测试断言 route 事件总数为 1）。

**真实隔离 1.18.18 sandbox（4398/4399，已回收）**

- `TestSandboxC6C7`（mock 计数器复位后整跑一次）：question ✅（官方 reply 路由）、**question-reload ✅**（live asked 留待 → 全新 adapter 进程 StartSession → GET /question + 真实 history `parentID` 事实恢复到同一 source-proven turn → 官方 reply 收敛）、todo ✅、permission ✅、mutations ✅（含 exact-200 与 archive 时间戳等值在真实 serve 上通过）。
- `TestSandboxC4TurnTerminals` / `TestSandboxC2ListGetCycle` / `TestSandboxPromptOptions` / `TestSandboxEndToEnd` / `TestSandboxArchiveDelete`：全部 PASS。
- 4096 全程 owner-managed；harness 未触碰。规定重装流程（killall+cp+open）导致 app 重建其托管 serve：4096 PID 23874 → 48742（父进程 48718 = 重装后 app），8777 = PID 48806 `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`。

**iOS**

- 定向测试（仅 CCCodeTests 选定两类，无 UI tests）：`ModelVariantSelectionTests` 7/7、`OpencodeWebVariantAgentWireTests` 4/4，TEST SUCCEEDED。
- 真机：现场探测到 paired iPhone（connected）；`scripts/run.sh device --device <现场值>` 完成 Debug 构建、安装并启动（`Launched application with org.openagi.cordcode`）。仅验证产物与启动，未做任何自动点击；设备标识未写入任何文档。

## 3. 诚实披露

### 3.1 /agent description 的证据门控偏差

指令字面要求"每个 verified row 必须显式携带 description"。真实 1.18.18 serve 的 /agent 共 7 行，其中 `compaction/summary/title`（均 `hidden:true` 内部 agent）**不带 description**；官方 schema 中 description 为 optional。若按字面全行强制，send/agent 列表在真实 serve 上直接失败（本人在 sandbox 首跑中实际复现）。按指令同节"可选字段只按同版本证据处理"实现为证据门控：在场必须 string（null/错型失败）、缺席仅 hidden 行合法、非 hidden 行缺席 fail closed。破坏测试与此同步。

### 3.2 其他

- `TestAudit008_QuestionReachesProjection`（directive-009 产物）更新为 A7 真实排序：旧用例只 arm turn、不观测 assistant 消息，恰是 audit-009 判定的宽松前提；新用例在 asked 前推入带 `parentID` 的 assistant `message.updated`。
- 断线重连场景在 fullpath 层验证（wire 级断流 + 实测二次拨号恢复）；真实 serve 层验证的是进程级 cold reload（更严）。真实 serve 未做 TCP 中途掐断。
- sandbox mock 的 A8 写入计数器为一次性：C6C7 的整跑通过记录在 mock 复位后的单次运行；连续重跑 todo 子测试会因 mock 状态失败（与产品代码无关）。
- iOS 仓本指令无产品代码改动（variant UI 为指令 9 已验证项，仅复跑定向测试）。

## 4. 双仓状态

- Mac 仓：本报告提交后 clean（实现 `1e9a714`、sandbox 回归 `5b11ba3`）。
- iOS 仓：clean @ `71007a4`。

## 5. 三轨

- exec-plan proven：`audit009-tail-review-fix-{impl,tests,regression}` 三元组全部 done（self-attested，测试项可独立复跑）。
- supervisor verified：**待 audit-010**。
- owner 真机 UI 矩阵：未进入（等待 audit-010 授权）。
