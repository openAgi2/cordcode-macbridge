# Contributing

Keep changes focused and reviewable.

## Development Rules

- Runtime logic belongs in `core/`, `config/`, and `agent/`.
- Wire protocol adaptation belongs in `go-bridge/`.
- `relay-server/` stays an independent Go module unless a separate migration
  decision changes that boundary.
- Do not add fallback/mock paths to production runtime code to hide real
  failures.
- Do not commit local credentials, deployment configs, DerivedData, or build
  artifacts.

## Required Checks

```bash
go build ./go-bridge
go test ./go-bridge/... -count=1
(cd relay-server && go test ./... -count=1)
xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' build
```

UI automation and real-device validation require explicit owner approval.
