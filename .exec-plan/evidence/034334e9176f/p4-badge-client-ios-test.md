# P4 badge client — iOS remote-web tests (2026-09-13)

Command: `bun x vitest run src/core/push src/core/protocol` (remote-web)
Scope: push + protocol suites (badge lifecycle, SW badge behavior, type contracts)
Full suite: 88/89 files pass; the 1 failing file (relay frame-connection-rpc, 23 tests) is a pre-existing environment failure — missing cordcode_relay_crypto.js WASM module, untouched by this change (verified: file has no local modifications).

          Tests  70 passed (70)
       Start at  01:16:58
       Duration  1.22s (transform 243ms, setup 0ms, collect 596ms, tests 783ms, environment 637ms, prepare 613ms)
    
