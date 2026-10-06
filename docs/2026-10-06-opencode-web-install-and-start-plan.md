# OpenCode Web：未安装可代装、未启动可点启动

Date: 2026-10-06
Status: 方案 v1，未实施。评审通过前不改业务代码。
Branch: `feat/ios-native-message-timeline`
HEAD: `2e39e805f51ab21a201f2f732ad9bda57c99690c`
参照实现: `docs/2026-09-22-dsh-web-install-and-start-plan.md`（dsh-web 同款交互，已实施；
本文是它的 OpenCode 对应方案。交互四拍、状态表、安装器约束逐节对齐，架构差异见 §6。）

需求（owner 2026-10-06 原话归纳）：MacBridge 启动但 OpenCode 4096 服务没启动时，
没有任何地方可以启动这个服务；希望在 OpenCode 行加「启动」按钮，点一下就能启动；
环境里没有 opencode 时给「安装」按钮。交互参照 DeepSeek Harness 行已实现的
安装/启动按钮。

## 0. 来源

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=2e39e805f51ab21a201f2f732ad9bda57c99690c
未提交状态=（与本方案无重叠，均属另一进行中任务 dsh session-media）
  M  CHANGELOG.md / agent/dsh-web/fakedsh_test.go / docs/protocol/*（3）
  M  go-bridge/{backend_capabilities,handlers,rpc_scopes,rpc_scopes_test,types}.go
  ?? agent/dsh-web/session_media{,_test}.go / core/session_media.go
  ?? go-bridge/handlers_session_media{,_test}.go / session_media_protocol_test.go
  ?? docs/protocol/samples/session-media/
任务预期分支=feat/ios-native-message-timeline
配套 iOS 路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套 iOS 分支=feat/ios-native-message-timeline
配套 iOS 提交=4666b5a5bdb23552472523db9171ed22c9369390
配套 iOS 未提交=（与本方案无重叠，属 session-markdown-image 任务；本方案不改 iOS）
上游=/Users/jacklee/Projects/opencode
上游提交=2fa3363c92（dev 分支，github-v1.2.25-2107-g2fa3363c92）
上游说明=dev 分支已是 v2 generation 形态；npm 发布线仍是 1.18.x。
  本 checkout 无 v1.18.x tag；`opencode serve` 的 1.18 语义以本仓生产代码
  （OpenCodeManagedServer，与本机 1.18.34 活体协同运行）为准，见 §1。
本机环境=opencode 1.18.34（npm 全局，/opt/homebrew/bin/opencode →
  ../lib/node_modules/opencode-ai/bin/opencode.exe）；npm registry
  opencode-ai@latest=1.18.34（2026-10-06 实测）。
```

上游安装入口（`/Users/jacklee/Projects/opencode/README.md`，2fa3363c92）：

- `README.md:53`：`npm i -g opencode-ai@latest`（或 bun/pnpm/yarn）
- `README.md:50`：`curl -fsSL https://opencode.ai/install | bash`
- `README.md:56-57`：brew 两个 formula

本方案只走 npm（理由见 §4）。dsh 参照实现的官方入口同样是 npm
（`npx @deepseek-ai/dsh web`，README.zh.md:26），两条安装器约束同构。

## 1. 现在为什么是「未配置」

OpenCode 行今天只显示 `hello`/`agents` 里的 status 文案。链路：

1. Mac App 启动 bridge 进程时 `RuntimeManager.launchBridgeProcess()`
   （`MacBridge/MacBridge/Services/RuntimeManager.swift:457`）调用
   `resolveManagedOpenCodeIfNeeded()`（`:1015`）。仅当
   `config.opencodeSource == .managedLocal` 时创建 `OpenCodeManagedServer` 并
   `ensureRunning(timeout: 5.0)`（`OpenCodeManagedServer.swift:185`）：
   在 `cliSearchPath` 里找 `opencode` CLI → 选端口（持久端口优先，否则
   4096…4196，`OpenCodeManagedServer.swift:304`）→ 收养健康旧进程或
   spawn `opencode serve --hostname 127.0.0.1 --port <port> --print-logs`
   （`:222`）→ 等 `/global/health` no-auth 401 + authed 200。
2. 任何一步失败（CLI 缺失、spawn 失败、健康超时、60 秒内 5 次失败熔断、
   端口段全占）→ `ensureRunning` 返回 nil → `resolveManagedOpenCodeIfNeeded`
   把 `config.opencodeURL` 置空（`RuntimeManager.swift:1028-1030`）。
3. argv 只在 URL 非空时带 `-opencode-web-url`（`RuntimeManager.swift:1490-1496`），
   凭据走 env `OPENCODE_WEB_SERVER_USERNAME/PASSWORD`（`:1530-1543`）。
4. go-bridge 侧 `agent/opencode-web/opencodeweb.go:340 InstanceStatus()`：
   URL 空 → not_configured；URL 在但探针失败 → `available=false` + 错误原文。
   描述符走布尔折叠 `detectInstanceStatusProber`
   （`go-bridge/agent_descriptor.go:243-246`、`:273-283`）：`available=false`
   一律收成 `not_configured`。
5. Mac 行 `not_configured` → 「未配置」（`BridgeStatusView.swift:338`），
   不可用行只有「重新检查」（`WorkspaceView.swift:658-666`）。

所以「没装 opencode」「4096 没拉起来」「拉起后进程死了」「探针认证失败」
在界面上是同一句「未配置」。枚举里已有 `not_detected` / `service_not_running`
（`agent_descriptor.go:22-25`），opencode-web 没用。这与 dsh-web 修复前的坑
同构（dsh 方案 §1）。

另一个缺口：`resolveManagedOpenCodeIfNeeded` 只在 bridge 进程启动时跑一次。
服务在会话中途死掉后，没有任何路径把它拉回来——直到下一次 bridge 重启
（含 120 分钟定时兜底重启）。这正是 owner 说的「没有地方去启动」。

iOS 侧不受本方案影响：`BackendModels.swift:25-37` 只判
`descriptor.status == "available"`，非 available 时禁用切换并透传 `reason`
文案，不映射具体 status 字符串（仅 codex-remote 的 `pairing_required` 特判）。
wire status 从 `not_configured` 变成 `service_not_running` 对 iOS 透明，
不新增按钮、不新增协议字段（对齐 dsh 方案 §3 的 iOS 边界）。

## 2. 人怎么走完

零件按四拍串。没有「先接通内部接口、按钮下期再做」。

### 2.1 未安装 → 安装并启动

1. **打开。** CordCode Link 工作站，「AI 工具」里的 OpenCode 行。没有第二层
   设置页，没有终端命令给用户抄。
2. **输入。** 行上是橙色「未安装」和「安装」。不要求填 URL、端口、npm 包名。
   Node.js 不在搜索路径里时，同一位置的按钮改成「需要 Node.js」，点了打开
   `https://nodejs.org`。Link 不下载、不安装 Node。
3. **发出去。** 点「安装」后，用已找到的 `npm` 绝对路径执行
   `npm install -g opencode-ai@<OD-2 选定的版本 spec>`（无 sudo、无 npx）。
   成功后验证 `opencode --version` exit 0，写安装记录，把 bin 绝对路径并入
   CLI 解析（安装记录优先），后半段走与「启动」按钮完全相同的路径：
   `ensureRunning` 拉起 4096 段的 managed server。不走 `npx` 当「已安装」。
4. **过程。** 按钮变「安装中」并禁用，npm 结束后变「启动中」，直到
   `/global/health` 通过。成功：行变绿「就绪」，按钮消失；若本次 URL 此前
   未传给 runtime（新装机器），按 §5 的规则同步 config 并 restart bridge
   （iOS 会有一次短暂重连，与任何配置变更一致）。npm 失败：仍是「未安装」，
   名字下一行显示 npm 的真实错误，按钮回到「安装」。npm 成功但 server
   起不来：变「未启动」加「启动」，字幕保留 spawn/健康检查原文。不回滚
   已装好的包，也不写成「未配置」。

全局 npm 目录不可写时不弹确认框、不提权，改装到
`~/Library/Application Support/CordCode Link/opencode-install/`，字幕写明
「已装到 CordCode 目录，终端里不一定有 opencode」。两条都失败才停在
「未安装」并显示真实错误。

### 2.2 已安装、4096 没起来 → 启动

1. **打开。** 同一行。典型场景：Link 启动时拉起失败（健康超时/熔断），或
   服务在会话中途死掉。
2. **输入。** 橙色「未启动」和「启动」。没有端口输入；端口仍是持久端口
   优先、4096…4196 兜底。
3. **发出去。** 点「启动」调用 RuntimeManager 的显式
   `startOpenCodeManagedServer()`：重置失败熔断窗口（用户显式动作不是循环），
   后台线程跑现有 `ensureRunning`（argv 与今天托管启动相同：
   `serve --hostname 127.0.0.1 --port <port> --print-logs`）。收养逻辑保留：
   端口上已是健康的 opencode 在听则直接收养，行变「就绪」，不起第二个进程。
4. **过程。** 按钮「启动中」并禁用。成功 → 行「就绪」；URL 已在 runtime
   手里时无需重启 bridge——`InstanceStatus` 对失败探针每次重探
   （`opencodeweb.go:345`：err 缓存不命中 TTL），行刷新即绿。失败 → 停在
   「未启动」，字幕是真实原因（CLI 缺失、spawn 退出、健康超时、端口段全占）。
   「重新检查」只重读状态，不启动（现状已只读：`POST /internal/agents/{id}/test`
   只重建描述符，探针 GET-only）。

### 2.3 已经在听

4096 段端口上已有健康 opencode 在应答：绿色「就绪」，没有安装/启动按钮。
谁拉起来的不重要（收养判定含 catalog 降级守卫，`OpenCodeManagedServer.swift:321-345`，
保留不动）。Link 退出仍按现状终止自己 spawn 的进程（`shutdownForExit`，
`RuntimeManager.swift:440-444`）。

### 2.4 失败时人还在哪

人留在同一行。字幕一行，hover 看全文。下一次点击是重试，不是另一条隐藏
路径。缺 API key 不是未安装也不是未启动：服务在听就是「就绪」，密钥仍由
用户在 OpenCode 侧配置；本方案不做密钥表单（对齐 dsh 方案 §2.4）。

## 3. 状态

go-bridge 侧 opencode-web 改为暴露结构化就绪
`StructuredInstanceReadiness()`（新文件 `agent/opencode-web/readiness.go`，
镜像 `agent/dsh-web/readiness.go` 的 seam 形态；`InstanceStatus()` 保留给
内部 caller）。描述符从 `detectInstanceStatusProber` 切到
`detectStructuredInstanceReadiness`（`agent_descriptor.go:243-246`）。
不新增 wire 枚举——`not_configured` / `service_not_running` / `available`
都已存在（`agent_descriptor.go:22-33`）。

| 条件 | wire status | 这一行显示 | 按钮 |
| --- | --- | --- | --- |
| source=managed_local，CLI 缺失（安装记录+搜索路径都没有 opencode） | 无持久 endpoint → `not_configured`；有持久 endpoint → `service_not_running`（探针连不上） | 未安装（Mac 行本地覆盖，按 CLI 探测，见下） | 安装；没有 node/npm 时改为「需要 Node.js」 |
| source=managed_local，CLI 在，服务没在听（含启动失败、中途死掉、无状态文件） | `service_not_running`；无持久 endpoint 时 `not_configured` | 未启动 | 启动 |
| 探针 401（服务在听，凭据不匹配） | `service_not_running` | 未启动 | 启动；字幕写明认证失败（重启通常不解决，dsh 同款处理） |
| 探针测出 v2 generation（隔离） | `service_not_running` | 未启动 | 启动；字幕是隔离原文（`unsupportedGenerationDetail`），重启不解决——已知局限，字幕承担解释 |
| 服务健康 + generation118 | `available` | 就绪 | 无 |
| source=external_http 未配置 URL | `not_configured` | 未配置 | 无（用户自管服务） |
| source=external_http，URL 在，服务没起 | `service_not_running` | 未启动 | 无（用户自管服务；字幕透传探针错误） |
| source=disabled | `not_configured` | 未配置 | 无 |

「未安装」的判别是 **Mac 行本地**的（Swift 侧 CLI 探测结果），不是 wire
status：CLI 是否安装这个事实的真相在 Swift（`DefaultOpenCodeCLIResolver`
+ 安装记录），go-bridge 不重复实现第二套 CLI 发现（避免双真相）。这是与
dsh 的一处刻意差异：dsh 的二进制发现在 go-bridge（`agent/dsh-web/binary.go`
`findDSHBinary`），因为座位 spawn 就在 go-bridge；OpenCode 的 spawn 在
Swift（§6）。iOS 只看 wire status：全新未装机器上 iOS 显示「后端暂不可用」
+ reason 透传，不显示「未安装」——接受，Mac 行是按钮与文案的权威面。

「安装中」「启动中」是 Mac 行本地态，不新增 wire 枚举（对齐 dsh 方案 §3）。
动作进行时 wire 仍可是 `not_configured` / `service_not_running`；行自己盖住
按钮文案，不闪回「未配置」。

行状态文本与按钮决策是 WorkspaceView 里的纯函数（镜像
`deepSeekRowStatusText` / `deepSeekSeatAction`，`WorkspaceView.swift:546-579`）：

```text
行文本（OpenCode 行）:
  wire available → 就绪
  source==managedLocal && cliFound==false → 未安装
  其余非 available（not_configured / service_not_running）→ 未启动

按钮矩阵:
  installing → 安装中（禁用）
  starting   → 启动中（禁用）
  source != managedLocal 或 wire available → 无按钮
  cliFound==false → npmFound ? 安装 : 需要Node.js
  其余 → 启动
```

## 4. 代装怎么做

镜像 dsh installer（`agent/dsh-web/installer.go`）的约束，落在 Swift
（新 `OpenCodeInstaller`，MacBridge Services；理由 §6）：

- **npm 发现**：`RuntimeConfig.defaultCLISearchPath`
  （`RuntimeManager.swift:253-276`，已含 bun/homebrew//usr/local/pnpm/
  volta/~/.npm-global）再加 nvm 最新版本 `bin`（GUI 不继承 shell PATH，
  dsh 方案 §4 同款坑）。找到绝对路径就记录，后续用绝对路径。
- **命令**：`npm install -g opencode-ai@<spec>`。spec 见 OD-2（推荐
  `1.18`：npm 会解析到最高 1.18.x）。不用 `sh -c`、不 sudo、不 npx。
- **prefix 回退**：全局 prefix 不可写 →
  `npm install --prefix <dataDir>/opencode-install opencode-ai@<spec>`，
  bin 落在 `<prefix>/node_modules/.bin/opencode`。记录 scope=
  `user-global` / `cordcode-prefix`，字幕注明。
- **验证**：装完必须 `opencode --version` exit 0 才算安装成功；失败 =
  安装失败，不启动（dsh §4 同款）。
- **安装记录**：`<dataDir>/opencode-install-record.json`，0600、无凭据：
  bin 路径、scope、版本、时间（镜像 dsh `dsh-web-install-record.json`）。
- **CLI 解析并入**：`DefaultOpenCodeCLIResolver`（
  `OpenCodeManagedServer.swift:51-67`）改为安装记录优先（记录里的 bin 可执行
  即用），再走搜索路径——镜像 dsh `findDSHBinary` 的 record-first 顺序，
  保证 CordCode-prefix 安装在 GUI PATH 缺口下跨重启仍可发现。
- **已有可执行 opencode 时不显示「安装」，不再跑 npm。**
- **单飞**：第二次点击在第一次结束前无效。
- **超时 10 分钟**，超时杀 npm 进程组，行回「未安装」，字幕写超时，
  不写成成功。
- **日志**：npm stdout/stderr 打码（token 样式）后可进 Link 日志；界面
  字幕用最后一段非空错误。本命令不传任何凭据。
- **不做 brew / curl 脚本安装**：官方三条入口里只走 npm——与 dsh installer
  同构（单一注册表路径、prefix 回退可预期、needNode 分支复用）。brew 与
  curl 脚本留给用户自己。

Node 不在范围内。没有 `node`/`npm` 时代装不能开始（「需要 Node.js」按钮
→ nodejs.org）。Link 不捆绑 Node 运行时（dsh §4 同款边界）。

## 5. 启动怎么接现有 managed server

保留 `ensureRunning` 的全部现有语义：收养判定（含 catalog 降级守卫）、
端口段选择、`--print-logs`、stderr 重定向与轮转、Desktop 配置同步、
失败熔断、Link 退出终止自有进程。改的是**调用时机与失败时的 URL 处理**：

1. **`resolveManagedOpenCodeIfNeeded` 失败不再清空 URL**：`ensureRunning`
   失败时，若状态文件（`opencode-managed-server.json`）里有持久 endpoint
   （url/username/password），照常写入 `config.opencodeURL/User/Pass`——
   runtime 拿到 URL 后探针失败，报 `service_not_running`（行显示「未启动」，
   而不是误导性的「未配置」）。服务后来被「启动」拉起后，探针恢复，行变绿，
   **无需重启 bridge**。无状态文件（全新机器/从未成功保存）才留空 URL →
   `not_configured`。
2. **新增显式 `startOpenCodeManagedServer()`**（RuntimeManager，
   `@MainActor` 入口 + `Task.detached` 执行，避免 `ensureRunning` 的
   `Thread.sleep` 卡主线程 5 秒）：
   - 重置 `consecutiveFailures` 熔断窗口（显式用户动作，不是 spawn 循环）；
   - 跑 `ensureRunning`；进行中发布 `starting` 本地态；
   - 成功且 `endpoint.url != config.opencodeURL` → 更新 config 三元组 +
     `syncDesktopConfig` + `applyConfigAndRestart`（URL 首次进 runtime，
     iOS 短暂重连，与任何配置变更一致）；URL 未变 → 只刷新行状态；
   - 失败 → `lastStartError` = `OpenCodeManagedServer.state` 的 reason
     原文（`unavailable/crashed(reason:)`），行字幕透传。
3. **安装后半段**（§4）成功后调用同一个 `startOpenCodeManagedServer()`。
4. **冷启动自动拉起保留**（OD-1）：`launchBridgeProcess` 里的
   `resolveManagedOpenCodeIfNeeded` 行为不变。诊断与「重新检查」保持只读
   （现状已满足：opencode-web 的 `RunDiagnostics` 与描述符探测都是 GET-only）。
5. **动作状态面**：RuntimeManager 新增
   `@Published var openCodeSeatAction: OpenCodeSeatActionState`
   （cliFound / npmFound / installing / starting / lastInstallError /
   lastStartError / lastInstallNote），数据源 = CLI 解析 + npm 发现 +
   installer + managed server state。WorkspaceView 直接读 runtimeManager
   （行内已有先例：codex-web 行读 `runtimeManager.codexDaemonConfigChanged`，
   `WorkspaceView.swift:591`）。**不新增 go-bridge management 端点**（§6）。

## 6. 为什么动作面在 Swift，不在 go-bridge（与 dsh 的结构差异）

dsh 的座位 spawn 在 go-bridge（`agent/dsh-web/resolver.go` `StartSeat`），
所以安装/启动走 management API（`POST /internal/agents/dsh-web/install|start`
+ `GET action-state`，`go-bridge/management_api.go:775-839`），状态与动作
同仓同进程。

OpenCode 的 managed server 在 **Swift**：`OpenCodeManagedServer` 持有进程、
收养判定、catalog 降级守卫、stderr 轮转，且 Desktop 同步依赖 AppKit
（`NSRunningApplication`/`NSWorkspace`，`OpenCodeManagedServer.swift:102-129`）
——整体无法迁去 Go。go-bridge 的 opencode-web 是纯 HTTP/SSE 客户端，
设计上就不 spawn（`opencodeweb.go:335-339` 探针只读）。management API 的
方向是 Swift→go-bridge，没有反向控制通道。

因此本方案的分工是：**状态面**在 go-bridge（结构化 readiness，行与 iOS
都能看到诚实的 `service_not_running`）；**动作面**在 Swift（RuntimeManager
直接调 `OpenCodeManagedServer` 与新 installer），不新增 management 端点、
不新增 wire 协议字段。用户可见交互与 dsh 行完全一致（同款按钮、同款四拍），
差异只在内部接线。把 managed server 迁到 go-bridge 以「完全对称」属于
高回归风险重构、零用户收益，不做。

## 7. 不做

- 不代装 Node、不捆绑 Node、不打开终端让用户贴命令。
- 不做 brew / curl 脚本安装路径（npm 一条，§4）。
- 不给 iPhone 做安装或启动按钮；iOS 零改动（§1 已核透传路径）。
- 不新增 wire 枚举、management 端点或协议字段。
- 不改 external_http 用户的行为：无按钮，字幕透传探针错误
  （bring-your-own-server 语义不变，`OpenCodeEndpointResolver.swift:14-16`）。
- 不移除冷启动自动拉起（OD-1 推荐保留；dsh 移除是 owner 2026-09-22 的
  明确改令，OpenCode 没有对应指令）。
- 不把 managed server 迁到 go-bridge（§6）。
- 不做 API key / provider 表单。
- 不改 `deprecated/opencode` legacy 驱动与 `agent/dsh` 的永不安装锁。
- 不改 Desktop 同步、收养与 catalog 降级守卫的现有语义。
- 不提交、不安装 App。本文件不是发布。

## 8. 过审后怎么验

先改完再测，不每改一行就构建。

- **go-bridge（`agent/opencode-web` + `go-bridge`）**：
  - readiness 全状态：URL 空 → `not_configured`；探针 unreachable →
    `service_not_running` + 错误原文；401 → `service_not_running` + 认证
    失败文案；no-auth 200 → `service_not_running` + 未启用认证文案；
    v2 形状 → `service_not_running` + 隔离原文；118 形状 → `available`。
    用现有 fake server 手段（`opencodeweb_test.go` 的注入式构造）。
  - 描述符切换：`agent_descriptor_test.go:965` 现有
    `detectInstanceStatusProber("opencode-web")` 断言随切换更新为
    structured 路径；`InstanceStatus` 既有测试（`opencodeweb_test.go:19-79`）
    不动。
  - 禁止回归：探针失败不得再折叠成 `not_configured`（镜像 dsh 方案 §7
    的「不再收成 not_configured」断言）。
- **Swift（MacBridge）**：
  - 决策矩阵纯函数测试（镜像 `DeepSeekSeatActionTests`：全状态行 ×
    按钮 × 进行中态盖字 × external_http 无按钮 × L10n 键非空）。
  - `resolveManagedOpenCodeIfNeeded`：ensureRunning 失败 + 状态文件在 →
    URL 保留持久值；无状态文件 → 空。用现有 stub 注入
    （`OpenCodeManagedServerTests` 的 processFactory/portProber 模式）。
  - 启动动作：熔断窗口重置；URL 变化 → config 更新 + restart 调用一次；
    URL 未变 → 不 restart；失败 → lastStartError 原文。
  - installer：argv 断言 `npm install -g opencode-ai@<spec>` 或
    `--prefix` 回退；无 sudo、无 npx；已有 bin 不再 exec npm；
    `--version` 失败 = 安装失败；记录文件 0600 且无凭据；CLI 解析
    record-first。用注入式 Process stub，不访问注册表。
  - 既有 `OpenCodeManagedServerTests` / `OpenCodeEndpointResolverTests`
    全绿（收养/端口/健康判定未动）。
- **一次增量 MacBridge 编译**（Release 覆盖安装按 CLAUDE.md 常规交付，
  不在本方案默认验证里重复）。
- **真实 npm 安装与真实 4096 启动不在默认验证里**：方案通过且实现落地后，
  由 owner 在工作站行点一次安装或启动；agent 只核对日志里出现本次独有的
  安装/启动结果，不用旧「未配置」截图当证据（dsh 方案 §7 同款）。

## 9. 实施时先做的接线

1. go-bridge：`agent/opencode-web/readiness.go`（StructuredInstanceReadiness）
   + `agent_descriptor.go:243-246` 切换 + 定向测试（含 §8 的禁止回归）。
2. Swift：`resolveManagedOpenCodeIfNeeded` 持久 URL 回退 + 测试。
3. Swift：RuntimeManager `openCodeSeatAction` 状态 + 
   `startOpenCodeManagedServer()` + 测试。
4. Swift：`OpenCodeInstaller`（npm 发现/安装/验证/记录）+ CLI 解析
   record-first + 测试。
5. WorkspaceView OpenCode 行：文案覆盖、按钮、字幕、进行中态
   （镜像 `deepSeekSeatControls`/`deepSeekSeatHint`，样式对齐现有
   bordered/small 按钮）+ 矩阵测试。
6. L10n 中英文（`opencode_web_*` 键，镜像 `dsh_web_*` 命名）+
   `CHANGELOG.md` `[Unreleased]` 一条：OpenCode 行区分未安装/未启动，
   点安装代装并启动，点启动拉起 4096 段 managed server。
7. 按 §8 收口测试。不碰 §0 里与本方案无关的未提交文件。

## OD（需 owner/评审裁决）

| ID | 问题 | 选项 | 推荐 | 状态 | 影响切片 | 验收 |
| --- | --- | --- | --- | --- | --- | --- |
| OD-1 | 冷启动自动拉起保留还是移除（dsh 为让「启动」按钮有机会出现而移除了自动补拉） | A 保留自动拉起（按钮只覆盖失败/中途死亡场景）；B 移除（对齐 dsh，重启后一律等用户点启动） | A：owner 本次只要求补恢复路径，未要求移除；移除是行为回退 | pending | §9 条目 2/3/5（B 需改 `resolveManagedOpenCodeIfNeeded` 语义与 §2.2 场景） | A：重启 Link 后服务自动就绪，行绿；B：重启后行「未启动」+「启动」 |
| OD-2 | 安装的版本 spec | A `opencode-ai@1.18`（钉 generation 线，装最高 1.18.x）；B `@latest`（官方 README 原文） | A：backend 硬门只认 generation118（`clientFor` fail-closed），装已知会被隔离的版本等于制造失败；backend 支持 v2 后随代码升 spec | pending | §9 条目 4（installer argv 与测试断言） | A：装完探针过门、行就绪；B：v2 上 npm 后新装机器行「未启动」+ 隔离字幕（诚实但卡死） |

## 证据索引（E，方案阶段已核）

- E-1 现状链路（§1 全部行号锚点）：verified，来源=本仓 2e39e805。
- E-2 dsh 参照实现（方案+Swift+go-bridge+测试锚点）：verified，同上。
- E-3 官方安装命令 `npm i -g opencode-ai@latest`：verified，
  `/Users/jacklee/Projects/opencode/README.md:53` @ 2fa3363c92。
- E-4 npm latest=1.18.34、本机 1.18.34（npm 全局）：verified，2026-10-06
  实测 `npm view` / `opencode --version` / `/opt/homebrew/bin/opencode` 符号链接。
- E-5 generation 门按 API 形状判定（`/global/health` + `/session` bare
  array → generation118，非版本号比对）：verified，
  `agent/opencode-web/probe.go:144-209`。1.18.34 与 1.18.18 同形状——
  本机 1.18.34 服务在跑时 opencode-web 行为可用（owner 日常使用成立）；
  标注为运行推断，实施门复核。
- E-6 iOS status 透传（零改动声明）：verified，
  iOS 仓 `BackendModels.swift:25-37` @ 4666b5a5。
- E-7 真实 npm 安装 + 真实 4096 启动：pending，实施后 owner 验收（§8）。
