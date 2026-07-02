# Backends And Configuration

MacBridge ships with a default hosted Relay endpoint. Relay route credentials,
OpenCode credentials, and signing credentials remain local and are never
committed.

## Backend Requirements

- Claude Code: install and authenticate the `claude` CLI.
- OpenCode: run a stable `opencode serve` HTTP server locally on loopback with
  `OPENCODE_SERVER_PASSWORD` set, then point MacBridge at it as an **External
  HTTP** endpoint (`http://127.0.0.1:<port>`). MacBridge connects to, but does
  not start or keep alive, this server. A passwordless server is rejected.
- Codex app-server: run the Codex app-server and point MacBridge to its
  WebSocket URL, usually `ws://localhost:4141`.
- Copilot ACP: not part of the current migrated runtime.

## Configuration Inputs

Supported configuration surfaces:

- MacBridge app settings.
- Runtime CLI flags for non-sensitive local settings such as port, backend
  mode, and local service URLs.
- Private environment variables for credentials, route identifiers, and
  management tokens.
- Private config files that are never committed.

Use `config.example.env` as a placeholder reference for command-line runtime
configuration. It intentionally contains no token, route ID, password, or
signing identity.
MacBridge passes sensitive runtime values through the child process environment
instead of argv so they do not appear in ordinary process listings.

## Relay

The app defaults to `wss://relay.byteseek.uk:8443`. The endpoint is public
configuration, not a credential. Users may:

- Run direct local WebSocket pairing on the same network.
- Use the default hosted Relay.
- Self-host `relay-server`, enter its `wss://` endpoint in MacBridge settings,
  and later restore the built-in default with one action.

When the selected endpoint changes, MacBridge registers a route with that Relay
and stores the resulting route ID and credential locally.

## OpenCode server source

MacBridge no longer implicitly hard-codes `127.0.0.1:64667`. The OpenCode
backend has an explicit **Server Source**, selectable in Settings:

- **External HTTP** (default for new shared use): connect to a stable
  `opencode serve` you started. The URL must be loopback (`http://127.0.0.1:<port>`);
  `localhost` is normalized to `127.0.0.1`. A non-empty password is required.
  MacBridge validates the endpoint by first proving the server requires auth
  (no-auth `/global/health` must return `401`) before accepting authenticated
  `200`. A passwordless server (no-auth `200`) is rejected as
  `server_unauthenticated`.
- **Legacy 127.0.0.1:64667**: upgrade-continuity compatibility mode. The only
  source allowed to keep running against a possibly-passwordless legacy
  listener, in which case it is flagged `legacy_insecure_unverified` and must
  not be treated as a secure shared endpoint.
- **Service discovery (future)**: reserved. The current stable `opencode` CLI
  does not expose `service` / `serve --register`, so this source is
  `not_configured` until a future CLI adds it.
- **Disabled**: OpenCode backend off; Claude and Codex are unaffected.

### First launch / migration

When an existing `com.opencode.server` LaunchAgent or a prior
`credentials.json` provides OpenCode credentials, MacBridge reuses them and
**migrates the source to `legacy_64667`** once, preserving existing OpenCode
behavior. A one-time notice guides configuring an External HTTP server for a
secure shared OpenCode. A genuinely fresh install defaults to **Disabled**
(never auto-falls to `64667`). MacBridge reports `401` / `not_configured`
directly instead of treating an auth mismatch or missing URL as success.

### Bring-your-own-server persistence

Phase A does **not** start or keep the OpenCode server alive. Keep the command
running, or install your own local LaunchAgent:

```bash
OPENCODE_SERVER_PASSWORD='<password>' \
opencode serve --hostname 127.0.0.1 --port <chosen-port>
```

If OpenCode logs `OPENCODE_SERVER_PASSWORD is not set; server is unsecured`,
MacBridge rejects the `external_http` endpoint; set the password and restart
the server. An optional LaunchAgent template (bind loopback, `chmod 600`) is
documented in the shared-service plan, but MacBridge does not install it.
