# Probe entry-point rules

This directory will contain non-product Phase 0 controller probes only. No probe is currently implemented.

Every future executable added here must document:

- exact evidence question and Gate P0 row;
- target ChatGPT App and embedded Codex versions;
- required human account/pairing action, if any;
- official origin/path allowlist;
- overall and per-operation timeouts;
- redaction boundary and permitted output schema;
- temporary device-key/enrollment lifecycle;
- deterministic revoke/cleanup command;
- expected failure classifications, including HTTP 409/single-owner;
- evidence artifact paths.

A probe must stop before a network mutation unless all automatic preflight checks pass. It must never disconnect or revoke another controller to make its own connection succeed.

## Current automatic preparation

The frozen call-site contract and live-fixture policy are available under `../testdata/phase0/`. No credential or network probe exists yet because the compliant independent-authentication path and owner-authorized account operation have not been established.

Before requesting owner action, implementation must provide one bounded command that:

1. uses an independently named, OS-protected nonextractable probe key;
2. keeps account, step-up and controller credentials in memory only;
3. allowlists the frozen official origin and exact paths;
4. writes only the redacted schema after secret scanning;
5. stops on 409/single-owner without disconnecting the official mobile controller;
6. exposes a deterministic revoke command for only the probe controller.

Static package inspection cannot establish any of these live properties.

`preflight.mjs` is the current non-mutating entry point. It verifies the exact ASAR contract, embedded helper binaries, device-key addon ABI, ChatGPT login-status availability and the two frozen OAuth callback ports. It does not request a bearer token, create a key, open a browser, contact controller endpoints or change pairing state:

```bash
/Applications/ChatGPT.app/Contents/Resources/cua_node/bin/node \
  agent/codex-remote/probe/preflight.mjs
```
