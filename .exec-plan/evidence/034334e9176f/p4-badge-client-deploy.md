# P4 badge client — deploy evidence (2026-09-13)

- Build: ./scripts/build-unsigned-release.sh → BUILD SUCCEEDED
- Runtime identity: cordcode-bridge-runtime 0.1.0 (commit: c7d120a103da, built: 2026-09-12T17:17:49Z)
- Install: codesign verified; backup /tmp/cordcode-p4-badge-install.KoNb5U/CordCodeLink.app; replaced /Applications/CordCodeLink.app; relaunched
- Listener: port 8777 pid 41597 = installed runtime; management /internal/status ready
- iOS side: remote-web commit aef93097 (client lifecycle + SW watermark); served by the runtime's remote-web assets — Mac runtime update is the only deployment needed (capability ships with the binary; see memory cordcode-link-prod-deploy)
