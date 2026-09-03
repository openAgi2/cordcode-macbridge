# 计划审批层第一批实施方案（grok / claude / dsh）

- 日期：2026-09-04（开工日）
- 性质：**实施方案文档**，呈 owner 裁决后经 exec-plan 驱动实施。事实基线 =
  [2026-09-03-plan-mode-cross-backend-survey.md](2026-09-03-plan-mode-cross-backend-survey.md)
  v1.5（基线提交 a2200cf），本文档不另造协议假设；调研档之外的新事实仅限 §2 前置核实
  结论（本日实测，锚点齐全）。
- 范围：第一批 = grok / claude / dsh 三链路 plan 审批端到端（Mac 桥接线 + iOS 呈现与应答）。
  第二批（codex 展示先行 / opencode）不在本分支，仅 §8 预留结论位。
- 驱动方式：`/exec-plan docs/2026-09-04-plan-approval-implementation.md start`；
  `.exec-plan/state/` 由 exec-plan 自行维护，禁止手写状态 json。

## 0. 来源清单（P0 门，2026-09-04 方案撰写时实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval  分支=plan/approval-layer
  提交=a2200cf4771b7ded4a09577bdcf9599d145d93c1  未提交状态=干净
  任务预期分支=plan/approval-layer → 匹配
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-plan-approval  分支=plan/approval-layer-ios
  提交=61f67bf63a8a5f6e10e9e9c62fbd9fe36f2236cd  未提交状态=干净 → 与任务指定配对一致
预期产品特性=permissionKind=plan_review 的计划审批卡（计划全文 + approve/requestChanges/quit
  动作 + 反馈输入）在 grok/claude/dsh 三 backend 端到端可用
上游（只读，均已在开工指令 pin 上，无需 checkout）：
  grok-build@72a61251fcffb464bcc687aeb5a998e5a98ec0c9（main，干净）
  deepseek-harness@49a606bc5b5934603f22a26957a07dc799ab0291（master，干净）
    ⚠ dsh 转发面核实额外使用 tag dsh-v0.1.1-rc.2（本机安装版，理由见 §2.1）
  codex@50fffd5ed367aa99491d9ec58575626fce4e9dd4（main，干净；第二批，未读）
  opencode@69c172e8a7c0086887b1f93ed5a162f14b6aa0c5（dev，干净；第二批，未读）
Claude SDK=https://unpkg.com/@anthropic-ai/claude-agent-sdk@latest/sdk.d.ts 实抓
  =0.3.259（2026-09-04 抓取，存 /tmp/claude-sdk-latest.d.ts）
```

## 1. 目标

iPhone 上出现**计划审批卡**：显示计划全文（markdown）、按 backend 能力提供
approve / requestChanges（带反馈文字）/ quit 动作，Mac 桥把动作翻译为各 backend
官方语义。三链路共用同一 wire 扩展（§4），后端翻译逐项镜像官方不变量（§5）。

明确不做（第一批）：行内评论（调研档 §7.2 建议 deliberately unsupported，等价物 =
requestChanges 反馈文本手写行号引用）；codex / opencode（第二批，§8）。

## 2. 前置技术核实结论（2026-09-04 本轮实测，零 owner 依赖）

### 2.1 dsh 转发面可见性——**闭合**（第一批 dsh 定档门槛通过）

调研档 §6.6 的疑问：plan 投影 / question `detail` 在 dsh-web 转发器消费面是否可见。
**结论：两者全部可见，且比调研档预期更好——wire 上还有官方 plan-review intent 标记。**

核实方法与锚点（关键事实：**本机安装版 dsh = 0.1.1-rc.2，与 pin 49a606bc
（=0.1.2-alpha.5）不同版本**；按 CLAUDE.md source-first 第 1 条「以目标版本源码为准」，
转发面核实用 git 只读读取 tag `dsh-v0.1.1-rc.2` 的树，未动 checkout）：

1. `question/requested` mux 帧**完整透传官方 AskUserQuestionItem**，zod 严格校验，含：
   `question`、`header`、`detail`（= plan 全文）、`options`、`multiSelect`、
   **`intent` 判别联合（`{kind:'plan-review', approve:<label>}`）**。
   锚点：`packages/host/apiproxy/src/api/events.schema.ts:22-29,51`（@ dsh-v0.1.1-rc.2）。
   → dsh plan 审批识别有官方结构化标记可依赖，无需猜「header 是不是 Plan review」。
2. mux `session/event` 帧是 **raw session-event passthrough**（`MuxFrame` 类型注释原文
   "raw session-event passthrough"）；`'plan/mode': {active: boolean}` 在已知会话事件表
   （`packages/core/session/src/known-event-types.ts:40` @ 同 tag；plan-mode
   `src/index.ts:53` last-wins 折叠）。
   锚点：`packages/host/apiproxy/src/api/events.ts:69-70`（@ dsh-v0.1.1-rc.2）。
   → plan 投影（active/pending）在转发面可见；第一批**不消费**它（审批卡由
   question 驱动已足够），只作为后续状态指示预留。
3. **版本漂移警示（新增风险项，进 §7）**：pin 49a606bc 已删除 ApiProxy 包
   （commit `4f00a8b82a` "refactor(api): remove ApiProxy package"），新架构为 Gateway
   `/api/remote.mux` + `$events` remote 事件（官方笔记
   `.agents/notes/implemented/architecture/2026-08-18-session-history-and-event-transport.md`：
   "`approval/request` and `user-questions/request` events are forwardable waterfalls"）。
   CordCode dsh-web 消费的 `/api/events.mux` 属 legacy ApiProxy 面。**本机安装版
   0.1.1-rc.2 仍是 legacy 面，plan 层实现必须绑定安装版 wire；用户升级 dsh 到
   0.1.2+ 时 dsh-web 整个传输层需迁移（既有适配风险，非本项新增，但在 §7 登记）。**
4. CordCode 现状挂点（本仓 a2200cf，与调研档 §6.4 一致并补准）：
   - `agent/dsh-web/approvals.go:326-338`：question 解析结构**无 `intent` 字段**——识别
     plan-review 需补解析；
   - `core/message.go:394-403`：`UserInputQuestion` **无 Detail 字段**——plan 全文无落点；
   - `agent/dsh-web/session.go:207`：`RespondQuestion` 恒传 `custom=""`；底层
     `respondQuestion(ctx, …, custom)`（`approvals.go:519+`）第 5 参即官方反馈通道。

### 2.2 relay 帧预算——**闭合**（§7.4 / §8.3-8 关联项）

10KB 级 plan 载荷经 relay（实时 + mailbox）**无隐性截断**，余量 ≥3 个数量级：

| 层 | 限制 | 锚点 |
| --- | --- | --- |
| relay-server WS 帧上限 | MaxFrameBytes 默认 **32 MiB** | `relay-server/internal/relay/server.go:183,576,597` |
| relay-server mailbox | MaxMailboxBytes 默认 **50 MiB**/设备，超容 FIFO 驱逐、单帧超容才拒绝 | `server.go:177,668`；`internal/relay/store.go:295,321` |
| go-bridge 入站队列 | 256 帧 / 8 MiB 队列级预算，无单帧截断 | `go-bridge/relay_inbound_scheduler.go:11-14,81-86` |
| go-bridge 出站 | gzip 阈 32KB（10KB 不触发）；分片仅 bulk 类；逻辑上限 50MB | `relay_conn.go:91,426-454`；`relay_outbound_writer.go:28-31` |
| 生产佐证 | grok plan 1.4–1.6KB 已在生产 relay 路径上线（调研档 §2.4） | go-bridge.log:2885,7928 |

→ **§8.3-8 的「截断策略」定性为纯 iOS 渲染层产品决策**（长文性能与滚动），不是传输
风险项。iOS 侧按 10KB 级 markdown 设计渲染即可，无需传输层截断。

### 2.3 claude SDK 类型复验——**闭合**（实抓 @latest = 0.3.259）

与调研档 §3 全部一致，无翻案项：

- `planApproval` 字段 **0 命中**（复验「不存在」结论）。
- `PermissionResult`：allow `{updatedInput?, updatedPermissions?, …}` | deny
  `{message: string(必填), interrupt?, …}`（sdk.d.ts:2324-2334）。
- `PermissionUpdate` 联合含 `{type:'setMode', mode, destination}`（:2359-2363）；
  `PermissionMode = 'default'|'acceptEdits'|'bypassPermissions'|'plan'|'dontAsk'|'auto'`
  （:2302）。
- 新增细节（调研档未列，无碍结论）：deny 分支另有 `interrupt?: boolean` 字段，第一批
  不使用。

### 2.4 claude ExitPlanMode 仓内 fixture——移入 Phase 1 实施

调研档 §3.4 要求补的 fixture 属代码改动，作为 Phase 1 任务执行（§6 列明 fixture 来源）。

## 3. Owner 决策点（呈报裁决，本文档不自行定稿）

| # | 决策点 | 选项 | **推荐** | 理由 |
| --- | --- | --- | --- | --- |
| D1 | iOS 计划卡形态（§8.3-1） | a) 独立计划卡 b) 扩展现有权限卡 | **a 独立计划卡**（`permissionKind=plan_review` 分支新视图） | 计划全文渲染（markdown 查看器 + 折叠/展开）与两键权限卡交互差异大；§25 已证明复用权限卡可行但展示差；新视图不动旧卡回滚面（旧卡零改动，分支不命中即回滚） |
| D2 | requestChanges / quit 进不进第一批（§8.3-2） | a) 三动作全进 b) 仅 approve（维持现状二值） | **a 全进** | 三 backend 官方语义都有落点（调研档 §7.2 词汇表：grok cancelled+feedback / abandoned，claude deny+message，dsh Keep-planning+custom）；不拆则「拒绝」语义偏差（quit≠打回）继续存在；通道全部现成，增量在 iOS 反馈输入框与 Mac 翻译表 |
| D3 | dsh quit 目标语义（§8.3-9） | a) dismiss（reject 整批 ≈ ASK_CANCELLED） b) `/plan off` | **a dismiss** | 官方 `ASK_CANCELLED` 语义「用户接管、agent 停下等消息」（调研档 §6.3）与统一 quit「用户拿回回合」一致；`/plan off` 是关闭 plan mode 的另一命令通道（commands.execute），语义是「退出规划状态」而非「不回答当前审批」，混用会让 agent 状态突变；且 dismiss 有现成 `RejectQuestion` 管道零新增 |
| D4 | 反馈必填性（§8.3-3） | a) requestChanges 反馈必填 b) 可选 | **b 可选** | 三 backend 官方均允许空反馈（grok feedback 仅在有文字时上 wire；dsh custom 可空；claude deny.message 需非空——Mac 端空时填固定文案）；必填只增加 iOS 输入阻力。文案区分：空反馈 requestChanges =「打回重做（未说明原因）」，quit =「放弃本次计划」 |
| D5 | claude 批准的模式二选（§8.3-4） | a) 固定单一行为（不透传 updatedPermissions） b) 透传 setMode 二选 | **a 固定不透传** | 透传属「基于类型的推断，需真机验证」（调研档 §3.3 未核实项）；第一批先用纯 allow（现有 RespondPermission allow 路径已在生产，形状零新增），行为 = 批准计划、后续写操作仍逐个走 iOS 权限卡（与 CordCode 远程审批面定位一致）；二选项等真机验证后第二批补（届时按开工指令另行申请授权） |
| D6 | 统一字段命名（§7.1） | 以调研档为基线：`permissionKind="plan_review"`、`plan{content,contentFormat,title,planFilePath}`、`permissionActions`、`planAction`、`feedback` | **按基线，另统一 camelCase**：`planAction` 取值 `approve\|requestChanges\|quit`（与 `permissionActions` 一致，弃调研档示例的 `request_changes` snake_case 写法） | 与既有 wire 词汇（`approveAlways` 等 camelCase）一致；**标注「待立项评审确认」**——本方案即立项评审载体，owner 认可 D6 即视为定稿 |

附带呈报（非阻断，owner 可在裁决时一并推翻）：

- 行内评论（§8.3-6）：按调研档建议第一批 **deliberately unsupported**；grok 等价物 =
  requestChanges 反馈文本手写行号引用（`Proposed plan line N: …` 官方格式，调研档 §2.2）。
- dsh plan 投影（active/pending）消费：第一批不做，只保留 §2.1 结论位。

## 4. 统一 wire 设计（调研档 §7.1 基线；D6 待评审确认后即为定稿）

### 4.1 下行：扩展现有 `permission_request`（不新开事件族）

沿用 §25 已验证管道（first-answer-wins、`permission_resolved` 收口、iOS 卡片消费链全部
复用）。`go-bridge/events.go:181-203` 现有字段不动，新增可选载荷：

```jsonc
{
  "type": "permission_request",
  "requestId": "...", "toolName": "...",
  "permissionKind": "plan_review",
  "permissionActions": ["approve", "requestChanges", "quit"],
  "plan": {                          // permissionKind=plan_review 时必填
    "content": "<markdown 全文>",
    "contentFormat": "markdown",
    "title": "<首个标题行，可选>",
    "planFilePath": "<本地路径，可选；claude 有>"
  }
}
```

- `core.Event`（`core/message.go:455-466` 区域）新增 `Plan *PlanPayload` 字段（结构体
  含上述四字段）；`PermissionKind="plan_review"` 命中 iOS 分支。
- `permissionActions` 语义化动作词汇（camelCase，D6）；各 backend 按能力裁剪——第一批
  三 backend 均为全集 `["approve","requestChanges","quit"]`。
- grok 空 plan（exit_plan_mode 无 planContent）时 content 为空串 + iOS 显示官方占位
  （`EMPTY_PLAN_PLACEHOLDER` 语义，调研档 §2.4）。

### 4.2 上行：扩展现有 `resolve_permission`（可选字段，非破坏性）

`go-bridge/types.go:179-183` `ResolvePermissionParams` 新增可选字段：

```jsonc
{ "sessionId": "...", "requestId": "...",
  "behavior": "allow" | "deny",               // 兼容保留：approve→allow，其余→deny
  "planAction": "approve" | "requestChanges" | "quit",   // 可选
  "feedback": "<反馈文字>"                     // 可选，requestChanges 时携带
}
```

- 旧客户端不带 `planAction` 时行为完全不变（`behavior` 兼容路径保留）。
- `core.PermissionResult`（`core/interfaces.go:112-116`）已有 `UpdatedInput`/`Message`
  字段承载反馈；新增可选 `PlanAction` 字段下传 backend。

### 4.3 各 backend 翻译表（逐项镜像官方不变量，锚点=调研档）

| 统一动作 | grok（@72a61251） | claude（SDK 0.3.259） | dsh（@0.1.1-rc.2 安装版） |
| --- | --- | --- | --- |
| approve | `{outcome:"approved"}`（approved 无 feedback，官方类型无此字段——行内评论并存场景第一批不做） | control_response allow（不带 updatedPermissions，D5） | 选中 `intent.approve` 指名的 label（不硬编码 "Approve"，官方 intent 按名指认） |
| requestChanges(反馈) | `{outcome:"cancelled", feedback}`（空反馈 → 无 feedback 字段） | deny + `message=feedback`（空时 Mac 填固定文案，SDK message 必填） | 选 Keep-planning label + `custom=feedback`（底层 respondQuestion 第 5 参） |
| quit | `{outcome:"abandoned"}`（修正 §2.3 语义偏差；cancelled 仅 stale 兜底） | deny + 固定 message（如 "The user dismissed the plan review."——与 quit 区分只在文案，wire 同形合法，调研档 §3.5） | reject 整批（= dismiss / ASK_CANCELLED「用户接管」，D3） |

### 4.4 协议包交付

Mac `docs/protocol/`（canonical）+ iOS `docs/protocol/`（mirror）同步
`permission_request.plan` / `resolve_permission.planAction/feedback` 字段说明与兼容性
注记；`hello.protocol.version` 不动（非破坏性可选字段，遵循现有 minor 演进惯例）。

## 5. 分期计划与验收标准

> 每期完成后 exec-plan 记录证据；D0-D4 分级见本仓 CLAUDE.md，本项整体 D3
> （状态/协议），禁止默认全量测试。

### Phase 1：Mac 协议层扩展（core + go-bridge）

改动：`core/message.go`（Event.Plan + PlanPayload）、`core/interfaces.go`
（PermissionResult.PlanAction）、`go-bridge/events.go`（permission_request 下发 plan）、
`go-bridge/types.go` + `handlers_relay.go`/`handlers.go`（resolve_permission 解析
planAction/feedback 并透传 backend）。

验收：
- `go test ./go-bridge/... -run 'Permission|Plan' -count=1` 与 `go build ./go-bridge`
  通过；
- wire 序列化单测：plan 载荷出现在 permission_request；旧形状（无 plan 字段）字节级
  不变（兼容回归）；
- `docs/protocol/` 更新并 commit。

### Phase 2：三 backend 接线（grok 升级 / claude 专门化 / dsh 识别）

**2a grok**（`agent/grokbuild/`）：`handlePlanBroadcast`（leader_subscriber.go:681-699）
补 `PermissionKind:"plan_review"` + `Plan{content=planContent, title=planApprovalTitle}`；
`permissionActions` 改三动作；应答侧（:934-939）按 §4.3 翻译表拆三路
（approve/request_changes+feedback/quit→abandoned）。
验收：`go test ./agent/grokbuild/... -count=1`；现有 grok_leader_relay 测试扩展
（plan 全文 + abandoned 修正断言）。

**2b claude**（`agent/claudecode/`）：`handleControlRequest`（session.go:827-889）加
ExitPlanMode 分支——`tool_name=="ExitPlanMode"` 时从 `ToolInputRaw.input` 抽
`plan`/`planFilePath`，emit `permissionKind=plan_review` + Plan 载荷；
`summarizeInput`（claudecode.go:2225）加分支（卡面摘要不塞全文）；
`RespondPermission`（session.go:979→respondPermissionContext）按 planAction 翻译
（D5=纯 allow；requestChanges→deny+message；quit→deny+固定文案）。
验收：`session_test.go` 新增 ExitPlanMode fixture 用例（§6）；`go test
./agent/claudecode/... -count=1`。

**2c dsh**（`agent/dsh-web/`）：question 解析结构（approvals.go:326-338）补
`intent`/`detail` 字段；`intent.kind=="plan-review"` 的 question **改emit
EventPermissionRequest**（permissionKind=plan_review + Plan{content=detail}，
`permissionActions` 三动作）替代 user_input 卡（同批非 plan question 不受影响）；
`RespondQuestion` 通道适配：plan 请求的应答走
`respondQuestion(custom=feedback)`（Approve）/ 同函数 Keep-planning+custom /
`RejectQuestion`（dismiss）。`core.UserInputQuestion` 不加 Detail（plan 走权限面，
user_input 面不动）。
验收：`go test ./agent/dsh-web/... -count=1`；fakedsh 测试补 plan-review intent
用例。

### Phase 3：iOS 呈现与应答（`cordcode-ios-plan-approval` 工作树）

改动面：
- `BackendModels` / `SessionProjection` / `SessionProjectionMapping`：解析
  `permissionKind=plan_review` + `plan` 载荷（新字段沿现有 optional 解析模式）；
- `TaskDockView.swift` + `AssistantTimelineRenderSpec.swift`：`permissionKind=="plan_review"`
  分支 → **独立计划卡（D1）**：卡面 = 标题 + 折叠预览（前 N 行）；点击进入全文视图
  （markdown 渲染，10KB 级按 §2.2 无传输截断，渲染层做默认折叠高度 + 展开）；
- 动作栏按 `permissionActions` 渲染三键（沿用 `permissionOptions(from:)`
  wireActions 优先机制，SessionProjectionMapping.swift:466）；requestChanges 弹多行
  反馈输入（D4 可选）；
- `CCCodeBridgeBackendClient`：`resolvePermission` 扩展携带
  `planAction`/`feedback`（behavior 兼容映射同步：approve→allow，其余→deny）；
- iOS `docs/protocol/` mirror 同步。

验收：
- 定向单测：projection 映射（plan 载荷解码/合并）、动作映射（三键 → planAction）；
- `scripts/run.sh test -only-testing:CCCodeTests/<相关类>` 通过；
- 交付前一次 `scripts/run.sh device`（有连接真机时；探测以 devicectl 为准）。

### Phase 4：端到端验证与收尾

- Mac：Release 构建 → 覆盖安装 `/Applications` → killall+open 重启 → 运行态核验
  （进程代际 + 新版本特征输出：plan_review 注册日志行）；禁止临时构建产物。
- iOS：真机安装后，**owner 验收矩阵**（产品语言测试矩阵表，LAN×relay、三 backend 全
  覆盖，一次给全）——真机 UI 操作/截图仍须 owner 授权，agent 只做日志级核验；
- CHANGELOG.md `[Unreleased]` 双仓各追加一节；secret scan（gitleaks）；
- Mac 行为级自验（无需 owner）：claude ExitPlanMode 用真实会话触发（本机 claude 已
  登录的场景）+ dsh/grok 走单测与 fakedsh 级验证，运行态日志佐证。

## 6. 测试与 fixture 计划（fixture 来源纪律：真实样本/官方 fixture，禁手造协议）

| 项 | fixture 来源 | 用途 |
| --- | --- | --- |
| grok exit_plan_mode | 本仓生产日志已证形状（planBytes=1635/1376）+ 上游 `exit_plan_mode/types.rs` @72a61251（调研档 §2.5 锚点）+ 上游 round-trip 测试 | 三 outcome + feedback 翻译断言；abandoned 修正回归 |
| claude ExitPlanMode | 本机 `~/.claude/projects/**/*.jsonl` 真实 transcript（调研档 §3.2 两例 7557/7883 字节，脱敏入仓）+ SDK 0.3.259 类型 | control_request 解析、plan 抽取、deny+message 应答形状；补 §3.4 缺口 |
| dsh plan-review question | 官方 apiproxy question spec（`packages/host/apiproxy/tests/api-proxy-question.spec.ts` @ dsh-v0.1.1-rc.2）+ plan-mode e2e（`apps/web/tests/plan-control-row.e2e.ts`）+ events.schema.ts 校验规则（§2.1 锚点） | intent 识别、detail→plan.content、Keep-planning+custom 应答 |
| wire 兼容 | 既有 permission_request 事件样本（§25 grok 最小档生产形状） | 旧客户端零破坏回归 |
| relay 大载荷 | 新增一条 8KB 级 plan permission_request 的 relay 单测（§2.2 结论的行为化锚定，防未来回归） | 帧预算护栏 |

iOS 侧：projection/映射单测沿用现有测试模式；UI 层不加 snapshot/UI test（未授权）。

## 7. 风险与回滚

| 风险 | 缓解 | 回滚 |
| --- | --- | --- |
| claude setMode 语义未验证（D5 相关） | 第一批不透传（纯 allow）；二选项延后并另行申请真机验证授权 | 无需回滚（未引入） |
| dsh 版本漂移（0.1.2-alpha.5 已删 ApiProxy；安装版 0.1.1-rc.2） | plan 层绑安装版 wire；fixture 用 0.1.1-rc.2 官方 spec；§2.1 警示登记；dsh 升级触发的传输层迁移属 dsh-web 既有适配责任，另案 | plan 分支不命中即回到通用 question 卡（dsh）/通用权限卡（claude）；grok 旧二值行为可由 permissionActions 缺省恢复 |
| iOS 计划卡长文性能（10KB 级 markdown） | 默认折叠高度 + 展开进独立视图；渲染层处理，传输层无截断（§2.2） | `permissionKind=plan_review` 分支不命中 = 旧权限卡路径 |
| SSV2 护栏：plan 全文是否算内容双写 | permission_request 本就是 control-plane 例外（todos/permission 已放行）；plan 载荷是该事件内部字段扩展，不进 messages timeline、不新增第二 writer；iOS 持久化沿 permission 卡现有投影路径 | 无新增数据路径 |
| 旧 iOS 配新 Mac（plan 字段被忽略） | 可选字段 + permissionActions 全集下旧卡退化为 approve/reject 两键（grok 现状语义），行为可接受 | 天然兼容 |
| 双仓协议漂移 | Phase 1 与 Phase 3 各自同步 protocol pack 并单独 commit（iOS mirror 即时 commit 规则） | git revert 各自分支 |

回滚总原则：全部分支改动落在 `plan/approval-layer` / `plan/approval-layer-ios`，不触
main；退役目录（agent/codex、agent/codex-web、agent/dsh、agent/opencode）零改动。

## 8. 第二批结论位（本分支只记录，不实施）

- codex（载体 codex-remote）：① Remote Control 链路对 plan 事件（PlanItem/PlanDelta）
  的实际透传可达性未核实（调研档 §4.6）；② codex-remote 连既有三类审批都是
  `ErrNotSupported`（`agent/codex-remote/session.go:121-123`），审批面补齐是独立前置
  工作量；③ approve 产品化（合成 "Implement the plan."）待 owner 裁决（§8.3-5）。
- opencode：stable serve 与 dev checkout（@69c172e）wire 漂移核对是前置（§8.3-10）；
  plan_exit question 桥已能答 Yes/No；requestChanges 无原生反馈通道是产品语义缺口。
- dsh plan 投影（active/pending）消费（§2.1 结论位）：第二批或 plan 状态指示需求出现
  时再接。

## 9. 红线合规自查

- 退役目录零改动：新实现仅落 `agent/grokbuild`、`agent/claudecode`、`agent/dsh-web`、
  `core/`、`go-bridge/` 与 iOS 对应面。
- 上游锚点：grok@72a61251、deepseek-harness@49a606bc（转发面绑安装版 tag
  dsh-v0.1.1-rc.2，理由 §2.1）、claude SDK 0.3.259 实抓；本方案每项行为映射均带
  上游符号/call site（§4.3 表 + §2 锚点）。
- 构建/安装前将按 P0 门重新生成来源清单（实际构建工作目录）；Mac 验证只走
  Release→/Applications→重启→运行态核验。
- UI 自动化 / 真机 UI 操作 / setMode 真机验证：未授权，Phase 4 只做日志级核验 +
  owner 验收矩阵。
