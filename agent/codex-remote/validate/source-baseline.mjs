#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { extractFile } from "file:///opt/homebrew/lib/node_modules/@electron/asar/lib/asar.js";

const scriptPath = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(scriptPath), "../../..");
const metadataPath = path.join(
  repoRoot,
  "agent/codex-remote/testdata/phase0/meta/source-baseline.json",
);
const metadata = JSON.parse(readFileSync(metadataPath, "utf8"));

function fail(message) {
  throw new Error(message);
}

function expectEqual(actual, expected, label) {
  if (actual !== expected) {
    fail(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

function expectIncludes(haystack, needle, label) {
  if (!haystack.includes(needle)) {
    fail(`${label}: missing ${JSON.stringify(needle)}`);
  }
}

function expectEndpointCallsite(haystack, endpoint, label) {
  const literalSegments = endpoint.split(/\{[^}]+\}/u).filter((segment) => segment.length > 0);
  for (const segment of literalSegments) {
    expectIncludes(haystack, segment, `${label} (${endpoint})`);
  }
}

function run(command, args, cwd = repoRoot) {
  return execFileSync(command, args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function sha256(buffer) {
  return createHash("sha256").update(buffer).digest("hex");
}

function plistValue(key) {
  return run("/usr/libexec/PlistBuddy", [
    "-c",
    `Print :${key}`,
    "/Applications/ChatGPT.app/Contents/Info.plist",
  ]);
}

expectEqual(metadata.schema_version, 1, "metadata schema");
expectEqual(metadata.classification, "STATIC-SOURCE-BASELINE-ONLY", "classification");
expectEqual(metadata.gate_effect, "does-not-pass-phase0", "gate effect");

expectEqual(
  run("git", ["branch", "--show-current"]),
  metadata.macbridge.branch,
  "MacBridge branch",
);
run("git", ["merge-base", "--is-ancestor", metadata.macbridge.source_gate_commit, "HEAD"]);
expectEqual(
  run("git", ["-C", metadata.ios.path, "branch", "--show-current"]),
  metadata.ios.branch,
  "iOS branch",
);
expectEqual(
  run("git", ["-C", metadata.ios.path, "rev-parse", "HEAD"]),
  metadata.ios.commit,
  "iOS commit",
);
expectEqual(
  run("git", ["-C", metadata.ios.path, "status", "--porcelain"]),
  "",
  "iOS worktree status",
);

expectEqual(
  plistValue("CFBundleShortVersionString"),
  metadata.chatgpt_desktop.short_version,
  "ChatGPT short version",
);
expectEqual(
  plistValue("CFBundleVersion"),
  metadata.chatgpt_desktop.bundle_version,
  "ChatGPT bundle version",
);
const asarPath = "/Applications/ChatGPT.app/Contents/Resources/app.asar";
const codexBinaryPath = "/Applications/ChatGPT.app/Contents/Resources/codex";
expectEqual(
  sha256(readFileSync(asarPath)),
  metadata.chatgpt_desktop.app_asar_sha256,
  "app.asar SHA-256",
);
expectEqual(
  run(codexBinaryPath, ["--version"]),
  metadata.chatgpt_desktop.embedded_codex_version,
  "embedded Codex version",
);
expectEqual(
  sha256(readFileSync(codexBinaryPath)),
  metadata.chatgpt_desktop.embedded_codex_sha256,
  "embedded Codex SHA-256",
);

const upstream = metadata.upstream_codex;
expectEqual(
  run("git", ["-C", upstream.path, "branch", "--show-current"]),
  upstream.branch,
  "upstream branch",
);
expectEqual(run("git", ["-C", upstream.path, "rev-parse", "HEAD"]), upstream.head, "upstream HEAD");
expectEqual(
  run("git", ["-C", upstream.path, "rev-parse", upstream.target_tag]),
  upstream.target_tag_object,
  "target tag object",
);
expectEqual(
  run("git", ["-C", upstream.path, "rev-parse", `${upstream.target_tag}^{}`]),
  upstream.target_peeled_commit,
  "target peeled commit",
);
expectEqual(
  Number(run("git", ["-C", upstream.path, "rev-list", "--count", `${upstream.target_tag}^{}..HEAD`])),
  upstream.head_ahead_count,
  "target-to-HEAD distance",
);
const upstreamScope = [
  "codex-rs/app-server-transport/src/transport/remote_control",
  "codex-rs/app-server/src/request_processors/remote_control_processor.rs",
  "codex-rs/app-server/src/lib.rs",
  "codex-rs/cli/src/remote_control_cmd.rs",
  "codex-rs/app-server-daemon",
];
expectEqual(
  run("git", [
    "-C",
    upstream.path,
    "diff",
    "--name-only",
    `${upstream.target_tag}^{}..HEAD`,
    "--",
    ...upstreamScope,
  ]),
  "",
  "target-to-HEAD remote scope diff",
);

const extractedBundles = new Map();
for (const bundle of metadata.asar_callsite_bundles) {
  const contents = extractFile(asarPath, bundle.path);
  expectEqual(sha256(contents), bundle.sha256, `${bundle.path} SHA-256`);
  extractedBundles.set(bundle.path, contents.toString("utf8"));
}

const mainBundle = extractedBundles.get(".vite/build/main-BvHpyFqC.js");
const transportBundle = extractedBundles.get(".vite/build/src-4lLVrYxe.js");
const webviewBundle = extractedBundles.get("webview/assets/app-initial-DJrCTPoN.js");
if (mainBundle == null || transportBundle == null || webviewBundle == null) {
  fail("one or more indexed ASAR bundles were not extracted");
}
for (const controllerPath of metadata.static_controller_findings.rest_paths.filter((value) =>
  value.startsWith("/codex/"),
)) {
  expectEndpointCallsite(mainBundle, controllerPath, "main controller call sites");
}
expectIncludes(mainBundle, metadata.static_controller_findings.websocket_path, "controller WSS path");
for (const productPath of metadata.static_controller_findings.rest_paths.filter((value) =>
  value.startsWith("/wham/"),
)) {
  expectEndpointCallsite(webviewBundle, productPath, "webview product-control call sites");
}
for (const header of metadata.static_controller_findings.websocket_headers) {
  expectIncludes(`${mainBundle}\n${transportBundle}`, header, "controller WSS headers");
}
expectIncludes(
  transportBundle,
  `oZ=${metadata.static_controller_findings.protocol_version}`,
  "controller protocol version",
);
expectIncludes(transportBundle, "env_id:H()", "controller env_id schema");
expectIncludes(transportBundle, "connectStream({envId:", "controller environment stream binding");
expectIncludes(
  transportBundle,
  `sZ=${metadata.static_controller_findings.controller_segment_target_bytes / 1024}*1024`,
  "controller segment target",
);
expectIncludes(
  transportBundle,
  `cZ=${metadata.static_controller_findings.controller_segment_max_bytes / 1024}*1024`,
  "controller segment maximum",
);
expectIncludes(transportBundle, "lZ=1024*1024*1024", "controller reassembly maximum");

const hostProtocol = readFileSync(
  path.join(upstream.path, "codex-rs/app-server-transport/src/transport/remote_control/protocol.rs"),
  "utf8",
);
const hostWebsocket = readFileSync(
  path.join(upstream.path, "codex-rs/app-server-transport/src/transport/remote_control/websocket.rs"),
  "utf8",
);
const hostSegment = readFileSync(
  path.join(upstream.path, "codex-rs/app-server-transport/src/transport/remote_control/segment.rs"),
  "utf8",
);
const hostTracker = readFileSync(
  path.join(upstream.path, "codex-rs/app-server-transport/src/transport/remote_control/client_tracker.rs"),
  "utf8",
);
expectIncludes(hostProtocol, "pub(crate) struct ClientEnvelope", "host ClientEnvelope");
expectIncludes(hostProtocol, "pub(crate) struct ServerEnvelope", "host ServerEnvelope");
expectIncludes(hostWebsocket, '"x-codex-subscribe-cursor"', "host subscribe cursor");
expectIncludes(hostSegment, "100 * 1024 * 1024", "host reassembly maximum");
expectIncludes(hostSegment, "REMOTE_CONTROL_SEGMENT_COUNT_MAX: usize = 1024", "host segment count");
expectIncludes(hostSegment, "REMOTE_CONTROL_SEGMENT_ASSEMBLY_MAX_COUNT: usize = 128", "host assembly count");
expectIncludes(hostTracker, "ConnectionOrigin::RemoteControl", "host app-server connection origin");

console.log(
  JSON.stringify(
    {
      result: "PASS",
      classification: metadata.classification,
      gateEffect: metadata.gate_effect,
      chatgptVersion: metadata.chatgpt_desktop.short_version,
      embeddedCodexVersion: metadata.chatgpt_desktop.embedded_codex_version,
      controllerProtocolVersion: metadata.static_controller_findings.protocol_version,
      checkedBundles: metadata.asar_callsite_bundles.map((bundle) => bundle.path),
      note: "Static/source baseline verified; real relay/controller Gate P0 remains unproven.",
    },
    null,
    2,
  ),
);
