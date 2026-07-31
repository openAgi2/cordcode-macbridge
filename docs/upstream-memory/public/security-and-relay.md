# Relay And Security Model

CCCode uses a direct-first connection strategy with an encrypted Relay path for
remote access.

## Direct Path

On a reachable local network, the iOS app connects directly to MacBridge. This
path avoids an intermediary and normally provides the lowest latency.

## Relay Path

When direct access is unavailable, MacBridge and the iOS app connect to a Relay
using secure WebSockets. The Relay routes encrypted protocol envelopes between
paired devices. It is not the authority for coding-agent permissions and does
not receive source-controlled device credentials.

The project Relay endpoint, `wss://relay.byteseek.uk:8443`, is a public
configuration value, not a secret. MacBridge provides it as the default and
allows a user to enter a compatible self-hosted endpoint.

## Credentials

- Device and route credentials are generated at runtime.
- MacBridge stores owner-side credentials locally.
- The iOS app receives device-specific material through the approved pairing
  flow.
- Credentials, private keys, VPS passwords, and deployment tokens must never be
  committed to source control or included in public documentation.
- Revocation invalidates the affected device's access.

## Offline Mailbox

The Relay can temporarily retain encrypted messages for a paired device that is
offline. After reconnecting, the device retrieves, decrypts, presents, and
acknowledges those messages. Mailbox storage does not remove the requirement
for end-to-end encryption or device authorization.

## Self-Hosted Relay

A compatible Relay may be deployed by an organization or individual. Configure
its secure WebSocket URL in MacBridge settings, then pair devices using the
updated MacBridge configuration.

Deployment-specific domains, certificates, firewall rules, service units, and
credentials belong in private operations documentation. They should not be
copied into the public product guide.

## Reporting Security Issues

Do not publish active credentials, private pairing artifacts, or exploitable
deployment details in an issue. Use the security reporting channel configured
by the repository owner.
