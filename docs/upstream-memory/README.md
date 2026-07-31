# Upstream Memory Archive

This directory is a local-only historical reference copied from the pre-split
repository:

- Source: `/Users/jacklee/Projects/opencode-cc-connect`
- Source commit: `a59870bc132d2118dd4b9c4a821512e77f48b791`
- Imported: `2026-06-13`
- Git policy: ignored through `.git/info/exclude`; do not force-add or publish.

## Authority

The current `cccode-macbridge` source code and its maintained `docs/` files are
authoritative. Files in this archive preserve project memory and may contain:

- former monorepo paths;
- obsolete Node.js UnifiedBridge architecture;
- superseded build, launchctl, FRP, certificate, or Relay procedures;
- machine-specific IP addresses, endpoints, device identifiers, and paths;
- implementation plans that describe intent rather than final behavior.

Verify every historical claim against the current split repositories before
using it for implementation or operations.

## Contents

- `public/`: the original repository's curated post-split overview.
- `migration/`: repository split decisions, completion evidence, and Task E.
- `architecture/`: MacBridge product architecture and phase decisions.
- `runtime/`: cc-connect/go-bridge runtime and backend source walkthroughs.
- `protocol/`: Bridge v1 and Relay v1 historical contract documents.
- `relay/`: E2E Relay design, VPS service design, and direct-path baselines.
- `connectivity/`: pairing, reconnect, lifecycle, and coordinator analysis.
- `operations/`: build, signing, regression, integration, and device runbooks.
- `legacy/`: useful but explicitly superseded UnifiedBridge/FRP-era material.

## Reading Order

1. `public/document-status.md`
2. `migration/2026-06-01-CCCode-iOS-MacBridge-拆仓与cc-connect合并方案完成情况.md`
3. `architecture/CCCode-Mac-Bridge-Phase0-开发决策摘要.md`
4. `runtime/2026-04-30-cc-connect-runtime-源码走读总纲.md`
5. Current repository `docs/protocol/`, then historical `protocol/` only for
   rationale and compatibility context.

