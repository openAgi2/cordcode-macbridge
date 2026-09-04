# Claude Code backend 官方能力收敛升级设计（source-first 对齐四 backend 重构范式）

状态：**v2.2 修订稿（2026-09-04）**。v1 一轮「修改后通过」（…design-review.md），
v2 采纳 B1–B3 / M1–M6 / S1–S10 与两项裁决（§11.1–11.5）；v2 二轮「通过」（
…design-review-r2.md），v2.1 落实 R2-S1..S7（§11.6）；v2.1 三轮「通过（APPROVE），
设计层可以停」（…design-review-r3.md），v2.2 落实 R3-S1..S4 与 HEAD 锚注记
（§11.7，无不采纳项）。未进入实施；实施前必须完成 §6 Phase 0 证据门并重新生成
来源清单。

范式参照：`docs/2026-08-16-dsh-web-backend-design.md`、
`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`、
`docs/2026-08-26-codex-remote-backend-implementation-plan.md`、
`docs/2026-08-28-grokbuild-leader-mode-design.md`。

---

## 0. 本次来源清单（P0）

### 0.1 MacBridge

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval（本 session 工作树）
分支=plan/approval-layer
提交=1d60760（v2 与一轮评审已随 0514e97 入库；390ed6e 只改 go-bridge RPC 信封、
  不向 Claude stdin 发控制请求——"发送侧控制=零"仍成立，一轮评审 §0 已核）
未提交状态=已修改设计稿（v2.2 相对入库 v2=0514e97 的跟踪修改）；未跟踪 r2/r3
  两份评审文件（R3-S1）
任务预期分支=本方案定位为 plan/approval-layer 续作（评审 M3 冻结，owner 经 2026-09-04 修订指示采纳）；
  文件暂存于本工作树 docs/、未提交；进入实施前按该配对落分支，配对未定时改 agent/claudecode 或 iOS 视为 P0 来源门失败
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-macbridge / main / a2200cf4771b7ded4a09577bdcf9599d145d93c1（只读参照四份范式文档与 CLAUDE.md）
预期产品特性=claude backend 模型目录真值化、会话内控制协议化、hooks 事件驱动（范围见 §6 Phase 3 收缩）、文件面边界化
```

模型列表相关逻辑已在 main 与 plan/approval-layer 间核对无差异（2026-09-04 核验），
本方案结论与安装版本无关。

### 0.2 iOS（配套按 M3 冻结为 plan 分支族）

```text
设计引用（模型选择器/abort 现状核验）=/Users/jacklee/Projects/cordcode-ios / main / 61f67bf63a8a5f6e10e9e9c62fbd9fe36f2236cd
实施配套（计划审批卡所在）=/Users/jacklee/Projects/cordcode-ios-plan-approval / plan/approval-layer-ios / dbf0c048359ef3fec5106ed31102847c4f311eb3
未提交状态=实施前必须重新解析
任务预期分支=iOS 配套一律挂 plan/approval-layer-ios，不钉 main（评审 M3；v1 曾误钉 main，本轮已改）
```

### 0.3 Claude 上游参照（本方案的新增锚点；版本锚为三段式，评审 S9）

```text
Agent SDK GitHub=https://github.com/anthropics/claude-agent-sdk-typescript
  本机 clone=/Users/jacklee/Projects/claude-agent-sdk-typescript（a79d677，2026-09-03；仅 examples/changelog，实现本体走 npm）
Agent SDK npm=@anthropic-ai/claude-agent-sdk@0.3.260（package.json 声明配对 claudeCodeVersion=2.1.260）
  本机解包=/Users/jacklee/Projects/claude-agent-sdk-npm/package（sdk.d.ts / agentSdkTypes.d.ts / bridge.d.ts 为类型契约）
本机 CLI 代际（至少两代并存，评审 B3）：
  PATH CLI=claude --version 2.1.234（npm 全局；native package 旁标 2.1.126，字节码压缩不可字符串取证）——CordCode 自 spawn worker 的实际代际
  Desktop 会话=~/.claude/sessions/48261.json 记录 version=2.1.258、entrypoint=claude-desktop-3p——外部 Claude App 会话代际
  SDK 配对=2.1.260（与 2.1.234 不同代）
官方文档=hooks reference https://code.claude.com/docs/en/hooks（2026-09-04 抓取全文；managed-settings 文档同日经评审核验）
```

版本锚策略：**不单写 2.1.234**。凡涉及控制 subtype / hooks 事件，锚写
「PATH CLI 2.1.234 × Desktop 2.1.258 × SDK 配对 2.1.260」三段；SDK 与 CLI 无公开
配对矩阵，逐项支持性一律 Phase 0 实测。

### 0.4 本机运行环境实测证据（2026-09-04；撰写窗口 10:00–11:45，评审时刻复核标注）

```text
cc-switch=/Applications/CC Switch.app，本地网关 127.0.0.1:15721（评审复核：PID 仍 95162，仍在监听）
cc-switch DB=~/.cc-switch/cc-switch.db（providers / proxy_config / proxy_request_logs，只读取证）
settings.json 重写=2026-09-04 11:41:59（cc-switch 整份重写；hooks 块并非 cc-switch 独占——
  SuperIsland、7823 PermissionRequest、ai-reminder 共存于同一文件，评审 §7 已核）
网关模型改写（sqlite 全量计数，2026-09-04 v2.1 时刻刷新；计数随使用漂移，教训以多映射事实为准）=
  claude-sonnet-5→glm-5.3（7603）、claude-fable-5→glm-5.3（677）、claude-opus-5→glm-5.3（584）
  claude-opus-4-8→glm-5.2（6943）
  identity：claude-sonnet-5→claude-sonnet-5（3069）、claude-opus-4-8→claude-opus-4-8（2002）等
  haiku 族多映射并存（R2-S4）：identity（538）、→glm-4.7（325）、→glm-5.3-flash（85，另有
    claude-haiku-4-5-20251001 变体 152）、→mimo-v2.5（50）、→glm-5-turbo（6）等
  ——改写映射随 cc-switch 供应商配置与时间变，同一别名族会漂到不同目标，不能假设单映射
网关 /v1/models（撰写窗口实测）=/claude-desktop 路由要求自有 Authorization token（bigmodel key 被拒"token 无效"）；/claude 路由无响应体；评审未复测（避免打网关），复测入 Phase 0
bigmodel 直连 /v1/models 实测=10 个模型（glm-4.5 … glm-5.3-flash），无 "1M" 变体
CordCode runtime 进程 env（评审时刻 PID 74112 复核仍真）=ANTHROPIC_BASE_URL=http://127.0.0.1:15721/claude-desktop（GUI 层泄漏）、
  ANTHROPIC_DEFAULT_*_MODEL 空串、ANTHROPIC_API_KEY 缺失；同刻 settings.json 已改为 bigmodel 直连且含 API_KEY/AUTH_TOKEN——env 优先级悬案的活体样本
7823 权限 hook 端点=settings.json 仍指向 http://127.0.0.1:7823/hooks/permission，但评审时刻无进程监听 7823——HTTP hook 静默失效的现成样本（S2/S3）
Managed settings=/Library/Application Support/ClaudeCode/ 不存在（评审 M4 核验）——本机无 Managed 落点
hooks 静默失效样本=真实 transcript 存在 hook_non_blocking_error + exitCode:127 + "cc-event-hook.sh: No such file"，会话继续（评审 S3）
```

---

## 1. 路线裁决（为什么是这个形态）

### 1.1 结构性前提：Claude Code 没有服务端

四份参照方案能"完全按源码重做调用映射"的共同前提是**上游是一个有官方 API 的
server/daemon，外部 turn 天然流经或被它广播**（dsh web mux、opencode serve
`/global/event`、codex app-server 事件流、grok leader socket）。Claude Code 的进程
模型是"每次 `claude` 都是独立短命 CLI 子进程"，官方从未提供跨进程会话总线。

用 dsh 方案 §2 坑 1 自己提炼的三面框架对照：

| 面 | dsh/opencode/codex/grok | claude code |
|---|---|---|
| 协议面（官方 API） | 最全面，重构基座 | spawn + stream-json + control protocol |
| 文件面（用户存储） | 有但被新路线绕开 | `~/.claude/projects` JSONL 是唯一跨进程真相 |
| 外部 turn | server 广播免费拿到 | 官方零能力，只能文件面反推 |

因此 claude 的正确形态天然是**混合面**：官方协议交互 + 官方 hooks 触发（范围见
§6 Phase 3）+ 文件面内容（边界化），不可能像 opencode-web 那样单面收敛。

### 1.2 否决路线

| 路线 | 否决理由 |
|---|---|
| B：嵌入 Agent SDK（Node 子进程） | 引入 Node 运行时 + 双进程桥接，破坏 macbridge "纯 Go + CLI 子进程"部署模型。dsh SDK-stdio 前车之鉴（dsh 方案 §2）：官方 SDK 面往往是最窄面（无 list、无外部观察），换来的是更重的进程模型 |
| C：全 API 化 | 无 server，结构不可能（§1.1） |
| D：维持现状只修配置源 | 不解决本轮实测暴露的三类问题：模型目录失真、发送侧控制通道闲置、外部 turn 依赖全目录轮询 |

### 1.3 采纳路线（评审 §8 同意，不重开）

以 Agent SDK 0.3.260 类型契约为协议参照，把 claude backend 收敛为四层：

1. **交互协议层**：stream-json + control protocol 全集（收发两侧，发送侧含
   request_id 配对封装——树内 stdout 侧尚无 `control_response` case，见 §2.4）；
2. **事件层**：官方 hooks，**范围收缩为 CordCode 自 spawn 会话（`--settings` 内联）**；
   外部会话事件注入默认关闭（M4 裁决，见 §6 Phase 3）；
3. **文件面边界层**：transcript JSONL 反推显式降级为无合同边界层（fixture 锁形状）；
4. **真值目录层**：模型目录以运行时声明为准（`initialize.models`（typed）为主源，
   `list_models` 为刷新/对照，见 §6 Phase 1）。

**落盘与配对（M3/S10 冻结）**：本方案为 `plan/approval-layer` 续作；iOS 配套一律
`plan/approval-layer-ios`。进入实施前不得在配对未定时修改 `agent/claudecode` 或
iOS 侧代码。

---

## 2. 前置核实结论（source-first 证据）

证据分级：**[SDK]** = SDK 0.3.260 类型声明（sdk.d.ts，行号已抽验）；
**[文档]** = 官方文档（2026-09-04 抓取）；**[实测]** = 本机 2026-09-04 实测；
**[树内]** = 本仓 plan/approval-layer @ 390ed6e 代码现状；**[待实测]** = Phase 0
硬门项。**未经 dump 取样，任何成功体形状不得当作已核实**（评审 §5 红线）。

### 2.1 控制协议面（官方存在；发送侧未用、信封见 §3.1）

| 控制请求 subtype | 证据 | 语义 |
|---|---|---|
| `initialize` | [SDK] sdk.d.ts:3989-3994 | 会话初始化：返回 `commands` / `agents` / `output_style` / **`models: ModelInfo[]`** / 账户信息。`ModelInfo`（sdk.d.ts:1266+）字段完整：`value`（API 调用用的 id）/ **`resolvedModel`（别名→canonical，官方例 'sonnet' → 'claude-sonnet-5'——正是 iOS 三键行渲染需要的 canonical 键；注意 canonical 与观测改写名是两件事：`sonnet` 的 resolved 是 `claude-sonnet-5`，观测改写名可能是 `glm-5.3`，haiku 族主改写更不是 glm-5.3，见 §3.3；R3-S2）** / `displayName` / `description` / `supportsEffort` / `supportedEffortLevels` / `supportsAdaptiveThinking` / `supportsFastMode` / `supportsAutoMode`。SDK `Query.supportedModels()`（:2738）即取自 initialize 缓存。注意 initialize 有副作用（hooks 注册、first-attached-client-wins、`perTaskStopAffordance`），入 Phase 0 探针清单。本机 streaming spawn 的成功体**[待实测]** |
| `list_models` | [SDK] sdk.d.ts:4051-4053 | `{subtype:'list_models'}`。JSDoc："Requests the worker's selectable model catalog… the worker's provider, settings cascade, and enforcement policy decide which models the session can run, so the thin client must **ask rather than read its own getModelOptions()**"，面向 thin-client 场景。**SDK 中不存在成功响应类型**（全 package 仅此一处 JSDoc 提及 `modelCatalog`）——成功体先 dump 再写解析器（M1）。CLI 2.1.234 支持性**[待实测]**；`caps.modelCatalog` 非 typed cap（见 M2），能力探测=在 init `capabilities[]`（sdk.d.ts:5131-5134，open set，JSDoc 仅点名 `interrupt_receipt_v1`/`interrupt_cancel_queued_v1`/`queued_notifications`）里搜字符串 + 以实发结果为准 |
| `set_model` | [SDK] sdk.d.ts:4377-4384 | `{subtype:'set_model', model?: string\|null}`；省略/null/`'default'` 重置为会话默认模型。会话内切换。**[待实测]** |
| `set_permission_mode` | [SDK] sdk.d.ts:4389+ | 会话内权限模式切换；`mode` **必填**，enum 含 `default`/`plan`/`acceptEdits`/`dontAsk`/`auto`/`bypassPermissions`。使用边界见 §6 Phase 2（活会话禁 plan/auto）。**[待实测]** |
| `interrupt` | [SDK] | 中断当前 turn；另有 `cancel_queued?: boolean` 与回执能力位 `interrupt_receipt_v1` / `interrupt_cancel_queued_v1`；旧 CLI 成功体可能无 `still_queued`（M6）。Stop 按钮语义应 `cancel_queued:true`；回执按 init capabilities 解析。**[待实测]** |
| `can_use_tool` | [SDK]+[树内] session.go:827-834 | **已在用**（收侧：CLI → bridge 请求权限决策，bridge 回 control_response） |
| ~~`snapshot`~~ | 评审 M6 核实 | **不存在该控制 subtype**；`systemPromptSnapshot` 是 initialize 的选项，不是控制请求。v1 误列，本轮删除 |

SDK 侧确认的相关选项：`canUseTool`（sdk.d.ts:1454）、`forkSession`（:1574）、
`resume`（:1910）、SDK 级 `hooks` 回调（:1595）。

### 2.2 hooks 面（官方存在；本方案使用范围按 M4 收缩）

[文档] code.claude.com/docs/en/hooks，2026-09-04 全文提取。

- **事件与版本门（B3）**：`SessionStart`（matcher: startup/resume/clear/compact/fork）、
  `Stop`、`StopFailure`、`SessionEnd`、`UserPromptSubmit`、`ConfigChange`（settings
  文件变化——cc-switch 整份重写的官方信号，见 §6 Phase 4）；**`PreModelSwitch` /
  `PostModelSwitch` 需 CLI ≥2.1.251**（官方文档原文；本机 PATH CLI 2.1.234 上为
  **文档级不存在**，不是 unknown subtype 惊喜；Desktop 2.1.258 有）。输入字段除
  `from_model`/`to_model` 外另有 `source`/`requested_model`（评审核验）。
- **payload 通用字段**：`session_id`、`prompt_id`（v2.1.196+）、`transcript_path`、
  `cwd`、`permission_mode`、`effort`、`hook_event_name`。
- **HTTP hook 形态**：`{"type":"http","url":...}` POST 到本地端点。[实测] cc-switch
  的 PermissionRequest hook 指向 `http://127.0.0.1:7823/hooks/permission`；评审时刻
  **7823 无监听**——HTTP hook 静默失效的现成样本。**CordCode 不订阅
  PermissionRequest**（避免与 stdio `can_use_tool` 通道双应答，S2）。
- **层级与合并（S4 拆开表述；标量顺序按 R2-S1 修正）**：标量优先级（官方
  高→低）= Managed > `--settings`（command line）> **local
  （`.claude/settings.local.json`）> project（`.claude/settings.json`）> user
  （`~/.claude/settings.json`）**；**hooks 数组跨层 merge 不是替换**（官方原文
  "merge rather than replacing"）。推论：CordCode `--settings` 内联 hooks 追加生效、
  不会打掉 cc-switch 的 PermissionRequest hook——既是优点（共存）也是双 hook 风险
  （见 S2 不订阅决定）。
- **静默失效风险**：hook 脚本/端点失效（exit 127 等）= 非阻塞错误、会话继续。
  [实测] 本机 transcript 已有 `hook_non_blocking_error` + `exitCode:127` 样本——
  活性检测升格为 Phase 3 **验收硬条件**（S3）。

### 2.3 真空区（官方确实没有，必须显式标注）

| 能力 | 结论 | 证据（M6 修正后） |
|---|---|---|
| 会话列表协议 API | 无控制 subtype | [SDK] 控制面无 list_sessions；**SDK 存在磁盘 API `listSessions()`（sdk.d.ts:992）**，读 `~/.claude/projects/`——与本仓 JSONL 扫描同族，**不升格为协议面**。`--resume` 是交互式 picker |
| 模型目录静态查询 API | 无独立 API；运行时面 = `initialize.models`（typed）+ `list_models`（thin-client 刷新） | [SDK] |
| 外部 turn 事件总线 | 无 server；hooks 是唯一官方跨进程信号，但注入面受 M4 裁决收缩 | [文档]+架构事实 |
| 官方 API `/v1/models` | api.anthropic.com 存在该端点，**官方鉴权是 `x-api-key`**；`Authorization: Bearer` 用于 WIF 短时 token，**不是把 API key 当 Bearer 的官方用法**（M6 修正）。网关兼容性参差：bigmodel 有（10 模型），cc-switch 本地代理不透传（**[实测]**，Phase 0 复测） | [实测]+[文档] |

### 2.4 现状盘点（树内 @ 1d60760，其后仅 docs 提交、"发送侧=零"等源码结论未变；实施前按 HEAD 重核；发送侧控制通道闲置 + 三个准确性修正）

| 现状 | 树内锚点 | 与目标的差距 |
|---|---|---|
| 收侧控制协议已实现（can_use_tool 权限往返、AskUserQuestion、ExitPlanMode→plan_review） | session.go:827-834、user_input.go、session.go:883-894 | 无差距，保留；**已扩展面勿回退** |
| **发送侧控制请求 = 零**，且 stdin 只写 `user` 与 `control_response`，**stdout 读循环无 `control_response` case**（评审 B1 补充） | 全仓无发送调用；handleReadLoopLine 无该 case | Phase 2 发送侧封装必须同时实现 request_id 配对收件 |
| `setPermissionMode` 仅改 bridge 本地状态（can_use_tool 到达前自动应答 bypassPermissions/acceptEdits/dontAsk） | session.go:1229-1243、:850-873 | Phase 2 直达 CLI，但**受限**（§6 Phase 2） |
| plan/approval-layer **已落地**（非"在途"，M3 修正）：SetLiveMode 对 plan/auto 显式拒 false；ExitPlanMode 批准 D5=纯 `allow` 不透传 updatedPermissions/setMode；HEAD 390ed6e 补 sessionActive 区分闲置 resume 与运行中不可切 | session.go:1236-1243、:1003-1030 | Phase 2 与该层的冲突表见 §6 Phase 2.3 |
| 模型目录 = settings.json 三槽位别名静态快照 + 失效的网关拉取 | settings_models.go、claudecode.go:320-342 | Phase 1 重构真值链（initialize.models 主源） |
| `usesCustomGateway` 被 GUI 层泄漏的 loopback 代理 URL 误判 | claudecode.go:346-351 + [实测] runtime env | Phase 1 修正（降级去向见 §6 Phase 1.4，S7） |
| `fetchModelsFromAPI` 仅发 `x-api-key`，既不读 `ANTHROPIC_AUTH_TOKEN` 也不发 Bearer（而自定义网关 spawn **已经**用 AUTH_TOKEN，claudecode.go:2097-2128） | claudecode.go:428-433 | Phase 1 补「网关兼容双头」定位，非官方标准鉴权（M6） |
| **`message.model` 并未接入目录**（M6 修正）：只在 usage 块用于 `emitContextUsage`（session.go:461-466），不进 catalog/GetModel | session.go:461-466 | Phase 1 观测源=「字段在帧上，尚未接线」，非"已有真值" |
| 外部 turn = transcript 全目录轮询（目录发现 60s + file-relay 3s，评审核验） | CLAUDE.md backend runtime model 节 | Phase 3 范围收缩后维持轮询为默认（M4） |
| bridge 共写用户 JSONL（custom-title 记录）+ sidecar；官方另有 `rename_session` 控制请求未用 | session_mutation.go、SDK union | Phase 4 对照（S1） |
| iOS 现状（M5）：选择器只写本地 `selectedModelInfo`、真正上送是 `send_message.model`；`CCCodeBridgeBackendClient` **未** conform `BackendModelSetting`，原生 App 不调 `switch_model`；取消按钮=abort→Claude 路径 `Close()`（stdin EOF→SIGTERM→SIGKILL）+**合成** `turn_completed{aborted}`，非 CLI interrupt；`CCCodeBridgeModel` 无 alias/resolved 字段（Mac `core.ModelOption.Alias` 虽在 core/interfaces.go:523，但其语义是 canonical 的**短名**、与 `resolvedModel` 方向相反，且当前 wire 不下发 Alias，不可承载——见 §6 Phase 1.1，R2-S2） | iOS main 核验（§0.2） | §7.3 按"能力位先行/后接线"重写 |
| P0 源码门表格无 claude 行 | CLAUDE.md 上游源码优先门 | §10 补齐（三段式版本锚，S9） |

---

## 3. 官方事实基线

### 3.1 协议帧（B1 修正后的正确信封）

- 控制请求（bridge → CLI，stdin）：
  `{"type":"control_request","request_id":<id>,"request":{"subtype":<subtype>,…}}`
  ——信封字段是 **`request`**，不是 `payload`。[SDK] sdk.d.ts:4285-4291；[树内] 收侧
  session.go:829 即 `raw["request"]`，fixture session_test.go:361 同形。
- 控制响应（CLI → bridge，stdout）：
  `{"type":"control_response","response":{"subtype":"success"|"error","request_id":<id>,…}}`
  ——`subtype` **嵌套在 `response` 里**，不是顶层。[SDK] sdk.d.ts:4332+；[树内] 写侧
  session.go:1033-1039 同形。
- 现状缺口：bridge stdin 只写 `user` 与 `control_response`；stdout 读循环**没有**
  `control_response` case。发送侧封装必须同批实现「发送 + request_id 配对收件 +
  超时 + 错误透传」，否则发出去无人收。

### 3.2 worker 代际矩阵（B3）

| 通道 | CLI 代际 | PostModelSwitch/PreModelSwitch | CordCode 可控性 |
|---|---|---|---|
| CordCode 自 spawn worker（PATH CLI） | **2.1.234** | **文档级不存在**（需 ≥2.1.251） | 可控（升级 CLI 或接受缺失） |
| 外部 Claude App 会话（claude-desktop-3p） | **2.1.258** | 存在 | 不可控（随 Desktop 更新） |
| SDK 类型契约配对 | 2.1.260 | 存在 | 参照物 |

推论：Phase 1 观测源在 2.1.234 上**只能靠 `message.model` 接线**；PostModelSwitch
只可能来自外部 Desktop 会话（且默认 M4 收缩后 CordCode 不给外部会话注入 hook，
该事件实际不可得——除非 owner 未来升级 CordCode 所用 CLI 并开放 hook 注入）。
Phase 0 探针矩阵必须按这两行分别出结论，**不得合并成单一真值**。

### 3.3 模型改写发生在网关侧，且映射不稳定（S5）

[实测] 网关改写分布（2026-09-04 v2.1 时刻，全量计数见 §0.4）：sonnet/fable/
opus-5 族多被改写为 glm-5.3、opus-4-8 族为 glm-5.2、存在大量 identity 行；
**haiku 族同时改写为 6+ 个不同目标**（identity / glm-4.7 / glm-5.3-flash /
mimo-v2.5 / glm-5-turbo / …）——同一别名族随供应商配置与时间漂移到不同目标
（R2-S4）。因此：

- CLI 侧任何"目录"（initialize.models / list_models / settings 别名）反映的是
  **请求侧模型名**；
- 真值以响应 `message.model` 为准（字段在帧上，Phase 1 接线）；
- iOS 目录行按**三键**展示：槽位（wire `id`，请求侧名）/ `resolved`（canonical）/
  观测改写名（`message.model`）；不得假设某别名族总是同一目标。

### 3.4 settings.json env 与进程 env 优先级（悬案，Phase 0 矩阵探针）

[实测] 三值并存且互不相同：runtime 进程 env（`ANTHROPIC_BASE_URL=127.0.0.1:15721/
claude-desktop`、`ANTHROPIC_DEFAULT_*` 空串、无 API_KEY）× settings.json（bigmodel
直连、有 API_KEY/AUTH_TOKEN、glm-5.3 族别名）× 会话实际执行（网关改写后的
glm-5.x）。评审时刻 runtime 换代（PID 74112）后仍然成立——这是比撰写窗口更强的
活体样本。Phase 0 探针 4 必须产出「进程 env × settings.env × 网关改写 ×
assistant.model」四元矩阵，禁止只读 settings.json 下结论。

### 3.5 transcript_path 异步写入

[文档] hooks payload 的 `transcript_path` 可能滞后；需要最终助手文本应使用 Stop 的
`last_assistant_message`。事件触发定向刷新时仍需容忍尾部滞后，不能假设 Stop 即终稿。

---

## 4. 架构（目标态分层）

```text
┌─ iOS (cordcode) ──────────────────────────────────────────────┐
│  能力位门控的新接线（§7.3）：模型三键行 / set_model / interrupt │
└──────────────┬────────────────────────────────────────────────┘
┌─ go-bridge claudecode backend ─▼──────────────────────────────┐
│ L1 交互协议层  stream-json + control protocol（收发两侧）      │
│    · 收：can_use_tool / AskUserQuestion / ExitPlanMode（既有） │
│    · 发：initialize / list_models / set_model /               │
│          set_permission_mode（受限）/ interrupt——统一封装     │
│    request_id 配对收件（stdout control_response case 新增）   │
│ L2 事件层      官方 hooks → Management API 本地 HTTP 端点      │
│    · 范围=CordCode 自 spawn 会话（--settings 内联，M4）        │
│    · 外部会话默认维持轮询；Managed 注入默认关                  │
│    · 活性检测=验收硬条件（exit 127 fixture）                   │
│ L3 文件面边界层 transcriptindex + JSONL 解析                   │
│    · 显式标注"无合同"；真实 fixture 锁形状；CLI 升级回归       │
│ L4 真值目录层  AvailableModels 重构（§6 Phase 1 降级链）       │
│    initialize.models（typed 主源）→ list_models → 观测 → 别名 │
└───────────────────────────────────────────────────────────────┘
```

替换关系：L4 取代 settings_models.go 的"settings 别名短路"与 mergeGatewayModels 的
"误判网关"路径；L2 按 M4 收缩后**不取代**外部 turn 轮询（轮询仍是外部会话默认）；
L3 不删除任何文件面代码，只加边界纪律。

---

## 5. 功能面完整映射

分级：**A**=官方化（协议/文档锚点）；**B**=官方触发 + 文件面内容；**C**=文件面
边界（显式无合同）；**D**=官方真空（防御性设计）。

| bridge 功能面 | 现状 | 目标 | 级 | 官方锚点 |
|---|---|---|---|---|
| spawn / turn 事件流 | stream-json（`--output-format stream-json --input-format stream-json --permission-prompt-tool stdio --include-partial-messages [--verbose]`，session.go:108-118） | 不变 | A | [SDK] 契约 |
| 权限决策（can_use_tool 往返） | 已实现 | 不变 | A | [SDK]+[树内] |
| AskUserQuestion | 已实现 | 不变 | A | [树内] user_input.go |
| **会话 initialize（目录+cap 探测）** | 从不发送 initialize | 会话建立时发送，缓存 models/capabilities/commands | A | [SDK] :3989 |
| plan approval / 权限模式 | 本地状态模拟 + SetLiveMode 拒 plan/auto + D5 纯 allow（**已落地**） | `set_permission_mode` 直达，但**仅** default/acceptEdits/bypassPermissions/dontAsk；活会话继续禁 plan/auto；ExitPlanMode 批准保持纯 allow（冲突表见 Phase 2.3） | A | [SDK] :4389 |
| 会话内模型切换 | 仅下次 spawn `--model` 生效；iOS 不调 switch_model | `set_model` 直达（iOS adapter 先行，§7.3） | A | [SDK] :4377 |
| 取消/中断 turn | abort→Close()（杀进程）+ 合成 aborted | `interrupt`（新能力位；缺位保持杀进程，禁止假装） | A | [SDK] |
| **模型目录** | settings 别名快照 + 失效网关拉取 | 真值链（§6 Phase 1：initialize.models 主源） | A+D | [SDK] + [实测]真空区 |
| 模型变化观测 | message.model 只进 usage，不进目录 | message.model 接线目录；PostModelSwitch 仅 ≥2.1.251 且 hook 注入开放后 | A | [文档]+[树内] |
| **外部 turn 发现** | transcript 全目录轮询（60s + file-relay 3s） | **维持轮询为默认**（M4）；hook 事件驱动仅 CordCode 自 spawn 会话、且该面本就有直播流（增益=Stop/ConfigChange 细粒度） | C→B(受限) | [文档] hooks |
| 会话列表 | JSONL 目录扫描 | 维持（SDK 磁盘 listSessions() 同族，不升格） | C | [SDK] :992 |
| 历史/富历史 | JSONL 解析 + transcriptindex | 维持，fixture 化 | C | — |
| 会话重命名 | 共写 JSONL custom-title 记录 | 维持 + 命名空间防撞；对照官方 `rename_session` 控制请求（Phase 4 评估迁移，dump 优先） | C→A(候选) | [SDK] union |
| usage / context | transcript 解析 | 维持；官方 `get_context_usage` 为升 A 候选（非本期阻断，Phase 4 后评估） | C | [SDK] |
| checkpoint | workspace git 快照（与 session 真值无关） | 不变 | — | [树内] checkpoint.go |
| resume / fork | spawn 参数 | 不变（`resume`/`forkSession` 语义已有） | A | [SDK] :1910/:1574 |
| settings 变化感知 | 无（靠 mtime 轮询 settings.json） | `ConfigChange` hook（自 spawn 会话）减少轮询 | B | [文档] |

---

## 6. 分阶段实施与硬门

### Phase 0 —— 证据包（硬门，全案前置；探针 spawn 必须逐字复用生产表面）

不可跳过。每项产出真实样本/日志归档，失败即停止后续 Phase。

1. **控制面探针（B2 修正）**：spawn 通道 = **逐字复用 `baseClaudeInnerArgs`**
   （session.go:108-118：`--output-format stream-json --input-format stream-json
   --permission-prompt-tool stdio --include-partial-messages --verbose`，**无 `-p`**）
   + 与生产相同的 env 注入（含 `CLAUDE_CODE_ENTRYPOINT` / provider env），stdin 保持
   打开直到探针结束。SDK 明示 one-shot `-p` 会关 stdin、控制方法 only works with
   streaming（sdk.d.ts:2588-2590、:3980）——纯 `-p` 对照组可做但**不得作为放行
   判据**。信封按 §3.1（`request` 嵌套）。逐项发送并记录 control_response 原文：
   `initialize` → `list_models` → `set_model` → `set_permission_mode` →
   `interrupt(cancel_queued:true)` → `rename_session`（成功体归档，供 Phase 4.2
   迁移评估；R2-S5）。三种合法结论：success / unknown subtype /
   无响应（各自 fail closed 语义不同，禁止混淆）。`set_permission_mode` 的
   `bypassPermissions` 档**单独记录**是 success 还是被拒（SDK 注明该 mode 需
   `allowDangerouslySkipPermissions`；今天的本地 auto-approve 不经过 CLI，没有
   可推断的历史行为——R2-S6）。
2. **代际矩阵（B3）**：上表按「PATH CLI 2.1.234（CordCode spawn 真值）× Desktop
   2.1.258（外部会话真值）」两行分别出结论；PostModelSwitch 在 2.1.234 记
   "文档级不存在"，不消耗探针预算冒充 unknown。
3. **目录双 dump（M1）**：`initialize` 成功体与 `list_models` 成功体**并排 dump**
   （audit-plan 双策略）：是否必须先 initialize 才认 list_models；两者 `models`
   是否同构；`resolvedModel` 在 cc-switch 别名场景下解析成什么。**dump 之前禁止写
   任何解析器**。同时记录 initialize 副作用（first-attached-client-wins、
   perTaskStopAffordance）。注意**两套 hook 不是一件事**（R2-S7）：
   `initialize.hooks` 是 SDK callback matcher（随后走 `hook_callback` 控制请求）；
   Phase 3 的 `--settings` HTTP hook 是 settings 层、由 CLI 自己 POST；
   `hooks_applied`（sdk.d.ts:4003-4006）只描述 initialize 携带的 SDK hooks
   集合替换先前集合。对照项（非硬门）：确认省略 `initialize.hooks` 的裸
   initialize 不影响 `--settings` HTTP hook 触发。
4. **cap 探测（M2）**：在 init `capabilities[]` 里搜字符串 `modelCatalog`（可能不
   在）；以 `list_models`/`initialize` 实发结果为最终判据。"无 cap 字符串"≠"无
   list_models"，禁止等价化。
5. **hooks 探针（M4/S2/S3）**：`--settings '{"hooks":…}'` 内联 HTTP hook + 本地
   token 鉴权接收端，dump SessionStart/Stop/UserPromptSubmit/ConfigChange 的 POST
   原文（PostModelSwitch 只在 ≥2.1.251 通道上补测）。同时记录本机既有静默失效样本
   （7823 死端点、exit 127 transcript 行）为 fixture。Managed 跨层合并实测在本机
   **无落点**（目录不存在、写 /Library 需 admin）——该项不是硬门失败，标记
   "无可测环境"。
6. **env 优先级矩阵（S6）**：受控组合「进程 env（含空串）× settings.env ×
   `--model`」，以 assistant `message.model` 为判据，产出 §3.4 四元矩阵，回填本
   方案并同步 CLAUDE.md。
7. **网关复测**：`/v1/models` 双路由 × 双鉴权头（`x-api-key` 与 `Authorization:
   Bearer <ANTHROPIC_AUTH_TOKEN>`）重放一次，确认"不透传"结论仍成立（撰写窗口
   实测，评审未复测）。

### Phase 1 —— 模型目录真值链（M1 修正后的降级链）

`AvailableModels()` 重构，每级 fail closed：

1. **主源：`initialize.models`（typed）**——会话建立时发送 `initialize`，缓存
   `models`（含 `value`/`resolvedModel`/`displayName`/effort 能力字段）。Mac wire
   新增 optional **`resolved`** 字段承载 `resolvedModel`（别名 `value` →
   canonical，官方方向）；槽位短名沿用现有 wire `id`（haiku/sonnet/opus）；
   **不复用 `core.ModelOption.Alias`**——其语义是 canonical 的**短名**（方向
   相反），且当前 wire 从不下发 Alias（settings_models.go 不填、
   modelItemsForWire 只发 id/name；R2-S2）。观测改写名单独键（wire 键名 Phase 1
   实施时定，建议 `observedModel`，同步进 docs/protocol/ canonical pack；探针前
   不强制——R3-S3），不与 resolved 混排。
2. **刷新/对照：`list_models`**——thin-client 场景的目录刷新；成功体形状以 Phase 0
   dump 为准，未知形状 fail closed 跳过。
3. **观测补充**：assistant `message.model` 从 usage 旁路**接线进目录**（session.go:
   461-466 现只进 emitContextUsage）——维护 "seen alive" 集合与「请求名→改写名」
   观测映射（§3.3 三键展示）。PostModelSwitch 仅在 ≥2.1.251 且 hook 开放时接入
   （当前=不可得，见 §3.2）。
4. **降级源与网关修正（S7）**：settings.json 三槽位别名保留为降级源（当
   initialize/list_models 均不可用或未支持时）；`usesCustomGateway` 排除 loopback
   代理型 base URL（127.0.0.1/localhost）后**明确降级去向**——loopback 代理型不
   拉 /v1/models、不误入网关合并分支，落到别名/观测级；真网关（非 loopback）走
   修复后的拉取：读 `ANTHROPIC_AUTH_TOKEN` + 双头发送（`x-api-key` + Bearer），
   定位为**网关兼容双头**（非官方标准鉴权，M6）。"真网关"与"GUI 泄漏代理"不得
   归为同类。
5. **显示（S5）**：目录行三键——槽位（wire `id`，请求侧名）/ `resolved`
   （canonical）/ 观测改写名（observed `message.model`）；同名槽位合并；当前
   真实模型高亮以观测值为准。haiku 族多映射（§0.4）正是三键并存的理由。

验收：iPhone 模型列表与 Mac 实际可用模型一致（用户原始诉求），当前真实模型高亮
正确；cc-switch 改配置后 iOS 刷新延迟 ≤ 一次会话启动/事件周期。

### Phase 2 —— 会话内控制补全（信封与 plan 层协调按 B1/M3）

1. **发送侧封装**：统一信封（§3.1）+ request_id 配对收件（stdout
   `control_response` case 新增）+ 超时 + 错误透传到 iOS 事件面（fail visibly，
   dsh §2.3 纪律 4）。
2. `set_model`：替换"下次 spawn 生效"；iOS 侧先补 adapter 接线（§7.3）；
   `'default'` 重置语义对齐。
3. `set_permission_mode`（**受限**，M3）：只对 `default` / `acceptEdits` /
   `bypassPermissions` / `dontAsk` 发送 CLI 控制帧；若未来要以官方控制帧离开
   plan mode，另开产品裁决，不藏在"替换本地模拟"里。与已落地 plan 审批层的
   **冲突表**（R2-S3；§2.4/§5/§8 风险 7 引用于此）：

   | 已落地机制 | 树内锚点 | Phase 2 交互规则 |
   |---|---|---|
   | `SetLiveMode` 对 plan/auto 显式返回 false | session.go:1236-1243 | 维持不变、不放宽；运行中切 plan/auto 继续禁止 |
   | ExitPlanMode 批准 = 纯 `allow`（D5：不透传 updatedPermissions/setMode） | session.go:1003-1030 | 不动；批准后写操作仍走 iOS 权限卡 |
   | 本地 auto-answer（bypassPermissions/acceptEdits/dontAsk 在 can_use_tool 到达前自动应答） | session.go:850-873 | CLI 控制帧生效后仅作缺位回退（能力位缺失/CLI 拒收时），避免双应答 |
   | `sessionActive` 真值（闲置 resume 可带 `--permission-mode` vs 运行中不可切） | handlers.go handleSetPermissionMode（390ed6e） | 维持其语义；受限四档之外不新增旁路 |
4. `interrupt`：iOS Stop 语义发 `cancel_queued:true`；回执按 init capabilities
   （`interrupt_receipt_v1`/`interrupt_cancel_queued_v1`）解析。**实施前二选一
   裁决（S8，owner/engineering）**：(a) interrupt=停 turn、留进程（Close 仍走
   stdin EOF 跑 Stop hooks 的既有路径不受影响）；(b) interrupt 后仍 Close。该
   选择与 Phase 3 Stop hook 定向刷新耦合，必须在编码前写死。
5. 能力门：控制 subtype 未获 CLI 支持（Phase 0 探针结论）→ 该功能保持现状实现
   并在 capability 面如实降级，不伪装支持。

### Phase 3 —— hooks 事件层（M4 收缩后）

1. **范围默认**：只对 **CordCode 自 spawn 会话**用 `--settings '{"hooks":…}'` 内联
   HTTP hook（官方 flag；只作用于带 flag 的那次 spawn；hooks 数组 merge，不打掉
   cc-switch 的 hooks）。该面本就有 stream-json 直播，增益=Stop/StopFailure/
   ConfigChange 细粒度信号与 `last_assistant_message`。
2. **Managed 层默认关（M4 裁决，owner 2026-09-04 采纳）**：写
   `/Library/Application Support/ClaudeCode/managed-settings.json` 需 admin、企业
   策略面、本机无落点。无 admin 时**外部会话保持轮询，不算方案失败**。若 owner
   未来显式打开：一次性 admin 安装 + 世界可读告知 + 只订 SessionStart/Stop/
   UserPromptSubmit/PostModelSwitch + 一键卸载；写入前单独裁决。
3. **接收端**：Management API 新增 `/internal/hooks/claude`（token 鉴权、仅
   127.0.0.1）。**不订阅 PermissionRequest**（S2，避免与 stdio can_use_tool 双
   应答；本机 7823 死端点即前车样本）。
4. **活性检测=验收硬条件（S3）**：hook 端点心跳自检；失活→回退轮询 + 如实上报
   事件源状态。本机 exit 127 transcript 样本作 fixture。
5. 验收：自 spawn 会话 Stop 事件驱动的定向刷新（transcript_path 单文件）可观测；
   hook 失活时行为与现状一致。

### Phase 4 —— 文件面边界层固化（+S1 候选）

1. transcriptindex + JSONL 解析标注"无合同边界层"：真实会话归档 fixture 包，锁
   type 枚举与关键字段形状；CLI 大版本升级跑 fixture diff 回归。
2. `appendJSONLRecord`（custom-title）与 sidecar：custom 记录类型加命名空间前缀
   防撞；**对照官方 `rename_session` 控制请求**（成功体 dump 已列入 Phase 0.1
   发送清单，R2-S5；未 dump 前维持现状）；写入策略文档化（对齐 dsh 坑 2"共写对齐在位写者"纪律）。
3. 候选（非本期阻断）：`get_context_usage` 把 usage/context 从文件面升 A；
   `ConfigChange` hook 减少 settings mtime 轮询。
4. 本 Phase 不删除任何文件面代码——文件面从"默认手段"降级为"显式边界"。

---

## 7. 测试与验收

### 7.1 证据纪律

- 每个 Phase 的外部行为断言先有 Phase 0/实施期真实样本，再写实现（本仓
  source-first 门既有纪律）。
- **未取得样本前禁止当已核实的形状**（评审 §5 红线清单）：`list_models` 成功体、
  本机 streaming spawn 的 `initialize` 成功体、`set_model`/`set_permission_mode`/
  `interrupt` 的 control_response 原文、PostModelSwitch/Stop/SessionStart HTTP POST
  原文、`rename_session` 成功体（R3-S4）、`--settings` 与 user hooks 合并后的 effective hooks。
- 探针脚本保留入仓（对标 DSH_TURN_REPRO），供 CLI 升级后复测。
- 控制协议 fixture 用真实 control_response 原文；未知 subtype/失败响应必须让功能
  降级而非测试绿。

### 7.2 MacBridge 验证范围（按 D 级纪律）

- Phase 1/2/3 各自定向测试类 + 一次 Release 增量构建；协议面（D3）按改动范围跑
  相关测试组，不默认全量。
- 安装遵循 `/Applications` 覆盖安装路径与运行态核验（进程代际 + 特征输出）。

### 7.3 iOS 接线（M5 重写：能力位先行，iOS 后接线）

原则：**Mac 先广告能力位，iOS 后接线；缺位时维持现状路径，禁止把旧 runtime 的
行为假装成新官方能力**。

1. **模型行**：Mac wire 新增 optional `resolved`（映射 initialize
   `resolvedModel`，别名→canonical）；槽位短名用现有 `id`；观测改写名另键。
   iOS `CCCodeBridgeModel` 增补 `resolved`（及观测键）字段（wire 变化进
   docs/protocol/ canonical pack）。**不复用 `core.ModelOption.Alias`**
   （方向相反、wire 不下发，R2-S2）。
2. **set_model**：iOS 侧先补 `CCCodeBridgeBackendClient` 对 `BackendModelSetting`
   的 conformance（现状未 conform、原生 App 不调 switch_model；选择器只写本地、
   上送走 `send_message.model`）——adapter 接线完成后再谈选择器状态同步。
3. **interrupt**：新能力位；缺位时保持 abort→Close（杀进程）现状；**禁止**把
   合成 `turn_completed{aborted}` 冒充官方 interrupt 回执。
4. **实施前**：补 `.claudeCode` if-compare 清单（约 29 处 / 12 文件，编译器不报
   错，opencode-web 评审 M1 同类病）。
5. iOS 配套分支一律 `plan/approval-layer-ios`（§0.2）。

### 7.4 归档

docs/protocol/ 若涉及 wire 变化（模型行字段、能力位）同步 canonical pack；
CHANGELOG 按节追加。

---

## 8. 风险、阻断与取舍

| # | 风险 | 缓解 |
|---|---|---|
| 1 | CLI 2.1.234 不支持 `list_models`/`initialize.models`（SDK 配对 2.1.260，不同代） | Phase 0 探针硬门（代际矩阵）；不支持则 Phase 1 降级到观测+别名层，方案仍成立 |
| 2 | hooks 事件版本门（PostModelSwitch ≥2.1.251）与双代 CLI 并存 | §3.2 代际矩阵分列出结论；2.1.234 记文档级缺失；产品是否升级 CordCode CLI 另裁决 |
| 3 | cc-switch 场景：CLI 目录是请求侧名、真实执行被网关改写且映射不稳定（含 identity 行） | 真值以 `message.model` 为准；三键展示；§3.3 分布数据 |
| 4 | Managed 注入=admin/企业策略面/本机无落点 | 默认关（M4）；外部会话保持轮询不算失败；未来开启走单独 owner 裁决+卸载 |
| 5 | hooks 静默失效（exit 127 非阻塞，本机有样本） | 活性检测=Phase 3 验收硬条件；失活自动回退轮询 |
| 6 | settings.json env vs 进程 env 优先级未证（三值悬案活体样本） | Phase 0 探针 6 四元矩阵；结论回填 §3.4 并入 CLAUDE.md |
| 7 | Phase 2 与已落地 plan 审批层双轨（SetLiveMode/D5/auto-answer） | §6 Phase 2.3 冲突表 + 受限模式集；ExitPlanMode 纯 allow 不动 |
| 8 | abort→interrupt 改变 Stop hook/进程复用语义（Close 依赖 stdin EOF 跑 Stop hooks） | Phase 2.4 编码前二选一裁决（S8），与 Phase 3 Stop hook 耦合 |
| 9 | transcript 无合同格式漂移（CLI 升级） | Phase 4 fixture 回归 + 探针复测 |
| 10 | `list_models` 成功体无类型，猜字段=返工 | dump 之前禁止写解析器（M1/评审 §5 红线） |
| 11 | 配对未定即改代码（重演 2026-08-24 错工作树事故） | §1.3 冻结：plan/approval-layer 续作 + plan/approval-layer-ios；配对未定时停止 |

**取舍声明**：不追求"消灭文件面"——文件面是 claude 架构下唯一全量真相（dsh 方案
坑 1 的镜像教训）；追求的是每个功能面**显式归属**到 A/B/C/D 级并配对应纪律。

---

## 9. 非目标

1. 不嵌入 Node/SDK 运行时（路线 B 已否决）；
2. 不为 Claude Code 自建 server/事件总线（官方真空不代偿）；
3. 不动其他四个 backend；
4. 不解决 iOS 侧缓存陈旧问题（已有独立结论）；
5. 不在本方案内做生产 VPS / 真机 UI 自动化（按既有授权边界）；
6. 不默认开启 Managed 层 hooks 注入（M4 裁决）；
7. 不在运行中会话发送 `set_permission_mode plan|auto`；ExitPlanMode 批准保持纯
   allow（M3 裁决）。

---

## 10. 权威证据入口（后续实施/评审用）

```text
Agent SDK 类型契约 = /Users/jacklee/Projects/claude-agent-sdk-npm/package/sdk.d.ts（0.3.260，配对 claudeCodeVersion 2.1.260）
  · SDKControlRequest 信封 :4285-4291（request 嵌套）  · SDKControlInitializeResponse.models :3989-3994
  · ModelInfo（value/resolvedModel/displayName/effort 能力）:1266+   · SDKControlListModelsRequest :4051（成功体无类型）
  · SDKControlSetModelRequest :4377   · SDKControlSetPermissionModeRequest :4389（mode 必填）
  · Query.supportedModels :2738   · 磁盘 listSessions :992   · canUseTool :1454 / forkSession :1574 / hooks :1595 / resume :1910
Agent SDK changelog = /Users/jacklee/Projects/claude-agent-sdk-typescript（a79d677）
hooks 官方参考 = https://code.claude.com/docs/en/hooks（2026-09-04 抓取全文入 §2.2）
CLI 版本锚（三段式，S9）= PATH CLI 2.1.234（CordCode spawn）× Desktop 会话 2.1.258（claude-desktop-3p）× SDK 配对 2.1.260
cc-switch 实测现场 = ~/.cc-switch/cc-switch.db（providers / proxy_config / proxy_request_logs）
本轮调研全链路结论 = 本方案 §0.4 + 评审报告 §7（证据复核表）
模型目录失真根因 = cc-switch 双供应商配置漂移 + 网关模型改写（映射不稳定）+ settings.json 快照滞后 + GUI env 泄漏
```

CLAUDE.md「上游源码优先门」表格建议同步补一行（版本锚三段式）：

```text
| Claude Code | 官方文档 + Agent SDK 类型契约 + cli.js 定点取证（无开源源码） | https://github.com/anthropics/claude-agent-sdk-typescript + https://code.claude.com/docs |
```

---

## 11. 评审采纳记录（v1 → v2，2026-09-04）

评审报告：`docs/2026-09-04-claudecode-official-capability-upgrade-design-review.md`
（结论：修改后通过）。本轮修订**全部采纳，无不采纳项**；逐项落点：

### 11.1 阻断问题

| 项 | 内容 | 落点 |
|---|---|---|
| B1 | 控制信封 `request` 非 `payload`；control_response 嵌套；stdout 无配对 case | §3.1 重写（含树内/SDK 双锚）；§2.4、§6 Phase 2.1（v2.1 纠正：原落点误写"风险 10"，该风险属 list_models 无类型，R2-S6） |
| B2 | Phase 0 探针 spawn 必须逐字复用 `baseClaudeInnerArgs`（无 `-p`、stdin 保持打开、同 env 注入）；`-p` 对照不作判据 | §6 Phase 0.1 重写 |
| B3 | 双代 CLI（PATH 2.1.234 / Desktop 2.1.258 / SDK 配对 2.1.260）；PostModelSwitch ≥2.1.251 版本门；Phase 0/1/3 拆代际矩阵 | §0.3、§3.2 新增矩阵、§6 Phase 0.2、风险 2 |

### 11.2 必改问题

| 项 | 内容 | 落点 |
|---|---|---|
| M1 | Phase 1 主源漏 `initialize.models`（typed，含 resolvedModel）；`list_models` 成功体无类型须 dump-first；initialize 副作用入探针 | §2.1、§6 Phase 0.3 / Phase 1.1-1.2、风险 10 |
| M2 | `caps.modelCatalog` 非 typed cap；能力探测=capabilities[] 搜字符串+实发判据 | §2.1 list_models 行、§6 Phase 0.4 |
| M3 | 与已落地 plan 审批层的冲突冻结：方案=plan/approval-layer 续作、iOS 配 plan/approval-layer-ios、受限模式集、ExitPlanMode 纯 allow、删"在途"措辞、冲突表 | §0.1/§0.2、§1.3、§2.4、§6 Phase 2.3、§7.3.5、§9.6-9.7、风险 7 |
| M4 | Managed=admin 面/本机无落点/默认关；`--settings` 只覆盖自 spawn；无 admin=外部会话保持轮询不算失败 | §1.3、§6 Phase 3.1-3.2、§8 风险 4、§9.6 |
| M5 | iOS 侧实为新接线：adapter 未 conform、abort=杀进程、Alias 字段复用、能力位先行、if-compare 清单 | §2.4、§6 Phase 2.2、§7.3 重写 |
| M6 | 六处过强/错误断言（snapshot 不存在、listSessions 磁盘 API、interrupt cancel_queued、Bearer 非 API key 官方用法、message.model 未进目录、hooks 共存） | §2.1（删 snapshot 行）、§2.3、§2.1 interrupt 行、§2.3/§6 Phase 1.4、§2.4、§0.4 |

### 11.3 建议（S1–S10）

| 项 | 落点 |
|---|---|
| S1 initialize/get_context_usage/rename_session/ConfigChange 补行 | §5 四行（后两者标候选） |
| S2 PermissionRequest 死端点记录 + 不订阅 | §0.4、§2.2、§6 Phase 3.3 |
| S3 exit 127 活性样本升验收硬条件 | §2.2、§6 Phase 3.4、风险 5 |
| S4 标量优先级与 hooks 数组合并拆开表述 | §2.2 |
| S5 网关改写三列分布 + 不稳定映射 | §0.4、§3.3、§6 Phase 1.5 |
| S6 settings/runtime env 再分叉，四元矩阵探针 | §3.4、§6 Phase 0.6 |
| S7 loopback 排除后的降级去向 + 真网关/泄漏代理不混类 | §6 Phase 1.4 |
| S8 interrupt 与 Close/Stop hook 耦合的二选一裁决 | §6 Phase 2.4、风险 8 |
| S9 版本锚三段式 | §0.3、§10 |
| S10 落盘/配对冻结 | §0.1、§1.3、风险 11 |

### 11.4 裁决点（owner 2026-09-04 经本轮修订采纳）

1. **Managed 注入默认关**；CordCode 自 spawn 会话走 `--settings` 内联 HTTP hook；
   开启 Managed 须显式 admin 安装 + 卸载能力，且单独裁决。
2. **本方案定为 plan/approval-layer 续作**；iOS 配套 plan/approval-layer-ios；
   活会话不发 `set_permission_mode plan|auto`；ExitPlanMode 批准继续纯 allow；
   plan 审批层按"已落地"对待（§2.4）。

### 11.5 未采纳项

**无。** 评审全部阻断项、必改项、建议与两项裁决建议均已在 v2 落实；评审 §5 红线
（未 dump 形状不得当已核实）已内化为 §7.1 纪律。

### 11.6 第二轮评审（r2）采纳记录（v2 → v2.1，2026-09-04）

r2 结论：**通过（APPROVE）**，不阻塞进入 Phase 0；报告
`docs/2026-09-04-claudecode-official-capability-upgrade-design-review-r2.md`。
v2.1 全部采纳，无不采纳项：

| 项 | 内容 | 落点 |
|---|---|---|
| R2-S1 | 标量优先级官方顺序 Managed > `--settings` > local > project > user（v2 把 user/project/local 写反） | §2.2 |
| R2-S2 | 不用 `ModelOption.Alias` 承载 `resolvedModel`（方向相反、wire 从不下发 Alias）；Mac wire 新增 optional `resolved`；槽位名沿用 `id` | §2.4、§6 Phase 1.1/1.5、§7.3.1 |
| R2-S3 | 「冲突表」从段落做成四行真表（SetLiveMode / D5 / 本地 auto-answer / sessionActive） | §6 Phase 2.3 |
| R2-S4 | haiku 多映射分布（identity 538 / glm-4.7 325 / glm-5.3-flash 85+152 / mimo-v2.5 50 / glm-5-turbo 6 等，v2.1 时刻实测），删除单映射 66 | §0.4、§3.3、§6 Phase 1.5 |
| R2-S5 | `rename_session` 进 Phase 0.1 发送清单，与 Phase 4.2 对齐 | §6 Phase 0.1、Phase 4.2 |
| R2-S6 | initialize 类型行号 :3989；§11.1 B1 落点纠错；bypassPermissions 探针单列记录（需 `allowDangerouslySkipPermissions`，本地 auto-approve 无历史可推断） | §2.1、§5、§10、§6 Phase 0.1、§11.1 |
| R2-S7 | `initialize.hooks`（SDK callback matcher，走 `hook_callback`）与 `--settings` HTTP hook（settings 层）是两套机制；`hooks_applied` 只管前者；补"省略 initialize.hooks 不影响 --settings HTTP hook"对照项 | §6 Phase 0.3 |

### 11.7 第三轮评审（r3）采纳记录（v2.1 → v2.2，2026-09-04）

r3 结论：**通过（APPROVE），设计层可以停，下一步是 Phase 0 证据包**；报告
`docs/2026-09-04-claudecode-official-capability-upgrade-design-review-r3.md`。
v2.2 全部采纳，无不采纳项；按 r3 §2 要求**不追**瞬时 sqlite 计数（文档已声明
漂移，haiku 多映射事实为锚）：

| 项 | 内容 | 落点 |
|---|---|---|
| R3-S1 | §0.1 未提交状态纠错：设计稿是已跟踪修改（相对入库 v2=0514e97），未跟踪的是 r2/r3 评审 | §0.1 |
| R3-S2 | `resolvedModel` 例子不再用「haiku → glm-5.3」（那是观测改写名且非 haiku 主改写）；改为「`sonnet` → resolved `claude-sonnet-5`，观测可能是 `glm-5.3`」，写明 canonical 与观测是两件事 | §2.1 |
| R3-S3 | 观测改写名 wire 键名：Phase 1 实施时定（建议 `observedModel`）并进 protocol pack，探针前不强制 | §6 Phase 1.1 |
| R3-S4 | `rename_session` 成功体补进 §7.1「未 dump 不得当已核实」红线清单，与其他 control_response 同等 | §7.1 |
| 附注 | §2.4 树内锚 390ed6e → 1d60760（其后仅 docs 提交，"发送侧=零"结论未变；实施前按 HEAD 重核） | §2.4 |

---

实施、评审、构建或安装前必须按双仓 P0 规则重新生成来源清单；本文记录不能替代
未来复核。
