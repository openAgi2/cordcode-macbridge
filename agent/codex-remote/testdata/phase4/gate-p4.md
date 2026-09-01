# Gate P4 observation-window adjudication

## Automated/source result

The Phase 4 topology and coexistence implementation is present and covered by
focused race tests. The fixed DTO, aggregation truth table, stale/epoch debounce,
management endpoints, Remote status redaction, and legacy-directory zero-diff
boundary all pass.

## Observation result

The required long real-environment observation window has not been freshly run in
this turn. The owner-confirmed bidirectional Remote projection is a critical
vertical-slice observation, but it is not equivalent to a full P4 window covering
Desktop sleep/exit, network transitions, controller revoke, and simultaneous
`codex-web` daemon failure. Those rows remain pending observation rather than being
promoted to a false PASS.

| Gate condition | Evidence | Adjudication |
| --- | --- | --- |
| Independent `codex-web` topology DTO and stale/epoch behavior | `topology_*_test.go` focused race suite | verified. |
| Independent Remote pairing/environment status and redaction | `management_codex_remote_test.go`, `management_api_test.go`, `pairing_test.go` | verified in source/tests. |
| `split_present` is not global failure | `AggregateDesktopTruthTable`, `DeriveSyncHealthFullTable` | verified. |
| Preserve both backends and rollback entry | source boundary and coexistence contract | verified at source boundary. |
| Long owner observation window | no new owner matrix capture in this turn | pending; not claimed. |

No backend is retired, no global health fallback is added, and no existing
`codex-web`/legacy `codex` code is modified.
