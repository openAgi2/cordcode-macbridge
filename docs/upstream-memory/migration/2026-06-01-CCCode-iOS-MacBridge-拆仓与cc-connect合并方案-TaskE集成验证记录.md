# Task E Integration Verification Record

Date: 2026-06-01

Status: passed. Direct LAN pairing, online Relay over 5G, real Claude Code send/receive, offline mailbox store/replay/ACK, revoke-deny, reconnect-after-network-interruption, and backend switching have been exercised against the split repository products.

## Scope Required By Plan

Task E requires validation with the split repository products:

- Real MacBridge app from `/Users/jacklee/Projects/cccode-macbridge`.
- Real iOS app from `/Users/jacklee/Projects/cccode-ios`.
- Direct path.
- Relay path.
- Offline mailbox replay.
- Revoke and reconnect behavior.
- Backend switching.

The plan explicitly says UI tests, snapshot tests, simulator automation, visual automation, and real-device validation require owner authorization. No mock, fallback, or placeholder path may be used as a substitute.

## Current Result

Task E product validation was started after owner authorization.

Completed evidence:

- MacBridge Debug macOS build passed from `/Users/jacklee/Projects/cccode-macbridge`.
- MacBridge app launched successfully.
- Embedded `cccode-bridge-runtime` launched from the app bundle.
- Runtime listened on TCP port `8777`.
- Runtime argv was checked after hardening and no longer exposed `management-token`, `opencode-pass`, `relay-route-id`, `relay-credential`, or `relay-endpoint`.
- Owner installed the real iOS app successfully.
- Owner scanned the pairing QR code successfully over LAN.
- Runtime management status reported local listening URL.
- Public-candidate MacBridge build was fixed so official Relay provisioning is skipped when no `CCCODEOfficialRelayEndpoint` is present in the app bundle.
- Relaunched MacBridge after the fix; runtime status stayed `relay.configured=false endpoint="" routeId=""`, with no stale old-domain relay endpoint or route in runtime argv/config.
- Built and installed a private local MacBridge test package at `/Applications/CCCodeBridge.app` with `CCCODEOfficialRelayEndpoint` injected into the app bundle Info.plist.
- Re-signed the private local package and verified `codesign --verify --deep --strict /Applications/CCCodeBridge.app`.
- Launched the installed MacBridge app; embedded runtime launched from `/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime` and listened on TCP port `8777`.
- Runtime management status reported `relay.configured=true` with the owner-provided official Relay endpoint and an allocated route id.
- A newly generated pairing payload was checked and contains both the LAN connection data and official Relay fields.
- 2026-06-04 relay-path manual test on iPhone 5G: owner scanned QR and approved on Mac; iOS appeared to have no user-visible reaction after approval.
- Mac-side evidence for that failed relay-path attempt: management status still reported relay configured, the Mac runtime maintained an established outbound TCP connection to the Relay endpoint, `/internal/devices` showed new iOS device records with `relayEnabled=true`, and the Relay route's pending pairing claims list was empty after approval. This narrows the failure to post-approval iOS result handling or the subsequent online relay connection/hello path, not to QR relay field generation or Mac approval reaching the device store.
- 2026-06-04 follow-up real-device result: after rebuilding/installing the iOS app with the post-approval connection failure surfaced in UI, owner confirmed iPhone on 5G can scan Relay QR, connect through Relay, use Claude Code mode, send a message, and receive a response.
- 2026-06-04 owner-reported regression results: revoke after pairing denies access as expected; network interruption reconnect recovers; backend switching works in the intended release path.
- 2026-06-04 direct mailbox investigation on the connected real iPhone: `devicectl` detected the installed `org.openagi.cccode` app and process, copied the app data-container preferences, decoded `saved_bridges_v1`, and recovered the current Relay route/device tuple for the real iOS pairing: route `rt_TsHSl1bhqNfuidOjLX7Osw`, device `dev_89cc3a6757c24d5c94c2959014a8994a`, endpoint `wss://relay.byteseek.uk:8443`.
- 2026-06-04 Relay mailbox API baseline for that real iOS device returned no pending frames: `GET /v1/routes/rt_TsHSl1bhqNfuidOjLX7Osw/devices/dev_89cc3a6757c24d5c94c2959014a8994a/mailbox?after=0&limit=100` with the device relay credential returned `{"frames":null}`. Route status with the route credential returned `pendingFrameCount=0` and `pendingMailboxBytes=0`.
- 2026-06-04 automated offline replay attempt did not complete: the Mac product bridge correctly rejects unauthenticated `/bridge` WebSocket probes with `auth.missing_token`, and the Mac side only stores device-token hashes, not plaintext device tokens. This prevents a Mac-side probe from impersonating the iOS business client to create a real authenticated backend turn. No mock envelope, fake mailbox frame, or placeholder event was injected.
- 2026-06-04 code fix implemented in `/Users/jacklee/Projects/cccode-macbridge`: MacBridge now generates encrypted durable mailbox markers for relay-enabled devices instead of trusting stale local broadcaster state as proof of delivery, RelayBridgeClient unregisters stale relay device connections from the broadcaster on close/re-auth, and relay-server treats `keyEpochId="mailbox:*"` envelopes as store-only so online devices are not sent mailbox ciphertext on the live channel.
- 2026-06-04 verification for the fix passed: `go test ./go-bridge/... -count=1` and `(cd relay-server && go test ./... -count=1)`.
- 2026-06-04 VPS deployment completed after owner provided working SSH access: rebuilt `relay-server` for linux/amd64, verified checksum on `47.236.182.45`, replaced `/opt/cccode-relay/bin/relay-server`, restarted `cccode-relay.service`, and confirmed `/healthz` returned `{"status":"ok"}` and `/readyz` returned `{"status":"ready"}`.
- 2026-06-04 local MacBridge deployment completed with the matching full mailbox fix: rebuilt `cccode-bridge-runtime`, installed it into `/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime`, re-signed and verified the app bundle, relaunched MacBridge, and confirmed runtime PID `64023` listens on TCP `8777`.
- 2026-06-04 post-deploy status checks passed: runtime management status reports `relay.configured=true`, endpoint `wss://relay.byteseek.uk:8443`, route `rt_TsHSl1bhqNfuidOjLX7Osw`; Relay route status reports `deviceCount=3`, `pendingFrameCount=0`, `pendingMailboxBytes=0`; the paired iOS device mailbox still returns no pending frames.
- 2026-06-04 self-run offline replay attempt: a temporary local trusted device was paired through the real MacBridge management/pairing flow and used to send real Claude Code requests over authenticated `/bridge`. Both attempts produced real `turn_completed` events (`0620700d-55f2-4b0e-9122-084069195299` and `c720253c-d86c-45a8-83d0-8d7b2ef2af36`), but the paired iOS device mailbox remained empty and route status stayed `pendingFrameCount=0`.
- 2026-06-04 diagnosis endpoint added and installed locally: `GET /internal/relay/delivery-prekeys` reports per-device in-memory delivery prekey watermarks without exposing prekey material. After installing the diagnostic runtime (`pid=85273`) and restarting VPS Relay to force reconnect, both non-revoked relay-enabled iPhone records still reported `availableCount=0` with `lowWatermark=10` and `targetCount=32`. This explains the empty mailbox result: MacBridge correctly fails closed when no iOS delivery prekey is available, because it cannot create an iOS-decryptable mailbox envelope.
- 2026-06-11 delivery-prekey blocker cleared on the real paired iPhone: `/internal/relay/delivery-prekeys` reported `availableCount=31`.
- 2026-06-11 offline mailbox baseline was captured from the production Relay route: `pendingFrameCount=0`, `pendingMailboxBytes=0`.
- The iOS app was terminated and a temporary trusted local device was paired through the real MacBridge pairing/approval/authentication flow. It sent a real Claude Code request through `/bridge`; session `18ddb3cc-7554-4070-9935-1c61d0e40a7f` reached a real `turn_completed` event. The temporary device was revoked after the probe.
- While the iOS app was offline, production Relay route status changed to `pendingFrameCount=1`, `pendingMailboxBytes=904`. No mock envelope, synthetic mailbox row, fallback, or placeholder event was used.
- After the real iPhone restarted, unlocked, and opened CCCode over cellular data, the installed `org.openagi.cccode` process became active and connected to the Relay endpoint over `pdp_ip0` (5G/LTE). The app's WebContent process also became active.
- Production Relay route status then changed from `pendingFrameCount=1`, `pendingMailboxBytes=904` back to `pendingFrameCount=0`, `pendingMailboxBytes=0`. This is the server-side ACK evidence for the fetched mailbox frame.
- The temporary white-screen incident during the run was not an app crash or mailbox deadlock. Device crash reports contained no CCCode crash, and the first white screen was a stale system snapshot while no CCCode process existed. Restarting the iPhone cleared the stale Xcode/CoreDevice debug session; CCCode then launched normally without a source change.

Release-boundary notes:

- The committed public-candidate source intentionally does not contain the owner-hosted Relay endpoint.
- `/Applications/CCCodeBridge.app` is a private local validation package with the endpoint injected outside Git.
- Real-device tooling used Xcode 27 beta against an iOS 27 beta build one revision newer than the bundled device support image. This caused CoreDevice launch/screenshot timeouts during evidence collection, but did not prevent the installed app from connecting to Relay and acknowledging the mailbox frame.

Previous signing failure class, now superseded by owner successful iOS install:

```text
No Account for Team "6L3SKKKWK5".
No profiles for 'org.openagi.cccode' were found.
```

No simulator, mock, fallback, or placeholder app was used as a substitute.

## Ready Inputs

MacBridge repository:

- `/Users/jacklee/Projects/cccode-macbridge`
- Task A build gates passed.
- Task D readiness report: `/Users/jacklee/Projects/cccode-macbridge/docs/public-readiness.md`
- Release checklist: `/Users/jacklee/Projects/cccode-macbridge/docs/release-checklist.md`

iOS repository:

- `/Users/jacklee/Projects/cccode-ios`
- Task B build gates passed.
- Task D readiness report: `/Users/jacklee/Projects/cccode-ios/docs/public-readiness.md`
- Release checklist: `/Users/jacklee/Projects/cccode-ios/docs/release-checklist.md`

## Validation Checklist To Run After Authorization

- Launch the real MacBridge app and confirm embedded runtime startup. Current result: passed.
- Pair the real iOS app with MacBridge. Current result: passed over LAN by owner.
- Verify direct WebSocket path. Current result: passed; LAN QR pairing passed and an authenticated local probe completed a real Claude Code backend turn.
- Use the installed private MacBridge package with the owner-provided official Relay endpoint for manual relay validation.
- Verify relay path from the real iOS app.
- Verify offline mailbox replay. Current result: passed on 2026-06-11; a real Claude `turn_completed` milestone changed the production Relay mailbox from zero to one pending encrypted frame while CCCode was offline, and the real iPhone's 5G reconnect consumed and acknowledged it, returning the route to zero pending frames.
- Revoke the paired device and confirm access is denied. Current result: passed by owner report.
- Verify reconnect after network interruption. Current result: passed by owner report.
- Verify backend switching for the intended release backends. Current result: passed by owner report.
- Record exact app builds, device model/iOS version, Mac model/macOS version, relay endpoint class, and pass/fail evidence.
