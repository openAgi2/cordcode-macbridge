# Getting Started

CCCode connects an iPhone or iPad to coding agents running on a Mac.

## Requirements

- CCCode installed on the iPhone or iPad;
- CCCodeBridge installed and running on the Mac;
- at least one supported coding backend available on the Mac;
- network access between the devices, either directly or through Relay.

## Pair A Device

1. Open CCCodeBridge on the Mac.
2. Open its pairing screen and generate a QR code.
3. In CCCode on iOS, scan the QR code.
4. Approve the pairing request on the Mac.
5. Select an available backend and open or create a session.

Pairing credentials are created for the approved device. They are not shared
between unrelated devices and are not compiled into either application.

## Connection Behavior

When the phone and Mac are reachable on the same local network, CCCode prefers
the direct connection. When direct access is unavailable, the apps use the
configured encrypted Relay automatically.

MacBridge uses `wss://relay.byteseek.uk:8443` by default. A user can replace it
in MacBridge settings with a compatible self-hosted Relay endpoint. The iOS app
receives the applicable connection information during pairing; the production
endpoint is not hardcoded independently in the iOS app.

Temporary network loss should not replace the session list with a fatal setup
screen. The app reconnects in the background, exposes a lightweight connection
state, and keeps already loaded content available.

## Revoke A Device

Use MacBridge device management to revoke a paired phone. A revoked device can
no longer use its previous route credentials and must be paired again before it
can connect.

## Troubleshooting

- Confirm that CCCodeBridge is running and that the selected backend is
  available.
- Refresh the pairing information if the device was previously revoked.
- For a custom Relay, verify that both applications can reach its secure
  WebSocket endpoint.
- Treat old FRP, VPN, manual port-forwarding, and launch-agent guides in this
  legacy repository as historical material, not required setup.
