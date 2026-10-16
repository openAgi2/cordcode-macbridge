# 本轮任务完成情况：Claude programmatic session 过滤与 API error 通知修复方案

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- Plan: `docs/2026-10-16-claude-programmatic-sessions-and-api-error-notifications-plan.md`
- Canonical State File: `.exec-plan/state/plan-bade133f63ef.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: 20/20 todos done, 20/20 proven; 10 re-verified, 10 self-attested (including justified N/A regression gates)
- Related Commits: implementation is the reviewed working tree on top of `f10c138a9561f22c1282ef25c464859c28681c01`; no implementation commit was created in this delivery
- Generated At: `2026-10-16T14:45:07Z`
- Queue Hash: `5bcc8f6760b9`

Source identity at install:

| Repository / artifact | Identity |
| --- | --- |
| MacBridge worktree | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`, branch `feat/ios-native-message-timeline`, commit `f10c138a9561f22c1282ef25c464859c28681c01`, dirty with this implementation |
| Paired iOS worktree | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`, branch `feat/ios-native-message-timeline`, commit `02a46521678950fd1df47ac951505f3dccba0d4e`, clean; **not modified** |
| Installed runtime | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`, SHA-256 `b98b0ee62dde99961f2b0bce6d27b857c34e580210ca6de8900d2870ea19d913`, version reports commit `f10c138a9561` and build time `2026-10-16T14:33:20Z` |
| Upstream package | `@anthropic-ai/claude-agent-sdk@0.3.301`, npm tarball retained under `build/upstream-claude-agent-sdk-0.3.301/` |

## 1. Overall Verdict (总体结论)
The v4 plan is implemented and deployed. Programmatic Claude sessions are now excluded from the default visible catalog and Web Push watcher using the official `includeProgrammatic:false` category, while transcripts and `ByKey` cache identity remain intact. Synthetic Claude API errors now become terminal `turn_error` events, and both live control planes settle to idle instead of presenting the failure as a completed turn.

All required queue items are proven complete. Focused tests, official SDK oracle comparison, whitespace check, and Release build were re-run or independently executed from artifacts; production process/listener/generation/log checks were performed against the installed app. No iOS source was modified, and no UI/snapshot/simulator automation was run.

## 2. Phase Completion Matrix (阶段完成矩阵)
| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| S1 classifier | proven-done | proven-done | n/a (justified) | complete | implementation self-attested; tests re-verified |
| S2 catalog visibility/cache | proven-done | proven-done | n/a (justified) | complete | implementation self-attested; tests re-verified |
| S3 Web Push exclusion | proven-done | proven-done | n/a (justified) | complete | implementation self-attested; tests re-verified |
| S4 API-error mapping | proven-done | proven-done | n/a (justified) | complete | implementation self-attested; tests re-verified |
| S5 live relay terminal handling | proven-done | proven-done | n/a (justified) | complete | implementation self-attested; tests re-verified |
| S6 official SDK oracle | n/a | proven-done | proven-done | complete | cross-implementation comparison re-verified |
| S7 docs/tests/deployment | proven-done | proven-done | proven-done | complete | build self-attested; tests and runtime checks re-verified |

### 2.1 Upstream Anchors (上游锚点)
| Fix / todo | Upstream anchor (file:line) or exemption card | First divergence vs upstream |
| --- | --- | --- |
| Programmatic classifier (`YD`) | `@anthropic-ai/claude-agent-sdk@0.3.301/package/sdk.mjs:1` (minified symbols `YD`/`nl`/`jt`; byte offsets approximately 379747/380202/380424) and `package/sdk.d.ts:1149-1158` | Go parses top-level JSON lines instead of raw substring matching, uses the existing 512KiB head rather than 64KiB, and adds a top-level 64KiB tail fallback. The plan explicitly documents both boundaries. |
| Default catalog visibility | `package/sdk.d.ts:1149-1158` (`includeProgrammatic`) and `package/sdk.mjs:1` (`L0` filter path) | CordCode's compatibility catalog scans visible top-level `~/.claude/projects` workspaces globally and retains hidden entries in `ByKey`; the official SDK is queried per `dir` and returns a filtered result list. |
| Web Push visibility boundary | `package/sdk.d.ts:1149-1158`; `package/sdk.mjs:1` (`L0` filter path) | The SDK has no Web Push concept. CordCode reuses the same visible `Sorted` set as its watcher boundary rather than inventing a title/status filter. |
| Official hidden-set oracle | `package/sdk.d.ts:1149-1158`; `package/sdk.mjs:1` (`L0`/`YD`) | The oracle is a development-only harness: official TS enumerates the target `dir`; Go compares its test-only internal `includeProgrammatic` switch over the same non-worktree project scope. |
| Synthetic API error → `turn_error` | Local protocol contract `docs/protocol/bridge-v1.md` (`turn_error` terminal semantics) plus real transcript fields from plan E-6; this is not an upstream SDK port | Claude has no public mapper API for this CordCode projection boundary. CordCode parses `isApiErrorMessage` / API error fields and maps them to its existing protocol-level failed-turn event. |

## 3. Key File Changes (关键文件变更)
- `go-bridge/claude_programmatic_sessions.go`: adds the official SDK/daemon classifier and 64KiB tail entrypoint reader.
- `go-bridge/handlers.go`: carries `entrypoint`, `sessionKind`, and `Programmatic` through bounded transcript metadata parsing.
- `go-bridge/claude_session_catalog.go`: filters programmatic entries from `Sorted` before fork/compact lineage, retains them in `ByKey`, preserves archived wire behavior, and emits the hidden-count log.
- `go-bridge/claude_web_push_watcher.go`: skips programmatic catalog entries and therefore never tails or notifies them by default.
- `go-bridge/handlers_relay.go`: parses synthetic API error fields and emits terminal `turn_error`; live and legacy relay handling settle to idle.
- `go-bridge/claude_programmatic_sessions_test.go`, `go-bridge/claude_session_catalog_programmatic_test.go`, `go-bridge/claude_web_push_watcher_test.go`, `go-bridge/claude_api_error_test.go`: cover official branches, head/tail windows, cache identity, archived contract, lineage order, watcher exclusion, mapper fields, and relay idle state.
- `GO_BRIDGE_ARCHITECTURE.md`: documents the visibility boundary and API-error terminal behavior.

## 4. Verification Evidence (验证证据)
### 4.1 Automated tests
- Commands:
  - `GOTOOLCHAIN=local go test ./go-bridge -count=1 -run 'TestClaude.*Programmatic|TestClaudeSessionCatalog|TestClaudeWebPushWatcher'`
  - `GOTOOLCHAIN=local go test ./go-bridge -count=1 -run 'TestClaude.*APIError|TestClaudeEntryToProjectionEvents|TestClaudeSessionFileRelay'`
  - `GOTOOLCHAIN=local go test ./go-bridge -count=1 -run 'Test(Is|Scan)Claude.*(Programmatic|Entrypoint)|TestClaude(LiveControlPlaneSettlesOnTurnError|LegacyRelayTurnErrorSettlesIdle)'`
  - `GOTOOLCHAIN=local CORDCODE_PROGRAMMATIC_ORACLE_DIR=/Users/jacklee/Projects/cordcode-ios-native-message-timeline ... go test ./go-bridge -count=1 -run TestClaudeProgrammaticOracleLocalSnapshot`
  - `git diff --check`
- Result: all passed.
- Attestation: `re-verified` (the three focused groups and diff check were re-run during exit audit; the oracle was re-run after deployment)
- Main test files: `go-bridge/claude_programmatic_sessions_test.go`, `go-bridge/claude_session_catalog_programmatic_test.go`, `go-bridge/claude_web_push_watcher_test.go`, `go-bridge/claude_api_error_test.go`
- Artifact paths:
  - `build/claude-programmatic-audit-group1.log`
  - `build/claude-programmatic-audit-group2.log`
  - `build/claude-programmatic-audit-focused.log`
  - `build/claude-programmatic-tests-group1.log`
  - `build/claude-programmatic-tests-group2.log`
  - `build/claude-programmatic-tests-focused.log`
  - `build/claude-programmatic-oracle-comparison.json`
  - `build/claude-programmatic-runtime-day-go-oracle.json`

Official SDK oracle:

- Official TS SDK `0.3.301`, `includeWorktrees:false`, full pagination.
- TS hidden = 22; Go hidden = 22; missing = 0; extra = 0.
- Main human session `7afc3bd7-c69b-4582-85cf-556fa4f54d69` remains visible.
- None of the approved 22 hidden baseline IDs remains visible in the fresh post-deploy project oracle.

Release build:

- First attempt failed on ambient Go toolchain/GOSUMDB behavior; failure log retained at `build/claude-programmatic-release-build.failed-gotoolchain.log`.
- Retry with `GOTOOLCHAIN=local` succeeded in 39 seconds.
- Success log: `build/claude-programmatic-release-build.log`.
- Artifact: `build/unsigned-release/Build/Products/Release/CordCodeLink.app`.
- Package: `dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip`.

### 4.2 Regression evidence
- Device / replay / benchmark / manual validation: production deployment/runtime verification, no UI automation.
- Attestation: `re-verified`
- Artifact paths:
  - `build/claude-programmatic-preinstall-source-manifest.txt`
  - `build/claude-programmatic-runtime-regression.txt`
  - `build/claude-programmatic-runtime-day-go-oracle.log`
  - `build/claude-programmatic-plugin-state.txt`

Runtime evidence:

- Only `/Applications/CordCodeLink.app` app/runtime processes are running; no temporary-path process remains.
- Runtime PID `66458`, PPID `66400`, `lstart Fri Oct 16 22:38:02 2026 +08`, later than build time `2026-10-16T14:33:20Z`.
- PID `66458` owns the 8777 listener.
- Installed binary is byte-identical to the Release runtime built from this worktree.
- Unique new log `claude catalog programmatic hidden` appears with `count=38`.
- Count interpretation: 38 is the runtime's **global visible-workspace catalog** count. The approved E-8 project scope is 22; fresh post-deploy project data still yields 35 total / 13 visible / 22 hidden and zero baseline hidden IDs visible. The global count is therefore expected rather than an oracle mismatch.
- `security-guidance` remains absent from current Claude enabled plugins; its bearer-token defect is not claimed as fixed.

### 4.3 Audit downgrade summary
- Downgraded todos: none after exit audit.
- Light audit issues found and repaired before completion: missing upstream-anchor metadata on port/parity todos and one artifact path containing a line suffix. Both were corrected in the canonical state.

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)
- The Claude SDK set can change in future versions. Any extension of `sdk-*` or daemon values must first re-check the then-current official implementation.
- Go deliberately uses top-level line JSON parsing for structured fields while the SDK implementation uses raw substring probing. The known constructed divergence is a nested `entrypoint` or `sessionKind` key; no real sample in this corpus depended on it.
- If an `entrypoint` only exists between 512KiB from head and 64KiB from tail, both implementations leave the session visible. This fail-open boundary is intentional and documented.
- The plugin's own authentication failure is not fixed; `security-guidance` remains disabled.
- No iOS visual automation was run by design. Runtime log/listener plus exact post-deploy catalog data verify the Mac behavior; iPhone rendering continues to consume the unchanged protocol.

## 6. Audit Focus (建议审核重点)
1. Verify `Sorted` filtering occurs before fork/compact lineage and that `ByKey` still retains hidden identity.
2. Re-run the official TS SDK with `includeWorktrees:false` and compare all 22 hidden IDs, not just counts.
3. Confirm the installed process generation, 8777 listener, and unique hidden-count log before judging production behavior.

## 7. Constraints (关键约束)
- iOS source was not modified.
- No UI tests, snapshot tests, simulator automation, agent-device, or real-device UI automation were run.
- No Node/Python SDK sidecar was added to production.
- No Claude transcript was deleted, moved, or rewritten.
- No fallback/mock/placeholder success path was introduced.
