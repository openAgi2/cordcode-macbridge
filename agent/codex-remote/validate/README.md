# Validator contract

Validators in this directory verify source metadata, redacted fixture schemas, ordering, provenance and secret absence. They do not emulate the OpenAI relay and cannot promote a fake interaction to Gate P0 evidence.

Each validator must:

- fail closed on target-version or hash drift;
- distinguish host-source assertions from target controller/binary assertions;
- produce a bounded, secret-free result;
- exit nonzero on missing evidence or schema drift;
- avoid modifying ChatGPT App, Codex configuration, system proxy, DNS, certificates or controller state;
- be replayable from the dedicated MacBridge worktree.

`source-baseline.mjs` currently verifies only the static/source baseline and prints `gateEffect=does-not-pass-phase0` on success.

`controller-fixtures.mjs` verifies the exact target ASAR call sites and the live-capture redaction contract. Its default mode is a static preflight only. `--require-live` deliberately fails while the repository lacks a real redacted observation set; that failure is the expected Phase 0 blocker, not a test defect.
