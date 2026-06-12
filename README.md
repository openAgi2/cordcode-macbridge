# CCCode MacBridge

MacBridge is the macOS companion for CCCode. It runs the desktop app, the local
bridge runtime, and the optional relay service used by iPhone and iPad clients.

## Contents

- `MacBridge/`: macOS app project.
- `go-bridge/`: local WebSocket runtime adapter embedded as `cccode-bridge-runtime`.
- `core/`, `config/`, `agent/`: product-scoped agent runtime code imported from the former `cc-connect` dependency.
- `relay-server/`: independent Go module for the public encrypted relay service.
- `docs/protocol/`: canonical protocol compatibility pack.

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
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' build
```

After the Xcode build, verify the embedded runtime:

```bash
BUILT_PRODUCTS_DIR=$(xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' -showBuildSettings | awk -F'= ' '/ BUILT_PRODUCTS_DIR = / {print $2; exit}')
test -x "$BUILT_PRODUCTS_DIR/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime"
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
