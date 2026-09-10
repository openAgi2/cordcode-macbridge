#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

function expect(value, message) {
  if (!value) throw new Error(message);
}

function expectRecord(record, method) {
  expect(record?.method === method, `expected ${method}`);
  expect(Array.isArray(record.requestKeys) && Array.isArray(record.resultKeys), `${method} shape missing`);
}

function expectSettings(summary, mode, label) {
  const requiredKeys = [
    "activePermissionProfile", "approvalPolicy", "approvalsReviewer", "collaborationMode",
    "cwd", "effort", "model", "modelProvider", "multiAgentMode", "personality",
    "sandboxPolicy", "serviceTier", "summary",
  ];
  expect(JSON.stringify(summary?.threadSettingsKeys) === JSON.stringify(requiredKeys), `${label} did not return complete ThreadSettings`);
  expect(summary.mode === mode, `${label} mode was not ${mode}`);
  expect(typeof summary.model === "string" && summary.model.length > 0, `${label} model missing`);
  expect(summary.reasoningEffort === null || typeof summary.reasoningEffort === "string", `${label} effort malformed`);
}

const file = resolve(process.argv[2]
  ?? "agent/codex-remote/testdata/slash-panel/plan-cold-live-2026-09-10.json");
const source = readFileSync(file, "utf8");
const fixture = JSON.parse(source);
expect(fixture.schemaVersion === 2, "schema drifted");
expect(fixture.classification === "LIVE-REDACTED-OBSERVATION", "not a live fixture");
expect(fixture.target?.runtime === "isolated-patched-foreground", "probe did not use the isolated patched runtime");
expect(fixture.target?.cliVersion === "codex-cli 0.0.0", "development CLI identity drifted");
expect(/^[0-9a-f]{40}$/.test(fixture.target?.sourceCommit), "source commit missing");
expect(/^[0-9a-f]{64}$/.test(fixture.target?.binarySha256), "binary hash missing");
expect(fixture.target?.signedDesktopBundleModified === false, "signed Desktop bundle was modified");
expect(fixture.environment?.online === true && typeof fixture.environment?.clientType === "string",
  "probe did not target its online isolated runtime");

const livePlan = fixture.observations?.livePlanUpdate;
expectRecord(livePlan?.response, "thread/settings/update");
expect(livePlan.response.resultKeys.length === 0, "Plan update ACK was not empty");
expectSettings(livePlan.notification, "plan", "live Plan notification");
expect(livePlan.notification.developerInstructionsKind === "string",
  "server did not resolve built-in Plan instructions");

const coldPlan = fixture.observations?.coldPlanRead;
expect(coldPlan?.restartEnvironmentStable === true, "Plan cold restart changed environment identity");
expectRecord(coldPlan?.resume, "thread/resume");
expectRecord(coldPlan?.response, "thread/settings/get");
expect(JSON.stringify(coldPlan.response.resultKeys) === JSON.stringify(["threadSettings"]),
  "Plan cold read response shape drifted");
expectSettings(coldPlan.settings, "plan", "cold Plan read");
expect(coldPlan.settings.model === livePlan.notification.model
  && coldPlan.settings.reasoningEffort === livePlan.notification.reasoningEffort,
"cold Plan read did not converge with the live notification");

const liveDefault = fixture.observations?.liveDefaultUpdate;
expectRecord(liveDefault?.response, "thread/settings/update");
expect(liveDefault.response.resultKeys.length === 0, "Default update ACK was not empty");
expectSettings(liveDefault.notification, "default", "live Default notification");

const coldDefault = fixture.observations?.coldDefaultRead;
expect(coldDefault?.restartEnvironmentStable === true, "Default cold restart changed environment identity");
expectRecord(coldDefault?.resume, "thread/resume");
expectRecord(coldDefault?.response, "thread/settings/get");
expect(JSON.stringify(coldDefault.response.resultKeys) === JSON.stringify(["threadSettings"]),
  "Default cold read response shape drifted");
expectSettings(coldDefault.settings, "default", "cold Default read");
expect(coldDefault.settings.model === liveDefault.notification.model
  && coldDefault.settings.reasoningEffort === liveDefault.notification.reasoningEffort,
"cold Default read did not converge with the live notification");

expect(fixture.cleanup?.threadDeleteEmptyAck === true, "disposable thread was not deleted");
expect(fixture.cleanup?.probeControllerRevoked === true, "probe controller was not revoked");
expect(fixture.cleanup?.isolatedHomeDeleted === true, "isolated CODEX_HOME was not deleted");
for (const match of source.matchAll(/"(?:thread|turn|item|environment)"\s*:\s*"([^"]+)"/g)) {
  expect(/^id-[0-9a-f]{12}$/.test(match[1]), `unredacted identity: ${match[1]}`);
}
expect(!/(access[_-]?token|refresh[_-]?token|cookie|device[_-]?key|pairing[_-]?code)\s*[=:]\s*["'][A-Za-z0-9+/_=-]{8,}/i.test(source),
  "fixture appears to contain a credential");
process.stdout.write("plan-cold-fixture PASS authoritativeColdSource=thread/settings/get\n");
