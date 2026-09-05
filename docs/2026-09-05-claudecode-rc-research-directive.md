# 调研指令：Claude Code Remote Control 客户端接入可行性（发给分析师 agent）

> 你是分析师 agent，任务是**只调研、不实现**：回答「CordCode bridge 能否作为 Claude
> Code Remote Control（下称 RC）的远程客户端接入」，产出 go/no-go 报告。全程遵守仓库
> `CLAUDE.md`（P0 来源门、source-first、fail-closed、禁止伪造成功）。开工前先读：
> 本仓 `CLAUDE.md`、`GO_BRIDGE_ARCHITECTURE.md` 的 claudecode 节、`think.md` 中
> 2026-09-05 的「Remote Control 发现」条目、`agent/codex-remote/`（对称先例）。

## 1. 背景与假设

CordCode 是 Mac bridge（本仓）+ 自有 iOS App 的产品，把各家 AI coding agent 暴露给
iPhone。Claude backend 现状：每个活跃 session 一个自有 `claude` CLI 子进程
（stream-json），Mac Desktop 发起的外部回合靠会话文件轮询旁观；反向（iOS 发起）Desktop
不实时显示——Claude 没有跨进程事件总线，Desktop 不 tail 会话文件（think.md 2026-09-05
已收口，勿重查）。

官方 RC（<https://code.claude.com/docs/en/remote-control>）提供了多端同步会话架构：
本地 Claude Code 进程出站 HTTPS 注册到 Anthropic 服务器，claude.ai/code 与官方手机 App
作为远程 surface 流式收发，终端/浏览器/手机可互换发消息，transcript 存 Anthropic 服务器
做同步锚点。

**待验证的核心假设（路线 A，codex-remote 同构）**：Desktop 托管会话并开启 RC，
CordCode bridge 扮演「远程客户端」角色（等价于官方手机 App 的位置）→ iOS 回合实际在
Desktop 托管的进程里执行 → Mac Desktop 原生实时流式，iPhone 侧直接消费官方事件流。

版本锚（2026-09-05 记录，你须重新核实并写入报告来源清单）：Desktop 内嵌 CLI
`~/Library/Application Support/Claude-3p/claude-code/2.1.260/`；PATH CLI 2.1.234；
官方文档以当日抓取为准。

## 2. 必答问题（按此顺序，A 组可能直接决定 no-go）

### A. 资格门（先做，失败即停并报告）

- A1 本机 RC 是否可用：在**测试目录**跑 `claude doctor` 与 `claude remote-control
  --verbose --debug-file <tmp>`，确认订阅资格、login token 类型、env 冲突。注意文档
  红线：`ANTHROPIC_BASE_URL` 指向非 api.anthropic.com（如本机 bigmodel 网关）、
  `DISABLE_TELEMETRY`/`DO_NOT_TRACK`/`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`/
  `DISABLE_GROWTHBOOK` 任一设置都会拒绝 RC。逐项查 shell env + `~/.claude/settings.json`
  的 env 块 + 项目 settings，写明各自作用域。
- A2 若网关 env 在全局生效：RC 会话只能走 api.anthropic.com，意味着这些会话的模型
  执行不再经网关改写（opus/sonnet→glm 映射失效）。这是产品语义变化，只记录事实与
  影响面，**不要替 owner 决策**。
- A3 订阅形态：RC 不支持 API key，需 Pro/Max/Team/Enterprise 的 claude.ai login。
  查清当前登录形态即可。

### B. RC 客户端协议面（核心工程问题）

- B1 远程客户端（claude.ai/code 浏览器端 / 官方 App）与 Anthropic 服务器之间的真实
  协议：传输（WebSocket/SSE/HTTP 轮询）、端点、鉴权头、会话发现与列表、事件流形状、
  发消息、权限应答、控制指令（model/effort/compact/clear/rename）。
- B2 该协议的公开度分级：逐项标注「官方文档化 / 可从官方客户端真实样本逆向 /
  完全黑盒」。检索面：code.claude.com/docs 的 llms.txt 全索引（注意 cross-session
  messaging、channels-reference 等相邻页）、claude-agent-sdk-typescript（RC host 语义
  是否暴露）、CLI `--debug-file`/`--verbose` 输出、claude.ai 前端加载的 JS。
- B3 本地宿主侧（`claude remote-control` 的注册/轮询/上行流）真实形状，用 debug-file
  取证。
- B4 事件粒度：远程端拿到的是逐 token 流式增量还是 message 粒度？权限弹窗如何下发与
  应答？subagent/workflow 进度、图片/文件附件、diff 拉取的形状？每一条都必须有真实
  样本支撑，不得从文档措辞推断。

### C. 会话托管与接管矩阵（决定产品形态）

- C1 Desktop Code tab 托管的会话开启 RC（Desktop 设置「Enable remote control by
  default」或 `remoteControlAtStartup`）后，是否出现在 claude.ai/code 会话列表？
- C2 **用户愿望的直接验证（最高优先实验）**：远程客户端向 Desktop 托管的会话发消息时，
  Desktop 本地 UI 是否实时流式显示？必须用真实实验证明（见 §3 方法），文档推断不算。
- C3 接管/共存语义矩阵：host 进程 × 操作端（Desktop 本地 UI / 浏览器远程端 / 官方
  App / 我们 bridge）× 行为（takeover 通知、second-terminal 不接管、Desktop reattach、
  离线显示）。文档分散提及，须实验拼出完整真值表。
- C4 会话由第三方进程（bridge spawn 的 claude 带 `--remote-control`）host 时，
  Desktop 与官方 App 的可见性/控制权（中间形态：官方端能看 CordCode 会话，但 Desktop
  仍不实时——单独记录）。
- C5 断线/睡眠/重连、消息排队、server 模式容量（默认 32）与 `--spawn` 三模式、
  停服后 4 小时 resume 窗口等生命周期语义。

### D. 认证与凭据

- D1 RC 要求 full-scope login token（`claude setup-token`/`CLAUDE_CODE_OAUTH_TOKEN`
  被拒）：token 存哪里、能否被第二个无头进程（bridge）复用、多进程并发刷新冲突、
  过期续期行为。
- D2 bridge 作为「设备端客户端」取得合法凭据的途径与风险；个人版是否受 Trusted
  Devices（Team/Enterprise 限定）影响。

### E. 工程化评估（仅当前面成立）

- E1 实现载体：Go 直写 vs Node sidecar 复用官方 npm SDK。给出依据（SDK 是否真的
  暴露 RC 客户端面，还是只有 host 面）。
- E2 版本漂移风险：协议无公开契约，Desktop/CLI 自动升级的破坏面、检测手段、
  fail-closed 设计要求。
- E3 与现有 claudecode backend 的关系：替代 / 共存（RC 模式 opt-in backend 变体）；
  iOS 侧（bridge-v1 协议）是否零改动。
- E4 隐私语义：transcript 上 Anthropic 服务器 vs 现在 CordCode E2E relay 的差异，
  写成 owner 决策材料，不替 owner 决策。

### F. 替代/补充路线（简述即可）

- F1 Agent SDK host 模式（bridge 用官方 SDK host 会话 + RC on，官方 App/浏览器作
  viewer）——是否更可行？解决了什么、没解决什么。
- F2 Dispatch（官方手机 App 派发 Desktop 会话）语义与本路线的边界。
- F3 官方开放信号面：changelog/GitHub issue 追踪建议。

## 3. 建议方法与红线

方法（按序）：

1. A 组资格检查（测试目录，隔离 env）。
2. 双端取证：本地 `claude remote-control --verbose --debug-file`（server 模式，
   `--spawn session`、`--capacity 1`、测试目录）+ **浏览器打开 claude.ai/code 作为
   远程端**操作（发消息、看流式、答权限），两侧同时取证。浏览器端操作优先于官方
   手机 App（真机/App 操作需 owner 明确授权）。
3. 样本归档：真实 wire/debug 样本脱敏后存 `scripts/claudecode-rc-probe/`（参照
   `scripts/claudecode-phase0/` 的组织方式），报告引用文件路径。
4. C2/C3 矩阵实验：需要 Desktop 托管会话时，用**测试项目目录**的会话；不得改 owner
   正在用的会话；不得改 `/Applications` 正式 App 与全局 `~/.claude/settings.json`
   （实验用临时 env/`--debug-file`/测试目录隔离；需要 Desktop 侧开 RC 设置时，先向
   owner 要授权并在实验后还原）。
5. 对照先例：`agent/codex-remote/` 当年 probe 的做法与落地路径。

红线：

- 只调研不实现；不改产品代码；不动生产 relay；不动 owner 正式环境配置（除上条已授权
  场景）。
- 每个协议形状断言必须挂真实样本（audit-plan 纪律）；无样本的只能列为阻塞或黑盒，
  不得写成「应该/大概是」。
- token/凭据值不得出现在任何产出物；样本脱敏后再归档。
- 报告结论分级标注：组件级已验证 / 生产路径已验证（本任务应全部是前者）。
- 时间盒：一个工作日当量内完成 A/B/C2 主干；超出先交中间报告再续。

## 4. 交付物

`docs/2026-09-XX-claudecode-rc-client-probe.md`（XX=完成日），包含：

1. 来源清单（路径+分支+提交 / 文档 URL+抓取日 / CLI 版本锚，按 CLAUDE.md 格式）；
2. go/no-go 结论 + 判据（B 组协议可逆向度、C2 Desktop 直播是否真实验证通过、D 组
   凭据可行性三者为硬门）；
3. C3 托管×操作矩阵真值表；
4. 成本估算（若 go：里程碑拆分）与风险清单；
5. owner 决策点清单（网关放弃、transcript 上云、opt-in 形态等）；
6. 证据附录：样本文件索引（脱敏后路径）。
