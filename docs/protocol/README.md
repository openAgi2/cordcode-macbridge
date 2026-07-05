# CordCode Protocol Pack

Canonical source for protocol compatibility lives in this directory.
The iOS repository may keep a synchronized copy, but compatibility
decisions should be reviewed against this MacBridge copy first.

> **命名说明：** 协议名称 `cordcode-bridge` / `cccode-relay` 及所有 wire 字面量
> （HKDF info、HTTP 头、签名域）是冻结的兼容性契约，不随产品名 CCCode→CordCode
> 变更。本文档标题与说明文字反映新品牌名 CordCode，但 wire 字面量保持原样。

## Current Versions

| Protocol | Name | Version | Schema revision |
| --- | --- | ---: | --- |
| Direct bridge | `cordcode-bridge` | 1 | `2026-07-05` |
| Relay envelope | `cccode-relay` | 1 | `2026-05-24-r1` |

## Source Of Truth

The pack was extracted from the current implementations:

- MacBridge: `go-bridge/bridge_v1_schema.go`
- MacBridge: `go-bridge/hello_handler.go`
- MacBridge: `go-bridge/types.go`
- iOS: `OpenCodeiOS/OpenCodeiOS/Services/Backend/BridgeModels.swift`
- iOS: `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift`
- iOS: `OpenCodeiOS/OpenCodeiOS/Services/Backend/BridgeMessageMapping.swift`

## Compatibility Rule

`hello.protocol.version` is the canonical protocol major negotiation field for new clients.
MacBridge rejects unsupported `hello` versions with:

```json
{"type":"hello_ack","ok":false,"error":{"code":"protocol.unsupported_version"}}
```

`register.protocol.version` remains a legacy registration path for the existing iOS transport.
It returns the server protocol result but does not currently reject mismatched versions.
New protocol work should use `hello` for version negotiation and keep `register` backward-compatible
until the legacy path is retired.

Non-breaking additions must use optional fields. Removing or changing field meaning requires a
new major `protocol.version`.

## Files

- `bridge-v1.md`: direct WebSocket envelope, handshake, RPC, events, and compatibility notes.
- `relay-v1.md`: end-to-end relay envelope and mailbox protocol.
- `schema/bridge-v1.types.ts`: TypeScript reference types matching Go JSON tags and iOS Codable fields.
- `samples/`: representative JSON fixtures for handshake and relay compatibility checks.
