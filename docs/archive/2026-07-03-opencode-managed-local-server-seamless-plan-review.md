# 代码走读级评审：OpenCode managed local server 无缝接入方案

> 评审对象：[docs/2026-07-03-opencode-managed-local-server-seamless-plan.md](../2026-07-03-opencode-managed-local-server-seamless-plan.md)
> 评审日期：2026-07-03
> 评审类型：方案文档（spec）评审，非产品代码评审（本轮无产品代码改动）
> 评审方法：逐节对照 MacBridge / go-bridge 现有源码核对"已存在/已修复"声明，并核查方案建立在哪些已证 vs 未证假设之上。

## 0. 总体结论

**结论：方案方向正确、复用映射准确、非目标边界清醒，但当前形态"还不能直接进入开发"**，有三个必须先关掉的硬伤，另有若干应在动工前澄清的契约与 UX 缺口。

一句话评价：**这是一份诚实的、对现有代码理解准确的规格，但它把若干尚未被仓库证据支撑的 `opencode serve` 行为当成了"事实"，并且其标题目标（"无缝"）与正文范围（不动自动重启 Desktop）在最常见的真实场景下并不自洽。**

### 三个 Must-fix（进入开发前必须解决）

| # | 问题 | 严重度 |
| --- | --- | --- |
| **B1** | 文档可跟踪性：`/docs/*` 被 `.gitignore` 忽略（见 `.gitignore:32`），本规格无法走 PR 评审，且 CHANGELOG（已跟踪）引用了 git 中不存在的文件 | 流程 |
| **B2** | `opencode serve` 命令的多个关键假设未被仓库证据支撑（`--print-logs`、默认 4096 起搜索端口） | 正确性 |
| **B3** | 标题目标"无缝"与正文范围冲突：Desktop 已运行时不会重读配置，最常见场景仍需手动重启 Desktop | 范围 |

### 已核对正确的复用声明（给作者的正面反馈）

方案 §4 列出的"现有实现可复用部分"**逐项属实**，证据见本文 §3。作者对现有代码的理解是准确的，没有出现"声称已修但其实没改"或"复制旧设计而不反查源码"的情况——这在这个仓库的历史方案里属于高水位。

---

## 1. 评审方法与证据基线

本次评审对照以下源码逐行核对（行号截至评审时 working tree）：

- `MacBridge/MacBridge/Services/OpenCodeEndpointResolver.swift`
- `MacBridge/MacBridge/Services/RuntimeManager.swift`
- `MacBridge/MacBridge/App/AppDependencies.swift`
- `agent/opencode/opencode.go`、`agent/opencode/sse_subscriber.go`
- `go-bridge/main.go`、`go-bridge/opencode-proxy.go`、`go-bridge/handlers.go`
- `core/message.go`（控制面 env deny-list）
- `MacBridge/MacBridgeTests/*`、`go-bridge/*_test.go`
- `.gitignore`、`CHANGELOG.md`、`config.example.env`、`BUILD_INSTALL_AND_RUNTIME.md`

---

## 2. Must-fix 详细说明

### B1. 文档可跟踪性：规格本身无法被评审流程捕获

**证据**

```text
.gitignore:27-33
# docs/ work notes — local-only (plans/specs/reviews/completion reports)
...
/docs/*
!/docs/protocol/
```

```text
$ git check-ignore -v docs/2026-07-03-opencode-managed-local-server-seamless-plan.md
.gitignore:32:/docs/*    docs/2026-07-03-opencode-managed-local-server-seamless-plan.md
```

**影响**

1. 本规格标注为"待评审的实现规格"（§标题），但它不会出现在任何 `git status` / PR diff 里——reviewer 无法在常规流程里看到它，§13"Reviewer 重点检查"事实上没有附着点。
2. `CHANGELOG.md:11-13`（CHANGELOG 是被跟踪的）记录了"新增 `docs/2026-07-03-...md`"，但该路径在 git 中不存在。任何 clone 仓库的人/CI/未来的 agent 顺着 CHANGELOG 找文件会落空，相当于跟踪文件引用了一个未跟踪文件。
3. 同类历史方案（如 `docs/2026-07-02-opencode-shared-service-discovery-plan.md`）也是 local-only，意味着这个项目的方案/评审/完成报告一族文档**整体不在版本控制里**，跨 session/跨人协作时只能靠本机文件系统传递。

**建议（任选其一，需 owner 决策）**

- **A. 把"对外的规格/评审/完成报告"纳入跟踪**：在 `.gitignore` 增加 `!/docs/2026-*.md` 或新建被跟踪的 `specs/` 目录专门放跨人评审的规格，`docs/` 继续做纯个人 work notes。
- **B. 接受 docs/ 全部 local-only**：则 CHANGELOG 不应记录具体 docs 文件名，改为记录"产出本地方案文档"即可，避免跟踪文件引用未跟踪文件。
- 推荐 A：本仓库 CLAUDE.md 强调"这些是持续更新的架构/运维真值"，而真值应当可跟踪、可评审。

---

### B2. `opencode serve` CLI 的关键假设缺乏证据

方案在 §0 把以下内容陈述为"stable `opencode` 1.17.13 的事实"：

> 第 32 行：`stable opencode serve 支持 loopback server + Basic Auth，且默认从 4096 起找可用端口。`
> 第 250 行启动命令：`opencode serve --hostname 127.0.0.1 --port <port> --print-logs`
> 第 268 行端口策略：`否则从 4096 开始探测到 4196`

**逐项核查**

| 声明 | 仓库内证据 | 结论 |
| --- | --- | --- |
| `opencode serve --hostname 127.0.0.1 --port <port>` 可用 | `Localization.swift:584` 的 Phase A 命令串、`OpenCodeEndpointResolver.swift:106` 注释 | ✅ 已证 |
| `OPENCODE_SERVER_PASSWORD` 走 env 做 Basic Auth | `config.example.env:9`、`agent/opencode/opencode.go` 全链路读取 | ✅ 已证 |
| `OPENCODE_SERVER_USERNAME` 被服务端接受 | `agent/opencode/opencode.go:84` 读取、`AppDependencies.swift:34/273` | ✅ 已证 |
| `/global/health` 无 auth 返回 401、有 auth 返回 200 + `healthy/version` | `OpenCodeEndpointResolver.swift:231-330` + `OpenCodeHealthValidatorTests` 10 例 | ✅ 已证 |
| **`--print-logs` 选项存在** | **全仓库 grep 零命中**（仅本方案文档出现） | ❌ **未证** |
| **opencode 默认从 4096 起搜索可用端口** | **零证据**；仓库内 4096 全部是测试 fixture（`MacBridgeBehaviorTests.swift:147/154/184/250/...` 作者随手选的样例 URL）或无关的 T08 pairing bucket cap（`CHANGELOG.md:161`） | ❌ **未证** |

**为什么这是 blocker**

- 若 `--print-logs` 不存在，§6 的启动命令会失败或被忽略，"logs/opencode-managed-server.log"无内容，ready 判定回退到纯 health polling（§6 的 "stdout `server listening on ...` 加速发现"也一并落空）。
- 端口策略本身（4096→4196 探测）作为 CordCode 的产品选择是合理的，**但 §0 把它包装成"opencode 的事实"是错的**。如果 opencode 实际并不从 4096 起搜索，那么"端口被占用时若占用者是健康的 managed server 可复用"这条规则（§6.3）的判定依据就需要重新论证。

**建议**

进入开发前的第 0 步，增加一个"CLI 实测取证"小节，把以下命令的真实输出固化进文档（owner 在本机执行一次即可）：

```bash
opencode --version
opencode serve --help              # 确认 --print-logs / --hostname / --port / 端口默认行为
OPENCODE_SERVER_PASSWORD=t opencode serve --hostname 127.0.0.1 --port 4097 --print-logs &
curl -i http://127.0.0.1:4097/global/health     # 期望 401
curl -i -u opencode:t http://127.0.0.1:4097/global/health  # 期望 200 + healthy/version
```

把实测结果作为 §0 的事实依据；探测端口范围改成"这是 CordCode 的产品决定（理由：避开常见占用、留 100 个槽位）"，不要再嫁接到 opencode 自身行为上。

---

### B3. 标题"无缝"与正文范围在最常见场景下不自洽

**冲突点**

- §0/§1/§14 的目标：用户打开 CordCode Link，iOS 扫码后**直接**看到 Mac 端 OpenCode Desktop 的项目和 session。
- §7（第 357-358 行）正文：

  > 若要真正满足"用户无感"，可在首次 managed_local 配置时...建议本轮先不自动重启，改为 CordCode Link 内显著提示。

**问题**

目标用户（已经在用 OpenCode Desktop 的人）打开 CordCode Link 时，**Desktop 大概率已经在运行**并绑定在它自带的 `vlocal` sidecar 上。`configureOpenCodeDesktopSettings` 写入的 `opencode.global.dat` 只在 Desktop 下次启动时生效（Desktop 不会热重载 `currentSidecarUrl`）。于是：

- iOS 扫码后连到 managed server，能看到 `projects[managedURL]`（Swift 侧已迁移好），但 **Desktop 自己还指向 vlocal**。
- 用户在 Desktop 里新开的 session 落到 vlocal scope，**不在 managedURL scope** → iOS 看不到 Desktop 的实时操作 → 项目/session"与 Desktop 保持一致"（§1.8）在 Desktop 不重启前不成立。
- 反过来也一样：iOS 在 managedURL 下新开的 session，Desktop 看不到。

也就是说，**"无缝"这个标题目标在"Desktop 已运行"这个最常见前置条件下，需要用户手动重启 Desktop 才能达成**——这与 §1"用户不需要...手动编辑 OpenCode Desktop 配置文件"和 §14"如果仍需要用户手动...则本任务未完成"的判据直接冲突。

**建议**

二选一，并在 §1/§14 显式写明：

- **收紧目标（推荐，诚实）**：把"无缝"限定为"Desktop 未运行或 Desktop 已指向 managed server 时无缝；Desktop 正在运行且指向 vlocal 时，CordCode Link 给出一次性、明确的'重启 Desktop 以完成切换'提示"。同时把 §14 的判据改成包含这一条件分支。
- **维持原目标**：则 §7 必须把"自动 quit + 重启 OpenCode Desktop"纳入本轮范围，并明确接受"会打断用户正在进行的 OpenCode turn"这个产品代价（CLAUDE.md 明确"不要默认强杀用户正在运行的任务，除非产品明确接受"）。当前正文是"建议本轮先不自动重启"，与维持目标矛盾。

无论选哪条，**§13 Reviewer 检查点应新增一条**："验收脚本是否覆盖了'Desktop 已运行'这条最常见的前置分支"。

---

## 3. §4 复用声明逐项核对（全部属实，给作者正面反馈）

| 方案 §4 声明 | 源码证据 | 核对 |
| --- | --- | --- |
| `OpenCodeEndpointResolver` 已有四 source + loopback 规范化 + password required | `OpenCodeEndpointResolver.swift:10-26`（四 case）、`:108-136`（normalizeLoopbackURL，localhost/127.0.0.1/::1→127.0.0.1，拒非 loopback/非 http/缺端口）、`:174-176`（password required） | ✅ |
| `OpenCodeHealthValidator` 已有 no-auth 401 + authed 200 双校验 | `OpenCodeEndpointResolver.swift:251-299`（先 401 再 authed 200，无密码 200 在非 legacy 路径判 `serverUnauthenticated`）、`:317-330`（校验 `healthy`+`version`） | ✅ |
| `configureOpenCodeDesktopSettings` 已写 `opencode.global.dat` + `opencode.settings`，已合并 `local` 项目集合 | `RuntimeManager.swift:730-809`；`migrateOpenCodeDesktopProjects` 的 `preferredSources: ["local", previousURL, "http://127.0.0.1:64667"]`（`:767`）**确实把 `local` 放首位**，按 worktree 去重（`:788/793`），`lastProject` 已存在不覆盖（`:804`） | ✅ |
| RuntimeManager 已传 `-opencode-url`、password 走 `OPENCODE_SERVER_PASSWORD` env | `RuntimeManager.swift:909-938`（argv，URL 空则不写）、`:940-975`（env，password 空 removeValue）；均为 `internal nonisolated static` 可测 | ✅ |
| `agent/opencode.New` 已取消隐式 fallback 64667 | `agent/opencode/opencode.go:74-82`（URL 空进 degraded，注释明确不再 fallback） | ✅ |
| `go-bridge` `-opencode-url` 默认空、URL 空时 descriptor `not_configured` | `go-bridge/main.go:36`、`agent_descriptor_test.go` 多例守护 | ✅ |
| `list_projects` 按 Desktop visible dirs 过滤、`list_sessions` 走 project-first cursor | `go-bridge/opencode-proxy.go:297-388`（`openCodeDesktopVisibleProjects` / `readOpenCodeDesktopVisibleProjectDirs`）、`go-bridge/handlers.go:932-979`（`paginateSessionList`） | ✅ |
| 控制面凭据 deny-list 不进 agent 子进程 | `core/message.go:31-51`（`OPENCODE_SERVER_USERNAME/PASSWORD` 在 deny exact + prefix）、`BuildAgentEnv` 三次清理、`message_env_test.go` 守护 | ✅ |

**这一节是整份方案最扎实的部分**——没有出现"声称已修其实没改"。作者确实反查了源码。

> 小一致性提醒：§4 说 Desktop 配置合并"已修复"，但 §12 step 4 又写"复用/加固 Desktop config sync，确保 local 项目集合合并到 managedURL"。两处对同一事实的措辞不一致，建议 §12 改为"复用现有 `migrateOpenCodeDesktopProjects`（已 `local`-first），仅补 managedURL scope 的回归测试"。

---

## 4. Should-fix（建议在动工前澄清）

### S1. Swift 写入侧与 Go 读取侧的 scope key 没有显式契约

**风险**

- Swift 侧 `configureOpenCodeDesktopSettings` 用 `projects[serverURL]` 写入（`RuntimeManager.swift:787/799`），`serverURL` 形如 `http://127.0.0.1:4096`。
- Go 侧 `readOpenCodeDesktopVisibleProjectDirs` 用 `strings.TrimRight(baseURL, "/")` 作为 key 去读（`opencode-proxy.go:357-386`），优先级 `baseURL > CurrentSidecarURL > "local"`。
- 两边只要在 **trailing slash / host 形式 / 端口** 任一处不一致（例如 Swift 一时写成 `http://127.0.0.1:4096/`，或 normalizeLoopbackURL 与 Go TrimRight 的归一化细节不同），Go 侧就读不到 `projects[managedURL]`，**静默回退到 `local`**——表面上项目还在，但 session scope 已分裂，且不会报错。

这是 §1.8"项目/session 列表与 Desktop 保持一致"最容易踩的隐形坑。

**建议**

在方案里明确写出"managedURL 规范形式契约"一条：

- managedURL 的 canonical 形式 = `http://127.0.0.1:<port>`（无 trailing slash、IPv4、显式端口）。
- Swift `normalizeLoopbackURL` 输出、Go `TrimRight(baseURL,"/")` 输入、`opencode-managed-server.json.url` 持久化值、Desktop `currentSidecarUrl`/`defaultServerUrl` 写入值，**四者必须字节一致**。
- 补一条 Go 测试：构造 `projects["http://127.0.0.1:4096"]`（无斜杠）和 `projects["http://127.0.0.1:4096/"]`（带斜杠）两种 fixture，断言 `baseURL="http://127.0.0.1:4096"` 时前者命中、后者回退 local——把这条契约钉死。

### S2. 单元测试可测性 seam 未指定

§10 的 Swift 测试计划列了"找不到 CLI → unavailable""端口被占用 → 选下一个端口""stdout 不是唯一 ready 条件"等。这些都是**端到端行为**，而现有可单测的组件（`OpenCodeHealthValidator`、`processArguments/processEnvironment`）之所以能测，是因为它们接收注入的 URLSession / 纯输入。

`OpenCodeManagedServer` 若直接 `Process()` + 真实 `opencode` 二进制 + 真实端口，CI 上要么 skip（没装 opencode）要么 flaky。

**建议**

在 §6 显式规定 `OpenCodeManagedServer` 的可测 seam：

- 注入 `CLIResolver`（找 `opencode` 路径）→ 单测可返回 nil 模拟"未安装"。
- 注入 `PortProber`（探测/选择端口）→ 单测可模拟"被占/被占满"。
- 注入 `HealthProbe`（封装 `/global/health` 调用）→ 复用 `OpenCodeHealthValidator` 的注入模式。
- 注入 `ProcessFactory`（构造 `Process`）→ 单测可不真起进程，验证 argv/env 不含 password。
- ready 判定写成纯函数 `evaluateReady(processAlive, noAuthStatus, authedStatus, stdoutHint) -> State`，对各类组合做表驱动单测，**完全不依赖真实 opencode**。

§10 的测试方法名保留，但应注明每条对应哪个 seam——否则实现者很可能写出依赖真实二进制的脆弱测试。

### S3. bootstrap 期间的 UX 与"Claude/Codex 不应被拖累"未写明

§7 推荐"延迟 `startBridge()`，等 managed server ready 再启 runtime"。但：

- managed server 启动需要 CLI 查找 + 端口探测 + health retry，可能数秒。这段时间 UI 显示什么？（现有 `RuntimeManager.status` 没有对应状态。）
- 若 managed server 启动失败（CLI not found / 端口占满），**bridge 是否仍要启动**？显然要——否则一个 OpenCode CLI 缺失会连累 Claude/Codex 全不可用。但方案没写这条不变量。

**建议**

§7 增加一段"启动不变量"：

- managed server ready 不是 bridge 启动的前置硬门；它是 OpenCode backend available 的前置。
- managed server 进入 `unavailable/crashed` 时，bridge 照常启动，OpenCode descriptor 报对应原因，Claude/Codex 不受影响。
- UI 在 bootstrap 期间显示"正在准备 OpenCode..."的临时状态，超时（如 10s）后转 diagnostic 而非无限 spinner。

### S4. orphan 进程与 PID re-attach 策略缺失

§3 写"CordCode Link 退出时可以停止 child-process 版 managed server"——这是**正常退出**路径。但 MacBridge **崩溃/被 kill** 时，子 `opencode serve` 成为孤儿继续监听端口。§6 的端口策略说"占用者是健康、认证匹配的 managed server 可复用"——但：

- "认证匹配"需要拿到旧 password 才能验证，即依赖 `opencode-managed-server.json` 存活。若该文件也丢（例如数据目录被清），无法证明占用者是"自己的"，只能跳到下一端口 → 旧孤儿永久泄漏至下次 reboot。
- 方案的 `State` 模型有 `pid` 字段（§6 状态模型），但正文没说启动时是否尝试 `kill(pid)` 收养/清理旧孤儿，还是一律走"端口复用"。两种策略各有风险（前者可能误杀同 PID 复用的无关进程；后者会累积孤儿）。

**建议**

§6 增加"启动时与既有占用者的处置"决策：

- 推荐策略：启动时若持久化文件存在且 `pid` 存活且该 pid 的命令行匹配 `opencode serve ... <persisted-port>`，则**收养**（re-attach 监督，不重启）；否则若健康+认证匹配则复用 endpoint 但启动新进程抢同端口会冲突——所以更稳妥是"持久化 pid 校验失败则 kill 旧占用者（仅当命令行确认是 opencode serve）后再起自己的"。
- 无论选哪条，写明并配测试。这块是经典 supervisor 难点，方案现在一笔带过。

### S5. managed server 持久化文件归属未定

§6 提议新文件 `opencode-managed-server.json`，紧接着又说"如继续复用 `credentials.json`，也必须 0600"——这是一个**未决的设计点**，会影响：

- 迁移测试（新装 vs 升级的初始状态）。
- 与现有 `SettingsViewModel` save/load（`opencode_user/opencode_pass/opencode_source/opencode_url` 一组字段）的字段拆分。
- password regenerate / restart 按钮读写哪个文件。

**建议**

§6 二选一并固定：推荐**新建独立文件**（managed server 的 url/port/pid 与用户凭据语义不同——前者是 CordCode 的运行态，后者是用户配置），并在 §10 补该文件 `0600` 权限与读写原子性的测试。

---

## 5. Nice-to-have（可在实现阶段处理）

- **N1（actor 一致性）**：`OpenCodeManagedServer` 的 actor 隔离未提。现有 `RuntimeManager` 是 `@MainActor` 并依赖 T05 的 `launchGeneration`/`applyConfigAndRestart` 做重启收敛（见 CHANGELOG 2026-06-19 T05）。managed server 的 start/stop/restart 必须接入同一套 generation 机制，否则"config 变更触发的 managed server 重启"与"bridge restart"会竞态，出现 bridge 指向还没 ready 的 managed server。建议 §7 加一句"managed server 重启与 bridge restart 共用 `launchGeneration` 收敛，不得各自独立 restart"。
- **N2（日志脱敏的边界）**：§6 写"不要把 password 打印到日志"。argv 不带 password（env 传递）已保证 CordCode 侧不泄；但 `--print-logs` 抓的是 **opencode 自己的 stdout/stderr**，opencode 若在调试日志里打印请求头/env，password 仍可能落地 `logs/opencode-managed-server.log`。这条 CordCode 控制不了，建议改为"opencode 自身日志中若发现 password 字样，按既有的 stderr 脱敏过滤（`core/message.go` 的 redactor）处理后再落盘"，不要承诺绝对不出现。
- **N3（`service_discovery_future` 迁移分支）**：§5 迁移表"尊重显式 source"。若存量用户 source 恰为 `service_discovery_future`（始终 unavailable），managed_local 不会自动启用，他们继续不可用。该 source 从未是默认值，影响面小，但 §5 可加一句"`service_discovery_future` 不视为可用 source，迁移时与 `disabled` 等价对待或提示用户改选"。
- **N4（CLI 查找 PATH）**：§6 的 CLI 查找优先级第 2 条用 `ProcessInfo.processInfo.environment["PATH"]`。注意 macOS GUI app 的 PATH 与 shell 不同（不含 `/opt/homebrew/bin` 除非 `~/.zshrc` 注入），这正是要硬编码 homebrew 路径的原因——方案已经覆盖，建议补一句"GUI app PATH 不含 homebrew，故必须显式探测 `/opt/homebrew/bin`"，避免后续维护者删掉硬编码路径。
- **N5（验收脚本的 `--print-logs` 依赖）**：§10 安装后核对命令 `pgrep -fl "...opencode serve"`——若最终实测 `--print-logs` 不存在，整套启动命令要改，验收脚本跟着改。建议验收脚本与 §6 启动命令标注"以 B2 实测结果为准"。

---

## 6. 逐节核对速查表

| 方案节 | 核对结论 | 关键证据 / 备注 |
| --- | --- | --- |
| §0 背景与结论 | **部分声明未证**（见 B2） | 4096 默认端口搜索、`--print-logs` 无证据 |
| §1 目标用户路径 | **范围与 §7/§14 不自洽**（见 B3） | Desktop 已运行时第 5/8 步不成立 |
| §2 非目标 | ✅ 边界清醒 | 不抓 IPC、不连无密码 server、不 mock 都正确 |
| §3 关键约束 | ✅ 安全/运行态约束正确；orphan 处置缺（S4） | local-first 回归来自真实回归，已尊重 |
| §4 现有可复用 | ✅ **逐项属实**（见 §3 表） | 本节是方案最扎实部分 |
| §5 managed_local source | ✅ 设计合理；迁移分支待补（N3） | 需同步改 `migratedSource`（`OpenCodeEndpointResolver.swift:202-205`）默认值 |
| §6 OpenCodeManagedServer | ⚠️ 命令假设未证（B2）、测试 seam 缺（S2）、orphan 策略缺（S4）、文件归属未决（S5） | 状态模型合理 |
| §7 AppDependencies 集成 | ⚠️ bootstrap UX/不变量缺（S3）、actor 收敛未提（N1）、目标冲突（B3） | 现有 `AppDependencies.init()` 可改为延迟 startBridge |
| §8 Settings UI | ✅ 方向正确 | 新增 source 后 Localization 文案要同步 |
| §9 go-bridge 改动 | ✅ 大部分无需改；补测试合理 | scope key 契约要钉死（S1） |
| §10 测试计划 | ⚠️ 测试 seam 未指定（S2） | 现有测试方法名清单见 §3 表 |
| §11 失败模式表 | ✅ 基本完整 | 可补"Desktop 已运行且指向 vlocal"一行 |
| §12 实现顺序 | ✅ 顺序合理；step 4 措辞与 §4 冲突 | step 0 应插入"CLI 实测取证"（B2） |
| §13 Reviewer 检查点 | ⚠️ 应补"Desktop 已运行分支验收"（B3）、"scope key 一致"（S1） | 本评审即对该节 |
| §14 验收判定 | ⚠️ 判据与 §7 范围冲突（B3） | 需按 B3 建议分条件分支 |

---

## 7. 给方案作者的可执行修订清单

按优先级排序，建议作者在进入开发前完成 D1–D3，开发中完成 D4–D7。

- **D1（阻断 / 流程）**：解决 B1——owner 决策 specs 归属（纳入跟踪的 `specs/` 或 `docs/protocol/` 同级，或从 CHANGELOG 移除具体文件名引用）。
- **D2（阻断 / 正确性）**：解决 B2——在 §0 之前插一节"opencode serve CLI 实测取证"，固化 `--print-logs`、端口默认行为、`OPENCODE_SERVER_USERNAME` 是否被服务端接受的真机输出；据此修正 §6 启动命令与端口策略的事实表述。
- **D3（阻断 / 范围）**：解决 B3——明确"Desktop 已运行"分支的验收路径（收紧目标 or 接受自动重启代价），同步修订 §1/§7/§13/§14。
- **D4**：钉死 managedURL canonical 形式契约并补 Go 回退测试（S1）。
- **D5**：在 §6 写明 `OpenCodeManagedServer` 的可测 seam（CLIResolver/PortProber/HealthProbe/ProcessFactory/纯函数 evaluateReady）（S2）。
- **D6**：在 §7 写明"managed server 失败不阻塞 bridge 启动 Claude/Codex"不变量 + bootstrap 期间 UI 状态（S3）。
- **D7**：在 §6 写明 orphan pid 收养/清理策略（S4），并固定持久化文件归属（S5）。
- **D8（一致性）**：§12 step 4 措辞与 §4 对齐；§13 补充两条 reviewer 检查点（见上）。

---

## 8. 评审结论

这份方案**值得做，且复用基础打得对**，但当前文本有两类问题需要在动工前消化：

1. **把未证假设当事实**（`--print-logs`、4096 默认端口）——这是 CLAUDE.md 明令"不得从旧文档整段复制配置而不反查源码"想防范的那类风险，作者对仓库内代码做得很好（§4 全对），但对 `opencode serve` 这个外部二进制没做到同等反查。
2. **标题目标与正文范围不自洽**（无缝 vs 不自动重启 Desktop）——需要 owner 在"收紧目标"和"接受强杀代价"之间明确选一条。

关掉 B1–B3、补上 S1–S5 后，这版可以成为可直接进入开发、且开发完能被独立审计的规格。当前形态**建议状态从"待评审的实现规格"改为"修订中（待 owner 决策 B1/B3 + 实测 B2）"**。
