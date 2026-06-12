# Backends And Configuration

MacBridge ships with a default hosted Relay endpoint. Relay route credentials,
OpenCode credentials, and signing credentials remain local and are never
committed.

## Backend Requirements

- Claude Code: install and authenticate the `claude` CLI.
- OpenCode: run an OpenCode server locally. If it requires auth, configure
  username/password in MacBridge settings or private environment variables.
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

## OpenCode First Launch

MacBridge generates a random local OpenCode username and password on first
launch and writes the matching desktop connection entry. A server process that
was already running keeps its original environment and may return `401` until
it is restarted with matching credentials. MacBridge reports that state
directly instead of treating it as a successful backend connection.
