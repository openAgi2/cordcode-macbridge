# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

This repo is the **Mac-side bridge aggregate** for CordCode: the macOS app,
its embedded WebSocket runtime, the public Relay server source, and the agent
drivers. The app users see is **CordCode Link**; the iOS client lives in a
separate repo (`../cordcode-ios/`).

It exposes locally-installed AI coding agent backends (Claude Code CLI, OpenCode
server, Codex app-server) to iPhone/iPad clients over a direct LAN WebSocket or
an end-to-end-encrypted public Relay.

**Two distinct deployment units share this repo:**

- **CordCode Link** (`MacBridge/` + `go-bridge/` + `agent/`): the macOS app that
  runs on the user's Mac; `go-bridge/` is embedded into the app as
  `cordcode-bridge-runtime`.
- **Relay server** (`relay-server/`, independent Go module `cordcode-relay`): the
  public encrypted relay deployed on a VPS — **not** part of the Mac app. This
  is why the repo is named `cordcode-macbridge` (the whole Mac-side bridge
  family), not `cordcode-link` (which would mislabel the Relay server source).

## New-session bootstrap

新 session 不能只读本文件中的摘要就开始修改运行链路。按任务范围先读根目录活文档：

- 相邻旧一体仓库 `../opencode-cc-connect/` 只作为历史设计和迁移证据；当前实现、命令、
  协议与支持范围以本仓库和 `../cordcode-ios/` 为准。不得从旧文档整段复制配置而不反查源码。
- 修改 Mac app、runtime 生命周期、构建安装、端口或日志：必须先读
  [BUILD_INSTALL_AND_RUNTIME.md](BUILD_INSTALL_AND_RUNTIME.md)。
- 修改 agent driver、Codex/OpenCode/Claude 事件、session、history、polling 或 capability：
  必须先读 [GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md)。
- 修改 `relay-server/`、VPS 部署、mailbox、route、HPKE 或生产 Relay：
  必须先读 [RELAY_SERVER_OPERATIONS.md](RELAY_SERVER_OPERATIONS.md)。
- 修改与 iOS 的配对、hello、重连、撤销、session/turn 同步：同时读
  `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`。
- 排查 Claude/Codex/OpenCode 的 session、history、live stream、执行态、列表分页或端到端
  同步异常：必须先检索本仓 `think.md` 和相邻 iOS 仓 `../cordcode-ios/think.md`，复用已有
  复盘结论；不要在已有结论覆盖的问题上从零重复调查。

这些是持续更新的架构/运维真值；`docs/YYYY-MM-DD-*.md` 主要是方案、评审和完成报告，
不能代替根目录活文档。`think.md` 是已知问题与复盘经验库，排障时作为活文档入口的一部分。

涉及 protocol、pairing、加密、Relay 或 connection state 的跨仓库改动，完整交付至少包括：

1. 更新 Mac 权威 protocol pack，并同步 iOS mirror/模型；
2. 在实际拥有行为的仓库增加定向测试；
3. 分别完成 MacBridge 与 iOS 的定向 build；
4. 按改动范围验证 direct、Relay、撤销、重连或 mailbox；
5. 发布前执行 secret scan。UI automation 和真机操作仍须 owner 明确授权。

## Build & test

> **Local env prerequisites**: iOS/真机与 macOS app 构建前需选择仓库要求的完整 Xcode。
> 改完 `go-bridge/` 或 `MacBridge/` 源码后，必须主动完成 Release 构建并覆盖安装到
> `/Applications`，无需用户提醒；失败时保留真实错误，不得继续使用旧 App 冒充部署成功。

There are **two independent Go modules** plus one Xcode project:

```bash
# go-bridge runtime + shared Go libs (root module: github.com/openAgi2/cordcode-macbridge)
go build ./go-bridge
go test ./go-bridge/... -count=1
go test ./go-bridge/... -run TestPaginationStableID -count=1   # single test

# relay-server is a SEPARATE module (module cordcode-relay) — cd into it
(cd relay-server && go test ./... -count=1)

# macOS app (SwiftUI). The Xcode build also compiles+embeds the Go runtime (see below).
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink \
  -configuration Debug -destination 'platform=macOS' build

# Swift unit tests (test target is CordCodeLinkTests, host = the app)
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink \
  -configuration Debug -destination 'platform=macOS' test
xcodebuild ... test -only-testing:CordCodeLinkTests/MacBridgeBehaviorTests/testSomeCase  # single test

# Unsigned Apple Silicon preview package → writes dist/*.zip + .sha256
./scripts/build-unsigned-release.sh

# 开发机覆盖安装并启动刚构建的 Release App
killall CordCodeLink 2>/dev/null || true
rm -rf /Applications/CordCodeLink.app
cp -R build/unsigned-release/Build/Products/Release/CordCodeLink.app /Applications/
open /Applications/CordCodeLink.app
```

CI (`.github/workflows/ci.yml`) has three jobs: `secret-scan` runs pinned gitleaks
on Ubuntu; `go` runs `go build`, `go test`, installs `@openai/codex` for pagination
tests, and runs `govulncheck` for both Go modules on macos-latest; `macbridge` runs
the Xcode build and unsigned Release package on macos-26 / Xcode 26.5.
Note: the root module is tested via the `go-bridge` path; `relay-server` must be tested from its own dir.

安装后至少核对：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "CordCodeLink|cordcode-bridge-runtime"
tail -n 100 "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"
```

`8777` 的监听者必须是 `/Applications/CordCodeLink.app` 内嵌的
`cordcode-bridge-runtime`，不能是旧一体仓库或当前源码目录里的开发二进制。

## Autonomous diagnosis and evidence collection

排查 bug 时，agent 必须先自行完成本机可执行的调查，再找 owner。owner 不是日志采集器、
命令转发器或实现路径选择器；除非动作需要真实设备 UI、人类账号、外部权限、视觉判断或
产品取舍，否则不得把“下一步该做什么”“请你跑命令给我日志”作为默认输出。

- 先读相关源码、活文档、`think.md` 复盘和已有测试，建立端到端事件链路假设；不要先要求
  owner 复述架构、复制日志或解释内部协议。
- Mac 侧日志、进程、端口、构建产物、Management API、runtime.json、配置文件和本仓测试，
  均由 agent 自行读取或运行。常见例子包括 `tail`/`rg`/`lsof`/`pgrep`/`curl`
  /`go test`/定向 `xcodebuild build`。
- 连接到 Mac 的 iPhone 只要能通过命令行只读取证，agent 应先自行探测 UDID 并抓取日志；
  不得默认要求 owner 打开 Terminal 复制 `idevicesyslog` 输出。只读日志采集与设备探测不等于
  UI automation；点击、输入、滑动、截图、视觉验收、真机 UI test 仍需 owner 明确授权。
- 跨 MacBridge/iOS 的端到端问题，先在可访问范围内同时对齐 MacBridge 日志、iOS 日志、
  protocol/event handler 源码和 session/turn 标识；不要只看一侧就让 owner 做人工二分。
- 只有当证据确实卡在 owner 不可替代的动作时，才向 owner 提一个最短 checklist；必须说明：
  agent 已经查了什么、还缺哪一个观察、owner 完成后回报什么结果。不得给 owner 多个工程实现
  选项来替 agent 做判断。
- 如果真实路径失败，保留失败现场并分析根因；不得用 mock、placeholder、旧日志、缓存快照或
  单侧成功冒充端到端成功。

## User-facing communication

面向 owner 汇报进展、阻塞或需要人工验收时，优先让 owner 一眼看懂“现在要做什么”，
不要把 agent 的内部执行细节、审计状态或排查过程原样抛给用户。

- 先给结论和下一步，再给证据。默认顺序是：已完成什么、卡在哪里、owner 需要做哪几步、
  owner 做完后应回报什么结果。
- owner 需要执行的动作必须写成简短、具体、可操作的 checklist；不要写成复杂工程选项，
  也不要要求 owner 理解 todo id、audit/proven 状态、端口排查、命令输出或协议细节后再决策。
- 除非选择会改变产品行为、验收标准或用户意图，否则不要让 owner 在实现路径之间做选择；
  可逆的工程细节由 agent 自行判断并继续推进。
- 内部细节可以保留在 `Evidence` / `Details` 小节，但只能作为补充。用户不应为了知道下一步
  要做什么而阅读日志、命令输出、任务队列编号或诊断推理。
- 优先使用产品语言描述人工动作。例如先说“重启 OpenCode Desktop，并确认 iPhone 上同一个
  session/history 正常”，再在证据区补充 `active server`、`lsof`、`401` 或 regression id。

## Deploying relay-server to the VPS

`relay-server/` is the public encrypted relay (`wss://relay.byteseek.uk:8443`, end-to-end
HPKE). It runs on a VPS as a **separate deployment chain** from the Mac app — committing code
here does **not** update the running relay. Code changes to `relay-server/` take effect only
after a binary update on the VPS.

### Credentials & access (one-time machine setup)

The VPS host/user/password live in **environment variables in `~/.zshrc`** (local to the dev
machine, never committed):

```bash
export CORDCODE_RELAY_VPS_HOST='<host>'
export CORDCODE_RELAY_VPS_USER='<user>'
export CORDCODE_RELAY_VPS_PASS='<password>'
```

An ssh alias is also expected in `~/.ssh/config`:

```
Host cccode-relay-prod
    HostName <host>
    User <user>
    PreferredAuthentications password
    PubkeyAuthentication no
```

The deploy script reads `CORDCODE_RELAY_VPS_PASS` and feeds it via `sshpass -e` (set `SSHPASS`)
so deployment is non-interactive. **Never commit the password** or any VPS credential.

> ⚠️ This VPS's sshd has slow banner exchange (UseDNS reverse lookup + intermittent network).
> The deploy script retries ssh/scp automatically with `ConnectTimeout`/`ConnectionAttempts`.
> Manual ssh may need a few tries; `source ~/.zshrc` first if creds are not in the shell env.

### First-time VPS setup

Full install (system user, dirs, systemd unit, nginx TLS, firewall) is documented in
[RELAY_SERVER_OPERATIONS.md](RELAY_SERVER_OPERATIONS.md). The relay listens on
`127.0.0.1:8780`, fronted by nginx `:8443` (TLS) for the public `wss://` endpoint.
The older FRP service is a separate historical deployment and must not be modified as part of
a Relay deploy.

### Routine binary update (after code changes)

```bash
# 1. 交叉编译 linux/amd64
(cd relay-server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o /tmp/cordcode-relay-server ./cmd/relay-server)

# 2. 安全部署：只读核查 → 备份 → 上传 → SHA 校验 → 原子替换 → 重启 → 健康检查
scripts/deploy-relay-vps.sh
```

`scripts/deploy-relay-vps.sh` preserves the existing binary's owner:group/mode, verifies the
uploaded SHA-256 matches the local build, and prints a one-line rollback command pointing at
the timestamped backup (`/opt/cordcode-relay/bin/relay-server.bak.<UTC>`).

After restart, the Mac's `RelayBridgeClient` reconnects automatically (PR-1 P0-B); expect a
brief `连接中` blip on iOS clients.

## Component map

| Path | Role |
| --- | --- |
| `MacBridge/` | SwiftUI macOS app. Owns the go-bridge process lifecycle, UI, settings, pairing UI. |
| `go-bridge/` | Go WebSocket runtime — the actual bridge. Entry: [go-bridge/cmd/cordcode-bridge-runtime/main.go](go-bridge/cmd/cordcode-bridge-runtime/main.go) → `gobridge.Main()` in [go-bridge/main.go](go-bridge/main.go). |
| `core/`, `config/` | Agent abstraction + config. Imported by go-bridge. |
| `agent/{claudecode,codex,opencode}` | Agent backends. Each registers itself via `init()` → `core.RegisterAgent`. |
| `transcriptindex/` | Boundary-safe transcript page index for paginated session loading (see `docs/2026-06-13-session-loading-systemic-redesign.md`). |
| `relay-server/` | **Independent Go module** for the public encrypted relay (VPS deployment). Deliberately separate per CONTRIBUTING. |
| `docs/protocol/` | Canonical protocol compatibility pack. This copy is the source of truth over the iOS repo's copy. |

## Maintainer documentation

从原一体仓库迁移并按当前拆分架构校正的长期文档：

- [构建、安装与运行态排查](BUILD_INSTALL_AND_RUNTIME.md)：Release 构建、覆盖安装、端口、日志、Management API 与常见故障。
- [go-bridge 当前架构与 backend 进程模型](GO_BRIDGE_ARCHITECTURE.md)：Claude/Codex/OpenCode 的事件与轮询边界、capability 和调试分层。
- [Relay Server 部署与运维](RELAY_SERVER_OPERATIONS.md)：独立 module 的构建、VPS 部署、验证与回滚。

涉及 iOS 连接、配对、重连或 session 同步时，同时读取相邻
`../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`；不要只看 Mac 侧推断客户端行为。

## Backend runtime model (必须理解)

iOS 只连接 Bridge `8777` / `8778` 或 Relay，不直连下面的 backend 端口。

| Backend | 运行模型 | 本地依赖 | 外部 turn 如何到 iOS |
| --- | --- | --- | --- |
| Claude Code | 每个活跃 session 一个独立 `claude` CLI 子进程，stdin/stdout stream-json | `claude` 在 runtime PATH 且已登录 | 其他 Terminal 中的 Claude 进程没有共享事件总线；iOS 必须用历史变化 polling 旁观 |
| Codex | 产品默认 `app_server` 模式；默认 stdio 启动每个 session 的 app-server，显式 URL 时连接共享 WebSocket service | app-server 可启动/可连接；`codex exec` CLI 只属于 `exec` backend | 默认由 iOS 历史变化 polling 旁观外部 turn；显式共享 URL 时 `EventSubscriber` 被动订阅并广播 |
| OpenCode | CordCode Link 默认托管 loopback-only `opencode serve`，Bridge 同时使用 agent 与 HTTP proxy | 新装默认 `managed_local`（端口 `4096...4196`，Basic Auth 写入 `opencode-managed-server.json`）；`64667` 仅为存量 legacy source | 有 resolved URL 时订阅 `/global/event` SSE；无 URL 时 fail-closed 不退避重连 64667，iOS 保留低频 polling 兜底 |

### Codex app-server

产品 `RuntimeConfig` 默认传：

```text
-codex-backend app_server
```

不要把 Codex backend 误判成“必须安装/查找 `codex exec` CLI”。当前有两个不同概念：

- **app-server backend（产品默认）**：Bridge 讲 JSON-RPC app-server 协议。未显式配置
  `-codex-app-server-url` 时使用 stdio transport 启动 `codex app-server`；显式 URL 时连接已有
  共享 WebSocket service。
- **exec backend（历史/显式模式）**：Bridge 才运行 `codex exec --json`，这一路才需要把
  `exec.LookPath("codex")` / `codex --version` 作为 required CLI 检查。

因此，后续 agent 修改 `agent/codex`、diagnostics、capability 或测试时必须遵守：

- `RunDiagnostics` 在 `app_server` 模式下不得运行 `cli` required check；`codex not found` 不能让
  app-server-only deployment 的 `OverallStatus` 变成 `failed`。
- 看到 `codex CLI not found` / `codex not found` 测试失败时，先确认 backend mode。若是
  `app_server`，优先检查诊断 gating 或 app-server 连接/stdio 启动路径，不要先让 owner 安装
  `@openai/codex` 来“修环境”。
- 只有 `exec` backend、明确覆盖 `codex exec` 的 integration/pagination 测试，或 stdio app-server
  启动路径本身失败时，才把本机 `codex` 可执行文件当作相关依赖。即便如此，也要把问题描述为
  “app-server launcher/exec backend dependency”，不要笼统写成“MacBridge 需要 codex CLI”。

若显式配置共享 URL，则产品态期待 Mac 上已有共享 Codex app-server，Bridge 是客户端和被动订阅者，
不应再启动第二个竞争性的 TCP app-server。排查共享模式：

```bash
lsof -nP -iTCP:4141 -sTCP:LISTEN
ps aux | grep '[c]odex app-server'
```

排查 stdio app-server 启动路径时才检查 launcher：

```bash
command -v codex
codex app-server --help >/dev/null 2>&1 || true
```

Codex lazy create 可能先返回
`pending-*`，第一次 send 后必须把 registry 与订阅 rebind 到真实 thread id。

### OpenCode server

新装默认 **Automatic / managed_local**：CordCode Link 自己启动并保活
loopback-only `opencode serve`，从 `4096...4196` 选择端口，生成随机 Basic Auth，
写入 data dir 的 `opencode-managed-server.json`（`0600`），并同步 OpenCode Desktop
配置。存量 `credentials.json` 只用于用户显式 source、外部 URL 和 legacy `64667`
兼容迁移。Bridge runtime 只接收 Swift 端解析出的 `-opencode-url` 与凭据；没有 resolved
URL 时 backend 报 `not_configured`，不得回落硬连 64667。排查：

```bash
cat "$HOME/Library/Application Support/CordCode Link/opencode-managed-server.json"
lsof -nP -iTCP:<managed-port> -sTCP:LISTEN
curl -i --max-time 3 http://127.0.0.1:<managed-port>/global/health
```

no-auth `/global/health` 返回 `401` 表示 server 要求认证，可继续做 authed 校验；
no-auth `200` 的 OpenCode server 会被判为 `server_unauthenticated` 并拒绝
（`legacy_64667` 例外，但会标 `legacy_insecure_unverified`）。OpenCode 的
create/resume/get/abort/list projects 等 server 专属语义仍可走
`go-bridge/opencode-proxy.go`，实时外部事件走 `agent/opencode/sse_subscriber.go`。

### Claude Code

Claude 没有共享 server 端口。Bridge 启动/恢复自己的 CLI session，只能直接收到该子进程
的 stdout 事件；用户在另一个 Terminal 发起的 Claude turn 只能通过共享 JSONL 历史被发现。
因此不能照搬 Codex/OpenCode 的“收到广播后停止 polling”策略。

Backend capability 由 `core/interfaces.go` 的可选接口推导，并在 `hello_ack.backends[]`
下发；不要维护脱离源码的手写能力真值表。完整细节见
[GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md)。

## Architecture concepts

### How the Go runtime is embedded in the Mac app

The committed `MacBridge/CordCodeLink.xcodeproj/project.pbxproj` is **generated by XcodeGen** from
[MacBridge/project.yml](MacBridge/project.yml) (requires XcodeGen ≥ 2.38.0; Swift 5.9, macOS 14.0, arm64).
A `preBuildScripts` entry runs `go build` cross-compiled to the target arch and injects version metadata via
`-ldflags -X` (`runtimeVersion`, `runtimeCommit`, `runtimeDate`), dropping the binary at
`Contents/Resources/cordcode-bridge-runtime`. If you add/change Go entry symbols or ldflag variable names,
update both the build script in `project.yml` and `go-bridge/runtime_version.go`.

### Swift ↔ Go handoff

The Mac app launches `cordcode-bridge-runtime` as a child `Process` ([RuntimeManager.swift](MacBridge/MacBridge/Services/RuntimeManager.swift)).
The runtime announces readiness by writing a **ready frame** to stdout and `runtime.json` in the data dir
(`~/Library/Application Support/CordCode Link/`), which includes `port`, `pid`, `managementUrl`, and `bridgeEpoch`.
`RuntimeManager` polls `runtime.json` + the `management-token` file, then drives the runtime via the
local Management API. It handles crash/auto-restart, sleep/wake, and stale-port-takeover. App config changes
(remote URL, OpenCode creds, relay route) apply by mutating `RuntimeConfig` and calling `restart()`.

### Three network surfaces in go-bridge

1. **Bridge WebSocket** (`:8777`, plus `:8778` TLS for wss Tailscale): the `cordcode-bridge` v1 protocol — handshake (`hello`/`hello_ack`), RPC, events. iOS clients connect here directly.
2. **Management API** (`127.0.0.1:<random>`, `/internal/*`, token-auth): local-only control surface for the Mac app — status, agents, pairing create/approve/reject, device list/revoke, relay prekeys, shutdown. See [go-bridge/management_api.go](go-bridge/management_api.go).
3. **Relay** (`cccode-relay` v1): end-to-end-encrypted (HPKE) opaque envelopes routed through `relay-server`. The relay never sees plaintext. MacBridge provisions a route via an Ed25519 activation identity persisted under the app data directory with `0600` file permissions ([RuntimeManager.swift](MacBridge/MacBridge/Services/RuntimeManager.swift), `OfficialRelayProvisioner`).

#### 三条远程连接路径与 TLS pin 的关系

| 路径 | 用途 | TLS 保护 | 是否用 TLS pin |
|---|---|---|---|
| **Relay**（默认） | 经公网中继 `wss://relay...`，HPKE 端到端加密 | 正规 CA 证书 + 系统信任 | ❌ 不需要 |
| **局域网** | 同一 WiFi 直连 `ws://192.168.x.x` | 无 TLS（局域网可信） | ❌ 不需要 |
| **Tailscale**（隐藏备选） | 经 Tailscale 隧道 `wss://100.x.x.x:8778` | MacBridge 自签名证书 | ✅ 需要 |

**Relay 是默认且推荐的远程连接方式**——开箱即用，无需额外软件。Tailscale 是隐藏的备选方案，需要用户在 Mac 和 iPhone 两端都安装 Tailscale 客户端，反而更麻烦，仅在 relay 不可用的特殊场景下才有意义。

**TLS pin 只是给 Tailscale 那条较弱的安全路径补的课。** Relay 路径本身就有正规 CA + HPKE 端到端加密，不需要 pin；局域网无 TLS 也不需要。go-bridge 在检测到 Tailscale IP 时（`resolveTailscaleRemote`）生成持久化自签名证书（`<dataDir>/tls-cert.json`，跨重启稳定）并经 `pairing_complete` 下发 SPKI pin（`BridgeV1TLSPin`）。iOS 据此校验 Tailscale 证书、拒绝伪造。证书持久化 + pin 派生逻辑见 [tls_cert_store.go](go-bridge/tls_cert_store.go)；日常走 relay 的用户不会触发 pin 代码路径（无 Tailscale IP → 不生成证书 → 不下发 pin）。

### Agent abstraction (core/interfaces.go)

`Agent` is the base interface (`StartSession`/`ListSessions`/`Stop`). Capabilities are **opt-in interfaces**
(`ProviderSwitcher`, `ModelSwitcher`, `MemoryFileProvider`, `HistoryProvider`/`RichHistoryProvider`,
`DiagnosticsProvider`, `TranscriptLocator`, `SessionEnvInjector`, `LiveModeSwitcher`, etc.) discovered by
type assertion. When adding a backend capability, add the interface in `core/`, implement in the relevant
`agent/*`, and gate the wire handler on the type assertion.

### Protocol versioning

`hello.protocol.version` is the canonical major-version negotiation field for new clients; `register` is a
legacy path kept backward-compatible. Non-breaking additions use optional fields; changing field meaning
requires a new major version. When protocol changes, update `docs/protocol/` and the iOS compatibility notes
together. Canonical versions are tracked in [docs/protocol/README.md](docs/protocol/README.md).

## Conventions (from CONTRIBUTING / SECURITY)

- Runtime logic belongs in `core/`, `config/`, `agent/`; wire protocol adaptation belongs in `go-bridge/`.
- `relay-server/` stays a separate Go module unless a deliberate migration decision changes that boundary.
- **Do not add fallback/mock paths to production runtime code to hide real failures.**
- Never commit credentials, route IDs, provisioning tokens, passwords, private keys, or Apple Team IDs.
  Only the documented public Relay endpoint may be committed (it's in `project.yml` Info.plist properties).
- UI automation and real-device validation require explicit owner approval.
- 始终用中文回复用户。
- 日志路径为 `~/Library/Application Support/CordCode Link/logs/go-bridge.log`（不再使用 `/tmp`，P2-8）。runtime 重启会重新打开日志文件；MacBridge 会按大小滚动（`maxLogBytes` 8MiB，保留 3 代）。日志从某时刻突然重新开始可能是 120min 定时兜底重启（`autoRestartIntervalMinutes` 默认 120），也可能是 `.starting` 卡住 60s 后的 supervisor 自愈，非必然 bug。排查时用 `tail -f ~/Library/Application\ Support/CordCode\ Link/logs/go-bridge.log | tee /tmp/evidence.log` 镜像，或临时关 `autoRestartEnabled`。
- **CHANGELOG.md**：每轮对外可见的改动完成后，在 `[Unreleased]` 下按现有格式追加一节（日期 — 主题），记录「改了什么 / 有何提升」。发布正式版时把 `[Unreleased]` 改为版本号与日期。
