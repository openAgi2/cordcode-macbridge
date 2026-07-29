# CCCode E2E Relay v1 协议冻结

> Schema revision: `2026-05-24-r1`  
> Status: Phase 0 frozen contract  
> Applies to: `go-bridge` relay connector, iOS relay frame connection, Relay service  
> Source design: `docs/2026-05-24-CCCode-E2E-Relay与加密离线同步实施方案.md`

本文档冻结 Relay v1 的 transport 与安全合同。现有 `cccode-bridge` v1
`hello` / `request` / `result` / `event` 仍是唯一业务协议；Relay 只封装和投递
经端到端加密的 Bridge v1 payload，不解释 backend、session 或消息正文。

## 1. 强制边界

1. Mac 的真实 backend/runtime 是 session、message、Todo、running state 的唯一权威。
2. Relay 不持有解密密钥，不解析 inner payload，不以 mailbox 密文回答业务读取。
3. 首版仅允许 Mac 到 iOS 的离线 durable milestone 投递；Mac 离线时 iOS 写操作返回
   `relay.bridge_offline`，不得排队执行。
4. 在线 channel 必须使用经长期 identity key 认证的临时 ECDHE traffic keys。
5. 离线 mailbox 必须使用一次性 iOS delivery prekey 与 Mac epoch 临时 key 派生内容密钥。
   prekey 耗尽时不得回退到长期 identity key 加密内容。
6. Counter 空洞、epoch chain/head 不一致、AEAD 验证失败、设备撤销均须停止应用 payload，
   设置恢复要求并回源 Mac；不得补造 completion、Todo 或正文。

规范术语 `MUST`、`MUST NOT`、`SHOULD` 表示实现门禁。字段命名为 JSON wire spelling；
Go 与 Swift 类型名称可依当地代码风格映射，但字段值与字节编码不得改变。

## 2. 标识与版本

| 项目 | 固定值 / 约束 |
| --- | --- |
| Relay protocol name | `cccode-relay` |
| Relay protocol version | `1` |
| Schema revision | `2026-05-24-r1` |
| Inner protocol | `cccode-bridge` version `1` |
| Cipher suite | `X25519-HKDF-SHA256-CHACHA20POLY1305` |
| HPKE suite | RFC 9180 Base Mode: `DHKEM(X25519, HKDF-SHA256)`, `HKDF-SHA256`, `ChaCha20Poly1305` |
| Counter representation | unsigned 64-bit integer, JSON decimal, maximum `2^64 - 1` |
| Epoch index representation | unsigned 64-bit integer, strictly increasing per destination device |

`bridgeId`, `deviceId`, `routeId`, `senderId`, `destinationId`, `keyEpochId` 和
`prekeyId` 均为不透明 UTF-8 string。Relay 可以索引 route/endpoint/cursor/TTL 元数据，不得获得 inner
business identifiers。

## 3. 密钥职责与存储

| Secret / public material | Owner | Lifetime | Allowed use |
| --- | --- | --- | --- |
| Mac identity X25519 key pair | go-bridge | 长期，按 bridge | 认证 handshake/epoch，不派生 payload traffic key |
| iOS identity X25519 key pair | iOS Keychain | 长期，按 bridge/device | 认证 handshake/epoch，不派生 payload traffic key |
| Online ephemeral X25519 key pair | 两端 | 单一在线 channel | 派生在线 direction traffic keys，channel 关闭立即删除 |
| iOS delivery prekey pair | iOS Keychain | 单次离线 epoch | public key 上传 Mac；private key 在 durable ack 后删除 |
| Mac mailbox epoch ephemeral pair | go-bridge | 单一 bounded epoch | 与 delivery prekey 派生 mailbox key；提交 outbox 后删除 |
| Device token | 已有 Bridge v1 认证路径 | 长期、可撤销 | direct Bridge token auth，不作为 Relay 加密密钥或 credential |
| Relay route credential | 两端安全存储 | 可轮换 | 连接 Relay 路由，不认证/解密 inner payload |

`TrustedDeviceRecord` 的向后兼容扩展须允许旧记录无 Relay 字段地解码。启用 Relay
后每设备至少持久化以下概念字段：

| Field concept | Required behavior |
| --- | --- |
| `identityPublicKey` | 绑定经可信升级或 HPKE pairing 验证的 iOS identity public key |
| `relayEnabled` | false/缺失时拒绝 Relay channel 与 prekey RPC |
| `channelGeneration` | 在线 handshake 防旧 channel 重放的单调值 |
| `deliveryPrekeys` | 未消费 public prekeys 与幂等 batch 状态 |
| `deliveryChainHead` | 最新已创建 epoch 的 index/digest/counter 摘要 |
| `relayRevokedAt` | 撤销后停止新 channel、投递与写入 |

## 4. 在线认证 ECDHE Channel

### 4.1 Handshake messages

Relay 外层可路由但不得修改下列握手 payload。字节序列的 canonical encoding 规则见
§7；所有 Base64 值使用 RFC 4648 standard alphabet with padding。

```json
{
  "type": "online_client_hello",
  "protocol": {"name": "cccode-relay", "version": 1, "schemaRevision": "2026-05-24-r1"},
  "bridgeId": "brg_...",
  "deviceId": "dev_...",
  "channelGeneration": 12,
  "iosEphemeralPublicKey": "<base64-32-bytes>",
  "clientRandom": "<base64-32-bytes>",
  "authTag": "<base64-32-byte-hmac>"
}
```

```json
{
  "type": "online_server_hello",
  "bridgeId": "brg_...",
  "deviceId": "dev_...",
  "channelGeneration": 12,
  "clientHelloHash": "<base64-32-byte-sha256>",
  "macEphemeralPublicKey": "<base64-32-bytes>",
  "serverRandom": "<base64-32-bytes>",
  "keyEpochId": "online:12",
  "authTag": "<base64-32-byte-hmac>"
}
```

### 4.2 KDF inputs

```text
identityShared = X25519(localIdentityPrivateKey, remoteIdentityPublicKey)
identityAuthKey = HKDF-SHA256(
    ikm = identityShared,
    salt = empty,
    info = utf8("cccode-relay/identity-auth/v1") || canonical(bridgeId, deviceId),
    L = 32)

clientHelloWithoutTag = canonical JSON of online_client_hello excluding authTag
clientAuthTag = HMAC-SHA256(identityAuthKey, clientHelloWithoutTag)

serverHelloWithoutTag = canonical JSON of online_server_hello excluding authTag
serverAuthTag = HMAC-SHA256(identityAuthKey, serverHelloWithoutTag)

transcriptHash = SHA-256(clientHelloWithoutTag || serverHelloWithoutTag)
ephemeralSecret = X25519(localEphemeralPrivateKey, remoteEphemeralPublicKey)
trafficRoot = HKDF-SHA256(
    ikm = ephemeralSecret,
    salt = transcriptHash,
    info = utf8("cccode-relay/online/v1"),
    L = 32)
iosToMacKey = HKDF-SHA256(trafficRoot, empty, utf8("ios-to-mac"), 32)
macToIosKey = HKDF-SHA256(trafficRoot, empty, utf8("mac-to-ios"), 32)
```

双方 MUST 在解密任何 secure envelope 前验证 identity auth tags、`bridgeId`、
`deviceId` 与严格递增的 `channelGeneration`。在线 channel 从每个方向 counter `1`
开始；关闭后 MUST 删除 ephemeral private key 与 direction keys。

## 5. Secure Envelope

### 5.1 外层信封

在线和 mailbox payload 共享同一 encrypted envelope 形状。`deliveryCursor` 是
Relay mailbox API 返回的投递位置，不进入端到端 envelope，也不替代 `counter`。

```json
{
  "version": 1,
  "routeId": "br_route_xxx",
  "senderId": "dev_xxx",
  "destinationId": "bridge",
  "channelGeneration": 12,
  "keyEpochId": "online:12",
  "prekeyId": null,
  "epochIndex": null,
  "epochEphemeralPublicKey": null,
  "previousEpochDigest": null,
  "epochAuthTag": null,
  "messageId": "uuid",
  "counter": 1,
  "ciphertext": "<base64-ciphertext-with-aead-tag>",
  "createdAt": "2026-05-24T08:00:00Z",
  "expiresAt": "2026-05-25T08:00:00Z"
}
```

Mailbox frame 使用：

```json
{
  "version": 1,
  "routeId": "br_route_xxx",
  "senderId": "bridge",
  "destinationId": "dev_xxx",
  "channelGeneration": 12,
  "keyEpochId": "mailbox:7",
  "prekeyId": "pk_uuid",
  "epochIndex": 7,
  "epochEphemeralPublicKey": "<base64-32-bytes>",
  "previousEpochDigest": "<base64-32-bytes-or-empty>",
  "epochAuthTag": "<base64-32-byte-hmac>",
  "messageId": "uuid",
  "counter": 1,
  "ciphertext": "<base64-ciphertext-with-aead-tag>",
  "createdAt": "2026-05-24T08:00:00Z",
  "expiresAt": "2026-05-25T00:00:00Z"
}
```

`prekeyId`、`epochIndex`、`epochEphemeralPublicKey`、`previousEpochDigest` 与
`epochAuthTag` 在在线 channel 中 MUST 为 `null`，在 mailbox epoch 的每个 frame
中 MUST 保持一致。`epochEphemeralPublicKey` 是 §8.2 中 iOS 派生 mailbox key
必需的 Mac 临时公钥；它将设计方案中已经要求的 epoch metadata 显式固定为 wire
字段。

### 5.2 Inner payload

Decrypted plaintext encodes `uint32_be(innerLength) || innerBytes || randomPadding`.
`innerBytes` MUST decode as exactly one existing Bridge v1 JSON message; after removing
padding, Relay transport MUST dispatch those original bytes through the same Bridge
handler/client path used by direct connections.

## 6. Counter, Nonce, Padding 与投递连续性

### 6.1 AEAD rules

| Rule | Contract |
| --- | --- |
| Algorithm | ChaCha20-Poly1305 |
| Nonce | 12 bytes: `0x00000000 || uint64_be(counter)` |
| Initial counter | `1` for each `(keyEpochId, senderId, destinationId)` |
| Receive acceptance | only `lastCommittedCounter + 1` |
| AAD | canonical bytes from §7.2 |
| Counter persistence | mailbox receive counter persists with durable local apply; online counter lasts for the channel |

Counter rollback, duplicate or gap MUST fail with `relay.counter_invalid`; no later frame in
that epoch may be dispatched. The receiver persists `localReconcileRequired` before acking
or continuing recovery.

### 6.2 Padding

`uint32_be(innerLength) || innerBytes` MUST be padded so its byte length is the next
multiple of 256. Padding bytes are random and authenticated inside the ciphertext. This
applies to:

- online `message.delta` / tool delta frames;
- all mailbox milestone frames.

Other online low-frequency control frames MAY use the same padding rule, but implementations
MUST NOT claim that padding hides timing, frequency or total-byte traffic analysis.

## 7. Canonical Encoding 与 AAD

### 7.1 Canonical JSON

Where this protocol says `canonical JSON`, implementations MUST encode UTF-8 JSON with:

- object keys sorted lexicographically by UTF-8 byte order;
- no insignificant whitespace;
- strings escaped per JSON without Unicode normalization;
- integers encoded as unsigned decimal without leading zeroes;
- Base64 strings exactly as specified in §4.1.

Crypto vectors MUST assert these exact bytes before asserting HMAC or ciphertext values.

### 7.2 Frame AAD

For each encrypted frame, AAD is canonical JSON of all readable envelope fields except
`ciphertext`. `createdAt` and `expiresAt` are authenticated because Relay must not silently
modify retention validity or delivery age.

```json
{
  "version": 1,
  "routeId": "br_route_xxx",
  "senderId": "bridge",
  "destinationId": "dev_xxx",
  "channelGeneration": 12,
  "keyEpochId": "mailbox:7",
  "prekeyId": "pk_uuid",
  "epochIndex": 7,
  "epochEphemeralPublicKey": "<base64-32-bytes>",
  "previousEpochDigest": "<base64-32-bytes-or-empty>",
  "epochAuthTag": "<base64-32-byte-hmac>",
  "messageId": "uuid",
  "counter": 1,
  "createdAt": "2026-05-24T08:00:00Z",
  "expiresAt": "2026-05-25T00:00:00Z"
}
```

For online frames the five mailbox epoch fields MUST be JSON `null`. Changing endpoint,
generation, epoch, message identity, time bounds or counter therefore invalidates the AEAD
tag. `deliveryCursor` is intentionally outside AAD because it is an opaque Relay replay
position; cryptographic continuity is enforced by counter and epoch chain/head.

## 8. Offline Delivery Prekey Epoch

### 8.1 Prekey storage and upload RPC

All RPC below are encrypted inner Bridge v1 `request` / `result` payloads over an authenticated
online channel.

```json
{
  "method": "get_delivery_prekey_status",
  "params": {}
}
```

```json
{
  "availableCount": 8,
  "lowWatermark": 10,
  "targetCount": 32,
  "maxCount": 64
}
```

```json
{
  "method": "upload_delivery_prekeys",
  "params": {
    "batchId": "batch_...",
    "prekeys": [
      {"prekeyId": "prekey_...", "publicKey": "<base64-32-bytes>"}
    ]
  }
}
```

```json
{
  "acceptedCount": 24,
  "totalAvailable": 32,
  "duplicateBatchId": false
}
```

Rules:

1. iOS queries once after every authenticated channel establishment and MAY query once per
   30 minutes while foreground-connected.
2. If `availableCount < 10`, iOS creates
   `min(32 - availableCount, 64 - availableCount)` prekeys.
3. iOS MUST durably store private prekeys and `batchId` in Keychain before uploading public keys.
4. `batchId` upload is idempotent; `prekeyId` is unique per device.
5. If accepting an entire new batch would exceed 64 unconsumed prekeys, Mac MUST reject the
   entire batch with `prekey_limit_exceeded`; iOS retains it for later retry.

### 8.2 Mailbox epoch derivation

Mac atomically consumes exactly one unconsumed prekey when beginning a bounded epoch.

```text
mailboxSecret = X25519(macEpochEphemeralPrivateKey, iosDeliveryPrekeyPublicKey)
mailboxRoot = HKDF-SHA256(
    ikm = mailboxSecret,
    salt = empty,
    info = utf8("cccode-relay/mailbox/v1") || canonical(bridgeId, deviceId, prekeyId, epochIndex),
    L = 32)
macToIosMailboxKey = HKDF-SHA256(mailboxRoot, empty, utf8("mac-to-ios"), 32)
```

An epoch is immutable after its bounded encrypted frames have been submitted to Relay/outbox.
Subsequent offline data MUST consume another prekey and create another epoch.

### 8.3 Authenticated chain

```text
epochHeader = canonical(prekeyId, epochIndex, macEphemeralPublicKey,
                        previousEpochDigest, firstCounter, lastCounter, frameCount)
epochAuthTag = HMAC-SHA256(identityAuthKey, epochHeader)
epochDigest = SHA-256(epochHeader || epochAuthTag)
```

iOS verifies `epochAuthTag`, sequential `epochIndex`, `previousEpochDigest` and every frame
counter before durable apply. After mailbox replay, it queries:

```json
{"method": "get_delivery_chain_head", "params": {}}
```

```json
{
  "epochIndex": 7,
  "epochDigest": "<base64-32-bytes>",
  "lastEpochFinalCounter": 3
}
```

The local head MUST equal the Mac head before iOS clears recovery waiting state. A missing
epoch, truncated tail or mismatched head produces `relay.chain_mismatch` and authoritative
reconcile.

## 9. Durable Mailbox 与 Observation Scope

### 9.1 Milestone allowlist

Only the following Bridge v1 events may be persisted in an offline mailbox:

| Inner event | Notes |
| --- | --- |
| `turn_completed` | terminal completion signal; full message text still comes from Mac reconcile |
| `todos_updated` | change notification; full Todo state is reconciled from Mac |
| `turn_error` | terminal/recovery signal; error detail remains recoverable only from Mac authority |
| `session_running_signal` | lightweight running-state hint requiring subsequent reconciliation |
| `delivery_reconcile_required` | secure control event; requires local durable reconcile marker |

`text_delta`, `thinking_delta`, tool content, complete messages, prompt content, file data
and session history MUST NOT enter a durable mailbox.

### 9.2 Observation scope RPC

```json
{
  "method": "set_observation_scope",
  "params": {
    "backendId": "codex",
    "sessionIds": ["..."],
    "deliveryMode": "full_stream",
    "includeRunningSessionSignals": true,
    "leaseSeconds": 45
  }
}
```

Allowed `deliveryMode` values are `full_stream` and `milestones_only`. A `full_stream`
scope MUST have a finite short lease; Relay v1 clients request 45 seconds and renew while
foreground. On lease expiration or background transition,
Mac switches that device to `milestones_only`; iOS renews the lease only while foreground and
connected.

### 9.3 Cursor and ack ordering

`deliveryCursor` is Relay-assigned and monotonic per destination mailbox. Replay responses
associate it with an opaque encrypted envelope. It is independent from Bridge event `seq`,
`keyEpochId` counter and backend-specific event IDs.

For each replayed item, iOS MUST perform one of these durable transitions before ack:

1. durably apply the verified milestone; or
2. durably write `localReconcileRequired` with its reason.

Only then may iOS ack the highest contiguous `deliveryCursor`. An ack before either durable
transition is a protocol violation.

### 9.4 Outbox overflow

Mac may retain a bounded encrypted outbox while disconnected from Relay. If it overflows,
Mac MUST abandon the current delivery epoch; after a fresh authenticated online channel it
sends `delivery_reconcile_required`. It MUST NOT append ordinary frames after a counter gap.

## 10. Relay Service API 与可见数据

The Relay service exposes transport operations only:

| Operation | Required behavior |
| --- | --- |
| endpoint connect/authenticate | Verify route credential and enabled endpoint identifier |
| online forward | Forward opaque handshake/encrypted frames between online route endpoints |
| mailbox enqueue/replay | Persist and replay opaque mailbox frames by destination cursor |
| mailbox ack | Delete/expire only acknowledged ciphertext frames per retention policy |
| endpoint revoke | Stop forwarding and delete queued ciphertext for revoked endpoint |

Relay may persist/log `routeId`, endpoint IDs, routing direction, ciphertext size, cursor, TTL,
connection status and aggregate latency/storage metrics. It MUST NOT log plaintext, decrypted
Bridge messages, keys, complete credentials, backend ID, session ID, project path, prompt,
tool data or Todo content.

## 11. Error Codes 与 Fail-Closed 行为

| Error code | Trigger | Required recovery |
| --- | --- | --- |
| `relay.bridge_offline` | iOS attempts write while Mac endpoint offline | Return failure; never queue request |
| `relay.not_enabled` | device without trusted relay binding attempts relay operation | Refuse channel/RPC |
| `relay.device_revoked` | revoked device connects or sends frame | Close channel; delete future mailbox eligibility |
| `relay.handshake_auth_failed` | identity-auth tag/transcript/generation invalid | Close without dispatch |
| `relay.counter_invalid` | duplicate, rollback or gap | Seal epoch; durable reconcile marker |
| `relay.decrypt_failed` | AEAD/AAD validation fails | Seal epoch; durable reconcile marker |
| `relay.chain_mismatch` | epoch chain or queried Mac head differs | Authoritative reconcile |
| `prekey_limit_exceeded` | new whole upload exceeds per-device hard limit | Reject batch atomically; iOS may retry |
| `relay.prekey_exhausted` | Mac has no one-time key for mailbox content | Send reconcile requirement when online; no encrypted detail fallback |
| `relay.mailbox_expired` | replay detects TTL/capacity loss | Authoritative reconcile |

Security failures MUST be diagnosable by classified error and opaque identifiers, without
logging payload or secret material.

## 12. Pairing Claim

Existing trusted devices may enable Relay only through an authenticated direct Bridge
connection that binds Mac/iOS identity public keys and route credential metadata.

New-device Relay pairing MUST use RFC 9180 HPKE Base Mode. The QR trust root contains:

```json
{
  "protocol": {"name": "cccode-relay", "version": 1, "schemaRevision": "2026-05-24-r1"},
  "bridgeId": "brg_...",
  "routeId": "route_...",
  "relayEndpoint": "wss://relay.example/bridge",
  "macIdentityPublicKey": "<base64-32-bytes>",
  "macIdentityFingerprint": "<sha256-base64>"
}
```

The HPKE claim plaintext contains the iOS identity public key, device metadata and a fresh
claim nonce. Mac approval is bound to the claim nonce and bridge identity. Relay key
substitution, altered ciphertext and approval replay MUST be rejected before Relay is enabled
for the device.

### 12.1 iOS availability gate

`OpenCodeiOS/project.yml` 当前最低部署目标是 iOS 16.0，而 Apple 提供的
`CryptoKit.HPKE` API 最低可用版本为 iOS 17.0。Phase 3 在启用新设备 Relay pairing
前 MUST 明确完成其中一项决策并提供互操作测试证据：

1. 将 Relay-capable 产品最低部署目标提高至 iOS 17.0 或更高，并使用原生
   `CryptoKit.HPKE`；或
2. 明确批准一项支持 iOS 16.0、通过 RFC 9180 vectors 审计的 HPKE 实现依赖。

在该决策完成之前，已有设备可信直连升级可继续开发；新设备 Relay pairing
MUST 保持 disabled。不得用自制 X25519 信封或静态密钥路径假装满足 HPKE 门禁。

## 13. Fixture 与实现门禁

Before enabling Relay code paths, both Go and Swift tests MUST consume shared vectors for:

1. canonical encoding bytes and AAD;
2. identity-authenticated online ECDHE transcript and direction keys;
3. ChaCha20-Poly1305 nonce/ciphertext for online and mailbox frames;
4. 256-byte padding encode/decode;
5. one-time prekey mailbox key, `epochAuthTag`, `epochDigest` and chain/head mismatch;
6. counter replay/gap, ciphertext/AAD tampering and prekey exhaustion;
7. RFC 9180 HPKE claim success, substitution and replay rejection.

The Phase 1 online feature gate MUST remain disabled until items 1-4 pass cross-language
vectors. The Phase 2 mailbox feature gate MUST remain disabled until items 5-6 pass. New
device Relay pairing MUST remain disabled until item 7 passes.
