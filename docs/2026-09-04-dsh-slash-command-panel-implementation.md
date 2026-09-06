# DSH「/」按钮命令面板实施方案（第一期）

- 日期：2026-09-04（v1.6 修订 2026-09-06）
- 性质：**实施方案**。事实基线 =
  [docs/2026-09-04-slash-command-skill-cross-backend-survey.md](2026-09-04-slash-command-skill-cross-backend-survey.md)
  **v1.2 @ `900664f`**（两轮独立评审通过，可全文引用）。本文不另造协议假设。
- 评审：[docs/2026-09-05-dsh-slash-command-panel-implementation-review.md](2026-09-05-dsh-slash-command-panel-implementation-review.md)
  §8 复评：**通过（无条件），可开工**。v1.1 落实全部 P0/P1/P2；D1–D5 **维持、无一推翻**。
- 范围：**仅 DeepSeek Harness（`dsh-web`）host 命令面板**。iPhone 输入框 `/` 按钮 →
  官方 `commands/list` → 点选走 `commands/execute`。含 `/plan`（进计划模式；后续
  plan-review 卡沿用已交付语义）。
- 驱动方式：`/exec-plan docs/2026-09-04-dsh-slash-command-panel-implementation.md start`

## 修订记录

- **v1.0（2026-09-04）**：初稿。
- **v1.1（2026-09-05）**：按评审落实。**P0** Phase 1 增加 codec Class ② 已知丢弃
  （`plan/mode`、`compaction/{start,prune,summary,end}`、`goal/change`、`feedback/record`）
  + 单测——不修则点 `/plan`/`compact`/`goal`/`feedback` 会 RPC 绿灯、后台流 reset。
  **P1** list 请求写死 `{args:{agentId}}`、响应裸数组（3080 活体三发对照收口）；§9
  原「list 包装」风险销项；§0 iOS 钉位 `e6fb7ee`、DSH checkout `d347e70`；D2/矩阵补
  `/export` 成功语义。**P2** 协议 schema 路径前缀、`input.images` 有意丢弃注、Phase 3
  改引 §8。
- **v1.2（2026-09-05）**：复评通过后补实施提醒（非阻断）：官方 list 元素的 `hint` 在
  `input.hint`，`compact`/`export` 无 `input` 键——Go 必须先解到中间 wire 结构再映射
  `SessionCommand`，禁止把官方 JSON 直接反序列化进 core 结构（§5.3）。
- **v1.3（2026-09-05）**：补 §0.1 开工源码优先门——必须先读
  `/Users/jacklee/Projects/deepseek-harness`（tag rc.2）官方 list/execute/事件实现，
  禁止自造命令协议。
- **v1.4（2026-09-05）**：iOS 配套改回功能工作树
  `/Users/jacklee/Projects/cordcode-ios-plan-approval` `plan/approval-layer-ios`
  （与 Mac `plan/approval-layer` 配对，不在 iOS `main` 上改）。
- **v1.5（2026-09-06）**：交付关账后回填实施经验（owner 指令：供后续 Codex / Grok
  Build 做 slash-command 时借鉴）。新增 **§11 实施后记**——三处按 owner 裁决超出第一期
  文本的交付、十轮返工坑与可迁移教训、交互流程四拍设计纪律（goal 全链路走查）、后续
  backend 迁移清单、有意保留的差距；并在 §2/§3.2/§4.3/§6.2/§8/§10 就地标注被推翻或
  修正的第一期决策。**§1-§10 保留为第一期方案原文（历史记录）；实际交付形态以 §11 为准。**
- **v1.6（2026-09-06）**：新增 **§12 二期方案——＋ 菜单合并命令入口**（owner 借鉴
  ChatGPT iOS 截图裁决：`/` 按钮并入 `＋`，底部 sheet 改为锚定 ＋ 的弹出菜单；四项
  细节裁决全部采纳推荐项）。§11.3 四拍走查的**第一拍入口**随之变更，其余三拍与全部
  官方 dispatch 语义不变。
- **v1.7（2026-09-06）**：§12 实施完成（owner「直接实施吧」）。iOS 侧：`SlashCommandPanelSheet.swift`
  退役，纯逻辑迁入 `Views/Chat/SlashCommandRouting.swift`（白名单/点选分流/发送判决
  原样 + 新增 `AttachMenuPlanner`）；输入框 `＋`（展开态与收起态）改 `UIMenu`
  锚定菜单，`/` 按钮、actionSheet、面板宿主路径全删；宿主新增会话级目录预取
  （失败静默回退内置三条）。定向单测 21/21 绿；真机已装机待 owner 走查（§12.6）。
  实施备注：节标题复用既有键 `chat.slashCommand.title`（未新增 §12.4 预告的
  `chat.attachMenu.commands` 键，避免同义双键）。
- **v1.8（2026-09-06）**：owner 真机走查两条反馈落地——① **撤回 M1 节头行**：菜单
  「命令」组与附件组均无标题，靠 UIMenu inline 分组分隔线区分（v1.7 备注的键复用
  随之失效，`chat.slashCommand.title` 仅剩附件拒绝 alert 一个用户）；② **命令发送后
  立即贴底**：`executeHostCommandLine` 补上与普通发送（`proceedSend`）同款的
  `beginTemporaryBottomStick(1.6s) + scheduleRender(.generation)`，不再等首个流式
  事件才滚底（goal/plan 补参发送、compact 点选、Plan chip off 全部覆盖）。单测
  21/21 绿，真机已装机。
- **v1.9（2026-09-06）**：新增 **§13 三期方案——子代理过程可见性**（owner 采纳
  L1+L2 优先级裁决；L3 子会话点进、L4 角色提示词/模式徽章不做）。三部分：L1a
  子代理工具行文案（iOS 渲染词表缺口，根因已查实：数据已到 iOS，descriptor 不识
  `subagent` → generic「任务已完成」降级）；L1b settle 通知行（官方注入父会话的
  `source.kind="subagent-settled"` user message，Mac live/cold 两路均 known-drop，
  本期新增 `context_injection` 投影 part 跨仓贯通）；L2 后台任务中心补真值
  （dsh provider 填 durationMillis/parentTaskId/顶层 rootSessionId + iOS 入口
  计数徽标）。

## 0. 来源清单（P0 门，v1.2 修订时实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=6197da5b8f89a805f7342bba8cd9f2e755c1fbd5
未提交状态=未跟踪本方案 + 评审报告（本修订只改方案文件）
任务预期分支=plan/approval-layer
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-plan-approval
配套分支=plan/approval-layer-ios
配套提交=b94289df3ee41ab5a7ced651140312da13d7685f
  （与 Mac 功能分支配对。默认 iOS 仓 main=`e6fb7ee` 已含本分支合入 + 其后 claudecode
   配套 merge；本项 iOS 改动仍落在本工作树，不合 main 除非 owner 另说。
   ChatInputAccessoryView 等方案锚点在本 HEAD 有效。）
配套未提交=干净
预期产品特性=dsh-web 会话输入框出现 / 按钮；点开列出 host 命令；点 /plan 经
  commands/execute 进入计划模式（非 send_message）；执行后 codec 不因副产物事件 reset
上游 DSH checkout=/Users/jacklee/Projects/deepseek-harness  master  d347e703908d0406b7a7ef80e3a0e594d86b2215
  协议/事件形状仍钉 tag dsh-v0.1.1-rc.2（b150a551b8）；master 前进不影响本项
目标二进制=本机 dsh 0.1.1-rc.2；活体座位 127.0.0.1:3080 pid 1055（与调研/评审同座位）
调研终版=docs/2026-09-04-slash-command-skill-cross-backend-survey.md v1.2 @ 900664f
评审=docs/2026-09-05-dsh-slash-command-panel-implementation-review.md
```

## 0.1 开工源码优先门（不读不许写 CordCode）

实施 agent **第一件事**是读官方 DeepSeek Harness 源码里 **已经存在** 的 list/execute/
事件实现，再在 CordCode 做透传。禁止凭印象、禁止从 Claude/OpenCode/本仓旧 adapter
另造一套命令协议。

1. 仓库：`/Users/jacklee/Projects/deepseek-harness`（只读，不改 checkout）。
2. 目标版本：**本机安装版 0.1.1-rc.2 = tag `dsh-v0.1.1-rc.2`（`b150a551b8`）**。
   当前 `master`（`d347e70`）只作对照；与 rc.2 冲突时以 tag / 装机版为准。
3. 动手改 `agent/dsh-web` / `go-bridge` / iOS **之前**，至少读并在实现说明里写下
   符号锚点：
   - `commands/list`、`commands/execute`（gateway `POST /api/<method>` + `args` 包装）
   - `parseCommand` 与未知命令 `undefined`、不写日志
   - 各命令 handler：`plan` / `compact` / `goal` / `feedback` / `permission` / `export`
   - 执行后 durable 事件：`plan/mode`、`compaction/*`、`goal/change`、`feedback/record`、
     `command/run|done`（`KNOWN_SESSION_EVENT_TYPES`）
4. CordCode 只做 **bridge 接线**：官方 RPC 原样调用、官方 JSON 用中间类型解码再映射。
   本仓 `permission_mode.go` 的 execute 形状是可复用先例，不是协议真值——真值在官方仓。
5. 违反本门（没读官方 call site 就写 list/execute/codec）= 本方案未执行，评审按未通过。

## 1. 目标

iPhone 在 **DeepSeek Harness 会话** 的输入框上出现 `/` 按钮。点开后列出该会话
官方 host 命令（活体 6 条：`compact` / `export` / `feedback` / `goal` /
`permission` / `plan`）。点选后按官方 `commands/execute` 执行，**不是**把
`/plan` 当聊天发出去。

选 `/plan` 的产品结果 = Mac 官方 web 输入 `/plan`：进入计划模式；之后模型要
审批时，已交付的 `permissionKind=plan_review` 卡继续工作，本项不改审批语义。

执行成功后，官方还会往会话日志追加副产物事件（见 §4.7）。这些事件必须进
dsh-web codec **已知丢弃**，否则 iPhone 解码器 reset——这是本项 Phase 1 的
发布阻断工作，不是可选项。

## 2. 明确不做（第一期）

| 项 | 理由 |
| --- | --- |
| DSH 技能（`skill.list` / 发字面 `/name`） | owner 锁定；执行语义与命令互不可替（调研 §3.2）；e2e 仍为未核实 #1 |
| 键入 `/` 弹出补全 | owner 锁定：只要按钮 |
| Claude / OpenCode / Grok / Codex 的 `/` 或技能 | owner 锁定 second |
| 用本面板去开 Claude/Codex/Grok/OpenCode 的 Plan | 调研 §8.2：那些 Plan 不是 host 命令 |
| 续命 `core.CommandProvider` / `SkillProvider` | 零消费死接口（扫本地 `*.md` 目录），与 DSH RPC 无关 |
| 把 `command/run`/`command/done` 或 §4.7 四族事件画进时间线 | 第一期以 execute RPC 回执为准。这些事件 **必须 known-drop**（不 reset），但 **不** surface。`/compact` 后 iPhone 旧时间线不重排（官方靠投影重渲染），第一期验收接受。compaction surface replacement 属更大工程，不塞进本项 |
| 客户端 contribution `/model` | 不在 host `commands/list`（调研 §3.1）；iOS 已有模型选择器 |
| `/plan off` 参数 UI、带消息的 `/plan <text>` | 第一期无参数输入；只执行 `/<name>`。退出计划模式另案（可用 Mac，或二期加参数） |

> **2026-09-06 回填（v1.5）**：本表两行已被 owner 后续裁决推翻——「不 surface 时间线」→
> **时间线持久命令卡 + Plan chip**（§11.1 #1，owner 官方截图裁决）；「第一期无参数框、
> `/plan off` 留二期」→ **官方认领输入框 + Plan chip 点按 = `/plan off`**（§11.1 #2，
> owner 报障「人类没法用」裁决）。表内其余各行维持。

## 3. Owner 决策

### 3.1 已锁定（2026-09-04 对话）

| # | 决策 | 裁决 |
| --- | --- | --- |
| L1 | 第一期范围 | **只做 DSH 命令面板（含 `/plan`），不做 skill** |
| L2 | Claude 目录 / OpenCode `POST /command` / 其他家 | **先不做** |
| L3 | iPhone 交互 | **输入框 `/` 按钮**；键入 `/` 先不做 |

### 3.2 推荐项（评审 2026-09-05 **全部维持**）

| # | 决策点 | **裁决** | 理由 |
| --- | --- | --- | --- |
| D1 | `/permission` 点了之后 | **打开现有 composer 权限菜单**，不新发一条 execute | 写路径已存在：iOS 权限菜单 → `set_permission_mode` → Mac `SetLiveMode` → `commands/execute /permission <preset>`。官方裸 `/permission` 也是弹选择器，等价成立 |
| D2 | `/export` | **留在列表并走 execute**。失败展示官方 `result.text`。成功 = 固定文案 `"Session log download requested."`，**iPhone 本机无产物、静默**；若 Mac web 客户端同时在线，ZIP 下到**那边**的浏览器插件。不做 iPhone ZIP UI | 评审 rc.2 `session-log-export`：成功只表示「已请求下载」，真正 `controller.download` 在 Mac web 观察成功后触发 |
| D3 | 命令参数 | **第一期无参数框**；execute 的 `line` = `/` + `name` | 六条命令裸执行全部无害（`/goal` 裸=查看、`/feedback` 裸=错误可见）。`/plan off` 与 `/plan <message>` 留二期 |
| D4 | capability 名 | **`session_commands`** | 对齐 `permission_mode` 三件套（`backend_capabilities.go` ModeSwitcher 先例）。与死接口 `CommandProvider` 无碰撞。session 域：list 强依赖 `agentId` |
| D5 | 无会话时按钮 | **隐藏** | 网关探针：缺 `args.agentId` 直接拒。无会话无合法 id，隐藏是唯一真话 |

> **2026-09-06 回填（v1.5）**：D2 已被 owner 白名单裁决收窄——iPhone 面板只保留
> compact/goal/plan 三条，export/feedback 不上面板（§11.1 #3）；D3 已被返工⑤推翻——
> 官方 dispatch 对声明 input 的命令是**认领输入框**不是点选即执行（§11.2 ⑤）。
> D1/D4/D5 维持有效。

## 4. 官方不变量（实施必须镜像）

来源：调研 v1.2 §3 + 评审 2026-09-05 活体（3080 pid 1055，只读 list）。目标版 **0.1.1-rc.2**。

1. **List 请求（已验死）**：`POST /api/commands/list`，payload **必须**

   ```json
   { "args": { "agentId": "<sessionId>" } }
   ```

   直传 `{agentId}` → 网关拒 `must contain exactly one plain-object args field`；
   `{args:{sessionId}}` → 拒 `missing "agentId"`。键名是 `agentId` 不是 `sessionId`。

2. **List 响应（已验死）**：业务 value = **裸数组**（无 `{commands:[…]}` 包装），
   恰好 6 条、name 排序：`compact, export, feedback, goal, permission, plan`。
   元素为官方 `CommandDescriptor {name, description, input.hint}`；`plan` 的
   description/hint 与 §5.2 示例逐字一致。另有 `input.images`（活体：goal/plan 为
   true）——第一期结构有意丢弃，不采集不下发。

3. **Execute**：`POST /api/commands/execute`。CordCode **生产形状**已经在跑：

   ```json
   { "args": { "agentId": "<sessionId>", "line": "/permission workspace-write" } }
   ```

   锚点：`agent/dsh-web/permission_mode.go` `commandsExecuteRequest` /
   `applySessionPermission`。新命令只换 `line`（`/plan`、`/compact`…），**不要**改成
   `session.prompt`，也不要加 images。
   （**2026-09-05 返工①修正**：部署座位网关按描述符强制 `images` 必填数组——execute
   载荷必须**无条件携带 `images: []`**，缺键弹官方错误 `missing "images"`；本句「不要加
   images」作废，见 §11.2 ①。「不要改成 `session.prompt`」维持。）

4. **未知命令**：官方 handler 返回 `undefined`、不写日志。CordCode 必须把
   「commandId 与 result.kind 都空」当成失败（permission 路径 `:133-138` 已如此）。

5. **解析**：`/^\/([a-z][a-z0-9_-]*)(?=$|[\t\n\r ])/u`（`/plan` 不会误配 `/plan/path`）。
   第一期不发带路径的 line。

6. **命令不是 user message**。把 `/plan` 当 `send_message` 就是当前 iPhone 进不了
   Plan 的那个 bug。测试必须覆盖「execute ≠ send_message」。

7. **执行后的会话日志副产物（P0）**：`command/run` + `command/done` 已在 codec
   Class ②。此外 4 条命令还会追加 **当前 codec 不认识**、且 **不带 ignorable** 的
   durable 事件（mux `session/event` raw passthrough 必达）：

   | 命令 | 追加事件 |
   | --- | --- |
   | `/plan` | `plan/mode` |
   | `/compact` | `compaction/start`、`compaction/prune`、`compaction/summary`、`compaction/end` |
   | `/goal` | `goal/change` |
   | `/feedback` | `feedback/record` |

   官方 `KNOWN_SESSION_EVENT_TYPES`（rc.2 `core/session/src/known-event-types.ts`）全收录。
   dsh-web codec default = `resetf("unknown required event type")`（`codec.go:233`）。
   `ignorable` 旁路只救「更新版本 harness 写的事件」，这四族是本版本注册表内事件。
   有 open turn 时 `/plan` 走 pendingIntent、在 in-turn 边界落 `plan/mode`，即
   2026-08-16「杀 mid-turn 码器」同类故障。

   **修法（Phase 1 发布阻断）**：把上表七个 type 加进 Class ② 已知丢弃（先例：
   `approval/asked|decided` 同因入清单）+ 单测：fake 流喂这些类型 → 不 reset、
   不产 timeline 事件。

## 5. Wire 设计

对齐 `permission_mode` 三件套，不手写进 `WireDescriptor.StaticCapabilities`。

### 5.1 core 接口（新，不要改 CommandProvider）

```go
type SessionCommand struct {
    Name        string // 不含前导 '/'，如 "plan"
    Description string
    Hint        string // 从官方 input.hint 映射而来；无 input 时为空串
    // 官方 descriptor 另有 input.images（活体 goal/plan=true）。第一期有意丢弃。
}
// 官方 list 元素 ≠ 本结构：hint/images 在嵌套 input 下，且 compact/export 没有 input 键。
// 解码必须走中间 wire 类型（Input 用指针或 omitempty），再映射到这里。

type SessionCommandCatalog interface {
    ListSessionCommands(ctx context.Context, sessionID string) ([]SessionCommand, error)
    ExecuteSessionCommand(ctx context.Context, sessionID, line string) error
}
```

`dsh-web.Agent` 实现该接口。`deriveBackendCapabilities`：类型断言成功则追加
`session_commands`（先例：`ModeSwitcher` → `permission_mode`，
`backend_capabilities.go:43-45`）。其他 backend 不实现 → 不广告 → iOS 不画按钮。

`line` 必须已是官方 slash 行（以 `/` 开头）。Handler 拒绝空、拒绝不以 `/` 开头、
拒绝内嵌换行。第一期 iOS 只拼 `/`+name。

### 5.2 bridge RPC（增量，旧客户端不受影响）

`list_session_commands`

```jsonc
// request
{ "sessionId": "<dsh session id>" }
// result（从官方裸数组映射而来，bridge 自有包装）
{
  "commands": [
    { "name": "compact", "description": "...", "hint": "" },
    { "name": "plan", "description": "Enter or leave plan mode", "hint": "[off|message]" }
  ]
}
```

无接口 → `not_supported`。sessionId 空 → `invalid_params`。

`execute_session_command`

```jsonc
// request
{ "sessionId": "...", "line": "/plan" }
// result
{ "ok": true }
```

官方 `result.kind=="error"` 或未命中命令 → RPC 失败，message 用官方 `result.text`
或固定「command not matched」。成功不合成 user 消息、不走 projection 文本。

协议同步：`docs/protocol/bridge-v1.md` 方法表、
`docs/protocol/schema/bridge-v1.types.ts` 的 `BridgeRPCMethod`、iOS mirror
（仓根 `docs/protocol/bridge-v1.md`）。capability 名 `session_commands` 写入
hello_ack backends[].capabilities 说明（一句：session 域命令目录+执行，现仅 dsh-web）。

### 5.3 dsh-web 实现要点

- List：`client.Call(ctx, "commands/list", commandsListRequest{Args:{AgentID: sessionID}}, &out)`，
  `out` 解 **官方裸数组**（中间类型，不是 `[]SessionCommand`）。再映射：`Name`/`Description`
  顶层；`Hint` ← `input.hint`（`input` 缺席如 compact/export → `Hint=""`）；丢弃
  `input.images`。空 name 丢弃。不合并技能、不注入 `/model`。
  **禁止**把官方 JSON 直接 `json.Unmarshal` 进 `SessionCommand`（顶层没有 hint，缺 input
  的两条会解失败或静默丢字段）。§5.3 单测「裸数组映射 6 条」必须覆盖 compact/export
  （无 input）和 plan（有 hint）。
- Execute：复用 `commandsExecuteRequest` / `commandsExecuteValue`（可从
  `permission_mode.go` 抽到 `commands.go`，`applySessionPermission` 改为调
  `ExecuteSessionCommand(ctx, sid, "/permission "+mode)`，行为零变化）。
- **Codec（P0）**：Class ② 追加 `plan/mode`、`compaction/start`、`compaction/prune`、
  `compaction/summary`、`compaction/end`、`goal/change`、`feedback/record`。
  `command/run`/`command/done` 已在清单，保持。第一期仍不把这些事件画进时间线。
- 单测：
  - fake Call：list 方法名 + payload `{args:{agentId}}`；裸数组映射 6 条（compact/export 无 input → Hint 空；plan 有 input.hint）。
  - execute `line` 原样；`/plan` 不得打到 `session.prompt`。
  - permission 回归：`SetLiveMode` 仍发 `/permission <preset>`。
  - codec：上列七 type 各喂一帧 → 不 reset、Events 空。

## 6. iOS

仅 `BackendKind.deepSeekWeb` 且 hello 含 `session_commands` 且当前有 `sessionId`。

锚点（`e6fb7ee`，与 v1.0 相对文件零改动）：`ChatInputAccessoryView.swift` compactRow /
leftToolbar（attachButton 旁）；capability 消费先例
`CCCodeBridgeBackendClient.swift` `capabilities.contains("permission_mode")`；
`set_permission_mode` 在 `CCCodeBridgeClient.swift`。

### 6.1 按钮

展开态 `leftToolbar`：附件按钮旁加 `/` 按钮（系统符号 `/` 或
`chevron.left.forwardslash.chevron.right`，accessibility `chat.input.slashCommand`）。
收起态 `compactRow` 同步一枚。

非 DSH / 无 capability / 无 session → 隐藏，不占位。

### 6.2 列表

点按钮 → `list_session_commands` → 列表：主标题 `name`（展示可带 `/` 前缀），
副标题 `description`。失败可见（官方错误原文）。

点选：

| name | 行为 |
| --- | --- |
| `permission` | 关掉命令列表，打开**现有**权限模式菜单（D1） |
| 其他 | `execute_session_command` `{line:"/"+name}` |

不把 line 写入输入框，不走 `send_message`。

> **2026-09-06 回填（v1.5）**：本表与上一句已被返工⑤/⑩修正——点选走官方三路分流：
> permission → 菜单（维持）；**声明 input（goal/plan）→ 认领输入框** `/name ` + 幽灵提示，
> 补参数后回车判决；**无 input（compact）→ 先收面板再立即 execute**；白名单收窄为
> compact/goal/plan。见 §11.1 #2/#3 与 §11.2 ⑤⑩。「不走 send_message」维持。

### 6.3 执行后

- `/plan`：等待官方 `plan/mode`（codec known-drop）与后续 plan-review（已有卡）。
  不要本地假「已在计划模式」徽章。
- `/compact` / `/goal` / `/feedback`：以 RPC 成功/失败为准；副产物事件 known-drop，
  时间线不因它们重排。
- `/export` 成功：iPhone 静默（无文件）。不要当成失败。Mac web 若开着，ZIP 可能出现在 Mac。
- 执行中可 disable 按钮防连点，完成后恢复。

### 6.4 测试

- capability 缺席 → 无按钮（非 dsh 回归）。
- 列表解码 6 条；点 `plan` 发出 `line="/plan"` 且 **0 次** `send_message`。
- 点 `permission` 不发 execute，走权限菜单入口。

## 7. 分期落地

| Phase | 内容 | 验收 |
| --- | --- | --- |
| 1 Mac 接口+RPC+**codec P0** | **先完成 §0.1 读官方源码并记下锚点**，再写 `SessionCommandCatalog`、derive `session_commands`、dsh-web list/execute、handlers、permission 回归、**Class ② 七事件 + 单测** | 实现说明含官方仓路径+tag+符号；`go test ./agent/dsh-web ./go-bridge` 定向；descriptor 仅 dsh-web 有该 cap；codec 未知事件测试覆盖 §4.7 |
| 2 协议 | canonical pack + iOS types mirror | `BridgeRPCMethod` 含两方法；旧 hello 无 cap 行为不变 |
| 3 iOS 按钮 | accessory `/`、列表、execute、permission 分流 | 定向单测；有真机时交付前装一次（§8 矩阵，非全量 UI test） |
| 4 收尾 | 双仓 CHANGELOG；Release 覆盖安装 Mac；owner 真机 | 见 §8 |

禁止每改一小点就全量 build。Mac 改 agent/go-bridge 后按仓库纪律 Release 装 `/Applications`（killall 主 app **和** `cordcode-bridge-runtime`）。

## 8. Owner 真机矩阵（产品语言）

环境：本机 dsh 0.1.1-rc.2 座位；iPhone 连已覆盖安装的 CordCode Link；DeepSeek Harness 会话。

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | 打开 DSH 会话 | 输入框出现 `/`；Claude/Codex/Grok/OpenCode 会话不出现 |
| 2 | 点 `/` | 6 条命令（可含中英描述），无技能组、无 `model` |
| 3 | 点 `plan` | 不在输入框留下 `/plan`；不出现「模型把 /plan 当聊天」；随后按 Mac 官方那样进入计划；**会话继续流式/可发下一条，不出现解码器重置/整段重来** |
| 4 | 在计划模式下让模型产出计划 | iPhone 仍出现已交付的计划审批卡，批准仍能执行 |
| 5 | 再点 `/` → `permission` | 出现现有 Read Only / Workspace Write / Full access 菜单，不是再发一条聊天 |
| 6 | 点 `compact` 或 `goal` | RPC 成功或官方错误可见；不是一条用户气泡；**同样不 reset 解码器** |
| 7 | 点 `export` | RPC 成功时 iPhone 没有文件、没有失败提示（静默）。若 Mac 上 DSH web 开着，ZIP 可能下到 Mac 浏览器 |

真机点击仍须 owner；agent 只做日志/Management 核验。矩阵 #3/#6 的「不 reset」是 P0 的验收，不能只看 RPC 绿灯。

> **2026-09-06 回填（v1.5）**：实际验收远超本表——owner 真机十轮返工（命令卡/Plan chip、
> goal 横条、认领输入框、goal 轮直播、气泡驻留、冷拉交错序、/compact 超时、面板点选即收
> 置灰），终版矩阵与逐轮证据链见
> [2026-09-05-dsh-slash-command-panel-owner-matrix.md](2026-09-05-dsh-slash-command-panel-owner-matrix.md)
> §1/§2.2-§2.13，全部于 2026-09-06 关账（§11.0）。

## 9. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| ~~list payload 包装与 execute 不一致~~ | **已销项（评审活体）**：list 与 execute 同为 `{args:{…}}`，list 键 `agentId` |
| 执行后未知事件 reset 码器（P0） | Phase 1 扩 Class ②；单测；矩阵 #3/#6 查「不 reset」 |
| `/plan` 在有 open turn 时进 pendingIntent（调研 §3.2） | 产品接受：与官方 web 相同，不在第一期做「立即生效」假状态 |
| `/export` iPhone 无文件 | D2：成功即静默；矩阵 #7。不造下载器 |
| 双客户端 Mac web 同时开 | `command/run\|done` 走 session/follow；第一期 iOS 不渲染 flow node。`/export` ZIP 可能落到 Mac web |
| 误用 CommandProvider | 代码评审门：新接口名禁止叫 CommandProvider；capability 测试断言 grok/claude/codex-remote/opencode-web **无** `session_commands` |

回滚：dsh-web 去掉接口实现 → cap 消失 → iOS 隐藏按钮。RPC 对旧客户端是未知方法，按既有 not_supported。codec Class ② 扩容即使回滚面板也应保留（防未来任何路径触发这些事件）。

## 10. 与后续案的边界

- 本项关闭 think.md「DSH iOS 不能开 Plan」中 **命令入口** 这一半。
- 不关闭：技能面板、键入 `/` 自动补全弹层（发送侧 `/`-整行判决已随返工⑤交付，见 §11.2 ⑤）、Claude 目录、OpenCode `POST /command`、Grok `set_mode`、Codex collaborationMode。
  `/plan off` UI 已由 Plan chip **超出第一期文本交付**（§11.1 #1/#2）。
- 计划审批卡文档与代码本项零改动（除 dsh-web codec 已知清单，避免 `/plan` 把码器打死）。

## 11. 实施后记（2026-09-06 关账回填，后续 slash-command 实现必读）

本方案已全量交付关账：执行队列 49/49 proven
（`.exec-plan/state/plan-4112d14c1d2b.json`）；完成情况见
[2026-09-06-dsh-slash-command-panel-implementation完成情况.md](2026-09-06-dsh-slash-command-panel-implementation完成情况.md)；
十轮 owner 真机返工的逐轮证据链见
[2026-09-05-dsh-slash-command-panel-owner-matrix.md](2026-09-05-dsh-slash-command-panel-owner-matrix.md)
§2.2-§2.13，2026-09-06 全部关账。官方源码真值：`/Users/jacklee/Projects/deepseek-harness`
@dsh-v0.1.3-alpha.1（`d347e70`），关键锚点 `packages/client/ui-commands/src/client/service.ts`
（dispatch/matchEnter/runDetached）、`ui-chat`（GenericCommandCard、message.ts、partial.ts）、
`ui-goal`（GoalBar、goal-command-input）、`ui-plan`（PlanModeControl）。

**后续 Codex / Grok Build（或任何 backend）做 slash-command 面板时，先读完本节再动手；
§1-§10 的第一期决策多处已被推翻，不要照抄。**

### 11.1 三处按 owner 裁决超出第一期文本（都有档可查，不是漏做）

| # | 第一期文本（原文位置） | 实际交付（owner 裁决） | 证据 |
| --- | --- | --- | --- |
| 1 | §2「把 `command/run\|done` 或四族事件画进时间线」明确不做（只 known-drop 不 surface） | **时间线持久命令卡 + Plan chip**：codec 解禁 `command/run\|done` 折叠 CommandRow 进投影 system turn part（词表 running/success/error，未知 kind fail-closed）；`planMode{active,pending}` 进快照/patch；输入框 Plan × 橙色 chip 点按 = `/plan off`（官方 off 公式，失败 inline 非弹窗）；占位符随 plan target 切换；删除「命令已执行」toast | 矩阵 §2.5（owner 提供官方截图裁决，2026-09-05 20:04 上线） |
| 2 | §2/D3「第一期无参数框，点选一律 execute `line="/"+name`」；`/plan off` 留二期 | **官方认领输入框（claim composer）**：声明 input 的命令（goal/plan）点选 → `/name ` 写入输入框（token 带尾随空格、原位替换、光标其后、拉起键盘）+ 官方幽灵提示（词典译文 > goal.active 变体 > 机器 hint），人补参数后回车；发送时按官方 matchEnter 判决整行 execute vs 普通发送；挂附件被拒且草稿附件保留。裸 `/goal` 在官方是 show（只回 usage）——旧交互把用户放进这条死胡同 | 矩阵 §2.8（owner 报障「人类没法用」裁决，2026-09-05 23:13 上线） |
| 3 | §1 六条全列 + D2「export 留在列表并走 execute」 | **面板白名单收窄为 compact/goal/plan 三条**：permission 走既有权限菜单（D1 维持）；export/feedback 不属于 iPhone 面，不再出现在面板 | 矩阵 §2.5（owner 同轮裁决，2026-09-05） |

### 11.2 十轮返工坑与可迁移教训

每轮的完整三层证据链（座位 journal 真值层 → 投影重建层 → 代码层）见矩阵 §2.2-§2.13；此处只留根因与教训。

| 轮 | 现象（owner 报障） | 根因 | 修复 | 可迁移教训 |
| --- | --- | --- | --- | --- |
| ① execute 载荷缺 images | 点任意命令弹官方网关错 `missing "images"` | execute 只发 `{agentId,line}`；部署座位网关按描述符强制 `images` 必填数组（rc.2 fixture 允许省略、官方 HEAD 已改名 `submittedAttachments`——校验随版本漂移） | `images: []` 无条件携带（活体三发探测收口：缺→拒、`[]`→成功、`null`→拒）+ 单测锁形 | **请求形状以活体探测收口**：descriptor/网关校验跨版本漂移，fixture 只是声明契约；空数组 = 官方「无附件」语义，是唯一双代兼容形 |
| ② 执行成功无反馈 | 「6 条命令点击任意一条都没反应」（RPC 与官方执行其实都成功） | iOS 成功路径只收面板；官方 settle `result{kind,text}` 被丢弃；副产物事件按方案 known-drop——「成功」与「没反应」在手机上无法区分。**方案没设计成功反馈是设计缺口，owner 批评成立** | settle 透传：core `SessionCommandResult` + wire 可选 `commandId/resultKind/resultText`（零值省键，additive） | **execute 的返回值就是官方反馈面**，别折叠成 `{ok:true}`；成功必须有用户可见出口 |
| ③ 反馈形态各说各话 | toast 与官方 Web 形态不对齐 | 官方反馈形态是**时间线持久命令卡**（GenericCommandCard：running/ok/error 状态机、标题裸命令名、摘要 = settle 原文逐字、多行可展开、error 红点）+ **Plan chip**（target 公式、off 失败 inline），不是 toast | 见 §11.1 #1（codec 解禁 → CommandRow；planMode 快照；UIKit chip + 占位符） | 反馈形态**照抄官方 UI 源码**（卡片状态机、chip 公式、失败文案），不自造 toast/弹窗；「known-drop 不 surface」只是防 reset 的底线，不是反馈设计 |
| ④ goal 横条/subagent 缺失 | Mac web 输入框上方有「进行中的目标」横条 + subagent 显示，iPhone 都没有 | goal 是**投影 whole-snapshot 会话级状态**（`goal/change` 不是时间线行），横条是独立 UI 面（GoalBar：四短路渲染 null、pause/resume/edit+clear、行内 `${message} (${code})` 错误、clearedGoalId 抑制、goalId 变化复位） | `mutate_session_goal` RPC + `session_goal` capability + GoalBannerView 官方镜像；subagent tool 行标题带 description | 命令副产物的呈现面不止时间线一种；官方每类投影（goal/plan/…）各有独立 UI 组件语义，逐组件镜像，不合并进时间线 |
| ⑤ 点选即裸发 | 点 goal 直接发出裸 `/goal`，只回官方 usage 文案，「人类没法用」 | **方案 D3 是自造交互**：官方交互判决表早写在 ui-commands 源码里——声明 `input` → `leadingClaim` **认领输入框**（不执行）；无 `input` → `runDetached` 立即执行。源码门当时只读了 list/execute/handler/事件注册表，**没读 `dispatch()`/`matchEnter()`** | 点选三路分流（permission 菜单 / claim / execute）；发送判决 matchEnter 镜像（带 input 整行执行 args-tolerant / 无 input 裸 token 执行 / 带参数与未知命令 → 普通发送 / 附件 refuseAttachments 保留草稿）；失败整行草稿还原 | **源码门必须读到 UI 交互层**：交互判决表在 UI 源码（service.ts dispatch/matchEnter）不在 RPC schema 里；「点选 = 执行」是自造，官方是「点选 = 认领或分离执行」 |
| ⑥ goal 轮 codec reset | goal 任务输出被拆流、命令在时间线完全不可见、页面持续跳动 | 三类自造严格性全中：未知 `source.kind:"goal"` 注入帧 reset（官方：kind≠user 全进 ContextMessageNode 合并，从不拆流）；`block-end` 归一化紧凑 JSON 与 delta 累积比对必分歧（官方：整值替换零校验）；settlement 与 delta 并存比对（官方：supersedes，从不比对）；基线 `agent-instructions`/`skill-catalog` 每次回放触发 reset。跳动 = reset → re-adopt → 整 turn upsert → 全行重排 | codec 全部改官方语义（known-fold / 整值替换 / 删 delta 比对死状态）；goal 四件套补齐（横条 + `/goal …` 右对齐用户气泡 `goalCommandText` `anchorSeq: seq-0.1` + 命令卡 + 上下文注入行〔未做，见 §11.5〕）；`inputLine` 在 run/settle 双携带；fixture = 真实 journal 三窗口 | **不要自造 delta/settle 比对**：官方流是「部分 → 整值替换」模型，比对只会制造假分歧；未知事件族先 known-fold 再逐族裁决 surface；跳动类症状先数 reset 计数；测试 fixture 必须来自真实 journal |
| ⑦ 指令卡流式期间消失 | goal 气泡上屏 1 秒即消失，任务完成才归位 | iOS 流式渲染优化（增量只重算尾段保帧率）把时间线切成「最后 user 前缀 + assistant 尾组」；命令卡是 **system 行**，落在「最后 user 之后、流式 assistant 之前」——两段都不含，整组被丢弃 | 尾段分组改逐消息建组：非 assistant 行（指令卡等 system 行）按全量 builder 同语义各自成组保留，assistant run 仍合并单个流式尾组（保住优化） | 移动端渲染优化必须审计**非 assistant 行**（system/tool 行）的分组路径；「只重算尾段」不得改变全量分组的可见集合 |
| ⑧ 冷拉交错序错乱 | 重开 goal 会话：多轮输出首尾相接成一条消息、命令卡聚簇在后 | 冷拉沿用 OpenCode 平坦折叠（`currentTurn` 只被 **user 行**设置）；goal 轮**没有 user 行** → 全部输出折进最后一个 user turn；命令行另成 `cmd:<id>` turn 追加其后。真值层 journal 交错序本来就对——错在重建层 | 冷热身份同源：turn 号取官方 `turn/start {"turn":N}`，冷/热共用 `dshwTurnID`/`dshwSessionPrefix`；**斜杠命令行是官方 turn 边界**——converter 发射 session_command 后重置平坦归属指针；plan/goal 快照是会话级状态，不构成边界、不重置 | 冷重建与 live 事件必须在**同一身份公式**上合并；无 user 行的轮次（goal/subagent/未来 agent 侧动作）靠 user 行归属的折叠必炸；排障先对齐三层证据（journal 真值 → 重建 → 代码），别先怀疑渲染 |
| ⑨ /compact 30 秒整报错 | 30s 整弹 `Client.Timeout exceeded` + 官方命令卡 `This operation was aborted` | 三层超时互相矛盾：wire 默认 `http.Client{Timeout:30s}` 是**硬顶**（Go 语义：per-request ctx 只能缩短不能延长）→ handler 90s 预算从未生效；iOS RPC 默认 30s 又先于任何 Mac 预算触发。官方 execute 无人为上限，取消信号归派发请求所有；`/goal` 秒回（轮次异步流式）故 30s 无感，`/compact` 同步等一次 LLM 摘要（分钟级）必炸 | 三处协同：默认 client 撤 blanket timeout + `unaryCtx` ctx 感知兜底；handler 预算 300s；iOS `requestTimeoutNanoseconds(for:)` 对 `execute_session_command` 同 300s。tripwire 单测锁「调用方期限赢过默认兜底」+ 无兜底负向验证 | **长命令超时预算要三层对齐**（HTTP client / bridge handler / 客户端 RPC 超时表），任一缺失即复发；`http.Client.Timeout` 是硬顶语义；同步长命令与秒回命令分开估预算 |
| ⑩ 面板点选不收、无置灰 | 点 compact 面板不收起，用户误以为点击无效而重复点击 | 官方 dispatch 点选即收菜单（consumeVia）+ 分离执行（runDetached 不等结算，失败落 composer inline）；官方菜单**没有**禁用态（并发同名命令靠服务器 busy 拒绝）；CordCode 面板滞留 + 全局 Bool 锁 | 点选**先收面板再发 RPC**；按命令名置灰 + 「执行中…」标注（owner 要求的移动端加码）；同名单飞、异名不互斥 | 面板 UX 镜像官方：点选即收、分离执行、失败 inline 不绑菜单生命周期；置灰这类移动端加码按**命令名粒度**做，别用全局锁把不同命令互斥死 |

### 11.3 交互流程设计纪律（owner 2026-09-06 明确要求写入）

> 「goal 功能的设计要走整个交互流程设计」——**一个命令不是一条 RPC，是一整段交互**。
> 对齐 CLAUDE.md《方案必须先写清交互流程》四拍：**打开输入的地方 / 输入 / 发出去 /
> 把过程展示出来**。四拍顺序不能乱；分期只能切明确标为不做的支线，不能把「输入」与
> 「过程展示」裁成二期。

以 goal 为完整走查（即第一期方案缺课、返工⑤⑥⑦⑧补齐后的真实形态）：

1. **打开输入的地方**：输入框 `/` 按钮（`session_commands` capability + 有 session 门控）
   → 命令面板 → 点 goal 行。面板只是入口，不是终点。
   （**2026-09-06 二期裁决**：此拍入口合并进 `＋` 弹出菜单，见 §12；其余三拍不变。）
2. **输入**：goal 声明 input → **认领输入框**：`/goal ` 前缀原位替换（连续换选不叠加）
   + 官方幽灵提示（目标进行中另有 active 变体文案），键盘拉起，人补目标文本；挂附件被
   官方语义拒绝且草稿附件保留。**裸命令是死胡同**（官方只回 usage）——第一期「点选即
   裸发」在这一拍上整个缺失，真机即报「人类没法用」。
3. **发出去**：回车经 matchEnter 判决 → `execute_session_command` → 官方
   `commands/execute`（载荷 `images:[]`），**绝不走 send_message**（命令不是 user
   message）；执行失败整行草稿还原，人还在输入框。
4. **把过程展示出来**：时间线 goal 专属 `/goal …` 右对齐气泡 + 命令卡
   （running→ok/error，settle 原文，流式期间驻留不消失）+ goal 横条
   （phase / pause / resume / edit / clear，行内错误）+ goal 轮流式直播；冷重开后按同一
   turn 身份交错重建（命令 N 在其输出 N 之前）。失败时命令卡红字 + 输入框还原，人不丢
   上下文。

**教训**：第一期方案把 goal 写成「点选 → execute `/goal`」一行字——第 2 拍整个缺失、
第 4 拍只写了「以 RPC 成功/失败为准」。这套走查在方案评审时本可拦住 D3。后续任何
backend 的命令面板方案，**评审前必须把每条命令走完这四拍**（官方已有 UI 的对照官方走，
不另造弹窗或点选即执行）；写不出人怎么走完，评审不得通过。

### 11.4 迁移清单（给下一个 Codex / Grok Build slash-command 实现）

开工前逐项核完再写代码：

1. **源码门读到 UI 层**：list/execute RPC 与事件注册表之外，必须读官方 dispatch()/
   发送判决/菜单与卡片组件——交互判决表在 UI 源码里，不在 RPC schema 里（⑤）。
2. **每条命令走四拍走查**（§11.3），官方已有 UI 的对照官方走查，不另造弹窗或点选即
   执行（⑤⑩）。
3. **反馈闭环按官方形态**：execute settle 透传 + 时间线命令卡 + 模式 chip；「成功」必须
   用户可见（②③）。
4. **请求形状以活体探测收口**：网关/descriptor 校验跨版本漂移；可选-for-humans 的键用
   空值显式携带 + 单测锁形（①）。
5. **事件面按官方投影语义**：部分→整值替换零校验、settlement supersedes partial、
   kind≠user 合并进 context 节点——不自造 delta 比对；未知官方事件族 known-fold 不
   reset，但「不 reset」只是底线，官方有可见形态的事件族要逐族裁决 surface 与否（③⑥）。
6. **身份冷热同源**：turn id 公式一处定义、冷拉与 live 共用；命令 = turn 边界；无 user
   行的轮次不能靠 user 行归属的平坦折叠（⑧）。
7. **超时预算三层对齐**：HTTP client（无 blanket 硬顶）/ bridge handler / 客户端 RPC
   超时表；同步长命令（压缩类 = 等 LLM）与秒回命令（轮次类 = 异步流式）分开估（⑨）。
8. **移动端渲染优化审计非 assistant 行**：流式增量分组不得裁掉 system/tool 行（⑦）。
9. **测试 fixture 用真实样本**：事件/投影形状来自真实 journal 或官方 fixture，手写
   fake 只验内部行为、不能反向证明协议形状（⑥⑧；亦见 CLAUDE.md 外部 Web/API 附加纪律）。
10. **每轮返工记档 owner 矩阵**：现象 → 三层证据链（journal 真值层 / 重建层 / 代码层）
    → 修复 → 回归要点；官方错误文案原文透传不转写（wire.go 坑 7 红线）。

### 11.5 有意保留的差距（recorded，非欠账）

- **ContextInjectionRow「上下文注入 · goal」披露行未实现**（官方 goal 四件套之四；官方
  锚点 message.ts + ContextInjectionRow.tsx 已记档，待 owner 裁决，矩阵 §2.9）。
- **面板置灰/「执行中…」是移动端加码**：官方菜单无禁用态，并发同名命令靠服务器 busy
  拒绝兜底；App 冷启动期间座位侧在跑的命令面板不感知（重开面板该行不置灰）。
- **/compact 300s 是人为上限**：官方无人为上限（取消信号归派发请求所有）；超大会话
  压缩超 300s 仍会超时报错（预期罕见）。
- **Lexical `/goal ` token 警告色不复刻**：owner 裁决只锁行为不变量「点 goal 之后人还
  能打字、发送的是整行 `/goal …`」，该不变量已由测试锁定。
- **export/feedback 不在 iPhone 面板**（§11.1 #3 白名单裁决）；D2 的「export 成功静默」
  语义随之在 iPhone 面不再可达。

## 12. 二期方案：＋ 菜单合并命令入口（2026-09-06 owner 裁决，v1.6）

### 12.0 背景与裁决

owner 提供ChatGPT iOS 截图
（`/Users/jacklee/Downloads/picture/2026-09-06 11.22.18.jpg`，读图记录于当日会话）：
点击输入框 `＋` 后弹出**悬浮圆角卡片菜单**（从 ＋ 上方弹出，约 69% 屏宽、无全屏遮罩、
无指向箭头），单列行 = 图标 + 短标题；第一组为 方案模式 / 追求目标 / 文件 / 相机 / 照片，
其后以灰色小字「插件」为节标题接插件列表。

现状对照（iOS 工作树代码核对，2026-09-06）：输入栏左侧 `＋`（attachButton，`plus`
图标）与 `</>`（slashCommandButton）**两枚按钮**；`＋` 点击走 `handleAttach()` 的
**UIAlertController actionSheet（底部弹出）**图片/文件，`/` 点击拉取目录后 present
**SwiftUI sheet（底部弹出）**——两个入口、两种底部弹层。owner 裁决：**合并成一枚 `＋`，
命令入口并入 `＋` 弹出菜单，菜单从 ＋ 处弹出（ChatGPT 式），删除 `/` 按钮。**

四项细节裁决（owner 2026-09-06 全部采纳推荐）：

| # | 决策点 | 裁决 |
| --- | --- | --- |
| M1 | 命令组节标题 | 加「命令」节标题与附件区分（ChatGPT 未分节，但那三条在它那边是输入侧功能；我们是 host 会话命令，语义不同）。**v1.8 撤回：owner 真机走查后裁决去掉头行，两组仅靠分隔线区分** |
| M2 | 行内容 | **图标 + 本地化短描述一行**（压缩会话 / 设定目标 / 计划模式），无 hint、无 description、无 footer；`/name` token 由认领流程自然出现在输入框 |
| M3 | 菜单数据 | **白名单三条静态构建 + 会话就绪时预取缓存**（hint/描述用官方缓存），缓存缺失/拉取失败用内置回退，**入口永不因网络失败被阻塞**（现状 sheet 失败弹错取消） |
| M4 | `/` 按钮 | **彻底删除**（展开态 slashCommandButton + 收起态 compactSlashButton）；`chat.input.slashCommand` 无障碍标识随按钮退役，菜单行为系统渲染 |

范围：**纯 iOS 单仓**（`/Users/jacklee/Projects/cordcode-ios-plan-approval`，
`plan/approval-layer-ios`）；Mac 与协议**零改动**。红线：§11 的全部官方 dispatch 语义
（route 三分流 / matchEnter 判决 / claim 输入框 / 同名单飞 / settle 命令卡）不变，
只换「打开输入的地方」这一拍。

### 12.1 交互流程四拍走查（CLAUDE.md 规则）

- **打开输入的地方**：输入框 `＋`（全后端可见）→ 锚定 ＋ 的弹出菜单；dsh-web +
  `session_commands` + 有 session 时菜单上部出现命令组（三条），否则只有 照片/文件；
  两组均无节标题（v1.8 撤回头行）。
- **输入**：压缩会话 → 无输入拍（立即执行）；设定目标 / 计划模式 → 菜单自动收起 →
  **认领输入框**（`/goal ` + 幽灵提示，返工⑤语义原样）；挂附件时选命令不提前拦，
  发送时按官方 `refuseAttachments` 拒绝并保留草稿（现状行为）。
- **发出去**：菜单点选自动收起（= 返工⑩「点选即收」，系统菜单天然满足）→
  compact 走 `execute_session_command` 立即执行；goal/plan 补参数回车经
  `SlashCommandLineAdjudicator` 判决；**绝不走 send_message**。
- **把过程展示出来**：时间线命令卡（running→ok/error，settle 原文）、goal 横条、
  Plan chip、流式、冷重建交错序——全部第一期交付原样；执行中的命令行在重开 ＋ 菜单时
  置灰 +「执行中…」（菜单打开时快照，与返工⑩ sheet 重开语义一致）。

### 12.2 UI 设计

实现取 **系统 UIMenu**（`attachButton.showsMenuAsPrimaryAction = true`；自定义非自适应
popover 不做——iPhone 上尺寸/收起/无障碍细节坑多，收益仅剩 footer 展位）。iOS 26
deployment 下 subtitle / inline 分节全可用。菜单结构：

```text
┌──────────────────────┐
│ 命令                    │  ← 节标题（UIMenu displayInline + title）
│  ▦  压缩会话            │  rectangle.compress.vertical（备选 archivebox，实现评审定稿）
│  ◎  设定目标            │  target
│  ☰  计划模式            │  list.bullet
│  ▨  照片                │  photo.on.rectangle
│  ⧉  文件                │  paperclip
└──────────────────────┘
   ↑ 锚定 ＋，从其上方弹出；「命令」节在上（对齐 ChatGPT：功能命令在前、附件在后），
     附件组不带节标题；执行中的命令行 disabled + subtitle「执行中…」
```

菜单构建时机：**状态变化即重建**（命令缓存更新 / `slashCommandExecutingNames` 变化 /
capability 或 session 变化），打开时即最新快照；不在打开期间动态刷新（系统限制，
owner 已接受）。`＋` 图标维持 `plus`；收起态 compactAttachButton 同样处理。

### 12.3 数据来源与缓存（M3）

- **预取**：capability+session 就绪处（原 slashCommandButton 显隐驱动同源）后台拉
  `list_session_commands` → 白名单过滤 → 缓存 `[BackendSessionCommand]` → 重建菜单。
  失败静默（回退兜底），不弹错、不阻塞 ＋；下次会话/capability 变化自动重试。
- **静态兜底表**（缓存缺失时构建菜单 + route 判决，不依赖网络）：
  `compact`（无 input）、`goal`（有 input，hint 回退「输入目标，智能体将持续执行」——
  官方 zh 词典原文）、`plan`（有 input，hint 回退「描述你的任务以生成计划」——官方
  占位符原文）。
- route 判决（claim vs execute）永远可答：优先官方 descriptor hint，回退内置表。

### 12.4 改动清单（iOS 单仓）

| 文件 | 改动 |
| --- | --- |
| `App/ChatInputAccessoryView.swift` | attachButton / compactAttachButton 换 UIMenu（注入：命令缓存、executing 快照、可用性）；**删除** slashCommandButton / compactSlashButton / `onSlashCommandTapped` 回调链 / 显隐逻辑；新增 `onSlashCommandSelected(BackendSessionCommand)` |
| `App/ChatUIKitContainerView.swift` | **删除** `presentSlashCommandPanel` / `presentSlashCommandPanelSheet`（sheet 路径整段）与 `handleAttach()` 的 actionSheet（照片/文件改菜单 action 直调 `showPhotoPicker` / `showDocumentPicker`）；新增命令预取+缓存注入；菜单点选回调接 `SlashCommandSelection.route` 分流（白名单下 permission 分支保留 fail-safe）；`presentSlashCommandErrorAlert` 保留（execute 失败仍在） |
| `Views/Chat/SlashCommandPanelSheet.swift` | Sheet **View 退役删除**；`SlashCommandSelection` / `SlashCommandLineAdjudicator` / `panelCommandWhitelist` / `BackendSessionCommand` **保留**并迁出（建议改名 `SlashCommandRouting.swift`，避免 Sheet 名不副实）——Adjudicator 仍管发送判决，Selection 管菜单点选分流 |
| `Resources/{zh-Hans,en}.lproj/Localizable.strings` | 新增：`chat.attachMenu.section.commands`（命令）、`chat.attachMenu.photo`（照片）、`chat.attachMenu.file`（文件）、`chat.attachMenu.compact`（压缩会话）、`chat.attachMenu.goal`（设定目标）、`chat.attachMenu.plan`（计划模式）；`chat.slashCommand.executing`（执行中…）保留复用；sheet footer 文案键退役。最终文案实现时与 App 既有 plan/goal 术语对齐 |
| 测试 | `SlashCommandPanelTests`：sheet View 用例（行渲染/footer/置灰展示）改写为**菜单构建用例**（分组结构、capability 门控、白名单过滤、执行中禁用、静态兜底表）；route / adjudicator / claim 生命周期用例**不动**（逻辑零变化） |
| Accessibility | `chat.input.slashCommand` 按钮标识删除；UIMenu 行由系统渲染、无 accessibilityIdentifier，UI 自动化按行标题定位（XCUITest 限制，记档） |

### 12.5 本期不做

- 「相机」独立附件入口（ChatGPT 有；范围外，owner 未要求，将来可加一条 action）。
- 菜单打开期间执行中状态实时刷新（快照语义，见 §12.2）。
- 挂附件时选命令的提前拦截（保持官方发送时拒绝）。
- 键入 `/` 自动补全弹层（§10 维持不关闭）；permission / export / feedback 上菜单（白名单裁决维持）。

### 12.6 验收（owner 真机）

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | dsh 会话点 `＋` | 悬浮菜单从 ＋ 上方弹出；命令三条（压缩会话/设定目标/计划模式）在上、照片/文件在下，两组均无节标题（v1.8） |
| 2 | 点 压缩会话 | 菜单即收；时间线命令卡 running→结果；执行期间重开 ＋ 该行置灰「执行中…」 |
| 3 | 点 设定目标 | 菜单即收；输入框 `/goal ` + 幽灵提示；补文本回车 → 命令卡 + goal 横条 + 流式全链路不回归 |
| 4 | 点 计划模式 | `/plan ` 认领；进计划模式后 Plan chip 出现、点按 `/plan off` 回归 |
| 5 | 非 dsh 会话 / 无会话点 `＋` | 只有 照片/文件，无命令组 |
| 6 | 附件回归 | 照片（≤5）/文件选择正常；挂附件后 `/goal …` 发送被官方文案拒绝且草稿保留 |
| 7 | 检查输入栏 | 展开/收起态均无 `/` 按钮 |
| 8 | 发送判决回归 | `/plan off` 整行执行；未知 `/xxx` 普通发送 |

### 12.7 风险

- **UIMenu 快照语义**：菜单打开期间 compact 完成不会实时恢复该行——与返工⑩ sheet
  重开语义一致，owner 已接受；回归点写入 §12.6 #2。
- **UIAction 无 accessibilityIdentifier**：自动化定位按标题文本；真机验收仍 owner 亲手。
- 纯 UI 入口收敛，`SlashCommandSelection` / `Adjudicator` / wire / Mac 全部零改动，
  回归面集中在菜单构建与 `/` 按钮删除（显隐逻辑一处，收起态同步一处）。

## 13. 三期方案：子代理过程可见性（2026-09-06 owner 裁决，v1.9）

### 13.0 背景与裁决

owner 真机跑一个多子代理任务后对照三张截图（Mac 3080 dsh 消息页 / dsh 官方网页端 /
iOS CordCode App）：iOS 对子代理过程几乎不可见——5 个子代理工具行全部渲染成裸
「任务已完成」，官方网页端每行带任务描述，settle 通知行（「上下文注入 ·
subagent-settled · Background subagent \<uuid\> …」）iOS 完全没有；同时 owner 找不到
后台任务中心入口在哪里。经 L1–L4 分级分析，owner 裁决：**做 L1（修语义失真）+ L2
（任务中心补真值与可发现性），不做 L3/L4**（见 §13.7）。

### 13.1 根因（已查实，源码锚点）

**L1a 子代理工具行 → 五连「任务已完成」**。数据链路逐环核实，**数据已完整到达
iOS，缺口在渲染词表**：

| 环节 | 锚点 | 事实 |
| --- | --- | --- |
| Mac live `tool/call` | `agent/dsh-web/codec.go` `applyToolCall`（~L621-650） | `ToolName="subagent"` + `ToolInput=toolStepTitle(args)`（description/prompt 键 → 任务描述，80 rune 截断）✅ |
| Mac reducer | `go-bridge/projection_reducer.go` ~L961-965 | part 带 `toolName`/`toolInput`，无 `title`（dsh 不产 path 标题）|
| iOS part 映射 | `SessionProjectionMapping.swift` `mapToolStep` | `title = part.title ?? pathFromToolInput ?? stringFromAnyCodable(toolInput)` → **= 任务描述** ✅ |
| iOS 呈现层 | `AssistantTimelineRenderSpec.swift` `buildTitle` | generic kind 的 default 分支 `return title` → 描述入 WebToolBlock.title ✅ |
| Web 快照 | `MessageWebSnapshotBuilder.swift` ~L603-611 | `WebToolBlock{toolName:'subagent', title:描述, primaryPath:nil}` ✅ |
| **词表缺口** | `shared-message-renderer/src/activity/deriveActivityDescriptor.ts` | `subagent` 不命中 isCommand/isMutate/isSearch/isRead 任何谓词 → `domain=generic, action=unknown` |
| 降级文案 | `formatActivityCopy.ts` | unknown action 不委托 rich engine；`trustworthyTarget` 只认 `primaryPath`（nil）→ GENERIC「任务已完成」裸行 ❌ |

**L1b settle 通知行缺失**。官方机制（deepseek-harness @ `d347e70`）：

- 子代理 settle 时 runtime **向父会话注入一条 user message**：
  `content=[{type:'text',text:结算摘要+结语}]`、
  `source={kind:'subagent-settled', form:'notice', summary:'<一行结算>', senderSessionId:'<子会话id>'}`
  （`packages/subagent/subagent/src/continuation.ts` ~L1636-1654；`settlementSummary`
  ~L325-347：`Background subagent <id> finished…/was stopped…/ran out of room…/
  declined…/failed before it finished.`）。
- 官方 UI 把 `source.kind != "user"` 的 user message 投影为 ContextMessageNode
  （`packages/client/ui-conversation/src/client/contract/records.ts` ~L98-110），渲染为
  ContextInjectionRow：标题 `message.contextInjection`（zh「上下文注入」/ en
  "Context injection"，`ui-chat/src/client/locale.ts` L46/L163）+ provenance label
  （未知 producer 用裸 kind `subagent-settled`，`context-provenance.ts`）+ notice form
  的折叠摘要 `source.summary`（`ContextBody.tsx` `noticeSummary` ~L533-537）；展开体 =
  model-facing 全文。
- **Mac 两路都丢**：live `codec.go applyUserMessage` default 分支 known-drop（注释已记
  「CordCode 投影暂无 context 行节点，recorded difference 见 owner 矩阵 §2.9」，
  即 §11.5 记录的差距，本期 owner 裁决补上）；cold `history.go` ~L213-217
  `source.kind != "user"` 直接 `continue`。

**L2 后台任务中心有骨架缺真值**。iOS 中心 UI 已完备（状态点/状态名/tokens/工具次数/
耗时/嵌套子任务 section/运行中可取消，`BackgroundTasksView.swift`；刷新链
`sessions_changed → background_tasks_changed → iOS .backgroundTasksNeedsRefresh` 已通，
`session_discovery.go` ~L363 对 BackgroundTaskProvider backend 自动发布）。缺口在
Mac dsh provider（`agent/dsh-web/background_tasks.go`）：

- `durationMillis` 恒缺——`dshSessionStats` 已解码 `LLMMs/ToolMs`（官方 sessionStats
  投影，官方面板「累计耗时」同源）但没填；wire 侧 `backgroundTaskToWire` 只在
  `FinishedAt-StartedAt` 非零时发 durationMillis，dsh 两时刻恒零 → 永远 OMIT。
- `parentTaskId` 恒空 → 详情「嵌套子任务」section 永不出现；`rootSessionId` 填的是
  直接父会话而非顶层根会话。
- 入口按钮（chrome 条 `square.stack.3d.up`）无计数徽标，子代理运行中也不提示——
  owner 实测「找不到在哪里」。

### 13.2 L1a：子代理工具行文案（iOS 单仓，渲染词表）

无新交互（修既有行的语义失真）。四拍落位：打开=既有会话时间线；输入=无；发出去=无
（已发生的工具调用）；过程展示=子代理工具行从裸「任务已完成」变为
「子代理任务完成 · \<任务描述\>」（运行中「正在运行子代理」、失败
「子代理任务失败 · \<描述\>」）。

改动（`shared-message-renderer`，message-web 与 remote-web 共用同包同修同测）：

| 文件 | 改动 |
| --- | --- |
| `src/activity/deriveActivityDescriptor.ts` | 新谓词 `isSubagentActivity`（normalizeToolToken 后 kind/name == `'subagent'`）→ `domain='orchestration'`, `action='runSubagent'`, `salience='high'`（独立行不进探索聚合，与 mutate 同级）, `confidence='parsed'`；分支放在 isCommand 判定之后（subagent 非 command 工具） |
| `src/activity/formatActivityCopy.ts` | ① `VERBS.runSubagent`：running `正在运行子代理`；`succeeded/failed/recorded` 接 target 描述（`子代理任务完成 · ${t}` / 无 t 时 `子代理任务已完成` 等同构）；cancelled `已取消子代理任务`；rejected `未启动子代理`；pending `等待批准：子代理任务`。② target 来源：`runSubagent` 时取 `block.title`（模型自写任务描述，与官方 ToolRow description 同源可信；沿用 trustworthyTarget 的最小过滤——非空、非 null 字面量、非 `…` 残串）。③ rich engine 委托条件排除 `runSubagent`（defaultSingleToolSummary 不识 subagent，会回吐 wrapper 文案，违反 §5.1.5） |
| 测试 | activity 既有测试文件补 runSubagent 用例：succeeded 带描述 / 无描述 / failed / 委托排除（不进 defaultSingleToolSummary）/ 普通工具（read/edit/bash）文案不变回归 |

### 13.3 L1b：settle 通知行（跨仓新投影 part `context_injection`）

四拍走查（镜像官方 ContextInjectionRow，非自造 UX）：

1. **打开输入的地方**：不新增入口——行随子代理 settle 自动出现在父会话时间线
   （官方同一位置：flow 内 ContextMessageNode 行）。
2. **输入**：无（runtime 注入，非用户动作）。
3. **发出去**：Mac dsh codec 识别 `user/message` `source.kind=="subagent-settled"`
   → 新 `context_injection` 投影事件 → reducer 落 system turn + part；cold 路从
   `session.history` 同一事件同 id 折叠（live/cold 同形，command part 同款保证）。
4. **过程展示**：iOS 时间线新行「上下文注入 · subagent-settled · \<一行结算\>」
   （官方 locale `message.contextInjection` zh/en 逐字）；点行展开 model-facing 全文
   （结算摘要 + "Its closing message:" + 结语）；多行体 pre 排版，收起保留摘要。

范围：**只 `subagent-settled`**。其余 context kind（`goal` 轮、`agent-instructions`、
`skill-catalog`、`agent-message` relay 等）维持 known-drop recorded difference
（§11.5 不扩；未知 kind 继续 fail-open 静默，不 reset 流）。

#### 13.3.1 Mac 改动

| 文件 | 改动 |
| --- | --- |
| `core/message.go` | 新 `ContextInjectionEvent{Kind, Form, Summary, Text, SenderSessionID string}`；`EventContextInjection` EventType；`Event.ContextInjection *ContextInjectionEvent` 权威 payload |
| `agent/dsh-web/events.go` | `dshSource` 增 `Form/Summary/SenderSessionID`（json omitempty，additive；其他 kind 恒空） |
| `agent/dsh-web/codec.go` | `applyUserMessage` default 分支前插 `case d.Source.Kind == "subagent-settled"`：Summary 空则维持静默丢（fail-open，不造行）；否则 emit `EventContextInjection{Text: joinTextBlocks(content), …}`，`ItemID="ctxinj:"+seq`。**不挂 activeTurn、不 validateActiveTurnStep**（注入发生在任意父会话状态，独立系统行，镜像 command 的 turnId 前缀模式） |
| `agent/dsh-web/history.go` | `user/message` 分支：`source.kind=="subagent-settled"` 且 Summary 非空 → `RichHistoryEntry{ID:"ctxinj:"+seq, Role:"context_injection", Content:Text}` + 新增 `ContextInjection *ContextInjectionEvent` 字段挂 meta（kind/form/summary/senderSessionId），不再 continue 丢弃 |
| `go-bridge/events.go` | `EventContextInjection` → event `"context_injection"`，data 全字段（summary/text 缺省省键） |
| `go-bridge/projection_reducer.go` | 新 case `"context_injection"`：`turnId="ctx:"+itemId`，upsert 恰一个 completed system turn + part `{Type:"context_injection", ItemID, ContextKind, ContextForm, ContextSummary, ContextText, ContextSenderSessionID}`（镜像 `session_command` 的整值替换语义） |
| `go-bridge/projection_types.go` | `ProjectionPart` 增 `ContextKind/ContextForm/ContextSummary/ContextText/ContextSenderSessionID`（wire json `contextKind/contextForm/contextSummary/contextText/contextSenderSessionId`，omitempty）+ 快照 writer `out.*` 拷贝 |
| `go-bridge/handlers_projection.go` | `openCodeRichHistoryEntryToProjectionEvents` 新 `role=="context_injection"` case → 同名事件（与 live 同 data 形） |
| 协议包 | `docs/protocol/bridge-v1.md` Part vocabulary 新节（command 节后）+ `schema/bridge-v1.types.ts` 新 variant + iOS mirror `docs/protocol/` 同步 |
| 测试 | codec live fixture（官方 continuation.ts 字段形状构造 sanitized 样本，标注源码锚点）；history cold 折叠；reducer part 形状/幂等 upsert；未知 context kind 维持丢、不 reset 回归 |

#### 13.3.2 iOS 改动（command part 同管道逐环镜像）

| 文件 | 改动 |
| --- | --- |
| `Models/SessionProjection.swift` | part 解码字段 `contextKind/contextForm/contextSummary/contextText/contextSenderSessionId` |
| `Models/SessionProjectionMapping.swift` | `mapPart` case `"context_injection"` → 新 `MessagePart.contextInjection(MessageContextInjection{itemId,kind,form,summary,text,senderSessionId})`（kind 空/summary 空且 text 空 → skip，fail-closed） |
| `Models/Message.swift` | `MessageContextInjection` struct + `MessagePart` case |
| `App/ChatTimelineAdapterUIKit.swift` | system group 提取 contextInjection part（`commandRow` 同式）+ 纯文本降级 `contextInjectionPlainText`（「上下文注入 · subagent-settled · summary」，accessibility/快照面） |
| `App/ChatMessageGroupModels.swift` | `ChatPresentationItem` 增 contextInjection payload |
| `App/MessageWeb/MessageWebModels.swift` | `WebContextInjectionRow{itemId,kind,form,summary,text}` + `WebTimelineItem.contextInjection`（缺省解码 nil，旧缓存兼容） |
| `App/MessageWeb/MessageWebSnapshotBuilder.swift` | `makeContextInjectionRow`（text 空不下发，同 command 的 inputLine 惯例） |
| `message-web/src/types.ts` | `WebContextInjectionRow` + item 字段 |
| `message-web/src/components/turns/ContextInjectionTurn.tsx`（新） | 官方 ContextInjectionRow 镜像：图标 + 标题（locale `chat.contextInjection.title` 上下文注入/Context injection）+ ` · ` + kind label + ` · ` + summary；点行展开 pre 全文（notice 语义：通常不展开即可读）；keepContentWhenOpen |
| `message-web/src/App.tsx` | item.contextInjection 非空 → 渲染 ContextInjectionTurn（CommandTurn 同位） |
| `Resources/{zh-Hans,en}.lproj/Localizable.strings` | `chat.contextInjection.title`（上下文注入 / Context injection——官方 locale.ts 逐字） |
| 测试 | part 解码/mapping（含缺字段 skip）；snapshot builder row 形状；ContextInjectionTurn 渲染（折叠摘要/展开/label 三段）；旧快照无字段兼容 |

### 13.4 L2：后台任务中心补真值 + 入口徽标

四拍走查：

1. **打开**：chrome 条既有「后台任务」按钮（capability 门控不变）；新增**计数徽标**
   ——有运行中子代理时绿色 tint + 运行数徽标；无运行但有任务时显示总数徽标；无任务
   无徽标（按钮仍在）。
2. **输入**：无（只读面）。
3. **发出去**：列表/详情读取走既有 `background_tasks.list/get`；取消走既有
   `background_tasks.cancel`（不变）。
4. **过程展示**：行内新增**耗时**（mm:ss，来自官方 sessionStats llmMs+toolMs）；详情
   「嵌套子任务」section 因 parentTaskId 填真值而首次出现；顶层根会话归属正确
   （多级嵌套下 rootSessionId=沿链到顶）。刷新链沿用
   `background_tasks_changed → .backgroundTasksNeedsRefresh`（徽标同链驱动）。

| 仓库 | 文件 | 改动 |
| --- | --- | --- |
| Mac | `core/message.go` | `BackgroundTask` 增 `DurationMillis int64`（0=unknown；与 StartedAt/FinishedAt 派生值并存，显式指标优先） |
| Mac | `go-bridge/background_tasks.go` | `backgroundTaskToWire`：`t.DurationMillis>0` 时发 `durationMillis`（既有 FinishedAt 派生分支保留，二者都有时显式值覆盖） |
| Mac | `agent/dsh-web/background_tasks.go` | ① `stats` 解码后 `task.DurationMillis = LLMMs+ToolMs`（>0 才填）；② 两遍扫描：先收集全部 `origin=subagent` 行建 SessionID set，`parentSessionId ∈ set` → `ParentTaskID=parentSessionId`、RootSessionID 沿链到顶（父不在 set = host 会话）；③ `GetBackgroundTaskDetail` 的 nestedTasks 已按 ParentTaskID 派生，自动点亮 |
| Mac | 测试 | `background_tasks_test.go`：duration 填真（>0）/未知 OMIT；单级/两级嵌套 parentTaskId+rootSessionId 链；title 占位回归 |
| iOS | `App/ChatUIKitContainerView.swift` | 订阅 `.backgroundTasksNeedsRefresh`（按钮可见时）→ 防抖拉 `listBackgroundTasks` → 徽标 = running>0 ? running : total（0 → 无徽标）；running>0 时按钮 tintColor 绿；会话打开/切走复位 |
| iOS | 测试 | 徽标计数矩阵（running 优先/total 兜底/0 隐藏）；刷新通知 → 拉取去重防抖 |

### 13.5 验收（owner 真机）

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | dsh 跑多子代理任务，iOS 看时间线 | 子代理工具行显示「子代理任务完成 · \<任务描述\>」（运行中/失败同构），不再五连「任务已完成」 |
| 2 | 等子代理结束（含失败/停止） | 时间线出现「上下文注入 · subagent-settled · Background subagent \<id\> …」行；点行可展开全文；冷重开会话行仍在 |
| 3 | 对照 dsh 官方网页端同会话 | 行语义/描述/结算文案一致（官方 locale 逐字） |
| 4 | 打开后台任务中心 | 行有耗时；嵌套任务详情出现「嵌套子任务」section；顶层归属正确 |
| 5 | 子代理运行中看 chrome 条 | 「后台任务」按钮带绿色计数徽标；settle 后徽标回落 |
| 6 | 回归 | 普通工具行（读取/编辑/执行）文案不变；＋菜单/goal/plan/命令卡不回归；非 dsh backend 无新行/无徽标 |

### 13.6 协议与文档同步

`context_injection` part 为 wire 增量：Mac `docs/protocol/bridge-v1.md`（Part
vocabulary 新节，live/cold 来源与 fail-open 语义）+ `docs/protocol/schema/bridge-v1.types.ts`
（additive variant）+ iOS `docs/protocol/` mirror 同步；两仓 CHANGELOG 各记一条。
L2 durationMillis/parentTaskId 为既有 wire 字段首次填真值，无 schema 变更。

### 13.7 本期不做（owner 裁决维持）

- **L3** 子会话点进（openChild 只读会话视图；`session.history(childId)` 通道已具备）。
- **L4** 角色提示词摘要/模式徽章（需消费官方 `list_agents` registry，新数据面）。
- 其余 context kind（goal 轮/agent-instructions/skill-catalog/agent-message relay）
  的时间线行——维持 §11.5 recorded difference。
- 后台任务列表 failed/cancelled 状态判定（list 面两态诚实维持；settle 行已把失败
  语义带给时间线，不在任务中心重复推断）。

### 13.8 风险

- **L1b 注入时序**：settle 注入可发生在父会话 idle（followup 新 turn）或 busy（steer
  在途 turn）。独立 system turn 不挂 activeTurn，与 command 卡同机制，reducer 整值
  upsert 幂等；live/cold 同 id（`ctxinj:<seq>`）防重。
- **词表扩展面**：L1a 只加 `subagent` 一个 action，不触碰既有六 action 文案与 rich
  engine 委托矩阵；回归用例钉住。
- **徽标拉取成本**：防抖 + 仅按钮可见时订阅，不引入常驻轮询；错误静默（徽标非权威
  面，列表打开时仍全量拉）。
- **冷热不一致窗口**：hydrate 前行缺席、hydrate 后补齐——与 command 卡同一表现，
  owner 已接受该模型。
