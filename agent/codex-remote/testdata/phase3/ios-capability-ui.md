# Phase 3 capability-driven iOS UI

The Remote descriptor currently advertises only capabilities implemented by the
MacBridge adapter:

| Capability | Remote behavior | iOS result |
| --- | --- | --- |
| `session_state` | present | session status is displayed and routed. |
| `session_history` | present | authoritative `thread/read` history is used. |
| `session_sync_v2` | present | projection/live events are the timeline authority. |
| `model_switch` | absent | model picker is not treated as a Remote control surface. |
| `permission_resolve`, `permission_mode`, `structured_user_input_v1` | absent | approval/question controls stay hidden and requests remain fail-closed. |
| `session_mutation`, `session_delete`, `session_pin`, pagination | absent | no unsupported mutation/pagination affordance is shown. |
| attachments | absent in the Remote app-server input sample | the Mac adapter rejects image/file turn input instead of dropping it silently. |

The iOS UI uses the descriptor for capability flags, while `BackendKind.codexRemote`
only supplies stable product copy and routing. In particular, the Remote path does
not inherit Codex-Web model-provider qualification, daemon paths, or approval UI.
The “Codex Desktop” model chrome is hidden when no real catalog is available; a
session metadata model is never advertised as a selectable Remote model.

The negative rows are deliberate capability boundaries, not placeholders. Adding
one later requires a sampled official app-server request/response plus a bridge
mapping and iOS test before the descriptor is widened.
