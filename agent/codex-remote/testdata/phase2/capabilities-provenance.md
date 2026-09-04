# Remote capability audit

The Remote backend advertises only capabilities backed by a concrete adapter
interface. `session_state` comes from the connected Remote lifecycle and
`session_history` from the official `thread/read` adapter. The following
operations are intentionally absent:

| Capability | Why it is absent |
| --- | --- |
| `model_switch`, `provider_switch` | No payload-preserving Remote `model/list`, `config/read`, or `thread/settings/update` capture; no adapter is enabled. |
| `permission_resolve` | Advertised via `SessionPermissionResponder` for the client-orchestrated Plan follow-through (`ThreadItem::Plan` → synthesized `plan_review` card → `turn/start` `"Implement the plan."` + Default mode). This is not a Remote `serverRequest` approval; command/fileChange requestApproval still has no sampled payload and stays fail-closed. |
| `structured_user_input_v1`, `question_reply` | No target Remote server-request payload was observed; the event pump rejects requests with `-32601`. |
| `session_mutation`, `session_delete` | No sampled Remote archive/rename/delete/fork response and no corresponding adapter interface. |
| `session_pagination`, `compression` | No Remote cursor/turn-compaction capability was frozen; the history reader uses bounded client-side trimming only. |

The bridge derives this set from the `codex-remote` identity and concrete
interfaces, not from the codex-web capability table. The regression test
`TestCodexRemoteDescriptorOnlyAdvertisesImplementedCapabilities` guards the
negative set, while `wire_descriptor.go` keeps static Remote claims empty.
