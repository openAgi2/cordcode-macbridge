# OpenCode 共享本地服务接入方案

> 日期：2026-07-02
> 状态：已完成 / 正式关闭（2026-07-03）。详见下方「状态更新（2026-07-03）」。
> 目标读者：接手开发的 agent
> 前提：不修改 OpenCode / OpenCode Desktop 源码；只改 CordCode MacBridge。

## 状态更新（2026-07-03）— 必读，本方案部分已被超越

⚠️ **接手 agent 请先读本节。** 本方案写于「CordCode 不能/不应自己起 OpenCode server」的前提
下，中心结论是只能做 `external_http`（bring-your-own-server，用户手动 `opencode serve`）。
**该前提后来被推翻**：后续派生 plan 实现了 `managed_local`（CordCode 自己托管 `opencode serve`），
并成为产品默认。下面逐条标注当前真值，避免按本文档做过时编码。

### 派生关系

- 本方案 → 派生 1：`docs/2026-07-02-opencode-project-first-session-list-plan完成情况.md`
  （OpenCode 数据面 project/session 列表改造，**不在本方案范围**，是本方案假设「已落地并复用」
  的前置基础能力，已完成）。
- 本方案 → 派生 2：`docs/2026-07-03-opencode-managed-local-server-seamless-plan完成情况.md`
  （MacBridge 自己托管 `opencode serve`，**推翻了本方案 §1 的中心结论**，已完成并正式关闭）。

### 哪些结论已过时 / 被超越（勿按本文档编码）

| 本方案位置 | 原结论 | 当前真值 |
| --- | --- | --- |
| §1 结论 | CordCode 只能 `external_http`，**不应自己起/keepalive server** | **已推翻**。CordCode 现默认自己托管 `opencode serve`（`managed_local` source），负责启动+健康检查+keepalive |
| T01 endpoint source | 四态：`external_http` / `legacy_64667` / `service_discovery_future` / `disabled` | **已扩展为五态**，新增 `managed_local` 且为 fresh-install 默认；`external_http` 降为高级手配项，不再主推 |
| T06 文案 | bring-your-own-server，CordCode 不启动 server | **过时**。Settings 主文案现为「自动托管（推荐）」；64667 / external_http 文案以当前活文档为准 |
| §9 验收第 13 条 | CordCode 不承诺 keepalive server | **作废**。CordCode 现正是 keepalive 方（managed_local supervisor） |
| §9 验收第 8 条 | 共享同一 server | 满足，但实现方式从「用户自带 server」变为「CordCode 托管 server」 |

### 仍有效的部分（可继续作为真值引用）

- **§2 已确认事实**：OpenCode server/client 模型、stable CLI 命令面、Desktop vlocal 限制、64667 来源
  —— 这些事实陈述仍准确。
- **T02 health/auth 校验链**：no-auth 401 + authed 200、`password_required` / `server_unauthenticated` /
  `not_opencode_server` 校验逻辑 —— 通用机制，在 `managed_local` 下照常复用，仍有效。
- **T03 RuntimeManager 显式传 `-opencode-url`**：已实现，派生 2 进一步扩展为注入 managed URL。
- **T04 Desktop 配置同步**：已实现，派生 2 进一步做了 graceful quit/reopen + 项目合并。
- **T05 go-bridge 去除隐式 64667 默认**：已实现且仍有效。
- **§7 失败模式**、**§8 安全约束**：通用，仍适用。
- **§9 验收第 1-7、9-12、14-16 条**：基本满足（不硬编码 64667、password 不进 argv、空 URL 不 dial 64667、
  Desktop 同步、no-auth 200 拒绝等）。
- **§10 后续方向**：10.1 stable service discovery、10.2 Desktop sidecar discovery —— 仍未实现，
  stable `opencode` 仍无 `service`/`--register` 命令，状态不变，仍 future-gated。

### 当前真值来源（优于本方案）

后续实现以这些文档为准，不要用本方案作为编码依据：

- `docs/2026-07-03-opencode-managed-local-server-seamless-plan完成情况.md`（派生 2 完成情况 + 正式关闭）
- `GO_BRIDGE_ARCHITECTURE.md`、`BUILD_INSTALL_AND_RUNTIME.md`（活文档）
- 源码：`MacBridge/MacBridge/Services/OpenCodeManagedServer.swift`、`OpenCodeEndpointResolver.swift`

## 0. 资料采纳规则

本方案以**当前本机可运行行为和源码**为真值。源码里存在的能力，如果 stable 二进制没有暴露，
不能作为当前默认实现依赖。

优先级：

1. 本机 stable `opencode` 二进制实测。
2. 当前本机 OpenCode 源码：`/Users/jacklee/Projects/opencode`。
3. 当前本机 CordCode MacBridge 源码：`/Users/jacklee/Projects/cordcode-macbridge`。
4. OpenCode 官方 docs/changelog/release note。
5. 其他调查笔记、评审报告或推断。

已核实并采纳评审报告中的关键纠偏：

- `opencode-ai@1.17.13` 的 stable `opencode` **没有** `service` 子命令。
- stable `opencode serve --help` **没有** `--register`。
- OpenCode 源码/tag 中存在 `packages/cli/src/services/daemon.ts`，但它不能直接证明 stable CLI
  已提供 `server.json/password` discovery 契约。
- 当前本机 `64667` listener 来自旧 `opencodeIosNew` unified bridge 启动的
  `opencode serve --hostname 0.0.0.0 --port 64667 --print-logs`，不是 OpenCode Desktop
  `vlocal` sidecar。
- Phase A 认证模型已在 stable `opencode` 1.17.13 上复测：
  - `OPENCODE_SERVER_PASSWORD=... opencode serve --hostname 127.0.0.1 --port <p>`
  - 无 auth `/global/health` → 401
  - `opencode:<password>` → 200 `{"healthy":true,"version":"1.17.13"}`
  - 错误密码 → 401
- 未设置 `OPENCODE_SERVER_PASSWORD` 时，stable `opencode serve` 的 `/global/health` 对无 auth 返回
  200；带任意 Authorization header 也会返回 200。这表示 server 未启用 Basic Auth。CordCode
  默认/自动路径不得接受无密码 OpenCode server，endpoint validation 必须先用 no-auth 探测证明
  server 要求认证，再验证配置凭据。

评审报告中未在本机核实的内容，例如 `lildax` / `@opencode-ai/cli` 2.0-preview 轨道，本方案只作为
未来调查方向，不作为实现依据。

## 1. 结论

在“不改 OpenCode 源码”的约束下，CordCode **不能直接接入 OpenCode Desktop 当前勾选的
`vlocal` sidecar**。Desktop `vlocal` 是 Electron 主进程随机 loopback 端口 + 随机 UUID 密码启动的
sidecar，连接凭据只通过 Electron IPC 暴露给 Desktop renderer，没有稳定文件或公开 API。
CordCode 作为外部进程拿不到该密码，不能可靠通过 `/global/health` 与 `/global/event` 认证。

原方案中“默认调用 `opencode service start` 并读取 `server.json/password`”的自动 discovery 路线，
在当前 stable `opencode` 上不可实现。该路线降级为**未来门控能力**：只有当 stable
`opencode` 明确暴露 `service` / `serve --register` 且端点经实测匹配 CordCode 数据面时，才可开启。

当前可直接开发的 stable-compatible 路线是：

1. 把 OpenCode 集成抽象为明确的 server source。
2. 首期实现 `external_http`：CordCode 连接用户或运维已启动的 stable `opencode serve` HTTP server，
   并把 OpenCode Desktop 默认 server 同步到同一个 URL。
3. 保留 `legacy_64667` 作为显式兼容模式，不能再把它伪装为默认共享方案。
4. 为未来 `service_discovery` 留 capability-gated 入口，但默认关闭。

这不能字面复用 Desktop `vlocal`，也不能在当前 stable 上做到完全自动“官方 daemon discovery”。
它能先消除最重要的产品问题：CordCode/iOS 与 OpenCode Desktop 可选择连接同一个 OpenCode server，
不再被固定 `64667` 和 Desktop `vlocal` 分裂成两个项目/session scope。

## 2. 已确认事实

### OpenCode 官方 server/client 模型

OpenCode 的官方心智模型是 **server + 多 client**：

```text
one OpenCode server
  ├── Desktop app connects to a server URL
  ├── Web UI connects to the same backend
  ├── CLI/TUI attaches via opencode attach
  └── SDK/API calls the HTTP API
```

这点可以作为产品方向：CordCode 不应让 Desktop、iOS、Web/TUI 各自绑定不同 OpenCode backend，
否则项目列表、session 列表、running state 和事件流会自然分裂。

stable `opencode` 1.17.13 实测命令面：

```text
opencode serve
  --port         default 0
  --hostname     default 127.0.0.1
  --mdns         enables mDNS and defaults hostname to 0.0.0.0
  --mdns-domain  default opencode.local
  --cors

opencode attach <url>
opencode web
```

`opencode attach` 可用于手动验证 OpenCode 自身的多 client 共享语义；`opencode web` 也可作
Web/TUI 共享验证，但不是 CordCode 的 canonical server 来源。

### OpenCode daemon discovery 状态

OpenCode 源码中存在 daemon discovery 机制：

- `/Users/jacklee/Projects/opencode/packages/cli/src/services/daemon.ts`
  - `Global.Path.state/server.json`
  - `Global.Path.state/password`
  - `service start` → `serve --register`
  - `password` 文件 `0600`

但 stable `opencode-ai@1.17.13` 二进制没有暴露这条命令面：

```bash
opencode service --help     # 回退到顶层 help，无 service 命令
opencode serve --help       # 无 --register
```

因此当前实现不得依赖 `server.json/password` 自动产生。

### OpenCode Desktop 当前限制

Desktop `vlocal` 的实现边界：

- `/Users/jacklee/Projects/opencode/packages/desktop/src/main/index.ts`
  - 端口来自 `OPENCODE_PORT` 或随机 loopback 端口
  - 密码来自 `randomUUID()`
  - 只把 `{ url, username, password }` resolve 到 `serverReady`
- `/Users/jacklee/Projects/opencode/packages/desktop/src/preload/types.ts`
  - `ServerReadyData` 只面向 renderer preload API
- `/Users/jacklee/Projects/opencode/packages/desktop/src/renderer/index.tsx`
  - renderer 把该数据作为 `type: "sidecar", variant: "base"` 注册为 Local Server

结论：不改 OpenCode Desktop 源码时，CordCode 没有稳定方式发现 `vlocal` 密码。

同时，Desktop 支持把默认 server 指向已配置的 HTTP server URL。实现目标不是阻止 Desktop 启动
`vlocal`，而是让 Desktop 的默认 active server 与 CordCode 使用同一个 `external_http` endpoint。
“Desktop 不启动 sidecar”不能作为验收条件。

### CordCode 当前实现

当前 OpenCode 路径仍围绕固定 `64667`：

- `go-bridge/main.go`
  - `-opencode-url` 默认 `http://localhost:64667`
  - `-opencode-user/-opencode-pass` 可从 `OPENCODE_SERVER_USERNAME/PASSWORD` 读取
- `agent/opencode/opencode.go`
  - 未传 URL 时 fallback 到 `http://localhost:64667`
- `MacBridge/MacBridge/Services/RuntimeManager.swift`
  - RuntimeManager 当前没有向 go-bridge argv 传 `-opencode-url`
  - user/pass 通过环境变量传入 runtime
  - `configureOpenCodeDesktopServerIfNeeded()` 固定写入 `http://127.0.0.1:64667`

因此，接入新的 endpoint 不是“继续使用现有 URL 入口”这么简单；Swift supervisor 必须新增显式
`-opencode-url <url>` argv，user/pass 仍走 env，避免 secret 进 argv。

### 当前 64667 运行态

本机实测：

```text
64667 listener: /opt/homebrew/bin/opencode serve --hostname 0.0.0.0 --port 64667 --print-logs
parent: /Users/jacklee/Projects/opencodeIosNew/bridge/src/start-unified.mjs
Desktop vlocal: 127.0.0.1:53603
```

这说明：

- `64667` 不是 Desktop `vlocal`。
- `0.0.0.0:64667` 与本方案 loopback-only 安全约束冲突。
- 后续实现和验证前，必须清理旧 unified bridge 进程，避免把旧环境当成 CordCode 行为。

## 3. 目标行为

### Phase A：stable-compatible `external_http`

默认可开发目标：

1. CordCode 支持显式 OpenCode HTTP endpoint 配置：URL + username + password。
2. URL 必须是 loopback：用户输入可填 `http://localhost:<port>`，保存和校验时规范化为
   `http://127.0.0.1:<port>`。
3. CordCode 对该 URL 执行 Basic Auth `/global/health` 校验。
4. go-bridge 使用该 URL 作为 OpenCode HTTP proxy 和 `/global/event` SSE source。
5. OpenCode Desktop 默认 server 配置同步到同一个 URL + credentials。
6. 若未配置 endpoint，不再默认假定 `64667` 是共享 server；OpenCode backend 应明确
   `not_configured` 或继续走用户显式选择的 legacy 模式。
7. 如果 URL 不可达、401、非 loopback、不是 OpenCode server，真实暴露错误，不自动启动第二个 server。

用户/开发者可用 stable OpenCode 启动共享 server，例如：

```bash
OPENCODE_SERVER_PASSWORD='<password>' \
opencode serve --hostname 127.0.0.1 --port <chosen-port>
```

端口可以由用户指定；CordCode 不把 `4096` 或 `64667` 当产品默认。
`OPENCODE_SERVER_PASSWORD` 必须设置；不设置时 stable `opencode serve` 会变成无认证 HTTP server，
CordCode 默认路径必须拒绝这类 endpoint。

### Phase B：future-gated `service_discovery`

未来仅当 stable `opencode` 满足以下条件时启用：

1. `opencode service --help` 明确暴露 `service start/status/stop/password`。
2. `opencode serve --help` 明确暴露 `--register`，或官方文档声明等价 discovery 契约。
3. `server.json/password` 文件路径、权限、字段经本机实测。
4. `/global/health`、`/global/event`、`/session`、`/session/:id/message`、`/session/:id/prompt_async`
   等端点与 CordCode 当前 OpenCode 数据面兼容。

满足后再把 `service_discovery` 从 disabled/future source 提升为默认 source。

## 4. 非目标

- 不修改 `/Users/jacklee/Projects/opencode` 源码。
- 不从 Electron IPC、renderer DevTools、内存、日志中抓取 `vlocal` 密码。
- 不让两个 OpenCode server 并发读写同一个 state 目录。
- 不把 `64667` 继续写成 Desktop 默认 server。
- 不把 `4096` 或任何新固定端口当成产品默认。
- 不在默认路径启动 `opencode web`；CordCode 需要的是 HTTP/SSE backend，不是 Web UI。
- 不接受无密码 OpenCode server 作为默认/自动 endpoint，即便它只绑定 loopback。
- 不默认使用 `--mdns`，因为 stable `--mdns` 会默认 `hostname=0.0.0.0`，与 loopback-only 冲突。
- 不引入 mock/fallback/placeholder 成功路径。
- 不运行 UI tests、snapshot tests、simulator automation；本方案验证以代码阅读、定向 build、
  定向 unit test 和手动 CLI health check 为主。

## 5. 架构设计

### 5.1 OpenCode server source

新增明确 source，而不是隐式 fallback 链：

```text
external_http            # Phase A，显式 URL，stable 可实现
legacy_64667             # 显式兼容模式，保留但不作为共享默认
service_discovery_future # Phase B，只有 stable CLI 能力满足后才启用
disabled
```

规则：

- source 必须显式选中。
- `external_http` 失败：OpenCode backend unavailable，带明确 reason。
- `legacy_64667` 失败：OpenCode backend unavailable，不能转去其他 source 制造“成功”。
- `service_discovery_future` 在 stable 能力未满足时直接 unavailable，reason 写明缺少 `opencode service` /
  `serve --register`。

### 5.2 Phase A endpoint 配置与校验

在 Swift supervisor 层增加 OpenCode endpoint resolver：

```text
OpenCodeEndpointResolver
  source: external_http | legacy_64667 | service_discovery_future | disabled
  url: string?
  username: string?
  password: string?
  validate() -> OpenCodeEndpoint
```

Phase A 不启动或常驻 OpenCode 外部进程；它只连接已存在的 stable `opencode serve` server。
这与当前 MacBridge supervisor “只管理 cccode-bridge-runtime” 的活文档约定一致。

Phase A 是 **bring-your-own-server**：CordCode 负责连接、认证、同步 Desktop 默认 server，不负责让
OpenCode server 开机自启或崩溃重启。共享 server 的持久化由用户/运维负责；managed daemon 属于
Phase B 或未来单独设计。

校验要求：

- URL 必须是 HTTP loopback。
- 配置中的 `localhost` 在保存/校验时规范化为 `127.0.0.1`，避免 IPv6 `::1` 与
  `opencode serve --hostname 127.0.0.1` 的 IPv4 listener 不匹配。
- password 必须非空；客户端未配置 password 时直接拒绝为 `password_required`。
- `external_http` 必须先发 no-auth `GET /global/health`：
  - 若返回 401，证明 server 要求认证，继续做带 Basic Auth 的 health 校验。
  - 若返回 200 且 body 是 OpenCode health，说明 server 未启用 Basic Auth，拒绝为
    `server_unauthenticated`。
  - 若连接失败、超时、非 OpenCode 响应，按下方 reason 分类。
- 只有 no-auth health 返回 401 后，带 Basic Auth 的 `/global/health` 返回 200 才能接受 endpoint。
- 禁止自动 discovery 使用 `0.0.0.0`、LAN IP、公网 URL。
- `/global/health` 必须成功。
- 401 归类为 `auth_failed`。
- 连接失败归类为 `unreachable`。
- 非 OpenCode 响应归类为 `not_opencode_server`。
- `legacy_64667` 是升级连续性的兼容例外，不代表安全共享 server。若迁移或显式选择该 source 时
  no-auth health 返回 200，CordCode 可以为保持旧行为继续标记为 legacy 可用，但必须显示
  `legacy_insecure_unverified` 警告，说明该 `64667` 可能是无密码或 `0.0.0.0` 监听的旧进程，并引导
  用户清理旧 bridge 后改配 `external_http`。新装和 `external_http` 不允许这个例外。
- 若 `legacy_64667` 的 no-auth health 返回 401，说明该 legacy endpoint 至少启用了认证；继续用迁移
  或用户配置的 username/password 做 authed health 校验，通过后可标记为 legacy 可用，无需
  `legacy_insecure_unverified` 警告。

### 5.3 RuntimeManager 接入

Phase A 成功解析 endpoint 后：

- RuntimeManager 必须向 go-bridge argv 新增：

```text
-opencode-url <endpoint.url>
```

- username/password 继续通过环境变量传：

```text
OPENCODE_SERVER_USERNAME
OPENCODE_SERVER_PASSWORD
```

理由：

- URL 不是 secret，可以进 argv。
- password 是 secret，不进 argv。
- go-bridge 已有 `clearControlPlaneEnv()` / agent env deny-list，继续阻止 `OPENCODE_SERVER_*`
  进入 agent/tool 子进程。

### 5.4 Desktop 配置同步

复用现有 `RuntimeManager.configureOpenCodeDesktopSettings` 的 JSON 写入思路，但 serverURL 和
credentials 来自 validated endpoint。

要求：

- 写入 `opencode.global.dat` 的 `server.list`：

```json
{
  "type": "http",
  "http": {
    "url": "http://127.0.0.1:<configured>",
    "username": "opencode",
    "password": "<password>"
  }
}
```

- 写入 `opencode.settings` 的 `defaultServerUrl` 为同一 URL。
- 不再写固定 `http://127.0.0.1:64667`。
- 如果必须维护 `currentSidecarUrl` 兼容字段，只能写同一 validated URL。
- 去重时移除同 URL 旧条目，但不要删除用户手动添加的其他 server。
- 不删除 Desktop 自带 `sidecar` Local Server；只改变默认 server 指向。

安全要求：

- 不把 password 写日志。
- 配置文件权限若由 Desktop 既有机制决定，CordCode 只做最小写入；实现时应记录该文件路径和权限。
- Desktop 服务器列表仍显示 `vlocal` 不算失败。

### 5.5 凭据来源与迁移

当前 CordCode 有三类凭据来源：

1. 环境变量 `OPENCODE_SERVER_USERNAME/PASSWORD`。
2. CordCode data dir `credentials.json`。
3. 旧 `com.opencode.server` LaunchAgent 中的 OpenCode env（legacy/少见路径）。

Phase A 规则：

- `external_http` source 使用 CordCode Settings 中的 URL + username/password。
- username/password 可沿用现有 `credentials.json` 存储格式，新增 URL/source 字段时必须向后兼容。
- 全新安装默认 source 为 `disabled` 或 `external_http` + `not_configured`；不得自动落到 `64667`。
- 升级安装若检测到既有 `credentials.json` 且用户尚未显式保存 source，自动迁移到
  `legacy_64667`，保持现有用户的 OpenCode 行为连续；同时在 Settings 或 backend reason 中给出一次性
  迁移提示，引导用户配置 `external_http` 共享 server。
- `legacy_64667` 的迁移提示必须明确：它只是为了不中断旧用户，不能证明端点启用了 Basic Auth，也不承诺
  与 Desktop `vlocal` 共享 session；本机旧 `64667` 还可能来自无密码、`0.0.0.0` 监听的 unified bridge。
  这是唯一允许带 `legacy_insecure_unverified` 警告继续运行的 source。
- 升级安装若已有显式 source 配置，尊重用户配置，不再自动迁移。
- 首次启动仍可复用旧 LaunchAgent 凭据作为 username/password 来源，但不能因此自动假定 URL 是
  `64667`；只有升级迁移或用户显式选择 `legacy_64667` 时才使用该 URL。
- 从 `legacy_64667` 切到 `external_http` 时，保留旧 credentials，但 endpoint source 改为
  `external_http`，Desktop 默认 server 改写为新 URL。
- 若未来启用 `service_discovery_future`，daemon `password` 文件将成为独立凭据来源；届时需另写迁移规则，
  不能与 `credentials.json` 混用。

### 5.6 go-bridge 接入

go-bridge 数据面保持现有 OpenCode hybrid 矩阵：

- HTTP proxy：get/list/create/resume session、list projects。
- send：先用 proxy 校验 server session，再由 `agent/opencode` 发送并 relay events。
- `/global/event` SSE passive subscriber：外部 turn 广播。

需要修改的配置行为：

- `go-bridge/main.go` 的 `-opencode-url` 默认改为空或只来自 `OPENCODE_BASE_URL`。
- `agent/opencode/opencode.go` 未获得 URL 时不再 fallback 到 `http://localhost:64667`。
- descriptor health check 对空 URL 返回 `not_configured`，不要 dial `64667`。
- live tests 允许通过 `OPENCODE_BASE_URL` 指向自定义 server。

### 5.7 手动共享验证工具

开发时可以用 stable OpenCode 验证 shared server 语义：

```bash
OPENCODE_SERVER_PASSWORD='<password>' \
opencode serve --hostname 127.0.0.1 --port <chosen-port>

opencode attach http://127.0.0.1:<chosen-port>
```

如需验证 Web/TUI 共享，可单独使用：

```bash
opencode web --hostname 127.0.0.1 --port <chosen-port>
opencode attach http://127.0.0.1:<chosen-port>
```

这些命令只用于验证 OpenCode server/client 架构共享 session，不代表 CordCode 应默认使用固定端口
或 `opencode web`。

### 5.8 Phase A 持久化指引

因为 Phase A 不由 CordCode supervisor 启动 OpenCode server，用户若希望重启后继续共享，需要自己让
`opencode serve` 常驻。文档和 Settings 文案必须直说：

```text
CordCode will connect to this OpenCode server, but it will not start or keep it alive.
Keep the command running, or install your own local LaunchAgent/service.
```

可在文档中提供本机模板，但不得提交个人绝对路径或密码。模板必须要求用户在本机运行
`command -v opencode` 后填入真实路径，并只绑定 loopback：

```xml
<!-- ~/Library/LaunchAgents/local.opencode.shared-server.plist -->
<plist version="1.0">
<dict>
  <key>Label</key><string>local.opencode.shared-server</string>
  <key>ProgramArguments</key>
  <array>
    <string>/absolute/path/from/command-v-opencode</string>
    <string>serve</string>
    <string>--hostname</string><string>127.0.0.1</string>
    <string>--port</string><string>CHOSEN_PORT</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>OPENCODE_SERVER_PASSWORD</key><string>REPLACE_WITH_LOCAL_PASSWORD</string>
  </dict>
  <key>StandardOutPath</key><string>/Users/REPLACE_WITH_USERNAME/Library/Logs/opencode-shared.log</string>
  <key>StandardErrorPath</key><string>/Users/REPLACE_WITH_USERNAME/Library/Logs/opencode-shared.err.log</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
```

因为 plist 内含 `OPENCODE_SERVER_PASSWORD`，创建后必须限制权限：

```bash
chmod 600 ~/Library/LaunchAgents/local.opencode.shared-server.plist
```

该模板只是用户本机配置指南，不属于 CordCode 自动安装内容。若未来要由 CordCode 管理此 LaunchAgent，
必须另写安全/生命周期设计。

## 6. 开发任务

### T01 新增 OpenCode endpoint source 与配置模型

文件建议：

- `MacBridge/MacBridge/Services/OpenCodeEndpointResolver.swift`
- `MacBridge/MacBridge/App/AppDependencies.swift`
- `MacBridge/MacBridge/ViewModels/SettingsViewModel.swift`
- `MacBridge/MacBridge/Views/SettingsView.swift`
- 对应 Swift tests

实现：

1. 增加 `external_http` / `legacy_64667` / `service_discovery_future` / `disabled` source。
2. Settings 增加 OpenCode server URL 字段和 source 选择。
3. URL 为空且 source 为 `external_http` 时返回 `not_configured`。
4. `legacy_64667` 只能在用户显式选择时使用。
5. `service_discovery_future` 默认 disabled，若用户强开但 stable CLI 不支持，返回明确错误。
6. 实现升级迁移：既有 `credentials.json` + 无显式 source → `legacy_64667` + 一次性迁移提示；
   新装 → `not_configured`。
7. 保存 URL 时将 `localhost` 规范化为 `127.0.0.1`。

测试：

- URL normalization。
- `localhost` 保存后变为 `127.0.0.1`。
- 非 loopback URL 拒绝。
- source 切换不丢现有 username/password。
- 未配置 URL 不 fallback 到 `64667`。
- 存量 `credentials.json` 升级落到 `legacy_64667`。
- 全新安装默认 `not_configured`。

### T02 endpoint health/auth 校验

测试归属：`OpenCodeEndpointResolverTests`。

实现：

1. password 为空直接拒绝为 `password_required`。
2. no-auth `GET /global/health`，2s 超时：
   - connection refused / timeout → `unreachable`。
   - 200 + OpenCode health body → `server_unauthenticated`，拒绝该 endpoint。
   - 401 → server 已启用认证，进入下一步。
   - 其他状态、非 JSON 或缺少 healthy/version → `not_opencode_server`。
3. 带 Basic Auth 再次 `GET /global/health`：默认 username 可用 `opencode`，但应允许用户覆盖。
4. authed 200 + OpenCode health body → validation 通过。
5. authed 401 → `auth_failed`。
6. authed 其他状态、非 JSON 或缺少 healthy/version → `not_opencode_server`。

OpenCode health body 的判定以 stable `opencode` 1.17.13 实测的 `{"healthy":true,"version":"..."}`
schema 为准。升级 OpenCode CLI 或未来启用 `service_discovery_future` 前，必须复测
`/global/health` schema；若官方 schema 变化，应先更新 validator 与测试，再放行新版本。

测试：

- healthy server：no-auth 401，authed 200。
- 401。
- password 为空时返回 `password_required`。
- no-auth 200 + OpenCode body 时返回 `server_unauthenticated`。
- 无密码 server 即使带 `opencode:<configured-password>` 返回 200，也必须被拒绝为
  `server_unauthenticated`。
- timeout。
- non-loopback URL。
- malformed health response。

### T03 RuntimeManager 显式传 `-opencode-url`

文件：

- `MacBridge/MacBridge/Services/RuntimeManager.swift`
- `MacBridge/MacBridgeTests/MacBridgeBehaviorTests.swift`

实现：

1. `RuntimeConfig` 增加 `opencodeURL` 和 `opencodeSource`。
2. endpoint valid 时，process arguments 增加 `-opencode-url <url>`。
3. user/pass 继续走 env，不进 argv。
4. endpoint invalid 时，go-bridge 仍可启动，但 OpenCode backend descriptor 必须 unavailable。

测试：

- argv 包含 discovered/configured URL。
- argv 不包含 password。
- env 包含 username/password。
- invalid endpoint 不写 `64667`。

### T04 改写 Desktop 配置同步

文件：

- `MacBridge/MacBridge/Services/RuntimeManager.swift`
- `MacBridge/MacBridgeTests/MacBridgeBehaviorTests.swift`

实现：

1. `configureOpenCodeDesktopServerIfNeeded()` 接收 validated endpoint。
2. 移除固定 `http://127.0.0.1:64667`。
3. 使用 endpoint URL 写 server list/defaultServerUrl。
4. 保留非目标 server。
5. 不删除 Desktop sidecar entry。
6. 密码不写日志。

测试：

- 写入 endpoint URL。
- 不再出现 `64667`，除非 source 显式为 `legacy_64667`。
- 重复调用不重复插入。
- 其他用户 server 不被删除。

### T05 go-bridge 去除隐式 64667 默认

文件：

- `go-bridge/main.go`
- `agent/opencode/opencode.go`
- `go-bridge/agent_descriptor.go`
- 对应 Go tests

实现：

1. `-opencode-url` 默认空或来自 `OPENCODE_BASE_URL`。
2. `agent/opencode.New` 未获得 URL 时返回明确 configuration error 或 degraded diagnostics。
3. descriptor health check 对空 URL 返回 `not_configured`。
4. live tests 仍允许通过 `OPENCODE_BASE_URL` 指向自定义 server。

测试：

```bash
go test ./go-bridge/... ./agent/opencode/... -count=1
```

### T06 文档与设置文案更新

文件：

- `BUILD_INSTALL_AND_RUNTIME.md`
- `GO_BRIDGE_ARCHITECTURE.md`
- `docs/backends-and-config.md`
- `MacBridge/MacBridge/Services/Localization.swift`

内容：

- 将 OpenCode 默认从“固定 64667”改为“显式 external HTTP endpoint”。
- 说明 `64667` 仅为 legacy/custom HTTP。
- 说明当前 stable `opencode` 不提供 `service` discovery；该能力 future-gated。
- 说明 Phase A 是 bring-your-own-server：CordCode 不启动、不 keepalive OpenCode server。
- 说明 Desktop 正在运行时，配置文件写入可能不会立即切换当前 active server；用户需重启 Desktop 或手动切换。
- 说明如果 OpenCode 日志出现 `OPENCODE_SERVER_PASSWORD is not set; server is unsecured`，CordCode 会拒绝
  `external_http` endpoint；用户必须设置 `OPENCODE_SERVER_PASSWORD` 后重启 server。
- Settings 中的命令示例改为：

```bash
OPENCODE_SERVER_PASSWORD='<password>' \
opencode serve --hostname 127.0.0.1 --port <chosen-port>
```

- 增加可选 LaunchAgent 持久化说明，但明确 CordCode 不自动安装。

### T07 验证

不运行 UI automation。建议验证：

```bash
go test ./go-bridge/... ./agent/opencode/... -count=1
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink \
  -configuration Debug -destination 'platform=macOS' build
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink \
  -configuration Debug -destination 'platform=macOS' test \
  -only-testing:CordCodeLinkTests/OpenCodeEndpointResolverTests \
  -only-testing:CordCodeLinkTests/MacBridgeBehaviorTests \
  -only-testing:CordCodeLinkTests/SettingsViewModelTests
```

修改 Go 或 Swift 产品代码后，按仓库规则执行 Release 构建与覆盖安装：

```bash
./scripts/build-unsigned-release.sh
killall CordCodeLink 2>/dev/null || true
rm -rf /Applications/CordCodeLink.app
cp -R build/unsigned-release/Build/Products/Release/CordCodeLink.app /Applications/
open /Applications/CordCodeLink.app
```

手动前置清理：

```bash
pgrep -fl 'opencodeIosNew|start-unified|opencode serve --hostname 0.0.0.0 --port 64667'
```

如发现旧 unified bridge 占用 `64667`，停止它；不要把旧进程当作 CordCode/OpenCode Desktop 行为。

人工确认项：

- CordCode runtime argv 使用 configured endpoint。
- OpenCode Desktop 默认 server 指向同一 endpoint。
- iOS 打开的 OpenCode session 与 Desktop 默认 server 中可见 session 一致。
- 不要求 Desktop 服务器列表里 `vlocal` 消失；只要求默认 active server 是 configured endpoint。
- 如果 Desktop 在配置写入时已经运行，重启 Desktop 或手动切换后再确认 active server。
- 可选：`opencode attach <endpoint-url>` 与 Desktop 默认 server 看到同一 session/state。

## 7. 失败模式

| 场景 | 行为 |
| --- | --- |
| `opencode` CLI 不存在 | external server 仍可配置；仅命令示例/检测显示 CLI missing |
| URL 未配置 | OpenCode backend `not_configured`，Claude/Codex 不受影响 |
| 新装且 URL 未配置 | OpenCode backend `not_configured`，Settings 显示配置指引 |
| 存量 `credentials.json` 且无显式 source | 自动迁移为 `legacy_64667`，保持行为连续并提示迁移到 `external_http` |
| password 为空 | 拒绝配置为 `password_required`，提示必须填写 CordCode 侧 password |
| URL 非 loopback | 拒绝，OpenCode backend unavailable |
| health unreachable | OpenCode backend unavailable；提示 connection failed |
| no-auth health 200 + OpenCode body | `external_http` 拒绝为 `server_unauthenticated`，提示用 `OPENCODE_SERVER_PASSWORD` 重启 server |
| no-auth health 401 + authed health 401 | OpenCode backend unavailable；提示 credential mismatch |
| health 响应不像 OpenCode | OpenCode backend unavailable；提示 not opencode server |
| 用户显式选择 legacy_64667 | 使用 `http://127.0.0.1:64667`，但标记为 legacy；若 no-auth 200，显示 `legacy_insecure_unverified`，不承诺安全或共享 Desktop vlocal；若 no-auth 401 且 authed 校验通过，则 legacy 可用且不加 insecure 警告 |
| 用户选择 service_discovery_future 但 stable CLI 无 service | OpenCode backend unavailable；reason 写明当前 stable opencode 缺少 `service` / `--register` |
| Desktop 未安装 | 跳过 Desktop 配置同步；CordCode/iOS 仍可连 external endpoint |
| Desktop 安装但未启动 | 写配置文件；下次 Desktop 启动默认连接 external endpoint |
| Desktop 已运行 | 写配置文件后当前 active server 可能不立即切换；需重启 Desktop 或手动切换 server |
| OpenCode server 进程退出 | CordCode/Desktop/iOS 同时失去该 backend；Phase A 不负责 keepalive |

## 8. 安全约束

- OpenCode password 不进 argv；继续通过 runtime env 或私有配置传给 go-bridge。
- 不把 password 打印到日志、错误提示、CHANGELOG 或 docs。
- password 必须非空；客户端未填 password 返回 `password_required`。
- `external_http` validation 必须先通过 no-auth health 返回 401 证明 server 要求认证；no-auth health
  返回 200 的 OpenCode server 必须拒绝为 `server_unauthenticated`，即使带 Authorization header 也会返回
  200。
- 无密码 OpenCode server 不作为 CordCode 默认/自动 endpoint；`legacy_64667` 仅为升级连续性的显式兼容例外，
  且必须带 `legacy_insecure_unverified` 警告。
- 只允许 loopback URL 作为 CordCode 自动/默认 endpoint。
- 拒绝 `0.0.0.0`、LAN IP、公网 URL 作为默认或自动配置；remote/LAN OpenCode 只能作为未来显式高级模式另行设计。
- stable `--mdns` 不作为默认，因为它会把 hostname 扩展到 `0.0.0.0`。
- `OPENCODE_SERVER_USERNAME/PASSWORD` 仍属于控制面 secret，不得传入 agent/tool 子进程。

## 9. 验收标准

完成 Phase A 后应满足：

1. 产品默认路径不再隐式硬编码 `64667`。
2. RuntimeManager 可以显式传入 OpenCode endpoint URL。
3. password 不出现在 argv / 日志。
4. password 为空时不能通过 endpoint validation，reason 为 `password_required`。
5. go-bridge 空 URL 不 dial `64667`，返回 `not_configured`。
6. 新装默认不连 `64667`；存量 credentials 升级默认落到 `legacy_64667` 并提示迁移。
7. OpenCode Desktop 默认 server 可同步到 configured endpoint。
8. CordCode/iOS/OpenCode Desktop 默认 server 可共享同一 server 的 session/state。
9. discovery 失败或 endpoint 不健康时真实暴露错误，不自动制造第二个 server。
10. 不需要修改 OpenCode 源码。
11. 定向 Go/Swift 测试与 macOS build 通过。
12. Desktop 仍启动 sidecar `vlocal` 时不判失败；只要默认 active server 指向 configured endpoint 即可。
13. Phase A 明确显示 OpenCode server 由用户/运维负责常驻，CordCode 不承诺 keepalive。
14. `external_http` 对 no-auth `/global/health` 返回 200 的 OpenCode server 必须拒绝为
    `server_unauthenticated`；只发 authed 请求拿到 200 不能作为认证成功证据。
15. `legacy_64667` 若绕过 passwordless 拒绝，只能带 `legacy_insecure_unverified` 警告继续运行，不能被
    当作安全共享 endpoint 或新装默认。
16. `/global/health` body schema 校验必须有测试守护；OpenCode CLI 版本升级时必须复测该 schema。

## 10. 后续可选 upstream / future 方向

### 10.1 stable service discovery

如果未来 stable `opencode` 公开 `service` / `server.json/password` discovery 契约，CordCode 可以把
`service_discovery_future` 提升为默认 source。提升前必须重新实测：

- 命令面。
- 文件路径和权限。
- HTTP endpoint 兼容性。
- daemon 生命周期与 owner 语义。
- 凭据与 `credentials.json` 的迁移规则。

### 10.2 Desktop sidecar discovery

如果 OpenCode Desktop 未来公开 sidecar discovery 契约，例如把 `vlocal` 的 `server.json/password`
以 `0600` 写入稳定位置，CordCode 可以新增 `desktop_sidecar_discovery` source，优先接入真正
Desktop `vlocal`。在此之前，不应把 Electron IPC 抓取或日志解析作为产品实现。
