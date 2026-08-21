# codex-web Backend 设计评审报告（source-first 架构评审）

- 日期：2026-08-21
- 评审对象：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)（v1.1 草案，未实施）
- 评审方式：独立 source-first 评审。完整阅读 7 份背景文档（两仓 CLAUDE.md、dsh-web 设计、opencode-web source-first 收敛计划、T3Code 综合对比、GO_BRIDGE_ARCHITECTURE.md、think.md），逐项核对 Codex 官方源码（pin `536f86e5cc9ec1ff38457d099bf320b9d08eeeba`，与文档声明一致），并核对旧 `agent/codex` 代码与 testdata。
- 核对的官方源码范围：`app-server/README.md`、`app-server-protocol/src/protocol/common.rs` 与 `protocol/v2/` 全目录、`app-server/src/`（error_code、thread_state、thread_lifecycle、message_processor、transport、request_processors）、`app-server-transport/src/transport/websocket.rs`、`app-server-daemon/src/`、`app-server-client/src/{lib,remote}.rs`、`cli/src/main.rs`、`tui/src/lib.rs`、`tui/src/startup_orchestration.rs`、`tui/src/app/`、`tests/suite/v2/`。
- 评审边界：纯文档评审，未修改代码与设计文档。Desktop/VS Code extension 是否共用 daemon、`0.148.0-alpha.21` 物理载荷与源码一致性两点，按设计自身 Phase 0 纪律仍属未证事实，维持 Gate 地位，不计入任何结论。

---

## 1. 最终结论

**APPROVE WITH CHANGES**

总体判断：这份设计的路线、纪律结构和证据链要求是三份 backend 设计（dsh-web → opencode-web → codex-web）中最严格的一份，方向完全符合 CordCode 初衷（官方 app-server 是唯一真相源，MacBridge 只做 API 客户端 + bridge-v1 翻译器，不自托管 harness）。§3.1 的协议断言逐条对照源码后**全部属实**，§7 映射表的全部 method 名在官方注册表中存在。

但存在 **1 个阻断问题**：§3.3 对"普通 Terminal TUI 与 daemon 关系"的源码事实陈述错误/不完整，而这是全文核心 Gate（§8.2 外部 turn 实时流）的直接事实基础；且 Gate 实验矩阵没有控制决定性前提变量（daemon 运行状态 × TUI 启动配置），可能产出错误的 FAIL/PASS 裁决。另有 3 个必改问题（experimental 分级错误、provider/model 映射缺失、SSV2 接线点清单缺失）和 5 个建议问题。修订后可进入 Phase 0。

---

## 2. 阻断问题

### B1：§3.3 共享运行时事实基础错误，核心 Gate 实验缺少决定性前提变量

- **严重级别**：阻断（影响项目 go/no-go 裁决的正确性）
- **文档位置**：§3.3 末段（"当前官方 CLI 源码只在 `interactive.agents_overview` 路径自动启动 daemon；普通 TUI 随后仍进入自己的 `codex_tui::run_main`。因此'启动 daemon 就能旁观所有普通 CLI turn'目前没有成立证据"）；§8.2 表；§12 "external host" 行；Phase 0 第 4 步
- **官方源码证据**：
  - `codex-rs/cli/src/main.rs:2588-2602`：daemon 自动启动确实只发生在 `interactive.agents_overview && remote.is_none()`（即 `codex agents`）——文档前半句正确；
  - `codex-rs/tui/src/lib.rs:909-920`：`can_reuse_implicit_local_daemon()` 对**默认启动配置**（无 `-c` 覆盖、非 strict、无非可重放覆盖）返回 true；
  - `codex-rs/tui/src/startup_orchestration.rs:138-143`：`reuse_implicit_local_daemon = !workload_identity && (cli.agents_overview || can_reuse_implicit_local_daemon(...))`——普通 `codex` 启动也会进入复用判定；
  - `codex-rs/tui/src/startup_orchestration.rs:178-186` + `tui/src/lib.rs:437-459`：`maybe_probe_default_daemon_socket()` 探测默认 control socket（unix only）；
  - `codex-rs/tui/src/lib.rs:850-876`：`app_server_target_for_launch()`——socket 存活 ⇒ `AppServerTarget::LocalDaemon`（TUI 整个 session 挂在共享 daemon 上），否则 `Embedded`；
  - `codex-rs/app-server-daemon/src/lib.rs:263-266`：daemon 绑定的正是同一个默认 socket 路径 `app_server_control_socket_path(codex_home)`；
  - `codex-rs/app-server/src/thread_state.rs:533-581`：`try_ensure_connection_subscribed`/`try_add_connection_to_thread` 证明单 daemon 进程内**多连接订阅同一 thread** 是一等公民模型。
- **为什么造成实现错误或返工**：文档措辞（"普通 TUI 随后仍进入自己的 run_main"）会让实施者得出"普通 TUI 不走 daemon"的错误预期。真实机制是双向的：
  1. daemon **未运行**时，普通 TUI 嵌入自有 app-server（Embedded）——此时 codex-web 连 daemon **看不到**这些 turn；
  2. daemon **已运行**（由 CordCode、`codex app-server daemon start` 或一次 `codex agents` 启动）时，默认配置的普通 TUI **会 attach 该 daemon**——外部 turn 旁观在源码层面有成立机制；
  3. TUI 带 `-c` 覆盖/strict-config/非可重放覆盖时，即使 daemon 在跑也 Embedded——旁观**不 universal**。

  §8.2 的 Terminal 行只写"Mac 发长回答，iPhone 打开同 session"，没有把"daemon 是否在运行""TUI 启动方式（默认 vs 带覆盖）"列为受控前提。Phase 0 若在 daemon 未启动的状态下开 Terminal TUI 测试，会得到假 FAIL 并按 §8.2 规则"停止实施"；反之只在 `codex agents` 场景测试会得到不可泛化的假 PASS。整个项目的核心 Gate 裁决会建立在未受控实验上。
- **建议修订**：
  1. 重写 §3.3 末段为三条源码事实（autostart 仅 agents 路径 / 默认配置 TUI 探测并复用运行中 daemon / 带覆盖启动强制 Embedded），引用上列文件行号；
  2. §8.2 表增加"前提条件"列，Terminal 行至少拆成三行：daemon 已运行 + 默认配置 TUI；daemon 未运行 + TUI；daemon 已运行 + 带 `-c` 覆盖 TUI（预期 Embedded 隔离，如实记录）；
  3. Phase 0 第 4 步与 §12 "external host" 行同步加这两个变量；明确判定规则：只有"daemon 运行 + 默认配置 TUI"路径 PASS 才算共享运行时 PASS，Embedded 隔离路径记为已知边界而非 FAIL。

---

## 3. 必改问题

### M1：§7 功能映射表的稳定性分级与官方 experimental 标注不一致（三处）

- **严重级别**：必改（违反文档自身的 §7 图例与 §11.2 门控纪律）
- **文档位置**：§7 表 "structured questions"、"permission request"、"plan/todos" 三行
- **官方源码证据**：
  - (a) requestUserInput：`app-server/README.md:278` "tool/requestUserInput — prompt the user with 1–3 short questions … **(experimental)**"；`app-server-protocol/src/protocol/v2/item.rs:1622/1631/1646/1690/1698` 全部标注 `/// EXPERIMENTAL`；
  - (b) availableDecisions：`item.rs:1505-1508` `#[experimental("item/commandExecution/requestApproval.availableDecisions")]`——它属于 **commandExecution** 审批；`app-server/src/transport.rs:174-192` `filter_outgoing_message_for_connection`：未启用 `experimentalApi` 时 `strip_experimental_fields()` 剥除 `availableDecisions`/`additionalPermissions`；`app-server/src/message_processor.rs:892-897`：experimental request 未开 capability 直接 `invalid_request`；而 `permissions.rs:773-785` 的 `PermissionsRequestApprovalParams` **没有** availableDecisions 字段（载荷是 `permissions: RequestPermissionProfile`，响应是 `GrantedPermissionProfile` + scope）；
  - (c) plan：`README.md:1636/1670` 与 `item.rs:1360` "item/plan/delta — streams proposed plan content for plan items **(experimental)**"——plan **item 生命周期**（started/completed）稳定，plan **delta 流式**是 experimental。
- **为什么造成实现错误或返工**：设计自己的图例规定 🧪 = "有官方 experimental 面，取样与版本门控后才启用"。(a) 行标 ✅ 意味着一期可不等版本门控就广告多题问答；(b) 行让 iOS 按 ✅ 基线设计"availableDecisions 渲染"，但若 codex-web 不开 `experimentalApi`，该字段在运行时被 server 剥除，UI 行为错乱——且该字段根本不在 permissions 审批上，属张冠李戴；(c) plan 行不区分 item 生命周期与 delta 流式，实施者可能直接消费 `item/plan/delta`。
- **建议修订**：(a) 改 🧪 并在说明中注明"README 标 experimental、类型 doc 标 EXPERIMENTAL，但 core config `tools.experimental_request_user_input` 默认启用、不经 experimentalApi 门控——仍须按 🧪 流程取样+版本门控"；(b) 拆开两个审批的字段描述：commandExecution 审批的 `availableDecisions`/`additionalPermissions` 标注"experimental 字段，未开 experimentalApi 时被服务端剥除（transport.rs:174-192），一期按剥除后形状设计 UI"；permissions 审批改为"呈现官方 RequestPermissionProfile 语义"；(c) plan 行注明"item 生命周期 ✅，`item/plan/delta` 流式 🧪"。

### M2：§7 映射表缺 provider/model 切换行；memory ⛔ 行理由错误——与退役门槛 #3/#4 的"无能力倒退"检查形成盲区

- **严重级别**：必改（能力倒退到退役裁决阶段才暴露 = 返工）
- **文档位置**：§7 表（list_models 之后缺行；"memory files | 无稳定等价面 | ⛔ | 不从 rollout 猜"）；§15 退役门槛
- **官方源码/仓库证据**：
  - 旧 `agent/codex` 实现 `ProviderSwitcher`（`agent/codex/codex.go:723-765` SetProviders/SetActiveProvider/ListProviders）、`SetModel`（`codex.go:176`）、`MemoryFileProvider`（`codex.go:701-720`，读项目/`CODEX_HOME` 的 AGENTS.md）——旧 backend 广告 `provider_switch`、模型切换与 memory 能力；
  - 官方面存在：`v2/thread.rs:61-63` `ThreadStartParams { model: Option<String>, model_provider: Option<String>, ... }`；`v2/turn.rs:122-124` `TurnStartParams.model` 注释 "Override the model for this turn and subsequent turns"；
  - memory 的事实：官方 memory 面是 AGENTS.md 输入文件（旧 backend 直读），与 rollout/session 真相无关——"无稳定等价面｜不从 rollout 猜"这条理由把两件事混为一谈。
- **为什么造成实现错误或返工**：codex-web 正确地禁止写 `config.toml`（§1），因此 provider/model 选择**只能**走 `thread/start`/`turn/start` 参数面——但表格没有任何一行映射 `switch_model`/`list_providers`/`set_provider`，实施者没有接线指令就会漏实现 `ModelSwitcher`/`ProviderSwitcher`，直到 §15 门槛 #4"cwd/provider/model 不变"验收时才发现 iOS 无法选第三方 provider 新建 session——这正是 CordCode 初衷的核心卖点之一。memory 行的错误理由则会让评审/实施者无法区分"故意不做"与"没做"。
- **建议修订**：
  1. §7 增两行：`list_providers/switch_model` → `thread/start{model,modelProvider}` + `turn/start{model}`（注明"旧 backend 经 provider_config.go 写 CLI 配置的路径在本 backend 禁止，全部走官方请求参数"），按 Phase 4 取样后定级；
  2. memory 行理由改为"deliberately unsupported：app-server 无 memory 文件 API；AGENTS.md 为官方输入文件而非 session 真相，一期不接（与 dsh-web 同判），如二期接入须走只读 AGENTS.md 路径并另行评审"，并把 memory/provider/model 三项列入 §15 退役对照清单，避免静默倒退。

### M3：SSV2/kernel 接线点清单缺失——dsh-web 评审 M4"漏任一处对应机制静默失效"的直接复发风险

- **严重级别**：必改
- **文档位置**：§9（真相清单）、§14 Phase 2（只写了"pathless hydrate、archive、rename/delete"）
- **证据**：dsh-web 设计 §4.3.2 的接线点表（backendSupportsProjectionHydrate / prepareProjectionHydrateSource pathless 分支 / produceProjectionHydrateRange / 两处 forceCold 集合 / agent_descriptor / main.go / server.go 注册）是该 backend 四轮评审中的必改项 M4，理由是"漏任一处对应机制静默失效"；codex-web 同样要进 pathless 家族（冷基线 `thread/read(includeTurns)`），但本文没有任何一张对应清单。另外 dsh-web 的 M1 教训（死会话尾部未答 turn 的封口需要 `SessionActivityProbing`）在 codex-web 有更好的官方解——thread/turn 自带 status——但设计没有写明用官方 status 作为 hydrate commit gate 的封口依据。
- **为什么造成实现错误或返工**：pathless hydrate 是"配置装配型"机制，漏一个集合该 backend 的冷开就静默退化为空投影或走错家族分支，且无报错可排查——这正是 opencode-web/dsh-web 都踩过的坑。
- **建议修订**：在 §9 或 §14 Phase 2 增"SSV2 接线点清单"表（逐点列出上述 7 处 + pathless 家族归属 + 明确不进 codex 旧 store-file 分支），并加一行"死会话尾封口：以官方 `thread.status`/turn `status` 为权威依据，评估是否需实现 `SessionActivityProbing` 等价物"。

---

## 4. 建议问题

### S1：steer 行补充必填前置 `expectedTurnId`

- 级别/位置：建议；§7 "steer" 行。
- 证据：`v2/turn.rs:193-196` `expected_turn_id: String`，注释 "Required active turn id precondition. The request fails when it does not match the currently active turn"。
- 影响：bridge 的 steer 路径必须跟踪当前 active turnId 才能发 `turn/steer`，这是 iOS 侧 API 语义变化点，宜在设计期写明而非留到 Phase 0 样本冻结才发现。

### S2：MCP elicitation 实为三个 variant，且 openai/form 需要 initialize capability

- 级别/位置：建议；§7 "MCP elicitation" 行（"form/url 两类"）。
- 证据：`v2/mcp.rs:733-758` `McpServerElicitationRequest::{Form, OpenAiForm(serde 名 "openai/form"), Url}`；`app-server-client/src/remote.rs:83-103` `RemoteAppServerConnectArgs.mcp_server_openai_form_elicitation` 是 initialize capability 之一。
- 修订：改为"Form/openai-form/Url 三类；openai/form 需在 initialize capabilities 中声明 `mcpServerOpenaiFormElicitation`"。

### S3：§17 权威证据入口的路径错误与缺漏

- 级别/位置：建议；§17。
- 证据：`app-server-protocol/src/protocol/v2.rs` **不存在**——v2 是目录（`v2/mod.rs`、`thread.rs`、`turn.rs`、`item.rs` 等 35 个文件）；WS listener、`/healthz`//`readyz`、非 loopback 认证强制在独立 crate `codex-rs/app-server-transport/src/transport/websocket.rs`（61-75、135-150）；daemon 复用判定在 `tui/src/lib.rs` + `tui/src/startup_orchestration.rs`——后两处恰是 B1 所涉事实的权威源，却不在证据入口清单里。
- 另：§7.1 第一句"turn/completed 只含最终 agent message fallback"与实际载荷不符——`TurnCompletedNotification = {threadId, turn: Turn}`，`Turn.items` 带三档 `itemsView`（NotLoaded/Summary/Full，`v2/thread_data.rs:350-383`）。方向（别拿 turn 载荷当工件源）是对的，措辞建议校正为"turn/completed 携带 `turn.items`（itemsView 可为 Full/Summary/NotLoaded）与 status/error；完整工件以持续消费 `item/*` 为准"。

### S4：§13.1 provenance 测试的禁止 import 清单补 `transcriptindex`

- 级别/位置：建议；§13.1 provenance 行（现只写"不 import `agent/codex`，无旧 session/history/file-relay/cache type alias"）。
- 理由：`transcriptindex/` 是 transcript JSONL 页索引层（Claude/Codex 文件历史路径在用）；codex-web 若 import 它即引入 JSONL 依赖，违反 §9"禁止旁路"，但 §2.2 的允许复用清单（"core、bridge-v1、SSV2、通用目录/git"）措辞不足以排除它。把 `transcriptindex` 及任何 rollout/file-relay 包加进 CI 可查的禁止清单。

### S5：§6.3 补记"CordCode 启动 daemon 的用户可见副作用"

- 级别/位置：建议；§6.3 进程归属。
- 理由：由 B1 的源码证据，CordCode 启动 daemon 后，用户**默认配置**的普通 Terminal `codex` 会 attach 该 daemon（行为静默改变）；CordCode 退出不停止 daemon 的取舍应记录这一影响面，并在诊断中区分"复用用户已有 daemon"与"CordCode 启动、用户 TUI 已 attach"两种状态，供 §10.2 冲突排查和未来生命周期决策使用。

---

## 5. 对"空目录独立实现"约束的专项审计

**结论：约束本身是三份设计中最强的，可执行；仅一处清单缺口（S4）。**

- §0/§2.2 用加粗引用块给出硬约束（"必须从一个空目录开始。禁止复制、改名、裁剪或包装"），并枚举五种禁止形态：`cp -R` 后修改、import 或 type alias/wrapper 复用旧 `Agent`/session/catalog/history/passive subscriber/file relay/codec/cache、复制文件仅改 package/name、共享基类套壳、**用旧 rollout/file-relay fixture 反向定义 API 形状**——最后一条直接封死了循环证据链（fake fixture 自证）。
- 允许复用面收窄为两类（跨 backend 公共接口；独立实现稳定后另案抽取），并明确"不得为了省第一版工作量预先抽取"——消解了 dsh-web 评审 M3 那类"复用 vs 独立"矛盾。
- §2.4 把 OpenCode Web 的失败路径画成禁止路线图；§2.1 基线表如实承认旧 `codex` 已在用 app-server（避免了"终于升级到官方 API"的虚假叙事）——与 GO_BRIDGE_ARCHITECTURE.md/CLAUDE.md 的现状描述一致。
- §13.1 provenance 测试 + §16 非目标 + §14 Phase 1 "从空目录新建……旧目录零行为改动"三处闭环。
- 实地核验：`agent/codex-web` 目前不存在 ✅；`agent/codex`（含 testdata）与 `agent/dsh-web` 均在，可作为 §2.2 禁令的对照物。
- 缺口：provenance 禁止清单未含 `transcriptindex`（见 S4）。

## 6. 对 Codex 官方源码/API 断言的逐项核查表

| # | 文档断言 | 官方证据 | 结论 |
|---|---|---|---|
| 1 | pin `536f86e5…` | `git rev-parse HEAD` 一致 | ✅ |
| 2 | transport 支持 `stdio://`、`unix://`、`unix://PATH`、`ws://IP:PORT` | README:26-28 | ✅ |
| 3 | WS listener 有 `/healthz`/`/readyz`；loopback 免远程 auth，非 loopback 强制认证 | `app-server-transport/src/transport/websocket.rs:61-75,135-150`（无 auth 拒绝启动并提示 `--ws-auth`）；README:31-34 | ✅（细节：healthz 要求无 Origin 头，README:34） |
| 4 | v2 提供 `thread/*`、`turn/*`、`item/*`、`model/list`、`config/read`、`permissionProfile/list` | `protocol/common.rs` method 注册表全量核对 | ✅ |
| 5 | §7 表全部 26 个 method 名 | common.rs 注册表逐一存在（含 `thread/name/set`、`thread/fork`、`thread/unarchive`、`thread/tokenUsage/updated`、`serverRequest/resolved`） | ✅ |
| 6 | turn 生命周期 `turn/started`→`item/started`→delta→`item/completed`→`turn/completed` | README:81-83、211、1616 | ✅ |
| 7 | 审批/提问/elicitation 是 server-initiated request，必须回 response | 客户端 `resolve/reject_server_request`（client lib.rs:516-573）；README:1741-1745 | ✅ |
| 8 | `thread/read` 只读、`includeTurns` 含历史不 resume | README:176 | ✅ |
| 9 | `thread/turns/list` experimental，不能作一期唯一历史路径 | README:177 "experimental" | ✅ |
| 10 | 单 writer：另一进程持有时 `thread/resume`/`archive`/`delete` 返回 `-32600`，只读仍可用 | README:369；`app-server/src/error_code.rs:3`；可执行契约 `tests/suite/v2/thread_resume.rs:330-334`（错误文案 "thread … already has an active writer"） | ✅ |
| 11 | `thread/unsubscribe` 只取消本连接订阅；最后一个订阅离开后约 30 分钟才卸载并 `thread/closed` | README:201、541-558；`request_processors/thread_lifecycle.rs:7` `THREAD_UNLOADING_DELAY = 30*60s`；v2 test `thread_unsubscribe.rs:39` | ✅ |
| 12 | steer 仅 active regular turn，review/compact 拒绝 | README:213 "Review and manual compaction turns reject turn/steer" | ✅（但漏 `expectedTurnId` 必填，S1） |
| 13 | requestUserInput 1–3 题批结构 | README:278；`item.rs:1647-1657` `questions: Vec<…>`、`isBlocking`、答案 `{answers: HashMap<id, {answers: Vec<String>}>}` | ⚠️ 形状属实，但官方标 experimental，文档标 ✅（M1a） |
| 14 | permissions 审批"只呈现官方 availableDecisions/profile 语义" | `item.rs:1505`（experimental，属 commandExecution）+ `transport.rs:189`（未开 experimentalApi 即剥除）+ `permissions.rs:773-785`（无该字段） | ❌ M1b |
| 15 | MCP elicitation "form/url 两类" | `mcp.rs:733-758` 三 variant | ⚠️ S2 |
| 16 | context usage 用官方窗口与累计量 | `thread.rs:1774-1780` `ThreadTokenUsage{total,last,model_context_window:Option}` | ✅（注意 `model_context_window` 可空） |
| 17 | 官方 client 有 request queue、ordered event queue、server request registry、bounded shutdown | client lib.rs:317-582（request/request_typed/notify/resolve/reject/next_event/shutdown）；remote.rs:154 `pending_events: VecDeque`、216-248 pending_requests 相关 | ✅ |
| 18 | daemon CLI：`daemon start/version/stop/restart`、`app-server proxy --sock <path>` | cli/main.rs:702-727；daemon lib.rs:80 `probe_app_server_version` | ✅ |
| 19 | "只在 agents_overview 路径自动启动 daemon" | main.rs:2588-2602 | ✅（半句） |
| 20 | "普通 TUI 随后仍进入自己的 run_main（不依赖 daemon）" | lib.rs:437-459、850-876、909-920；startup_orchestration.rs:138-186：默认配置 TUI 探测并复用运行中 daemon | ❌ B1 |
| 21 | §2.1 基线表（旧 codex 每 session stdio app-server、显式 URL 走共享 WS、file relay 仍在） | 本仓 CLAUDE.md + GO_BRIDGE_ARCHITECTURE.md + think.md（31-38 帧/turn 实测） | ✅ |
| 22 | §3.2 旧 testdata 清单 | `agent/codex/testdata/` 逐一存在（thread_list_sanitized.json、turn_started/completed、agentMessage/reasoning delta、command、fileChange、mcpToolCall、dynamicToolCall、contextCompaction、turn_plan_updated、structured_user_input/） | ✅ |
| 23 | §2.3 列出的 TUI 文件 | `tui/src/app/{session_lifecycle,app_server_events,thread_events,thread_routing}.rs` 均存在 | ✅ |
| 24 | §2.3 移植许可 | Codex 为 Apache-2.0（LICENSE 首行），"移植算法+注释记录上游"可行 | ✅ |

## 7. 对共享运行时与外部 turn 实时流 Gate 的专项结论

**结构判定：合格。事实基础：有错（B1）。**

- 设计没有把共享运行时当作既成能力：§3.3 把五点列为 Phase 0 前的假设、§8.2 定义 PASS/PARTIAL/FAIL、PARTIAL 不得退役旧 backend、§15 门槛 #2 要求 PASS 或 owner 明示接受 PARTIAL、§12 external host 样本组——这正是"未经源码与真实样本证明的能力必须保持 Gate"的正确形态。
- 但如 B1 所证：源码层面**支持**该机制的证据（默认配置 TUI 复用 daemon + 单进程多连接订阅模型 thread_state.rs:533-581）已存在，文档却以一条不完整的源码陈述导出"没有成立证据"的结论，且实验矩阵未控制决定性变量。同时必须强调：即使 Gate PASS，**带 `-c` 覆盖/strict 的 TUI 启动永远是 Embedded**，"旁观所有普通 CLI turn"在设计目标措辞（§0 命题 2）上应降为"旁观默认配置下的普通 CLI turn"，否则验收口径自身不可满足。
- `codex-web/iOS 自己发起 → Mac 官方客户端续聊` 方向（§8.2 第四行）与双向接力（门槛 #4）无争议：thread 持久化在 CODEX_HOME，`thread/resume`/`thread/list` 覆盖。

## 8. 对 SSV2、ownership 与旧 backend 并存/退役的专项结论

- **SSV2 闭环（§9）**：真相 owner（官方 app-server）、唯一 writer（全部写入经官方 JSON-RPC）、可丢弃投影、identity 五元组（backendId+threadId+turnId+itemId+connection epoch）、"删除 checkpoint 后只靠官方 API 重建"验收（§13.1）、重连固定为 read-校准-再按需 resume（§8.3，不假设 replay cursor——与官方无 since/replay 语义一致）、pending 交互只在官方重发后才 surface——逻辑闭环 ✅。缺口是 M3 的接线点清单与死会话封口依据。
- **事件 identity（§7.1）**：`(threadId, turnId, itemId)` reducer 身份 + 禁止正文相似度去重 + "未识别通知 fail-closed 而非递归猜字段"——与 think.md 2026-07-22 rollout identity 复盘的教训一致 ✅。
- **JSONL fallback 禁令**：§9"禁止旁路"、§16 非目标、§6.2 "不得退回 JSONL parser 假装可用"——闭环 ✅（S4 补 transcriptindex 后更严）。
- **并存（§10）**：旧 `codex` 每 session stdio app-server 是**独立进程**，与 daemon 互为"另一 writer"——`-32600` 冲突路径真实存在（README:369 + thread_resume.rs:330），§10.2 的冲突翻译/禁自动 kill 设计成立；"切回旧 backend 前观察 `thread/closed`"与 30 分钟卸载语义精确吻合（README:549）✅。A/B 用不同 id 测试 session 的隔离规则正确。
- **退役（§15）**：10 条门槛 + "先移除入口、源码留观察窗、旧 session 不迁移（必须能直接打开原 thread）"符合初衷 ✅。缺口：provider/model/memory 的能力对照未入门槛清单（M2）。

## 9. 文档中准确、应保留的设计决策

1. **§0 的身份定义**（"官方长驻 app-server 的 API 客户端 + bridge-v1 协议翻译器"）与 IMPORTANT 块对"官方 API"的三重排除（非 Responses API、非自建兼容服务）——一句话消灭身份漂移。
2. **§2.1 基线表**：如实承认旧 `codex` 已在用 app-server，把差异正确锚定为"共享长驻服务 + 官方 history API + 外部 turn 实时流"，而非虚构的"从 exec 升级"。
3. **§3.0 六级证据优先级 + 完整证明元组**——比 dsh-web/opencode-web 的对应条款更完整（新增"官方客户端 call site"为第一优先级）。
4. **§3.2 对旧 fixture 的定性**："证明现有 codec 的来源，不等于自动冻结 0.148.0-alpha.21"——正确阻断旧样本冒充新版本证据。
5. **§3.4 bug 修复纪律**：七步定位"第一处分歧"、四条禁止（复制旧 handler/递归 JSON 搜索/重造官方已有 queue/同一假设叠修复）、"一次定向修复未改变现象必须停止产品代码修改"——把 think.md 全部历史教训（rollout EOF 假完成、delta/completed 重复、provider 非流式）制度化。
6. **§6.2 探活 ≠ 能力就绪**的六步递进（transport→initialize→initialized→thread/list→model/list→contract 一致），任一失败即 `not_configured`/`incompatible`。
7. **§6.3/§10.2 进程归属与冲突处理**：复用不 stop/restart、`startedByCordCode` 标记、禁自动 kill 未知进程。
8. **§7.1 事件红线**与 **§7.2 registry 纪律**（epoch+request id 键控、不向新连接重放旧 response、"Mac/iOS 先答者得必须实测、不能沿用 DSH 结论"）。
9. **§8.1 对"逐字流式"的克制**：预先排除 provider 单帧/Bridge 33ms 批处理/iOS 66ms 渲染节奏的混淆变量，并把帧级指标写进 §13.2。
10. **§12 样本包**明确"手写 fake server 只能测内部逻辑，不能作为外部协议形状证据"；§13.1 把 provenance/ownership/SSV2 重建全部列为测试项。

## 10. 可直接执行的逐条修订清单

按优先级执行；全部为文档修订，不涉及代码。

| # | 位置 | 修订动作 | 对应问题 |
|---|---|---|---|
| 1 | §3.3 末段 | 重写为三条源码事实并附行号：autostart 仅 agents 路径（cli/main.rs:2588-2602）；默认启动配置的普通 TUI 探测并复用运行中 daemon（tui/lib.rs:909-920、437-459、850-876；startup_orchestration.rs:138-186）；带 `-c` 覆盖/strict 启动强制 Embedded。结论改为"机制存在但覆盖面有边界，覆盖面本身是 Phase 0 待测事实" | B1 |
| 2 | §8.2 表 | Terminal 行拆三行并加"前提条件"列（daemon 已运行+默认配置 / daemon 未运行 / daemon 已运行+带覆盖→预期 Embedded 隔离）；判定规则注明只有第一路径 PASS 才算共享运行时 PASS | B1 |
| 3 | §12 external host 行 + Phase 0 第 4 步 | 同步加入 daemon 状态 × TUI 启动方式两个受控变量及采样要求 | B1 |
| 4 | §0 命题 2 措辞 | "Mac 上由官方 Codex 客户端发起的 turn"限定为"默认启动配置下的官方客户端 turn（覆盖启动的隔离边界见 §3.3）" | B1 |
| 5 | §7 structured questions 行 | ✅→🧪；说明加"README:278/类型均标 experimental，core config 默认启用、不经 experimentalApi 门控；仍须取样+版本门控" | M1a |
| 6 | §7 permission request 行 + command approval 行 | permissions 审批改为"呈现官方 RequestPermissionProfile 语义（permissions.rs:773-785）"；commandExecution 审批注明 availableDecisions/additionalPermissions 为 experimental 字段、未开 experimentalApi 被 transport.rs:174-192 剥除，一期按剥除后形状设计 | M1b |
| 7 | §7 plan/todos 行 | 拆注：item 生命周期 ✅；`item/plan/delta` 流式 🧪（README:1636/1670） | M1c |
| 8 | §7 新增两行 | `list_providers/switch_model` → `thread/start{model,modelProvider}`（thread.rs:61-63）+ `turn/start{model}`（turn.rs:122-124）；注明禁写 config.toml、全部走请求参数，Phase 4 取样后定级 | M2 |
| 9 | §7 memory 行 + §15 | memory 理由改为 deliberate-unsupported（AGENTS.md 是官方输入文件非 session 真相，一期不接，二期如接须只读+另案评审）；§15 增补 provider/model/memory 能力对照项 | M2 |
| 10 | §9 或 §14 Phase 2 | 增 SSV2 接线点清单表（backendSupportsProjectionHydrate / pathless 分支 / produceProjectionHydrateRange / 两处 forceCold / agent_descriptor / main.go / server.go；不进 codex 旧 store-file 分支）+ 死会话尾封口以官方 thread/turn status 为权威依据并评估 SessionActivityProbing 等价物 | M3 |
| 11 | §7 steer 行 | 补"必填 `expectedTurnId` 前置（turn.rs:193-196），bridge 须跟踪 active turnId" | S1 |
| 12 | §7 MCP elicitation 行 | "form/url 两类"→"Form/openai-form/Url 三类（mcp.rs:733-758）；openai/form 需 initialize capability `mcpServerOpenaiFormElicitation`" | S2 |
| 13 | §17 | `…/protocol/v2.rs` → `…/protocol/v2/`（目录）；增补 `codex-rs/app-server-transport/`（listener/auth/healthz）与 `tui/src/lib.rs`、`tui/src/startup_orchestration.rs`（daemon 复用判定）；§7.1 第一句按 `Turn{items,itemsView}` 校正（thread_data.rs:350-383） | S3 |
| 14 | §13.1 provenance 行 | 禁止 import 清单补 `transcriptindex` 与任何 rollout/file-relay 包 | S4 |
| 15 | §6.3 | 补记 CordCode 启动 daemon 的副作用（默认配置 TUI 将 attach；诊断区分"复用已有"与"CordCode 启动且用户 TUI 已 attach"） | S5 |
