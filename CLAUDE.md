# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

CCCode MacBridge is the **macOS companion** for CCCode. It exposes locally-installed
AI coding agent backends (Claude Code CLI, OpenCode server, Codex app-server) to iPhone/iPad
clients over a direct LAN WebSocket or an end-to-end-encrypted public Relay. This repo is the
**Mac side only**; the iOS client lives in a separate repo.

## Build & test

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
```

CI (`.github/workflows/ci.yml`) runs gitleaks, `go test` on macos-latest, and the Xcode build.
Note: the root module is tested via the `go-bridge` path; `relay-server` must be tested from its own dir.

## Deploying relay-server to the VPS

`relay-server/` is the public encrypted relay (`wss://relay.byteseek.uk:8443`, end-to-end
HPKE). It runs on a VPS as a **separate deployment chain** from the Mac app — committing code
here does **not** update the running relay. Code changes to `relay-server/` take effect only
after a binary update on the VPS.

### Credentials & access (one-time machine setup)

The VPS host/user/password live in **environment variables in `~/.zshrc`** (local to the dev
machine, never committed):

```bash
export CCCODE_RELAY_VPS_HOST=47.236.182.45
export CCCODE_RELAY_VPS_USER=root
export CCCODE_RELAY_VPS_PASS='<password>'   # relay VPS root password
```

An ssh alias is also expected in `~/.ssh/config`:

```
Host cccode-relay-prod
    HostName 47.236.182.45
    User root
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
`relay-server-install.md` (kept outside this repo). The relay listens on `127.0.0.1:8780`,
fronted by nginx `:8443` (TLS) for the public `wss://` endpoint. (The same VPS also runs the
older frp tunnel at nginx `:9090`; do not touch it during relay deploys.)

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
3. **Relay** (`cccode-relay` v1): end-to-end-encrypted (HPKE) opaque envelopes routed through `relay-server`. The relay never sees plaintext. MacBridge provisions a route via an Ed25519 activation identity stored in Keychain ([RuntimeManager.swift](MacBridge/MacBridge/Services/RuntimeManager.swift), `OfficialRelayProvisioner`).

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
