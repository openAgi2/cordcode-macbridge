# Phase 3 capability-driven iOS UI

The Remote descriptor currently advertises only capabilities implemented by the
MacBridge adapter:

| Capability | Remote behavior | iOS result |
| --- | --- | --- |
| `session_state` | present | session status is displayed and routed. |
| `session_history` | present | authoritative `thread/read` history is used. |
| `session_sync_v2` | present | projection/live events are the timeline authority. |
| `model_switch` | present | picker rows come only from official app-server `model/list`; selected `model`/`effort` are sent through official `turn/start`. |
| `permission_resolve`, `permission_mode`, `structured_user_input_v1` | absent | approval/question controls stay hidden and requests remain fail-closed. |
| `session_mutation`, `session_delete`, `session_pin`, pagination | absent | no unsupported mutation/pagination affordance is shown. |
| attachments | absent in the Remote app-server input sample | the Mac adapter rejects image/file turn input instead of dropping it silently. |

The iOS UI uses the descriptor for capability flags, while `BackendKind.codexRemote`
only supplies stable product copy and routing. In particular, the Remote path does
not inherit Codex-Web model-provider qualification, daemon paths, or approval UI.
The “Codex Desktop” model chrome is shown only when the live Remote app-server
returns a real catalog; failures stay visible as an empty/error result, with no
built-in fallback or session-metadata row promoted into a selectable model.

The remaining negative rows are deliberate capability boundaries, not placeholders.
