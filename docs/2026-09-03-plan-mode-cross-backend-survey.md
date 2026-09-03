# 跨 Backend Plan Mode 适配调研（完整档「计划审批层」设计输入）

- 日期：2026-09-03（调研执行日；成文收尾于 2026-09-04 凌晨，文件名沿用调研执行日，与文中全部证据时间戳一致）
- 性质：**只读调研文档**，为「专门的计划审批层」立项提供事实输入；不构成实施方案，不替 owner 决定 UI 形态或立项范围。
- 上游裁决：owner 2026-09-03 验收 §25 最小档时原话——「iOS 端是不是太简单了呢，看不懂计划的详情，而且按钮就两个……如果需要做到 mac 端那种可以看到计划文档，且下面一堆按钮，估计要专门的计划方案，还要适用于其他的 backend」。完整档（计划全文 + 接近官方客户端的动作集 + 跨 backend 抽象）须以本文档为设计输入另案立项。
- 调研方法：上游源码 source-first（每个结论带 路径:行号 锚点 + commit）；Claude Code 以本机真实 transcript 样本 + 官方文档/SDK 类型为准；产品运行日志佐证；所有「未取得样本」「未核实」项如实标注。

## 修订记录

- **v1.0（2026-09-03 调研 / 09-04 凌晨成文）**：初版。
- **v1.1（2026-09-04）**：按独立评审报告修订（逐项处置见下表）。
- **v1.2（2026-09-04）**：owner 质询后撤回「恢复 codex-web」并列前置选项（v1.1 采自评审 C1 建议原文）：恢复不解决 codex 审批层缺口（无 wire 审批在两个载体上同样成立），只省 plan 展示接线量，不足以推翻当天退役裁决；§4/§8 改为 codex-remote 单一载体基线，回滚机制仅留备注，并补列「codex-remote 经 Remote Control 链路的 plan 事件可达性」为待核实项。

| 评审项 | 处置 | 说明 |
| --- | --- | --- |
| A1 grok `agent_view/*` 漏 `app/` 前缀 | **采纳** | 全文路径修正为 `xai-grok-pager/src/app/agent_view/{plan,viewer}.rs`；§2.5 清单补全 crate 全路径 |
| A2 think.md 行号 | **采纳** | `:460-473` → `:482-491`（修订时逐字复验：「官方 `turn/plan/updated` 是 todo 唯一结构化真相」确在 :482） |
| A3 codex 协议路径 | **采纳** | 修正为 `app-server-protocol/src/protocol/v2/{turn,thread,item,tests}.rs`；`common.rs`/`event_mapping.rs` 在 `protocol/` 根（无 v2 子目录）；全部行号复验一致 |
| A4 CollaborationModes 定性过时 | **采纳** | 亲验 `features/src/lib.rs:380-382,1556-1560`：`Stage::Removed, default_enabled: true`，注释明言恒启用——主线已非 experimental gate；实验性仅在协议类型层（EXPERIMENTAL 注释/`#[experimental]` 属性）。§4/§7/§8 相应改写，「动作产品化需裁决」保留唯一硬理由：非官方 wire 审批语义 |
| B1 line_viewer.rs 行号 | **不采纳** | 评审称实际为 :1675/:1679-1681/:1687-1689/:1696-1699/:1706-1707；修订时 @72a61251 逐行复验，`build_shortcut_button('y'/'a'/'s'/'s'/'q')` 调用行实测 = **:1673/:1685/:1689/:1699/:1708**，与 v1.0 原文一致——评审给出的行号指向邻近注释/标签行（如 :1679-1681 是 a 键 label 选择三元组、:1706-1707 是 quit 注释行与 `quit_spans` 起始），非按钮构建调用行。原文行号准确，维持 |
| B2 plan.rs 区间 | **部分采纳** | approve+评论块精化为 `:187-205`（187 起 `review_comments` 构造、205 Interject 块闭合）；`a` 键直达维持 `:407-414`——复验条件从 :407 `if !is_commenting` 起，评审建议的 :410-414 起点在条件式中段 |
| B3 agent.ts 区间 | **采纳** | 修正为 build :141-155 / plan :156-181；原「亲验 :150-181」含 build 尾部，撤销该区间表述 |
| B4 session.ts 区间 | **采纳** | ToolPart :315-322；state 四态联合 :259-301；completed 形状精化 :277-290 |
| B5 question.ts 行号 | **采纳** | Request :35-40；asked/replied/rejected 事件 :58-60 |
| B6 PlanModeControl 行号 | **采纳** | 组件 :19、`off()` 执行体 :36-50、`client/index.ts:60-61`（`commands.execute(sessionId, '/plan off', [])`）——逐行复验一致 |
| B7 事件清单漏 message.removed | **采纳** | 补列 `message.removed`（session.ts:605） |
| B8 tests.rs:5107 引用错位 | **采纳** | :5107 实为 requestUserInput（ToolRequestUserInputParams）测试；approval 反序列化测试在同文件 :682/:715 |
| B9 todo_write「禁用」表述过强 | **采纳** | 改为提示词级约束（:118 "Do not use todo_write to track this planning phase"）；tool-todo 仍注册于 :240-241 |
| B10 base bundle/preset 关系 | **采纳** | 改为「两个各自携带配置的独立载体」；base patch（:307-309 装配 plan-mode 插件）不引用 standard preset（grep 复验无引用） |
| C1 codex-web 退役时效 | **采纳** | codex-web 2026-09-04 退役（源码保留、回滚=加回 drivers）；§1/§4/§7/§8 载体表述改为 codex-remote 基线（其 approval 面仍 `ErrNotSupported`），「恢复 codex-web」列为前置决策选项 |
| C2/C3 | 无需修订 | C2：claude 文档页未重抓为本文档 §3.6 已声明的局限，评审以 SDK 类型完成决定性验证；C3：来源清单时点语义经评审确认无污染 |

修订时点来源清单（P0 门，2026-09-04 修订开始时实测）：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge  分支=main  提交=e1daf972e368597ebf325e6546f7d939fdba383d
  未提交状态=2 个未跟踪（本文档、评审报告）；相对 v1.0 的 bbbdbb7 新增 codex-web 退役两提交（c6df219,e1daf97），
  退役 diff 不含 agent/ 与 go-bridge/ 任何文件（评审已核验，调研锚点无污染）
仓库路径=/Users/jacklee/Projects/codex         分支=main   提交=50fffd5...（与 v1.0 相同）  未提交状态=干净
仓库路径=/Users/jacklee/Projects/opencode      分支=dev    提交=69c172e...（与 v1.0 相同）  未提交状态=干净
仓库路径=/Users/jacklee/Projects/deepseek-harness 分支=master 提交=49a606b...（与 v1.0 相同） 未提交状态=干净
仓库路径=/Users/jacklee/Projects/grok-build    分支=main   提交=72a6125...（与 v1.0 相同）  未提交状态=干净
iOS 仓=本次修订零读零写（main 已推进至 61f67bf，与调研时点 aeba911 不同，未核对其内容，本文档不依赖 iOS 时点敏感锚点）
修订操作=仅编辑本文档；全程未触碰任何上游 checkout 与 iOS 仓
```

## 全局来源清单（P0 门，2026-09-03 调研开始时实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge  分支=main  提交=bbbdbb78df3bb8240a2279e92c2e17a7ff7ab161  未提交状态=干净(0)
仓库路径=/Users/jacklee/Projects/cordcode-ios  分支=main  提交=aeba9111164a021f2df38cf3f831335a57baa1d1  未提交状态=干净(0)
  ⚠ 任务简报称 iOS main 工作树有未提交改动，本次实测 status --porcelain 为空（0 行）——以实测为准；另有工作树
    cordcode-ios-codex-remote@ace26731、cordcode-ios-remote-web-push@ba34e908，本次未触碰。全程对 iOS 仓零写操作。
仓库路径=/Users/jacklee/Projects/codex        分支=main   提交=50fffd5ed367aa99491d9ec58575626fce4e9dd4  未提交状态=干净(0)
仓库路径=/Users/jacklee/Projects/opencode     分支=dev    提交=69c172e8a7c0086887b1f93ed5a162f14b6aa0c5  未提交状态=干净(0)
仓库路径=/Users/jacklee/Projects/deepseek-harness 分支=master 提交=49a606bc5b5934603f22a26957a07dc799ab0291  未提交状态=干净(0)
仓库路径=/Users/jacklee/Projects/grok-build   分支=main   提交=72a61251fcffb464bcc687aeb5a998e5a98ec0c9  未提交状态=干净(0)
  （恰为 §25 基准 commit，与已核实基准事实同源）
本机 claude CLI=/opt/homebrew/bin/claude 2.1.234
```

调研局限（如实声明）：嵌套 `claude -p` 实测因本 shell 无凭据（宿主持有 `ANTHROPIC_AUTH_TOKEN`，Keychain 无 `Claude Code-credentials` 条目）返回 "Not logged in"，无法现场触发 ExitPlanMode 权限请求；Claude 真实样本改取自本机 `~/.claude/projects/**/*.jsonl` 会话 transcript（claude CLI 自写、与桥消费的 stream-json 同源格式）+ 官方文档/SDK 类型，样本来源已在节内逐条标注。

---

## 1. 总览对照表

| Backend | 官方 plan mode | 计划数据形状（桥可拿到什么） | 官方审批动作全集 | 桥可答性（现状） | 统一抽象可归一性 | 建议优先级 |
| --- | --- | --- | --- | --- | --- | --- |
| Grok Build（基准） | ✅ plan mode + `x.ai/exit_plan_mode` 反请求 | **请求内 planContent 全文**（markdown，camelCase `{sessionId,toolCallId,planContent?}`）；生产实测 1.4–1.6KB | approve / requestChanges（**带文字反馈**）/ 行内评论 / copy / quit（共 5 键） | ✅ 已上线（§25：approved/cancelled 二值；无反馈） | **高**（动作词汇最全，天然基准） | 第一批（最小档已交付，升级最直接） |
| Claude Code | ✅ permission-mode `plan` + `ExitPlanMode` 工具（需权限） | **`input.plan` 全文** + `planFilePath`（+ 已废弃 `allowedPrompts`）；本机实测计划 7.5–7.9KB | 批准（auto mode / 手动逐批 二选）/ No-keep-planning（**反馈走 deny.message**）/ 交互式另有 Ctrl+G 编辑计划、clear-context 变体 | ✅ can_use_tool 管道已上线；ExitPlanMode 当前无特殊处理（当通用权限卡，计划全文挤在 toolInput 里） | **高**（审批门真实存在、桥已可代答） | 第一批 |
| DeepSeek Harness (dsh-web) | ✅ `/plan` 命令 + `exit_plan_mode` 工具（plan-review intent） | **question `detail` = plan 全文**（markdown，须 `#` 标题开头） | Approve / Keep-planning（**支持 custom 自由文字反馈**）/ dismiss（用户拿回回合） | ✅ question 管道已上线（`respondQuestion` 已有 custom 参数，**公开包装未透出**）；plan 投影对转发器的暴露面未核实 | **高**（原生反馈通道齐全） | 第一批（补一项转发面核实） |
| Codex（Web 已退役；产品载体 Desktop/codex-remote） | ✅ collaboration mode `Plan`（主线恒启用；app-server wire 类型标 EXPERIMENTAL） | `PlanItem{text}` 完成项 + `PlanDelta` 流（app-server `{"type":"plan","id","text"}`）；无长度上限 | TUI「Implement this plan?」三选：implement / clear-context+implement / stay——**纯客户端编排，无 wire 审批请求** | ⚠️ plan 可旁观（退役前 codex-web 已消费 `turn/plan/updated`；现载体 codex-remote approval 面未接）；「审批」只能合成用户消息+切模式（非官方请求语义） | **中**（展示先行；动作语义需 owner 裁决） | 第二批（展示）/ 动作另裁决 |
| OpenCode Web | ✅ 原生 `plan` agent + `plan_exit` 工具 | `.opencode/plans/<ts>-<slug>.md` **纯 markdown 文件**（Mac 本地可直读）+ 通用 tool part；无 plan 专属 SSE 事件 | question「switch to build agent?」**Yes/No（无自由文字反馈）**；Yes=合成 build 消息 | ✅ question.asked→question_reply 管道已上线；**版本漂移警示**（checkout 是 dev 分支，产品连 stable serve） | **中**（动作词汇最小；受版本漂移制约） | 第二批（先核对 stable wire） |

> 「官方无此功能」结论：**五个 backend 全部存在某种 plan mode**——本次调研没有出现需要写「官方无此功能」的 backend；差异在于审批门的形态（真反请求 vs 客户端编排 vs question 通道）。

---

## 2. 基准：Grok Build（§25 已核实事实 + 本次补齐）

### 2.1 §25 已核实事实（转录，上游 @72a61251）

- 官方 leader 广播 `x.ai/exit_plan_mode` 反请求，plan 全文在 `planContent`；wire 为 camelCase `{sessionId, toolCallId, planContent?}`，应答 `{outcome:"approved"|"cancelled"|"abandoned", feedback?}`——`feedback` 仅 cancelled 且用户输入文字时存在。官方 round-trip 测试覆盖三种 outcome + 无 plan 分支。
- Mac 端 grok TUI 审批界面 = plan.md 文档查看器（行号/着色/滚动）+ 底部 5 键按钮栏：`a` approve、`s` request changes（带反馈）、`c` 行内评论、`y` copy plan、`q` quit plan（owner 2026-09-03 23:06 真机截图 + 源码核实）。
- iOS 最小档（§25 已交付）：「计划审批: <首个标题行>」权限卡 + 允许/拒绝两键；允许→`approved`、拒绝→`cancelled`（无 feedback）。**已知语义偏差**：iOS「拒绝」= TUI 的 quit（放弃）而非 request changes（打回改稿），两者对 agent 后续行为不同。

### 2.2 本次补齐：行内评论如何上行 wire（已核实）

**结论：行内评论没有任何独立 wire 字段。`PlanComment {id, line_range, text}` 是 TUI 本地结构，提交时被 `format_feedback` 聚合成一段文字，沿「cancelled + feedback」一次发出；若用户在留评论的同时选择批准，wire 仍是 `{outcome:"approved"}`（approved 无 feedback），评论转由一条注入的用户消息（Interject）送达模型。**

锚点（均为 `/Users/jacklee/Projects/grok-build` @ `72a61251`）：

1. 评论结构 `PlanComment {id, line_range: Range<usize>, text}`：`crates/codegen/xai-grok-pager/src/views/plan_approval_view.rs:48-53`。
2. 聚合格式化 `format_feedback(freeform)`（plan_approval_view.rs:110-144）：每条评论渲染为
   `Proposed plan line N:` / `Proposed plan lines N-M:` + 引用原文片段（`> ` 前缀，`inline_plan_snippets` :205-227）+ `Comment:\n<text>`；多条以 `\n\n` 连接；自由文字在已有评论时冠 `Additional feedback:\n` 前缀。文件后备计划（`PlanReviewSource::FileBacked`）的评论格式为 `@plan.md:N-M\n<text>`（:192-203）。
3. **request changes 路径**（`s` 键 → Prompt 焦点 → Enter 提交）：`send_plan_feedback` 把 `format_feedback` 结果经 `send_cancelled(to_send)` 发出 → wire `{outcome:"cancelled", feedback:"<聚合文本>"}`。`app/agent_view/plan.rs:299-319`；`s` 键切换到 Prompt：`app/agent_view/viewer.rs:150-157`；Enter 提交分发：`app/agent_view/plan.rs:415-446`。
4. **approve + 评论并存路径（新发现，§25 未记录）**：用户留了评论又按 `a` 批准时，wire 上 `send_approved()` 不带 feedback（类型上 approved 也无此字段），评论被格式化为 `"The user approved the plan with the following review comments:\n\n<formatted>"` 并经 `Action::Interject` 作为后续用户消息注入会话。`app/agent_view/plan.rs:187-205`。
5. **casual 评论（无待审请求时也可评论）**：plan 预览查看器里可随时写行内评论并 Ctrl+Enter 发送（`send_casual_plan_comments`），同样经 `format_plan_comments` 聚合为 Interject 消息，不需要存在 pending 的 exit_plan_mode 请求。`app/agent_view/viewer.rs:163-169`、`app/agent_view/plan.rs:669-671,728`。
6. 三个 outcome 的发送函数与空 feedback 过滤：`plan_approval_view.rs:147-190`（`send_approved` / `send_abandoned` / `send_cancelled` / `send_stale_cancel`）。

### 2.3 对 §25 记录的两处修正（成文时核实）

1. **wire 类型文件路径**：§25.2 写 `crates/codegen/xai-grok-workspace/src/exit_plan_mode/types.rs`；当前 checkout @72a61251 该目录不存在，权威路径为 `crates/codegen/xai-grok-tools/src/implementations/grok_build/exit_plan_mode/types.rs`（pager 经 `use xai_grok_tools::implementations::grok_build::exit_plan_mode::{...}` 引用，`plan_approval_view.rs:3-6`）。
2. **`q` 键语义**：§25.2 记「q→cancelled」，源码实际是 `q` → `abandon_plan()` → `{outcome:"abandoned"}`（`app/agent_view/viewer.rs:159-162`、`app/agent_view/plan.rs:274-281`；`send_abandoned` plan_approval_view.rs:179-181）。`cancelled` 无 feedback 仅出现在 stale 兜底（`send_stale_cancel` :187-189）。**这意味着 CordCode 最小档「拒绝→cancelled」与官方 TUI「quit→abandoned」存在第三个语义偏差点**：grok 上游对 cancelled/abandoned 有不同处理路径（完整档设计时需在 grok 分支上区分映射，见 §7）。

### 2.4 其余锚点

- 5 键栏渲染（可点击按钮 + 快捷键）：`views/file_search/line_viewer.rs:1673`（y copy plan）、`:1685`（a approve）、`:1689`（casual 模式 s send）、`:1699`（s request changes）、`:1708`（q quit plan）——均为 `build_shortcut_button` 调用行，修订时逐行复验；鼠标点击同动作 `app/agent_view/viewer.rs:455-470`。`a` 键直达批准：`app/agent_view/plan.rs:407-414`。
- 空 plan 占位（exit_plan_mode 无 planContent 时仍可 approve/quit/request-changes）：`plan_approval_view.rs:12-27`（`EMPTY_PLAN_PLACEHOLDER`）。
- 体积：wire 无截断逻辑；生产实测（本机 `~/Library/Application Support/CordCode Link/logs/go-bridge.log`）：
  - `:2885` `2026-09-03T23:02:41 grokbuild: leader exit_plan_mode registered ... wireId=7 planBytes=1635`
  - `:7928` `2026-09-03T23:05:14 ... wireId=8 planBytes=1376`

### 2.5 来源清单

```text
上游仓库路径=/Users/jacklee/Projects/grok-build  分支=main  提交=72a61251fcffb464bcc687aeb5a998e5a98ec0c9  未提交状态=干净
锚点=crates/codegen/xai-grok-tools/src/implementations/grok_build/exit_plan_mode/types.rs:9-25,31-121；
    crates/codegen/xai-grok-pager/src/views/plan_approval_view.rs:3-6,12-27,48-53,110-144,147-190,192-247；
    crates/codegen/xai-grok-pager/src/app/agent_view/plan.rs:180-207,274-281,299-319,359-458,459,669-671,728；
    crates/codegen/xai-grok-pager/src/app/agent_view/viewer.rs:150-170,455-470；
    crates/codegen/xai-grok-pager/src/views/file_search/line_viewer.rs:1673-1708
产品日志=~/Library/Application Support/CordCode Link/logs/go-bridge.log:2885,7928（2026-09-03 生产运行）
本仓实现（现状对照）=agent/grokbuild/acp_types.go:425-436；leader_subscriber.go:607-609,681-709,916-961（plan 分支 :933-941）
```

---

## 3. Claude Code

### 3.1 官方 plan mode 语义

- 存在。plan mode 是 permission mode 之一（`--permission-mode plan` / 交互式 Shift+Tab 循环 / SDK `permissionMode`）。语义：**写类工具（file edit、写 shell 命令，v2.1.212 起 touch/rm 等）无论 allow 规则一律路由进审批回调**，只读工具照常执行——是 deny-by-default 的写门，不是全工具封死。出处：官方文档 Agent SDK permissions（"plan routes file-edit and shell-write tools to your canUseTool callback regardless of allow rules"）。
- 计划呈现：模型调用 **`ExitPlanMode` 工具**（确切大小写；官方 tools-reference：「Presents a plan for approval and exits plan mode | Permission required: Yes」），计划全文在工具 input。另有 `EnterPlanMode`（input `{}`，无需权限）。stream-json 的 `system/init` 帧带 `permissionMode` 字段（camelCase；本机 2.1.234 嵌套运行实测确认该键存在）；**plan-mode 退出会触发 `conversation_reset` 事件**（SDK d.ts 原文注释 "Emitted by /clear, plan-mode exit, and fresh-session flows"——对 CordCode session 同步是有用信号）。
- 交互式审批界面（官方文档 permission-modes「Review and approve a plan」，逐字）：
  - **"Yes, and use auto mode"**（auto mode 不可用时文案为 "Yes, auto-accept edits"；bypass 会话则变 "Yes, and switch to BYPASS PERMISSIONS..."）
  - **"Yes, manually approve edits"**
  - **"No, keep planning"**（"stay in plan mode and tell Claude what to change"）
  - 变体：`Ctrl+G` 在默认编辑器直接改计划再继续；`showClearContextOnPlanAccept` 开启时多一个「批准并清空规划上下文」选项。
  - **批准的本质是切换 permission mode**（"Approving a plan exits plan mode and switches the session to the permission mode each approve option describes"）。
  - 未取得交互式截图（需真机 UI 操作，本次只读调研不做）。

### 3.2 计划数据形状（本机真实 transcript 样本）

来源：`~/.claude/projects/**/*.jsonl`（claude CLI 自写会话记录，格式与桥消费的 stream-json 消息同源；195 个文件含 ExitPlanMode 字样，配对出 2 条完整 tool_use→tool_result 链）。样本均为脱敏摘录，仅取结构与规模：

- `ExitPlanMode` tool_use input 字段：**`['allowedPrompts', 'plan', 'planFilePath']`**
  - `plan`：markdown 全文。实测两例分别 **7557 / 7883 字节**——iOS 计划渲染必须按多 KB 长文设计。
  - `planFilePath`：计划落盘路径（`~/.claude/plans/<slug>.md`）——Mac 本地可直读，是全文展示的第二来源。
  - `allowedPrompts`：`[{tool:"Bash", prompt:"..."}]` 数组。**版本漂移记录**：官方 TS SDK 类型已标 `allowedPrompts` 为 Deprecated（v2.1.205 起忽略），但本机 2.1.234 真实 transcript 仍在发送——按纪律以真实样本为准记录，标注为「已废弃仍在线」。
- 批准后的 tool_result（逐字开头）：`"User has approved your plan. You can now start coding. Start with updating your todo list if applicable\n\nYour plan has been saved to: <planFilePath>"`。
- 拒绝路径：本机未配对到 ExitPlanMode 专属拒绝样本（仅有通用工具拒绝 tool_result：`"The user doesn't want to proceed with this tool use. ... STOP what you are doing and wait..."`，来自 `-Users-jacklee-Projects-Chat/4077da23...jsonl`）。**plan 专属拒绝文案未取得本机样本**，以官方文档为准（见 3.3）。
- 大小限制：plan 字段无任何文档化上限（stdin 管道整体 10MB cap 与 plan 无关）。

### 3.3 动作全集（wire 形状）

**请求**（headless stream-json，官方 SDK 类型 `SDKControlRequest` + 我方 adapter 实收同构）：

```jsonc
{
  "type": "control_request",
  "request_id": "<应答必须原样回显>",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "ExitPlanMode",
    "input": { "plan": "<计划全文 markdown>", "planFilePath": "...", "allowedPrompts": [...] },
    "tool_use_id": "...",
    "permission_suggestions": [ /* PermissionUpdate[]，可选 */ ]
    /* 其余可选字段：decision_reason、title、display_name、agent_id 等 */
  }
}
```

**应答**（`control_response`，`{behavior:"allow"} | {behavior:"deny", message}`）：

- allow：`{behavior:"allow", updatedInput?: {...}, updatedPermissions?: PermissionUpdate[]}`。
- **deny：`{behavior:"deny", message: string}`——`message` 必填，是官方指定的「拒绝反馈回模型」通道**（文档原文："When denying, provide a message explaining why. Claude sees this message and may adjust its approach"）。这正对应交互式 "No, keep planning" + 用户文字的语义。
- **`planApproval` 字段不存在**：当前 `sdk.d.ts` 全文、两份 SDK 参考与全站文档均无此字段（曾见的旧印象属误传，勿按其实现）。批准时表达「auto mode vs 手动逐批」的候选机制是 allow 分支 `updatedPermissions: [{type:"setMode", mode:"acceptEdits"|"default"|"auto"|..., destination:"session"}]`——setMode 本身是文档化机制，但「用它实现计划批准的模式二选」属**基于类型的推断，文档未逐字写明，须真机验证**（未核实项）。
- bare `-p`（无 canUseTool / `--permission-prompt-tool`）：文档明确 "ask 判定即 terminal deny"，产生 `system/permission_denied` 事件与 `result.permission_denials[]`（`tool_input` 含 ExitPlanMode 的 plan 全文，可作旁观观测点）；是否绝对不输出 control_request 帧：类型注释强烈指向否，**未实测**（本机无凭据）。

### 3.4 桥可答性（我方现状）

- 桥以 `--input-format stream-json` 双向流驱动 claude（`agent/claudecode/claudecode.go:31`），can_use_tool 请求在产品中真实到达并已可代答（权限卡链路上线已久）。
- `handleControlRequest`（`session.go:827-889`）：`subtype != "can_use_tool"` 丢弃；AskUserQuestion 走 V2 结构化输入引擎；autoApprove/dontAsk/acceptEditsOnly 本地代答；否则 emit `EventPermissionRequest`（ToolInput=summarize、ToolInputRaw=原始 input）。**ExitPlanMode 无任何特殊处理**（agent/claudecode 全目录 grep 0 命中）——今天它会作为通用权限卡出现，`summarizeInput`（`claudecode.go:2225`）无 ExitPlanMode 分支，ToolInput 是整个 input 的 JSON 序列化（含全文 plan，展示效果差但**数据已在 wire 上**）；`ToolInputRaw.plan` 即计划全文。
- 应答路径已具备：`RespondPermission`（`session.go:979`）→ `respondPermissionContext` 写 `{type:"control_response", response:{subtype:"success", request_id, response:{behavior:"allow", updatedInput} | {behavior:"deny", message}}}`（deny 的 message 通道已在 AskUserQuestion 拒绝路径使用，实测语义可用）。
- 仓内 fixture 现状：`session_test.go:361` 有 can_use_tool/AskUserQuestion 真实形状 fixture；**无 ExitPlanMode fixture**（调研发现，完整档实施时应补）。
- 结论：**桥今天就能收到并代答 ExitPlanMode（allow / deny+message）**；缺的是：plan 全文的专门展示、requestChanges 与 quit 的动作区分（现都落 deny）、以及 deny.message 的 iOS 反馈输入。

### 3.5 映射到统一抽象的评估

- `approve` → control_response allow（可选带 updatedPermissions setMode——**owner 决策点**：是否透传「auto mode vs 手动逐批」二选）。
- `requestChanges(反馈)` → control_response deny + message（官方语义就是 keep planning + 反馈）。**原生支持，通道已在**。
- `quit` → deny（无 message / 固定文案）。注意：**claude wire 上 requestChanges 与 quit 同为 deny**，差异只在 message 有无与文案——统一抽象里两个动作在 claude 侧映射到同一 wire 形状，属「动作词汇归一但 wire 不分」的合法情形。
- `comment` → 无行内评论概念；等价物是 deny.message 自由文本（或批准后的普通用户消息）。deliberately unsupported 建议。
- `copy` → 纯 iOS 本地动作（plan 文本已有）。
- 特有项：`planFilePath`（本地全文第二来源）、`allowedPrompts`（已废弃，忽略）、批准=切权限模式的语义（iOS 需文案/选项表达）。

### 3.6 来源清单

```text
官方文档（claude-code-guide agent 检索，2026-09-03）：
  https://code.claude.com/docs/en/permission-modes（交互式三选项/Ctrl+G/clear-context/批准=切模式）
  https://code.claude.com/docs/en/agent-sdk/typescript（canUseTool/control_request/control_response/PermissionResult/
    bare -p terminal deny/SDKPermissionDeniedMessage 原文）
  https://code.claude.com/docs/en/agent-sdk/python（ExitPlanMode input {"plan": str}）
  https://code.claude.com/docs/en/agent-sdk/permissions（plan 模式写门原文）
  https://code.claude.com/docs/en/tools-reference（ExitPlanMode/EnterPlanMode 条目）
  https://unpkg.com/@anthropic-ai/claude-agent-sdk@latest/sdk.d.ts（SDKControlRequest/SDKControlResponse/
    conversation_reset/updatedPermissions setMode 类型）
本机真实样本=~/.claude/projects/ 两个 transcript（ExitPlanMode input 三字段、plan 7.5-7.9KB、批准 tool_result 文案）；
  通用拒绝 tool_result（Projects-Chat/4077da23）。plan 专属拒绝文案：未取得本机样本，仅文档。
本机嵌套实测=/tmp/plan-survey-claude/sample1.jsonl（claude 2.1.234；Not logged in，仅捕获 init 帧 permissionMode 键）
本仓锚点=agent/claudecode/claudecode.go:31,2225；session.go:427-428,827-889,979,respondPermissionContext；
  session_test.go:361
未核实项=planApproval 字段确认不存在（已查证为「不存在」）；setMode 实现计划批准模式二选=推断需真机验证；
  bare -p 是否输出 control_request=未实测
```

---

## 4. Codex（Codex Web 与 Codex Desktop 共用 app-server 协议族）

> **载体时效（v1.1）**：codex-web 已于 2026-09-04 从产品 lineup 退役（源码保留、回滚=加回 drivers；Codex 产品面由 `codex-remote` 独立承接）。本节协议事实对 codex-remote 同构适用（同一 app-server 协议族）；涉及产品载体的表述已按退役时点标注，完整档的 Codex 载体基线 = **codex-remote**（v1.2 撤回「恢复 codex-web」并列选项，理由见 §8.3-7 备注）。

### 4.1 官方 plan mode 语义

- 存在。Plan 是两种 collaboration mode 之一：`ModeKind {Plan, Default}`（serde snake_case → `"plan"`/`"default"`；`codex-rs/protocol/src/config_types.rs:668-683`）。
- 进入途径：TUI `/plan` 命令（`slash_command.rs:42,130`；分发 `chatwidget/slash_dispatch.rs:84-102`）、Shift+Tab 循环（`keymap.rs:2183-2185` → `chatwidget/settings.rs:633`）、启动配置（`core/src/config/mod.rs:758`）、app-server `turn/start.collaborationMode`（`protocol/v2/turn.rs:249-251`）与 `thread/settings/update.collaborationMode`（`protocol/v2/thread.rs:269-271`）。**实验性标注在协议类型层而非 feature gate**（v1.1 修订）：这两个字段及其注释标 EXPERIMENTAL（`#[experimental]` 属性/注释：`protocol/v2/turn.rs:244,249`、`protocol/v2/thread.rs:265,269`、`protocol/v2/item.rs:272`）；`Feature::CollaborationModes`（`features/src/lib.rs:382`）的 FeatureSpec 为 **`Stage::Removed, default_enabled: true`**（`:1556-1560`），:380-381 注释原文 "Kept for config backward compatibility; behavior is always collaboration-modes-enabled."——**协作模式在主线恒启用**，不再是 experimental 开关。**模型不可自主进出 Plan**：自动触发的 turn 禁止进入或离开（`session/turn_input.rs:57-74`）。
- 限制是**提示词级 + 两条硬规则**，无沙箱/工具白名单分支（`ModeKind::Plan` 非测试引用仅 6 处，无一在 sandbox/工具注册）：① `update_plan`（TODO 清单工具，与 plan mode 无关的 checklist）在 Plan mode 被拒（`tools/handlers/plan.rs:87-91`）；② `request_user_input` 提问工具默认仅 Plan 可用（`config_types.rs:699-701`）。提示词模板：`codex-rs/collaboration-mode-templates/templates/plan.md`（三阶段会话式规划，最终计划须 decision complete，每 turn 至多一个 `<proposed_plan>` 块，修订块须完整替换旧计划）；注入点 `core/src/context/world_state/collaboration_mode.rs:24-78,163-169`。

### 4.2 计划数据形状

- **不存在 plan/exit_plan 工具**（全仓检索 `proposed_plan|PlanTool|exit_plan` 佐证）。计划 = 助手消息中被标签包裹的 markdown 文本协议：
  - `const OPEN_TAG = "<proposed_plan>"; CLOSE_TAG = "</proposed_plan>"`（`utils/stream-parser/src/proposed_plan.rs:7-8`，本次亲验）。
  - 流式解析产出 `ProposedPlanStart/Delta/End`；完成项 id 规则 `format!("{turn_id}-plan")`（`core/src/session/turn.rs:1714`）。
  - 核心协议：`EventMsg::PlanUpdate(UpdatePlanArgs)`（`protocol.rs:1523`）与 `EventMsg::PlanDelta(PlanDeltaEvent{thread_id,turn_id,item_id,delta})`（`protocol.rs:1545,1962-1968`）；完成项 `TurnItem::Plan(PlanItem{id, text})`（`protocol/src/items.rs:50,184-188`，本次亲验——**text 是无界 String**）。
  - app-server v2：`ThreadItem::Plan {type:"plan", id, text}`（`protocol/v2/item.rs:270-277`，serde camelCase，注释标 EXPERIMENTAL、完成项 authoritative、deltas 拼接不保证一致）+ 通知 `PlanDeltaNotification{threadId,turnId,itemId,delta}`（`protocol/v2/item.rs:1433-1438`；映射 `protocol/event_mapping.rs:372-377`）。
  - 官方测试：`app-server/tests/suite/v2/plan_item.rs`（mock SSE `<proposed_plan>...` → 断言 PlanItem 与 PlanDelta 拼接）。
- 桥现状：退役前的 codex-web adapter 已消费 plan 流（我仓 `think.md:482-491` 已有结论：`turn/plan/updated` 是 todo 的唯一结构化真相源、plan 展示缓存——非审批）；现产品载体 codex-remote 同协议族、事件面同构可达（**协议族推断，Remote Control 链路对 plan 事件的实际透传未核实**），需自行接线（其 approval 基建现状见 4.4）。
- 体积：无截断（全仓无截断常量命中）。

### 4.3 动作全集（审批门形态：TUI 纯客户端编排，无 wire 审批请求）

- turn 结束后若本 turn 出现过 plan item 且仍在 Plan mode，TUI 弹 **"Implement this plan?"** 三选项（`tui/src/chatwidget/plan_implementation.rs:9-19`，本次亲验）：
  - **"Yes, implement this plan"** → 提交固定用户消息 `"Implement the plan."` 并携带 Default mode mask（`:13,36-41`）。
  - **"Yes, clear context and implement"** → 新线程，用户消息内嵌 plan 全文前缀（`:14-19,57-63`）。
  - **"No, stay in Plan mode"** → 不动（留在 Plan mode）。
  - 触发条件 `turn_runtime.rs:226-251`。提示词明言不要在计划里问 "should I proceed?"——**退出 Plan mode 无需任何审批请求，纯用户动作**。
- **带文字反馈的通道存在但属于提问工具而非计划审批**：`request_user_input`（questions[{id,header,question,options[{label,description}]}]，客户端自动加 free-form "Other" 选项；spec `request_user_input_spec.rs:19-83`）。
- **app-server approval 家族无 plan 变体**（逐条枚举核实）：v2 仅有 `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`item/permissions/requestApproval`（`protocol/common.rs:1690-1718`）与 v1 `execCommandApproval`/`applyPatchApproval`（`:1747-1755`）。应答全集：core `ReviewDecision`（`protocol.rs:4053-4088`）中 **`Denied{rejection: String}` 是唯一带文字反馈的变体**（属 exec/fileChange 审批，不是 plan）；v2 `CommandExecutionApprovalDecision`（Accept/AcceptForSession/AcceptWithExecpolicyAmendment/ApplyNetworkPolicyAmendment/Decline/Cancel，`protocol/v2/item.rs:64-83`）、`FileChangeApprovalDecision`（Accept/AcceptForSession/Decline/Cancel，`:113-122`）。

### 4.4 桥可答性

- **旁观**：两个 backend 都能收到 PlanItem/PlanDelta（退役前的 codex-web 已在用；现产品载体 codex-remote 同协议族，需自行接线）。
- **代答「审批」**：没有官方审批请求可答。等价动作 = 官方客户端编排：向 thread 发用户消息 `"Implement the plan."`（或含反馈的自然语言）+ 切 collaborationMode（`thread/settings/update` 或 `turn/start.collaborationMode`）。CordCode 作为官方客户端**技术上可同样编排**，但这不是 wire 审批语义——**是否产品化属 owner 决策**（v1.1 修订：feature gate 论据移除——主线恒启用；剩余风险为协议类型仍标 EXPERIMENTAL、目标部署能力须按部署验证）。
- 我方现状：退役前 codex-web 的 approval 映射只覆盖 command/fileChange/permissions 三类（`agent/codex-web/interactions.go:244-306`；`classifyServerRequest:258-274` 无 plan method；源码随退役保留）；应答 allow→`{"decision":"accept"}`、deny→`{"decision":"cancel"}`（`:385-415`）。**codex-remote 的 `RespondPermission` 目前直接 `ErrNotSupported`**（`agent/codex-remote/session.go:121-123`）——连既有三类审批都未接，是比 plan 更早的缺口。

### 4.5 映射评估

- 计划展示：`ThreadItem::Plan{text}` 直接映射统一 plan 数据模型（markdown 全文）——**高度可归一，建议展示先行**。
- `approve` → 合成 `"Implement the plan."` + 切 Default mode（**非官方审批语义，需 owner 裁决**；wire 类型标 EXPERIMENTAL，目标部署需按部署验证）。
- `requestChanges(反馈)` → 普通用户消息携带反馈文字（自然语言编排，无结构化通道）。
- `quit` → 切离 Plan mode（`thread/settings/update.collaborationMode=default`）或什么都不做。
- `comment` → 无对应概念（可归到 requestChanges 文本）。
- deliberately unsupported 建议：clear-context-and-implement 变体（新建线程语义重）。

### 4.6 来源清单

```text
上游仓库路径=/Users/jacklee/Projects/codex  分支=main  提交=50fffd5ed367aa99491d9ec58575626fce4e9dd4  未提交状态=干净
锚点=protocol/src/config_types.rs:668-683,699-701；tui/chatwidget/plan_implementation.rs:9-19（亲验）；
    tui/chatwidget/slash_dispatch.rs:84-102；tui/keymap.rs:2183-2185；features/src/lib.rs:380-382,382,1556-1560；
    core/src/session/turn_input.rs:57-74；core/src/session/turn.rs:1714；core/src/tools/handlers/plan.rs:87-91；
    utils/stream-parser/src/proposed_plan.rs:7-8（亲验）；protocol/src/protocol.rs:1523,1545,1962-1968,4053-4088；
    protocol/src/items.rs:50,184-188（亲验）；app-server-protocol/src/protocol/v2/turn.rs:244,249-251；
    app-server-protocol/src/protocol/v2/thread.rs:265,269-271；
    app-server-protocol/src/protocol/v2/item.rs:64-83,113-122,270-277,1433-1438,1733-1743,1785-1787；
    app-server-protocol/src/protocol/common.rs:1690-1718,1747-1755,2807-2867；
    app-server-protocol/src/protocol/event_mapping.rs:372-377；request_user_input_spec.rs:19-83；
    collaboration-mode-templates/templates/plan.md；
    测试=app-server/tests/suite/v2/plan_item.rs；core/tests/suite/items.rs:444-505；
    protocol/v2/tests.rs:682,715（requestApproval 反序列化）、:5107（requestUserInput/ToolRequestUserInputParams 测试，非 approval——v1.1 修正引用错位）；
    protocol/common.rs:2807-2867（内联 #[test]，:2813 起 v1 ExecCommandApprovalParams fixture）
本仓锚点=agent/codex-web/interactions.go:244-306,385-415（codex-web 2026-09-04 退役，源码保留）；agent/codex-remote/session.go:121-123；
    think.md:482-491
未核实项=安装版 Codex daemon 与 checkout 同版本性（未比对目标二进制版本）；协作模式主线恒启用（gate 已 Removed），
    但 app-server 协议类型仍标 EXPERIMENTAL——目标部署（ChatGPT Desktop / 远端 controller）是否提供协作模式能力须按部署验证
```

---

## 5. OpenCode Web

> **版本漂移警示（纪律项）**：本机 checkout 为 `dev` 分支（Effect HttpApi 重写后，2026-09-02 #44944）；CordCode opencode-web 实际连接的是 stable 轨 `opencode serve`（与我仓记忆「opencode 双轨道打包」一致）。以下 wire 名以 dev 分支源码为准，**完整档实施前必须按目标 stable 版本核对**（尤其 question/permission 路由名与字段）。

### 5.1 官方 plan mode 语义

- 存在，机制 = **原生 primary agent**（不是 mode 字段）：`plan` agent 与 `build` 并列（`packages/opencode/src/agent/agent.ts:141-155` build / `:156-181` plan，本次亲验）。权限即 plan mode 的全部约束：`edit: {"*": "deny", ".opencode/plans/*.md": "allow", <全局plans目录>: "allow"}`、`task.general: "deny"`、`plan_exit: "allow"`——**编辑受限靠 permission 体系按路径 deny/allow 实现**。
- 进入方式：TUI Tab/Shift-Tab 循环 agent（`packages/tui/src/config/keybind.ts:130-131`）或 `/agents` 对话框（`packages/tui/src/app.tsx:678-685`）；CLI `--agent plan`（`src/cli/cmd/run.ts:927`）；**wire 上发消息带 `agent:"plan"` 即选 plan mode**（PromptPayload，`groups/session.ts:70` + `src/session/prompt.ts:1499-1520`）。模型自主进入的 `plan_enter` 工具已在 commit `fa559b0385`（2026-02-24）临时禁用，当前只注册 `plan_exit`（`src/tool/registry.ts:248`）。非实验模式注入只读 reminder（`src/session/reminders.ts:26-49`）；实验开关 `OPENCODE_EXPERIMENTAL_PLAN_MODE`（`src/effect/runtime-flags.ts:47`）启用「计划文件工作流」reminder（:70-89）。

### 5.2 计划数据形状

- **计划 = 纯 markdown 文件，无结构化 schema**：`Session.plan()`（`src/session/session.ts:331-336`）——vcs 项目写 `<worktree>/.opencode/plans/<created>-<slug>.md`，否则全局 data 目录 plans/。模型用普通 write/edit 工具编辑该文件（这些工具调用以通用 tool part 形式出现在 SSE 流里）。
- **无 plan 专属 SSE 事件**：事件全集为 `session.*`、`message.updated/removed`、`message.part.updated/removed/delta`、`session.diff/error`、`permission.asked/replied`、`question.asked/replied/rejected`（`packages/schema/src/v1/session.ts:571-676`（`message.removed` :605，v1.1 补列）、`v1/question.ts:58-60`）——plan 全部经通用 tool part + question 事件表达。
- 桥获取全文的两条路：① plan 文件在 Mac 本地，桥可直读（计划路径可从 plan agent 会话的工具调用/reminder 推断）；② `plan_exit` 工具调用的 question 文本内含计划路径。ToolPart 形状 `{type:"tool", callID, tool, state, metadata}`，state 四态 `pending/running/completed/error` 联合定义 :259-301、ToolPart :315-322，completed 态形状 `{status:"completed", input, output: string, title, metadata, time}`（`v1/session.ts:277-290`）。
- 体积：计划文件无上限；工具 output 截断 `MAX_LINES=2000 / MAX_BYTES=50KB`（可配 `tool_output`）只影响 part.output 不影响 md 文件（`src/tool/truncate.ts:17-18,41-43`）。

### 5.3 动作全集（审批门 = plan_exit 工具弹 question）

`plan_exit` 工具（**无参数**，`src/tool/plan.ts:13`，本次亲验）execute 时弹 question（:30-44，亲验）：

- 问题文本：`"Plan at <path> is complete. Would you like to switch to the build agent and start implementing?"`，header "Build Agent"，**`custom: false`（只有 Yes/No，无自由文字反馈）**，options：Yes="Switch to build agent and start implementing the plan" / No="Stay with plan agent to continue refining the plan"。
- **Yes** → 合成 build-agent 用户消息 `"The plan at <path> has been approved, you can now edit files. Execute the plan"`（synthetic text part，agent 切 build；:53-69）；工具 output `"User approved switching to build agent. Wait for further instructions."`（:73）。
- **No** → `Question.RejectedError`（:46）——模型留在 plan agent 继续改计划。**拒绝无反馈通道**（用户想给反馈只能 reject 后另发普通消息）。
- wire：SSE `question.asked`（`QuestionRequest{id:"que_*", sessionID, questions:[{question,header,options,multiple?,custom?}], tool?{messageID,callID}}`，`v1/question.ts:35-40`）+ `POST /question/:requestID/reply` body `{answers: string[][]}` / `POST /question/:requestID/reject`（`server/routes/instance/httpapi/groups/question.ts`）。
- TUI 侧无专门计划面板组件（session-ui 仅 plan agent 图标配色与 agent 选择器）；切回 build 的自动跟随靠监听 `message.part.updated` 中 `part.tool === "plan_exit"` 完成态（`packages/tui/src/routes/session/index.tsx:334-340`）。

### 5.4 桥可答性

- opencode-web adapter **已有完整 question 管道**：`question.asked` → `user_input_requested`（`agent/opencode-web/questions.go`、`wire_descriptor.go`；测试 `interactions_mutations_c6_c7_test.go:80-167` 有官方形状 fixture），question_reply 走官方 answers body。
- 因此 **plan_exit 的 Yes/No 桥今天理论上就能代答**（它就是一道普通 question）；缺的是把它识别为「计划审批」并取到计划全文（读 plan 文件）。
- 版本漂移是主要风险（见节首警示）。

### 5.5 映射评估

- `approve` → question reply `answers=[["Yes"]]`。
- `requestChanges(反馈)` → **官方无反馈通道**：只能 reject（=No，留在 plan agent）后由用户另发消息；或 deliberately unsupported（反馈为空）。
- `quit` → reject + （可选）`/plan off` 等价动作——opencode 无独立「放弃计划」语义，离开 plan agent 即放弃。
- `comment` → 不支持（计划是本地文件，Mac 端可改文件，但那是文件编辑不是审批动作）——deliberately unsupported 建议。
- 计划展示 → 读 `.opencode/plans/*.md` 文件全文（Mac 本地直读，与 grok 的 request-内全文不同来源形态）。

### 5.6 来源清单

```text
上游仓库路径=/Users/jacklee/Projects/opencode  分支=dev  提交=69c172e8a7c0086887b1f93ed5a162f14b6aa0c5  未提交状态=干净
锚点=packages/opencode/src/agent/agent.ts:141-155,156-181（亲验）；src/tool/plan.ts:13,30-75（亲验），46,53-73；
    src/session/session.ts:331-336；src/tool/registry.ts:248；src/session/reminders.ts:26-49,70-89；
    src/effect/runtime-flags.ts:47；packages/tui/src/config/keybind.ts:130-131；packages/tui/src/routes/session/index.tsx:334-340；
    packages/schema/src/v1/session.ts:259-301,277-290,315-322,571-676（message.removed :605）；v1/question.ts:35-40,58-60；
    v1/permission.ts:23-29,61-66；
    groups/question.ts；groups/session.ts:70；src/session/prompt.ts:1499-1520；src/tool/truncate.ts:17-18,41-43
历史=commit fa559b0385（plan_enter 禁用）；origin commit 0a3c72d678（plan mode 引入，含历史 plan fixture）
本仓锚点=agent/opencode-web/questions.go；events.go:745-765；permissions.go:14-26；wire_descriptor.go；
    interactions_mutations_c6_c7_test.go:80-167
未核实项=stable opencode serve（产品实际连接的目标版本）与 dev 分支的 wire 名一致性——完整档实施前必须按目标版本核对
```

---

## 6. DeepSeek Harness（dsh-web）

### 6.1 官方 plan mode 语义（本次调研的反转性发现：一等功能，非「无此功能」）

- 存在，核心包 `packages/plan/plan-mode`（`@deepseek-ai/dsh-plan-mode`）。
- **`/plan` 命令**（`src/index.ts:225-268`，本次亲验）：`/plan [off|message]` 进入/退出；带消息时经 `agent.steer()` 作为下步普通用户消息注入；`off` 即退出。
- 会话事件 `'plan/mode': {active: boolean}`（:46，log-only、last-wins，resume/fork 可恢复）；客户端投影 `PlanProjection {active, pending}`（`packages/plan/plan-mode/src/types.ts:21-24`）。
- 约束是**提示词级**（v1.1 修订，原表述过强）：preset 指引文本明言 plan 阶段不要用 `todo_write` 跟踪（`packages/preset/agent-presets/presets/standard/agent.cordis.yml:117-118`，"Do not use todo_write to track this planning phase"）——`todo_write` 工具本身仍注册（tool-todo `:240-241`），非工具级禁用；plan 与 permission-presets/sandbox 相互独立（`docs/subsystems/plan.md:5`）。
- 默认装配：base bundle 装配 plan-mode 插件（`packages/bundle/base/cordis.patch.yml:307-309`，**不引用 standard preset**——两者是各自携带配置的独立载体，v1.1 修正关系表述）+ standard preset 携带 plan:policy 指引（`agent.cordis.yml:110-124`）+ web-app 的 `ui-plan`（`packages/bundle/web-app/cordis.patch.yml:299`）——**官方 web 前端默认带计划 UI**。

### 6.2 计划数据形状

- **`exit_plan_mode` 工具**（与 Claude 同名；`src/index.ts:60,271-359`，本次亲验 :300-359）：
  - 入参 `plan`（string，必填）：**完整 markdown 计划，必须以 `#` 标题开头**（工具内校验 `^#\s+\S`，否则报错）——工具描述原文 "The complete plan, as markdown, starting with a # heading that names it"。
  - 计划**不落盘**（全仓 `plan.md` 命中仅为文档文件名）；计划本体即工具调用 payload，经审批 question 的 `detail` 字段整文呈现（:305 `detail: args.plan`）。
  - 投影 `presentCall`：card 'generic'、title 取计划首个标题（:348-353）。
- 桥可见形态：`user-questions/request` waterfall（question `detail` = plan 全文）+ `plan` 投影 `{active,pending}` 经 `$events` 流。**具体投影/事件经何 API 面向 dsh-web 转发器暴露，本轮未核实**（dsh-web 的 mux 广播粒度需另查）——已列为完整档前的小核实项。

### 6.3 动作全集（审批门 = user-questions 带 plan-review intent）

审批通过 `interaction.ask` 弹「Plan review」（:300-317，亲验）：

- question：header "Plan review"、问题 "Approve this plan and leave plan mode?"、**`detail` = 计划全文**、两个选项：
  - **Approve** — "Leave plan mode; the plan is carried out from the next step."
  - **Keep planning** — "Stay in plan mode; feedback goes back to the model."
- **presentation intent 标记**：`intent: {kind: 'plan-review', approve: <label>}`（`packages/interaction/user-questions/src/types.ts:33-48`）——有能力的前端据此渲染专门计划审批面板；**answer 里可以带 `custom` 自由文字**：选 Keep planning 且 `item.custom` 非空时，工具抛错 `"The user chose to keep planning; their feedback: <custom>"` 把反馈带回模型（:331-336）——**原生带反馈打回通道**。
- **Approve** → 工具返回 `{approved: true}`，plan mode 在下一步静默退出（pendingIntents，:338-341）。
- **dismiss**（用户不答、拿回回合说话）→ `ASK_CANCELLED` 分支：抛错告知模型 "The user dismissed the plan review to speak instead; stay in plan mode, stop here, and wait for their message."（:312-316）——语义是「用户接管」而非「放弃」。
- web UI：`PlanReviewPanel`（`packages/client/ui-user-questions/src/client/PlanReviewPanel.tsx`，plan-review intent 专用面板）+ `PlanChip`（`packages/client/ui-plan/src/client/PlanModeControl.tsx:19` 组件、`:36-50` `off()` 执行体；实际退出经 `client/index.ts:60-61` 的 `commands.execute(sessionId, '/plan off', [])`）；e2e `apps/web/tests/plan-control-row.e2e.ts`。
- 另有独立的 user-approval 体系（`ApprovalOutcome = allowed-once|rejected|cancelled|unavailable`，`packages/interaction/user-approval/src/types.ts:32`，fail-closed）——与 plan 审批分属两个 seam。

### 6.4 桥可答性

- dsh-web adapter **已有 question 管道**：`question/requested` → question_reply/question_reject（`agent/dsh-web/streams.go:176-177`；`RespondQuestion`/`RejectQuestion` `session.go:204-213`；capability 已广告 `question_reply`，`wire_descriptor.go:30`）。
- **带反馈打回在 wire 上原生可行**：底层 `respondQuestion(ctx, sessionID, questionID, optionIDs, custom)`（`approvals.go:519`）第 5 参即官方 `custom` 字段（`:544-545` 组装进 answers body），**但公开包装 `RespondQuestion` 目前恒传 `""` 未透出**——完整档实施时把 feedback 从上层接到这个参数即可（调研发现的现成挂点）。
- 待核实：plan 投影（active/pending）与 question `detail`（计划全文）在转发器消费面上的实际可见性（见 6.2）。

### 6.5 映射评估

- `approve` → 回答 question 选中 Approve label。
- `requestChanges(反馈)` → 选中 Keep planning + `custom` = 反馈文字（**原生通道，语义与 grok request-changes 一致**）。
- `quit` → 对应面较绕：reject 整批会取消整个 ask batch（`RejectQuestion` "any rejected question cancels the WHOLE batch"，session.go:210-213）≈ dismiss 语义；真正的「关 plan mode」是 `/plan off` 命令（commands.execute 通道）——**完整档需在 dsh 上定义 quit 的目标语义**（dismiss vs /plan off），列为 owner/实施确认项。
- `comment` → 无行内评论；custom 反馈文本即等价物。
- 计划展示 → question `detail` 全文（markdown）。

### 6.6 来源清单

```text
上游仓库路径=/Users/jacklee/Projects/deepseek-harness  分支=master  提交=49a606bc5b5934603f22a26957a07dc799ab0291  未提交状态=干净
锚点=packages/plan/plan-mode/src/index.ts:46,60,225-268,271-359（:300-359 亲验）；src/types.ts:21-24；
    packages/interaction/user-questions/src/types.ts:21-30,33-48,85-89；
    packages/interaction/user-approval/src/types.ts:32,44-58,85-90；
    packages/preset/agent-presets/presets/standard/agent.cordis.yml:110-124,117-118（todo_write 提示词约束）,240-241（tool-todo 注册）；
    packages/bundle/base/cordis.patch.yml:307-309；packages/bundle/web-app/cordis.patch.yml:299；
    packages/client/ui-plan/src/client/PlanModeControl.tsx:19,36-50；packages/client/ui-plan/src/client/index.ts:60-61；
    packages/client/ui-user-questions/src/client/PlanReviewPanel.tsx；apps/web/tests/plan-control-row.e2e.ts；
    docs/subsystems/plan.md:5；docs/subsystems/approval.md:5
本仓锚点=agent/dsh-web/streams.go:176-177；session.go:204-213；approvals.go:519-568；wire_descriptor.go:17-18,30
未核实项=plan 投影与 question detail 在 dsh-web 转发器消费面（mux/$events 订阅粒度）的实际可见性——完整档前小核实项
```

---

## 7. 统一 plan 抽象建议（完整档设计输入）

> 本节只给设计输入与选项，不做产品决策。字段名建议尽量沿用既有 wire 词汇（`permissionKind`/`permissionActions` 的做法），最终命名以立项评审为准。

### 7.1 wire 统一 plan 数据模型

**建议沿用 §25 已验证的路线：扩展既有 `permission_request` 管道，而非新开事件族**（first-answer-wins、`permission_resolved` 收口、iOS 卡片消费链全部复用；grok 最小档已验证该管道可承载 plan 审批）。在 `EventPermissionRequest`（`go-bridge/events.go:181-203`）上增加可选 plan 载荷：

```jsonc
{
  "type": "permission_request",
  "requestId": "...", "toolName": "...",
  "permissionKind": "plan_review",              // 新枚举值，启用 iOS 计划卡分支
  "permissionActions": ["approve", "requestChanges", "quit"],  // 按 backend 能力裁剪（沿用 iOS wireActions 优先渲染机制）
  "plan": {                                      // 可选；permissionKind=plan_review 时必填
    "content": "<markdown 全文>",
    "contentFormat": "markdown",                 // 现阶段全部 backend 均为 markdown
    "title": "<首个标题行（可选，展示用）>",
    "planFilePath": "<本地路径（可选；claude/opencode 有）>"
  }
}
```

各 backend 的 plan 全文来源（同一模型字段，不同取法）：

| Backend | content 来源 | 备注 |
| --- | --- | --- |
| grok | 请求内 `planContent` | 已在（§25）；空 plan 时 content 为空串 + 占位说明 |
| claude | `ToolInputRaw.input.plan`（+ `planFilePath` 本地兜底） | 数据已在 wire，仅缺抽取 |
| dsh | question `detail` | 待核实转发器可见性（§6.6） |
| codex | `ThreadItem::Plan.text` | 退役前 codex-web 已消费（turn/plan/updated）；现载体 codex-remote 同构可达，需接线 |
| opencode | `.opencode/plans/*.md` 本地读文件 | 需要路径解析（从 plan_exit question 文本/tool part） |

体积现实：grok 生产 1.4–1.6KB、claude 实测 7.5–7.9KB、codex/opencode 无上限——**iOS 渲染必须按 10KB 级 markdown 设计**（截断策略 + 全文入口是 UI 需求项，见 7.3）。

**应答侧**：既有 `resolve_permission` 参数为 `{sessionId, requestId, behavior: "allow"|"deny"(|"always")}`（`go-bridge/types.go:179-183`）。建议增加可选字段而非新 RPC：

```jsonc
{ "sessionId": "...", "requestId": "...",
  "behavior": "allow" | "deny",        // 兼容保留：approve→allow，requestChanges/quit→deny
  "planAction": "approve" | "request_changes" | "quit",   // 可选，语义化动作（Mac 按 backend 翻译）
  "feedback": "<用户反馈文字>"          // 可选，request_changes 时携带
}
```

Mac 侧按 backend 翻译：claude `request_changes→deny+message`、`quit→deny(固定文案)`；grok `request_changes→cancelled+feedback`、`quit→abandoned`；dsh `request_changes→Keep-planning+custom`；opencode `request_changes→question reject`。`core.PermissionResult`（`core/interfaces.go:112-116`）已有 `UpdatedInput`/`Message` 字段，反馈通道有现成挂点，无需改 core 接口形状（或加一个可选 PlanAction 字段）。

### 7.2 统一动作词汇表 + 各 backend 映射

统一词汇：**approve / requestChanges(带反馈) / quit / copy（本地）**；行内评论 comment 与各 backend 特有项单列。

| 统一动作 | grok | claude | dsh | codex（若产品化编排） | opencode |
| --- | --- | --- | --- | --- | --- |
| approve | `{outcome:"approved"}` | control_response allow（模式二选待裁决） | 选 Approve label | 合成 "Implement the plan." + 切 Default（非官方语义） | question reply Yes |
| requestChanges(反馈) | `{outcome:"cancelled", feedback}` | deny + message | Keep planning + custom | 普通用户消息（自由文字） | **无反馈通道**：reject 后另发消息或降级为无反馈 |
| quit | `{outcome:"abandoned"}` | deny（与 requestChanges 同 wire，靠 message 有无区分） | reject（≈dismiss）或 `/plan off`（语义待定） | 切离 Plan mode 或不动 | reject（=No，留在 plan agent） |
| comment（行内） | TUI 本地聚合进 feedback/Interject | 无（deliberately unsupported 建议） | 无（custom 文本即等价） | 无 | 无 |
| copy | iOS 本地 | iOS 本地 | iOS 本地 | iOS 本地 | iOS 本地 |
| 特有 | approve+评论→Interject；casual 评论 | 批准=切权限模式（auto/manual 二选）；Ctrl+G 改计划；conversation_reset 信号 | dismiss「用户接管」第三态；/plan off | clear-context-implement 变体（建议不支持）；wire 类型标 EXPERIMENTAL | 计划=文件可本地改；版本漂移 |

**词汇表结论**：approve/requestChanges/quit 三动作可全 lineup 归一（各 backend 语义都有落点，最弱的是 opencode 的 requestChanges 无原生反馈、codex 的 approve 非官方语义）。comment 仅 grok 有真实交互形态，建议第一批 deliberately unsupported（grok 的等价物是 requestChanges 反馈文本里手写行号引用）。

### 7.3 iOS UI 需求清单（只列需求与选项，设计决策留 owner）

1. **计划全文渲染**：markdown 渲染（标题/列表/代码块），独立可滚动视图（覆盖层或推屏）；是否带行号（grok TUI 有，行号是行内评论的前提——若 comment 不做，行号价值降低）。
2. **长文策略**：默认折叠高度 + 「展开全文」/独立全文页；10KB 级文本的性能与滚动；超大计划的截断上限与提示（当前无官方上限）。
3. **动作栏**：按 `permissionActions` 动态渲染（沿用 wireActions 优先机制）；三键 approve/requestChanges/quit 的主次布局。
4. **反馈输入**：requestChanges 时弹多行输入框（必填或可选——语义上 grok/dsh/claude 均允许空反馈，但空反馈的 requestChanges 与 quit 行为差异需文案区分）。
5. **计划全文第二入口**：claude `planFilePath` / opencode plans 目录的 Mac 本地文件——是否提供「查看文件原始内容」。
6. **行内评论**：仅 grok 有真实语义；做=需要行级选择 UI + 聚合预览（成本高）；不做=统一降级为反馈文本（建议第一批不做，owner 裁决）。
7. **backend 特有语义的表达**：claude 批准的模式二选（auto vs 手动逐批）是否在卡上呈现；dsh dismiss 第三态是否需要按钮或忽略。
8. **与既有权限卡的关系**：新卡片类型（permissionKind=plan_review 分支）vs 扩展现有卡——影响 iOS 改动面与回滚（§25 证明复用权限卡可行，完整档因有全文渲染大概率需要新视图）。

### 7.4 实施风险与开放问题（技术面）

- codex：「审批」为客户端编排非 wire 语义，行为等价性需真机矩阵；wire 类型（`collaborationMode` 字段等）仍标 EXPERIMENTAL，目标部署能力须按部署验证（feature gate 已移除、主线恒启用——v1.1 修订，原「目标部署未启用时不可用」表述不再成立）。
- opencode：stable serve 与 dev 分支 wire 名漂移（实施前必核对目标版本）。
- dsh：plan 投影/question detail 在转发器消费面的可见性未核实（一次小调研可闭合）。
- claude：`allowedPrompts` 已废弃仍在线（忽略即可）；`setMode` 表达批准模式属推断需真机验证；ExitPlanMode 无仓内 fixture（实施时补）。
- 体积：所有 backend 的计划都无官方上限；relay 路径下 10KB 级 payload 需确认 mailbox/帧预算（HPKE envelope 大小）无隐性截断。

---

## 8. 结论与建议

### 8.1 分批建议（供 owner 裁决）

- **第一批（审批门真实、桥通道现成）**：
  1. **grok**——最小档已上线，升级=补 plan 全文字段 + requestChanges/quit 拆分（abandoned 修正）+ 反馈输入；上游锚点最全。
  2. **claude**——can_use_tool 管道已可代答，plan 全文已在 ToolInputRaw；升级=ExitPlanMode 专门化 + deny.message 反馈通道 + iOS 计划卡。
  3. **dsh**——question 管道 + custom 参数原生支持带反馈打回；先闭合一项「转发面可见性」小核实即可定档。
- **第二批（展示先行 / 依赖前置）**：
  4. **codex**——载体基线 = **codex-remote**（v1.1：codex-web 已于 2026-09-04 退役；v1.2：恢复路径不成立，单一载体）。先做 plan 展示（PlanItem/PlanDelta 同协议族同构，退役前 codex-web 已验证消费路径；codex-remote 需自行接线，**且 Remote Control 链路对 plan 事件的实际透传未核实——实施前先验证**）；「审批动作」因非官方 wire 审批语义，等 owner 裁决后再定。codex-remote 连既有三类审批都是 `ErrNotSupported`，若要覆盖须先补 approval 面（独立工作量）。
  5. **opencode**——先按目标 stable 版本核对 wire（版本漂移警示），plan_exit question 桥已能答 Yes/No；requestChanges 无原生反馈是产品语义缺口。

### 8.2 官方无此功能的 backend

无。五个 backend 均存在 plan mode（差异在审批门形态：grok/claude/dsh=真反请求或 question 通道；codex=客户端编排；opencode=question 通道但无反馈）。

### 8.3 待 owner 产品决策清单

1. iOS 计划卡形态：独立计划卡（markdown 全文查看器）还是扩展现有权限卡？（7.3-8）
2. 「拒绝」拆分：requestChanges（带反馈）与 quit 是否都进第一批？（grok/claude/dsh 都能区分；不拆则维持现状语义偏差）
3. 反馈输入必填还是可选？空反馈的 requestChanges 与 quit 的文案区分。
4. claude 批准的模式二选（auto mode vs 手动逐批）：透传（updatedPermissions setMode，需真机验证）还是固定单一行为？
5. codex「approve」是否产品化为合成 "Implement the plan." + 切模式？（非官方 wire 审批语义是唯一硬理由；协议类型标 EXPERIMENTAL、目标部署须按部署验证——feature gate 论据已按 v1.1 修订移除）
6. 行内评论（仅 grok 真实语义）第一批做不做？（建议不做）
7. codex-remote 的 approval 面补齐（现 ErrNotSupported）是否纳入本项或另案？（codex-web 退役后 Codex 计划层的产品载体即 codex-remote。v1.2 备注：退役设计留有回滚开关（drivers 加回 id），但本调研**不把「恢复 codex-web」作为选项**——它不解决审批层缺口（codex 无 wire 审批在两个载体上同样成立），只省 plan 展示的接线工作量，不足以推翻退役裁决；真正该补的核实项是 codex-remote 经 Remote Control 链路的 plan 事件可达性）
8. 计划全文大小上限与 iOS 截断策略（含 relay mailbox 帧预算确认）。
9. dsh quit 的目标语义（dismiss vs /plan off）。
10. opencode stable 版本核对的时机（并入完整档立项或先行小任务）。

### 8.4 调研覆盖度声明

- 已核实：grok 全链（含本次补齐的评论上行 + 两处 §25 修正）、codex 源码级全量、opencode dev 分支源码级全量、dsh 源码级全量（亲验关键路径）、claude 文档级 + 本机真实 transcript 样本 + 我方 adapter 源码。
- 未取得样本：claude plan 专属拒绝文案（仅文档）、bare -p 的 control_request 行为（仅类型注释指向）、交互式 UI 截图（全部 backend，需真机 UI 操作）。
- 未核实（已在各节列出）：dsh 转发面可见性、opencode stable 版本 wire 一致性、codex 目标部署协作模式能力（协议类型标 EXPERIMENTAL，按部署验证）、codex-remote 经 Remote Control 链路的 plan 事件可达性、claude setMode 批准语义。

---

## 附：本次调研执行记录

- 方法：5 路并行子调研（codex / opencode / dsh 上游源码，CordCode 双仓现状锚点，Claude 官方文档与 SDK 类型）+ 主线亲验（grok 关键路径逐文件读、codex/opencode/dsh 承重锚点抽查、claude 本机 transcript 取样、产品运行日志取证、嵌套 claude CLI 探测）。
- 全程只读：两 CordCode 仓零写操作（本文档为唯一新建文件）；上游 checkout 零触碰；iOS 仓仅读指定文件。
- 时间：2026-09-03。
