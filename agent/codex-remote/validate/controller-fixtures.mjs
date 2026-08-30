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
// Test hook: point the live scan at a scratch directory instead of the
// evidence tree (CR_PHASE0_LIVE_ROOT must contain the fixture files directly).
const liveRoot = process.env.CR_PHASE0_LIVE_ROOT ?? path.join(phase0Root, "live");
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

const mode = process.argv.includes("--require-live") ? "require-live" : "static-preflight";
const historyOnly = process.argv.includes("--history-only");
// --history-only skips the frozen-ASAR static pin (which froze the
// 26.825.32147 target and fails while the installed Desktop is newer) and the
// controller-path bundle checks; the live redaction-policy scan and the live
// fixture checks still run. Target-version drift is recorded by
// probe/history_probe.mjs in fixture.target.drift_assessment and is an owner
// adjudication, not something this validator silently forgives.
if (!historyOnly) {
  const fixture = JSON.parse(readFileSync(staticPath, "utf8"));
  expect(fixture.classification === "STATIC-CALLSITE-ONLY", "static classification drifted");
  expect(fixture.gate_effect === "does-not-pass-phase0", "static fixture must not pass Gate P0");
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
}

const liveContract = JSON.parse(readFileSync(liveContractPath, "utf8"));
expect(liveContract.classification === "LIVE-REDACTED-OBSERVATION", "live contract drifted");

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

if (mode === "require-live") {
  expect(liveFiles.length > 0, "real redacted live fixture is missing");
  const liveFixtures = liveFiles.filter((file) => path.extname(file) === ".json").map((file) => ({
    file,
    value: JSON.parse(readFileSync(file, "utf8")),
  }));
  expect(liveFixtures.length > 0, "real redacted JSON fixture is missing");
  const historyContract = liveContract.history_probe;
  const observedKinds = new Set();
  const counts = { controller: 0, history: 0 };
  for (const { file, value } of liveFixtures) {
    expect(value.classification.startsWith("LIVE-REDACTED-OBSERVATION"), `${path.relative(repoRoot, file)} classification invalid`);
    for (const field of liveContract.required_metadata_fields) {
      expect(value.metadata?.[field] != null, `${path.relative(repoRoot, file)} missing metadata.${field}`);
    }
    if (value.schema_version === 2 && value.gate_effect === "g0-evidence-input") {
      expect(historyContract != null, "live contract missing history_probe section");
      counts.history += 1;
      const rel = path.relative(repoRoot, file);
      for (const field of historyContract.required_top_level_fields) {
        expect(value[field] != null, `${rel} history fixture missing top-level ${field}`);
      }
      for (const section of historyContract.required_data_sections) {
        expect(value.data?.[section] != null, `${rel} history fixture missing data.${section}`);
      }
      const longest = value.data?.longestThread ?? {};
      for (const section of historyContract.required_longest_thread_sections) {
        expect(longest[section] != null, `${rel} history fixture missing data.longestThread.${section}`);
      }
      expect(value.probe?.caps != null, `${rel} history fixture missing probe.caps`);
      expect(value.target?.detected?.chatgpt_desktop_version != null, `${rel} history fixture missing target.detected versions`);
      expect(value.adjudication?.result != null, `${rel} history fixture missing adjudication.result`);
      continue;
    }
    counts.controller += 1;
    for (const observation of value.observations ?? []) observedKinds.add(observation.kind);
  }
  // Controller-path observation-kind union stays a FULL-mode gate only: it is
  // intentionally red on the current evidence set (cursor reconnect remains
  // FAIL-BLOCKED from the parent plan). History fixtures get their own
  // structural validation above; --history-only never weakens the full gate.
  if (historyOnly) {
    expect(counts.history > 0, "history-only require-live found no history fixtures (schema_version 2, gate_effect g0-evidence-input)");
  } else {
    const missingKinds = liveContract.required_observation_kinds.filter((kind) => !observedKinds.has(kind));
    expect(missingKinds.length === 0, `live fixture is incomplete; missing observation kinds: ${missingKinds.join(", ")}`);
  }
  var requireLiveSummary = { controllerFixtures: counts.controller, historyFixtures: counts.history };
}

console.log(
  JSON.stringify(
    {
      result: "PASS",
      mode: historyOnly ? `${mode}+history-only` : mode,
      classification: historyOnly ? "LIVE-REDACTED-OBSERVATION" : fixture.classification,
      checkedTarget: historyOnly ? "skipped-frozen-asar-pin (target drift is owner-adjudicated; see fixture target.drift_assessment)" : fixture.target,
      liveFixtureCount: liveFiles.length,
      requireLive: typeof requireLiveSummary === "object" ? requireLiveSummary : undefined,
      gateEffect: historyOnly ? "live-redaction-policy-only" : "does-not-pass-phase0",
      note: historyOnly
        ? "History-fixture validation path: redaction policy + history fixture structure; §3.0.7/§3.0.5 assertions live in validate/history-fixture-assert.mjs; frozen-ASAR static pin intentionally skipped (drift recorded by the probe)."
        : "Static call sites and live capture contract are ready; real controller observations remain missing.",
    },
    null,
    2,
  ),
);
