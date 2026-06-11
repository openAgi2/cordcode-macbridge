# Public Readiness Report

Date: 2026-06-11

This repository is a public-candidate MacBridge repository created by clean source import. It is not marked ready to publish until the owner confirms signing, release, remote secret scanning, and final manual product validation.

## Scope

Included:

- MacBridge macOS app.
- `go-bridge` runtime adapter.
- `relay-server` as an independent Go module.
- Minimal `core/`, `config/`, and `agent/` code copied from local `cc-connect` for Claude Code, OpenCode, and Codex support.
- Protocol compatibility docs under `docs/protocol/`.

Excluded:

- iOS app and `message-web`.
- `core/engine.go`, `core/streaming.go`, and `agent/acp/`.
- Handoffs, `.exec-plan`, internal completion reports, private deploy artifacts, and real deployment configuration.

## Added Repository Files

- `README.md`
- `LICENSE` (`AGPL-3.0-only`)
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.gitignore`
- `.github/workflows/ci.yml`
- `docs/protocol/`
- `docs/backends-and-config.md`
- `docs/signing-and-release.md`
- `config.example.env`
- `.gitleaks.toml`

## Secret And Private Marker Scan

Command class:

```bash
rg -n "<known private marker alternation>" . --glob '!.git/**'
rg -n "(API[_-]?KEY|SECRET|TOKEN|PASSWORD|PRIVATE[_-]?KEY|PROVISION).*[=:][[:space:]]*[A-Za-z0-9_./+=-]{12,}" . --glob '!.git/**'
```

Result:

- No known real relay endpoint, VPS IP, Apple team ID, private source path, handoff, or exec-plan marker was found.
- One password-pattern hit remains in `MacBridge/MacBridge/Services/RuntimeManager.swift`; it assigns the user-provided OpenCode password from runtime configuration into the child process environment and is not a hardcoded secret.

## CI

CI is configured for pull requests and pushes to `main`:

- Gitleaks secret scan.
- `go build ./go-bridge`
- `go test ./go-bridge/... -count=1`
- `(cd relay-server && go test ./... -count=1)`
- MacBridge Debug macOS `xcodebuild`

## Remote Verification

- Private remote: `https://github.com/openAgi2/cccode-macbridge`
- Default branch: `main`
- Fresh-clone Gitleaks Git-history scan: passed.
- Fresh-clone Gitleaks working-tree scan: passed.

## Remaining Owner Decisions Before Public Release

- Decide final bundle identifier, signing identity, hardened runtime, and notarization flow.
- Decide the production Relay endpoint release-time injection policy.
- Decide the notarized direct-download or other distribution release channel.
