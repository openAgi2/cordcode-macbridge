# 评审报告：跨 Backend Plan Mode 适配调研（docs/2026-09-03-plan-mode-cross-backend-survey.md）

- 评审日期：2026-09-04
- 评审对象：`docs/2026-09-03-plan-mode-cross-backend-survey.md`（488 行，只读调研档，为「计划审批层」立项提供事实输入）
- 评审性质：独立可复查性核验——来源清单复核 + 承重锚点独立抽查（本仓亲验 + 两个 Explore 子代理逐条核验上游）+ 时效性审查。**不修改被评审文档**，仅产出本报告。
- 评审结论：**有条件通过（结论层可信、锚点层需修订后再作为引用基线）**。全部决定性发现（grok 两处 §25 修正、`planApproval` 不存在、dsh custom 挂点、claude 桥可代答、codex 无 wire 审批、opencode question 通道）均经独立核验成立；但存在 4 条 A 级（按文档路径/行号无法直接复查，或定性过时）与 6 条 B 级（行号偏移）偏差，其中 A4（codex CollaborationModes 定性过时）会削弱 §8 分批建议中的一个论据，C1（codex-web 退役时效性）会影响 §4/§8.1 的载体基线表述。建议：采纳结论，按本报告「A 级修订项」修正后作为完整档设计输入。

---

## 1. 评审来源清单（评审时点实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge  分支=main  提交=bbbdbb78df3bb8240a2279e92c2e17a7ff7ab161
  未提交状态=8 个已修改（BUILD_INSTALL_AND_RUNTIME.md、CHANGELOG.md、CLAUDE.md、GO_BRIDGE_ARCHITECTURE.md、
  MacBridge/MacBridge/Services/RuntimeManager.swift、MacBridge/MacBridgeTests/MacBridgeBehaviorTests.swift、
  think.md）+ 3 个未跟踪（本文、调研档、docs/2026-09-04 codex-web 退役两檔）
  注：调研档来源清单记录的「干净(0)」是 2026-09-03 调研开始时的实测，与退役修改不在同一时点，两者不矛盾——
  退役完成文档（docs/2026-09-04-...-完成情况.md）的来源清单明确记录「调研档本次未触碰」；
  且本次核验了退役修改的文件范围：**不含 agent/、go-bridge/ 任何文件**，即退役改动对调研档的全部源码锚点无污染。
仓库路径=/Users/jacklee/Projects/codex  分支=main  提交=50fffd5ed367aa99491d9ec58575626fce4e9dd4  未提交状态=干净（子代理实测）
仓库路径=/Users/jacklee/Projects/opencode  分支=dev  提交=69c172e8a7c0086887b1f93ed5a162f14b6aa0c5  未提交状态=干净（子代理实测）
仓库路径=/Users/jacklee/Projects/deepseek-harness  分支=master  提交=49a606bc5b5934603f22a26957a07dc799ab0291  未提交状态=干净（子代理实测）
仓库路径=/Users/jacklee/Projects/grok-build  分支=main  提交=72a61251fcffb464bcc687aeb5a998e5a98ec0c9  未提交状态=干净（亲验）
存档样本=/tmp/claude-sdk.d.ts（unpkg @anthropic-ai/claude-agent-sdk@latest，426,240 字节，评审时抓取）
退役裁决依据=docs/2026-09-04-codex-web-backend-retirement-完成情况.md（owner 2026-09-04 指令；源码保留、产品面移除、回滚=加回 drivers）
```

## 2. 核验方法与覆盖度

| 范围 | 方式 | 覆盖 |
| --- | --- | --- |
| 本仓全部锚点（agent/grokbuild、agent/claudecode、agent/dsh-web、agent/opencode-web、agent/codex-remote、core、go-bridge、think.md） | 评审亲验（sed/grep 逐区间） | 完整 |
| grok-build 上游承重锚点（types.rs 修正、q→abandoned、approve+评论 Interject、request changes 路径、5 键栏、casual 评论） | 评审亲验 | 完整 |
| codex 上游全部锚点（26 条 + 2 项事实核查） | Explore 子代理逐条核验 | 完整 |
| opencode 上游全部锚点（17 条 + 历史核查） | Explore 子代理逐条核验 | 完整 |
| deepseek-harness 上游全部锚点（14 条 + 事实核查） | Explore 子代理逐条核验 | 完整 |
| claude 官方文档引用（code.claude.com 4 页 + unpkg sdk.d.ts） | SDK 类型亲验（unpkg @latest）；文档页面未重抓（以 SDK 类型为准） | 决定性字段已验 |
| 退役目录（agent/codex、agent/codex-web、agent/dsh、agent/opencode） | **按 owner 指令不审计**（2026-08-25 / 2026-09-04 / 2026-08-17 / 2026-08-19 退役） | 不适用；时效性影响见 C1 |

行号判定标准：内容与声称一致且行号偏差 ≤ ±3 记为「一致」；仅行号偏移、内容成立记为 B 级；按文档路径/行号直接定位失败或定性错误记为 A 级。

## 3. 决定性结论核验结果（全部成立 ✅）

1. **grok 修正点 1**：`exit_plan_mode/types.rs` 权威路径确为 `crates/codegen/xai-grok-tools/src/implementations/grok_build/exit_plan_mode/`（mod.rs + types.rs 实存）；`xai-grok-workspace/src/exit_plan_mode/` 不存在（亲验）。types.rs:20-21 注释 `"approved"`, `"cancelled"`, `"abandoned"` 三值，:80-112 含 abandoned round-trip 测试——「修正成立」。
2. **grok 修正点 2**：`q` 键 → `abandon_plan()` → `send_abandoned()`（viewer.rs:159-161，plan.rs:274-281）；`send_cancelled(feedback)` 仅出现在 request changes 与 stale 兜底（plan_approval_view.rs:175-190）——「§25.2 q→cancelled 记录错误、真实为 abandoned」成立。
3. **grok approve+评论并存**：plan.rs:185-207 的 `"The user approved the plan with the following review comments:"` + `send_approved()` + `Action::Interject` 逐字确认——「§25 未记录的新发现」成立。
4. **claude `planApproval` 字段不存在**：评审直接抓取 unpkg `@anthropic-ai/claude-agent-sdk@latest` sdk.d.ts（426KB）全文件 grep `planApproval` 0 命中；codex 仓 grep 亦 0 命中（子代理验证 B 项）——「勿按其实现」结论成立并可复查。
5. **claude 桥现状**：`handleControlRequest`（session.go:827-889）无 ExitPlanMode 特殊处理（agent/claudecode 全目录 grep 0 命中，亲验）；`summarizeInput` 定义于 claudecode.go:2225；`RespondPermission`/`respondPermissionContext`（:979 起）deny 带 message——「桥今天就能代答 allow/deny+message、数据已在 wire」成立。
6. **dsh custom 挂点**：`respondQuestion(…, custom string)` 第 5 参（approvals.go:519），:545-548 组装 `entry["custom"]` 进 answers body——「公开包装未透出、底层原生可行」成立。
7. **codex 无 plan 审批 wire 变体**：子代理事实核查 A——`exit_plan` 0 命中、无 PlanTool（仅 `PlanToolOutput` = update_plan 的输出结构）、无任何 plan 反请求——「计划=文本协议、审批=客户端编排」成立（plan_implementation.rs:9-19 三选项逐字一致）。
8. **opencode question 通道**：plan_exit 无参、custom:false Yes/No、No→RejectedError、Yes 合成消息（plan.ts 各锚点逐字命中）；本仓 question 管道与官方形状 fixture 实存（interactions_mutations_c6_c7_test.go:99-167）。
9. **dsh 反转性发现**：`exit_plan_mode` 工具（# 标题校验 `^#\s+\S`）、Plan review question（header/detail=args.plan/intent plan-review）、Keep planning+custom 带反馈抛错——全部逐字命中。核心反转结论（「dsh 有一等 plan mode 且原生带反馈打回」）成立。
10. **体积样本**：claude 本地 transcript 的 plan 7.5-7.9KB 规模（文档自述样本）；grok 生产日志 1.4-1.6KB 属文档取证，未重复验证（日志文件可能已滚动），但为产品运行日志来源、可信等级足够。

## 4. A 级修订项（引用前必须修正）

**A1. grok 上游路径漏 `app/` 前缀（系统性）。**
§2.2（3 条）、§2.3、§2.5 来源清单中所有 `agent_view/plan.rs`、`agent_view/viewer.rs` 锚点，真实路径为 `crates/codegen/xai-grok-pager/src/app/agent_view/{plan,viewer}.rs`（find 实测目录 `src/app/agent_view/`，另有同层 `app/agent_view/viewer_tests.rs`）。按文档字面路径 `sed`/跳转将直接失败。`views/file_search/line_viewer.rs` 路径无误。

**A2. think.md 行号错误。**
§4.2「我仓 think.md:460-473 已有结论」与 §4.6 来源清单「think.md:460-473」——实际该结论在 **think.md:482-491**（:482 逐字为「官方 `turn/plan/updated` 是 todo **唯一**结构化真相」；:485-491 为 planCache/TodoProvider）。think.md:460-473 是无关的 2026-08-23 ccswitch config.toml 条目。行号错误约 +22 行（后续行随内容增加偏移）。转述内容本身准确。

**A3. codex app-server-protocol 路径偏差。**
§4.6 锚点中 `app-server-protocol/v2/turn.rs`、`v2/thread.rs`、`v2/item.rs` 真实路径为 `codex-rs/app-server-protocol/src/protocol/v2/*`（多一层 `protocol/`）；`v2/common.rs`、`v2/event_mapping.rs` 两个文件在 `.../src/protocol/` 根目录（**无独立 v2 目录**）。所有引用行号（含 :270-277 PlanItem、:1433-1438 PlanDeltaNotification、:1686-1718 三类 requestApproval）内容均核验一致，仅路径需修正。

**A4. codex `Feature::CollaborationModes` 定性过时（实质性）。**
§4.1、§4.5、§7.4、§8.1 反复将「experimental feature gate」作为 codex 动作产品化的阻塞论证（「挂在 experimental feature gate 上」「feature gate 风险」）。子代理核验：features/src/lib.rs:382 变体仍在，但同文件 :1556-1560 的 FeatureSpec 为 **`Stage::Removed, default_enabled: true`**，:380-381 注释原文 "Kept for config backward compatibility; behavior is always collaboration-modes-enabled."——该 feature 在主线已**从 stage gate 移除且恒启用**，不再是 experimental 开关。真正的实验性标注在 app-server 协议层（v2/turn.rs:244,249、v2/thread.rs:265,269、v2/item.rs:272 的 EXPERIMENTAL 注释）。建议修正为：「CollaborationModes 主线恒启用（feature gate 已 Removed，仅保留配置兼容位）；app-server wire 类型仍标 EXPERIMENTAL（协议稳定性声明）；目标部署（ChatGPT Desktop / 远端 controller）是否提供协作模式能力须按部署验证」。注意这不推翻「approve=客户端编排非 wire 审批语义、产品化需 owner 裁决」的结论，但**该结论目前只剩「非官方语义」一条硬理由**，建议在 §8.3-5 决策清单里同步改写表述（去掉 feature gate 论据）。

## 5. B 级修订项（行号/表述偏移，不影响结论，建议顺手修正）

| # | 位置 | 声称 | 实际 | 性质 |
| --- | --- | --- | --- | --- |
| B1 | §2.2-2、§2.4 | line_viewer.rs:1673/1685/1689/1699/1708 | y copy :1675、a approve :1679-1681、casual s send :1687-1689、s request changes :1696-1699、q quit :1706-1707（同上区间，内容一致） | 偏移 2-4 行 |
| B2 | §2.2-4、§2.4 | plan.rs:187-204 / :407-414 | approve+评论 Interject :185-207；a 键直达 :410-414 | ±3 内 |
| B3 | §5.1 | agent.ts:140-181（亲验 :150-181） | build :141-155、plan :156-181——亲验区间含 build 尾部 6 行 | 轻微 |
| B4 | §5.2/§5.6 | session.ts:259-301（completed 态） | 259-301 为 Pending/Completed/Error 四状态整体定义；completed 实际 :277-290 | 区间过宽 |
| B5 | §5.2/§5.6 | question.ts:27-35,66-69 | Request :35-40；asked/replied/rejected 事件 :58-60 | 偏移 8 行 |
| B6 | §6.3 | PlanModeControl.tsx:20-24（PlanChip → /plan off） | 组件 :19 起；执行体 :36-50（+ client/index.ts:60-61 commands.execute） | 偏移 ~16 行 |
| B7 | §5.2 | 事件全集清单 | inventory 准确但漏列 `message.removed`（session.ts:605）；「无 plan 专属事件」结论不受影响 | 完整性小瑕疵 |
| B8 | §4.6 | v2/tests.rs:5107（approval 相关测试） | 实为 requestUserInput 测试；同文件 approval 测试在 :682/:715 等 | 引用错位 |
| B9 | §6.1 | agent.cordis.yml:110-124「禁用 todo_write」 | 该段为提示词约束（:118 "Do not use todo_write to track this planning phase"）；tool-todo 仍注册于 :240-241（未 disabled）；grep 确认 todo_write 仅 3 处同类文案命中 | **表述过强**：非工具禁用 |
| B10 | §6.1 | 「standard preset + base bundle 默认装配」 | base/cordis.patch.yml:306-307 装配 plan-mode 插件成立，但 base patch 不引用 standard preset——二者是各自携带配置的两个载体 | 关系表述不精确 |

## 6. C 级：时效性与上下文提醒

**C1. codex-web 于 2026-09-04 退役（与本文档同日落案，评审时点已生效）。**
owner 2026-09-04 指令：codex-web 从 iOS 端与 MacBridge 端产品面移除（不再出现、不再挂载），`agent/codex-web` 源码保留、回滚=加回 drivers；Codex 产品面由 `codex-remote` 独立承接（退役文档来源清单与 /internal/agents 复核已记录）。影响：§4.4「旁观：codex-web 已在用」、§4.6「本仓锚点=agent/codex-web/interactions.go」、§8.1「codex——先做 plan 展示（PlanItem 已消费）」的**产品载体已不存在**。调研档成文于退役决定之前（09-04 凌晨收尾），未作时效性标注。建议：完整档立项时 Codex 载体基线改为 **codex-remote**（同协议族，PlanItem/PlanDelta 同构可达；文档已记录其 RespondPermission=ErrNotSupported，即「展示可先行、审批面需先补 approval 基建」，§8.3-7 已在列）；或明确「恢复 codex-web」为前置决策。**退役目录 agent/codex-web 已按 owner 指令不在本次审计范围，本报告未核验其内部锚点。**

**C2. claude 官方文档页面未重抓。**
4 个 code.claude.com 文档引用（permission-modes / agent-sdk typescript/python/permissions / tools-reference）依赖文档转述；评审以 unpkg sdk.d.ts @latest 独立验证了其中的决定性字段（planApproval 不存在、conversation_reset 原文、setMode 存在、ExitPlanMode 存在），但文档 URLs 的可访问性/页面措辞未复核。文档自身在 §3.6 已标注「未取得交互式截图」「bare -p 未实测」等局限，与本次评审一致。

**C3. 全局来源清单的时点语义正确（无造假嫌疑）。**
文档声明「未提交状态=干净(0)」并注明「2026-09-03 调研开始时实测」；评审时点工作树不干净系 09-04 退役修改所致。退役完成文档自己的 P0 清单记录了「调研档未触碰」，两份文档可以互相印证，不构成来源污染。

## 7. 总体判定

1. **调研结论层：可信**。所有承重结论（§2 基准与两处修正、§3 claude 桥可答性、§4 codex 无 wire 审批、§5 opencode question 通道+版本漂移警示、§6 dsh 反转性发现+custom 挂点、§7 统一抽象输入、§8 分批建议）均有真实源码/样本支撑，未发现任何「结论与源码事实相反」的错误。五个 backend 全有 plan mode、无「官方无此功能」项的总结正确。
2. **锚点层：存在 4 A + 6 B 偏差**，其中 A4（experimental gate 定性过时）与 A6/B9（todo_write 提示词约束非禁用）两处会让读者高估「官方收紧度」；A1/A2/A3 三类路径行号问题会让按文档复查者「找不到引用」——引用本档时建议按本报告修正后再传播。
3. **评审本身的局限**：opencode/dsh/codex 上游锚点由 Explore 子代理核验（子代理报告已逐条附证据，未发现与亲验结果冲突之处）；claude 文档页与 grok 产品日志体积样本未重复取证；iOS 仓锚点（wireActions 机制）未核验（文档 §7.1 为设计输入引用，非事实锚点）。
4. **对「完整档立项」的建议**：采纳文档为设计输入，引用锚点前执行 A1-A4 修订（均为机械性修正）；§8.3「待 owner 决策清单」的措辞同步受 A4/C1 影响（codex 部分去掉 feature gate 论据、载体改 codex-remote）。

---

## 附：评审执行记录

- 方法：评审亲验本仓全部锚点与 grok 上游承重锚点（sed/grep/find/git 逐项）；2 个 Explore 子代理逐条核验 codex（26+2）、opencode（17+2）、deepseek-harness（14+2）；SDK 类型直接抓取 unpkg；退役影响面以退役完成文档 + git status 文件范围交叉确认。
- 全程只读：评审未修改调研档、未修改任何源码；唯一写操作 = 本文档（docs/2026-09-04-plan-mode-cross-backend-survey-review.md）。
- 时间：2026-09-04。

---

# 第二轮评审（v1.1 修订后复查，2026-09-04）

- 评审对象：`docs/2026-09-03-plan-mode-cross-backend-survey.md` **v1.1**（488 → 537 行，新增「修订记录」节：15 项采纳逐项处置表 + 修订时点 P0 来源清单）
- 评审结论：**通过（v1.1 修订质量良好，建议仅带 3 项小修正）**。15 项采纳全部真实落地、逐项复验无新错误；两处「不采纳/部分采纳」争议经独立复验**均为文档方正确、第一轮评审有误**——其中 B1 系第一轮评审误判（本报告撤回该项），B2 部分采纳处置正确。新发现问题 3 项（A5 dsh 行号、A6 codex 路径前缀、A7 重复行号），其中 A5 是**第一轮评审报告输出遗漏项**（拟稿存在、交付报告未含），一并补上。
- 第二轮评审时点：macbridge HEAD=`21e0410`（提交 v1.1 + 评审报告）；上游四仓 HEAD 与第一轮相同；评审独立复验了争议点（grok 上游逐行 grep）、A4/B10 落地行号、dsh 三处行号。

## 1. 争议点裁决（第一轮 vs 修订方，独立复验，本轮最终裁定）

**B1——修订方正确，第一轮评审 B1 项撤回。**
grep -n 精确复验 `line_viewer.rs`（grok-build @72a61251）五处按钮 `build_shortcut_button` 调用行：

| 按钮 | v1.0/v1.1 原文 | 第一轮评审声称 | 实测（grep） |
| --- | --- | --- | --- |
| y copy plan | :1673 | :1675 | **:1673** |
| a approve | :1685 | :1679-1681 | **:1685** |
| s send（casual） | :1689 | :1687-1689 | **:1689** |
| s request changes | :1699 | :1696-1699 | **:1699** |
| q quit plan | :1708 | :1706-1707 | **:1708** |

第一轮评审的偏差行号系基于**无行号 sed 输出目测计数**（sed -n + 数行）所得，错 2-4 行；修订方用逐行 grep 复验得正确值。**「B1 维持原文、不采纳」的处置正确**。第一轮报告 B1 项即行撤回，以本表为准。修订记录对第一轮谬误的转述（「评审给出的行号指向邻近注释/标签行」）准确——:1706 确是 quit 注释行、:1707 是 `quit_spans` 起始。

**B2——部分采纳处置正确。**
带行号复验 plan.rs：approve+评论块 `:187-205` 精确（187=`let review_comments = {`，205=Interject 块闭合）——修订方把第一轮建议的「185-207 整体区间」精化为「187-205 承重段」是更优做法；`a` 键 `:407-414` 正确（407=`if !is_commenting` 条件起点、412=`{`、413=`approve_plan()`、414=闭合）——第一轮建议的 :410 起点确在条件式中段，修订维持原文正确。

## 2. 15 项采纳落地核验（逐项）

| 项 | 落地核验 | 判定 |
| --- | --- | --- |
| A1 grok `app/` 前缀 | §2.2（3 条）、§2.3（2 条）、§2.4、§2.5 全部改为 `crates/codegen/xai-grok-pager/src/app/agent_view/{plan,viewer}.rs`，§2.5 清单为 crate 全路径 | ✅ |
| A2 think.md 行号 | §4.2/§4.6 均为 `:482-491`；已逐字复验 :482 命中「官方 `turn/plan/updated` 是 todo 唯一结构化真相」 | ✅ |
| A3 codex 路径 | §4.1/§4.2/§4.6 改 `app-server-protocol/src/protocol/v2/{turn,thread,item,tests}.rs`；`common.rs`/`event_mapping.rs` 归位 `protocol/` 根（无误导）；**但 `codex-rs/` 前缀仍缺见 A6** | ⚠️→A6 |
| A4 CollaborationModes | §4.1 改写（`Stage::Removed, default_enabled: true` 恒启用、实验性仅在协议类型层）；§4.5/§7.4/§8.3-5 全部移除「experimental gate」论据；复验 features/src/lib.rs:380-382/:1556-1560 逐字命中 | ✅ |
| B3 agent.ts | §5.1/§5.6 = `:141-155` build / `:156-181` plan，「亲验 :150-181」表述已撤销 | ✅ |
| B4 session.ts | §5.2/§5.6：四态 :259-301、completed :277-290、ToolPart :315-322 | ✅ |
| B5 question.ts | §5.3/§5.6：Request :35-40、事件 :58-60 | ✅ |
| B6 PlanModeControl | §6.3/§6.6：组件 :19、off() 执行体 :36-50、client/index.ts:60-61 | ✅ |
| B7 message.removed | §5.2 事件全集补列（:605 标注） | ✅ |
| B8 tests.rs:5107 | §4.6 标注 :5107=requestUserInput 非 approval，补 :682/:715 | ✅ |
| B9 todo_write | §6.1 改「提示词级约束」+ tool-todo :240-241 注册；复验 :118 文案与注册均在 | ✅ |
| B10 base/preset | §6.1 改「独立载体」+ :307-309；复验 base patch id/name 在 :307-308、grep 无 standard 引用 | ✅ |
| C1 codex-web 退役 | §1 表格行、§4 节首时效框、§4.2/§4.4、§7.1/§7.2、§8.1-4、§8.3-5/7 全部改 codex-remote 载体基线 +「恢复 codex-web」前置选项；无残留「codex-web 在产品面」表述 | ✅ |

## 3. 新发现问题（第二轮，建议 v1.2 修正）

**A5（真实错误，建议必须修正）：§6.3 三处 dsh 行号仍为错误值。**
第一轮评审报告交付时遗漏了该组偏差（拟稿有 A5 项、最终报告未含——本轮自我纠正承认）。带行号复验 deepseek-harness @49a606bc `packages/plan/plan-mode/src/index.ts`：

| 文档 v1.1 引用 | 实测 |
| --- | --- |
| Keep planning + custom 抛错 `（:331-336）` | 条件 :336、feedback :337、抛错 :340-341（块 :336-341） |
| Approve → `{approved: true}` `（:338-341）` | `pendingIntents.set` :345、`return { approved: true }` :346（:345-346） |
| dismiss → ASK_CANCELLED `（:312-316）` | 检查 :323、抛错 :324-325（块 :317-328） |

内容描述全部准确（偏 5-8 行），与第一轮 B 级性质相同——建议改为 :336-341 / :345-346 / :317-328。

**A6（路径定位）：§4 全节路径前缀不统一，均缺 `codex-rs/`。**
codex 仓库所有 crate 在 `codex-rs/` 子目录下，而 §4.1 正文（`protocol/v2/turn.rs`、`protocol/v2/item.rs`、`protocol/event_mapping.rs` 等）与 §4.6 清单（`protocol/src/config_types.rs`、`tui/…`、`core/…`、`features/…`、`app-server-protocol/…` 全部）缺前缀——按「上游仓库路径=/Users/jacklee/Projects/codex」repo 根解释时路径不存在。§4.1 中 `codex-rs/protocol/src/config_types.rs` 与 `codex-rs/collaboration-mode-templates/templates/plan.md` 两处带前缀，同节不一致。第一轮评审遗漏（子代理核验使用了绝对路径，当时仅注意 `protocol/` 层）。建议：统一加 `codex-rs/` 前缀，或在 §4.6 清单顶部注明「锚点路径均相对 codex-rs/ 子目录」。

**A7（文字瑕疵）：§4.6 `features/src/lib.rs:380-382,382,1556-1560` 有重复项 `382`**——应为 `:380-382,1556-1560`。

## 4. 修订记录节质量

- 处置表与第一轮报告 A/B 清单逐项对应（15 采纳 + B1 不采纳 + B2 部分采纳 + C2/C3 无需修订），无遗漏项、无夸大；评级引用（如 B9 正确引用第一轮「表述过强」定性）准确。
- 修订时点来源清单：**HEAD=`e1daf97` 属实**——git log 验证 `c6df219`（codex-web 退役）、`e1daf97`（deprecated.md 标记）在前、`21e0410`（提交 v1.1+评审报告）在后，时间线自洽；「未提交状态=2 个未跟踪」与提交前状态一致。
- 「iOS 仓 main 已推进至 61f67bf、未核对其内容、本文档不依赖 iOS 时点敏感锚点」——诚实且必要的声明（iOS 仓从调研时点 aeba911 已前移）。
- 唯一可改进：处置表引述「评审给出的实际行号」时（B1 行）以第一轮错误值为对象，客观上把错误值记录进了修订记录；随本报告 B1 撤回后，建议在 B1 行末加「（该评审行号经二轮复验为错误，原文维持正确）」字样，防止未来读者从修订记录反向引用错误行号。

## 5. 总体判定（第二轮）

1. **v1.1 修订质量：优秀。** 15 项采纳全部真实落地且未引入新错误；对 B1/B2 两个争议点，修订方坚持了正确的立场并给出了正确的复验证据（第一轮评审在此两点均有误，其中 B1 为评审自身错误——本报告已撤回）。
2. **新增问题 3 项**：A5（dsh 行号，第一轮遗漏）建议 v1.2 修正；A6（codex-rs/ 前缀，第一轮遗漏）、A7（重复 382）建议顺手修正——均不影响任何结论，且 A5 的描述性内容准确。
3. 第一轮评审的交付缺陷（B1 误判、dsh 行号遗漏、codex-rs/ 前缀未点名）在本次复查中已全部纠正；两轮合并后，文档剩余偏差仅 A5-A7 三项，均为行号/路径/文字层面。

## 附：第二轮评审执行记录

- 复验方式：grep -n 精确定位（grok line_viewer.rs 五按钮、plan.rs 区间带行号、codex features 行、dsh index.ts 行、base cordis patch 行）；git log 验证修订时点提交链。
- 全程只读：未修改调研档；唯一写操作 = 在本评审报告追加本轮章节。
- 时间：2026-09-04。
