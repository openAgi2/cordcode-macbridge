# Install On macOS

The current downloadable build is an unsigned Apple Silicon preview. It is
intended for testing before Developer ID signing and Apple notarization are
available.

## Install

1. Download the `macos-arm64-unsigned.zip` file and its `.sha256` file from the
   GitHub prerelease.
2. Verify the checksum:

   ```bash
   shasum -a 256 -c CordCodeLink-*-macos-arm64-unsigned.zip.sha256
   ```

3. Unzip the archive and move `CordCodeLink.app` to `/Applications`.
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
- OpenCode CLI. CordCode Link starts a managed local OpenCode server automatically on first use.

For OpenCode, new installs use **Automatic / managed_local**: CordCode Link starts a
loopback-only `opencode serve`, writes random Basic Auth credentials to
`~/Library/Application Support/CordCode Link/opencode-managed-server.json`, and syncs
OpenCode Desktop to the same server. Existing setups may keep `Legacy 64667` for
upgrade continuity, but it is not the fresh-install default.

## Uninstall

Quit CordCode Link, then remove `/Applications/CordCodeLink.app`.

To also remove local pairing, Relay, and bridge state:

```bash
rm -rf "$HOME/Library/Application Support/CordCode Link"
defaults delete org.openagi.cordcode.link 2>/dev/null || true
```

Relay credentials are stored under the CordCode Link application support directory with
file permissions restricted to the local user; there are no current Relay Keychain items
to remove for a normal reset.
