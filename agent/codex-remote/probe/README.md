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
