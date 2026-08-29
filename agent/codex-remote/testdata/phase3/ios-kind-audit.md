# Phase 3 iOS `codexRemote` kind audit

## Source gate

* Paired worktree: `/Users/jacklee/Projects/cordcode-ios-codex-remote`
* Branch: `codex/codex-remote-backend-ios`
* Frozen Phase 0 ancestor: `d0762cb9a05997b615ef4589f39afad8f4b4db04`
* Current delivery commit is required to be a descendant of that ancestor and the
  paired worktree must be clean when the validator runs.

The static audit is replayable with:

```sh
node agent/codex-remote/validate/ios-codex-remote-audit.mjs
```

The validator intentionally proves source wiring, ancestry, and the frozen legacy
directory boundary only. It does not claim Swift compilation, UI automation, or a
physical-device result.

## Exhaustive production-path review

| Surface | Decision | Reason |
| --- | --- | --- |
| `BackendModels.swift` enum, wire decoding, display name/icon, cache identity, live-stream and root-catalog flags | included | `codexRemote` is a distinct product/wire/cache identity and uses the same push/session-list shape proven by the bridge descriptor. |
| `BridgeDiscoveryService.swift` | included | A discovered `codex-remote` descriptor must select the new enum case. |
| `CCCodeBridgeBackendClient.swift` | included | Normalizes the stable backend ID and derives SSV2/capability flags from the descriptor; pagination remains disabled. |
| `ChatViewModel+CodexStreaming.swift` | included | Remote events use the Codex live-event reducer and external-turn attribution. |
| `ChatViewModel+AgentRuntimeStatus.swift` | included | Remote runtime events are presented through the Codex status source, not the Claude fallback. |
| `ChatViewModel+Generation.swift` | included | Remote is in the live-stream allowlist and sends no invented reasoning effort. |
| `ChatUIKitContainerView.swift` | included | “Codex Desktop” chrome, hidden unsupported controls, and session composer behavior are explicit. |
| `SessionLifecycleDiagnosticPhase.swift`, `ServerViewModel.swift`, `SelectionSheets.swift` | included | Pairing/offline/protocol diagnostics and unsupported interaction copy remain truthful. |
| `ChatViewModelSessionSyncV2Tests.swift`, `BridgeTransportTests.swift`, `CCCodeBridgePhase2Tests.swift`, `AgentRuntimeStatusTests.swift` | included | Tests cover kind decoding, independent identity, SSV2 routing, and Codex runtime presentation. |
| `serverCreationCases` | intentionally excludes | Remote is paired by CordCode Link/Desktop enrollment; iOS must not fabricate a second creation flow. |
| `ServerSettingsView.swift` reasoning toggle | intentionally excludes | The composer model configuration uses the live per-model effort rows; there is still no standalone permission-mode toggle for Remote. |
| `ModelManagementService.swift` Codex-Web provider/order branches | intentionally excludes | Remote uses the official `model/list` rows directly and does not inherit Codex-Web provider/config assumptions. |
| permission-mode/approval/question menus | intentionally excludes | Remote does not advertise a resolver or structured-input capability; the product remains fail-closed. |
| legacy Codex history polling or daemon/path assumptions | intentionally excludes | Remote history comes from the app-server `thread/read` projection through the bridge. |
| generic cache/storage code | intentionally excludes | `BackendServerIdentity.cacheScopeKey` already includes backend kind, endpoint, and username. |
| `agent/codex-web/**`, `agent/codex/**` | frozen | The Phase 0–4 boundary requires zero worktree and delivery-diff changes. |

Unknown future backend kinds continue to fail closed in the existing decoding path;
the audit does not widen accepted wire aliases.
