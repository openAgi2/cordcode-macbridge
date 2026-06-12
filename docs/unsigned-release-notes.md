# Unsigned Preview

This prerelease is an Apple Silicon test build of CCCode MacBridge.

It is ad-hoc signed for local execution and has not been notarized by Apple.
macOS will therefore require the user to control-click the app and choose
**Open** on first launch. Do not disable Gatekeeper globally.

See `docs/install-macos.md` in the repository for installation, checksum,
first-launch, and uninstall instructions.

Supported runtime adapters:

- Claude Code
- OpenCode
- Codex

The default encrypted Relay endpoint is `wss://relay.byteseek.uk:8443`.
MacBridge also supports a user-configured compatible Relay.
