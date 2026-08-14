# Session Projection v2 observed wire samples

These two files are a paired, sanitized capture from the installed production MacBridge runtime
at commit `da8551d6d525`. A temporary client completed the real direct
pairing → approval → authenticated `hello(session_sync_v2)` path, set a full-stream observation
scope, and sent a real Codex turn. The runtime emitted `projection_patch` 717→718. A separately
paired connection then called `get_session_projection(sinceRev: 717)` and received the contiguous
717→720 journal suffix. The first pull patch is exactly the push `data` object.

This is not a Go test object, schema example, handwritten payload, mock, or synthesized fixture.
The capture clients were revoked immediately after collection.

Sanitization changed only identifying/content scalar values: bridge/session/turn/message/request
identifiers, user text, and timestamps. Envelope keys, patch keys, key presence/absence, JSON value
types, array cardinality, revision continuity, and the push↔first-pull-patch equality were retained.
The uncommitted raw capture hashes were:

- push: `sha256:241ef7cc7cb6a48379d29e47c7b7afc51cf4166e0902696056cc540de4711a90`
- pull: `sha256:0f54dc89e473909ea3f29154ee88bdf77c93f7cbb2d86507910243c553c61f74`

Independent wire presence extraction for every observed patch reports:

| Canonical patch field | Observed presence |
| --- | --- |
| `baseRev` | present |
| `syncRev` | present |
| `execution` | present |
| `upsertTurns` | present |
| `partOps` | absent |
| `replacesClientIds` | absent |

The automated contract test independently decodes each patch through the canonical typed
`ProjectionPatch`, re-encodes it, and compares all six fields one by one, including presence.
