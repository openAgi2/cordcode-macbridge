# Development

Current development happens in the split repositories.

## iOS Client

The `cccode-ios` repository contains the iOS application and message renderer.
A typical validation sequence is:

```bash
cd message-web
npm ci
npm run build:ios
npm run test
cd ..

xcodebuild \
  -project OpenCodeiOS/CCCode.xcodeproj \
  -scheme CCCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  build
```

Run focused unit tests for changed behavior. Changes to pairing, reconnection,
session switching, model selection, streaming, or network recovery also require
real-device regression before release.

## MacBridge

The `cccode-macbridge` repository contains the macOS application, bridge
runtime, agent/core packages, and Relay server.

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1

(cd relay-server && go test ./... -count=1)

xcodebuild \
  -project MacBridge/CCCodeBridge.xcodeproj \
  -scheme CCCodeBridge \
  -configuration Debug \
  -destination 'platform=macOS' \
  build
```

Use the exact commands documented by the repository when its project settings
or supported destinations change.

## Cross-Repository Changes

Protocol, pairing, encryption, Relay, and connection-state changes usually
affect both repositories. A complete change should:

1. update the wire models and protocol documents on both sides;
2. add focused tests in the owning repository;
3. build both applications;
4. verify direct and Relay paths as applicable;
5. run secret scanning before publishing.

## Current Runtime Scope

The migrated MacBridge runtime supports Claude Code, OpenCode, and Codex.
Historical references to additional backends in this legacy repository do not
guarantee current support.

## Release Configuration

The default Relay endpoint is injected into MacBridge as public build
configuration. Device credentials and route credentials remain runtime data.
Code signing, provisioning, notarization, and release-channel credentials must
be managed outside source control.
