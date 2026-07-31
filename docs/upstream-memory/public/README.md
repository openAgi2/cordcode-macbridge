# CCCode Public Documentation

This is the curated documentation set for the current split architecture.

## Read In This Order

1. [Getting started](getting-started.md) explains the user-facing connection
   flow.
2. [Architecture](architecture.md) describes ownership across the iOS and
   MacBridge repositories.
3. [Relay and security model](security-and-relay.md) explains direct and remote
   connectivity without exposing deployment secrets.
4. [Development](development.md) lists the current build and validation entry
   points.
5. [Document status and migration notes](document-status.md) explains what is
   current and what remains historical.

## Scope

These documents intentionally avoid machine-specific paths, credentials,
private deployment details, and temporary validation notes. They are suitable
as the basis for public repository documentation.

The split repositories remain authoritative for source-specific commands,
protocol schemas, release configuration, and supported platform versions.
