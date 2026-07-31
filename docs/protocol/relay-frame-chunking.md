# Relay Frame Chunking

`relay_chunks_v1` is an optional `cordcode-bridge` v1 capability for splitting large MacBridge-to-client online Relay messages into independently authenticated scheduling quanta. It does not change Direct WebSocket, mailbox, or client-to-Mac frames.

## Negotiation

The client advertises `relay_chunks_v1` in the authenticated inner `hello`. MacBridge may send chunked envelopes only when the connection is Relay, the client declared the capability, and successful `hello_ack.capabilities.relay_chunks_v1` is strictly `true`.

Clients initialize acknowledgement to false. Receiving a chunk before strict acknowledgement is `relay.chunk_unnegotiated`: close the secure transport and reject pending requests. There is no legacy reinterpretation or silent fallback.

## Envelope metadata and AAD

```json
{"chunk":{"groupId":"550e8400-e29b-41d4-a716-446655440000","index":0,"count":13}}
```

`groupId` identifies one logical Bridge JSON message; `messageId` remains unique per wire frame. `index` and `count` are uint32, `count` is 1...1024, and `index < count`. All chunk fields are authenticated in canonical AAD.

The `chunk` key uses conditional-add semantics: it is present in AAD only when the envelope field is present. A legacy envelope must not encode `"chunk":null`; its canonical AAD bytes remain unchanged.

## Encoding and reassembly

```text
Bridge JSON -> optional whole-message gzip -> split -> per-chunk padding -> per-chunk AEAD
per-chunk AEAD -> remove padding -> bounded reassembly -> optional one-time gzip -> Bridge JSON
```

For a group, `groupId`, `count`, and `contentEncoding` are fixed. Only `index`, `counter`, and `messageId` vary. Intermediate plaintext never enters JSON/type dispatch. One unfinished group per device is permitted in v1; a second group, duplicate/out-of-order index, inconsistent group fields, overflow, or 15-second idle timeout is a transport-closing protocol failure.

The sender may interleave non-chunk control or interactive frames between chunks, and may schedule chunks for other devices independently. For one device, after index `0` has been written, no chunk from another group may be written until the active group completes or the transport closes. Supersede may discard a group before index `0`; after index `0`, the sender must finish that group or close the transport so the receiver cannot retain an orphaned reassembly state.

Only bulk Relay results may be chunked. Control, recovery, interactive, and metadata frames remain single-envelope and may be scheduled between chunks. The writer assigns counter, nonce, and frame `messageId` only when selecting the next wire frame.

Initial limits: 32 KiB target, 16 KiB minimum, 1,024 chunks, 50 MiB reassembled bytes, one unfinished group per device, and 15 seconds idle timeout. Mailbox envelopes must reject `chunk`.
