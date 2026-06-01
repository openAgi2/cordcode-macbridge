# Signing And Release

The repository builds with local signing for development. Public release
requires owner-provided Apple Developer credentials and notarization policy.

## Development

The project uses placeholder public-candidate bundle identifiers:

- App: `org.openagi.cccode.macbridge`
- Tests: `org.openagi.cccode.macbridge.tests`

Use a private Xcode configuration or local project override for team-specific
settings. Do not commit:

- Apple Developer Team IDs.
- Provisioning profile names or UUIDs.
- Certificate names tied to an individual account.
- Notarization credentials.

## Release Gate

Before release:

- Set final bundle identifiers.
- Configure hardened runtime and notarization.
- Run CI, Gitleaks, and the manual Task E integration checklist.
