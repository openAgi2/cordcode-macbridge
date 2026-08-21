# 开发报告 6：Canonical Stage-0 evidence pack

- Reported commit: `4a215b0`
- Scope: WP-FIX + E1–E7 capture/checkers only
- Product code: unchanged
- Reported result: WP-FIX/E1/E3/E4/E5/E6/E7 captured; E2 blocked; worktree clean

## Evidence result reported by the developer agent

| ID | Reported state | Reported observation |
|---|---|---|
| WP-FIX | captured | `/project` facts derived from `http[].status/response`; duplicate summary mismatch fails |
| E1 | captured | non-empty `variant` admitted and persisted on the corresponding user-message model; unset control omits it |
| E2 | blocked | two reasoning-stream strategies both enter retry without materializing a reasoning part or terminal |
| E3 | captured | a second client creates/sends; the capture client observes global SSE deltas and terminal, then reload converges |
| E4 | captured with provenance limitation | `/provider` shape is preserved in sanitized evidence; raw is withheld because provider options echo the deterministic credential |
| E5 | captured with single-model limitation | `/provider.default` is config-independent; valid/invalid/absent config model behavior captured |
| E6 | captured | PATCH title returns Session.Info; list/by-ID converge; missing session returns NotFound |
| E7 | captured | DELETE returns true; list/by-ID converge to absence; no `session.deleted` was observed in the window |

The developer explicitly stopped before mapping, product code, protocol, WireDescriptor, capabilities, iOS work, build, or install.
