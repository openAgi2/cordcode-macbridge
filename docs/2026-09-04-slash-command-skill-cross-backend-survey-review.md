# 评审报告：五 backend「/」命令与技能调研

- 评审日期：2026-09-04
- 评审对象：`docs/2026-09-04-slash-command-skill-cross-backend-survey.md`（650 行，只读调研档；HEAD `b1b08e8` 已入库）
- 评审性质：独立可复查核验——来源清单复核 + 承重锚点抽查（本仓亲验 + 上游 Explore 子代理 + OpenCode 活体 list GET + Claude Phase 0 dump）。**不修改被评审文档**。
- 评审结论：**有条件通过（结论层可信，作立项输入前须修 A 级项）**。owner 两问的答案、五家 list/execute 形态分类、`~/.agents/skills` 共享库、core 死接口，均经独立核验成立。存在 **2 条 A 级**（Codex 权限应答现状写错；DSH iOS 权限入口引用退役 backend）和若干 B 级行号/计数偏差。未核实清单 10 项诚实，#3 其实可被已引用 dump 部分关闭。

---

## 1. 评审来源清单（评审时点）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=b1b08e812e1198acb7ca5d14cd0a96387dadd3bb（调研落盘；调研 §0 钉的开始提交=bdc2cda，其后仅本 docs 提交）
未提交状态=未跟踪 docs/2026-09-04-slash-command-skill-ios-survey-brief.md（owner 指令）+ 本评审文件
任务预期分支=只读调研落盘当前工作树 docs/
配套 iOS main=/Users/jacklee/Projects/cordcode-ios  main  db7972cfa3f5fdb995c7b5a7e5117dd60c0d88b3（与调研一致；plan 卡已合入）
辅助 iOS 工作树=/Users/jacklee/Projects/cordcode-ios-plan-approval  plan/approval-layer-ios  bf9a8d9b（调研后前进，本轮未当分析基线）
辅助 Mac=/Users/jacklee/Projects/cordcode-macbridge-claudecode-official
  claudecode/official-capability  68f04af（调研钉 ee46068；dumps 仍在，本轮按 dump 文件核验）
上游（与调研 pin 一致）：
  codex             50fffd5ed367aa99491d9ec58575626fce4e9dd4
  grok-build        72a61251fcffb464bcc687aeb5a998e5a98ec0c9
  deepseek-harness  49a606bc5b5934603f22a26957a07dc799ab0291
  opencode          69c172e8a7c0086887b1f93ed5a162f14b6aa0c5
活体：dsh 0.1.1-rc.2 :3080 pid 1055；opencode 1.18.20 :4096 pid 944；claude 2.1.234；grok 1.0.13
本轮活体：OpenCode GET /command /skill /agent（Basic Auth，只读）；未打 DSH execute、未发消息
```

调研 §0「开始时干净、提交 bdc2cda」与当前 HEAD 不矛盾：那是调研开工身份；本文件随后以 `docs(survey)` 入库。

---

## 2. 决定性结论（全部成立）

| # | 结论 | 独立证据 |
|---|---|---|
| 1 | **只有 DSH 与 OpenCode 是 list+execute 双全，形态不同** | DSH：`commands/list`+`commands/execute`（Typert Remote）+ 技能无 execute。OpenCode：`GET /command` + `POST /session/:id/command`（server 展开后走 prompt）。源码成立 |
| 2 | **DSH 是唯一独立命令执行 RPC**；技能=`/name` 文本 + host pre-step | `commands/src/index.ts` execute；`ui-skill` 注释 "lands plain text"；`tool-skill` pre-step。CordCode 生产先例仅 `/permission`（`permission_mode.go:27,111-128`） |
| 3 | **Claude 无 execute RPC**；官方语义=prompt 文本；CordCode `send_message` 已是该通道 | Phase 0 dump：`system/init.slash_commands` 48 项无 plan；`initialize.commands` 48 项含 `argumentHint`。`session.go:907-916` 原样写 stdin。`think.md:1008-1059` `/handoff-doc` 生产实证 |
| 4 | **Grok 执行=prompt 文本；`/plan` 必须 `session/set_mode`**；桥从未发送 | shell `BUILTIN_COMMANDS` 无 plan；pager 有 `/plan`→`SessionModeId::new("plan")`。`grokbuild.go:573 SetMode` 只写内存；包内 `"session/set_mode"` 零命中（勿与 `session/set_model` 混淆） |
| 5 | **Codex 无命令目录+执行协议面**；`command/exec` 是 OS 沙箱 | `v2/command_exec.rs:21-22` 原文。TUI `SlashCommand` 是客户端枚举。技能走 `skills/list` + `UserInput::Skill` |
| 6 | **DSH `/plan` 会随 `/` 面板免费获得，且只有 DSH 会** | `/plan` 是 host 真命令（`plan-mode/src/index.ts:225-228`，hint `[off|message]`）。execute 与已有 `/permission` 同通道。其余四家 Plan 不在命令 execute 面上（Claude=permission mode 已交付；Codex=collaborationMode 另案；Grok=set_mode 新接线；OpenCode=plan agent） |
| 7 | **`~/.agents/skills` 被多家消费** | Claude dump 的 `slash_commands` 前 7 项即 audit-plan/exec-plan/handoff-doc/…；OpenCode 活体 `/command` 含同一批 `source:skill`。与调研描述同形 |
| 8 | **`CommandProvider`/`SkillProvider` 零消费**；hello_ack 无命令/技能位 | 全仓无 `.(core.CommandProvider)` 断言。`deriveBackendCapabilities` + StaticCapabilities 无 command/skill 位。立项可对齐 `permission_mode` 范式——建议成立 |

OpenCode 活体（本轮，1.18.20 :4096）：`GET /command` **14** = 2 command（init/review）+ 12 skill——与调研逐项一致。`GET /agent` **7** 含 `plan`（primary）。`GET /skill` 现为 **12**（调研写 11，见 B5）。

---

## 3. A 级修订项（引用前必须修正）

**A1. Codex `RespondPermission` 现状写错（§6.3）。**

调研写 `[Mac] session.go:121-123` 为 `ErrNotSupported`。实际这三行是 `Events` / `CurrentSessionID` / `Alive`。`RespondPermission` 在 `agent/codex-remote/plan_review.go:104`，代理到计划审批，**产品路径已实现**。`RespondQuestion`/`RejectQuestion` 才是 `ErrNotSupported`。

不推翻「无 slash execute 面」；但 §6.3 把已交付的 plan 审批应答说成不支持，会污染后续接线判断。改为：slash/skills 未接；plan 审批应答已接；`skills/list` 等仍未接。

**A2. DSH iOS「权限预设无入口」引用退役 backend（§3.4）。**

调研引 `SelectionSheets.swift:230-231`「权限模式由 MacBridge DSH_PERMISSION_MODE 预设控制」——这是 **退役 `.deepSeek`**。产品 `.deepSeekWeb` 在 :232-233，文案是官方审批经 Bridge 应答。

更关键：`BackendModels.prefersComposerPermissionModes` 对 `.deepSeekWeb` 为 **true**，且 dsh-web 实现 `ModeSwitcher` → hello_ack 广告 `permission_mode`。iOS **会显示** composer 权限模式菜单，走 `set_permission_mode` → Mac `commands/execute /permission …`。

「无 `/` 面板」仍对；「permission 无任何 iOS 入口」不对。`/` 面板对 DSH 的增量是 **整表命令（含 `/plan`）+ 技能组**，不是第一次获得 permission 写路径。

---

## 4. B 级修订项（行号/计数/引用，不改结论）

| # | 位置 | 声称 | 实际 | 性质 |
|---|---|---|---|---|
| B1 | §6.1 | TUI SlashCommand **~70** | enum **59** 变体（`slash_command.rs:15-84`） | 数量偏大 |
| B2 | §4.2 | 六 subtype 全 success 锚 `dumps/main-summary.json` **含 bypass** | `main-summary.json` 有 initialize/list_models/set_model/permission(acceptEdits+default)/interrupt/rename；**bypass 在 `bypass-summary.json`** | 引用文件错 |
| B3 | §5.1 | `command/index.ts:46` 聚合 | `:46` 是 Default `{INIT,REVIEW}`；聚合在 `:70-152` | 行号 |
| B4 | §3.2 | `API_REMOTE_FORWARDED_EVENTS` = `gateway/src/stream-protocol.ts:16-35` | 该文件是 mux 帧类型。真正的 allowlist 在 `packages/api/remotes/src/remote-events.ts:16-35`（含 `commands/change`，**不含** `command/run\|done`）。双客户端命令生命周期走 **session/follow**，调研后半段已写对 | 路径张冠李戴 |
| B5 | §5.1 | 活体 `/skill` **11** | 本轮 **12**（计数漂移，与 haiku sqlite 同类） | 刷新或写「调研时刻」 |
| B6 | §7.1 | pager builtin **~60** | vec 约 **71**（含 hidden） | 数量 |
| B7 | §7.3 | `catalog_session_list.go:78-80` 调 `session/list` | 78-80 是 catalog 子进程注释；`session/list` 在 262/319。无 `x.ai/commands/list` 结论成立 | 行号 |
| B8 | §7.3 | `leader_subscriber.go:672` | 672 注释；`handlePlanBroadcast` :680 | ±8 |
| B9 | §3.4 iOS | `agentTag` :146；plan_review :1539；`PlanModeEntryAction` :409 | agentTag **144**（146=modelTag）；mapWirePlanReview **1542**；enum **412** | 行号 |
| B10 | §3.1 | 活体 6 命令顺序 `plan / permission / compact / …` | host `list()` **按 name 排序** → compact, export, feedback, goal, permission, plan。集合对 | 顺序 |
| B11 | §3.2 | execute payload 含 `images:[]` 且样本是 `/plan off` | CordCode 生产只发 `{agentId,line}` 无 images；`/plan off` 在官方 fixture/web 客户端 | 把官方样本说成 CordCode 生产形状 |
| B12 | §6.2 | `command_exec.rs` 未写 `app-server-protocol/src/protocol/v2/` 前缀 | 内容正确，路径缺两层 | 与计划调研 A3 同类 |

---

## 5. 未核实清单评审

| # | 调研自评 | 本轮 | 建议 |
|---|---|---|---|
| 1 DSH 技能 e2e | 诚实 | 维持 | 立项 Phase 0 做一次 `/handoff-doc` 类技能经 send_message |
| 2 rc.2 vs master 事件载体 | 诚实 | 维持 | execute 广播不要假设 mux allowlist 含 `command/run` |
| 3 Claude `skills` 元素结构 | 标未 dump | **可降级**：同一 `envmx-A-baseline.jsonl` 的 init 帧里 `skills` 是 **22 个名字字符串**（`skills[0]=="audit-plan"`），不是对象。`slash_commands` 48 个名字。`initialize.commands` 键为 `name/description/argumentHint/aliases`（22/48 有 hint） | 把已见形状写进正文，#3 改为「无独立 source 字段」 |
| 4 Codex 装机 vs checkout | 诚实 | 维持 | |
| 5 Remote Control 活体 | 诚实 | 源码透传成立 | |
| 6 Grok 1.0.13 目录活体 | 诚实 | 维持 | |
| 7 iOS plan agent 端到端 | 诚实 | 发送层 `agent` 字段已具备 | |
| 8 OpenCode v2 / compress | 诚实 | v2 `switchAgent` 在 OpenAPI 成立 | |
| 9–10 Claude 内置 `/compact` 等 | 诚实 | 维持 | |

---

## 6. 对立项的含义（不替 owner 选 UI）

调研的接入最小缺口表可以当设计输入，前提是改完 A1/A2：

- **DSH**：list 必须钉装机路径（`POST /api/skill.list` + `{sessionId}`，不是 master 的 `skills/list`）。execute 复用 `commands/execute`。`/plan` 与 `/permission` 同通道；permission 的 iOS 写入口**已经存在**（composer 权限菜单），面板增量是 `/plan`+其余 host 命令+技能组。
- **Claude**：list 消费已到的 `system/init`（或 `initialize.commands` 多拿 hint）；execute 不要新 RPC。
- **OpenCode**：`GET /command`（可按 `source` 分组）+ `POST /session/:id/command`；plan 走 agent 通道，不进 `/`。
- **Codex**：命令面板 fail closed；技能面板才有官方面。
- **Grok**：目录可解 ACP；`/plan` 必须新接 `session/set_mode`，禁止文本 fallback。

capability 新位对齐 `permission_mode` 范式——与死接口现状一致，建议保留。

---

## 7. 修订优先级

**P0（作基线前）：** A1、A2。

**P1：** B2、B4、B11、未核实 #3 用已有 dump 收口。

**P2：** 其余 B 级行号/计数；OpenCode `/skill` 12 注明时刻。

结论层（§1 总表、§8.3 两问）**可以引用**；§3.4 DSH iOS 权限入口、§6.3 Codex RespondPermission **不可引用**，须先改。
