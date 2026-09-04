# 调研指令：五 backend「/」命令与技能 — Mac 实现/调用，以及如何接到 iOS

发给：**只读分析师 agent**（可 source-first 读官方源码与 CordCode 双仓，**禁止改代码、禁止立项实施方案、禁止开始实现**）。

- 日期：2026-09-04
- 发起人：owner
- 产出：一份调研报告（建议文件名 `docs/YYYY-MM-DD-slash-command-skill-cross-backend-survey.md`，写在 **Mac 仓**）
- 性质：事实输入。列出「官方有什么 / CordCode 已经接到哪一层 / iOS 缺什么 / 接入选项」。**不替 owner 决定 UI，不写 PR 计划。**

---

## 0. 你要回答的产品问题

owner 观察到：DeepSeek Harness 官方 web 输入 `/` 会弹出命令 + 技能列表（截图已归档）。其中：

- 命令例：`plan`（Enter or leave plan mode）、`permission`、`model`、`compact`、`export`、`goal`、`feedback`
- 技能例：`audit-plan`、`exec-plan`、`handoff-doc`、`ios-real-device-doc`、`skill-creator`…

选中后**执行该命令/技能**，不是把 `/plan` 当用户聊天发出去。

因此 iPhone 一直「不能开 DSH 计划模式」，根因更像是 **iOS 没有官方 `/` 面板**，而不是再做一个孤立的 Plan 开关。

owner 想要评估的产品方向：

1. iPhone 输入 `/`，或点一个 `/` 按钮，弹出**当前 backend 的命令和技能列表**；
2. 用户点选后，按该 backend **官方执行通道**跑起来（与 Mac 官方客户端同语义）；
3. **五个现役 backend 都要覆盖**，不是只做 DSH。

五个现役产品面（CLAUDE.md lineup，2026-09-04）：

| 产品名 | CordCode backend id | 官方源码（本机只读 checkout） |
| --- | --- | --- |
| Claude Code | `claude` / `claudecode` | 无独立仓；本机 `claude` CLI + 官方 SDK 类型 + CordCode `agent/claudecode` |
| Codex Desktop | `codex-remote` | `/Users/jacklee/Projects/codex` |
| Grok Build | `grokbuild` | `/Users/jacklee/Projects/grok-build` |
| DeepSeek Harness | `dsh-web` | `/Users/jacklee/Projects/deepseek-harness` |
| OpenCode Web | `opencode-web` | `/Users/jacklee/Projects/opencode` |

退役目录 **只读、禁止当实现落点**：`agent/{codex,codex-web,dsh,opencode}`。

---

## 1. 硬约束（违反即报告作废）

1. **只读。** 不改 CordCode、不改上游 checkout、不 stash、不合 main、不装包。发现必须改代码才能验证的项，标「未核实 / 需活体」，不要为了闭合去改。
2. **P0 来源门。** 读任何源码之前、形成任何「源码形状」结论之前，为**每一个**仓库/工作树记录：

   ```text
   仓库路径=<绝对工作树路径>
   分支=<精确分支名；游离必须明示>
   提交=<完整哈希>
   未提交状态=<干净，或逐项列出>
   任务预期分支=<本指令或 CLAUDE.md>
   配套仓库路径/分支/提交=<Mac ↔ iOS>
   上游路径/分支/提交=<该 backend 官方仓>
   预期产品特性=斜杠命令/技能的 list + execute
   ```

   相对路径 `../cordcode-ios` **不是**已授权工作树。先 `git worktree list`，再配对。不要默认 `main`，也不要沿用上一任务的 plan-approval 工作树除非路径+分支对得上。
3. **上游源码优先。** 每个 backend 先读**官方产品**里 `/` 面板、命令表、技能表、执行 call site、测试；再读 CordCode adapter。禁止用 CordCode 旧 adapter 或另一家 backend 的形状去猜这一家。
4. **目标版本。** checkout 的 commit 必须写进报告。若本机安装的 CLI/App 版本与 checkout 不同，以**用户机器上实际跑的目标二进制**为准，checkout 只作对照并标版本漂移。
5. **样本纪律。** request/response/事件形状必须有：官方 schema/测试，或脱敏真实样本。没有样本的项 = 未核实或移出能力，禁止「实现期再确认」放行。
6. **Fail closed。** 未知 generation、未接线的 execute、把 slash 当 chat 发出去，都要明确写成不可行，不要设计 fallback 假装成功。
7. **不要写设计方案正文。** 可以列「接入选项 A/B/C + 各自证据缺口」，不要写「建议 Phase 1 改这些文件」。UI 文案、按钮位置留给 owner。
8. **真机 / UI 自动化不做。** 不点 Codex/Grok/DSH 官方窗口。Mac 侧可用只读：源码、测试、已有日志、Management API、本机配置文件。
9. **测试文件若必须落盘，只用 `/tmp`，调研结束删除；不要写进仓库。**

---

## 2. 先读这些（避免重复劳动）

按这个顺序建立上下文，**不要从零再做一遍计划审批调研**：

| 文档 | 用途 |
| --- | --- |
| 本仓 `CLAUDE.md` + 配套 iOS `CLAUDE.md` | 来源门、backend 运行模型、跨仓纪律 |
| `GO_BRIDGE_ARCHITECTURE.md` | 各 backend 事件/capability 边界 |
| `docs/2026-09-03-plan-mode-cross-backend-survey.md` | Plan 审批门形态；**本任务不是再做 plan_review 卡** |
| `docs/2026-09-04-codex-ios-plan-mode-entry.md` | Codex：iPhone 能批、不能开 Plan；Plan 是 collaborationMode 不是 slash |
| `think.md` 后续案「iOS 进入 Codex / DSH / Grok 计划模式」 | 已挂起的入口，本调研要判断 slash 面板是否覆盖它们 |
| iOS `IOS_MAC_INTERACTION_FLOW.md`（配套仓） | hello/capability/session 同步，不要只看 Mac 猜 iOS |

**已经成立、不要推翻去重查的产品事实（除非你找到反证）：**

- DSH：Mac「标准」套餐 + 官方 `/plan`（`commands/execute`）才能进计划；iPhone 把 `/plan` 当聊天发出去无效。
- Codex：Plan = `collaborationMode` plan/default；批准 = 代发 `"Implement the plan."`。官方 TUI「Implement this plan?」不是 wire 审批。
- Claude：Plan = permission-mode；iPhone **已经能**开计划模式（与 slash 不是同一入口）。
- Grok：iPhone 选 Plan 目前不启动 grok plan（SetMode 只写内存）。
- OpenCode 计划审批卡尚未做。
- 计划**审批卡**（全文 + 批准/打回/放弃）已在 grok/claude/dsh/codex（Mac 发起）交付。本调研不改这条语义。

owner 原话要点：之所以没做 iOS 开 Plan，是因为 **Plan 在 DSH 上本质是 `/` 命令的一项**；他想先搞清楚五家 Mac 端 `/` 怎么实现、怎么调用，再谈 iOS 接入。

---

## 3. 必须区分的概念（报告里不许混用）

对每个 backend 把用户能在 `/` 里看到的东西拆开标类型。至少这几类：

| 类型 | 典型例子（DSH 截图） | 常见执行方式 |
| --- | --- | --- |
| **slash 命令** | `plan`、`compact`、`export` | 官方 `commands/execute` 或等价 RPC，**不是** user message |
| **技能 / skill** | `exec-plan`、`handoff-doc` | 可能是工作区 SKILL.md、斜杠技能、或注入 prompt；参数/文件依赖可能不同 |
| **模式开关** | DSH `permission`；Claude Plan；Codex Plan | 可能是 permission preset / permission-mode / collaborationMode，**不一定出现在 slash 列表** |
| **选择器** | DSH `model` | 可能已有 iOS 模型面板，slash 只是另一入口 |
| **agent 切换** | OpenCode `plan` agent | 不是命令 |
| **纯本地 UI** | copy、打开文件 | 无 wire |

把「进入计划模式」标成上述哪一类。**禁止**默认五家的 Plan 都是 `/plan`。

还要区分三层「Mac 端」：

1. **官方客户端**（Codex.app / grok TUI / `dsh web` / `claude` CLI / OpenCode Desktop）— `/` 面板的真正实现通常在这里。
2. **CordCode Link + go-bridge adapter** — 有没有 list/execute 接线、有没有 capability。
3. **iOS** — 现在几乎肯定没有通用 `/` 面板。

结论必须写清：证据来自 (1) 还是 (2)。不要把官方 web 截图当成 CordCode 已实现。

---

## 4. 每个 backend 必须交的事实表

对 **Claude / Codex Desktop / Grok / DSH / OpenCode** 各做一节，结构相同。

### 4.1 官方 `/` 面板（源码）

- 用户怎么打开：键入 `/`、按钮、命令面板快捷键？
- 列表数据从哪来：硬编码、`commands/list`、扫描 `.claude/skills` / `.agents/skills`、MCP、workspace SKILL.md？
- 命令 vs 技能在 UI 上是一列还是分组？过滤/搜索？
- 列表项身份：name、description、参数 schema、是否需要确认、是否会进 transcript。
- **Plan / 进计划模式** 是否在这个列表里？若否，官方入口是什么（你必须写入口类型，见 §3）。

锚点要求：官方仓路径 + 完整 commit + 符号/文件:行号 + 对应测试（有则写）。

### 4.2 官方执行通道（调用方式）

对「选中列表项之后」逐项写：

- 方法名 / HTTP path / RPC method（原文）
- 请求体关键字段（命令名、session id、参数、是否 empty args）
- 成功/失败/未知命令的官方行为
- 是否改变 session 模式（plan on/off、permission preset、model、agent）
- 是否等于发一条 user message（若是，必须明文标出，这通常是 iOS 当前错误路径）
- 双客户端：Mac 官方 UI 与 CordCode 同时执行会不会抢、是否 first-answer-wins、有无广播

**DSH 至少核清：** `commands/execute`（已知 `/plan` 走这条）是否也执行 skill；skill 是否另一条 API；`permission`/`model` 是命令还是已有 preset/model 通道的别名。

**Codex 至少核清：** TUI slash（如 compact）vs collaborationMode Plan 是不是两套；有没有 `command/list` 之类 app-server 方法；Remote Control 是否透传。

**Claude 至少核清：** slash 命令、skill、permission-mode Plan 三套关系；bridge 已有 `set_permission_mode` 与 slash execute 是否独立。

**Grok / OpenCode：** 先证明官方有没有「命令目录 + 执行」；没有就写「官方无此功能 / 仅 TUI 本地」，不要发明 `/plan`。

### 4.3 CordCode Mac 现状

在 `agent/<id>/`、`go-bridge/`、`core/interfaces.go`、hello_ack capabilities 里找：

- 有没有 list 命令 / list 技能
- 有没有 execute 命令 / execute 技能
- 有没有把 slash 当 `send_message` 发出去的错误路径
- capability 有没有可广告的位（没有就老实说没有，不要建议先手写广告）
- 退役 adapter 里的旧实现只能当历史坑，不能当现役协议证据

### 4.4 iOS 现状

在配套 iOS 仓找：

- 输入条有没有 `/` 按钮或 slash 补全
- 已有的模式/权限/模型入口（Claude Plan、DSH 预设、模型选择器）会不会和 `/` 面板重复
- 若执行成功，现有 session_sync_v2 / 计划卡 / 权限卡能不能接住后续事件（例如 DSH `/plan` 之后的 plan_review 卡）

### 4.5 接到 iOS 的**选项清单**（不是方案）

每家只列证据支撑得住的选项，例如：

- A. iOS `/` 面板 → 新 bridge RPC `list_commands` / `execute_command`（需官方 list+execute 都存在）
- B. 复用已有通道（DSH `commands/execute`、Claude `set_permission_mode`、Codex `collaborationMode`）
- C. 无官方 execute：iOS **不能**做，或只能做本地 UI
- D. skill 与 command 必须拆开（参数、文件、工作区依赖）

每个选项写：**已有锚点 / 还缺的样本 / 会破坏什么已交付行为**（尤其不要把 slash 当 chat 发）。

标四态：`supported` / `deliberately unsupported` / `not applicable` / `future`。

---

## 5. 跨 backend 对照（报告末尾必须有）

一张总表，列至少：

| Backend | 官方有 `/` 列表？ | 列表含命令？含技能？ | 执行 API | Plan 是否在 `/` 里 | CordCode 已接线 list？execute？ | iOS 现状 | 接入最小缺口 |
| --- | --- | --- | --- | --- | --- | --- | --- |

另附一张 **「进入计划模式」对照**，避免和 slash 调研搅在一起：

| Backend | 进 Plan 的官方动作 | 是否 slash | iOS 今天能否进入 | slash 面板能否顺便覆盖 |
| --- | --- | --- | --- | --- |

明确写出 owner 关心的那句是否成立：

> 「iOS 做 `/` 面板之后，DSH 的 Plan 会不会免费获得？」  
> 「Claude / Codex / Grok / OpenCode 会不会也因此能开 Plan？」

后一句的答案必须按家给，允许四家是否。

---

## 6. 建议阅读顺序（省时间）

1. **DSH**（owner 截图已钉死体验；已知 `commands/execute` + `/plan`）。先把命令 vs 技能、execute vs chat 钉死，作为对照标尺。
2. **Claude**（slash + skill + 已有 iOS Plan 模式，最容易和 `/` 面板重复）。
3. **OpenCode**（command/skill/agent 容易混；必须对**本机实际 serve 版本**，不要只读 dev checkout）。
4. **Codex**（slash vs collaborationMode 必须拆开；只读 `codex-remote`，不要碰退役 `codex`/`codex-web` 当现役）。
5. **Grok**（可能没有 DSH 那种 slash 目录；没有就诚实写没有）。

五家都要交 §4 的表，即使结论是「官方无 list」。

---

## 7. 交付物

1. Mac 仓一篇调研 md，文首：日期、只读声明、完整来源清单、上游 commit。
2. §1 总表 + 五节分 backend + §跨 backend 对照 + 「未核实清单」。
3. 每条形状声明带锚点：`仓库@commit  文件:行  符号`。原始日志/截图与源码解释分开标。
4. 未核实项集中列表，不要散落在正文却不进清单。
5. **不要**附带补丁、文件改动列表、Phase 排期。owner 问「怎么接到 iOS」时，用 §4.5 选项表回答即可。

写完后用中文给 owner 一段话：五家谁有真正的 `/` list+execute、DSH 的 Plan 会不会随面板免费获得、哪家必须继续用现有模式开关而不是 slash。

---

## 8. 分析师开工时贴的来源提示（仍须你自己实测覆盖）

本机默认官方 checkout（**开始读之前仍要 git 核实分支/commit/是否干净**）：

```text
Codex     /Users/jacklee/Projects/codex            预期 main
Grok      /Users/jacklee/Projects/grok-build       预期 main
DSH       /Users/jacklee/Projects/deepseek-harness 预期 master；安装版 CLI 可能与 checkout 不同，以安装版为准
OpenCode  /Users/jacklee/Projects/opencode         预期 dev；产品连的是本机 serve，必须对安装/运行版本
Claude    本机 `claude` CLI + 官方 SDK 类型；CordCode agent/claudecode
```

CordCode 逻辑仓：`cordcode-macbridge` + `cordcode-ios`。工作树可能是 `main`，也可能仍有 `plan/approval-layer` / `plan/approval-layer-ios`。以 `git worktree list` + 当前任务为准，**不要猜**。

DSH 官方 `/` 面板截图（owner 2026-09-04）：命令含 `plan` / `permission` / `model` / `compact` 等，技能含 `audit-plan` / `exec-plan` / `handoff-doc` 等；输入条已显示 `Workspace Write` 与模型名。报告里描述官方 UX 时可以引用这些项名为对照，但 **列表完整性以源码/活体为准，截图不是完整目录**。
