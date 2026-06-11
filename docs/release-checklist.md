# Release Checklist

This checklist is for preparing a MacBridge release from the split repository.

## Preflight

- Confirm `LICENSE` remains AGPL-3.0-only.
- Confirm bundle identifier and signing team are correct for the release channel.
- Confirm no real relay endpoint, token, private path, or deployment secret is committed.
- Run a dedicated secret scan on the final remote default branch.

Current private-remote verification (2026-06-11):

- `openAgi2/cccode-macbridge`, default branch `main`.
- Fresh-clone Gitleaks Git-history and working-tree scans passed.

## Build Gate

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1
(cd relay-server && go test ./... -count=1)
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' build
```

## Bundle Gate

```bash
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' -showBuildSettings
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
