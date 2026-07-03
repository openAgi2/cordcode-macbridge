# CordCode MacBridge

This repo is the **Mac-side bridge aggregate** for CordCode: the desktop app,
its embedded runtime, the public Relay server source, and the agent drivers —
all in one repo. The macOS app users see is **CordCode Link**; the iOS client
lives in a separate repo (`cordcode-ios`).

## Contents

| Subsystem | What it is | Product / deployment |
| --- | --- | --- |
| `MacBridge/` | macOS SwiftUI app | **CordCode Link** (display name), `org.openagi.cordcode.link` |
| `go-bridge/` | local WebSocket runtime + Relay crypto (connector side) | embedded in CordCode Link as `cordcode-bridge-runtime` |
| `relay-server/` | public encrypted Relay **server** (independent Go module `cordcode-relay`) | deployed on a VPS, **not** part of the Mac app |
| `agent/{claudecode,codex,opencode}/` | per-agent drivers | embedded in CordCode Link |
| `core/`, `config/`, `transcriptindex/` | shared Go libs | imported across subsystems |
| `docs/protocol/` | canonical protocol compatibility pack | — |

`relay-server/` is a separately deployed server (runs on a VPS at the public
Relay endpoint) — distinct from CordCode Link which runs on the user's Mac.
That is why this repo is named `*-macbridge` (the whole Mac-side bridge
family) rather than `*-link` (which would mislabel the Relay server source).

## Backend Requirements

MacBridge adapts locally installed agent backends. Users must install and
authenticate the backends they want to expose:

- Claude Code CLI.
- OpenCode server.
- Codex app-server.

See `docs/backends-and-config.md` for configuration surfaces and placeholder
environment examples. The default hosted Relay endpoint is public configuration;
route credentials, pairing tokens, management tokens, and OpenCode passwords are
generated or stored locally and are not committed.

## Build

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1
(cd relay-server && go test ./... -count=1)
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' build
```

After the Xcode build, verify the embedded runtime:

```bash
BUILT_PRODUCTS_DIR=$(xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' -showBuildSettings | awk -F'= ' '/ BUILT_PRODUCTS_DIR = / {print $2; exit}')
test -x "$BUILT_PRODUCTS_DIR/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime"
```

## Install A Preview Build

Unsigned Apple Silicon preview packages can be produced without a paid Apple
Developer account:

```bash
./scripts/build-unsigned-release.sh
```

The archive and SHA-256 checksum are written to `dist/`. See
`docs/install-macos.md` for the new-user installation flow and expected
Gatekeeper warning.

## Protocol

Direct bridge and relay compatibility are documented in `docs/protocol/`.
`hello.protocol.version` is the canonical major version negotiation field for
new clients.

## Release Status

This repository is public and licensed under AGPL-3.0-only. Unsigned preview
packages can be distributed through GitHub prereleases. A warning-free public
release still requires Developer ID signing and Apple notarization. See
`docs/signing-and-release.md`, `docs/public-readiness.md`, and `PRIVACY.md`.
