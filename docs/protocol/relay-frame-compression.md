# Relay Frame Compression

`relay_gzip_v1` is an optional `cordcode-bridge` v1 capability for compressing large MacBridge-to-client relay frames before end-to-end encryption. It does not change direct WebSocket traffic and it does not enable client-to-Mac compression.

## Negotiation

The client advertises support in the authenticated inner Bridge `hello`:

```json
{"type":"hello","protocol":{"name":"cordcode-bridge","version":1},"capabilities":["relay_gzip_v1"]}
```

MacBridge enables compression only when all of these conditions hold:

- the active connection is a Relay connection;
- the client declared `relay_gzip_v1`;
- the Bridge accepted the `hello`.

The successful `hello_ack.capabilities` map contains `"relay_gzip_v1": true`. Clients that omit the capability retain the original uncompressed envelope format. In particular, older iOS clients remain wire-compatible without an update.

Clients must advertise the capability only when they can decode gzip streams. The browser implementation feature-detects `DecompressionStream`; the iOS implementation advertises it only on Relay connections and applies a bounded gzip decoder.

## Envelope and authenticated metadata

A compressed Relay envelope adds the optional field:

```json
{"contentEncoding":"gzip"}
```

`contentEncoding` is included in the envelope AEAD additional authenticated data (AAD). Adding, removing, or changing it after encryption therefore causes authentication failure. An absent field means the decrypted bytes are the original UTF-8 Bridge JSON payload.

For `contentEncoding: "gzip"`, the sender and receiver pipeline is:

```text
Bridge JSON -> gzip -> frame padding -> ChaCha20-Poly1305
ChaCha20-Poly1305 -> remove padding -> gzip decode -> Bridge JSON
```

Compression must happen before encryption. WebSocket `permessage-deflate` acts on the already encrypted Relay envelope and cannot meaningfully compress its ciphertext.

## Sender policy

The current MacBridge implementation considers payloads at least 32 KiB and sends gzip only when the compressed bytes are smaller than the original JSON. This threshold and size check are sender policy, not additional wire requirements. Small or incompressible frames remain in the legacy format even after negotiation.

Compression, authentication, or decompression failures are surfaced as real transport failures. Implementations must not silently reinterpret a gzip frame as uncompressed JSON.
