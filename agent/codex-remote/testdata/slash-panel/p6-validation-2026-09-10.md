# P6 delivery validation (2026-09-10)

No UI test, snapshot test, or device/simulator interaction automation was run. The only simulator
execution was the focused non-UI `CCCodeTests/SlashCommandPanelTests` unit-test bundle.

## Results

| Gate | Result | Durable evidence |
| --- | --- | --- |
| Go runtime build | PASS | `.build/P6-20260910/cordcode-bridge-runtime`; arm64; version `0.1.0-dev`; SHA-256 `502a03ec0a152ea57732e672e6e237c281e88fe7fe1bce7a7bae49307746b9dd` |
| Go regression | PASS | `.build/P6-20260910/go-tests.log`: core, codex-remote, and go-bridge all pass; go-bridge 72.957s |
| Mac Release build | PASS | `.build/P6-20260910/mac-release-build.log` contains `BUILD SUCCEEDED`; app identity `org.openagi.cordcode.link` / `0.1.0`; embedded runtime reports commit `700f071b70de`, build `2026-09-10T13:39:51Z`, SHA-256 `7a0915f3a49b9c7f7399b0ab6c08d9a2f5aee2ce188aaa999b017a7f9a158f05` |
| Protocol mirror | PASS | Mac canonical and iOS mirror `bridge-v1.md` plus `schema/bridge-v1.types.ts` are byte-identical; `TestProtocolMirrorInSync` passes with the explicit iOS mirror path |
| Protocol/static contracts | PASS | `.build/P6-20260910/protocol-contract-tests.log`; both worktrees pass `git diff --check`; frozen Mac `agent/codex-web` and `agent/codex` have no diff from HEAD or working tree |
| iOS build for testing | PASS | `/Users/jacklee/Projects/cordcode-ios-plan-approval/.build/P6-20260910/ios-build-for-testing.log` contains `TEST BUILD SUCCEEDED`; app identity `org.openagi.cordcode` / `1.0.0` |
| Focused Swift non-UI tests | PASS | `/Users/jacklee/Projects/cordcode-ios-plan-approval/.build/P6-20260910/ios-unit-tests.xcresult`: 18 passed, 0 failed/skipped/expected failures; 245.112s total; executable SHA-256 `652449951a8e5bd90f2f34bc324d87a074aef29fdbd4244eeb1bca644c371fee` |
| Upstream cold-read tests | PASS | `.build/P6-20260910/upstream-thread-settings-get.log`: 1/1 integration test; `.build/P6-20260910/upstream-persisted-resume.log`: 3/3 focused unit tests |
| Live evidence validators | PASS | `native-actions-fixture PASS section=all`; `plan-cold-fixture PASS authoritativeColdSource=thread/settings/get` |
| Secret scan | PASS | Gitleaks scanned the Mac delivery diff, the full `agent/codex-remote` tree, and the iOS delivery diff with redacted output; no finding |
| Physical-device availability | BLOCKED | `xcrun xctrace list devices` reports both known iPhones offline; no install or owner UI matrix was claimed |

The first standalone Go runtime command used the nonexistent package
`./cmd/cordcode-bridge-runtime` and failed visibly. It was corrected to the repository-owned
`./go-bridge/cmd/cordcode-bridge-runtime`; the corrected fail-fast build and identity checks pass.
