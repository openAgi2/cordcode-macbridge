# 独立审计 6：Canonical Stage-0 evidence pack

- Audited commit: `4a215b0`
- Verdict: **partial, evidence usable under the design-owner restrictions below**
- Product-freeze verdict: **PASS**

## Independently re-run

The following completed successfully from the committed worktree:

- `check_workspace_project.py` and `--self-test` (6 destructive mutations);
- `check_evidence_queue.py` and `--self-test` (11 destructive mutations);
- `check_canonical_execution_design.py` and `--self-test`;
- `check_samples.py --require-all` and `--self-test` (A1–A10 remain separate and green);
- `git diff --check` and product-file scope inspection.

The commit changes only harness/sample/inventory/exec-plan evidence files. No `agent/opencode-web` product translator, `go-bridge`, `core`, protocol, WireDescriptor, capability, or iOS product file changed.

## Verified evidence

- WP-FIX now derives from the real `http[]` response and catches summary disagreement.
- E1 proves a non-empty `variant` request, 204 admission, persistence on the corresponding latest user-message model, and omission on the unset control.
- E2 honestly proves a blocked capture path: no reasoning SSE/reload part appeared, retry continued, and no terminal arrived in the observation window.
- E3 proves the external client request, global-SSE live deltas/terminal, and persisted reload convergence without list polling.
- E4 proves the key-preserving provider envelope shape from a live sanitized capture.
- E5 proves server-side config/default behavior and, importantly, that an invalid configured model may still be admitted and persisted if a caller omits explicit validated model selection.
- E6/E7 prove exact success/failure and reload convergence. E7 also proves that implementation must not depend on observing `session.deleted` for caller-initiated deletion.

## Audit limitations and owner disposition

1. **E4/E5 raw archive absent.** Directive-006 requested raw + sanitized. The checker intentionally consumes sanitized evidence because the live response echoes a deterministic credential under `options`. This is a provenance deviation, so the directive is not a literal full PASS. Owner disposition: accept it only for key/type/catalog semantics; `env`, `options`, credential values, and provider-auth behavior remain opaque and out of product mapping.
2. **E1 report overclaim.** The reported prose says an earlier variant-bearing turn remains variant-bearing, but the independent facts show `earlierTurnKeepsVariant=[]`; the checker only proves the latest set/unset messages. Owner disposition: canonical uses only request admission, same-message persistence, and unset omission.
3. **E5 single-model ambiguity.** Absent-config fallback cannot distinguish `/provider.default` from first-model fallback in this sandbox. Owner disposition: exact fallback order comes from the pinned official UI source; the live evidence is used for envelope shape, config/default separation, and the invalid-config hazard, not as sole proof of every fallback branch.

These limitations do not require another capture round for the first convergence scope. They require narrower canonical mappings and negative tests, which the design-owner revision supplies before product work.
