# Architecture

CCCode is split into an iOS client and a macOS bridge application.

```text
CCCode on iPhone or iPad
        |
        | local-network WebSocket
        | or end-to-end encrypted Relay transport
        v
CCCodeBridge on macOS
        |
        +-- bridge protocol adapter
        +-- agent runtime
        +-- Claude Code
        +-- OpenCode
        +-- Codex
```

## Repository Boundaries

### `cccode-ios`

Owns:

- the SwiftUI application and view models;
- conversation and session presentation;
- pairing and connection state;
- local persistence;
- the bridge transport and iOS protocol models;
- the embedded message renderer.

The iOS app communicates through its backend abstraction. Views do not open
their own HTTP or WebSocket connections.

### `cccode-macbridge`

Owns:

- the macOS menu-bar application and settings;
- bridge lifecycle and local-network discovery;
- the embedded Go bridge runtime;
- agent adapters and session/event translation;
- Relay client integration;
- the compatible Relay server implementation.

Runtime behavior belongs in the agent/core layer. The bridge layer translates
between the shared wire protocol and that runtime.

## Connection Flow

1. MacBridge advertises a direct local-network endpoint and its configured
   Relay route.
2. The user pairs the iOS app by scanning a QR code and approving the device.
3. The iOS app prefers the direct endpoint when the phone can reach the Mac.
4. Outside the local network, both devices use the encrypted Relay path.
5. Reconnection happens in the background and cached content remains usable
   while connectivity is recovering.

## Backend Process Models

Backends do not all expose events in the same way:

- Codex uses a shared app-server and can broadcast events to multiple clients.
- OpenCode uses a shared HTTP/SSE service and can broadcast session events.
- Claude Code uses independent CLI processes. Cross-client observation may
  require polling persisted session history because one process cannot receive
  another process's stdout stream.

This distinction is important when changing refresh, streaming, and session
state logic.

## Protocol Ownership

The iOS and MacBridge repositories each carry synchronized protocol documents.
Any wire change must be updated and tested on both sides. The copies in the
split repositories are canonical; historical protocol notes in this workspace
are supporting evidence only.
