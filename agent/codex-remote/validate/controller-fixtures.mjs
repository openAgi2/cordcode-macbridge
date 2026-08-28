#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { extractFile } from "file:///opt/homebrew/lib/node_modules/@electron/asar/lib/asar.js";

const scriptPath = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(scriptPath), "../../..");
const phase0Root = path.join(repoRoot, "agent/codex-remote/testdata/phase0");
const staticPath = path.join(
  phase0Root,
  "static-26.825.32147-alpha.12.2/controller-call-sites.json",
);
const liveContractPath = path.join(phase0Root, "live-fixture-contract.json");
const liveRoot = path.join(phase0Root, "live");
const asarPath = "/Applications/ChatGPT.app/Contents/Resources/app.asar";

function fail(message) {
  throw new Error(message);
}

function expect(condition, message) {
  if (!condition) fail(message);
}

function includes(contents, value, label) {
  expect(contents.includes(value), `${label}: missing ${JSON.stringify(value)}`);
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function endpointSegments(endpoint) {
  return endpoint.split(/\{[^}]+\}/u).filter((segment) => segment.length > 0);
}

function walk(directory) {
  const files = [];
  for (const entry of readdirSync(directory)) {
    const candidate = path.join(directory, entry);
    if (statSync(candidate).isDirectory()) files.push(...walk(candidate));
    else files.push(candidate);
  }
  return files;
}

const fixture = JSON.parse(readFileSync(staticPath, "utf8"));
const liveContract = JSON.parse(readFileSync(liveContractPath, "utf8"));
expect(fixture.classification === "STATIC-CALLSITE-ONLY", "static classification drifted");
expect(fixture.gate_effect === "does-not-pass-phase0", "static fixture must not pass Gate P0");
expect(liveContract.classification === "LIVE-REDACTED-OBSERVATION", "live contract drifted");
expect(
  sha256(readFileSync(asarPath)) === fixture.target.app_asar_sha256,
  "installed app.asar does not match the frozen target",
);

const bundles = new Map();
for (const source of fixture.sources) {
  const contents = extractFile(asarPath, source.path);
  expect(sha256(contents) === source.sha256, `${source.path} hash drifted`);
  bundles.set(source.path, contents.toString("utf8"));
}
const main = bundles.get(".vite/build/main-BvHpyFqC.js");
const transport = bundles.get(".vite/build/src-4lLVrYxe.js");
const webview = bundles.get("webview/assets/app-initial-DJrCTPoN.js");
expect(main != null && transport != null && webview != null, "required ASAR bundle missing");

for (const endpoint of fixture.api_families.codex_controller.paths) {
  for (const segment of endpointSegments(endpoint.path)) includes(main, segment, endpoint.path);
  for (const field of endpoint.request_fields ?? []) includes(main, field, endpoint.path);
}
for (const endpoint of fixture.api_families.wham_product_control.paths) {
  for (const segment of endpointSegments(endpoint.path)) includes(webview, segment, endpoint.path);
  for (const field of endpoint.request_fields ?? []) includes(webview, field, endpoint.path);
}
includes(transport, `oZ=${fixture.websocket_contract.protocol_version}`, "protocol version");
for (const header of fixture.websocket_contract.headers) includes(transport, header, "WSS header");
for (const type of fixture.websocket_contract.server_envelope_types) includes(transport, `\`${type}\``, "server envelope type");
for (const type of fixture.websocket_contract.client_envelope_types) includes(transport, `\`${type}\``, "client envelope type");
for (const field of fixture.websocket_contract.routing_fields) includes(transport, field, "routing field");
includes(transport, "connectStream({envId:", "environment stream binding");
includes(transport, "findStream(e.env_id,e.stream_id)", "inbound environment routing");
includes(transport, "x-codex-subscribe-cursor", "reconnect cursor header");
includes(transport, "e.cursor!=null&&(this.cursor=e.cursor)", "accepted cursor advancement");
includes(main, "allow_os_protected_nonextractable", "device key policy");
includes(main, fixture.enrollment_contract.device_key_addon.signed_payload_domain, "device key payload domain");
includes(main, fixture.enrollment_contract.step_up_oauth.client_id, "step-up OAuth client id");
includes(main, fixture.enrollment_contract.step_up_oauth.authorization_scope, "step-up OAuth scope");
expect(
  sha256(readFileSync(fixture.enrollment_contract.device_key_addon.path)) ===
    fixture.enrollment_contract.device_key_addon.sha256,
  "device-key addon hash drifted",
);
includes(main, "codex.remote_control.enroll", "step-up scope");
includes(main, "remote_control_controller_websocket", "controller scope");

const forbiddenNamePatterns = liveContract.forbidden_field_name_patterns.map(
  (value) => new RegExp(`(?:^|[_.-])${value.replaceAll("_", "[_.-]")}(?:$|[_.-])`, "iu"),
);
const forbiddenValueMarkers = liveContract.forbidden_value_markers;
const liveFiles = walk(liveRoot).filter((file) => path.basename(file) !== "README.md");
for (const file of liveFiles) {
  const contents = readFileSync(file, "utf8");
  for (const marker of forbiddenValueMarkers) {
    expect(!contents.includes(marker), `${path.relative(repoRoot, file)} contains forbidden marker`);
  }
  if (path.extname(file) === ".json") {
    const parsed = JSON.parse(contents);
    const pending = [parsed];
    while (pending.length > 0) {
      const current = pending.pop();
      if (Array.isArray(current)) pending.push(...current);
      else if (current != null && typeof current === "object") {
        for (const [key, value] of Object.entries(current)) {
          expect(
            !forbiddenNamePatterns.some((pattern) => pattern.test(key)),
            `${path.relative(repoRoot, file)} contains forbidden field ${key}`,
          );
          pending.push(value);
        }
      }
    }
  }
}

const mode = process.argv.includes("--require-live") ? "require-live" : "static-preflight";
if (mode === "require-live") {
  expect(liveFiles.length > 0, "real redacted live fixture is missing");
  const liveFixtures = liveFiles.filter((file) => path.extname(file) === ".json").map((file) => ({
    file,
    value: JSON.parse(readFileSync(file, "utf8")),
  }));
  expect(liveFixtures.length > 0, "real redacted JSON fixture is missing");
  const observedKinds = new Set();
  for (const { file, value } of liveFixtures) {
    expect(value.classification.startsWith("LIVE-REDACTED-OBSERVATION"), `${path.relative(repoRoot, file)} classification invalid`);
    for (const field of liveContract.required_metadata_fields) {
      expect(value.metadata?.[field] != null, `${path.relative(repoRoot, file)} missing metadata.${field}`);
    }
    for (const observation of value.observations ?? []) observedKinds.add(observation.kind);
  }
  const missingKinds = liveContract.required_observation_kinds.filter((kind) => !observedKinds.has(kind));
  expect(missingKinds.length === 0, `live fixture is incomplete; missing observation kinds: ${missingKinds.join(", ")}`);
}

console.log(
  JSON.stringify(
    {
      result: "PASS",
      mode,
      classification: fixture.classification,
      checkedTarget: fixture.target,
      liveFixtureCount: liveFiles.length,
      gateEffect: "does-not-pass-phase0",
      note: "Static call sites and live capture contract are ready; real controller observations remain missing.",
    },
    null,
    2,
  ),
);
