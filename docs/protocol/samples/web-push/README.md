# Web Push contract fixtures (shape-only)

These are **internal contract fixtures**: they pin the wire shape (envelope, field names,
JSON tags) of `web_push_v1` for cross-repo consistency tests. They are NOT external
interoperability evidence. Real-device/real-endpoint evidence lives under the separate
sample gates defined by
`cordcode-ios/docs/2026-08-26-remote-web-push-notification-implementation-plan.md` §3.2:

- WP-SUB-1 / WP-SUB-LOCAL-1 / WP-SUB-2 — real iOS `PushSubscription.toJSON()` archives
- WP-RESP-1/2/3 — real Apple push service responses
- EVT-TURN-1 / EVT-PERM-1 / EVT-INPUT-1 / EVT-ERROR-1 — real backend event payloads

Placeholder values (`BASE64URL_fixture_*`, `*.example.comfixture` hosts) are deliberate:
they prevent these files from being mistaken for real captured secrets, and gitleaks-safe.
Until the real sample gates pass, notification producers remain disabled and these fixtures
prove shape only.
