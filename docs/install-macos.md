# Install On macOS

The current downloadable build is an unsigned Apple Silicon preview. It is
intended for testing before Developer ID signing and Apple notarization are
available.

## Install

1. Download the `macos-arm64-unsigned.zip` file and its `.sha256` file from the
   GitHub prerelease.
2. Verify the checksum:

   ```bash
   shasum -a 256 -c CCCodeBridge-*-macos-arm64-unsigned.zip.sha256
   ```

3. Unzip the archive and move `CCCodeBridge.app` to `/Applications`.
4. Control-click the app, choose **Open**, then confirm **Open**.

The extra confirmation is expected for an unsigned, unnotarized build. Do not
disable Gatekeeper globally.

## First Launch

MacBridge starts its embedded bridge runtime automatically and listens on port
`8777`. It creates local management credentials and a Relay route on first
launch. The default Relay is `wss://relay.byteseek.uk:8443`; it can be replaced
in Remote Access settings.

Install and authenticate at least one supported backend:

- Claude Code CLI;
- Codex app-server;
- OpenCode server.

If an authenticated OpenCode server was already running before MacBridge's
first launch, restart that server after MacBridge generates its local OpenCode
credentials. A `401` health result means the running server and MacBridge are
using different credentials.

## Uninstall

Quit CCCodeBridge, then remove `/Applications/CCCodeBridge.app`.

To also remove local pairing, Relay, and bridge state:

```bash
rm -rf "$HOME/Library/Application Support/CCCode Bridge"
defaults delete org.openagi.cccode.macbridge 2>/dev/null || true
```

Keychain items use the service name `org.openagi.cccode.macbridge.relay` and can
be removed with Keychain Access when a complete reset is required.
