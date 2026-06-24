# Release Checklist

This checklist is for preparing a MacBridge release from the split repository.

## Preflight

- Confirm `LICENSE` remains AGPL-3.0-only.
- Confirm bundle identifier and signing team are correct for the release channel.
- Confirm only the documented public Relay endpoint is committed; no route
  credential, token, private path, or deployment secret may be present.
- Run a dedicated secret scan on the final remote default branch.

Current public-remote verification (2026-06-12):

- `openAgi2/cordcode-macbridge`, default branch `main`.
- Gitleaks Git-history and working-tree scans passed before public visibility
  was enabled.

## Build Gate

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1
(cd relay-server && go test ./... -count=1)
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' build
./scripts/build-unsigned-release.sh
```

## Bundle Gate

```bash
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' -showBuildSettings
test -x "$BUILT_PRODUCTS_DIR/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime"
```

## Manual Product Gate

- Launch MacBridge and confirm the runtime starts.
- Pair a real iOS device.
- Verify direct WebSocket connectivity.
- Verify relay connectivity.
- Verify offline mailbox replay.
- Verify revoke blocks the device.
- Verify Claude Code, OpenCode, and Codex each complete one basic request on the intended release machine.

## Unsigned Preview Gate

- Confirm the artifact filename includes `unsigned`.
- Verify the SHA-256 sidecar.
- Install after removing the previous app and local MacBridge state.
- Confirm first launch starts the embedded runtime and creates a new Relay
  route.
- Confirm documentation explains the expected Gatekeeper warning without
  recommending a global security bypass.
