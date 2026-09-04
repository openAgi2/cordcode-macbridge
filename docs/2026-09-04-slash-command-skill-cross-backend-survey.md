# 五 backend「/」命令与技能调研——官方实现、CordCode 接线层与 iOS 接入缺口

- 日期：2026-09-04
- 性质：**只读调研报告（事实输入）**。不改代码、不立项实施方案、不替 owner 决定 UI。owner 问
  「怎么接到 iPhone」时看各 backend 节的「接入选项」小节（选项 A/B/C/… + 四态标注）。
- 调研指令：[docs/2026-09-04-slash-command-skill-ios-survey-brief.md](2026-09-04-slash-command-skill-ios-survey-brief.md)
  （本文件在当前工作树为未跟踪状态，属 owner 指令文件，本次仅读）。
- 与既有调研的边界：**计划审批卡**（全文 + 批准/打回/放弃）调研与交付见
  [docs/2026-09-03-plan-mode-cross-backend-survey.md](2026-09-03-plan-mode-cross-backend-survey.md)，本报告不改其语义；
  「进入计划模式」在本报告只作为 `/` 目录中的一项**类别**分析（谁在列表里、走什么通道），
  不重做审批卡调研。Codex 计划模式入口另案见
  [docs/2026-09-04-codex-ios-plan-mode-entry.md](2026-09-04-codex-ios-plan-mode-entry.md)。
- 证据分层声明：每条形状声明带锚点（`仓库@commit 文件:行 符号`）。**活体探针**（本机运行中服务的
  只读 HTTP 请求，仅 list 类，未执行任何命令、未发送任何消息）与**源码/文档结论**分开标注。
  Claude 无本机官方源码仓，以官方文档 + CLI 2.1.234 实测 dump（仓内归档）为准。

---

## 0. 来源清单（P0 门，2026-09-04 调研开始时实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=bdc2cda73a16d1d8c6c079a7dd1a12be171a12a4（与 main 同提交）
未提交状态=干净（仅 1 个未跟踪：docs/2026-09-04-slash-command-skill-ios-survey-brief.md，owner 指令文件，仅读）
任务预期分支=只读调研 + 本报告落盘当前工作树 docs/；不改任何源码
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios  main  db7972cfa3f5fdb995c7b5a7e5117dd60c0d88b3
  （2026-09-04 20:23 已合并 plan/approval-layer-ios，即计划审批卡 iOS 面已进 main；实测干净）
辅助读取（在途分支，只读未触碰）=/Users/jacklee/Projects/cordcode-macbridge-claudecode-official
  claudecode/official-capability  ee460685e90e2cabda1bce6b6d90d6235a50fb41（bdc2cda 的后代，领先 5 个提交：
  Claude 控制面 Phase 0-2）；该树脏：M agent/claudecode/continuation.go、M agent/claudecode/session_mutation.go、
  ?? agent/claudecode/testdata/transcript-shapes/ 等——属在途工作，本报告引用其中 Phase 0 dump 时逐条标注
上游（四仓实测干净）：
  codex             /Users/jacklee/Projects/codex             main    50fffd5ed367aa99491d9ec58575626fce4e9dd4
  grok-build        /Users/jacklee/Projects/grok-build        main    72a61251fcffb464bcc687aeb5a998e5a98ec0c9（SOURCE_REV=a549186d…）
  deepseek-harness  /Users/jacklee/Projects/deepseek-harness  master  49a606bc5b5934603f22a26957a07dc799ab0291
  opencode          /Users/jacklee/Projects/opencode          dev     69c172e8a7c0086887b1f93ed5a162f14b6aa0c5（版本 1.18.26）
目标二进制（按指令 §1.4 以用户机器实际运行为准）：
  claude   /opt/homebrew/bin/claude                                       2.1.234
  dsh      /opt/homebrew/lib/node_modules/@deepseek-ai/dsh               0.1.1-rc.2（座位 127.0.0.1:3080 活体，pid 1055）
  opencode managed serve（Basic Auth，4096 端口）                         1.18.20（活体，pid 944）
  grok     /Users/jacklee/.grok/bin/grok                                  1.0.13 (5e9a58528b76)
  codex    ChatGPT.app 内嵌 codex-cli                                     0.153.0-alpha.5
版本漂移：
  dsh      装机 0.1.1-rc.2 = tag dsh-v0.1.1-rc.2（b150a551b8，master 祖先）；master 已合并 0.1.2-alpha.5
  grok     装机 1.0.13（2026-08-28）vs checkout 1.0.16（2026-09-01），隔 1.0.14/15/16 三个 patch
  opencode 装机 1.18.20（= 提交 a9cac91d60）vs checkout dev 1.18.26——命令/技能/agent 协议相关文件 0 diff（§5.0）
  codex    装机 0.153.0-alpha.5 vs checkout main@50fffd5——未逐项 diff（未核实清单 #4）
活体只读探针（2026-09-04，全部为 list/catalog 类 GET/POST，未执行命令、未发消息、未改状态）：
  dsh 3080      POST /api/session.list（仅取 sessionId）、POST /api/commands/list、POST /api/skill.list；
                对照探针：/api/skills/list（master 新路径）在 0.1.1-rc.2 上 404
  opencode 4096 GET /command、GET /skill、GET /agent、GET /doc（OpenAPI 3.1 schema）
预期产品特性=斜杠命令/技能的 list + execute（按各家官方通道形态分别核实）
全程零写操作：两 CordCode 仓、四上游 checkout 均未修改（本报告文件为唯一新增）
```

---

## 1. 总表

| Backend | 官方有 `/` 列表？ | 列表含命令？含技能？ | 官方执行 API | Plan 是否在 `/` 里 | CordCode 已接线 list？execute？ | iOS 现状 | 接入最小缺口 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **DSH（dsh-web）** | ✅ web 输入 `/` 弹两组菜单 | ✅ 命令（host 目录）+ ✅ 技能（目录 RPC） | 命令：`POST /api/commands/execute`；技能：**无 execute**，`/name` 字面文本随 `session/prompt` 发送、host pre-step 注入正文 | **✅ 是**（`/plan` 是 host 真命令，活体目录 6 条含 plan） | list ❌ / execute ⚠️ 仅 `/permission` 一条线（`commands/execute` 有生产使用先例） | 无 `/` 面板；`/plan` 当聊天发 → host 不识别、进模型当普通文本 | bridge 新增 list（`commands/list`+`skill.list`，目标版路径已活体）与 execute 透传 |
| **Claude Code** | ✅ CLI 交互 `/` 菜单；**程序化目录已在帧里** | ✅ 内置+插件+用户定义混排（自定义命令已并入 skills 体系） | **无 execute RPC**：官方语义 = 把 `/name args` 作为 user prompt 文本发送（CLI 入口展开） | ❌ 程序化目录 48 项无 plan（交互式有 `/plan` prompt-前缀语义）；Plan=permission mode | list ❌（`system/init` 帧已带 `slash_commands`/`skills`，未消费）/ execute ✅ 隐式（send_message 原样进 user message，对用户定义命令即官方语义，有生产实证） | 无 `/` 面板；Plan 入口已交付（permission-mode 三件套） | bridge 消费 `system/init.slash_commands`+`skills` 并下发目录；execute 复用现有 send_message |
| **OpenCode Web** | ✅ TUI/web `/` 面板；**server 暴露完整列表 API** | ✅ `GET /command` 一列混排（`source`: command/mcp/skill） | `POST /session/:id/command`（必填 `command`+`arguments`，可选 agent/model/parts）；模板展开后走普通 prompt 路径 | ❌ plan 不是命令，是 built-in **agent**（primary，edit 全 deny） | list ❌（`GET /agent` 已被 ListAgents 用；`/command`、`/skill` 未接）/ execute ❌（`/session/:id/command` 未接） | 无 `/` 面板；输入条有通用 agentTag（plan agent 是否可选未核实 #7） | bridge 接 `GET /command`（含技能）+ `POST /session/:id/command` |
| **Codex Desktop（codex-remote）** | ⚠️ 仅 TUI 本地（~70 条内置枚举；**无自定义命令**） | 命令 ✅（TUI 概念）；技能是独立子系统（SKILL.md + `skills/list` app-server 方法） | **无命令 list/execute 面**：TUI 动作在协议层散落（`thread/compact/start`、`thread/settings/update`、`turn/start.collaborationMode`…）；技能用户侧 = `$name` mention 进 `turn/start` input | ⚠️ TUI 有 `/plan`（动作=切 collaborationMode）；协议层 Plan=`collaborationMode`（EXPERIMENTAL） | list ❌ / execute ❌（仅 plan 审批卡的 `collaborationMode` 代发接线存在） | 无 `/` 面板；能批不能开（挂起另案） | 命令面板无官方语义可走（fail closed）；技能面板可接 `skills/list` |
| **Grok Build** | ✅ TUI 双层（pager 本地 + shell 解析）；**ACP 层有目录通道** | ✅ `_meta.availableCommands`（内置）+ skills + workflows；`x.ai/commands/list` 拉取 | **无独立 execute RPC**：`/cmd args` 作为 prompt 文本发 `session/prompt`，shell 端 `resolve_human_intent` 解析（builtin 动作 / skill 展开） | ⚠️ TUI `/plan` 是 **pager 本地动作**→映射 `session/set_mode`；shell 解析面无 plan | list ❌（`initializeResult` 未解码 `availableCommands`；catalog 子进程未调 `x.ai/commands/list`）/ execute ❌（prompt 文本通道未用于命令；SetMode 只写内存不上 wire） | 无 `/` 面板；选 Plan 不生效（既有事实） | 解码 `availableCommands` / 接 `x.ai/commands/list`；`/plan` 项需映射 `session/set_mode` 而非 prompt 文本 |

> 「官方有 `/` 列表」一列指**协议/程序化可用的目录面**，不含纯 TUI 本地弹窗（Codex TUI 有弹窗但不上协议）。

---

## 2. 概念标尺（报告内统一分类，禁混用）

按指令 §3，把 `/` 面板里可能出现的东西拆成六类。五家对「plan / 进计划模式」的归类：

| 类别 | DSH | Claude | OpenCode | Codex | Grok |
| --- | --- | --- | --- | --- | --- |
| **slash 命令**（官方命令目录 + 执行 RPC 或等价物，**不是** user message） | `plan`、`permission`、`compact`、`export`、`goal`、`feedback` | 程序化目录里的内置命令（`compact`、`clear`、`model`…）——但执行=文本 | 内置 `init`、`review`（`POST /session/:id/command`） | 无自定义；TUI 内置不上协议 | shell builtin（`compact`、`always-approve`、`memory`…，经 prompt 文本解析） |
| **技能 / skill** | 目录 RPC 单列一组；执行=prompt 字面文本 + host pre-step 注入 | 与自定义命令合并（`SKILL.md`）；执行=渲染正文进会话单条消息 | `source:"skill"` 出现在 `GET /command`；同一 execute API | SKILL.md 子系统；模型侧 `skills_list/read` 工具 + 用户侧 `$name` mention | SKILL.md 发现（含 `.claude/.agents` 兼容）；`/name` 文本→`<skill_information>` 信封 |
| **模式开关** | `permission`（命令形态的 preset 切换）；`plan`（命令形态的模式切换） | permission mode（`Shift+Tab`/`--permission-mode`/`set_permission_mode`）——**不在程序化命令表** | 无模式字段；等价物=agent 切换 | `collaborationMode`（TUI `/plan` 的动作本体） | `session/set_mode`（TUI `/plan` 的动作本体）+ `session/prompt._meta.mode` |
| **选择器** | `model`（纯客户端 contribution；真实切换=独立 RPC `session/selectModel`） | `model`（本地 UI；协议面=`set_model` 控制请求） | `model`（消息级 model 字段） | `model`（TUI；协议面=`model/list`+`thread/settings/update`） | `model`（TUI；协议面=`session/set_model`） |
| **agent 切换** | 无（agent preset 是另一轨：`agentPreset.*`） | `agents` 目录在 init 帧里（子代理体系） | **plan 即此列**：per-message `agent` 字段 / v2 `POST /api/session/:id/agent` | TUI `/agents`（子代理） | 无（单 agent 模型） |
| **纯本地 UI** | popup 装饰、菜单交互 | `/theme`、`/diff`、`/context` 等终端专属 | TUI keymap 斜杠（本地 palette） | TUI 大多数命令 | pager 本地命令（`settings`、`theme`、`vim-mode`…） |

**三层「Mac 端」**（结论归属声明）：本报告 §3–§7 每节的 X.1/X.2 结论来自**官方客户端/服务端源码或活体**
（第 1 层）；X.3 来自 CordCode Link + go-bridge（第 2 层）；X.4 来自 iOS 仓（第 3 层）。owner 截图描述的
DSH 官方 UX 仅作对照引用，目录完整性以源码/活体为准。

**跨家共性发现（本机实证）**：`~/.agents/skills/`（8 个技能：audit-plan、exec-plan、handoff-doc、
ios-real-device-doc、skill-creator、source-command-handoff-doc、supervise、takeover）是被多家共同消费的
共享技能库——DSH（user-agents 根）、Codex（`~/.agents/skills` 原生根）、OpenCode（外部技能根，活体
`GET /skill` 返回位置含 `~/.agents/skills/...`）原生读取；Claude 经 `~/.claude/skills`（其中 supervise 等
为指向 `~/.agents` 的 symlink）；Grok 项目级 `./.agents/skills` 原生、用户级经 `~/.claude` 兼容门控。
owner 截图里的技能名单 = 这个共享库的投影。

---

## 3. DeepSeek Harness（dsh-web）

### 3.1 官方 `/` 面板（源码 @49a606b + 活体 0.1.1-rc.2）

- **打开方式**：输入框键入 `/`，词首触发（[DSH] `packages/client/ui-input-trigger/src/core/detect.ts:48`
  `detectTrigger`；`:` 之后与 `//` 的第二个斜杠不触发，detect.ts:14-26）。
- **列表数据源**（多 source 触发管线，一个 source 一个菜单组）：
  - 命令组：HTTP RPC `commands/list`（[DSH] `packages/client/ui-commands/src/client/service.ts:142`
    `ctx.remote.commands.list(sessionId)`；目录缓存 `directory.ts:34` `CommandDirectory`，single-flight）+
    客户端本地 contribution（`/model`，[DSH] `packages/client/ui-model-selection/src/client/index.ts:131`）。
  - 技能组：RPC `skills/list`（master；[DSH] `packages/client/ui-skill/src/client/index.ts:101`），
    `order:2` 排在命令组之下（index.ts:137-182）。
- **分组与过滤**：命令/技能是**两个菜单组**（[DSH] `packages/client/ui-input-trigger/src/core/menu.ts:30`
  `seedGroups`）；命令组 fuzzy 过滤（`service.ts:108 fuzzyCandidates`），技能组前缀匹配（index.ts:146）。
- **列表项身份**：`InputTriggerCandidate {name, description?, icon?, hint?, section?, value?, drill?}`
  （[DSH] `packages/client/ui-input-trigger/src/types.ts:47-62`）；命令元数据 `CommandDescriptor
  {name, description, input?{hint, images?}}`（[DSH] `packages/interaction/commands/src/types.ts:13-58`）。
  popupSelect 子面板（`/model`、`/permission` 裸调用时弹选择器）行带 `confirmation?`（风险确认门，
  [DSH] `packages/client/ui-commands/src/client/contract.ts:19-26`）。
- **内置命令表**：无中央硬编码表，全部插件 `commands.register` 注册——plan（[DSH]
  `packages/plan/plan-mode/src/index.ts:226`）、permission（`packages/interaction/permission-presets/src/index.ts:257`）、
  compact、export、goal、feedback；`/model` 不在 host 表（纯客户端 contribution）。
- **Plan 是否在列表里**：**是**。`/plan` 是 host 端真命令，`input.hint='[off|message]'`，`images:true`。
  **活体**（0.1.1-rc.2，`POST /api/commands/list`，真实 sessionId）：返回 6 条 =
  `plan / permission / compact / export / feedback / goal`——与 owner 截图命令集完全一致
  （截图中的 `model` 来自客户端 contribution，不在 host 目录，与源码结论吻合）。
  **技能活体**（0.1.1-rc.2 旧路径 `POST /api/skill.list`，payload 直传 `{sessionId}`）：返回
  `{skills:[{name, description, modelInvocable}]}` 共 8 条 = `~/.agents/skills` 全集，与截图技能集一致。
- **官方测试**：[DSH] `packages/interaction/commands/tests/commands.spec.ts:57,301,424,528`；
  `packages/client/connection/tests/fixture-commands.client.spec.ts:57`（wire 级）；
  e2e `apps/web/tests/skill-user-invoke.e2e.ts:101`、`plan-control-row.e2e.ts:50`。

### 3.2 官方执行通道

- **命令执行**：`POST /api/commands/execute`（envelope `[DSH] packages/client/connection/src/client/rpc.ts:60-73`
  `ClientRequest{type:'client-request', rpcId, method, payload}`；`POST /api/<method>` 形态与 CordCode
  dsh-web wire 客户端一致——[Mac] `agent/dsh-web/wire.go:186` `Client.Call`，注释明言同构）。
  **payload 实证**：`{args:{agentId:<sessionId>, line:'/plan off', images:[]}}`（[DSH]
  `fixture.ts:3412-3432`；[Mac] `agent/dsh-web/permission_mode.go:111-131` 生产代码同形）。
- **成功/失败/未知命令**：未知或语法不命中 → handler 返回 `undefined`，**不写任何日志**（[DSH]
  `packages/interaction/commands/src/index.ts:332-400` `execute`；`parseCommand:118` 正则
  `/^\/([a-z][a-z0-9_-]*)/`）；命中则先 append `command/run` → handler → `command/done`（durable
  会话日志事件对）；handler 出错 = `result {kind:'error', text}`（仍算已执行、有日志）；传输层失败 =
  `{ok:false, error:{code,message,details}}`。
- **技能不走 execute**：UI 选中技能只把 `/name ` 字面文本放进输入框，随普通 `session/prompt` 发送
  （[DSH] `packages/client/ui-skill/src/client/index.ts:172-181`，注释原文 "the pick lands plain text and
  the prompt ships the same literal"）；host 端 `agent/pre-step` 监听扫描 user 消息首行 leading `/name`，
  命中 user-invocable 技能则把渲染正文作为 injected instructions 追加进本步请求（[DSH]
  `packages/skill/tool-skill/src/index.ts:177-204`）。**重名时命令赢**（客户端 adjudication 先 claim）。
  这解释了既有产品事实：iPhone 把 `/plan` 当聊天发 → `/plan` 是命令不是技能 → pre-step 不认 →
  字面文本进模型；而 `/handoff-doc` 这类**技能名**若从 iPhone 发出，按官方语义会被 host 注入执行。
- **模式影响**：`/plan` 切 plan 模式（状态=会话日志事件 `plan/mode {active}`，[DSH]
  `packages/plan/plan-mode/src/index.ts:46,412-432`——有 open turn 时入 `pendingIntents`，到下一个
  in-turn pre-step 边界才生效，非简单 toggle；带消息经 `agent.steer()` 注入）；`/permission <preset>`
  直接切 preset 并 `approval.setPolicy`（[DSH] `permission-presets/src/index.ts:257-274`，**这是 web 客户端
  唯一的 preset 写路径**，无独立 API）；`/model` 真实切换走独立 RPC `session/selectModel`
  （`packages/client/ui-model-selection/src/client/directory.ts:92`）。
- **是否等于发 user message**：命令=**否**（独立 RPC + durable 日志）；技能=**是**（字面文本随
  session/prompt，host 侧注入——这是官方语义，不是 CordCode 的错误路径）。
- **双客户端**：命令生命周期 `command/run|done` 经 `session/follow` 流广播，**每个客户端渲染为常驻
  flow node**（[DSH] `packages/client/ui-commands/src/client/service.ts:440-447` 注释原文）；本地提交 ack
  `command/executed` 只发执行端（service.ts:41）。全局事件转发 allowlist 见 [DSH]
  `packages/api/gateway/src/stream-protocol.ts:16-35`。

### 3.3 CordCode Mac 现状（@bdc2cda）

- `commands/execute` **有生产使用**，但唯一用途 `/permission`：[Mac] `agent/dsh-web/permission_mode.go:27`
  常量、`:111 applySessionPermission`（`SetLiveMode` 活动会话即时切换走它；`SetMode` 持久化走
  `settings.update`，`:142-159`）。
- **无 list 接线**：dsh-web 全部 RPC 面无 `commands/list` / `skill.list` 调用；`command/run`、
  `command/done` 事件在 codec 被列为 known control-plane 直接丢弃（[Mac] `agent/dsh-web/codec.go:225-226`）。
- `/plan`：CordCode 不发送；plan 审批卡走官方 question 面（[Mac] `agent/dsh-web/approvals.go:462-481`
  `intent.kind=="plan-review"` 升格 plan_review；应答 `:181-199`）。
- capability：`StaticCapabilities` 无命令/技能位（[Mac] `agent/dsh-web/wire_descriptor.go`）。

### 3.4 iOS 现状（@db7972cf）

- 无 `/` 面板（输入条按钮清单无命令项，[iOS] `App/ChatInputAccessoryView.swift:118-147`）。
- DSH permission preset **无 iOS 入口**（[iOS] `Views/Chat/SelectionSheets.swift:230-231` 明文「权限模式由
  MacBridge DSH_PERMISSION_MODE 预设控制」）。
- `/plan` 文本原样作为 `send_message.content` 发出（[iOS] `Services/Bridge/CCCodeBridgeClient.swift:370-383`，
  无拦截层）→ 按 §3.2 官方语义进模型当普通文本，不进 plan。
- **DSH `/plan` 之后的 plan-review 卡 iOS 已能接住**（backend-agnostic 的 `permissionKind=plan_review`
  路径，[iOS] `CCCodeBridgeBackendClient.swift:1539` / `App/TaskDockView.swift:260`）——面板接线后无需改卡面。

### 3.5 接入选项（非方案；四态：supported / deliberately unsupported / not applicable / future）

- **A. iOS `/` 面板 → 新 bridge RPC**（`list_commands` 走 `POST /api/commands/list` + `list_skills` 走
  `POST /api/skill.list`（**目标版 0.1.1-rc.2 是旧路径、payload 直传 sessionId**；master 已改为
  `skills/list`+args 包装——版本钉死必须跟随装机版）+ `execute_command` 透传 `commands/execute`）。
  已有锚点：wire 客户端 [Mac] `agent/dsh-web/wire.go:186`；execute 生产先例 `permission_mode.go:111`；
  list/技能路径活体已验（§0）。缺样本：`commands/list`/`skill.list` 返回体已在活体取得（6 命令/8 技能，
  字段见 §3.1）。破坏面：无（新增 RPC）；注意 command 与 skill 的执行语义必须拆开（D）。
- **B. 复用现有通道先接 Plan（不做整面板）**：`/plan` 即 `commands/execute {line:'/plan'}`，通道同
  `/permission`——最小缺口是 bridge 暴露一个 execute 入口（或临时复用 permission 通道的模式）。
  supported（证据齐）。
- **C. 技能执行复用 send_message**：按官方语义，技能=字面 `/name args` 文本 + host pre-step 注入——
  现有 `send_message` 已是官方通道（无需新 execute）。supported（源码+注释背书；未做端到端活体验证，
  见未核实 #1）。
- **D. 命令与技能必须拆开**：目录分两个来源、执行两条语义（execute RPC vs prompt 文本）；`/model`
  建议路由到已有模型面板（独立 RPC `session/selectModel`，[Mac] `agent/dsh-web/models.go:184-198` 已接），
  `/permission` 可路由到 permission 面或命令通道（两者官方等价，前者是后者的唯一写路径）。
- **not applicable**：DSH 无 agent 切换类；`/export`（ZIP 下载）在 iOS 的呈现形态属产品决策。

---

## 4. Claude Code

### 4.1 官方 `/` 面板（文档 + CLI 2.1.234 实测 dump）

- **打开方式**：交互 CLI 键入 `/` 弹菜单；命令只在消息**开头**识别（官方 commands.md："A command is
  only recognized at the start of your message"）。
- **列表数据源**：CLI 内部发现 `.claude/skills`（项目）、`~/.claude/skills`（个人）、插件 skills、
  managed settings；**自定义命令已并入 skills 体系**（官方 skills.md Note 原文："Custom commands have
  been merged into skills. A file at `.claude/commands/deploy.md` and a skill at
  `.claude/skills/deploy/SKILL.md` both create `/deploy` and work the same way"）。菜单混排内置命令
  [Skill] [Workflow] 标记 + MCP prompts（`/servername:promptname (MCP)`）。
- **程序化目录（对桥最关键）**：CLI 2.1.234 在 CordCode 同款 spawn 参数下——
  - **stdout `system/init` 帧**已带 `slash_commands`（48 个名字）、`terminal_slash_commands`（终端专属
    集）、`skills`、`agents`、`permissionMode`、`tools`、`mcp_servers`、`plugins`
    （**实测 dump**：[Mac-claudecode-official@ee46068] `scripts/claudecode-phase0/dumps/envmx-A-baseline.jsonl`
    stdout 帧 keys 实录；该帧 CordCode adapter 今天就在读——[Mac] `agent/claudecode/session.go:417-418`
    `case "system"` → `handleSystem:435`，但未消费 `slash_commands`/`skills` 字段）。
  - **控制面 `initialize` 请求**返回 `commands`（48 项，`{name, description(尾缀 " (user)" 标用户定义),
    argumentHint}`）、`agents`（11 项）、`models`、`current_permission_mode`（**实测 dump**：
    `dumps/main.jsonl` req_1 成功体；SDK 类型锚 [SDK] sdk.d.ts:3989-3994）。
  - 官方文档确认 system/init 的 `slash_commands` 就是程序化命令面（agent-sdk/skills："The `system/init`
    message lists the ones available in your session in its `slash_commands` field. Commands that need an
    interactive terminal … don't appear in the list"）。
- **列表项身份**：`initialize.commands` 给 name/description/argumentHint；`system/init` 只给名字数组 +
  `skills` 数组。frontmatter 全字段（name/description/argument-hint/allowed-tools/model/agent/context/
  disable-model-invocation/user-invocable 等，官方 skills.md Frontmatter reference）。
- **Plan 是否在列表里**：**不在程序化目录**（48 项实测无 plan）。交互式文档有 `/plan [description]`
  （commands.md；permission-modes.md："Enter plan mode by pressing `Shift+Tab` or prefixing a single
  prompt with `/plan`"——即 prompt 前缀语义，会话级切换靠 Shift+Tab）。**入口类型=模式开关
  （permission mode）**，非命令。模式全集 default/acceptEdits/plan/auto/dontAsk/bypassPermissions。

### 4.2 官方执行通道

- **统一事实：没有 execute RPC。官方执行 = 把 `/name args` 作为 user prompt 文本发送**：
  - headless 官方原文："User-invoked skills and custom commands work in `-p` mode: include
    `/skill-name` in the prompt string and Claude Code expands it before running."
  - SDK 官方原文："Send a command by including it in your prompt string, the same way you send regular
    text. Dispatch doesn't depend on the `skills` option."
  - 展开：渲染后的 SKILL.md 内容**作为单条消息进入会话**（skills.md "Skill content lifecycle"）；
    `$ARGUMENTS`/`$N` 替换、`` !`cmd` `` 预执行 shell 注入（失败则整个调用中止，模型看不到内容）、
    `@file` 引用（文档部分覆盖）。
- **内置命令按条目区分**：终端专属（`/login`、`/theme`…）不可用；带参可用（≥2.1.205）：`/model`
  `/effort` `/fast` `/color` `/rename` `/mcp` `/config key=value`；`/compact` 程序化可用（SDK 文档示例：
  发 `/compact` 文本，完成信号= `system/compact_boundary` 消息带 `compact_metadata.pre_tokens` 与
  `trigger:"manual"`；需已有对话历史，否则 success 结束 + "Not enough messages to compact."）。
- **模式影响**：`set_permission_mode` 控制请求（**实测 dump** 六 subtype 全 success：initialize/
  list_models/set_model/set_permission_mode(含 bypass)/interrupt(cancel_queued)/rename_session，
  [Mac-claudecode-official@ee46068] `dumps/main-summary.json`；SDK 类型 sdk.d.ts:4389+，mode enum
  default/plan/acceptEdits/dontAsk/auto/bypassPermissions）；`set_model`（sdk.d.ts:4377-4384）。
  注意在途分支已把这些接进 bridge（`docs/2026-09-04-claudecode-official-capability-upgrade-design.md`
  §2/§6；Phase 2 commit a863505）。
- **成功/失败/未知命令**：未知/终端专属命令作为普通文本进模型（无专用错误码）；`--disable-slash-
  commands` 整面关闭；`--bare` 跳过发现。
- **双客户端**：无共享事件总线（CLI 进程级，既有架构事实）；经 CordCode 会话发出的命令只影响该
  会话进程。
- **生产实证（重要）**：CordCode 现有 send_message 隐式通道**已经官方语义地在执行用户定义命令/
  技能**——[Mac] `agent/claudecode/session.go:907-917` 把 `/xxx` 原样写 stdin user message；transcript
  回显 CLI 注入标签 `<command-name>`/`<command-args>`/`<local-command-stdout>`（展示侧由
  `claudecode.go:1546-1568 normalizeClaudeUserText` 清洗）；[Mac] `think.md:1008-1059`（2026-07-04 复盘）
  记录 iPhone 发 `/handoff-doc` 等确被 CLI 执行。**对内置命令的 stream-json 行为未逐条实测**（未核实 #10）。

### 4.3 CordCode Mac 现状（@bdc2cda + 在途分支）

- **list**：未接线。`system/init` 帧到达但 `slash_commands`/`skills` 未消费（§4.1）；`initialize`
  控制请求未使用（Phase 0 探针已证明可行，在 ee46068 分支）。
- **execute**：隐式可用（send_message 原样透传，§4.2 生产实证）。
- 死接口遗产：`CommandDirs`/`SkillDirs`（[Mac] `claudecode.go:1924,1938`）与 `CompressCommand`
  （`:1948` 返回 "/compact"）零消费（core 三接口 `core/interfaces.go:673,696,704` 均无 type assertion）。
- **set_permission_mode 已交付**：[Mac] `handlers.go:4390 handleSetPermissionMode`（spawn `--permission-mode`
  拼接 `session.go:129-131`；运行中 `SetLiveMode`（`session.go:1236`）对 plan/auto 方向显式拒 false，
  其余本地代答——在途 Phase 2 将改为官方 `set_permission_mode` 控制请求直达）。
- capability：`permission_mode`（ModeSwitcher）已广告；无命令/技能位。

### 4.4 iOS 现状（@db7972cf）

- 无 `/` 面板；**Claude Plan 入口已交付**（[iOS] `App/ChatUIKitContainerView.swift:5939
  makePermissionModeMenu` + `SelectionSheets.swift:345 setMode` + `BackendModels.swift:409
  PlanModeEntryAction`；RPC 三件套 `CCCodeBridgeClient.swift:832,844`）——这就是官方模式通道，**不是**
  slash 面。
- `/plan` 文本发出 = 字面文本进模型（不在程序化命令表，CLI 不会当命令处理）——与「Plan 入口已另行
  交付」互不冲突。

### 4.5 接入选项

- **A. list**：消费 `system/init.slash_commands` + `skills`（帧已到，零新 CLI 协议；每次 spawn 刷新，
  目录随 cwd 变化）——supported（dump 实证）。备选：控制面 `initialize.commands`（多拿
  description/argumentHint；注意 initialize 有副作用——first-attached-client-wins、hooks 注册，Phase 0
  已实测可控但在途分支才接线）。缺样本：`system/init` 的 `skills` 数组元素结构未逐字段 dump（未核实 #3）。
- **B. execute**：复用现有 send_message（面板点选 → 插入 `/name ` + 参数提示 argumentHint）——对
  用户定义命令/技能即官方语义，**不是**「把 slash 当 chat 发出去」的错误路径（该错误路径仅指 DSH
  `/plan` 这类 host 命令）。supported（生产实证）。
- **C. 内置命令的官方映射**（面板若收录内置命令）：`/model`→`switch_model`（在途 `set_model` 直达）、
  `/effort`→effort 通道、权限类→`set_permission_mode`、取消→`interrupt`（在途）；`/compact`→发
  `/compact` 文本（官方程序化语义）。mixed：部分 supported（通道已在/在途），部分 deliberately
  unsupported（终端专属命令不收录）。
- **D. Plan**：继续走 permission-mode 入口，**不经 `/`**——not applicable（`/plan` 不在程序化目录；
  官方模式开关已交付）。`/clear`（重置上下文）与 conversation_reset 信号的联动属产品决策项。
- 破坏面：B 无新破坏；A 需注意 `--bare`/`--disable-slash-commands` 不得出现在 spawn 参数（当前没有）。

---

## 5. OpenCode Web（opencode-web）

### 5.1 官方 `/` 面板（源码 @69c172e + 活体 1.18.20）

- **打开方式**：TUI 输入 `/` 补全（[OC] `packages/tui/src/component/prompt/autocomplete.tsx:695`）；
  web/desktop 输入 `/` 弹 popover（[OC] `packages/app/src/components/prompt-input/slash-popover.tsx:22`）。
- **列表数据源（server API，非本地扫描）**：
  - `GET /command`（[OC] `packages/opencode/src/server/routes/instance/httpapi/groups/instance.ts:139`
    `HttpApiEndpoint.get("command",...)`；聚合 `packages/opencode/src/command/index.ts:46` 起：内置
    `init`/`review` + MCP prompts（source:"mcp"）+ **全部技能注册为命令（source:"skill"）**）。
  - `GET /skill`（同文件 `:159-168`；技能发现 `packages/opencode/src/skill/index.ts:173
    discoverSkills`：`~/.claude/skills`、`~/.agents/skills`、项目 `.claude`/`.agents`、config
    `{skill,skills}` 目录、`skills.paths`、`skills.urls`）。
  - `GET /agent`（[OC] `packages/opencode/src/agent/agent.ts:35-55` Info schema；内置 build/**plan**/
    explore/general/compaction/title/summary）。
- **活体（1.18.20，Basic Auth GET）**：`/command` 返回 14 条 = 2 命令（init、review，source:"command"）
  + 12 技能（source:"skill"，模板即 SKILL.md 正文）；`/skill` 11 条带 `location`（横跨
  `~/.agents/skills`、`~/.config/opencode/skills`、`~/.claude/skills`）；`/agent` 7 个（**plan 为
  built-in primary**，build agent 的 permission 表含对 `~/.claude/skills/*` 的 external_directory
  allow——技能库共享的运行时证据）。
- **分组与过滤**：TUI 把 skill 来源**显式过滤出 `/` 面板**（`autocomplete.tsx:451 if
  (serverCommand.source === "skill") continue`，技能走 `/skills` 对话框）；web 端**不排除**，在
  popover 里以 badge 区分（slash-popover.tsx:335-356）。agent 不混在 `/` 列表（subagent 在 `@` 面板）。
- **列表项身份**：`Command.Info {name, description?, agent?, model?, source?, template, subtask?,
  hints[]}`（[OC] `command/index.ts:22`）；技能 `Skill.Info {name, description?, location, content}`。
- **Plan 是否在列表里**：**否**。plan 是 built-in primary **agent**（edit 全 deny、仅允许写
  `.opencode/plans/*.md`，[OC] `agent/agent.ts:156-181`）。**入口类型=agent 切换**。
- **官方测试**：[OC] `packages/opencode/test/agent/agent.test.ts:72 "plan agent denies edits except
  .opencode/plans/*"`；`test/skill/skill.test.ts:94,245,273`；`test/session/prompt.test.ts:1815`；
  路由演练 `test/server/httpapi-exercise/index.ts:142-144`。

### 5.2 官方执行通道

- **`POST /session/:sessionID/command`**（[OC] `groups/session.ts:343`；payload schema
  `packages/opencode/src/session/prompt.ts:1536 CommandInput`）：`{command(必填), arguments(必填
  string), agent?, model?, variant?, messageID?, parts?[仅 file]}`——**活体 OpenAPI**（`GET /doc`，
  1.18.20）与此一致，响应 `SessionV1.WithParts`（创建的消息 + parts）。
- **执行语义 = 展开后走普通发消息路径**（[OC] `prompt.ts:1356 command` 函数）：找不到命令报
  `Command not found` + 可用列表；`$1..$n`/`$ARGUMENTS` 替换（无占位符时参数追加模板尾）；`` !`cmd` ``
  本地 shell 预执行内联；`@file` 由 `resolvePromptParts` 展开；命令 frontmatter 可带 agent/model/
  subtask；最终 `yield* prompt({...})` 进普通 prompt 管线，随后发布事件 **`command.executed`**
  （`prompt.ts:1474`；data `{name, sessionID, arguments, messageID}`，[OC]
  `packages/schema/src/v1/legacy-event.ts:8`）。**要明标：这是官方「展开为 user message」语义**，
  与 Claude 同族，但由 server 端（而非客户端）展开，并有专用 API 与事件。
- **技能执行同 API**：技能已注册为命令（source:"skill"），`POST /session/:id/command` 以技能名为
  command 即可；模型侧另有 `skill` 工具按需加载（[OC] `packages/opencode/src/tool/skill.ts:12`）。
- **模式影响**：无模式字段。agent 切换 = 消息级 `agent` 字段（服务端发现与 session 当前不同则
  `setAgentModel` patch + `session.updated` 事件，[OC] `prompt.ts:672-689`、`session/session.ts:765`）；
  **活体 OpenAPI 另有 v2 面** `POST /api/session/:sessionID/agent`（v2.session.switchAgent）。
- **模式类内置斜杠（TUI 本地入口，server 有 API）**：`/compact`→`POST /session/:id/summarize`
  （payload `{providerID, modelID, auto?}`）、`/share`→`POST /session/:id/share`、`/undo`→`POST
  /session/:id/revert`（[OC] `groups/session.ts:279-383`；TUI 侧 `packages/tui/src/routes/session/index.tsx:472-670`）。
- **双客户端**：`/global/event` SSE 是 server 级广播（首条 `server.connected`、10s `server.heartbeat`，
  [OC] `groups/global.ts:70-96`）；命令执行后第二客户端可见 `command.executed` + 消息流事件；agent
  切换经 `session.updated` 感知（v1 运行时）。

### 5.3 CordCode Mac 现状（@bdc2cda）

- **list**：`GET /agent` 已被消费（[Mac] `agent/opencode-web/agents.go:136 ListAgents` 实现 AgentLister）；
  `GET /command`、`GET /skill` 未接。
- **execute**：`POST /session/:id/command` 未接（grep `/command` 零命中）；发送走
  `/session/<id>/prompt_async`（[Mac] `session.go:270-278`），body 含 per-request `agent` 字段
  （`agents.go:163 resolvePromptAgent`——**agent 切换通道（含 plan agent）在发送层已具备**，显式
  agent 不在注册表会报错不静默回退）。
- SSE 解码无 `command.executed`（[Mac] `agent/opencode-web/events.go:325-383` 事件清单无此项）。
- 模型切换：官方 1.18 无专用端点，pending 记录随下次 prompt 生效（[Mac] `models.go:236`）。

### 5.4 iOS 现状（@db7972cf）

- 无 `/` 面板；输入条有通用 `agentTag`（[iOS] `App/ChatInputAccessoryView.swift:146`）与
  `ChatViewModel+AgentSelection.swift`——**opencode-web 的 plan agent 是否已能经此选择未核实**（#7）。
- 无 permission/mode 入口（opencode-web 不广告 `permission_mode`）。

### 5.5 接入选项

- **A. iOS `/` 面板 → bridge 新 RPC**：list = `GET /command`（命令+技能一列，`source` 可分组）+
  `GET /agent`（已接）；execute = `POST /session/:id/command`。supported（活体 + 源码 + 1.18.20↔dev
  协议 0 diff）。缺样本：无（活体已取）；可选 v2 面（`GET /api/command`、`POST /api/session/:id/agent`）
  语义未验证（#8）。
- **B. Plan = agent 切换**：per-message `agent:"plan"`（CordCode 发送层已具备 resolvePromptAgent）或
  v2 switchAgent；与 `/` 面板正交，agentTag 路径可能已覆盖（未核实 #7）。future（待核实后定级）。
- **C. 模式类动作映射**：compact→`POST /session/:id/summarize`（bridge `compress_context` 是否对接
  opencode-web 未查——capability 表 `compression` 现仅 Codex app_server，未核实 #8 合并追踪）。
- **D. 技能与命令同 API**（OpenCode 特有：不需拆执行面，但 UI 分组仍建议按 `source`）。

---

## 6. Codex Desktop（codex-remote）

### 6.1 官方 `/` 面板（源码 @50fffd5）

- **打开方式**：TUI 输入 `/` 弹内置枚举（[Codex] `codex-rs/tui/src/slash_command.rs:12
  pub enum SlashCommand`，~70 条；动态 service-tier 命令 `tui/src/bottom_pane/slash_commands.rs:84
  commands_for_input`）。**无自定义斜杠命令**（全仓无 `~/.codex/commands` 类机制；`codex-rs/prompts`
  crate 是内置系统提示词不是用户命令）。
- **列表数据源**：TUI 本地枚举 + `model/list` 派生——**协议层没有命令目录方法**（§6.2）。
- **技能（独立子系统，非命令）**：SKILL.md 发现（[Codex] `codex-rs/ext/skills/src/host_roots.rs:73
  roots_from_layer_stack`：项目 config skills、`~/.agents/skills`(:103-108)、`$CODEX_HOME/skills`、
  仓库 `.agents/skills` 向上探测、插件根）；app-server 方法 **`skills/list`**（参数 `{cwds,
  force_reload}`，[Codex] `codex-rs/app-server-protocol/src/protocol/common.rs:809`；`v2/plugin.rs:20`）、
  `skills/extraRoots/set`(:814)、`skills/config/write`(:957)、通知 `skills/changed`(:1851)；TUI `/skills`
  菜单（`tui/src/chatwidget/skills.rs:29`）。
- **列表项身份**：技能目录元素含 SKILL.md 路径与 frontmatter 元数据（`skills/src/model.rs:15`）。
- **Plan 是否在列表里**：TUI **有 `/plan`**（`slash_command.rs` 枚举含 Plan；任务进行中禁用
  `:225 available_during_task`），其动作=设置 collaborationMode（`tui/src/chatwidget/slash_dispatch.rs:84
  apply_plan_slash_command` → `settings.rs:646` → `thread/settings/update` 的 `collaborationMode` 字段）。
  **入口类型=模式开关（collaborationMode）**；协议层无命令语义。
- **官方测试**：[Codex] `app-server/tests/suite/v2/plan_item.rs:34,100`；`collaboration_mode_list.rs:29`；
  `skills_list.rs:48,135`（#42284 新增）；`compaction.rs:247`；`command_exec.rs`。

### 6.2 官方执行通道

- **没有命令 list/execute 面**：`command/*` 全家是 **OS 进程沙箱执行**（[Codex]
  `v2/command_exec.rs:21-30 CommandExecParams` 注释原文 "Run a standalone command (argv vector) in the
  server sandbox without creating a thread or turn"）与 shell 字符串（`thread/shellCommand`，
  `v2/thread.rs:1126`）——**与斜杠命令无关**。TUI 斜杠动作在协议层散落为独立方法：
  `/compact`→**`thread/compact/start`**（common.rs:657；进度走 turn/item 通知，完成发
  `thread/compacted`:1917）；`/model`→`model/list`(:1043)+`thread/settings/update.model/effort`；`/plan`→
  `thread/settings/update.collaborationMode`（或 `turn/start.collaborationMode:972`）；`/review`→
  `review/start`:1037；`/recap`→`getConversationSummary`:1375；`/init`→本地 prompt 当普通消息。
- **技能执行**：模型侧 `skills_list`/`skills_read` 工具（[Codex] `ext/skills/src/tools/mod.rs:58`）+
  提示词目录注入；**用户侧 = `$<skill-name>` mention**：`turn/start` input 的
  `UserInput::Skill{name, path}`（[Codex] `v2/turn.rs:419`；app-server README.md:2119 有 JSON 原文示例）。
- **collaborationMode（Plan）**：`ModeKind{Plan,Default}`（[Codex] `protocol/src/config_types.rs:673`）；
  `turn/start.collaborationMode` 与 `thread/settings/update.collaborationMode` 字段**均标 EXPERIMENTAL**
  （`v2/turn.rs:244`、`v2/thread.rs:265`）；预设列表 `collaborationMode/list`:1112；实验门：未声明
  `experimentalApi` capability 的连接调用实验方法会被拒（`app-server/src/message_processor.rs:903-907`，
  错误文案 `experimental_api.rs:31`）。「Implement this plan?」三选是 TUI 本地编排，无 wire 审批
  （`tui/src/chatwidget/plan_implementation.rs:9`；既有调研已覆盖）。
- **Remote Control（codex-remote 的链路）**：**全量 JSON-RPC 透传，无方法白名单**——[Codex]
  `app-server-transport/src/transport/remote_control/protocol.rs:106` 信封直接包完整 JSON-RPC；
  `client_tracker.rs:94 handle_message` 原样转 `TransportEvent::IncomingMessage`；唯一 host 白名单是
  连接 URL 域名校验（`protocol.rs:193 is_allowed_remote_control_chatgpt_host`）。两道通用门：
  必须 initialize + 实验方法需 capability。⇒ controller（CordCode codex-remote）理论上可调用
  `skills/list`、`collaborationMode/list`、`thread/compact/start` 全部方法面（活体可达性未验，#5）。
- **双客户端**：app-server 事件（turn/item/plan delta）面向所有连接下发；compact 等动作产生的 item
  流同样广播。
- **版本漂移**：装机 0.153.0-alpha.5（ChatGPT.app）vs checkout main@50fffd5——未逐项 diff（#4）。

### 6.3 CordCode Mac 现状（@bdc2cda）

- `collaborationMode` 接线存在但仅服务 plan 审批卡：[Mac] `agent/codex-remote/plan_review.go:162
  startTurnWithCollaborationMode`（`turn/start` 带 mode）、`:194 updateThreadCollaborationMode`
  （`thread/settings/update`）、`:31 planImplementationCodingMessage = "Implement the plan."`。
- **无命令/技能调用**：`skills/list`、`collaborationMode/list`、`thread/compact/start` 均未接
  （[Mac] `agent/codex-remote/` grep 无）。
- RespondPermission 为 `ErrNotSupported`（[Mac] `session.go:121-123`，既有调研已记录）。

### 6.4 iOS 现状（@db7972cf）

- 无 `/` 面板；Codex collaborationMode 无入口（挂起另案）；能批不能开（think.md:3-9 裁决）。

### 6.5 接入选项

- **A. 命令面板**：**不可行（fail closed）**——官方无命令目录+执行协议面；TUI 命令是客户端概念。
  deliberately unsupported。
- **B. 技能面板**：可接 app-server `skills/list` + 用户侧 `$name` mention（`turn/start` input
  Skill item）——supported（源码面齐）；前置：Remote Control 链路活体验证（#5）+ 目标版本核对（#4）。
- **C. 模式动作映射表（客户端自建目录）**：把 TUI 命令子集映射到各自协议方法（compact→
  `thread/compact/start`、model→现有模型通道、Plan→collaborationMode）——属客户端编排非官方 slash
  语义，与 [docs/2026-09-04-codex-ios-plan-mode-entry.md] 挂起另案同一决策域（owner 裁决项）。
  future。
- **D. Plan**：不随 `/` 面板免费获得；开 Plan 仍需 collaborationMode 接线（另案不变）。

---

## 7. Grok Build（grokbuild）

### 7.1 官方 `/` 面板（源码 @72a6125）

- **打开方式**：TUI 输入 `/` 弹下拉（pager 层）。
- **列表数据源（双层）**：
  - pager 本地全集（~60 条，[Grok] `crates/codegen/xai-grok-pager/src/slash/commands/mod.rs:75
    builtin_commands()`）：tutorial、settings、dashboard、…、model、effort、context、compact、…、
    **plan**、view-plan、…、skills、… 等，动作经 `CommandResult::{Action, QueueCommand, PassThrough,
    Error}` 表达（`slash/command.rs`）。
  - shell 端 builtin（agent core 解析）：[Grok] `crates/codegen/xai-grok-shell/src/session/slash_commands.rs:66
    BUILTIN_COMMANDS`（compact、always-approve/yolo、flush、dream、memory、context、hooks-*、plugins、
    reload-plugins、session-info、feedback、deep-research、workflow、goal；`PROMPT_COMMANDS` 含 loop）。
  - **ACP 协议目录（对桥最关键）**：**未用标准 `prompts` 字段**（全仓 grep `PromptDefinition` 0 命中；
    initialize 构建无 `.prompts()` 调用，[Grok] `crates/codegen/xai-grok-shell/src/agent/mvp_agent/acp_agent.rs:492-558`）。
    目录走三条自定义通道：① `InitializeResponse._meta.availableCommands`（pre-session builtin，
    acp_agent.rs:543）；② 会话期 `SessionUpdate::AvailableCommandsUpdate`（builtins+skills+workflows，
    [Grok] `session/acp_session_impl/session_setup.rs:247-281`，ACP 0.10.4 unstable 特性）；③ 拉取式
    ext 方法 **`x.ai/commands/list`**（请求 `{session_id?, cwd?, kind?}`，响应 `{commands:
    Vec<acp::AvailableCommand>, tools?}`，[Grok] `slash_commands.rs:852-871`；分发表
    `extensions/session_admin.rs:53`）。
- **技能**：SKILL.md 发现（[Grok] `crates/codegen/xai-grok-agent/src/prompt/skills.rs:64`；Local 层
  `./.grok/skills`、`./.agents/skills`、`./.claude/skills` → Repo → User（`~/.grok/skills` 等，`~/.claude`/
  `~/.cursor` 兼容受 CompatConfig 门控）→ paths → Server → Bundled）；模型侧 `skill` 工具 +
  `<agent_skill>` 提示行；用户侧 `/name` 斜杠（frontmatter `user-invocable: true` 进目录）。
- **分组**：命令/技能/工作流合成一个 AvailableCommand 列表（builtins 在前，官方测试
  `available_commands_orders_builtins_first`）；同名冲突 skill 降级 `plugin:name`（`PAGER_COMMAND_KEYS`）。
- **Plan 是否在列表里**：TUI 有 `/plan`（pager 命令，[Grok] `pager/src/slash/commands/plan.rs:23-31`
  → `Action::SetPlanMode`/`EnterPlanMode`）；**shell 解析面（BUILTIN_COMMANDS）没有 plan**。
  **入口类型=模式开关（session mode）**：`session/set_mode` ACP 方法（[Grok]
  `pager/src/app/dispatch/modes.rs:62` `SessionModeId::new("plan")`；shell 侧 `acp_agent.rs:2229-2250
  set_session_mode`；模式枚举 `xai-grok-tools/src/types/session_mode.rs:13` default/plan/ask）+
  逐 prompt `session/prompt._meta.mode`（acp_agent.rs:1113-1118）+ 模型侧 `enter_plan_mode` 工具。
  无 `grok plan` 子命令（CLI 子命令枚举无 plan，[Grok] `pager/src/app/cli.rs:8-149`；仅顶层
  `--no-plan` flag）。
- **官方测试**：[Grok] `slash_commands_tests.rs`（~40 例）；`acp_session_tests/plan_mode_*`；
  `dispatch/tests/modes.rs:149-316`（`slash_plan_no_args_not_in_plan_enters_plan_mode` 等）；
  `replay_buffer_send_update_tests.rs:443`。

### 7.2 官方执行通道

- **执行 = `/cmd args` 作为 prompt 文本发 `session/prompt`**：pager 的 ACP 命令包装为透传
  （[Grok] `pager/src/slash/acp_command.rs:1-6` `AcpSlashCommand`）；`/compact` 也是 `QueueCommand
  ("/compact …")`（`commands/compact.rs:20-28`）；shell 端在 `session/acp_session_impl/turn.rs:460`
  调 `slash_commands::resolve_human_intent()` 从 prompt 文本解析 → `SlashCommandOutcome::Builtin`
  （core 执行动作，无模型往返）或 `::InvokeSkill`（读 SKILL.md、`$ARGUMENTS`/`$SKILL_DIR` 替换，包
  `<skill_information>` 信封随 `<user_query>` 送模型，`slash_commands.rs:1386,1470`）。
- **`session/prompt` 请求体**：`{"sessionId", "prompt":[{type:"text",text:"/cmd args"}], "_meta":
  {promptId?, mode?("agent"|"ask"|"plan"), verbatim?, sendNow?, screenMode?}}`（[Grok]
  `acp_agent.rs:998-1161`；pager 构造 `app/effects/mod.rs:1233`）。
- **pager 本地命令不上 prompt**：plan/model/effort 等经各自 Effect → ACP 方法（`session/set_mode`、
  `session/set_model`）。**要明标：把 `/plan` 当 prompt 文本发给 grok 不会进 plan**（shell 无此
  builtin）。
- **模式/权限/模型**：PermissionMode 枚举含 Plan 值但**仅 BypassPermissions 在 spawn 接线**（[Grok]
  `xai-grok-agent/src/config.rs:928-951` 注释原文）；yolo 开关 ACP 化为 `session/new _meta.yoloMode` +
  通知 `x.ai/yolo_mode_changed`；模型切换 `session/set_model`（目录 `initialize._meta.modelState` /
  ext `x.ai/models/list`）。
- **双客户端**：共享 session 的命令执行经正常 session update 广播；`AvailableCommandsUpdate`
  forwarded-but-not-persisted（官方测试名即语义）。
- **版本漂移**：装机 1.0.13 vs checkout 1.0.16（1.0.14 模型按 effort 声明 id / interjection 原子投递；
  1.0.15 session 关闭不阻塞；1.0.16 企业签名 requirements、MCP session bind 注入、**斜杠建议下拉
  长名修复**（UI 层）、子 agent 等待上限）——目录/执行协议面无记录性变更，未逐项 diff（#6）。

### 7.3 CordCode Mac 现状（@bdc2cda）

- **list**：未接。[Mac] `agent/grokbuild/acp_types.go:102 initializeResult` 只解码
  protocolVersion/agentCapabilities/agentInfo/authMethods/`_meta`（仅 `modelState` 被消费，
  `session.go:309-311`）——`availableCommands` 在 `_meta` 里但未解码；catalog 子进程
  （`grok agent --no-leader stdio`，[Mac] `catalog_session_list.go:78-80`）只用于 `session/list` +
  模型目录，未调 `x.ai/commands/list`。
- **execute**：prompt 文本通道未用于命令；SetMode 只写内存（[Mac] `grokbuild.go:573`；全包无
  `session/set_mode` 调用）；模型切换是真实接线（`session.go:480` `session/set_model`）。
- plan 审批卡走 leader 广播 `x.ai/exit_plan_mode`（[Mac] `leader_subscriber.go:672`，已交付）。

### 7.4 iOS 现状（@db7972cf）

- 无 `/` 面板；Plan 选择不生效（[Mac] `think.md:11` 裁决「Grok iOS Plan 只写 agent 内存」）。

### 7.5 接入选项

- **A. list**：解码 `initializeResult._meta.availableCommands`（每 turn ACP 子进程与 catalog 子进程
  握手即有）+ catalog 子进程调 `x.ai/commands/list`（支持 cwd skill 发现）——supported（源码面齐；
  活体未验 #6）。缺样本：装机 1.0.13 的 `availableCommands` 实际内容（可用 catalog 子进程握手 dump）。
- **B. execute**：对 shell builtin 与 user-invocable skill = prompt 文本（现有 Send 通道即可，官方
  语义）；**pager 本地命令（plan/model/effort 等）必须映射各自 ACP 方法**（set_mode/set_model），
  不能当 prompt 文本。mixed。
- **C. Plan**：接 `session/set_mode`（官方 ACP 方法，CordCode 从未发送）——supported（源码面齐；
  双客户端模式下 Desktop 模式指示漂移问题同 Codex #4 待验）。
- **D. 破坏面**：把 `/plan` 文本发出去 = 官方明确不会进 plan（fail closed 已在 §7.2 钉死，禁止当
  fallback）。

---

## 8. 跨 backend 对照

### 8.1 总对照（细化 §1）

| 维度 | DSH | Claude | OpenCode | Codex | Grok |
| --- | --- | --- | --- | --- | --- |
| 官方 list 面 | `commands/list` + `skill.list`（rc.2）/`skills/list`（master），session 域 | `system/init.slash_commands`+`skills`（帧已到）/ `initialize.commands` | `GET /command`（混排）+`/skill`+`/agent` | ❌（命令无；技能 `skills/list` 有） | `_meta.availableCommands` + `x.ai/commands/list` |
| 官方 execute 面 | 命令 `commands/execute`；技能=prompt 文本+host 注入 | ❌（=prompt 文本，CLI 入口展开） | `POST /session/:id/command`（server 端展开） | ❌（散落方法；技能 `$name` mention） | ❌（=prompt 文本，shell 解析） |
| 命令是否 user message | 否（RPC+durable 日志） | 是（官方语义） | 展开后是（server 展开+`command.executed`） | — | 是（官方语义） |
| 技能是否 user message | 是（host 注入） | 是 | 是（同命令 API） | `$name` 进 input（Skill item） | 是（信封注入） |
| 执行可观测事件 | `command/run`/`command/done`（广播） | transcript 注入标签（本地） | `command.executed`（SSE 广播） | — | session update |
| 模式开关通道 | `/plan`、`/permission` 都是命令 | permission mode（`set_permission_mode`） | agent 切换 | `collaborationMode` | `session/set_mode` |

### 8.2 「进入计划模式」对照（与 slash 调研解耦）

| Backend | 进 Plan 的官方动作 | 是否 slash | iOS 今天能否进入 | `/` 面板能否顺便覆盖 |
| --- | --- | --- | --- | --- |
| DSH | `/plan` 命令（`commands/execute`；off 退出；带消息 steer 注入） | **是**（host 真命令） | 否（当聊天发无效） | **是——免费获得**（execute 通道接上即同语义；后续 plan-review 卡 iOS 已能接住） |
| Claude | permission mode（Shift+Tab / `--permission-mode` / `set_permission_mode` 控制请求） | 否（交互式 `/plan` 是 prompt 前缀；程序化目录无此项） | **是**（已交付 permission-mode 入口） | 不需要也不会（面板不带来新 Plan 能力） |
| OpenCode | 切到 `plan` agent（per-message `agent` 字段 / v2 switchAgent） | 否（agent 切换） | 未核实（agentTag 通用入口存在，opencode-web 数据面已接，端到端未验 #7） | 否（agent 面板的事，非 `/`） |
| Codex | `collaborationMode`（`thread/settings/update` 或 `turn/start`，EXPERIMENTAL） | TUI 是；协议层是模式字段非命令 | 否（挂起另案） | 否（无命令 execute 面；开 Plan 仍需 collaborationMode 接线） |
| Grok | `session/set_mode`（TUI `/plan` 的动作本体；另有 prompt `_meta.mode`） | TUI 是（pager 本地）；协议层是模式方法 | 否（SetMode 只写内存） | 部分——`/plan` 项须映射 `session/set_mode`（新接线），纯文本发送官方明确不进 plan |

### 8.3 owner 两问的答案

> **「iOS 做 `/` 面板之后，DSH 的 Plan 会不会免费获得？」——会，且只有 DSH 会。**
> `/plan` 是 DSH host 端真命令（活体目录 6 条含 plan）；执行通道 `commands/execute` 与 CordCode 已有
> 生产使用的 `/permission` 同一条；选中后行为与 Mac 官方 web 完全同语义（含 pending-intent、
> `plan/mode` 事件、后续 plan-review 审批卡——iOS 已能接住）。DSH 是五家中唯一「命令目录 + 独立
> 执行 RPC + Plan 在目录里」三件全齐的。

> **「Claude / Codex / Grok / OpenCode 会不会也因此能开 Plan？」——按家：**
> - **Claude：不会，也不需要**——`/plan` 不在程序化命令目录（实测 48 项无 plan），Plan 官方入口是
>   permission mode，iOS 已有等价入口（已交付）。`/` 面板对 Claude 的增益是命令/技能目录
>   （`system/init` 帧已在，只差消费）。
> - **Codex：不会**——官方没有命令 list+execute 协议面（TUI 概念）；Plan=collaborationMode，开 Plan
>   仍是挂起另案，`/` 面板不改变这件事。
> - **Grok：不会自动**——命令执行=prompt 文本，但 `/plan` 在官方 TUI 是 pager 本地动作（映射
>   `session/set_mode`），shell 解析面无 plan；面板要把 `/plan` 映射到 `session/set_mode` 才能开
>   （新接线，非 execute 通道；`session/set_mode` 是官方 ACP 方法，CordCode 从未发送）。
> - **OpenCode：不会**——plan 是 built-in agent 不是命令，`/` 面板不含 plan；但 agent 切换官方通道
>   已存在（per-message `agent` 字段，CordCode 发送层已具备），iOS agentTag 可能已部分覆盖（未核实 #7）。

---

## 9. 未核实清单（集中；正文已就地标注编号）

1. **DSH 技能执行端到端**：`/name` 字面文本经 CordCode `send_message` → dsh-web `session.prompt` →
   host pre-step 注入的完整链路未做活体验证（源码+官方注释背书；探针仅覆盖 list 类）。
2. **DSH rc.2 与 master 的事件载体差异**：`API_REMOTE_FORWARDED_EVENTS`（mux 转发 allowlist）核实自
   master；0.1.1-rc.2 的 apiproxy 形态未逐项对照（影响第二客户端对 `commands/change` 类事件的感知面）。
3. **Claude `system/init` 的 `skills` 数组元素结构**：dump 确认键存在与 `slash_commands` 内容（48 项），
   `skills` 元素的逐字段结构未 dump；`initialize.commands` 的 description 尾缀 `(user)` 是唯一来源
   标记，未见独立 source 字段。
4. **Codex 装机版与 checkout 差异**：ChatGPT.app codex-cli 0.153.0-alpha.5 vs checkout main@50fffd5
   未逐项 diff（skills/app-server 面）；TUI `/plan` 双客户端模式下 Desktop 模式指示漂移问题（另案
   文档 §4.4 既有问题）未验。
5. **codex-remote 链路活体可达性**：Remote Control 全量透传为源码结论（无方法白名单）；controller
   实际调用 `skills/list` / `collaborationMode/list` / `thread/compact/start` 未活体验证，且现有
   initialize 是否声明 `experimentalApi` capability 未核（experimental 方法依赖它）。
6. **Grok 装机 1.0.13 目录面活体**：`availableCommands` / `x.ai/commands/list` 在装机版的实际内容
   未 dump（可用 catalog 子进程握手取证）；1.0.13↔1.0.16 相关面未逐项 diff。
7. **iOS agent 选择器对 opencode-web plan agent 的端到端可用性**：数据面（ListAgents/`GET /agent`、
   per-send agent）已接；agentTag → 选择 plan agent → 下一条消息带 `agent:"plan"` 的完整链路未验。
8. **OpenCode v2 面**：活体 OpenAPI 确认 `GET /api/command`、`POST /api/session/:id/agent`（switchAgent）
   等路由存在于 1.18.20，语义未调用验证；bridge `compress_context` 是否对接 opencode-web
   （`POST /session/:id/summarize`）未查（capability 表 `compression` 现仅 Codex app_server）。
9. **Claude 内置命令的 stream-json 行为矩阵**：实证仅覆盖用户定义命令/技能（transcript 标签 + 07-04
   复盘）；内置命令（如 `/compact`）经 stdin user message 的逐条行为未实测（官方文档覆盖 -p 模式，
   stream-json 输入模式同构推定）。
10. **`claude -p "/compact"` 一次性调用**（空会话无 resume）：官方文档未逐字覆盖（SDK 文档要求已有
    历史）；对本调研结论无影响（CordCode 走常驻会话）。

---

## 附：调研执行记录

- 方法：主会话 P0 来源清单 + 前置文档（Plan 审批调研 v1.5、Codex 计划入口另案、think.md 索引、双仓
  CLAUDE.md、GO_BRIDGE_ARCHITECTURE.md、IOS_MAC_INTERACTION_FLOW.md grep）+ 7 路并行只读探查
  （DSH/OpenCode/Codex/Grok 上游源码、CordCode Mac 接线、iOS 现状、Claude 官方文档）+ 主会话亲验
  （活体只读探针：DSH 0.1.1-rc.2 双目录、OpenCode 1.18.20 三列表+OpenAPI；Claude CLI 2.1.234 仓内
  dump 复核：`system/init.slash_commands` 与 `initialize.commands`；本机技能目录/运行进程/版本锚点；
  在途分支 ee46068 的 Phase 0-2 证据链）。
- 全程只读：两 CordCode 仓、四上游 checkout 零修改；本报告文件为唯一新增；探针均为 list/catalog 类
  只读请求，未执行任何命令、未发送任何消息。
- 时间：2026-09-04。
