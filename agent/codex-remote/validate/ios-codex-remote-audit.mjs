#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptPath = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(scriptPath), "../../..");
const iosRoot = process.argv[2] ?? "/Users/jacklee/Projects/cordcode-ios-codex-remote";
const baseline = "d0762cb9a05997b615ef4589f39afad8f4b4db04";
const expectedBranch = "codex/codex-remote-backend-ios";

function run(command, args, cwd = repoRoot) {
  return execFileSync(command, args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function fail(message) {
  throw new Error(message);
}

function expect(condition, message) {
  if (!condition) fail(message);
}

function expectFileContains(relativePath, needles) {
  const absolutePath = path.join(iosRoot, relativePath);
  const contents = readFileSync(absolutePath, "utf8");
  for (const needle of needles) {
    expect(contents.includes(needle), `${relativePath}: missing ${JSON.stringify(needle)}`);
  }
}

expect(run("git", ["-C", iosRoot, "branch", "--show-current"]) === expectedBranch, "iOS branch is not the authorized codex-remote branch");
expect(run("git", ["-C", iosRoot, "status", "--porcelain"]) === "", "iOS worktree must be clean for the source audit");
run("git", ["-C", iosRoot, "merge-base", "--is-ancestor", baseline, "HEAD"]);

expectFileContains("OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift", [
  "case codexRemote",
  'case "codex-remote": return .codexRemote',
  "case .codexRemote:",
  "BackendServerIdentity",
  "backendKind.rawValue",
  "usesBackendLiveEventStream",
  "usesRootOnlySessionCatalog",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOS/Services/Backend/BridgeDiscoveryService.swift", [
  '"codex-remote"',
  ".codexRemote",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift", [
  'return "codex-remote"',
  "supportsSessionSyncV2 = capabilities.contains(\"session_sync_v2\")",
  "supportsSessionPagination = false",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift", [
  "baseConfig?.backendKind == .codexRemote",
  "case .codexRemote:",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+AgentRuntimeStatus.swift", [
  "case .codex, .codexWeb, .codexRemote:",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift", [
  "currentInputBackendKind == .codexRemote",
  'return "Codex Desktop"',
]);
expectFileContains("OpenCodeiOS/OpenCodeiOS/Models/SessionLifecycleDiagnosticPhase.swift", [
  "case .codexRemote:",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOSTests/BridgeTransportTests.swift", [
  "testFromWireKind_codexRemote",
  "BackendKind.serverCreationCases.contains(.codexRemote)",
]);
expectFileContains("OpenCodeiOS/OpenCodeiOSTests/AgentRuntimeStatusTests.swift", [
  "testCodexRemoteUsesCodexRuntimePresentationSource",
]);

expect(run("git", ["diff", "--name-only", "--", "agent/codex-web", "agent/codex"]) === "", "legacy Codex backend directories have worktree changes");

console.log(JSON.stringify({
  result: "PASS",
  classification: "STATIC-IOS-SOURCE-AUDIT",
  iosRoot,
  baseline,
  branch: expectedBranch,
  worktree: "clean",
  legacyBackendDiff: "clean",
  note: "This validator proves source wiring and ancestry only; it does not claim Swift compilation, UI automation, or a physical-device observation.",
}, null, 2));
