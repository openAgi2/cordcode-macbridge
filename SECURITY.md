# Security Policy

Report security issues privately to the repository owner. Do not open public
issues for vulnerabilities, credentials, pairing bypasses, relay abuse, or
device trust failures.

## Sensitive Areas

- Device pairing and revocation.
- Relay route credentials and mailbox envelopes.
- Local management API token handling.
- Agent process spawning and environment redaction.
- File read and workspace access RPCs.

## Public Release Checklist

- Only the documented public Relay endpoint may be committed. No route ID,
  provisioning token, password, private key, or Apple Team identifier may be
  committed.
- `relay-server` production environment files stay outside the repository.
- Release builds use explicit signing and notarization owned by the publisher.
- Protocol changes update `docs/protocol/` and the iOS compatibility notes.
