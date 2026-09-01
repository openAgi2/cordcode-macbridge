# codex-remote Backend 实施方案（ChatGPT Desktop Remote Control 接力）

- 日期：2026-08-26
- 状态：**已完成（proved-complete，2026-08-29）。全计划 111/111 done+proven，Phase 0–5 全部 proven-done，完成报告见 [2026-08-28-2026-08-26-codex-remote-backend-implementation-plan完成情况.md](2026-08-28-2026-08-26-codex-remote-backend-implementation-plan完成情况.md)。**
  历史裁决：2026-08-28 Owner 改写 Gate P0（接受无 cursor 的首连 live 流，进入 Phase 1），当日曾短暂标记 FAIL-BLOCKED（见下方停工说明与 §13 尾部回填）。cursor 断线续传 / interrupt / 官方 iOS controller 共存为**按设计保留的已知缺口**，产品路径 fail-closed，不广告为已实现。
- 上下游：**本计划是 codex-remote 产品线的根方案（无母方案）。** 下游派生：懒加载历史方案 [2026-08-30-codex-remote-lazy-history-implementation-plan.md](2026-08-30-codex-remote-lazy-history-implementation-plan.md) 建立在本计划 Phase 0/1 的配对/WSS/envelope/live 基础设施上；iOS 仓 `docs/2026-08-31-remote-web-ios-feature-parity-gap-analysis.md` 为浏览器端平行产品线（写于本计划交付前，矩阵未覆盖 codex-remote）。
- 停工说明：[2026-08-28-codex-remote-phase0-fail-blocked.md](2026-08-28-codex-remote-phase0-fail-blocked.md)
- 目标仓库：`cordcode-macbridge`，后续涉及 `cordcode-ios`
- 相关既有方案：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)
- 评审报告：[2026-08-26-codex-remote-backend-implementation-plan-audit.md](2026-08-26-codex-remote-backend-implementation-plan-audit.md)
- 第二轮评审：[2026-08-26-codex-remote-backend-implementation-plan-audit-r2.md](2026-08-26-codex-remote-backend-implementation-plan-audit-r2.md)
- 上游官方源码：`/Users/jacklee/Projects/codex`

> [!IMPORTANT]
> 本方案新增与 `codex-web` 平级的 `codex-remote` backend。`codex-web` 保留现状，继续服务官方
> local daemon/UDS 拓扑；`codex-remote` 通过 OpenAI Remote Control 数据面进入 ChatGPT Desktop
> 当前持有的私有 app-server。两个 backend 不互相冒充，不共享运行态，不以一个失败否定另一个的价值。

> [!WARNING]
> “使用 Codex Remote Control 源码”不等于“可以把未修改的 ChatGPT Desktop 指向 CordCode Relay”。
> iPhone 与 MacBridge 仍走 CordCode LAN/HPKE Relay；MacBridge 与 Desktop 私有 app-server 之间，
> 在当前官方实现下必须先通过 OpenAI Remote Control Relay。除非未来官方提供可配置 broker/本地
> controller transport，否则禁止以修改 App 包、全局代理、凭据劫持或 DNS/MITM 伪造该能力。

## 0. 本次来源清单

### 0.1 MacBridge

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=83eb6da4b2584fd170d83b05f742946b5472c9be
未提交状态=未跟踪 docs/2026-08-26-codex-remote-backend-implementation-plan.md（本任务正在修订的方案文件）；无其他 MacBridge 工作区改动
任务预期分支=用户指定在当前 MacBridge 仓库 docs 新增实施方案；未指定功能分支
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios / main / 476e20941c1c7a46ce47ccd9aadc84bd4ef8da43；/Users/jacklee/Projects/codex / main / 25a6e316c81fb7600d1d75f3e63ffe26be10b7c8
预期产品特性=新增 codex-remote 独立 backend，实时接入 ChatGPT Desktop 私有 app-server；保留 codex-web
```

### 0.2 iOS

```text
仓库路径=/Users/jacklee/Projects/cordcode-ios
分支=main（本地领先 origin/main 14 个提交）
提交=476e20941c1c7a46ce47ccd9aadc84bd4ef8da43
未提交状态=删除 docs/2026-08-25-remote-web-push-notification-design.md；未跟踪 3 份 remote-web push 文档；均与本方案无关，本次不读取为产品事实、不修改
任务预期分支=当前仅写 MacBridge 方案；进入 iOS 实施前必须重新解析唯一配套工作树
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-macbridge / main / 83eb6da4b2584fd170d83b05f742946b5472c9be
预期产品特性=后续新增 BackendKind.codexRemote 与独立缓存/会话 identity，不覆盖 codexWeb
```

### 0.3 Codex 上游

```text
仓库路径=/Users/jacklee/Projects/codex
分支=main
提交=25a6e316c81fb7600d1d75f3e63ffe26be10b7c8
未提交状态=干净；HEAD 与 origin/main 一致
任务预期分支=只读核验最新官方源码
配套仓库路径/分支/提交=/Applications/ChatGPT.app 26.820.60940；内嵌 codex-cli 0.150.0-alpha.8
预期产品特性=Remote Control host enrollment、pairing、WSS、stream multiplex、ACK/cursor/reconnect、普通 app-server JSON-RPC connection 投影
```

### 0.4 2026-08-28 成功先例补强修订来源

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=224a632e032aea913c78223b7d2231ffa78f39db
未提交状态=修改 docs/2026-08-26-codex-remote-backend-implementation-plan.md（本轮方案补强）；修改前工作区干净
任务预期分支=用户明确指定上述 MacBridge docs 文档；该绝对路径当前唯一对应 main 工作树
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios / main / d0762cb9a05997b615ef4589f39afad8f4b4db04（仅核对元数据，工作区干净，本轮不读取或修改 iOS 产品源码）
预期产品特性=补强 codex-remote 的宿主/controller Gate、空目录与来源隔离、source-first 修复顺序、controller ownership 和 iOS 接线防漏纪律；不改变既定产品拓扑
```

本轮借鉴输入为同一 MacBridge `main` 工作树内的
`docs/2026-08-21-codex-web-backend-design.md` 与
`docs/2026-08-18-opencode-web-backend-design.md`。本轮未重新核验 Codex 外部协议 shape；§3 与 §14 的
既有外部协议结论仍归属于其各自记录的上游/App/binary 来源，新增内容只约束实施顺序与证据门。

实施、评审、构建或安装前必须按双仓 P0 规则重新生成来源清单；本文记录不能替代未来复核。

## 1. 路线裁决

### 1.1 新增平级 backend，不直接修改 codex-web

新增：

```text
agent/codex-remote/
BackendID = codex-remote
WireKind  = codex-remote
iOS kind  = BackendKind.codexRemote
建议显示名 = Codex Desktop
```

保留：

```text
agent/codex-web/
BackendID = codex-web
用途 = 官方 local daemon / WebSocket-over-UDS；CLI TUI、未来可能附着 daemon 的 IDE/宿主
```

原因：

1. `codex-web` 已完成并有真实 daemon fixture、SSV2、交互和真机证据，不应用尚未证明的 controller
   认证路径污染其生命周期；
2. local daemon 与 Desktop Remote environment 是不同宿主、不同认证、不同连接身份和不同故障域；
3. 两条路径需要并行 A/B、独立诊断和快速回滚；
4. 当前 Desktop 更新改变 daemon attach 决策，不代表 daemon 对 TUI/IDE/独立主机失去价值。

### 1.2 不立即完整复制 codex-web

Phase 0 只实现隔离证据探针，不创建完整产品 backend。探针必须先证明 controller enrollment、
environment 发现、WSS initialize、Desktop live turn 和 interrupt。最难的未知在认证/设备密钥与官方
controller API，而不是 Thread/Turn/Item 翻译；在这条链未通过前完整复制会制造大量无可用 transport
的重复代码。

Phase 0 PASS 后允许从冻结提交复制 `codex-web` 的 transport-neutral 上层，形成独立 `codex-remote`
首版。复制必须逐文件白名单、记录来源提交和差异理由。两条 backend 稳定后再抽公共核心，禁止为了
提前“去重”同时重构已经工作的 `codex-web`。

### 1.3 成功先例的正确迁移方式

从 `dsh-web`、`opencode-web`、`codex-web` 迁移的第一原则不是 adapter 文件布局，而是：

> **先证明目标 ChatGPT Desktop 私有 app-server 已通过官方 Remote Control 数据面接纳 MacBridge
> 这个独立 controller，再把 CordCode 作为该 Remote stream 上的第二个产品客户端接入。**

这项证明必须同时包含：

1. **宿主身份**：收到的 stream 确实属于用户选中的 Desktop environment 和它当前持有的私有
   app-server，而不是同账号历史、standalone app-server、fake relay 或另一个 Remote host；
2. **实时同一性**：Desktop 创建的唯一测试 thread/turn 能被 MacBridge 实时收到，MacBridge 对该
   active turn 的 `turn/interrupt` 能由同一个官方 app-server 仲裁并在 Desktop 收口；
3. **controller 共存性**：MacBridge 的 enroll/pair/connect 不得静默挤掉、撤销或冒充 ChatGPT iOS
   的 controller；若上游实际是 single-owner/HTTP 409 模型，必须在 Phase 0 暴露并重新裁决产品语义。

只有这条宿主/controller 拓扑 PASS 后，才能借鉴模块分层、事件泵、session binding、SSV2、capability
和 iOS 接线。不能先完成一个面向自有 app-server、fake relay 或历史轮询的完整 adapter，再把真实
Desktop controller 接线留到产品末期；这种实现即使单测全绿，也只证明翻译器或替代宿主可用。

### 1.4 代码来源隔离与空目录原则

`agent/codex-remote/` 是本方案的唯一 backend 实施目录。Phase 0 探针与产品入口在同一目录内按子目录
和注册状态隔离：Phase 0 可以从空目录创建 probe、脱敏 fixture 和验证工具，但不得注册产品 backend，
也不得提前堆完整 adapter；进入 Phase 1 后才在该目录增加最小 controller Transport 竖切：

> **禁止 `cp -R agent/codex-web agent/codex-remote` 后批量改名，也禁止 import、包装或 type alias
> `agent/codex` / `agent/codex-web` 的 Agent、lifecycle、transport、session store 或 ownership 状态机。**

§5.3 的“允许复制”是 Phase 0 PASS 后的逐文件白名单迁移，不是目录复制豁免。每个文件仍须先删除
不成立的 daemon/UDS 假设，再用 Remote envelope 内的真实 app-server payload 重证。参考源的职责固定为：

| 参考源 | 可以借鉴 | 不能用来证明 |
|---|---|---|
| ChatGPT App controller call site + 目标版本脱敏 fixture | enroll/refresh、device-key proof、environment 绑定、controller WSS 与错误语义 | bridge-v1、SSV2、iOS 产品行为 |
| Codex 官方 host/server/protocol/reducer | app-server JSON-RPC、thread/turn/item、server request、host Remote transport | 闭源 controller wire 中尚未取样的字段、时序或多 controller 规则 |
| `agent/codex-web/` | transport-neutral RPC/reducer、bridge 映射、SSV2/pathless、CordCode 接线骨架 | controller 认证、Remote envelope、environment 寻址、daemon 广播等价性 |
| 旧 `agent/codex/` 与其他 backend | `core` interface、注册/descriptor/iOS 接线索引、历史故障清单 | Remote API shape、event ordering、session ownership 或 fallback 语义 |

`codex-remote` 的定义是：

> **OpenAI Remote Control controller client + app-server virtual Transport + bridge-v1 协议翻译器。**

session、history、turn、interaction 和 live 状态以目标 Desktop app-server 经官方 Remote stream 提供的
API/事件为唯一数据面。禁止用 rollout/JSONL/SQLite/file tail、同账号历史轮询或另起 standalone
app-server 补造 Remote 实时事实。

### 1.5 Source-first 证据与修复顺序

每项能力固定按以下优先级建立；低优先级材料不能覆盖高优先级事实：

1. 目标版本 ChatGPT App 的 controller 真实 call site；
2. Codex 官方 host/server/protocol/reducer 与对应测试；
3. 同版本 Desktop ↔ OpenAI relay ↔ controller 的真实脱敏时间线和 fixture；
4. app-server payload → bridge-v1/SSV2 的显式映射与 replay test；
5. `codex-web`/其他 backend 仅作 CordCode 架构和历史回归参考。

完整证明元组为：

```text
controller call site
  + host/server/protocol source
  + 目标 binary 的真实脱敏 fixture
  + 明确 bridge-v1/SSV2 映射
  + 真实 Desktop 最小双向竖切
  + 定向 replay/integration test
```

缺任一项只能标记 `unverified` 或 `EVIDENCE-ONLY`，不得靠复制 `codex-web`、增加历史 fallback 或扩大
adapter 代码量继续放行。实施期发现 auth、pairing、stream、history、interaction 或重连问题时，先并排
采集官方 controller 与 MacBridge 的同场景时间线，找到**第一处分歧**再修；一次修复未改变真实
Desktop 现象，就停止叠补丁并回到官方调用链重新定位。

### 1.6 实施目录与既有 backend 零修改边界

Phase 0–Phase 4 默认必须满足：

```text
主要新增实现 = agent/codex-remote/**
agent/codex-web/** = 零修改
agent/codex/** = 零修改
```

产品接线确实需要改动 `go-bridge/`、Mac App、权威 protocol pack 和 iOS 时，只允许围绕新增
`codex-remote` identity/capability 做可独立回滚的加法；不得借机重构、修补或改变 `codex-web`、旧
`codex` 的生命周期、事件、缓存、session 或用户可见行为。

§5.3 的白名单复制方向只能是“从冻结来源读取 → 新文件写入 `agent/codex-remote/`”。复制源文件保持
不变，后续修复也不自动双写。每个 Phase 的回归门必须包含：

```bash
git diff --exit-code -- agent/codex-web agent/codex
```

若实施中发现真正属于既有 backend 的共享缺陷，保留证据并另立独立任务；不得把它夹带进
`codex-remote` 交付。§5.5/Phase 5 的公共核心抽取不属于本计划默认执行范围，只有两个 backend 都有
真实 E2E 和稳定观察窗、且 owner 明确另行授权后才可启动。

## 2. 目标拓扑与信任边界

### 2.1 产品数据路径

```text
ChatGPT Desktop UI
        │ stdio
        ▼
Desktop 私有官方 app-server
        │ host WSS：OpenAI Remote Control server transport
        ▼
OpenAI secure relay
        │ controller WSS：client/environment/stream 多路复用（概念示意，非 envelope 字段表）
        ▼
MacBridge codex-remote
        │ bridge-v1
        ├──────── LAN ────────┐
        └── CordCode HPKE Relay ─┤
                                 ▼
                         CordCode iPhone/iPad
```

两层 Relay 职责严格分开：

| 区段 | 数据面 | 身份与加密 owner | 是否可替换 |
|---|---|---|---|
| Desktop app-server ↔ MacBridge | OpenAI Remote Control | ChatGPT account/workspace、controller device key、短期 remote-control token | 当前不可替换；未修改 Desktop 只连接官方允许的 Remote host |
| MacBridge ↔ iOS | CordCode LAN/HPKE Relay | CordCode 配对、Bridge capability、现有 Relay 加密 | 保持不变 |

MacBridge 不向 iOS 暴露 OpenAI token、controller token、device key、environment 原始认证载荷或
app-server endpoint。CordCode Relay 只看既有加密 bridge 帧，不成为 OpenAI Remote broker。

### 2.2 为什么一期不能使用自有 Remote broker

当前官方 app-server：

1. 以 `config.chatgpt_base_url` 生成 `/wham/remote/control/server/*` 与 host WSS；
2. 生产 URL 只允许 HTTPS `chatgpt.com`/子域及 staging；另允许 localhost 供开发测试；
3. host enrollment 必须使用 ChatGPT authentication 与 account id，API key 不支持；
4. ChatGPT Desktop 的私有 app-server 启动参数和 Remote host transport 由官方宿主控制。

因此自建兼容 broker 只有在官方 Desktop 能显式选择它时才有产品意义。以下方案一期禁止：

- 修改或重签 ChatGPT App；
- 用 `chatgpt_base_url=localhost` 代理全部 ChatGPT API，仅劫持 Remote 路径；
- DNS、证书 MITM、动态库注入或进程劫持；
- 把 standalone app-server 接入自有 broker后宣称已经接入 Desktop 私有 runtime；
- 把 Codex rollout/SQLite/file tail 包装成 Remote live transport。

代码可预留 `RemoteControlBroker` 抽象，但一期只实现经证据证明的 `OpenAIRemoteBroker`。未来官方若
支持独立 `remote_control_base_url`、本地 peer transport 或第三方 controller SDK，再另案增加
`CordCodeRemoteBroker`。

## 3. 官方事实基线

实施时必须从以下源建立逐项 call-site + protocol + 真实样本证明：

| 能力 | 最新上游位置 | 已知事实 |
|---|---|---|
| app-server 同时启动本地与 Remote transport | `codex-rs/app-server/src/lib.rs` | stdio 私有 app-server 默认可按持久化设置启动 Remote Control |
| host enroll/refresh/pair | `codex-rs/app-server-transport/src/transport/remote_control/{server_api,enroll}.rs` | 返回 server/environment identity 与短期 host token |
| host WSS | `.../remote_control/websocket.rs` | 主机主动出站，支持 reconnect cursor、ACK、分片和 token refresh |
| relay envelope | `.../remote_control/protocol.rs` | JSON-RPC 外包 client/stream/seq envelope |
| Remote → 普通 app-server connection | `.../remote_control/client_tracker.rs` | initialize 建立 `ConnectionOrigin::RemoteControl`，后续消息进入普通 `IncomingMessage` |
| runtime control API | `codex-rs/app-server/src/request_processors/remote_control_processor.rs` | enable/disable/status/pair/client list/revoke |
| CLI/daemon | `codex-rs/cli/src/remote_control_cmd.rs`、`codex-rs/app-server-daemon/` | 实验性 start/stop/pair 与 remote-control enabled daemon |
| controller 客户端 | 当前 `/Applications/ChatGPT.app/Contents/Resources/app.asar` | `/codex/remote/control/client/*` 用于 controller enroll/refresh 与 expected-path 校验；`/wham/remote/control/*` 用于 client pairing/list/MFA 等 ChatGPT API；device-key challenge/proof 与短期 controller scope token |

当前 Desktop 包只读审计不是稳定公开 SDK。所有从 `app.asar` 得出的 client wire shape必须标记
`experimental/private implementation detail`，用目标版本真实脱敏捕获冻结，版本变化时 fail closed。

### 3.1 源码与出货二进制版本基线

上表的开源实现核验于 Codex `main@25a6e316c81fb7600d1d75f3e63ffe26be10b7c8`。当前出货
ChatGPT App 内嵌 `codex-cli 0.150.0-alpha.8`。第二轮评审已完成真实上游 fetch 与定向 diff：

```text
tag=rust-v0.150.0-alpha.8
annotated tag object=4111e744（评审记录）
peeled commit=fcbdb57851be70192fd0c21faa9e529146e93ff1
HEAD=25a6e316c81fb7600d1d75f3e63ffe26be10b7c8
HEAD ahead=107 commits
```

在 `app-server-transport/src/transport/remote_control/`、remote-control processor/protocol、
`cli/src/remote_control_cmd.rs` 与 `app-server-daemon/` 的定向范围内，alpha.8 到 HEAD 只有两处测试
fixture 各增加一行 `bedrock_access_keys: None`；生产实现无差异。因此 §3 表中的 host 侧源码事实对
当前出货 alpha.8 的**源码基线**成立，先前“无法定位 alpha.8”的风险已排除。

该结论不等于出货二进制身份证明：App 可能含构建差异或内部补丁，controller 侧仍主要位于
`app.asar` 与闭源 relay。Phase 0 仍必须：

1. 记录 alpha.8 tag/peeled commit、HEAD、107 提交距离和两行测试-only diff，保存可复跑命令与输出；
2. 对 app.asar、内嵌 binary 的真实脱敏 fixture 与行为采样赋予最高优先级；源码、schema、注释与
   binary fixture 冲突时，以目标 binary fixture 为当前兼容合同，并回写差异；
3. 同时记录 Desktop App 版本、内嵌 Codex 版本、协议版本和样本生成时间，任一变化触发重新冻结；
4. 未来目标版本若 tag 缺失，恢复“尝试 fetch；不可定位则明确记录并以 binary fixture 为准”的通用
   程序，不得拿邻近 tag 或 HEAD 冒充目标版本。

## 4. 认证与设备密钥方案

### 4.1 目标认证流程

```text
用户选择“连接 ChatGPT Desktop”
  → MacBridge 发起显式 ChatGPT account/workspace 授权
  → 必要时浏览器完成 step-up / MFA / SSO / passkey
  → MacBridge 在 macOS Keychain 创建独立非导出设备密钥
  → 使用 QR 或 manual pairing code 绑定目标 Desktop environment
  → 完成 controller enroll challenge/proof
  → 获得短期 remote_control_controller_websocket token
  → 建立 WSS，并在连接 challenge 中再次用设备密钥签名
```

用户必须能在 MacBridge 中查看：绑定账号/工作区的非敏感标识、environment 显示名、在线状态、最近
连接时间，以及“断开并撤销”动作。登出、revoke、workspace policy 禁用或 token 失效必须立即停止
连接并呈现真实错误。

### 4.2 凭据红线

生产实现禁止：

- 扫描、复制、导出或长期保存 ChatGPT Desktop/Codex 的原始 access/refresh token；
- 把 `~/.codex/auth.json`、ChatGPT Keychain 项或 bearer token 发往 CordCode Relay/iOS；
- 在日志、diagnostics、crash report、fixture 中记录 token、pairing code、MFA/step-up token或私钥；
- 把 API key 当作 ChatGPT Remote authentication；
- 静默冒用另一个 App 的登录态，使用户无法单独撤销 MacBridge controller。

### 4.3 Phase 0 认证探针的受控例外

若公开/正式授权入口不足，Phase 0 可以验证“复用本机 Codex ChatGPT 登录态作为 enroll 引导”是否
技术可行，但必须满足：

1. owner 在当前实验中明确授权；
2. 优先调用官方认证加载/刷新逻辑或受控 helper，不自行遍历凭据文件；
3. token 只在本机内存中使用，不输出、不落盘、不转发；
4. 只用于换取独立 controller enrollment；成功后 controller 依赖自己的 device key/token；
5. 实验结果不得自动升级为生产合同；若无法建立独立可撤销身份，Gate 判定 BLOCKED。

“CCSwitch 能读取 Codex 配置/凭据”不能证明 Remote controller 可以只靠 bearer token工作；controller
还必须满足 step-up、device-bound key、scope token 和连接 challenge。

## 5. 包结构与代码复用边界

### 5.1 Phase 0 探针

从空目录建立以下 Phase 0-only 结构；此时不注册 backend：

```text
agent/codex-remote/
  README.md                # 明示 Phase 0-only、Gate 状态与清理/revoke步骤
  probe/                   # 非产品入口；不注册 backend
  testdata/phase0/         # 只允许脱敏 fixture/meta
  validate/                # envelope、顺序、秘密扫描与版本检查
```

探针不得调用 iOS、修改 ChatGPT App、写 `config.toml`、替换系统代理或更改现有 daemon/LaunchAgent。
所有网络和凭据动作必须有可回收 identity、超时、日志脱敏和 revoke 收尾。

### 5.2 Phase 0 PASS 后的产品目录

```text
agent/codex-remote/
  codexremote.go          # backend 注册、identity、依赖组装
  lifecycle.go            # auth/pair/environment/online/revoke 状态机
  auth.go                 # 显式授权与短期 token，零原始 token外泄
  device_key_darwin.go    # Keychain/Secure Enclave 可用性与签名
  enrollment.go           # enroll/refresh/step-up
  remote_protocol.go      # controller envelope、ACK/cursor/chunk、Pong、ClientClosed
  transport.go            # controller WSS 与 stream 虚拟 Transport
  rpc.go                  # app-server JSON-RPC correlation/epoch
  events.go               # agent-level event pump
  sessions.go
  catalog_thread_list.go
  history.go
  session.go
  turn.go
  codec.go
  interactions.go
  userinput.go
  models.go
  permission_modes.go
  context_usage.go
  diagnostics.go
  wire_descriptor.go
  testdata/official-<desktop-app-version>-<codex-version>/
```

### 5.3 允许从 codex-web 复制的白名单

仅在 Phase 0 PASS 后，从明确冻结的 MacBridge 提交逐文件复制并改写 provenance：

- `rpc.go`：保留 JSON-RPC correlation、server request、epoch、bounded shutdown；底层 Transport 改为
  Remote stream；
- `codec.go`、`events.go`：保留 Thread/Turn/Item reducer 与 bridge 事件映射，删除所有“daemon 广播”
  的来源假设并用真实 Remote fixture重证；
- `catalog_thread_list.go`、`history.go`、`sessions.go`、`session.go`、`turn.go`；
- `interactions.go`、`userinput.go`、`models.go`、`permission_modes.go`；
- `context_usage.go`：仅在同一官方 `Thread.path` 豁免和版本门仍成立时复制；
- transport-neutral 单测与官方 app-server payload fixture。

每个复制文件必须：

1. 记录来源仓库提交、原文件和复制日期；
2. 删除/改写 shared-daemon、UDS、同 socket、多 connection 广播等不再成立的注释；
3. 增加 Remote envelope 解包后 app-server payload parity test；
4. 不用“代码相同”代替 Remote 真实样本；
5. 后续 bug fix 不自动双写，直到公共核心抽取门通过。

### 5.4 禁止复制的 codex-web 部分

- `lifecycle.go` 中 daemon socket 探测/start/fail-closed 路径；
- `lifecycle_managed.go` 与 managed server 清理语义；
- UDS/proxy/TCP endpoint 选择；
- LaunchAgent 与 `CODEX_APP_SERVER_USE_LOCAL_DAEMON` 注入；
- standalone/Desktop CLI 版本配平作为连接前提；
- `shared-daemon-required`、`external-daemon-reused`、`cordcode-started-daemon` 状态；
- Desktop 必须无私有子进程、FD 必须指向 daemon socket 的拓扑断言；
- 把 daemon connection #2 广播行为直接类推成 Remote stream 行为的测试。

### 5.5 后续公共核心抽取门

本阶段不属于 `codex-remote` 默认实施范围。只有 `codex-web` 与 `codex-remote` 都完成真实 E2E、稳定
一个观察窗，并由 owner 明确另行授权后，才允许另案抽取：

```text
agent/codex-appserver/
  rpc/
  codec/
  sessions/
  interactions/
  models/
```

抽取必须保持两个 backend 的 wire identity、transport epoch、capability 和 diagnostics 独立。不得为了
减少重复，把 lifecycle、认证或连接状态揉成一个带大量条件分支的 backend。

## 6. Remote transport 设计

### 6.1 两级连接对象

`codex-remote` 需要区分：

1. **controller WSS**：MacBridge 到 OpenAI relay 的长连接，绑定 `client_id`；
2. **目标 environment 绑定与 app-server stream**：MacBridge 必须选择一个 Desktop environment，并为
   app-server initialize 建立独立 `stream_id` 与 `ConnectionEpoch`；但 controller 如何在 REST、WSS
   握手、连接级状态或控制消息中指定/切换 `environment_id`，属于闭源 relay 契约，当前不得预设。

已证实的 host 侧 `ClientEnvelope`/`ServerEnvelope` 字段全集不含 `environment_id`。Phase 0 必须用真实
fixture 证明 environment 绑定发生在哪一层，才能冻结“一个 controller WSS 对应一个或多个 environment”
以及 stream 的实际复用模型。

上层现有 `Client` 只看普通 JSON-RPC Transport；Remote 层负责 envelope：

```text
JSON-RPC request/response/notification/server-request
    ↕
client_message / server_message
    + client_id
    + stream_id
    + seq_id
    + cursor / ack / chunk metadata
    ↕
controller WSS

environment 选择/绑定
    = 闭源 relay 契约，Phase 0 fixture 待证明
```

不得把 controller WSS 的 reconnect 等同于 app-server stream continuity。发生序列缺口、环境离线、
token刷新失败或 stream 被服务端关闭时，必须关闭旧 epoch，重新 initialize，并按 SSV2 规则执行
`thread/read(includeTurns:true)` 冷校准。

### 6.2 必须实现的 transport 语义

- client/server envelope schema validation；
- per-stream 单调 `seq_id`；
- ACK 与未确认发送缓冲；
- reconnect cursor；host 侧已证实通过 `x-codex-subscribe-cursor` handshake header 递交，controller
  侧的 cursor 位置与时序必须由 fixture 冻结；
- payload 分片/重组及上限；已证实 host 常量为 target 100KB、单段最大 150KB、单消息重组最大
  100MB、最多 1024 段、最多 128 个并发重组；controller fixture 若不同，以目标 binary 为准；
- `Ping`、`ServerEvent::Pong{status: Active|Unknown}`、`ClientClosed` 与空闲 stream 回收；已证实 host
  空闲阈值 10 分钟、sweep 30 秒，必须落入边界测试；
- `client_id`/`stream_id` mismatch 立即断线；environment 绑定 identity 的校验位置待 Phase 0 fixture
  证明后补入，不把它伪装成 envelope 字段；
- device-key WebSocket challenge/proof；
- controller enroll/refresh 两步端点已证实；“controller token 到期前主动刷新”的具体调度仍是
  `assumption pending Phase 0 fixture`，未取样前不得写死提前量或退避；
- bounded queue、背压、超时与明确终止；
- unknown envelope/type 诊断但不泄漏 payload；
- app-server JSON-RPC 层保持原始 request id、server request id 与错误对象。

### 6.3 environment 选择

一期只允许用户显式选择已经配对、在线的 ChatGPT Desktop host。禁止仅按 hostname 猜测目标。持久化：

- controller `client_id`；
- environment id；
- 非敏感显示名/设备类型；
- 最近成功连接时间；
- 绑定的 account/workspace 非敏感 identity；
- device key Keychain reference。

环境离线、Desktop 退出/睡眠、账号登出、workspace policy 禁用、客户端被 revoke 必须是不同错误码，
不能统一显示“Codex 未安装”。

## 7. Backend 生命周期与状态

建议状态机：

```text
not_configured
  → authorization_required
  → step_up_required
  → pairing_required
  → environment_offline | connecting
  → connected
  → reconnecting
  → token_expired | revoked | policy_disabled | protocol_incompatible | errored
```

关键规则：

1. `connected` 必须同时证明 controller WSS、目标 environment 在线、stream initialize 成功；
2. environment 列表可见但未建立 stream 不得广告 session/turn capability；
3. Desktop 使用私有 stdio app-server 是本 backend 的正常宿主事实，不再标为 split failure；
4. shared daemon 是否健康只影响 `codex-web`，不得覆盖 `codex-remote` 状态；
5. Desktop Remote Control 未开启时显示可操作的 `pairing_required`/`remote_disabled`，不自动修改
   Desktop 设置；
6. revoke/登出必须清理内存 token、关闭 WSS/streams，并删除本 backend 自己的 enrollment state；
7. 不删除 ChatGPT Desktop、Codex CLI 或 `codex-web` 的认证/daemon 状态。

## 8. 分阶段实施与硬门

### Phase 0：Remote controller 证据探针

目标：不接产品 backend，证明官方链路可以被 MacBridge 合规接入。

任务：

1. 冻结当前 Desktop/App/内嵌 Codex/上游 commit；把 §3.1 已证实的 alpha.8 tag、peeled commit、
   HEAD 领先 107 提交、定向范围仅两行测试 fixture 差异写入 Phase 0 evidence，并复跑命令确认引用未漂移；
2. 完成 host/server 与 controller/client call-site 索引；controller 同时索引
   `/codex/remote/control/client/*` 与 `/wham/remote/control/*` 两个路径族，记录各自 base、用途和版本；
3. 冻结 REST enroll/refresh、WSS handshake/challenge 和 envelope 脱敏 fixture；必须明确证明
   controller 如何指定/切换目标 environment，并捕获重连时 cursor 的实际递交位置；
4. 建立临时 controller device key 与可撤销 enrollment；
5. 列出已配对 environment，选中当前 Desktop；
6. 用 Desktop 创建带唯一标记的测试 thread/turn，证明 environment/stream 属于目标 Desktop 当前私有
   app-server；同账号历史、standalone app-server 或 fake relay 不算宿主身份证明；
7. 在 ChatGPT iOS controller 已配对并连接的状态下再连接 MacBridge，记录是否支持并发 controller、
   是否返回 single-owner/HTTP 409、是否踢掉既有 controller，以及断线后的 ownership 恢复语义；
8. WSS 建连，发送 app-server `initialize`/`initialized`；
9. `thread/list`/`thread/read`；
10. Desktop 发起长 turn，探针实时收到 `turn/started`、多帧 item delta、唯一 `turn/completed`；
11. 探针对同一 active turn执行 `turn/interrupt`，Desktop 与探针看到一致终态；
12. 主动断网/重连一次，验证 seq/ACK/cursor 与冷校准边界；
13. revoke controller，证明旧 token/device连接失效且 Desktop 其他配对不受影响；
14. 对 dumps 和日志执行 secret scan。

Gate P0 PASS：上述 1–14 全部有真实证据。若只能 list/read、不能收到 live/interrupt，判
`EVIDENCE-ONLY`，不得复制完整 backend或接 iOS。若 MacBridge 连接必须静默抢占 ChatGPT iOS 的
controller，判 `EVIDENCE-ONLY` 并重新裁决产品语义，不得把“先断开官方手机端”藏成实现细节。若必须
读取并长期冒用 Desktop 原始 token、修改 App 包或代理全部 ChatGPT 流量，判 `BLOCKED`，重新打开
认证/宿主设计。

> [!IMPORTANT]
> **2026-08-28 Owner 改写 Gate P0。** 冻结目标 ChatGPT Desktop `26.825.32147` /
> Codex `0.150.0-alpha.12.2`。attempt-008 证明：独立 controller 在
> `thread/resume(excludeTurns)` 之后能收到 Desktop 当前线程的 `turn/started`、
> `item/agentMessage/delta`、`turn/completed`。官方 **仍然不** 在 controller WSS
> envelope 上交付 reconnect `cursor`。Owner 明确开始 Phase 1，接受无 cursor 的
> 首连 live 流，并把下列项列为已知缺口（产品 fail-closed，不得广告）：
>
> - 任务 6 唯一标记 thread（partial：env/stream 已绑定，未盖探针标记）
> - 任务 7 官方 iOS controller 共存 / HTTP 409
> - 任务 11 `turn/interrupt`
> - 任务 12 seq/ACK/cursor 重连
>
> 证据见 [2026-08-28-codex-remote-phase0-fail-blocked.md](2026-08-28-codex-remote-phase0-fail-blocked.md)
> 与 `agent/codex-remote/testdata/phase0/live/attempt-001`…`attempt-008`。
> 不得合入 `main`，不得提前做 Phase 3 iOS 接线，除非后续任务明确开始。

### Phase 1：codex-remote 骨架与最小 app-server Transport

> 2026-08-28：Owner 已改写 Gate P0，本节可实施。已知缺口见上，不得在 diagnostics
> 或 capability 中广告 interrupt / cursor 重连 / iOS 共存。

1. 从空目录注册独立 backend identity；
2. 实现 auth/device key/enrollment/environment/controller WSS；
3. 将一个 environment stream 实现为现有 app-server `Transport` 等价接口；
4. 复制并改写最小 `rpc.go`；
5. 只接 `initialize`、`thread/list/read`、`thread/start/resume`、`turn/start/interrupt` 和文本 delta；
6. diagnostics 明确显示 remote host/environment/protocol/app-server version；
7. `codex-web` 零行为变化测试。

Gate P1 PASS：MacBridge 自动测试 + 真实 Desktop 双向最小竖切；不接 iOS 完整产品面。

### Phase 2：上层 adapter 复制与 parity

按 §5.3 白名单复制：

- catalog/history/SSV2；
-完整 Thread/Turn/Item codec；
- steer/interrupt/reconnect；
- approval/requestUserInput/MCP elicitation；
- models/config/permission profiles；
- archive/rename/delete/fork 等逐项 capability。

每项必须有：官方 app-server payload fixture + Remote envelope fixture + bridge 映射 + 定向测试。
未知或未取样 capability fail closed，不从 `codex-web` 广告表直接继承。

### Phase 3：iOS 独立产品接线

实施前重新解析唯一双仓配套工作树。新增：

- `BackendKind.codexRemote`；
- 独立 wire/backend/cache/SSV2 identity；
- 显示名“Codex Desktop”；
- 授权、配对、environment 离线/撤销/协议不兼容状态；
- capability 驱动的 session、history、turn、interaction UI；
- 与 `codexWeb` 并列，不合并同 thread id 的缓存。

实施时必须把 iOS 接线分成两类审计：编译器可发现的 `switch` 穷举，以及不会报编译错误的
`if ==`、`guard`、字符串 kind、backend allowlist/denylist、cache scope、SSV2/hydrate/reconnect 名单。
后者必须用 `rg` 全量扫描并逐处记录“加入 / 不加入及理由”；禁止机械追加
`|| backend == .codexRemote`。每一处归组由 Remote 真实 capability 和 identity 决定，不能因为
`codexWeb` UI 相似就继承其 daemon/目录/缓存语义。

这是 D3 状态/协议改动：只跑直接相关测试组，交付前按来源门执行一次连接真机的增量构建、安装和启动；
UI tests、snapshot tests、模拟器自动化和真机自动点击仍需 owner 明确授权。

Gate P3 PASS：owner 按一次性测试矩阵验证 Desktop ↔ iPhone 双向实时接力、取消、审批/提问、LAN/Relay、
断网恢复与两 backend 并存。

### Phase 4：拓扑监控与产品迁移

1. 原 `split_present` 不再单独等价于产品失败；
2. 分别显示 `codex-web local-daemon` 与 `codex-remote desktop-environment` 健康度；
3. Desktop 私有 stdio + codex-remote connected 是 Remote 路径 healthy；
4. Desktop shared daemon + codex-web connected 仍是 local-daemon 路径 healthy；
5. 两者均可用时不自动合并 backend或 session identity；
6. 观察窗内保留 `codex-web` 和回滚入口；未经 owner 单独裁决不退役任何 backend。

### Phase 5：公共核心收敛（独立后续）

满足 §5.5 后审计真实重复，再抽 `codex-appserver` 公共包。该阶段不是 `codex-remote` 首次交付门，
不得为了代码洁癖阻塞产品验证，也不得留下永久无主的双份 reducer。没有 owner 对该独立任务的明确
授权时，不进入本阶段，`agent/codex-web/` 与 `agent/codex/` 继续保持零修改。

## 9. 测试与证据矩阵

### 9.1 自动化测试

| 层 | 必测项 |
|---|---|
| auth | account/workspace mismatch、step-up required、token refresh、logout、revoke、policy disabled；秘密零日志 |
| device key | create/sign/delete、challenge字段/目标 origin/path/token hash校验、错误 key 拒绝 |
| environment binding | 真实 fixture 证明 environment 在 REST、WSS handshake、连接状态或控制消息中的绑定/切换方式；禁止预设为 envelope 字段 |
| envelope | client/stream/seq、ACK/cursor、分片、重复/乱序/缺口、oversize、unknown type、Ping/Pong Active/Unknown、ClientClosed |
| transport | WSS reconnect、stream reconnect、epoch、背压、超时、bounded shutdown |
| RPC | initialize、并发 response、notification、server request、错误原样保留 |
| parity | 同一 app-server fixture 经 UDS 与 Remote 解包后的 codec/SSV2 输出一致；仅比较协议语义，不假装宿主相同 |
| lifecycle | offline/sleep/app closed、remote disabled、revoked、incompatible、重新配对 |
| controller ownership | ChatGPT iOS 已连接时 MacBridge enroll/connect；并发、409、踢出、重连与 revoke 语义均有真实证据，禁止静默抢占 |
| coexistence | codex-web daemon故障不影响 codex-remote；Remote故障不停止/重启 daemon |
| provenance | `agent/codex-remote` import 图不含旧 `agent/codex` 或 `agent/codex-web` ownership/lifecycle；首版审查证明不是目录 copy/rename；白名单复制逐文件含来源与 Remote parity test；Phase 0–4 的 `git diff --exit-code -- agent/codex-web agent/codex` 必须通过 |
| security | secret scan、日志脱敏、Keychain ACL、token不进入 bridge/iOS/fixtures |

禁止用 fake relay 证明外部 client API 形状；fake 只用于已由真实样本冻结后的本地错误分支和时序回归。

精确边界断言至少包含：host 分片/重组常量（target 100KB、最大 150KB、重组最大 100MB、最多
1024 段、最多 128 个并发重组）、host 空闲回收常量（10 分钟、sweep 30 秒）、host token refresh
失败退避（24–36 秒），以及 host reconnect 的 `x-codex-subscribe-cursor` header。这四类均进入上游
基线/兼容性测试；其中 refresh 退避明确不是 controller 调度合同，分片、空闲和 cursor 也不自动
类推 controller wire。controller 侧任何不同值或位置均以目标 App/binary fixture 为准。

### 9.2 真实 Desktop Gate

| # | 前提条件 | 动作 | 必须观察到 |
|---|---|---|---|
| 1 | Desktop Remote 已开启并配对；MacBridge controller connected | Desktop 创建 session并发送长回答 | codex-remote 实时收到同 thread start/delta/completed，不靠 history轮询 |
| 2 | 同一 session 双方保持打开 | iPhone 续聊 | Desktop 原会话实时显示并继续；无 active writer冲突 |
| 3 | Desktop active turn | iPhone 取消 | 官方 turn interrupted；双方唯一终态 |
| 4 | Desktop 请求审批/提问 | iPhone 作答 | response回原 server request；Desktop 正常继续并收口 |
| 5 | MacBridge controller 暂时断网 | 恢复网络 | cursor/ACK 或 stream重建后无重复；冷校准与 live identity合并 |
| 6 | Desktop 退出或 Mac 睡眠 | iPhone 打开 backend | 明确 environment offline，不显示假历史实时态 |
| 7 | 撤销 MacBridge controller | 尝试重连 | 旧身份被拒绝；重新授权/配对路径明确 |
| 8 | codex-web daemon 同时运行 | 两 backend 分别发起独立测试 session | 互不停止、互不改配置、identity/cache不串扰 |
| 9 | CordCode LAN | 执行 1–4 | 功能与状态完整 |
| 10 | CordCode HPKE Relay | 执行 1–4 | 与 LAN 语义一致，无 token 泄漏 |
| 11 | ChatGPT iOS 已连接同一 Desktop Remote environment | 连接/断开/revoke MacBridge controller，并分别从两端观察 | 官方 controller 不被静默踢出或撤销；若上游只允许 single-owner，产品明确阻断并呈现真实状态，不伪装并存成功 |

## 10. 安全与隐私验收

交付前必须证明：

1. MacBridge拥有独立、可撤销的 controller identity；
2. 私钥不可导出或至少只存在于 macOS Keychain，并设置合理访问控制；
3. ChatGPT/Codex 原始 access/refresh token不被复制到 MacBridge持久化；
4. controller token、pairing code、step-up token不进入日志、diagnostics、crash、fixture、iOS 或 Relay；
5. WSS 只连接证据冻结的官方 HTTPS/WSS origin；重定向和 challenge target严格校验；
6. environment/client identity mismatch fail closed；
7. 账号登出、workspace切换、revoke 后立即断开；
8. MacBridge卸载/重置只删除自己的 key/enrollment，不破坏 Desktop/CLI；
9. repo、产物、dumps、日志执行 secret scan；
10. 对私有/实验 API 的兼容边界和用户提示诚实，不宣传为公开稳定 SDK。

## 11. 风险、阻断与取舍

| 风险 | 影响 | 处置 |
|---|---|---|
| controller API 未公开且随 App 更新变化 | 连接突然失效 | Desktop+Codex版本门、真实 fixture、unknown fail closed、保留 codex-web |
| 无正式第三方 OAuth/step-up入口 | 无法合规 enrollment | Phase 0 提前阻断；不以偷读 token包装完成 |
| device-key proof依赖 Desktop 私有实现 | 签名或 scope 漂移 | call-site+真实 challenge冻结；Keychain独立实现；版本不匹配不连接 |
| 双 Relay 增加延迟和公网依赖 | LAN也需 OpenAI relay 才能进 Desktop | 指标拆分两段延迟；诚实显示 offline；不声称纯本地 |
| Mac睡眠/Desktop退出 | Remote environment离线 | 复用官方 keep-awake/在线语义；不伪造后台可用 |
| workspace policy/MFA | 部分账号不可用 | 显式 policy/step-up状态；不降级到账号绕过 |
| controller single-owner/HTTP 409 | MacBridge 可能挤掉 ChatGPT iOS 或无法连接 | Phase 0 在官方手机端已连接时实测；禁止自动抢占；不能共存则重新裁决产品语义并 fail closed |
| 两 backend thread 同名 | UI混淆或缓存串扰 | BackendID纳入所有 identity；显示宿主来源；不按 threadId跨 backend合并 |
| 复制上层形成长期分叉 | bug fix漂移 | 来源标记、parity测试、观察窗后公共核心抽取门 |

## 12. 回滚与删除边界

- `codex-remote` 在独立 driver/feature flag 后启用；关闭它不得改变 `codex-web`、daemon、Desktop设置或
  iOS既有 session；
- Phase 0–Phase 4 不修改 `agent/codex-web/` 与 `agent/codex/`；回滚不依赖还原这两个目录；
- 回滚只移除 `codex-remote` driver/BackendKind产品入口并撤销 MacBridge自己的 controller enrollment；
- 不删除用户 Desktop配对、Codex认证、daemon、session、SQLite或 rollout；
- 未经 owner 明确授权，不退役 `codex-web`、不恢复旧 `codex`、不合并 backend identity；
- 任何需要修改 ChatGPT App、代理系统流量或读取原始 token持久化的方案必须另开设计并获得明确授权，
  不得作为本计划的“临时 fallback”。

## 13. 完成定义

`codex-remote` 只有同时满足以下条件才可宣称完成：

1. MacBridge以独立可撤销 controller身份连接官方 Remote environment；
2. 不修改 ChatGPT App，不注入 Desktop进程，不代理全部 ChatGPT流量，不长期读取/保存原始 Codex token；
3. Desktop与 iPhone可在双方同时在线时对同一 thread双向创建、续聊、实时旁观和取消；
4. approval、requestUserInput等 server request保持原始 identity并由官方 app-server仲裁；
5. 断线、重连、睡眠、Desktop退出、登出、revoke、版本漂移均有真实可区分状态；
6. SSV2删除本地 projection 后能由官方 `thread/read` + Remote live事件重建；
7. CordCode LAN与HPKE Relay均通过同一验收语义，OpenAI凭据不离开 Mac；
8. `codex-web` 行为和测试保持不变，两 backend可并存和独立回滚；
9. Mac/iOS定向测试、真实 Desktop Gate、owner真机矩阵和secret scan全部有版本化证据；
10. MacBridge controller 不静默抢占、踢出或撤销 ChatGPT iOS controller；若上游明确只允许
    single-owner，产品状态与使用限制已单独裁决并通过 owner 验收；
11. Phase 0–Phase 4 的交付 diff 中 `agent/codex-web/` 与 `agent/codex/` 保持零修改；
12. 文档、源码注释和产品文案不再把“Desktop必须接入共享daemon”宣称为唯一成立拓扑。

在 Phase 0 认证与真实 live/interrupt Gate 通过之前，本计划只能标记“研究中”，不得用历史可读、
fake relay、共享 store 或独立 standalone app-server冒充 Desktop Remote 接力已经实现。
2026-08-28 起本计划标记为 **FAIL-BLOCKED / 按原门禁停工**，不是“研究中可继续实施”。

> [!NOTE]
> **2026-09-01 状态回填**：上一段的 FAIL-BLOCKED 是 2026-08-28 时点的历史裁决记录。Owner
> 当日改写 Gate P0（接受无 cursor 首连 live 流）后实施继续，本计划已于 2026-08-29 以
> **proved-complete** 收口：111/111 done+proven，Phase 0–5 全部 proven-done（完成报告见文首
> 链接）。改写 Gate P0 时列出的已知缺口（cursor 断线续传、interrupt、官方 iOS controller
> 共存）按设计保留 fail-closed，完成定义第 10 条的验收口径为“缺口如实呈现、不伪装并存”，
> 已随完成报告通过。

## 14. audit-plan 评审意见处置记录

### 14.1 第一轮评审

评审依据：[2026-08-26-codex-remote-backend-implementation-plan-audit.md](2026-08-26-codex-remote-backend-implementation-plan-audit.md)，
结论为“有条件通过，无 P0，1 处内容形状需修正，1 处版本基线需显式化，4 条 P2 补强”。

| 评审项 | 处置 | 回写位置 | 理由 |
|---|---|---|---|
| P1-1：envelope 图移除 `environment_id`，绑定机制待 fixture | **采纳** | §6.1、§6.2、§8 Phase 0、§9.1 | host envelope 字段全集无该字段，controller/relay 绑定属于闭源契约，原图事实错误 |
| P1-2：区分 HEAD 与出货 alpha.8，尝试 fetch tag并以 binary fixture 为准 | **采纳** | §3.1、§8 Phase 0 | remote_control 活跃演进，HEAD 不能替代更老出货二进制 |
| P2-1：controller token“到期前刷新”标 assumption | **采纳** | §6.2 | 端点已证实，但 controller 调度时序尚无真实 dump |
| P2-2：segment/idle/cursor/refresh 精确常量进入断言 | **部分采纳** | §6.2、§9.1 | segment、idle、cursor 进入 transport/兼容性断言；24–36 秒是 **host** token refresh 失败退避，只进入上游基线测试，不写成 controller 产品时序，避免跨角色类推 |
| P2-3：同时索引 `/codex/...` 与 `/wham/...` 路径族 | **采纳** | §3、§8 Phase 0 | 两族用途和 base 不同，必须分别冻结版本归属与样本 |
| P2-4：覆盖 Pong Active/Unknown 与 ClientClosed | **采纳** | §5.2、§6.2、§9.1 | 两者是已证实 wire variant，并影响空闲探测与主动关闭/重连边界 |
| Gate 补强：environment 绑定与 subscribe cursor 样本 | **采纳** | §8 Phase 0、§9.1 | 它们分别是目标寻址和断线连续性的关键未证实环节 |

未采纳部分仅为“把 host 24–36 秒刷新退避直接当作 controller 调度合同”；理由已在表内说明。
除此之外，评审提出的修正与补强全部采纳，架构路线、凭据红线和先探针后接线顺序保持不变。

### 14.2 第二轮评审

第二轮依据：[2026-08-26-codex-remote-backend-implementation-plan-audit-r2.md](2026-08-26-codex-remote-backend-implementation-plan-audit-r2.md)，
结论为“通过，可进入 Phase 0”；第一轮全部处置逐行闭环，未发现新未证实声明。

| 第二轮观察 | 处置 | 回写位置 | 理由 |
|---|---|---|---|
| P3-1：§2.1 的 client/environment/stream 是概念图，可加注脚 | **采纳** | §2.1 | 避免读者再次把架构概念误读为 envelope 字段全集 |
| P3-2：§9.1“前三组”指代可更显式 | **采纳** | §9.1 | 改为逐类命名 segment/reassembly、idle、refresh-backoff、cursor，并分别限定 host/controller 角色 |
| P3-3：回写 alpha.8 tag、107 提交、仅测试 2 行 diff | **采纳** | §3.1、§8 Phase 0 | 第二轮已用真实 fetch+diff 提前完成该门，应该升级为当前已证实输入而非继续保留未知分支 |

第二轮没有不采纳项。alpha.8 与 HEAD 的生产源码一致仅消除当前 host 侧源码漂移风险，不取消
binary fixture、controller wire、environment 绑定和闭源 relay 的 Phase 0 Gate。

## 15. 成功先例借鉴与不迁移项

本节记录从 `2026-08-21-codex-web-backend-design.md` 与
`2026-08-18-opencode-web-backend-design.md` 借鉴的施工纪律，避免实施者只复制最终代码形状。

| 先例中的原则 | 本计划处置 | 回写位置 | Remote 场景下的解释 |
|---|---|---|---|
| 先证明官方 GUI/宿主拓扑，再做完整 adapter | **采纳** | §1.3、Phase 0 | 改写为先证明目标 Desktop environment + 私有 app-server + MacBridge controller 的真实 live/interrupt 链路 |
| 新 backend 从空目录开始，旧实现只作索引 | **采纳** | §1.4、§5.3–§5.4、§9.1 provenance | 禁止 bulk copy/import/wrapper；Phase 0 PASS 后仅允许带 provenance 与 Remote parity 的逐文件白名单迁移 |
| 官方实现和同版本真实样本优先于旧 adapter | **采纳** | §1.5、§3、Phase 0 | controller 以目标 App call site/fixture 为准，host/app-server 以官方源码和 binary fixture 为准 |
| 先最小双向竖切，再扩 catalog/history/interaction/SSV2 | **采纳** | §8 Phase 0–Phase 3 | list/read 不等于实时接力；live + interrupt + ownership Gate 失败即停线 |
| iOS switch 与 `if ==`/allowlist 分开审计 | **采纳** | Phase 3 | 编译通过不能证明新 kind 已进入所有缓存、SSV2、重连和 UI 行为分支 |
| 旧 backend 并存、独立 identity、可回滚 | **采纳** | §1.1、§7、Phase 4、§12–§13 | `codex-web` 与 `codex-remote` 不共享 lifecycle/cache，不因新路线成立自动退役旧路线 |
| 新实现集中在独立目录，既有 backend 默认冻结 | **采纳并强化** | §1.4、§1.6、§5、§9、§12–§13 | Phase 0 探针和产品实现均落在 `agent/codex-remote/`；Phase 0–4 对 `agent/codex-web/`、`agent/codex/` 设零修改回归门 |
| codex-web 的“同 daemon、同 socket、Desktop 无私有 app-server” | **不采纳** | §2、§5.4、§7 | 这是 local-daemon 路线的宿主不变量；Remote 的正常宿主正是 Desktop 私有 app-server + 官方 relay |
| opencode-web 的“agent 只连接现成 URL、不负责认证/配对” | **不采纳** | §4、§6–§7 | Remote controller 必须拥有独立 device key、enrollment、pairing、refresh 和 revoke 生命周期 |
| 完全禁止复制任何已验证上层文件 | **不采纳** | §5.3、§5.5 | `codex-web` 与 `codex-remote` 解包后共享同一官方 app-server JSON-RPC 语义；允许逐文件白名单复制能保留已验证 reducer，但必须重新做 Remote fixture/parity，禁止目录复制 |
| 用 fake/沙盒服务完成现网成熟验收 | **不采纳** | §8–§9、§13 | fake 只允许在真实 fixture 冻结后测试本地错误分支；完成定义必须经过真实 Desktop、官方 relay 和 controller identity |
