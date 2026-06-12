# Public Readiness Report

Date: 2026-06-12

This repository is public. It was created by clean source import and passed
working-tree and complete-history secret scanning before public visibility was
enabled.

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

- The public default Relay endpoint is intentionally committed as non-secret configuration.
- No Relay credential, VPS IP, Apple team ID, private source path, handoff, or exec-plan marker was found.
- One password-pattern hit remains in `MacBridge/MacBridge/Services/RuntimeManager.swift`; it assigns the user-provided OpenCode password from runtime configuration into the child process environment and is not a hardcoded secret.

## CI

CI is configured for pull requests and pushes to `main`:

- Gitleaks secret scan.
- `go build ./go-bridge`
- `go test ./go-bridge/... -count=1`
- `(cd relay-server && go test ./... -count=1)`
- MacBridge Debug macOS `xcodebuild`

## Remote Verification

- Public remote: `https://github.com/openAgi2/cccode-macbridge`
- Default branch: `main`
- Gitleaks Git-history scan on all 10 commits: passed on 2026-06-12.
- Gitleaks working-tree scan: passed on 2026-06-12.
- Anonymous GitHub API access returned HTTP 200 after publication.

## Available Release Channel

- Ad-hoc signed Apple Silicon preview zip.
- SHA-256 checksum sidecar.
- GitHub prerelease workflow.
- New-user installation and uninstall documentation.

## Remaining Owner Decisions For A Notarized Release

- Decide final bundle identifier, signing identity, hardened runtime, and notarization flow.
- Decide the notarized direct-download or other distribution release channel.
