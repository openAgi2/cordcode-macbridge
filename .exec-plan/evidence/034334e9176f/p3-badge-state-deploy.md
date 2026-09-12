# P3 badge state — deploy evidence (2026-09-13)

- Build: ./scripts/build-unsigned-release.sh → BUILD SUCCEEDED
- Runtime identity: cordcode-bridge-runtime 0.1.0 (commit: 24fb458e8391, built: 2026-09-12T16:48:06Z)
- Install: codesign verified; old app backed up to /tmp/cordcode-p3-badge-install.MpM0wa/CordCodeLink.app; replaced /Applications/CordCodeLink.app; relaunched
- Listener: port 8777 pid 23593 = /Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime
- Management /internal/status: status=ready
- Data files intact: web-push-vapid/subscriptions/ledger.json + samples; badge-state file absent until first client binding (P4 client work) — expected
- Log check: only pre-existing codex-remote pairing error and suppressed-notification warnings; no new errors, no badge diagnostics (no client binding yet)
