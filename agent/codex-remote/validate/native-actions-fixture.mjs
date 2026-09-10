#!/usr/bin/env node

// Offline contract checks for the owner-authorized, redacted Remote Control
// observation emitted by probe/native_actions_probe.mjs.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

function fail(message) {
  throw new Error(message);
}

function expect(condition, message) {
  if (!condition) fail(message);
}

function expectSuccess(record, method) {
  expect(record?.method === method, `expected ${method}`);
  expect(record.error === null, `${method} recorded an RPC error`);
  expect(Array.isArray(record.requestKeys), `${method} request keys missing`);
  expect(Array.isArray(record.resultKeys), `${method} result keys missing`);
}

function expectHasKeys(actual, expected, label) {
  expect(expected.every((key) => actual.includes(key)), `${label} keys incomplete`);
}

function validateEnvelope(fixture, source) {
  expect(fixture.schemaVersion === 1, "fixture schema version drifted");
  expect(fixture.classification === "LIVE-REDACTED-OBSERVATION", "fixture is not live evidence");
  expect(fixture.source === "official Remote Control controller protocol v3", "fixture source is not Remote Control v3");
  expect(fixture.authorization === "owner-authorized-2026-09-10", "owner authorization marker missing");
  expect(fixture.target?.desktopVersion === "26.903.61454", "Desktop target drifted");
  expect(fixture.target?.bundleVersion === "8378", "Desktop bundle target drifted");
  expect(fixture.target?.embeddedCodexVersion === "codex-cli 0.153.4", "embedded Codex target drifted");
  expect(fixture.target?.expectedSourceTag === "rust-v0.153.4", "source tag does not match runtime target");
  expect(!/(access[_-]?token|refresh[_-]?token|authorization|cookie|device[_-]?key)\s*[=:]\s*["'][A-Za-z0-9+/_=-]{16,}/i.test(source),
    "fixture appears to contain a credential");
  for (const match of source.matchAll(/"(?:thread|turn|item|id)"\s*:\s*"([^"]+)"/g)) {
    expect(/^id-[0-9a-f]{12}$/.test(match[1]), `unredacted identity found: ${match[1]}`);
  }
  expect(fixture.cleanup?.threadDelete?.emptyAck === true, "disposable thread deletion not proven");
  expect(fixture.cleanup?.probeControllerRevoked === true, "probe controller cleanup not proven");
}

function validateC0C1(fixture) {
  const section = fixture.c0c1;
  expect(section?.environment?.online === true, "Remote environment was not online");
  expect(section.environment.clientType === "CODEX_DESKTOP_APP", "Remote target was not Desktop");
  expectHasKeys(section.environment.environmentFields, ["app_server_version", "client_type", "client_version", "env_id", "online"],
    "Remote environment");
  const attach = section.readOnlyAttach;
  expectSuccess(attach?.readBefore, "thread/read");
  expectSuccess(attach?.resume, "thread/resume");
  expectSuccess(attach?.readAfter, "thread/read");
  expect(attach.initialStatus === "notLoaded", "bounded inventory did not select a notLoaded thread");
  expect(attach.identityStable && attach.cwdPresent && attach.modelStable && attach.effortStable,
    "read/resume/read authority was not stable");
  expect(attach.turnsExcluded === true, "excludeTurns resume returned turns");
  expectSuccess(section.coldReopen?.response, "thread/resume");
  expect(section.coldReopen.sameThread === true, "cold controller session changed thread identity");
}

function validateCompact(fixture) {
  const compact = fixture.actions?.compact;
  expectSuccess(compact?.response, "thread/compact/start");
  expect(compact.emptyAck === true, "compact ACK was not the official empty object");
  expect(compact.lifecycle?.terminalStatus === "completed", "compact turn did not complete");
  expect(compact.lifecycle?.userNotificationObserved === false, "compact lifecycle synthesized a user item");
  expect(/^id-[0-9a-f]{12}$/.test(compact.lifecycle?.turn), "compact turn identity missing");
  expect(/^id-[0-9a-f]{12}$/.test(compact.lifecycle?.item), "compact item identity missing");
  expectSuccess(compact.cold?.response, "thread/turns/list");
  expect(compact.cold.sameTurn === true && compact.cold.status === "completed", "cold history lost compact terminal turn");
  expect(Array.isArray(compact.cold.itemTypes) && compact.cold.itemTypes.length === 0,
    "target cold turn summary unexpectedly exposed an item payload");
  const coldItems = fixture.supplementalColdCapture?.compactColdItems;
  expectSuccess(coldItems?.response, "thread/items/list");
  expect(coldItems.entryCount === 1 && coldItems.sameTurnAndItem === true,
    "authoritative cold item path lost compaction turn/item identity");
  expect(coldItems.itemType === "contextCompaction", "cold compaction item type drifted");
  expect((fixture.observationSummary?.notificationMethods?.["item/started"] ?? 0) >= 1,
    "compact item/started notification was not observed");
  expect((fixture.observationSummary?.notificationMethods?.["item/completed"] ?? 0) >= 1,
    "compact item/completed notification was not observed");
}

function validatePlan(fixture) {
  const catalog = fixture.actions?.collaborationCatalog;
  expectSuccess(catalog?.response, "collaborationMode/list");
  expect(catalog.count === 2, "unexpected collaboration preset count");
  expect(catalog.entries?.some((entry) => entry.name === "Plan" && entry.mode === "plan"), "Plan preset absent");
  expect(catalog.entries?.some((entry) => entry.name === "Default" && entry.mode === "default"), "Default preset absent");
  for (const name of ["planUpdate", "defaultUpdate"]) {
    const update = fixture.actions?.[name];
    expectSuccess(update?.response, "thread/settings/update");
    expect(update.emptyAck === true, `${name} ACK was not empty`);
    expect(update.sent?.developerInstructionsIsNull === true, `${name} did not explicitly select built-in instructions`);
    expect(typeof update.sent?.model === "string" && update.sent.model.length > 0, `${name} did not preserve a model`);
  }
  expect(fixture.actions.planUpdate.sent.reasoningEffort === "medium", "Plan preset mask was not applied");
  expect(fixture.actions.defaultUpdate.sent.reasoningEffort === null, "Default preset mask was not applied");
  const notifications = fixture.supplementalColdCapture?.planNotifications;
  expect(notifications?.plan?.mode === "plan" && notifications?.default?.mode === "default",
    "authoritative Plan/Default notification values were not captured");
  expect(notifications.plan.model === fixture.actions.planUpdate.sent.model
    && notifications.default.model === fixture.actions.defaultUpdate.sent.model,
  "Plan/Default notifications did not preserve the thread model");
  expect(notifications.plan.observedAtMs >= notifications.plan.updateResponseObservedAtMs
    && notifications.default.observedAtMs >= notifications.default.updateResponseObservedAtMs,
  "captured Plan/Default response-notification ordering is inconsistent");
  expect(notifications.plan.developerInstructionsIsNull === false
    && notifications.default.developerInstructionsIsNull === false,
  "server did not resolve explicit null to built-in collaboration instructions");
  expect((fixture.observationSummary?.notificationMethods?.["thread/settings/updated"] ?? 0) >= 2,
    "Plan/Default authoritative notifications were not both observed");
}

function validateGoal(fixture) {
  expectSuccess(fixture.actions?.goalGetEmpty?.response, "thread/goal/get");
  expect(fixture.actions.goalGetEmpty.isNull === true, "initial goal was not empty");
  expectSuccess(fixture.actions?.goalSet?.response, "thread/goal/set");
  expectHasKeys(fixture.actions.goalSet.recordFields,
    ["createdAt", "objective", "status", "threadId", "timeUsedSeconds", "tokenBudget", "tokensUsed", "updatedAt"],
    "goal record");
  expect(fixture.actions.goalSet.status === "paused", "goal status did not round-trip");
  expect(fixture.actions.goalSet.tokenBudget === 256, "goal token budget did not round-trip");
  expect(fixture.actions.goalSet.threadMatches === true, "goal response changed thread identity");
  expectSuccess(fixture.actions?.goalClear?.response, "thread/goal/clear");
  expect(fixture.actions.goalClear.cleared === true, "goal clear was not acknowledged");
  expectSuccess(fixture.actions?.goalGetCleared?.response, "thread/goal/get");
  expect(fixture.actions.goalGetCleared.isNull === true, "goal remained after clear/readback");
  expectSuccess(fixture.supplementalColdCapture?.goalColdReadback?.response, "thread/goal/get");
  expect(fixture.supplementalColdCapture.goalColdReadback.isNull === true,
    "goal clear did not survive controller reconnect/resume");
  expect((fixture.observationSummary?.notificationMethods?.["thread/goal/updated"] ?? 0) >= 1,
    "goal updated notification was not observed");
  expect((fixture.observationSummary?.notificationMethods?.["thread/goal/cleared"] ?? 0) >= 1,
    "goal cleared notification was not observed");
}

const file = resolve(process.argv[2] ?? "agent/codex-remote/testdata/slash-panel/native-actions-live-2026-09-10.json");
const section = process.argv[3] ?? "all";
const source = readFileSync(file, "utf8");
const fixture = JSON.parse(source);
validateEnvelope(fixture, source);
const validators = { c0c1: validateC0C1, compact: validateCompact, plan: validatePlan, goal: validateGoal };
if (section === "all") {
  for (const validate of Object.values(validators)) validate(fixture);
} else {
  expect(Object.hasOwn(validators, section), `unknown section: ${section}`);
  validators[section](fixture);
}
process.stdout.write(`native-actions-fixture PASS section=${section}\n`);
