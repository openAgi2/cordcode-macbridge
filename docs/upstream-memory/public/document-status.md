# Document Status And Migration Notes

## Source Of Truth

The `cccode-ios` and `cccode-macbridge` repositories are the source of truth for
current source code, build commands, protocols, supported backends, and release
configuration.

This repository is the former integration workspace. Its historical records
remain valuable, but most are snapshots of a particular implementation stage.

## Maintained Here

Only the following set is curated as current public documentation:

- the repository root `README.md`;
- `docs/README.md`;
- files under `docs/public/`.

## Historical Or Internal

Treat these categories as non-current unless a split repository links to them:

- dated design plans and completion reports;
- review, audit, and test evidence;
- handoff documents;
- `todo.md`, `think.md`, agent instructions, and execution-plan state;
- files under `docs/archive/`;
- old VPS, FRP, VPN, certificate, process-launch, or local-path guides.

Some historical files contain environment-specific values and were written
before the repository split. They must not be published in bulk.

## Migration Rules

When useful historical content is promoted into public documentation:

1. verify it against the current split repositories;
2. remove credentials, private infrastructure details, and absolute paths;
3. describe stable behavior rather than one machine's setup;
4. move canonical protocol details beside the implementing source;
5. label version-specific or operational material explicitly.

## Superseded Assumptions

The following former assumptions are no longer valid:

- iOS, MacBridge, the Go bridge, and the message renderer are maintained as one
  monorepo;
- a separately managed local bridge process is the normal user workflow;
- FRP, Tailscale, or a VPN is required for ordinary remote use;
- every backend mentioned in old capability tables exists in the migrated
  runtime;
- old deployment endpoints and service definitions are production guidance.
