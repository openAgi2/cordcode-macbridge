# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

CCCode MacBridge is the **macOS companion** for CCCode. It exposes locally-installed
AI coding agent backends (Claude Code CLI, OpenCode server, Codex app-server) to iPhone/iPad
clients over a direct LAN WebSocket or an end-to-end-encrypted public Relay. This repo is the
**Mac side only**; the iOS client lives in a separate repo.

## New-session bootstrap

新 session 不能只读本文件中的摘要就开始修改运行链路。按任务范围先读根目录活文档：

- 相邻旧一体仓库 `../opencode-cc-connect/` 只作为历史设计和迁移证据；当前实现、命令、
  协议与支持范围以本仓库和 `../cccode-ios/` 为准。不得从旧文档整段复制配置而不反查源码。
- 修改 Mac app、runtime 生命周期、构建安装、端口或日志：必须先读
  [BUILD_INSTALL_AND_RUNTIME.md](BUILD_INSTALL_AND_RUNTIME.md)。
- 修改 agent driver、Codex/OpenCode/Claude 事件、session、history、polling 或 capability：
  必须先读 [GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md)。
- 修改 `relay-server/`、VPS 部署、mailbox、route、HPKE 或生产 Relay：
  必须先读 [RELAY_SERVER_OPERATIONS.md](RELAY_SERVER_OPERATIONS.md)。
- 修改与 iOS 的配对、hello、重连、撤销、session/turn 同步：同时读
  `../cccode-ios/IOS_MAC_INTERACTION_FLOW.md`。

这些是持续更新的架构/运维真值；`docs/YYYY-MM-DD-*.md` 主要是方案、评审和完成报告，
不能代替根目录活文档。

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
# go-bridge runtime + shared Go libs (root module: github.com/openAgi2/cccode-macbridge)
go build ./go-bridge
go test ./go-bridge/... -count=1
go test ./go-bridge/... -run TestPaginationStableID -count=1   # single test

# relay-server is a SEPARATE module (module cccode-relay) — cd into it
(cd relay-server && go test ./... -count=1)

# macOS app (SwiftUI). The Xcode build also compiles+embeds the Go runtime (see below).
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge \
  -configuration Debug -destination 'platform=macOS' build

# Swift unit tests (test target is CCCodeBridgeTests, host = the app)
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge \
  -configuration Debug -destination 'platform=macOS' test
xcodebuild ... test -only-testing:CCCodeBridgeTests/MacBridgeBehaviorTests/testSomeCase  # single test

# Unsigned Apple Silicon preview package → writes dist/*.zip + .sha256
./scripts/build-unsigned-release.sh

# 开发机覆盖安装并启动刚构建的 Release App
killall CCCodeBridge 2>/dev/null || true
rm -rf /Applications/CCCodeBridge.app
cp -R build/unsigned-release/Build/Products/Release/CCCodeBridge.app /Applications/
open /Applications/CCCodeBridge.app
```

CI (`.github/workflows/ci.yml`) runs gitleaks, `go test` on macos-latest, and the Xcode build.
Note: the root module is tested via the `go-bridge` path; `relay-server` must be tested from its own dir.

安装后至少核对：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "CCCodeBridge|cccode-bridge-runtime"
tail -n 100 "$HOME/Library/Application Support/CCCode Bridge/logs/go-bridge.log"
```

`8777` 的监听者必须是 `/Applications/CCCodeBridge.app` 内嵌的
`cccode-bridge-runtime`，不能是旧一体仓库或当前源码目录里的开发二进制。

## Deploying relay-server to the VPS

`relay-server/` is the public encrypted relay (`wss://relay.byteseek.uk:8443`, end-to-end
HPKE). It runs on a VPS as a **separate deployment chain** from the Mac app — committing code
here does **not** update the running relay. Code changes to `relay-server/` take effect only
after a binary update on the VPS.

### Credentials & access (one-time machine setup)

The VPS host/user/password live in **environment variables in `~/.zshrc`** (local to the dev
machine, never committed):

```bash
export CCCODE_RELAY_VPS_HOST='<host>'
export CCCODE_RELAY_VPS_USER='<user>'
export CCCODE_RELAY_VPS_PASS='<password>'
```

An ssh alias is also expected in `~/.ssh/config`:

```
Host cccode-relay-prod
    HostName <host>
    User <user>
    PreferredAuthentications password
    PubkeyAuthentication no
```

The deploy script reads `CCCODE_RELAY_VPS_PASS` and feeds it via `sshpass -e` (set `SSHPASS`)
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
  go build -trimpath -ldflags='-s -w' -o /tmp/cccode-relay-server ./cmd/relay-server)

# 2. 安全部署：只读核查 → 备份 → 上传 → SHA 校验 → 原子替换 → 重启 → 健康检查
scripts/deploy-relay-vps.sh
```

`scripts/deploy-relay-vps.sh` preserves the existing binary's owner:group/mode, verifies the
uploaded SHA-256 matches the local build, and prints a one-line rollback command pointing at
the timestamped backup (`/opt/cccode-relay/bin/relay-server.bak.<UTC>`).

After restart, the Mac's `RelayBridgeClient` reconnects automatically (PR-1 P0-B); expect a
brief `连接中` blip on iOS clients.

## Component map

| Path | Role |
| --- | --- |
| `MacBridge/` | SwiftUI macOS app. Owns the go-bridge process lifecycle, UI, settings, pairing UI. |
| `go-bridge/` | Go WebSocket runtime — the actual bridge. Entry: [go-bridge/cmd/cccode-bridge-runtime/main.go](go-bridge/cmd/cccode-bridge-runtime/main.go) → `gobridge.Main()` in [go-bridge/main.go](go-bridge/main.go). |
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
`../cccode-ios/IOS_MAC_INTERACTION_FLOW.md`；不要只看 Mac 侧推断客户端行为。

## Backend runtime model (必须理解)

iOS 只连接 Bridge `8777` / `8778` 或 Relay，不直连下面的 backend 端口。

| Backend | 运行模型 | 本地依赖 | 外部 turn 如何到 iOS |
| --- | --- | --- | --- |
| Claude Code | 每个活跃 session 一个独立 `claude` CLI 子进程，stdin/stdout stream-json | `claude` 在 runtime PATH 且已登录 | 其他 Terminal 中的 Claude 进程没有共享事件总线；iOS 必须用历史变化 polling 旁观 |
| Codex | 产品默认 `app_server` 模式，共享 WebSocket JSON-RPC service | `codex` 已登录；默认 URL `ws://127.0.0.1:4141` | `EventSubscriber` 被动订阅 `turn/started`、delta、plan、completed，并通过 broadcaster 推送 |
| OpenCode | OpenCode Desktop HTTP + SSE service，Bridge 同时使用 agent 与 HTTP proxy | 默认 `http://127.0.0.1:64667`，Basic Auth 由 MacBridge 管理 | `/global/event` SSE passive subscriber 广播；iOS 仍保留 polling 兜底 |

### Codex app-server

产品 `RuntimeConfig` 默认传：

```text
-codex-backend app_server
-codex-app-server-url ws://127.0.0.1:4141
```

因此产品态期待 Mac 上已有共享 Codex app-server。Bridge 是客户端和被动订阅者，不应再启动
第二个竞争性的 TCP app-server。排查：

```bash
command -v codex
lsof -nP -iTCP:4141 -sTCP:LISTEN
ps aux | grep '[c]odex app-server'
```

若显式 URL 未配置，`agent/codex` 也支持由 session 通过 stdio 启动 app-server；不要把这一
开发/嵌入模式和产品默认的共享 `4141` 模式混在一起。Codex lazy create 可能先返回
`pending-*`，第一次 send 后必须把 registry 与订阅 rebind 到真实 thread id。

### OpenCode server

MacBridge 首次启动会复用现有 `com.opencode.server` LaunchAgent 凭据，或生成随机本地凭据，
保存到 data dir 的 `credentials.json` 并同步 OpenCode Desktop 配置。Bridge runtime 通过
环境注入取得凭据；控制面凭据不得传给 agent/tool 子进程。排查：

```bash
lsof -nP -iTCP:64667 -sTCP:LISTEN
curl -i --max-time 3 http://127.0.0.1:64667/health
```

`401` 表示服务存在但凭据不同步，不是健康成功。OpenCode 的 create/resume/get/abort 等
server 专属语义仍可走 `go-bridge/opencode-proxy.go`，实时外部事件走
`agent/opencode/sse_subscriber.go`。

### Claude Code

Claude 没有共享 server 端口。Bridge 启动/恢复自己的 CLI session，只能直接收到该子进程
的 stdout 事件；用户在另一个 Terminal 发起的 Claude turn 只能通过共享 JSONL 历史被发现。
因此不能照搬 Codex/OpenCode 的“收到广播后停止 polling”策略。

Backend capability 由 `core/interfaces.go` 的可选接口推导，并在 `hello_ack.backends[]`
下发；不要维护脱离源码的手写能力真值表。完整细节见
[GO_BRIDGE_ARCHITECTURE.md](GO_BRIDGE_ARCHITECTURE.md)。

## Architecture concepts

### How the Go runtime is embedded in the Mac app

The committed `MacBridge/CCCodeBridge.xcodeproj/project.pbxproj` is **generated by XcodeGen** from
[MacBridge/project.yml](MacBridge/project.yml) (requires XcodeGen ≥ 2.38.0; Swift 5.9, macOS 14.0, arm64).
A `preBuildScripts` entry runs `go build` cross-compiled to the target arch and injects version metadata via
`-ldflags -X` (`runtimeVersion`, `runtimeCommit`, `runtimeDate`), dropping the binary at
`Contents/Resources/cccode-bridge-runtime`. If you add/change Go entry symbols or ldflag variable names,
update both the build script in `project.yml` and `go-bridge/runtime_version.go`.

### Swift ↔ Go handoff

The Mac app launches `cccode-bridge-runtime` as a child `Process` ([RuntimeManager.swift](MacBridge/MacBridge/Services/RuntimeManager.swift)).
The runtime announces readiness by writing a **ready frame** to stdout and `runtime.json` in the data dir
(`~/Library/Application Support/CCCode Bridge/`), which includes `port`, `pid`, and `managementUrl`.
`RuntimeManager` polls `runtime.json` + the `management-token` file, then drives the runtime via the
local Management API. It handles crash/auto-restart, sleep/wake, and stale-port-takeover. App config changes
(remote URL, OpenCode creds, relay route) apply by mutating `RuntimeConfig` and calling `restart()`.

### Three network surfaces in go-bridge

1. **Bridge WebSocket** (`:8777`, plus `:8778` TLS for wss Tailscale): the `cccode-bridge` v1 protocol — handshake (`hello`/`hello_ack`), RPC, events. iOS clients connect here directly.
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
- 日志路径为 `~/Library/Application Support/CCCode Bridge/logs/go-bridge.log`（不再使用 `/tmp`，P2-8）。runtime 重启会重新打开日志文件；MacBridge 会按大小滚动（`maxLogBytes` 8MiB，保留 3 代）。日志从某时刻突然重新开始 = MacBridge 120min 定时兜底重启覆盖（`autoRestartIntervalMinutes` 默认 120），非 bug。排查时用 `tail -f ~/Library/Application\ Support/CCCode\ Bridge/logs/go-bridge.log | tee /tmp/evidence.log` 镜像，或临时关 `autoRestartEnabled`。
- **CHANGELOG.md**：每轮对外可见的改动完成后，在 `[Unreleased]` 下按现有格式追加一节（日期 — 主题），记录「改了什么 / 有何提升」。发布正式版时把 `[Unreleased]` 改为版本号与日期。
