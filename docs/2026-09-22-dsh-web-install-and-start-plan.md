# DeepSeek Harness：未安装可代装、未启动可点启动

Date: 2026-09-22
Status: 方案 v3，未实施。评审通过前不改业务代码。
Branch: `feat/ios-native-message-timeline`
HEAD: `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e`
对照评审: `docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md`（round 2，修改后通过；v3 落地后视为通过）

## 本轮修订（v2 → v3）

只处理 round 2 的两处条件。设计方向不改，不进入开发。

采纳：

- R2′-1 建议：没有 `dsh` 二进制时优先 `not_detected`，不报 `port_conflict`。安装是唯一前进动作；装完点「启动」时，端口冲突由 spawn 错误浮出。
- R2′-1 的判别顺序：先看有没有二进制，再 `host.describe`，失败后再用 TCP 是否连上区分「无监听」和「在听但不应答」，然后才 `lsof`。
- R2′-2：§0 的 iOS 行改为 v3 编写时的提交与未提交状态，并写明快照会漂移、以实施门复核为准。

不采纳：

- R2′-1 备选（`port_conflict` 优先，先清端口再谈安装）。理由：没装 `dsh` 时清端口不能把行变成可启动；安装才是前进动作。端口被占留到点「启动」后由 spawn 错误露出，§5 已有这条覆盖。

## 本轮修订（v1 → v2）

设计方向不改。本轮只处理评审报告。

采纳：

- R1-1：§0 改为当前工作树的完整来源清单，并记下 round 1 清单与现在的差异。
- R1-2 路径 1：§0 写明 owner 改令出处；两处注记改写后声明为本变更集的 doc-only 交付。
- R2-1：`port_conflict` 补只读判定，不靠 spawn 错误。
- R2-2：§7 增加「诊断 / 重新检查 / 冷启动不调用 starter」回归。
- R2-3：§8 增加 CHANGELOG 条目。
- R2-4 的日期：补上 2026-08-15 指令 v2。`README.zh.md` 行号 v2 复核仍是 26 与 29，不改。

不采纳：

- R1-2 路径 2（回退两处注记，改成「待评审」）。理由：owner 已在 2026-09-22 本会话明确说「修改不代装裁决，改为可以代装」。注记不回退。v1 把注记写成「已被取代」，那是评审通过前的既成事实口吻，本轮改掉。
- 不把「冷启动不再自动补拉」写成与代装并列的 owner 原文裁决。理由：owner 的改令只覆盖代装。冷启动行为是由同一会话的「未启动 + 启动按钮」交互要求推导的方案结论，下面分开写。

范围只对 **dsh-web / DeepSeek Harness 工作站行**。评审通过并实施之后，下面两点才取代旧约束；在此之前，运行中的代码仍是旧行为。

1. **不代装 → 可以代装。** 这是 owner 改令。被取代的是 2026-08-13 设计（`docs/2026-08-13-dsh-driver-design.md`）加 2026-08-15 owner 产品指令 v2（`agent/dsh/discovery.go:3-8`：「NEVER installs」）。用户点「安装」后，Link 可以下载并安装官方 `@deepseek-ai/dsh`，装完立刻启动 3080。legacy `agent/dsh` 探测链的永不安装锁不在这条改令里，保持不动。
2. **冷启动缺位自动补拉 → 等用户点「启动」。** 这是方案推导，不是另一句 owner 裁决原文。2026-08-19 设计写的是「本进程从未持有座位时自动补拉」。owner 要求重启后显示「未启动」和「启动」，自动补拉会让这个按钮没有机会出现。因此打开 Link 时只探测，不补拉。

2026-08-19 设计里其余约束继续有效：座位默认 `127.0.0.1:3080`、端口即身份、只绑 loopback、永不 `--trusted-host`、Link 退出不杀已拉起的座位、宽限内 hello 不得折成 `not_configured`。历史完成报告不改写。

## 0. 来源

v2 编写时复核。round 1 评审时的 Mac HEAD 是 `8091d7b05e5fe335648b4b1d61a4ff7fadd64db8`，当时工作树是 10 个已修改加 3 个未跟踪。那些与本方案无关的投影/协议脏文件已进入 `86452bf` 与 `b4e42d5`，不再留在工作树。v1 只列了其中 6 个，且漏了与评审范围重叠的两处注记。下面是现在的清单，不是 round 1 的快照。

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=b4e42d5c5a9af6c12abe062a32a30bccece3bd5e
未提交状态=
  M  GO_BRIDGE_ARCHITECTURE.md
     本变更集 doc-only 交付：座位模型节的方向注记。不是已实施记录。
  M  docs/2026-08-19-dsh-web-canonical-3080-instance-design.md
     本变更集 doc-only 交付：文首方向注记。正文不改写。
  ?? docs/2026-09-22-dsh-web-install-and-start-plan.md
     本文件。
  ?? docs/2026-09-22-dsh-web-install-and-start-plan-review.md
     round 1 评审报告。本轮不改它。
  ?? docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md
     round 2 评审报告。本轮不改它。
任务预期分支=feat/ios-native-message-timeline
配套 iOS 路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套 iOS 分支=feat/ios-native-message-timeline
配套 iOS 提交=544a8de705bd91db6c03e66092d4122507a2074b
配套 iOS 未提交=干净（v3 编写时）
  round 2 评审时是 1870d059，当时 2 个 M：TimelineCardCells.swift、
  NativeTimelineViewController.swift，均已进入 544a8de，与 dsh-web status 无重叠。
  iOS 快照会漂移，status 结论以实施门复核为准。
上游=/Users/jacklee/Projects/deepseek-harness
上游提交=0d1f50007f9bca3f52b06e1c3074fa14d5fb0720
上游描述=dsh-v0.1.6-alpha.1-5-g0d1f50007f
```

裁决出处（R1-2 路径 1）：

- 时间：2026-09-22，本会话。
- 载体：owner 消息原文「修改『不代装』裁决，改为可以代装。然后写个详细的实施计划文档」。没有单独的书面备忘录或提交。仓内在 v1 之前没有这条改令的记录，所以 round 1 无法从仓库核实。
- 内容：只改代装。同一会话更早的消息要求未安装显示「未安装」+「安装」，已安装但未启动显示「未启动」+「启动」。冷启动不再自动补拉是从这后一句推导的，不是这句改令的原文。

官方安装入口不是本仓历史文档里的 `npm i -g`，而是 README 的 npm 运行命令：

- `README.zh.md:26`：`npx @deepseek-ai/dsh web`（v2 复核仍是这一行；上一行 23 是「安装 Node.js，然后运行」）
- `README.zh.md:29`：默认 `http://127.0.0.1:3080`；`--no-open` 不打开浏览器
- `apps/cli/package.json:14-16`：包名 `@deepseek-ai/dsh`，bin 名 `dsh`
- 仓库根 `package.json:8-10` 的 `node: ^22.19.0 || >=24.0.0` 是贡献者 monorepo 约束。`apps/cli/package.json` 没有 `engines`。实施时读**发布包**的 engines；没有就不要用 monorepo 版本去拒绝用户的 Node。

写计划时 `npm view @deepseek-ai/dsh version` 为 `0.1.5-rc.2`。本机 `/opt/homebrew/bin/dsh` 是 `0.1.7-alpha.1`。两者都不是安装目标。代装走注册表默认解析，不复制本机二进制，不从上游 checkout 装。

## 1. 现在为什么是「未配置」

工作站行只显示 `hello`/`agents` 里的 status 文案。dsh-web 走布尔探针：

- `agent/dsh-web/dshweb.go:228` `InstanceStatus()` 只回 available + detail
- `go-bridge/agent_descriptor.go:271-280` `detectInstanceStatusProber` 把 `available=false` 一律收成 `not_configured`
- `MacBridge/MacBridge/Views/BridgeStatusView.swift:338` `not_configured` → 「未配置」
- `WorkspaceView.swift:609-616` 不可用行只有「重新检查」，没有安装/启动

所以「找不到 `dsh`」「3080 没起来」「补拉失败」「还在探测」在界面上是同一句「未配置」。枚举里已经有 `not_detected` 和 `service_not_running`（`agent_descriptor.go:23-25`），dsh-web 没用。全局文案是「未找到」「未运行」（`Localization.swift:1166-1168`）。本方案不改 Claude/Grok 的全局文案，只在 DeepSeek Harness 行覆盖成「未安装」「未启动」。

现有冷启动会自己补拉（`GO_BRIDGE_ARCHITECTURE.md` 座位模型第 3 点，`resolver.go` 的 `spawnOnSeat`）。这和「重启后显示未启动、等用户点启动」冲突。实施时改的是这条冷启动，不是再叠一个按钮在自动补拉旁边。

`agent/dsh/discovery.go:3-8` 和 `discovery_test.go:290` 的 `TestProbeChainNeverInstalls` 锁的是 **legacy SDK 探测链**。代装代码不得写进 `agent/dsh/`。那条 grep-lock 保留。

## 2. 人怎么走完

零件按这四拍串。没有「先接通 API、按钮下期再做」。

### 2.1 未安装 → 安装并启动

1. **打开。** CordCode Link 工作站，「AI 工具」里的 DeepSeek Harness 行。没有第二层设置页，没有终端命令给用户抄。
2. **输入。** 行上是橙色「未安装」和「安装」。不要求填 URL、端口、npm 包名。Node.js 不在搜索路径里时，同一位置的按钮改成「需要 Node.js」，点了打开 `https://nodejs.org`。Link 不下载、不安装 Node。
3. **发出去。** 点「安装」后，用已找到的 `npm` 执行 `npm install -g @deepseek-ai/dsh`（无版本钉、无 sudo、无 `--unsafe-perm`）。成功后用装出来的 `dsh` 绝对路径走现有座位补拉：`dsh --profile web --host 127.0.0.1 --port 3080 --no-open`。不走 `npx` 当「已安装」——npx 缓存不是下次 `LookPath("dsh")` 能稳定找到的二进制，重启后又会变回未安装。
4. **过程。** 按钮变成「安装中」并禁用，直到 npm 结束再变「启动中」，直到 `host.describe` 应答或失败。成功：行变绿色「就绪」，按钮消失。npm 失败：仍是「未安装」，名字下用一行显示 npm 的真实错误，按钮回到「安装」。npm 成功但座位起不来：变成「未启动」加「启动」，名字下保留补拉原文。不回滚已装好的包，也不写成「未配置」。

全局 npm 目录不可写时，不弹第二张确认框，不提权。改装到 `~/Library/Application Support/CordCode Link/dsh-install/`，字幕写明「已装到 CordCode 目录，终端里不一定有 dsh」。两条都失败才停在「未安装」并显示真实错误。

### 2.2 已安装、3080 没起来 → 启动

1. **打开。** 同一行。典型场景是重启电脑后打开 Link，用户没有自己跑 `dsh web`。
2. **输入。** 橙色「未启动」和「启动」。没有端口输入。座位仍是探测列表首位，默认 3080。
3. **发出去。** 点「启动」只调用现有 `spawnOnSeat`，argv 与今天托管启动相同（`resolver.go:156-157` 的 `--profile web --host 127.0.0.1 --port <座位> --no-open`）。不新开 3096。端口上已经是 dsh 在听，则收养，行直接「就绪」，不再起第二个进程。
4. **过程。** 按钮「启动中」并禁用。30 秒内（`managedBootTimeout`，`resolver.go:131`）`host.describe` 成功 → 「就绪」。失败 → 停在「未启动」，字幕是补拉原文（二进制退出、非 dsh 占用端口、超时）。「重新检查」只重读状态，不启动。

打开 Link、刷新、点「重新检查」、跑诊断，都不得偷偷补拉。只有「安装」的后半段和「启动」可以拉起座位。

### 2.3 已经在听

3080（或用户配置的座位）已有 dsh 在应答：绿色「就绪」，没有安装/启动按钮。谁拉起来的不重要。Link 退出仍不杀这个进程。

### 2.4 失败时人还在哪

人留在同一行。没有跳到诊断面板才看得到原因。字幕一行，hover 看全文。下一次点击是重试，不是另一条隐藏路径。

缺 API key 不是未安装，也不是未启动。座位在听就是「就绪」。密钥仍由用户在 dsh Web 里配置；本方案不做密钥表单。就绪不等于已经能在 iPhone 上聊。

## 3. 状态

dsh-web 改为和 codex-remote 一样暴露结构化就绪（`agent/codex-remote/diagnostics.go:16-34`，`detectStructuredInstanceReadiness`，`agent_descriptor.go:249`）。描述符不再走布尔折叠。

| 条件 | wire status | 这一行显示 | 按钮 |
| --- | --- | --- | --- |
| 搜索路径和记录的绝对路径都没有 `dsh`。优先于下一行「端口占用」：3080 同时被非 dsh 占用时仍走本行 | `not_detected` | 未安装 | 安装；没有 Node/npm 时改为「需要 Node.js」 |
| 有 `dsh`，座位没在听，本进程没在宽限里 | `service_not_running` | 未启动 | 启动 |
| 座位 `host.describe` 成功 | `available` | 就绪 | 无 |
| 本进程曾经持有、正在 120 秒宽限 | `available` | 就绪 | 无；detail 仍是 reconnecting，不闪成未启动 |
| 已有 `dsh`，TCP 能连上座位，`host.describe` 失败，且 lsof 命令行不含 `dsh`。没有二进制时不走本行 | `port_conflict` | 端口占用 | 无安装按钮；字幕是 lsof 看到的命令行，不是 spawn 错误 |
| 用户点过启动/安装后半段，补拉失败，二进制还在 | `service_not_running` | 未启动 | 启动；字幕保留原文 |

禁止再把以上任何一条收成 `not_configured`。宽限内继续禁止 `available=false`（`dshweb.go:224-230` 的理由不变）。

「安装中」「启动中」是 Mac 行的本地态，不新增 wire 枚举。动作进行时 hello 仍可以是 `not_detected` 或 `service_not_running`；行自己盖住按钮文案，不要闪回「未配置」。

iOS 不新增按钮，不新增协议字段。round 1 评审报告第 4 节已在当时的配套提交 `279c9018` 核清：不按 status 藏入口。v2 在当前配套提交 `1870d059` 上复核仍成立：`BackendModels.swift:25` 只在 `status != "available"` 时禁用切换并透传 reason；`deepSeekWeb` 仍是常驻枚举（`:61`，wire 匹配 `:81`）。状态从 `not_configured` 变成 `not_detected` / `service_not_running` / `port_conflict` 不移除入口。实施门再对当时的配套提交复核一次；本方案不改 iOS。

## 4. 代装怎么做

搜索 `dsh` 的路径 = 今天 runtime 已合并的 CLI 路径（`RuntimeManager.swift:252-274`：bun、homebrew、`/usr/local`、pnpm、volta、`~/.npm-global/bin`）再加 nvm 最新版本的 `bin`。GUI 不继承 shell PATH，nvm 安装的 `dsh` 今天会被误报成没有。找到绝对路径就记下来，下次用绝对路径，不靠 `LookPath` 碰运气。

代装命令，成功路径：

```text
npm install -g @deepseek-ai/dsh
```

约束：

- 只用已经找到的 `npm` 绝对路径，不用 `sh -c`。
- 不钉版本。官方 README 没有版本。装完记录 `dsh --version` 到安装记录，供诊断，不参与是否就绪。
- 不从 `/Users/jacklee/Projects/deepseek-harness` 装，不把本机已有的 alpha 二进制复制进 Link。
- 不 `sudo`。prefix 不可写就落到 CordCode 数据目录的 prefix，并用 `npm install --prefix <that> @deepseek-ai/dsh`。记录里写 `scope=user-global` 或 `scope=cordcode-prefix`，以及 bin 绝对路径。
- 已有可执行 `dsh` 时不显示「安装」，也不再跑 npm。
- 单飞。第二次点击在第一次结束前无效。
- 超时 10 分钟。超时杀掉该 npm 进程组，行回到「未安装」，字幕写超时，不写成成功。
- npm 的 stdout/stderr 可进 Link 日志；界面字幕用最后一段非空错误。不记录用户 home 之外的凭据，npm 输出里若出现 token 样式则打码。本命令本身不传 token。
- 装完必须能执行 `dsh --version`，然后才进入启动。`--version` 失败 = 安装失败，不启动。

Node 不在范围内。没有 `node`/`npm` 时代装不能开始。这不是把「不代装」从 Node 上偷偷留一口；官方前置就是用户自己的 Node，Link 不去捆绑 Node 运行时。

## 5. 启动怎么接现有补拉

保留 `spawnOnSeat`、`--no-open`、loopback、宽限、Link 退出不杀座位、端口占用如实报错。改调用时机：

- `backgroundResolve` / 冷启动 `Resolve`：只探测座位。没有应答且本进程从未持有 → 返回「未启动」，**不** `managedStart`。
- 宽限到期：仍按 2026-08-19 补拉。这是「用户这次已经让它跑起来、中途掉了」，不是「重启电脑后第一次打开 Link」。
- `RunDiagnostics` 今天会 `Resolve` 并可能补拉（`diagnostics.go:60-72`，注释写明 MUTATING）。实施后诊断与「重新检查」只读，不得成为第三条启动路径。
- 「启动」和「安装」后半段调用同一个显式 `StartSeat`。进行中的 RPC 仍立即 `backend_unavailable`，不阻塞在补拉上。
- `port_conflict` 只读判定，不先 spawn。一句顺序：先找二进制，没有就停在 `not_detected`，不看端口；有了才做只读 `host.describe`，成功即 `available`；失败后再 TCP 连接座位端口，连不上就是无监听、`service_not_running`，连上才 `lsof`。命令行含 `dsh` → `service_not_running`（进程在，接口未就绪）；不含 `dsh` → `port_conflict`，字幕用该命令行，不用 `LastSpawnErr`；`lsof` 失败不猜占用，停在 `service_not_running`，字幕写「端口开着但 host.describe 失败，lsof 不可用」。没有二进制时即使 3080 被非 dsh 占用，也不报 `port_conflict`。用户点「启动」或安装后半段之后，现有 spawn 错误（`diagnostics.go:76` 的 non-dsh）可以覆盖字幕，因为它比冷探测更具体。

「重新检查」继续走 `POST /internal/agents/{id}/test`（`management_api.go:653`），该接口只重建描述符。新增：

- `POST /internal/agents/dsh-web/install`
- `POST /internal/agents/dsh-web/start`

都只接受本机 management token。返回 `{status, detail, binPath?}`。Swift 在 `ManagementAPIClient` 加对应方法，`WorkspaceView` 的 DeepSeek 行在不可用时按 status 放按钮，样式对齐现有「配对」「重新检查」（`WorkspaceView.swift:576-616`）。

安装记录写在数据目录、`0600`、无凭据：bin 路径、npm scope、版本、时间。resolver 启动时读这个路径作为 `cli_path`，避免 GUI PATH 在安装成功后仍找不到。

## 6. 不做

- 不代装 Node、不捆绑 Node、不打开终端让用户自己贴命令。
- 不做 API key / provider 表单。
- 不给 iPhone 做安装或启动按钮。
- 不改 Claude、Codex、Grok、OpenCode 的状态文案和按钮。
- 不恢复 3096–3196，不在 3080 之外再起一个「Link 私有 dsh」。
- 不改 `agent/dsh` 的永不安装锁。legacy SDK 路线仍不代装。
- 不改写 2026-08-13 / 2026-08-19 正文。两处注记只指向本文件、标明 owner 改令与方案推导的差别，不写成「已被取代」。
- 不提交、不安装 App。本文件不是发布。

## 7. 过审后怎么验

先改完再测，不每改一行就构建。

- `agent/dsh-web`：冷启动探测不到座位时不调用 starter；`StartSeat` 才调用；二进制缺失 → `not_detected`；有二进制且座位暗 → `service_not_running`；座位在听 → `available`；宽限内仍 `available`；补拉错误原文不被收成 `not_configured`。
- 诊断只读：`RunDiagnostics` 与描述符刷新（「重新检查」走的 `POST /internal/agents/{id}/test`）在座位暗、本进程从未持有时不调用 starter。与冷启动不补拉同一条断言，单独测，避免只锁冷启动、诊断仍能补拉。
- `port_conflict`：fake lsof 报非 dsh 监听且 `host.describe` 失败 → `port_conflict`，且 starter 未被调用；lsof 失败 → `service_not_running`，不猜占用。
- 安装器：argv 断言为 `npm install -g @deepseek-ai/dsh` 或 `--prefix` 回退；无 sudo、无 npx、无版本钉；已有 bin 时不再 exec npm。用 fake npm，不访问注册表。
- `agent/dsh` 的 `TestProbeChainNeverInstalls` 仍必须过。
- Swift：DeepSeek 行 `not_detected` 显示「未安装」+「安装」；`service_not_running` 显示「未启动」+「启动」；`available` 无这两个按钮。Claude 的 `not_detected` 仍是「未找到」。
- 定向 `go test` 覆盖上述包，再一次增量编译 MacBridge。不跑全量 UI Test。
- 真实 npm 安装和真机 3080 不在默认验证里。方案通过且实现落地后，由 owner 在工作站行点一次安装或启动；agent 只核对日志里出现本次独有的安装/启动结果，不用旧「未配置」截图当证据。

## 8. 实施时先做的接线

1. dsh-web `StructuredInstanceReadiness`，描述符改走结构化检测。
2. 冷启动 `Resolve` 停止补拉；抽出 `StartSeat`。诊断改为只读。
3. 安装器 + 安装记录 + 两条 management POST。
4. 工作站行按钮、字幕、进行中态、中英文字案。
5. 探测路径补 nvm；安装后的绝对路径写回 resolver。
6. `CHANGELOG.md` 的 `[Unreleased]` 记一条：工作站行区分未安装/未启动，点安装会代装并启动，点启动才拉起 3080。
7. 按第 7 节收口测试。不碰 §0 里与本方案无关的未提交文件。
