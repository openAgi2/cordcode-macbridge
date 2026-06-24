# Privacy

CordCode Link runs coding-agent backends on the user's Mac. The application
does not include analytics, advertising, or telemetry SDKs.

## Local Data

MacBridge stores local configuration, pairing records, generated management
credentials, backend connection settings, logs, and Relay credentials on the
Mac. Sensitive runtime credentials are not committed to this repository.

Coding backends may access files and execute tools according to their own
configuration and the permissions granted by the user.

## Network Data

On a local network, the iOS client can connect directly to MacBridge.

When Relay is used, the Relay service processes routing identifiers, connection
metadata, and encrypted envelopes needed to deliver messages. Message payloads
are end-to-end encrypted between paired devices. Offline mailbox storage, when
used, stores encrypted envelopes until delivery or expiry.

The coding backends themselves may contact their respective providers. Their
data handling is governed by the backend and provider selected by the user.

## User Control

Users can revoke paired devices in MacBridge, select a self-hosted compatible
Relay, or remove local application state during uninstall.

Security issues should be reported according to `SECURITY.md`.
