# Claude Code backend 官方能力收敛升级设计评审报告

- 评审对象：`docs/2026-09-04-claudecode-official-capability-upgrade-design.md`（v1，2026-09-04，未实施）
- 评审日期：2026-09-04
- 评审方法：source-first 独立复核。对照 Agent SDK 0.3.260 类型契约（本机解包 `sdk.d.ts`）、官方 hooks/managed-settings 文档（2026-09-04 抓取）、本工作树 `plan/approval-layer` HEAD 源码、iOS 双工作树、本机 cc-switch / settings / runtime 活体只读采样。纪律：audit-plan（内容形状断言必须有样本或类型原文；否定性断言标注查证层面）。
- 评审边界：纯文档评审，未改设计稿、未改代码、未跑 Phase 0 探针（那是硬门，评审不代替）。

---

## 0. 本次评审来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=390ed6efe18b793ef5ccd886c007c93ba010e964
未提交状态=修改 .exec-plan/state/plan-dfb27dce3681.json（与本方案无关）；新增本方案文件 + 本评审文件
任务预期分支=用户指示评审 docs/ 下未提交方案；落在当前 session 工作树
配套 Mac main=/Users/jacklee/Projects/cordcode-macbridge  main  a2200cf4771b7ded4a09577bdcf9599d145d93c1（只读对照四份范式文档与 CLAUDE.md）
配套 iOS 设计引用=/Users/jacklee/Projects/cordcode-ios  main  61f67bf63a8a5f6e10e9e9c62fbd9fe36f2236cd
配套 iOS 计划审批=/Users/jacklee/Projects/cordcode-ios-plan-approval  plan/approval-layer-ios  dbf0c048359ef3fec5106ed31102847c4f311eb3
预期产品特性=本评审不实施；核验对象是 claude backend 模型目录真值化 / 会话内控制协议化 / hooks 事件驱动 / 文件面边界化 的设计稿
上游 SDK=/Users/jacklee/Projects/claude-agent-sdk-npm/package  @anthropic-ai/claude-agent-sdk@0.3.260（package.json claudeCodeVersion=2.1.260）
上游 TS 仓=/Users/jacklee/Projects/claude-agent-sdk-typescript  a79d677（仅 examples/changelog）
本机 CLI=claude --version 2.1.234（/opt/homebrew/bin/claude → @anthropic-ai/claude-code@2.1.234）；旁路 native package.json 仍标 2.1.126；Desktop 会话文件 version=2.1.258 entrypoint=claude-desktop-3p
```

设计稿 §0.1 钉的 Mac 提交是 `aabbfe6`。本评审 HEAD 相对它只多 1 个提交：

`390ed6e feat(plan): set_permission_mode 响应补 sessionActive`

该提交只改 go-bridge RPC 信封，**不**向 Claude stdin 发送控制请求。设计稿「发送侧控制 = 零」在 HEAD 仍成立；但「plan/approval-layer 在途」已经是落地代码，不是文档预告。

---

## 1. 结论

**修改后通过（APPROVE WITH CHANGES）。**

路线裁决正确：Claude Code 没有跨进程 server，不能照搬 dsh-web / opencode-web / codex-remote / grok leader 的单面 API 化；否决嵌入 Node SDK 有本仓 dsh SDK-stdio 前车，且会破坏「纯 Go + CLI 子进程」部署模型。`list_models` 控制请求与 hooks 跨层合并确实是上一轮讨论里低估的官方面——本轮在 SDK 类型与官方文档上独立坐实。Phase 0 作全案硬门、fail closed、不伪装能力，符合四份参照文档的纪律。

但存在 **3 项阻断**：若按原文实施，Phase 0 会测错表面、发送侧会写错信封、Phase 1/3 会把两个不同代际的 CLI 当成同一个 worker。另有若干必改（initialize 已有 typed 模型目录被漏掉、`caps.modelCatalog` 不是 typed 字段、plan 分支权限面已落地却仍引用 iOS main、Managed 写入需要 admin）。全部可在设计文档层修复，**不推翻混合面四层路线**。

---

## 2. 阻断问题

### B1. 控制信封字段是 `request`，不是 `payload`（内容形状错误）

- **严重级别**：阻断（按原文写发送侧，CLI 解析不到 subtype）
- **文档位置**：§3.1「协议帧」；Phase 0 探针 1 会继承该形状
- **证据**：
  - SDK 0.3.260 `sdk.d.ts:4285-4291`：`SDKControlRequest = { type:'control_request'; request_id: string; request: SDKControlRequestInner }`
  - 成功/错误响应是 `{ type:'control_response'; response: { subtype:'success'|'error'; request_id; response?|error } }`（`sdk.d.ts:4332+`），不是顶层 `payload`
  - 树内收侧已经按 `request` 解析：`agent/claudecode/session.go:827-833` `raw["request"]`；fixture `session_test.go:361` 同样是 `"request":{"subtype":"can_use_tool",...}`
- **为什么会造成返工**：发送侧若写成 `payload.subtype`，CLI 会当成未知/空 subtype。Phase 0 会得到「unknown subtype / 无响应」并按硬门停掉 Phase 1，把「信封写错」误判成「CLI 2.1.234 不支持 list_models」。
- **建议修订**：§3.1 与所有探针/fixture 描述改成与 SDK 和树内收侧同一信封。补一句：bridge 现有 stdin 只写 `user` 与 `control_response`，**尚未处理 stdout 上的 `control_response`**（`handleReadLoopLine` 无该 case）——发送侧封装必须同时加 request_id 配对，否则发出去无人收。

### B2. Phase 0 探针 spawn 写成 `-p`，与生产通道不是同一表面

- **严重级别**：阻断（硬门测错通道 → 假 FAIL / 假 PASS）
- **文档位置**：§6 Phase 0 第 1 步「`claude -p --output-format stream-json --verbose`，bridge 现有 spawn 通道即可」——前后自相矛盾
- **证据**：
  - 生产 spawn：`session.go:108-118` `baseClaudeInnerArgs` = `--output-format stream-json --input-format stream-json --permission-prompt-tool stdio --include-partial-messages [--verbose]`，**没有 `-p`**
  - `-p` 只出现在一次性 helper（`pr_content.go` / `commit_message.go`，且 `--output-format json`）
  - SDK `Query` 控制方法注释（`sdk.d.ts:2588-2590`）：「only supported when streaming input/output is used」
  - `sdk.d.ts:3980`：one-shot / `-p` 会关 stdin，之后送不出 `stop_task` 一类控制请求
  - CLI `--help`：`--input-format` / `--output-format` 标注 only works with `--print`；设计的探针命令带了 `-p` 却**没带** `--input-format stream-json`，走的是关 stdin 的 print 默认 text 输入
- **为什么会造成返工**：在关 stdin 的 `-p` 上发 `list_models` 得到失败，不能证明生产 streaming 会话不支持；反过来在 `-p`+stream-json 上偶然成功，也不能证明与 `--permission-prompt-tool stdio` 共存。这正是 codex-web 评审 B1「Gate 实验缺少决定性前提变量」的同类病。
- **建议修订**：Phase 0 探针必须 **逐字复用** `baseClaudeInnerArgs` + 与生产相同的 env 注入（含 `CLAUDE_CODE_ENTRYPOINT` / provider env）。允许额外做一组对照（纯 `-p`）但不得作为放行判据。明确 stdin 保持打开直到探针结束。

### B3. 本机至少两代 CLI，PostModelSwitch 有硬版本下限；设计把它写成单一锚 2.1.234

- **严重级别**：阻断（Phase 1 观测源 / Phase 3 事件集建立在未受控的 worker 代际上）
- **文档位置**：§0.3 / §6 Phase 0 / §6 Phase 1 第 2 步 / §6 Phase 3 事件集 / 风险 1
- **证据**：
  - PATH CLI：`claude --version` = **2.1.234**（npm `@anthropic-ai/claude-code@2.1.234`）
  - SDK 包声明配对：`claudeCodeVersion: 2.1.260`（与 2.1.234 **不是同一代**）
  - Desktop 会话文件 `~/.claude/sessions/48261.json`：`version=2.1.258`，`entrypoint=claude-desktop-3p`（与 CordCode spawn 入口同类，但版本更新）
  - 官方 hooks 文档原文：`PostModelSwitch requires Claude Code v2.1.251 or later`（2026-08-28 changelog：该版本才加入 Pre/PostModelSwitch）
  - 设计把 CLI 2.1.234 是否支持 `list_models` 列为待实测，却把 PostModelSwitch 直接写进 Phase 1 真值链和 Phase 3 最小事件集，**没有把「2.1.234 vs ≥2.1.251 vs Desktop 2.1.258」列为受控变量**
- **为什么会造成返工**：CordCode 自 spawn 的 worker 是 2.1.234，按官方文档 **没有** PostModelSwitch；外部 Terminal/Desktop 可能是 2.1.258，有该事件。Phase 0 若只探 PATH `claude`，会把「Desktop 外部 turn 的模型切换观测」一并判死，或反过来只在 Desktop 上探到事件就广告给 iOS。风险 1 只覆盖 `list_models`/`caps.modelCatalog`，漏了 hooks 事件的版本门。
- **建议修订**：
  1. Phase 0 矩阵拆成至少两行：PATH CLI 2.1.234（CordCode spawn 真值）× Desktop/会话文件版本（外部 turn 真值）
  2. PostModelSwitch / PreModelSwitch 在 2.1.234 上预期为**文档级不存在**，不是 unknown subtype 惊喜；Phase 1 观测源在该代际只能靠 `message.model`
  3. 若要把 PostModelSwitch 当 Phase 1/3 主源，先写明产品是否升级 CordCode 所用 CLI，或接受「仅外部 Desktop 会话有该事件」的不对称

---

## 3. 必改问题

### M1. Phase 1 主源漏掉已 typed 的 `initialize.models`；`list_models` 成功体在 SDK 中无类型

- **文档位置**：§2.1 / §4 L4 / §6 Phase 1 第 1 步
- **证据**：
  - `SDKControlInitializeResponse.models: ModelInfo[]`（`sdk.d.ts:3989-3994`），字段完整：`value` / `resolvedModel`（别名→canonical，例如 `sonnet` → `claude-sonnet-5`）/ `displayName` / `description` / `supportsEffort` / `supportedEffortLevels` / `supportsAdaptiveThinking` / `supportsFastMode` / `supportsAutoMode`（`sdk.d.ts:1266-1305`）
  - `Query.supportedModels()`（`:2738`）走的是 initialize 缓存，**没有** `Query.listModels()`
  - `SDKControlListModelsRequest` 只有 `{subtype:'list_models'}`，**找不到** `SDKControlListModelsResponse`；`modelCatalog` 全 package 只出现在该请求的一句 JSDoc
  - 树内 **从不发送** `initialize`（`agent/claudecode` 0 命中），因此生产路径今天拿不到这份 typed 目录
- **为什么会造成返工**：把 thin-client 专用的 `list_models` 当唯一主源，会（1）用无类型的 success `Record<string, unknown>` 猜字段；（2）忽略 SDK 已经给本地 streaming 会话准备好的 initialize 目录；（3）漏掉 `resolvedModel`——这正是「haiku → glm-5.3」行渲染需要的官方字段。发送 `initialize` 还有副作用（hooks 注册、first-attached-client-wins、`perTaskStopAffordance`），必须写进 Phase 0。
- **建议修订**：Phase 1 主源改为「`initialize` 响应 `models`（typed）→ `list_models`（thin-client / 刷新）→ 观测 → settings 别名」。Phase 0 增加：是否必须先发 `initialize` 才认 `list_models`；initialize 的 `models` 与 `list_models` 成功体是否同构（**必须 dump 两份样本并排**，audit-plan 双策略）。

### M2. `caps.modelCatalog` 被写成能力门，但 init 帧没有该 typed cap

- **文档位置**：§2.1 `list_models` 行；Phase 0 第 2 步
- **证据**：`system/init` 的 `capabilities?: string[]`（`sdk.d.ts:5131-5134`）JSDoc 只点名 `interrupt_receipt_v1` / `interrupt_cancel_queued_v1` / `queued_notifications`，open set。`modelCatalog` 不是其中列出的值，也不是 `SDKControlInitializeResponse` 的字段。
- **建议修订**：Phase 0 把 cap 探测改成「在 init `capabilities[]` 里搜字符串 `modelCatalog`（可能不存在）+ 以 `list_models`/`initialize` 实发结果为准」。禁止在未 dump 到该字符串时，把「无 cap」写成与「无 list_models」等价的失败语义。

### M3. 发送侧 `set_permission_mode` 与本分支已落地的 plan 审批层会双轨（Risk 6 已被坐实）

- **文档位置**：§5「plan approval / 权限模式」行；§6 Phase 2.3；§8 风险 6；§0.2 iOS 钉 main
- **证据（本分支已合并，不是在途文档）**：
  - ExitPlanMode → `permissionKind=plan_review`（`session.go:883-894`）
  - 批准 D5 = 纯 `allow`，**不**透传 `updatedPermissions`/`setMode`（`session.go:1003-1030`）；owner 已在实施方案 §3 D5 裁决
  - `SetLiveMode` 对 `plan`/`auto` **显式返回 false**（`session.go:1236-1242`）；HEAD `sessionActive` 正是为了让 iOS 区分「闲置 resume 带 `--permission-mode`」与「运行中切不进去」
  - 本地 `bypassPermissions`/`acceptEdits`/`dontAsk` 仍在 `can_use_tool` 到达前自动应答（`session.go:850-873`）
  - iOS 计划卡在 **`plan/approval-layer-ios` @ dbf0c048**，不在设计引用的 main @ 61f67bf
- **为什么会造成返工**：若 Phase 2 把 `setPermissionMode`「替换」成 CLI 控制帧，会（1）让活会话切 plan，推翻 `SetLiveMode` 与 `sessionActive`；（2）批准计划后再 `set_permission_mode` 离开 plan，推翻 D5「后续写操作仍走 iOS 权限卡」；（3）只改 CLI、不改本地 auto-answer，造成双应答。设计写在 plan 分支上却把 iOS 配套钉 main，实施时会重演 2026-08-24 错工作树事故。
- **建议修订**（实施前必须冻结，二选一写进正文）：
  1. **推荐**：本方案作为 `plan/approval-layer` 续作；iOS 配套改为 `plan/approval-layer-ios`。Phase 2 只对 `default|acceptEdits|bypassPermissions|dontAsk` 发 CLI 控制帧；**继续禁止**运行中切 `plan`/`auto`；ExitPlanMode 批准保持纯 allow。
  2. 从 `main` 另开独立功能分支，Phase 2 再与 plan 层对齐——不得在未选定配对时开始改代码。
  无论哪条：删除「在途」措辞，改成「已落地，冲突点如下表」。

### M4. Managed 层注入被写成「唯一全覆盖层」，但写入 `/Library/...` 是 admin 企业策略面

- **文档位置**：§6 Phase 3.1；§8 风险 3（已标 owner 裁决，但缺操作事实）
- **证据**：
  - 官方路径 VERIFIED：macOS `/Library/Application Support/ClaudeCode/managed-settings.json`
  - 官方语义：Managed 压过 `--settings` 与 user/project/local；`Managed settings apply wherever Claude Code runs on this machine`
  - **本机该目录不存在**；用户级 App 默认写不了 `/Library`
  - 官方：`--settings` **只作用于带该 flag 的那一次 spawn**，看不到 Terminal / Claude.app
  - hooks **数组合并**不是替换：`--settings` 追加 hooks，不能删掉 managed；`disableAllHooks` 关不掉 managed hooks
- **建议修订**：把「能否写 managed」从产品开关里拆出前置事实：无 admin = Phase 3 外部会话只能继续轮询（或用户自愿安装 profile）。推荐默认：**CordCode 自有会话走 `--settings` 内联 HTTP hook；Managed 默认关闭**。若 owner 打开 Managed，必须写明：需要一次 admin 授权、世界可读、只订 SessionStart/Stop/UserPromptSubmit/PostModelSwitch、提供卸载。Phase 0 第 3 步「跨层合并实测」在本机目前没有落点，不能当作硬门失败。

### M5. iOS §7.3 把「会话内 set_model / interrupt」写成既有选择器/取消按钮的自然延伸，实际是新接线

- **文档位置**：§5 / §6 Phase 2 / §7.3
- **证据**（iOS main 与 companion 同构，计划卡除外）：
  - 选择器只写本地 `selectedModelInfo`，真正上送是 `send_message.model`；`CCCodeBridgeBackendClient` **未** conform `BackendModelSetting`，原生 App 不调 `switch_model`
  - 取消按钮已绑 `abort_generation` → Claude 路径是 `Close()`（stdin EOF → SIGTERM → SIGKILL）+ **合成** `turn_completed{aborted}`，不是 CLI `interrupt`
  - `core.ModelOption` 已有 `Alias` 字段（`core/interfaces.go:523`），iOS `CCCodeBridgeModel` **没有** alias / resolved 字段
  - `.claudeCode` if-compare 生产代码约 29 处 / 12 文件（编译器不报错，opencode-web 评审 M1 同类）
- **建议修订**：§7.3 拆成「Mac 先广告能力位 / iOS 后接线」。`set_model` 要先接 adapter 再谈选择器同步。`interrupt` 必须新能力位；缺位时保持杀进程，禁止把旧 runtime 的 abort 假装成官方 interrupt。别名行优先复用 `ModelOption.Alias` + initialize `resolvedModel`，不要另造平行字段。补 if-compare 清单。

### M6. 若干官方形状/否定性断言写错或过强

| 项 | 文档说法 | 独立核实 | 修订 |
|---|---|---|---|
| `snapshot` 控制 subtype | §2.1 列为备查 | **不存在**该 subtype；`systemPromptSnapshot` 是 initialize 选项 | 删掉或改名 |
| 「SDK 全量检索无 list_sessions」 | §2.3 | **有** `listSessions()` 磁盘 API（`sdk.d.ts:992`），读 `~/.claude/projects/`；**没有** control subtype | 结论「无 CLI 列会话 API」可留，证据改为「无 control subtype；SDK 磁盘 API 与本仓 JSONL 扫描同族，不升格为协议面」 |
| `interrupt` | 「中断当前 turn」 | 另有 `cancel_queued?: boolean`、`interrupt_receipt_v1` / `interrupt_cancel_queued_v1`；旧 CLI 成功体可能无 `still_queued` | Phase 2 写明 Stop 按钮应 `cancel_queued:true`；按 init capabilities 解析回执 |
| `fetchModelsFromAPI` 补 Bearer | 写成官方双鉴权 | 官方 Messages/Models 文档：API key 走 `x-api-key`；`Authorization: Bearer` 是 WIF 短时 token，不是把 API key 当 Bearer。本仓自定义网关 spawn **已经**用 `ANTHROPIC_AUTH_TOKEN`（`claudecode.go:2097-2128`），但 `/v1/models` 拉取既不读该 env、也不发 Bearer | 改成「网关兼容双头」+ 「拉取路径补读 AUTH_TOKEN」；不要写成 anthropic.com 官方 /v1/models 的标准鉴权 |
| 树内 `message.model`「已收」 | §3.3 / §2.4 | 只在 usage 块里用于 `emitContextUsage`（`session.go:461-466`），**不**进 catalog / `GetModel()` | 观测补充不是「已经有真值」，是「字段在帧上，尚未接入目录」 |

---

## 4. 建议

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| S1 | 功能面表缺 `initialize` / `get_context_usage` / `rename_session` / `ConfigChange` hook | SDK union 35 个 subtype；hooks 文档 ConfigChange | §5 补行：initialize（目录+cap 探测）、get_context_usage（usage 从文件面升 A 的候选）、rename_session（对照 Phase 4 JSONL 共写）、ConfigChange（cc-switch 重写 settings 的官方事件，可减轮询） |
| S2 | PermissionRequest HTTP hook 与 `can_use_tool` 并存 | 本机 settings 仍有 `http://127.0.0.1:7823/hooks/permission`；评审时 **7823 无监听** | Phase 0 记录 cc-switch 权限 hook 死端点（exit/HTTP 非阻塞）。CordCode 不要再订 PermissionRequest，避免与 stdio 权限通道双应答 |
| S3 | hooks 静默失效本机已有样本 | 真实 transcript `hook_non_blocking_error` + `exitCode:127` + `cc-event-hook.sh: No such file`，会话继续 | 活性检测从「建议」升格为 Phase 3 验收硬条件；可引用该样本作 fixture |
| S4 | `--settings` 对 hooks 是追加不是覆盖 | 官方：标量 `--settings` 压过 user/project/local 但低于 Managed；hooks 数组 merge | §2.2 拆开「标量优先级」与「hooks 合并」。CordCode 内联 hooks 不会打掉 cc-switch 的 PermissionRequest——这是优点，也是双 hook 风险 |
| S5 | 网关改写比设计写的更乱 | sqlite `proxy_request_logs`：`claude-sonnet-5→glm-5.3`（7564）、`claude-fable-5→glm-5.3`（641）VERIFIED；另有大量 `claude-opus-4-8→glm-5.2`（6943）和 **identity** `claude-sonnet-5→claude-sonnet-5`（3037） | 真值链必须按「请求名 / 网关改写名 / 别名槽位」三列展示；不能假设 sonnet 族总是 glm-5.3 |
| S6 | settings.json 与 runtime env 已再度分叉 | 当前 user settings `ANTHROPIC_BASE_URL=https://open.bigmodel.cn/api/anthropic`，槽位 glm-5.3 族（haiku 仍 glm-4.7，REASONING 仍 glm-5.2）；**运行中** runtime PID 74112 仍是 `ANTHROPIC_BASE_URL=http://127.0.0.1:15721/claude-desktop`，DEFAULT_* 空串 | 这比撰写窗口更强地坐实 §3.4 三值悬案。Phase 0 探针 4 必须把「进程 env vs settings.env vs 网关改写 vs assistant.model」做成矩阵，禁止只读 settings.json |
| S7 | `usesCustomGateway` 排除 loopback 之后，官方短路会再次丢掉网关目录 | `claudecode.go:326-351` | Phase 1 第 4 步写明：排除 loopback 后走哪一级降级；不要在修误判的同时把「真网关」与「GUI 泄漏代理」当成同一类 |
| S8 | abort→interrupt 会改变 Stop hook / 进程复用 | `session.go:1280-1330` Close 依赖 stdin EOF 跑 Stop hooks | Phase 2 interrupt 是「停 turn、留进程」还是「停 turn 且仍 Close」必须二选一；与 Phase 3 Stop hook 定向刷新耦合 |
| S9 | CLAUDE.md 上游表补行 | 两棵 Mac 树 `Claude.md:467-472` 均无 Claude 行 | 采纳 §10 补行；版本锚写成「PATH CLI + Desktop 会话 version + SDK 包」，不要只写一个 2.1.234 |
| S10 | 设计落在脏的 plan 工作树 | 用户已说明可挪 main / 开功能分支 | 见文末「落盘位置」；评审不替 owner 选，但实施前必须选 |

---

## 5. 内容形状核验表（audit-plan）

| 内容类型 | 设计主张 | 本轮核验 | 评级 |
|---|---|---|---|
| `control_request` 信封 | `payload.subtype` | SDK + 树内 fixture 均为 `request.subtype` | 🔴 错 |
| `control_response` 信封 | 同 request_id，`subtype:'success'` | 树内写入是 `response.subtype=success` 嵌套（`session.go:1033-1039`）；SDK 同形 | 🟡 需把嵌套写清楚 |
| `list_models` 请求 | `{subtype:'list_models'}` @4051 | 原文与行号 VERIFIED | 🟢 |
| `list_models` 成功体 | 未给出字段 | SDK **无** response 类型；**无本轮 dump** | 🔴 无样本（Phase 0 P0） |
| `initialize.models` | 未提及 | `ModelInfo` 全字段 typed；无本轮 dump | 🔴 漏类型且无样本 |
| `set_model` | omit/null/`default` 重置 | VERIFIED `sdk.d.ts:4377-4382` | 🟢 |
| `set_permission_mode` | 会话内切换 | VERIFIED；`mode` **必填**；enum 含 plan/dontAsk/auto | 🟡 补必填与 enum |
| `interrupt` | 中断当前 turn | VERIFIED 存在；形状不完整（缺 `cancel_queued` / receipt） | 🟡 |
| `caps.modelCatalog` | 能力门控 | 仅 JSDoc 一词；init `capabilities[]` 未列出 | 🟡 待 dump |
| HTTP hook | `{type:http,url}` POST | SDK + 官方文档 + 本机 7823 先例 VERIFIED | 🟢 |
| hooks 跨层 merge | 原文「merge rather than replacing」 | 官方逐字 VERIFIED | 🟢 |
| PostModelSwitch 输入 | `from_model`/`to_model` | VERIFIED；另有 `source`/`requested_model`；**需 CLI ≥2.1.251** | 🟡 缺版本门与完整字段 |
| Stop `last_assistant_message` | 终稿不用 transcript | 官方 VERIFIED | 🟢 |
| `transcript_path` 滞后 | 异步写入 | 官方 VERIFIED | 🟢 |
| exit 127 非阻塞 | 策略写错路径静默失效 | 官方 VERIFIED + 本机 JSONL 样本 | 🟢 |
| 网关 request_model→model | sonnet-5/fable-5→glm-5.3；haiku-4-5→flash | sonnet/fable→glm-5.3 VERIFIED；另有 opus-4-8→glm-5.2 与 identity 行 | 🟡 补全分布 |
| 会话列表 API 真空 | SDK 无 list_sessions | 无 control subtype 🟢；磁盘 `listSessions()` 存在 | 🟡 证据过强 |

**未取得样本、实施前禁止当已核实的形状：** `list_models` 成功体、`initialize` 成功体（本机 streaming spawn）、`set_model`/`set_permission_mode`/`interrupt` 的 control_response 原文、PostModelSwitch/Stop/SessionStart HTTP POST 原文、`--settings` 与 user hooks 合并后的 effective hooks。这些正是 Phase 0 存在的理由——**不得在修订稿里把它们提前写成已证。**

---

## 6. 功能面覆盖对照（相对设计 §5）

| 面 | 设计处置 | 独立核实 | 缺口 |
|---|---|---|---|
| spawn / stream-json | 不变 | 生产 flags 与设计 Phase 0 不一致（B2） | 探针对齐生产 |
| can_use_tool / AskUserQuestion | 不变 | VERIFIED；本分支另有 ExitPlanMode | 写入「已扩展，勿回退」 |
| plan / permission mode | CLI `set_permission_mode` 直达 | 本地模拟 + SetLiveMode 拒 plan/auto + D5 纯 allow | M3 |
| 会话内模型 | `set_model` | 仅下次 spawn `--model`；iOS 不调 switch_model | M5 |
| 取消 | `interrupt` | 杀进程 + 合成 aborted | S8 / M5 |
| 模型目录 | list_models 主源 | 漏 initialize.models；gateway 误判 VERIFIED | M1 / S7 |
| 外部 turn | hooks + 轮询降级 | 目录发现 60s + file-relay 3s VERIFIED | 版本门 B3；Managed M4 |
| 会话列表 | 维持 JSONL | SDK 磁盘 API 同族，不升格 | M6 |
| 重命名 | 共写 custom-title | VERIFIED `session_mutation.go:50` | 官方另有 `rename_session` 控制请求（S1） |
| usage | transcript | 官方有 `get_context_usage` | S1 候选，非本期阻断 |

---

## 7. 本机 §0.4 证据复核（评审时刻，只读）

| 声明 | 现在 | 判定 |
|---|---|---|
| cc-switch `/Applications/CC Switch.app`，15721，PID 95162 | 仍在听 `127.0.0.1:15721`，PID **仍是 95162** | 🟢 |
| `~/.cc-switch/cc-switch.db` 表 | providers / proxy_config / proxy_request_logs 均在 | 🟢 |
| settings.json 11:41:59 重写 | mtime `Sep 4 11:41`；当前槽位 glm-5.3 族 | 🟢 时间窗；hooks **并非**只剩 cc-switch 一块（SuperIsland + 7823 + ai-reminder 共存） |
| 网关改写 sonnet-5/fable-5→glm-5.3 | sqlite 计数坐实 | 🟢；补 opus-4-8→glm-5.2 / identity 行 |
| `/v1/models` 双路由 | 本轮未重放 curl（避免打网关） | 保持设计的 [实测]，本评审 **未复测** |
| runtime `ANTHROPIC_BASE_URL=...15721/claude-desktop`，API_KEY 缺失 | PID 74112 **仍泄漏** 15721/claude-desktop；DEFAULT_* 空串；settings.json 已改为 bigmodel 直连且 **有** API_KEY/AUTH_TOKEN | 🟢 进程侧仍真；settings 侧已漂移——正是 env 优先级悬案 |
| HTTP hook 7823 先例 | settings 仍指向 7823；**当前无进程监听 7823** | 🟢 配置先例；运行时已是静默失效样本 |
| Managed 文件 | `/Library/Application Support/ClaudeCode/` 不存在 | 🟢 无落点 |

撰写窗口（10:00–11:45）的网关 `/v1/models` 与 bigmodel 10 模型结论，本评审不推翻、也不升格为「刚才还成立」。复测放在 Phase 0 第 5 步。

---

## 8. 路线与两个 owner 裁决点

路线本身：**同意混合面，同意否决嵌 Node SDK / 全 API 化 / 只修配置源。** 不需要重开路线讨论。

设计标出的两个裁决点，评审建议如下（**不是**代 owner 拍板）：

1. **Managed 注入**  
   推荐默认关。只对 CordCode spawn 用 `--settings` HTTP hook。打开 Managed 必须走显式 admin 安装，并允许一键卸载。没有 admin 时 Phase 3 外部会话保持轮询，不算方案失败。

2. **Phase 2 与 plan/approval-layer**  
   推荐把本方案定为该分支续作，配套 iOS 用 `plan/approval-layer-ios`。活会话不发 `set_permission_mode plan|auto`；ExitPlanMode 批准继续纯 allow。若要官方控制帧离开 plan mode，另开产品裁决，不要藏在「替换本地模拟」里。

---

## 9. 修订优先级（给作者）

**P0（改完才能进入 Phase 0 编码/探针）：**

1. 修正控制信封 `request` / `control_response` 嵌套，并写明必须配对 stdout 响应
2. Phase 0 spawn = 生产 `baseClaudeInnerArgs`，stdin 保持打开
3. Phase 0/1/3 拆 PATH CLI 2.1.234 vs Desktop ≥2.1.251/2.1.258；PostModelSwitch 按官方下限 fail closed
4. 冻结工作树配对（plan 续作 vs 从 main 新开），iOS 来源与 Mac 一致
5. Phase 1 纳入 `initialize.models`；`list_models` 成功体标为待 dump，禁止先写解析器

**P1（修订稿应有，否则实施会撞墙）：**

6. Phase 2 与已落地 plan 层的冲突表（SetLiveMode / D5 / 本地 auto-answer）
7. Managed 的 admin/本机无文件事实；默认 `--settings` only
8. iOS §7.3 改为新接线 + 能力位降级；别名用现有 `Alias`/`resolvedModel`
9. 纠正 list_sessions / snapshot / Bearer / message.model 已收 等过强断言

**P2：** S1–S10。

---

## 10. 落盘位置

方案与本评审目前都在 `cordcode-macbridge-plan-approval` 工作树 `docs/`，未提交。这不影响评审结论。

若进入实施：按 M3 选定配对后再移动/开分支。在配对未定时改 `agent/claudecode` 或 iOS，应视为 P0 来源门失败并停止。
