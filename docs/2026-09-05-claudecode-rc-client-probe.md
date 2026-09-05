# Claude Code Remote Control 客户端接入可行性 probe 报告

- 调研指令：`docs/2026-09-05-claudecode-rc-research-directive.md`
- 执行日：2026-09-05；分析师 agent；**只调研未实现**（未改产品代码、未动
  生产 relay、未动 owner 正式环境配置）
- 证据包：`scripts/claudecode-rc-probe/`（样本索引见 §7）
- 结论分级：本文全部结论为**组件级已验证**（本机静态/活体取证）或
  **文档层语义**（已标注）；无任何「生产路径已验证」。

## 0. 总结论

**no-go（资格前提不满足）——owner 于 2026-09-05 确认无 Pro/Max/Team
订阅**；RC 官方硬要求订阅（API key 不支持），因此路线 A（Desktop host +
bridge 远程客户端）与 F1（SDK bridge worker host）**全部搁置**，除非未来
订阅。工程可行性证据（官方 SDK @alpha 接入面）已勘明归档，未来订阅后可
直接续用，无需重新调研协议面。

（2026-09-05 调研时点的中间结论为「当前环境条件性 no-go」，订阅裁决后
升级为最终 no-go；以下正文保留中间判定过程作为依据链。）

按指令三个硬门：

| 硬门 | 判定 | 依据 |
| --- | --- | --- |
| B 协议可逆向度 | **通过（强于预期）** | 不是「可逆向」——是**官方 SDK 公开面**：`@anthropic-ai/claude-agent-sdk` 0.3.260 的 `/bridge`（worker/host 接入）与 `/browser`（客户端消费）两个导出，端点/鉴权/事件/控制指令/生命周期语义类型级完整；另有 CLI 二进制内嵌客户端实现（本机已静态提取）。见 §3 |
| C2 Desktop 实时直播真实实验 | **阻塞（未实验）** | 本机 RC 资格被 A 组资格门挡死，任何活体实验无法开展。文档层有正面语义（reattach、transcript 同步锚点），但按指令红线「文档推断不算」。阻塞链与解除条件见 §2/§4 |
| D 凭据可行性 | **部分通过（静态）** | full-scope token 硬要求确认；Desktop host-creds 注入机制 = 「宿主把凭据交给第二进程」的官方同构先例；SDK 明说 `createCodeSession` "works from any process"；多进程并发刷新/过期行为活体黑盒。见 §5 |

A 组资格门（指令设计为可能一票否决）**在本机成立**：`claude doctor` RC
面板六项全红，`claude remote-control` 直接拒绝。但其性质是**可解除的环境
阻塞**（改一处全局配置 + 登录），不是路线否决。按指令「A 组失败即停并
报告」，活体实验（B3 活体层、C2、C3 矩阵、D1 活体层、浏览器端取证）全部
未做，本报告即为中间报告 + 续期条件。

**给 owner 的一句话**：RC 接入在协议面已经「半公开」（官方 SDK 有 alpha
级接入面），真正的拦路虎是三个产品语义决策（网关失效、transcript 上云、
订阅资格），以及一个未做的关键实验（远程发消息时 Desktop 是否原生实时
显示——需要你先解除本机资格门才能实验）。

## 1. 来源清单

| 来源 | 锚 | 用途 |
| --- | --- | --- |
| 本仓 | `cordcode-macbridge-claudecode-official`，分支 `claudecode/official-capability`，提交 `958964e`，工作树干净 | 指令、think.md、codex-remote 先例 |
| PATH CLI | npm global 2.1.234（commit 7215ba60b06d），`/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`（Mach-O arm64） | doctor/verbose 活体 + strings 静态 |
| Desktop 内嵌 CLI | `~/Library/Application Support/Claude-3p/claude-code/2.1.260/claude.app`（2.1.258 目录并存，活体进程为 2.1.260） | 活体进程 env 取证；2.1.260 strings 复检 |
| Claude Desktop | 1.46388.3（deploymentMode=3p，进程参数取证） | Code tab 进程模型 |
| Agent SDK | 本机 `/Users/jacklee/Projects/claude-agent-sdk-npm/package/`（`@anthropic-ai/claude-agent-sdk` **0.3.260**，含 bridge.d.ts / browser-sdk.d.ts / sdk.d.ts）；`~/Projects/claude-agent-sdk-typescript`（a79d677，仅文档无源码） | SDK 类型面（B2/E1 核心证据） |
| 官方文档 | code.claude.com/docs：`en/remote-control`、`en/mobile`、`en/desktop`、`llms.txt` 全索引（2026-09-05 抓取） | 资格/生命周期/矩阵文档层 |
| codex-remote 先例 | `agent/codex-remote/`（README 的 Gate P0 台账与安全契约） | probe 方法与红线参照 |

版本锚更新（对 CLAUDE.md「版本锚三段式」的修正建议）：PATH CLI 2.1.234 ×
Desktop 内嵌 CLI **2.1.260**（CLAUDE.md 写 2.1.258，已过时）× SDK 0.3.260。

## 2. A 组资格门（完成，全部实锤）

### A1 本机 RC 不可用——两层原因，均在 user settings env 块

`claude doctor`（测试目录 `/tmp/rc-probe/work`）RC 面板六红（样本
`samples/a1-doctor-default.txt`）：

1. Not connected to the Anthropic API（`ANTHROPIC_BASE_URL` 指向
   `open.bigmodel.cn` 网关——来自 **`~/.claude/settings.json` 的 env 块**）
2. Not signed in to claude.ai
3. claude.ai subscription auth not active
4. Sign-in is missing the user:profile scope
5. Feature-flag evaluation disabled（`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`，同在 user env 块）
6. Remote Control rollout could not be verified

三层作用域排查（样本 `samples/a1-settings-scope-map.md`）：agent shell 无
相关变量；`~/.zshrc` 等五个启动文件 grep 零命中；本仓无项目 settings；
**唯一生效点是 user settings env 块**。

两次覆盖实验（样本 `a1-doctor-project-override.txt`、干净 HOME 基线
`a1-doctor-cleanhome.txt`）：

- 命令行 env 显式设 `ANTHROPIC_BASE_URL=https://api.anthropic.com` +
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=0` → 面板**不变**；
- 测试目录 project settings `.claude/settings.json` env 块同值覆盖 → 面板
  **不变**（user env 块优先于 project env 块与 shell env）；
- `HOME=/tmp/rc-probe/home` 干净基线 → base URL / feature-flag 两条阻塞
  **消失**，仅剩「未登录 claude.ai」族。

→ **解除 RC 资格的唯一路径是修改 `~/.claude/settings.json` 本身（owner
决策点）+ claude.ai 登录**，shell 层或项目层 workaround 不存在。

`claude remote-control --verbose --debug-file`（样本 `a1-rc-verbose.txt`）
在登录检查处直接拒绝，同时输出 RC auth state 面板（`hasOAuthAccessToken=false`、
`oauthScopes=none`、`ANTHROPIC_API_KEY=set`、`ANTHROPIC_AUTH_TOKEN=set`、
`telemetryDisabledBy=CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`、
`tengu_ccr_bridge=true`、`growthBookLastFetched=23d ago` 等）。

### A2 网关语义变化（只记录事实，不替 owner 决策）

若解除资格门：RC 会话的模型请求走 `api.anthropic.com`，**网关的
opus/sonnet→glm-5.3 改写对 RC 会话失效**，执行消耗 claude.ai 订阅额度、
模型为官方模型。附带发现（样本 `a1-desktop-host-auth-env.md`）：

- **Desktop Code tab 的会话当前并不吃全局 base URL**：活体子进程
  `ANTHROPIC_BASE_URL=http://127.0.0.1:15721/claude-desktop`，15721 的
  listener 是第三方工具 **cc-switch**（进程取证）——Desktop 侧网关改写
  由 cc-switch 代理承担，host 注入优先于 user settings env 块；
- 因此「Desktop 托管会话开 RC」的前置不止开设置一项，还包括把
  Desktop/cc-switch 的 provider 切回官方直连（owner 正在用的链路）；
- Desktop 宿主还给子进程注入 `DISABLE_GROWTHBOOK=1`（文档列为 RC 拒绝
  条件之一）；该注入是否被 RC 资格检查豁免——未知（活体阻塞）。

### A3 订阅形态

当前认证形态 = 网关 token（settings env 的 `ANTHROPIC_AUTH_TOKEN`/
`ANTHROPIC_API_KEY`）。keychain 无 `Claude Code-credentials` 条目、
`~/.claude/.credentials.json` 不存在 → **本机从未以 claude.ai OAuth 登录**。
**owner 2026-09-05 确认：不持有 Pro/Max/Team 订阅**——RC 资格前提
（订阅硬要求，API key 不支持）不满足，此项为最终阻塞，非配置可解。

## 3. B 组：RC 客户端协议面（静态层完整，活体阻塞）

### B1 协议真实形状（静态证据：CLI 内嵌实现 + SDK 类型 + 内嵌 API 表）

**远程客户端协议 = `api.anthropic.com` 上的 `/v1/code/sessions` REST+SSE
族**（CCR 专用面；与 Agent Platform 的 `/v1/sessions` 共享事件模型但路径
不同）：

```text
GET    /v1/code/sessions                          列会话
GET    /v1/code/sessions/{id}                     会话详情
POST   /v1/code/sessions/{id}                     更新标题等
POST   /v1/code/sessions/{id}/events              发事件（AddClientEventFromClient 请求体）
GET    /v1/code/sessions/{id}/events              轮询（分页）
GET    /v1/code/sessions/{id}/events/stream       SSE 长流，?from_sequence_num= 游标续传
POST   /v1/code/sessions/{id}/mark_read           标记已读
POST   /v1/code/sessions/{id}/client/presence     在线状态（client_id）
POST   /v1/code/sessions/{id}/archive             归档
POST   /v1/code/sessions                          createCodeSession（创建 CCR 会话，返回 cse_* id）
POST   /v1/code/sessions/{id}/bridge              mint worker JWT（= worker 注册，bump epoch）
```

以上每一行都有两路以上独立证据：CLI 2.1.234 二进制内嵌客户端实现代码
（minified 上下文，样本 `b3-cli-static-endpoints.md`）+ SDK 0.3.260
`.d.ts` 类型面（`browser-sdk.d.ts` 的 `SSEOptions{streamUrl,sendUrl,
sessionId,headers}`、`bridge.d.ts` 的 `createCodeSession`/
`fetchRemoteCredentials` 注释）+ 二进制内嵌 Markdown API 表（样本
`b2-embedded-api-table.md`）。

鉴权：`Authorization: Bearer <OAuth accessToken>` +
`anthropic-client-platform`（标注 host surface：web/iOS/Android/desktop）；
Trusted Devices 强制时另需 `X-Trusted-Device-Token`。传输：**HTTPS + SSE
为主**（browser-sdk 明示 SSE=v1alpha2 新标准、WebSocket 为迁移遗留；
`/v2/session_ingress/mcp/ws/` 与 `wss://bridge.claudeusercontent.com` 为
MCP ingress / bridge 域，非主通道）。

### B2 公开度分级（逐项）

| 面 | 分级 | 证据 |
| --- | --- | --- |
| host 行为语义（注册/轮询/spawn/capacity/生命周期/接管文案） | **官方文档化** | remote-control.md（2026-09-05 抓取） |
| worker/host 接入 API（create/mint worker JWT/attach/控制指令/关闭码） | **官方 SDK 类型公开（@alpha）** | bridge.d.ts 全文（含「works from any process」原话） |
| 客户端消费 API（SSE 读 + POST 写 + 头要求） | **官方 SDK 类型公开（@alpha）** | browser-sdk.d.ts（含 query() 示例代码） |
| 端点路径族 / 事件类型名 / ccr 内部标识符 | **可从官方客户端真实样本逆向**（CLI 二进制即官方客户端） | 本机 strings 提取（b3 样本） |
| claude.ai/code 前端 JS | **受限黑盒**：无头抓取被 Cloudflare challenge 挡（403 cf-mitigated，实测）；需真实浏览器会话 | 实测 + browser-sdk 可作替代参照 |
| SSE 帧真实 payload / wire 细节 | **黑盒（无样本）**：静态只有类型名与字段名，无真实帧样本 | 阻塞于 A 组（需一个活体 RC 会话） |

注意 @alpha 的官方风险声明（bridge.d.ts 原文）：**"This is a separate
versioning universe from the main `query()` surface: breaking changes here
do NOT bump the package major."**

### B3 本地宿主侧形状

静态层（SDK bridge.d.ts + CLI strings）：host/worker 模型 =
`createCodeSession`（cse_* 会话）→ `fetchRemoteCredentials`（`POST
/{id}/bridge` 返回 `{worker_jwt, api_base_url, expires_in, worker_epoch}`，
**调用即 worker 注册、每次 bump epoch**；JWT 4 小时）→
`attachBridgeSession`（SSETransport + CCRClient；`from_sequence_num`
续传；心跳默认 20s、服务器 `ccr_heartbeat_policy` 覆盖；`PUT /worker`
状态 idle/running/requires_action；`POST /worker/events/{id}/delivery`
上报 processing/processed；`reportMetadata` 上报 branch/dir）。关闭码
语义：401=JWT 过期（换新凭据重连）、**4090=epoch 被顶（不再是 active
worker，即 takeover 机制）**、4091=init 失败、4093=心跳持续失败、
4094=凭据耗尽、403/404=永久拒绝；503/瞬断无限重试。

活体 debug-file 取证：**阻塞**（本机无 RC 资格，起不了任何 RC 进程）。

### B4 事件粒度与交互形状（逐项，均静态证据）

- **流式粒度**：SSE 事件流；`event_deltas[]=agent.message` /
  `agent.thinking` 查询参数 opt-in「live-preview `event_start`/
  `event_delta`」→ **token 级增量是官方支持的 opt-in 能力**（内嵌 API 表
  原文）；不 opt-in 时为事件粒度。
- **权限弹窗**：`can_use_tool` 走 `SDKControlRequest`/
  `SDKControlResponse` 双向通道（`sendControlRequest` 转发给 claude.ai、
  `onPermissionResponse` 收应答、`sendControlCancelRequest` 撤销）；
  文档补充：mid-turn 提示入队、其他对话框默认 5 分钟超时（`dialogExpiry`
  可调）、手机端权限模式限 Manual/Accept-edits/Plan。
- **控制指令全集**（SDK 回调面即官方指令清单）：`interrupt`、`stop_task`
  （按任务停）、`background_tasks`（Ctrl+B）、`set_model`、
  `set_max_thinking_tokens`、`set_permission_mode`、`rename_session`。
- **subagent/工作流进度**：事件族 `agent.thread_message_sent/received`、
  `agent.session_thread_message_*`、`agent.thread_context_compacted`、
  thread 级 stream 端点（`/threads/{tid}/stream`）——与官方「subagent/
  workflow 进度全端同步」的宣传对应。
- **附件/资源**：`resources` 子资源（`file` / `github_repository`）挂接；
  手机照片落 `~/.claude/uploads/` 后传路径（mobile.md）。
- **diff 拉取形状**：**黑盒**（静态无 diff 专用端点/事件命中；可能走
  resource 或事件 payload，无样本不下结论）。
- **镜像模式**：`attachBridgeSession({outboundOnly:true})` ——只出站
  （本地→CCR）不开 SSE 入站，「远程能看不能开」——官方明确支持的
  view-only 接入形态（对 CordCode 潜在价值高）。

## 4. C 组：会话托管与接管矩阵

### C2（最高优先实验）：阻塞，未实验

阻塞链（每条都是 owner 动作或 owner 授权动作）：

1. `~/.claude/settings.json` env 块（网关 base URL + 禁 traffic flag）→
   需 owner 修改（不可用 shell/project 层替代，见 A1）；
2. 本机无 claude.ai 登录 → 需 owner `claude auth login`（Pro/Max/Team）；
3. Desktop 托管会话当前走 cc-switch 代理 → 需 owner 把 Desktop provider
   切回官方直连（改变 owner 正在用的执行链路）；
4. Desktop 侧 RC 设置（`remoteControlAtStartup` / 「Enable remote control
   by default」，think.md 2026-09-05 取证：当前未开启）→ 指令明示需
   owner 授权且实验后还原。

文档层正面语义（不作为验证，仅列作实验假设）：resume 带 RC 的会话
「reattaches it to the existing claude.ai session」、transcript 存
Anthropic 服务器做同步锚点、官方把 Desktop 列为 Trusted Devices 可
view/steer 端。**机制层提醒**：attach 的关闭码 4090（epoch superseded）
表明同会话同时只有一个 active worker——「远程端发消息 + Desktop 本地
同时实时显示」若成立，必然不是双 worker 抢占，而是 Desktop 进程本身
作为唯一 worker 把事件渲染进 UI。这正是 C2 实验要验证的。

### C3 托管 × 操作矩阵（真值表：文档层=文档语义；机制层=静态代码证据；活体=BLOCKED）

host 进程 × 操作端（行为：接管通知 / 第二终端不接管 / Desktop reattach /
离线显示）：

| host \ 操作端 | Desktop 本地 UI | 浏览器 claude.ai/code | 官方手机 App | CordCode bridge |
| --- | --- | --- | --- | --- |
| **Desktop 托管会话（RC 开）** | 原生 UI（本终端即 host） | 文档：会话进 claude.ai/code 列表，可发消息/答权限；活体 BLOCKED | 文档：同上 + 推送；活体 BLOCKED | 文档/机制：走 `/v1/code/sessions` 客户端面接入（browser-sdk 模型）；与官方端互为「remote surface」；活体 BLOCKED |
| **终端 `claude --remote-control`（CLI host）** | 文档：resume 时 Desktop **reattach 到同一 claude.ai 会话**（不开新列表项）；活体 BLOCKED | 文档：列表明示 + takeover 文案（"Another device … took the session over"）；活体 BLOCKED | 文档：同浏览器；权限模式受限；活体 BLOCKED | 同上；活体 BLOCKED |
| **第二终端 resume 同会话** | — | 文档：**不接管**——第二终端打印通知且该终端 RC off（跨会话消息不可达），要移动需显式 `/remote-control`；活体 BLOCKED | — | — |
| **bridge spawn 的 claude（第三方 host，C4）** | 语义上等价上一行：Desktop resume 会 reattach（接管 bridge worker，机制=4090 epoch 顶替；单向移动而非并行直播）——**推断+机制证据，活体 BLOCKED** | 官方端可见（cse 会话入列表）——SDK 面支持；活体 BLOCKED | 同左；活体 BLOCKED | bridge 自持（自有通道） |
| **离线/进程死亡** | 文档：进程死后数秒内 offline；server 模式可被远程消息复活 | 文档：离线显示 + 消息排队，重连后补投 | 同左 | 事件断流（bridge 需自处理重连/游标） |

### C5 生命周期语义（文档层齐全，活体 BLOCKED）

断线重连：机器睡眠自动重连，重建期间**消息/权限/状态排队**后补投；server
模式离线 ~10 分钟退出、interactive 无限重试；心跳失联 ~30 分钟（先重注册
~30 分钟再断开）；HTTP 403 重试 3 分钟后断开并指认拒绝方（网络边缘/
代理/VPN/防火墙）；`claude remote-control`/`--continue`/`--session-id` 的
server-resume 窗口约 **4 小时**；`--spawn` 三模式（same-dir 默认 / 
worktree / session 单会话拒绝并发）；`--capacity` 默认 32（与
`--spawn=session` 互斥）；对话框超时默认 5 分钟（`dialogExpiry`）；
`CLAUDE_CLIENT_PRESENCE_FILE` 抑制推送。

设置解析链（CLI strings）：`remote_control_at_startup`（org_policy）优先，
user/project/local 各层 `remoteControlAtStartup` 均被读取（文档：project
里 `true` 被忽略、`false` 生效）。

## 5. D 组：认证与凭据

- **D1**：full-scope login token 硬要求（文档原话：setup-token /
  `CLAUDE_CODE_OAUTH_TOKEN` "can only make model requests"；`ANTHROPIC_API_KEY`
  设置须先 unset）。存储：macOS keychain（本机无条目=未登录）。**无头进程
  复用的官方先例**：① Desktop host-creds 注入（`CLAUDE_CODE_HOST_CREDS_
  FILE` + 属主/权限校验 + `CLAUDE_CODE_HOST_AUTH_ENV_VAR`，样本
  `d1-credential-shapes.md`）；② SDK `createCodeSession` 官方注释「thin
  HTTP wrapper with no implicit auth, so it works from any process」。凭据
  生命周期静态已知：OAuth token 自动刷新（v2.1.224 起 CLI 自愈）、worker
  JWT 4h + 每次 mint bump epoch。**多进程并发刷新冲突：黑盒**（无活体）。
- **D2**：bridge 合法取得凭据的两条途径：owner 在 bridge 机器登录一次
  （keychain 共享，CLI 多进程原生支持）或 host-creds 文件模式（bridge 自管
  0600 文件注入子进程）。风险：token 为全账号 scope；`untrusted_device`
  错误族 + `X-Trusted-Device-Token` 头存在表明 ELEVATED 安全层的设备信任
  机制——**个人版 Pro/Max 是否触发 enrollment 约束：黑盒**（文档说
  Trusted Devices 是 Team/Enterprise beta，但 bridge.d.ts 的 enforcement
  flag 未限定计划，需活体）。

## 6. E/F 组：工程化评估与替代路线

### E1 实现载体

**主路径：Node sidecar 直接用官方 SDK**（`@anthropic-ai/claude-agent-sdk`
的 `/bridge` + `/browser` 导出），依据：两个导出就是为外部进程接入设计
的（@alpha）。**备选：Go 直写 HTTP**（端点族简单、thin wrapper 可镜像），
但 SSE 帧 payload 无 wire 样本前 Go 侧风险显著更高（fail-closed 前提下
等于每次升级都可能碎）。建议与 codex-remote 相同的「Node probe 先行、
Go 化后置」路径。

### E2 版本漂移风险

高。官方明示 @alpha 面 breaking 不 bump major；CLI/Desktop 自动升级
（owner 本机 CLI 侧 `DISABLE_AUTOUPDATER=1`，Desktop 自动）。要求：形状锁
fixture 包（对每个消费的事件/端点形状绑定真实样本，同 codex-remote
`wire_descriptor` 模式）+ 未知形状 fail closed + 版本探针。

### E3 与现有 claudecode backend 的关系

opt-in backend 变体（非替换）：现有 stream-json 子进程模型继续服务网关
用户；RC 变体服务 claude.ai 订阅用户。iOS 侧**不是零改动**：RC 事件模型
（`agent.*`/`event_start`/`event_delta`/控制指令族）与现有 stream-json
投影不同，需新增投影层；但 bridge-v1 协议面（session/turn/事件信封）可
保持不变。

### E4 隐私语义（owner 决策材料）

现状：CordCode E2E relay，中继见不到明文；Claude backend 会话内容留在
本机 JSONL。RC 路线：**transcript 存 Anthropic 服务器**（同步锚点），
远程端消息经 Anthropic 服务器中转。对「用官方云换多端同步」的接受度是
产品取舍，不替 owner 决策。

### F 替代/补充路线

- **F1（本次升格为最强候选）Agent SDK host 模式**：bridge 自己 spawn
  claude（或 SDK `query()` host）+ `createCodeSession` +
  `attachBridgeSession` → bridge 进程即 worker；claude.ai/code 与官方
  App 免费获得 viewer/steer；iPhone 走 bridge 自有通道（也可用
  browser-sdk `query({sse})` 直接消费官方流）。解决了：不依赖 owner 手动
  开 Desktop RC、bridge 主导生命周期、`outboundOnly` 镜像模式可选。没解
  决：**Desktop「常驻实时显示」仍是 reattach/接管语义**（owner 在
  Desktop 点开该会话 = 接管 bridge worker，4090），不是旁观式直播；
  transcript 上云语义不变。
- **F2 Dispatch**：Pro/Max 限定（Team/Enterprise 不可用）；手机 App 内
  持久 Cowork 会话派生 Desktop Code 会话 + 完成推送。与本路线正交
  （派生的是 Desktop 端会话，不改变 bridge 接入面）。
- **F3 开放信号**：SDK bridge/browser 导出 @alpha、browser-sdk 里
  `ccr_v2_subscribe_sse_web` flag（web 端 SSE 消费刚开闸）表明官方正在
  把 CCR 面产品化。建议跟踪：code.claude.com/docs/en/changelog、
  anthropics/claude-agent-sdk-typescript releases、CLI `--help` diff。

## 7. 证据附录（样本索引，均脱敏）

| 文件 | 内容 |
| --- | --- |
| `scripts/claudecode-rc-probe/samples/a1-doctor-default.txt` | 默认环境 doctor RC 面板（六红） |
| `scripts/claudecode-rc-probe/samples/a1-doctor-project-override.txt` | project env 覆盖无效实验 |
| `scripts/claudecode-rc-probe/samples/a1-doctor-cleanhome.txt` | 干净 HOME 基线（阻塞分离） |
| `scripts/claudecode-rc-probe/samples/a1-rc-verbose.txt` | remote-control 拒绝 + auth state 面板 |
| `scripts/claudecode-rc-probe/samples/a1-remote-control-debug-file.jsonl` | --debug-file 原始 JSONL |
| `scripts/claudecode-rc-probe/samples/a1-settings-scope-map.md` | 三层作用域事实表 |
| `scripts/claudecode-rc-probe/samples/a1-desktop-host-auth-env.md` | Desktop host-auth + cc-switch 链路 |
| `scripts/claudecode-rc-probe/samples/b2-embedded-api-table.md` | CLI 内嵌 Sessions/Threads API 表 |
| `scripts/claudecode-rc-probe/samples/b3-cli-static-endpoints.md` | CCR 端点族/事件名/传输/ccr 族 |
| `scripts/claudecode-rc-probe/samples/d1-credential-shapes.md` | 凭据存储/刷新/host-creds 形状 |

上游文件引用（未复制入仓）：SDK 0.3.260
`/Users/jacklee/Projects/claude-agent-sdk-npm/package/{bridge,browser-sdk,sdk}.d.ts`。

## 8. 若续期：成本估算与 owner 决策点

### 续期成本（工作日当量）

- **M0 owner 解除资格门**（人工 ~1h）：`claude auth login`（需订阅）+
  全局 settings 决策（见下）+（若做 C2）Desktop provider 切官方 + RC 设置
  授权（实验后还原）。
- **M1 probe 续期**（1–2 天当量）：一个活体 RC 会话双端取证（CLI host
  `--debug-file` + 浏览器 claude.ai/code 操作），SSE wire 样本归档，
  C2/C3 矩阵实验收口，D1 并发刷新观察。
- **M2 POC**（2–3 天当量）：Node sidecar 消费一个 RC 会话（browser-sdk
  `query({sse})` 或 bridge worker attach），iPhone 最小链路。
- **M3 产品化**（视 M2）：backend 变体 + iOS 投影层 + 形状锁 fixture +
  fail-closed + 退出审计。

### 风险清单（若 go）

1. @alpha 面无契约，breaking 不 bump major（官方明示）——必须形状锁 +
   fail closed；
2. takeover/epoch 单 worker 语义与「多端并行直播」期望的冲突（C2 实验
   核心）；
3. token 全账号 scope + transcript 上云（安全/合规评审必做）；
4. Desktop/cc-switch 链路耦合（owner 环境特有，升级可能改变 host 注入
   行为）；
5. Cloudflare 对 claude.ai 面（api.anthropic.com 面理论上不受影响，未验）。

### owner 决策点清单（2026-09-05 裁决后收口）

1. ~~模型执行语义~~：随订阅前提一并搁置——无订阅则不存在该取舍。
2. **隐私语义**：同上，仅在恢复该路线时再裁。
3. ~~资格前提~~：**已裁决（2026-09-05）：无 Pro/Max/Team 订阅 → RC
   全路线搁置**。唯一恢复条件 = 未来订阅（Pro/Max；Team/Enterprise 需
   管理员开 RC toggle）。
4. ~~probe 续期授权~~：随 3 失效。若未来订阅，按 §8 续期成本 M0→M1
   执行即可，协议面证据包可复用。
5. **无订阅期间的现状确认**：Claude backend 维持现有形态（网关 glm 执行 +
   stream-json 子进程 + file-relay 旁观 Desktop）；「Desktop 实时显示 iOS
   消息」在无订阅下仍无官方路径（think.md「无跨进程总线」结论不变，RC
   是唯一官方多端同步通道且被订阅门挡住）。
