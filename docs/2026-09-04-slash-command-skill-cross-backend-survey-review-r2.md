# 评审报告 r2：五 backend「/」命令与技能调研 v1.1

- 评审日期：2026-09-04
- 评审对象：`docs/2026-09-04-slash-command-skill-cross-backend-survey.md` **v1.1**（commit `d021e02`）
- 对照基线：`docs/2026-09-04-slash-command-skill-cross-backend-survey-review.md`（v1.0，有条件通过；A1/A2 + B1–B12 + 未核实 #3）
- 评审方法：逐项对照上一轮 14 项，**不采信修订表自述**；A1/A2 与关键 B 项在本仓/iOS/上游/dump 上复读。未重跑 DSH list；OpenCode `/skill` 计数沿用 r1 活体 12。
- 评审边界：纯文档评审，未改调研档、未改代码、未代为提交未跟踪文件。

---

## 0. 来源清单（本轮）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=d021e0232f8f5957786980cd36f494b34f667504
未提交状态=未跟踪
  docs/2026-09-04-slash-command-skill-cross-backend-survey-review.md（r1）
  docs/2026-09-04-slash-command-skill-ios-survey-brief.md（owner 指令）
  本 r2 文件
配套 iOS=/Users/jacklee/Projects/cordcode-ios  main  db7972cfa3f5fdb995c7b5a7e5117dd60c0d88b3
辅助 Mac=claudecode/official-capability  1b2a0224（调研修订表已注记；dumps 仍可读）
上游 pin 与 v1.0 一致（codex 50fffd5 / grok 72a6125 / dsh 49a606b / opencode 69c172e）
```

相对 r1：Mac HEAD `b1b08e8` → `d021e02`（仅本调研 v1.1）。iOS main 未变。

---

## 1. 结论

**通过（APPROVE）。可作为立项事实输入。**

上一轮 2 条 A 级、12 条 B 级、未核实 #3 收口建议，正文均已改到对应章节，不是只写在修订表。owner 两问的答案未回潮。无新阻断、无新必改。

剩余建议级：`command/index.ts:46` 仍是 `export const Default` 的起始行（对象本体 :47-49，可忽略）；`parseCommand` 正则仍写简化式（不在上一轮 14 项内，不挡引用）。

---

## 2. 上一轮条款逐项复核

### A 级

| 项 | 复核 | 证据 |
|---|---|---|
| **A1 Codex RespondPermission** | ✅ | §6.3 改写：`plan_review.go:104` 代理 `RespondSessionPermission`；`session.go:121-123` 标明为 Events/ID/Alive；`RespondQuestion`/`RejectQuestion` 在 `:135-136` 返回 `ErrNotSupported`。本轮复读源码一致。误引归因 `654dd8b`（`feat(codex-remote): 同步 Codex Plan 到 iOS 计划审批卡`）属实 |
| **A2 DSH iOS 权限入口** | ✅ | §3.4/§3.5-D 推翻「无入口」：`.deepSeekWeb` + `prefersComposerPermissionModes`（`BackendModels.swift:80-87`，先于 capability）→ composer 菜单 → `set_permission_mode` → `commands/execute /permission`。增量改为整表 host 命令（含 `/plan`）+ 技能组。退役 `.deepSeek` 文案与产品分支拆开。本轮复读 iOS 源码一致 |

### B 级

| 项 | 复核 | 落点 |
|---|---|---|
| B1 枚举 59 | ✅ | §6.1 |
| B2 bypass dump 分文件 | ✅ | §4.2：`main-summary.json` + `bypass-summary.json` |
| B3 OC 聚合行号 | ✅ | §5.1：`:46` Default；`:105` MCP、`:134` skills、`:166` list——与 `command/index.ts` 现树一致 |
| B4 allowlist 真身 | ✅ | §3.2：`remotes/src/remote-events.ts:16-35`；写明不含 `command/run\|done`，生命周期走 session/follow |
| B5 `/skill` 12 | ✅ | §5.1；注明随本机技能库增减 |
| B6 pager 71 | ✅ | §7.1 |
| B7 catalog 行号 | ✅ | §7.3：注释 `:78-80`，`session/list` 调用 `:319`（本轮复读 `catalog_session_list.go:319`） |
| B8 handlePlanBroadcast | ✅ | §7.3：`:680`，`:670-679` 注释 |
| B9 iOS 三处行号 | ✅ | agentTag `:144`、mapWirePlanReview `:1542`、PlanModeEntryAction `:412`；路径改为 `Services/Backend/BackendModels.swift` |
| B10 命令 name 序 | ✅ | §3.1：`compact / export / feedback / goal / permission / plan` |
| B11 payload 拆开 | ✅ | §3.2：官方 fixture 含 images；CordCode 生产仅 `{agentId, line}` |
| B12 command_exec 路径前缀 | ✅ | §6.2：`app-server-protocol/src/protocol/v2/command_exec.rs`；同节 `thread.rs`/`turn.rs` 同步补前缀 |

### 未核实 #3

✅ 部分收口写入 §4.1 + §9.3：`skills` = 22 个名字字符串；`initialize.commands` 键 `{name, description, argumentHint, aliases}`，22/48 带 hint。仍开放「无独立 source 字段 / aliases 语义」——范围正确，不再把已见形状标成未 dump。

---

## 3. 二轮新发现（建议级，不挡引用）

| # | 说明 |
|---|---|
| R2-S1 | `command/index.ts:46` 精确说是 `export const Default = {`，`INIT/REVIEW` 在 :47-48。不影响聚合行号修正 |
| R2-S2 | §3.2 `parseCommand` 仍写 `/^\/([a-z][a-z0-9_-]*)/`；上游实际带 `(?=$|[\t\n\r ])`。不在 r1 必改清单，立项解析命令行时再钉 |

§1 总表 DSH iOS 列仍是「无 `/` 面板」，与 §3.4 权限菜单已存在不矛盾（面板 ≠ 权限齿轮）。§8.3 两问未改，仍成立。

---

## 4. 引用与入档

- **可以引用** v1.1 全文作立项事实输入，包括先前不可引用的 §3.4、§6.3。
- 未核实 #1/#2/#4–#10 仍是实施前探针项，不是文档缺口。
- claudecode-official `1b2a0224` 未进 main：文档继续标「在途」正确。

**入档：** 按 `0514e97` 先例，建议把 r1 + 本 r2 两份评审报告一并提交（与调研正文成套）。`survey-brief.md` 是 owner 指令，是否入库由你定；我未代为 `git add`。
