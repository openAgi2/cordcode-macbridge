# Signing And Release

The repository builds with local ad-hoc signing for development and unsigned
preview distribution. A warning-free public release requires owner-provided
Apple Developer credentials and a notarization policy.

## Development

The project uses placeholder public-candidate bundle identifiers:

- App: `org.openagi.cordcode.link`
- Tests: `org.openagi.cordcode.link.tests`

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

## Unsigned Preview Channel

Until Developer ID credentials are available, run:

```bash
./scripts/build-unsigned-release.sh
```

This creates an Apple Silicon zip and SHA-256 checksum under `dist/`. The
GitHub `Unsigned Release` workflow can publish the same artifacts as a
prerelease. Users must control-click the app and choose Open on first launch.

Unsigned previews are not a replacement for Developer ID signing or
notarization. Never instruct users to disable Gatekeeper globally.

## Future Notarized Channel

After joining the Apple Developer Program:

1. sign the app and embedded runtime with a Developer ID Application identity;
2. enable and verify hardened runtime settings;
3. submit the archive with `notarytool`;
4. staple the notarization ticket;
5. verify the final artifact with `codesign` and `spctl`;
6. upload the notarized artifact to GitHub Releases or the selected download
   channel.
