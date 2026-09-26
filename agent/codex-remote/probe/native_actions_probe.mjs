#!/usr/bin/env node

// Targeted live evidence probe for the Codex Remote native Compact / Plan / Goal
// action surface. Credentials and raw ids stay in memory; stdout is a compact,
// content-free fixture suitable for source control.

import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import {
  cleanupController,
  collectManualPairingCode,
  enrollController,
  openRpcSession,
  pairedEnvironments,
  selectEnvironment,
} from "./lib/controller_session.mjs";
import { resolveCodexPath } from "./lib/codex_path.mjs";

const helperSource = new URL("./device_key_helper.swift", import.meta.url).pathname;
const startedAt = Date.now();
const observations = [];
const rawEvents = [];
let probeThreadId = null;

function ref(value) {
  if (typeof value !== "string" || value.length === 0) return null;
  return `id-${createHash("sha256").update(value).digest("hex").slice(0, 12)}`;
}

function observe(kind, detail = {}) {
  observations.push({ tMs: Date.now() - startedAt, kind, ...detail });
  process.stderr.write(`${JSON.stringify({ event: kind, ...detail })}\n`);
}

function installedTarget() {
  const plist = "/Applications/ChatGPT.app/Contents/Info.plist";
  const read = (args) => spawnSync("/usr/bin/defaults", args, { encoding: "utf8" }).stdout.trim();
  const codex = spawnSync(resolveCodexPath(), ["--version"], { encoding: "utf8" }).stdout.trim();
  return {
    desktopVersion: read(["read", plist, "CFBundleShortVersionString"]),
    bundleVersion: read(["read", plist, "CFBundleVersion"]),
    embeddedCodexVersion: codex,
    expectedSourceTag: "rust-v0.153.4",
  };
}

function rpcError(response) {
  if (response.error == null) return null;
  const code = response.error.code;
  return { code: typeof code === "string" || typeof code === "number" ? code : null,
    messageLength: typeof response.error.message === "string" ? response.error.message.length : null };
}

async function call(session, method, params, timeoutMs = 30_000) {
  const requestKeys = Object.keys(params ?? {}).sort();
  const response = await session.rpc(method, params, { timeoutMs });
  const record = {
    method,
    requestKeys,
    durationMs: response.ms,
    observedAtMs: Date.now() - startedAt,
    error: rpcError(response),
    resultKeys: response.error == null && response.result != null && typeof response.result === "object"
      ? Object.keys(response.result).sort() : [],
  };
  observe("rpc", { method, error: record.error != null, durationMs: record.durationMs });
  return { response, record };
}

function paramsOf(rpc) { return rpc?.params ?? {}; }

function onLiveMessage(method, rpc) {
  const params = paramsOf(rpc);
  const threadId = params.threadId ?? params.thread?.id ?? params.turn?.threadId ?? params.goal?.threadId;
  if (probeThreadId != null && threadId !== probeThreadId) return;
  const item = params.item;
  rawEvents.push({ method, params, at: Date.now() });
  observe("notification", {
    method,
    thread: ref(threadId),
    turn: ref(params.turnId ?? params.turn?.id),
    item: ref(item?.id),
    itemType: typeof item?.type === "string" ? item.type : null,
    turnStatus: typeof params.turn?.status === "string" ? params.turn.status : null,
  });
}

async function waitFor(predicate, label, timeoutMs = 180_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const found = rawEvents.find(predicate);
    if (found != null) return found;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`notification timeout: ${label}`);
}

function requireSuccess(entry, label) {
  if (entry.response.error != null) throw new Error(`${label} rpc error code=${entry.response.error.code}`);
  return entry.response.result;
}

function presetMask(preset, current) {
  if (preset == null || typeof preset.name !== "string" || preset.name.length === 0) {
    throw new Error("collaboration preset missing stable name");
  }
  const mode = preset.mode ?? current.mode;
  const model = preset.model ?? current.model;
  const effort = Object.hasOwn(preset, "reasoning_effort") ? preset.reasoning_effort : current.reasoningEffort;
  if (!["plan", "default"].includes(mode) || typeof model !== "string" || model.length === 0) {
    throw new Error("collaboration preset mask incomplete");
  }
  return { mode, settings: { model, reasoning_effort: effort ?? null, developer_instructions: null } };
}

function collaborationSummary(event) {
  const settings = paramsOf(event).threadSettings;
  const collaboration = settings?.collaborationMode;
  return {
    threadSettingsKeys: settings != null && typeof settings === "object" ? Object.keys(settings).sort() : [],
    collaborationKeys: collaboration != null && typeof collaboration === "object" ? Object.keys(collaboration).sort() : [],
    mode: collaboration?.mode ?? null,
    model: collaboration?.settings?.model ?? null,
    reasoningEffort: collaboration?.settings?.reasoning_effort ?? null,
    developerInstructionsIsNull: collaboration?.settings?.developer_instructions === null,
    observedAtMs: event.at - startedAt,
  };
}

async function main() {
  const fixture = {
    schemaVersion: 1,
    classification: "LIVE-REDACTED-OBSERVATION",
    target: installedTarget(),
    authorization: "owner-authorized-2026-09-10",
    source: "official Remote Control controller protocol v3",
    c0c1: {},
    actions: {},
    cleanup: {},
  };
  const enrollment = await enrollController({ helperSource, observe });
  let session = null;
  try {
    const environments = await pairedEnvironments(
      enrollment.token, enrollment.accountID, enrollment.clientID,
      () => collectManualPairingCode(
        "Codex native actions probe pairing",
        "<p>Use a fresh Desktop Remote Control pairing code. The probe never prints or stores it.</p>",
      ),
    );
    const selected = selectEnvironment(environments.body.items);
    fixture.c0c1.environment = {
      selectedIndex: selected.index + 1,
      online: selected.environment.online === true,
      clientType: selected.environment.client_type,
      environmentFields: Object.keys(selected.environment).sort(),
    };
    session = await openRpcSession({ enrollment, environment: selected.environment, observe, onLiveMessage });

    const listed = await call(session, "thread/list", { limit: 10, sortKey: "recency_at", sortDirection: "desc" }, 60_000);
    const listResult = requireSuccess(listed, "thread/list");
    const candidate = (listResult.data ?? []).find((thread) => {
      const status = typeof thread.status === "string" ? thread.status : thread.status?.type;
      return status === "notLoaded" && typeof thread.id === "string";
    });
    if (candidate == null) throw new Error("no notLoaded thread in bounded read-only inventory");
    const candidateId = candidate.id;
    const readBefore = await call(session, "thread/read", { threadId: candidateId }, 60_000);
    const resume = await call(session, "thread/resume", { threadId: candidateId, excludeTurns: true }, 60_000);
    const readAfter = await call(session, "thread/read", { threadId: candidateId }, 60_000);
    const beforeThread = requireSuccess(readBefore, "thread/read before").thread;
    const resumedThread = requireSuccess(resume, "thread/resume").thread;
    const afterThread = requireSuccess(readAfter, "thread/read after").thread;
    fixture.c0c1.readOnlyAttach = {
      thread: ref(candidateId),
      initialStatus: typeof candidate.status === "string" ? candidate.status : candidate.status?.type,
      readBefore: readBefore.record,
      resume: resume.record,
      readAfter: readAfter.record,
      identityStable: [beforeThread?.id, resumedThread?.id, afterThread?.id].every((id) => id === candidateId),
      cwdPresent: [beforeThread?.cwd, resumedThread?.cwd, afterThread?.cwd].every((cwd) => typeof cwd === "string" && cwd.length > 0),
      modelStable: beforeThread?.model === resumedThread?.model && resumedThread?.model === afterThread?.model,
      effortStable: beforeThread?.reasoningEffort === resumedThread?.reasoningEffort && resumedThread?.reasoningEffort === afterThread?.reasoningEffort,
      turnsExcluded: Array.isArray(resumedThread?.turns) && resumedThread.turns.length === 0,
    };

    await session.close();
    session = await openRpcSession({ enrollment, environment: selected.environment, observe, onLiveMessage });
    const coldResume = await call(session, "thread/resume", { threadId: candidateId, excludeTurns: true }, 60_000);
    fixture.c0c1.coldReopen = {
      response: coldResume.record,
      sameThread: requireSuccess(coldResume, "cold thread/resume").thread?.id === candidateId,
    };

    const modes = await call(session, "collaborationMode/list", {}, 60_000);
    const modeResult = requireSuccess(modes, "collaborationMode/list");
    fixture.actions.collaborationCatalog = {
      response: modes.record,
      count: Array.isArray(modeResult.data) ? modeResult.data.length : null,
      entries: (modeResult.data ?? []).map((entry) => ({
        name: entry.name,
        mode: entry.mode ?? null,
        hasModel: Object.hasOwn(entry, "model"),
        hasEffort: Object.hasOwn(entry, "reasoning_effort"),
        keys: Object.keys(entry).sort(),
      })),
    };

    const started = await call(session, "thread/start", {
      cwd: "/tmp", ephemeral: false, approvalPolicy: "never", sandbox: "read-only",
    }, 60_000);
    const startedThread = requireSuccess(started, "thread/start").thread;
    if (typeof startedThread?.id !== "string") throw new Error("thread/start omitted thread id");
    probeThreadId = startedThread.id;
    fixture.actions.thread = {
      id: ref(probeThreadId), response: started.record, cwdIsTmp: startedThread.cwd === "/tmp",
      model: startedThread.model, reasoningEffort: startedThread.reasoningEffort ?? null,
    };

    const goalGetEmpty = await call(session, "thread/goal/get", { threadId: probeThreadId });
    fixture.actions.goalGetEmpty = { response: goalGetEmpty.record, isNull: goalGetEmpty.response.result?.goal == null };
    const goalSet = await call(session, "thread/goal/set", {
      threadId: probeThreadId, objective: "CordCode native action probe", status: "paused", tokenBudget: 256,
    });
    fixture.actions.goalSet = {
      response: goalSet.record,
      recordFields: Object.keys(goalSet.response.result?.goal ?? {}).sort(),
      status: goalSet.response.result?.goal?.status ?? null,
      tokenBudget: goalSet.response.result?.goal?.tokenBudget ?? null,
      threadMatches: goalSet.response.result?.goal?.threadId === probeThreadId,
    };
    const goalUpdated = await waitFor((event) => event.method === "thread/goal/updated"
      && paramsOf(event).threadId === probeThreadId, "goal updated", 60_000);
    fixture.actions.goalSet.notification = {
      fields: Object.keys(paramsOf(goalUpdated).goal ?? {}).sort(),
      status: paramsOf(goalUpdated).goal?.status ?? null,
      observedAtMs: goalUpdated.at - startedAt,
    };
    const goalClear = await call(session, "thread/goal/clear", { threadId: probeThreadId });
    fixture.actions.goalClear = { response: goalClear.record, cleared: goalClear.response.result?.cleared === true };
    const goalCleared = await waitFor((event) => event.method === "thread/goal/cleared"
      && paramsOf(event).threadId === probeThreadId, "goal cleared", 60_000);
    fixture.actions.goalClear.notification = {
      keys: Object.keys(paramsOf(goalCleared)).sort(),
      observedAtMs: goalCleared.at - startedAt,
    };
    const goalGetCleared = await call(session, "thread/goal/get", { threadId: probeThreadId });
    fixture.actions.goalGetCleared = { response: goalGetCleared.record, isNull: goalGetCleared.response.result?.goal == null };

    const current = { mode: "default", model: startedThread.model, reasoningEffort: startedThread.reasoningEffort ?? null };
    const planPreset = (modeResult.data ?? []).find((entry) => entry.mode === "plan" || entry.name === "Plan");
    const defaultPreset = (modeResult.data ?? []).find((entry) => entry.mode === "default" || entry.name === "Default");
    const planPayload = presetMask(planPreset, current);
    const planUpdate = await call(session, "thread/settings/update", { threadId: probeThreadId, collaborationMode: planPayload });
    fixture.actions.planUpdate = {
      response: planUpdate.record,
      emptyAck: planUpdate.response.error == null && Object.keys(planUpdate.response.result ?? {}).length === 0,
      sent: { presetName: planPreset.name, mode: planPayload.mode, model: planPayload.settings.model,
        reasoningEffort: planPayload.settings.reasoning_effort, developerInstructionsIsNull: planPayload.settings.developer_instructions === null },
    };
    const planUpdated = await waitFor((event) => event.method === "thread/settings/updated"
      && paramsOf(event).threadId === probeThreadId, "plan settings updated", 60_000);
    fixture.actions.planUpdate.notification = collaborationSummary(planUpdated);
    const defaultPayload = presetMask(defaultPreset, { ...current, mode: planPayload.mode,
      model: planPayload.settings.model, reasoningEffort: planPayload.settings.reasoning_effort });
    const defaultUpdate = await call(session, "thread/settings/update", { threadId: probeThreadId, collaborationMode: defaultPayload });
    fixture.actions.defaultUpdate = {
      response: defaultUpdate.record,
      emptyAck: defaultUpdate.response.error == null && Object.keys(defaultUpdate.response.result ?? {}).length === 0,
      sent: { presetName: defaultPreset.name, mode: defaultPayload.mode, model: defaultPayload.settings.model,
        reasoningEffort: defaultPayload.settings.reasoning_effort, developerInstructionsIsNull: defaultPayload.settings.developer_instructions === null },
    };
    const defaultUpdated = await waitFor((event) => event.method === "thread/settings/updated"
      && paramsOf(event).threadId === probeThreadId && event.at > planUpdated.at, "default settings updated", 60_000);
    fixture.actions.defaultUpdate.notification = collaborationSummary(defaultUpdated);

    const turnStart = await call(session, "turn/start", {
      threadId: probeThreadId,
      input: [{ type: "text", text: "Reply exactly READY." }],
    }, 60_000);
    const turnId = requireSuccess(turnStart, "turn/start").turn?.id;
    if (typeof turnId !== "string") throw new Error("turn/start omitted turn id");
    await waitFor((event) => event.method === "turn/completed" && paramsOf(event).turn?.id === turnId, "seed turn completed");
    fixture.actions.seedTurn = { response: turnStart.record, turn: ref(turnId), completed: true };

    const compact = await call(session, "thread/compact/start", { threadId: probeThreadId }, 60_000);
    fixture.actions.compact = {
      response: compact.record,
      emptyAck: compact.response.error == null && Object.keys(compact.response.result ?? {}).length === 0,
    };
    requireSuccess(compact, "thread/compact/start");
    const compactStarted = await waitFor((event) => event.method === "item/started"
      && paramsOf(event).item?.type === "contextCompaction", "context compaction started");
    const compactItemId = paramsOf(compactStarted).item.id;
    const compactTurnId = paramsOf(compactStarted).turnId;
    await waitFor((event) => event.method === "item/completed"
      && paramsOf(event).item?.id === compactItemId, "context compaction completed");
    const compactTurn = await waitFor((event) => event.method === "turn/completed"
      && paramsOf(event).turn?.id === compactTurnId, "compaction turn completed");
    fixture.actions.compact.lifecycle = {
      turn: ref(compactTurnId), item: ref(compactItemId),
      terminalStatus: paramsOf(compactTurn).turn?.status ?? null,
      userNotificationObserved: rawEvents.some((event) => event.method === "item/started"
        && paramsOf(event).item?.type === "userMessage" && paramsOf(event).turnId === compactTurnId),
    };

    await session.close();
    session = await openRpcSession({ enrollment, environment: selected.environment, observe, onLiveMessage });
    const settingsNotificationsBeforeColdResume = rawEvents.filter((event) => event.method === "thread/settings/updated").length;
    const coldResumeProbe = await call(session, "thread/resume", { threadId: probeThreadId, excludeTurns: true }, 60_000);
    const coldResumeResult = requireSuccess(coldResumeProbe, "cold disposable thread/resume");
    await new Promise((resolve) => setTimeout(resolve, 1_000));
    const coldThread = coldResumeResult.thread;
    fixture.actions.planColdReadback = {
      response: coldResumeProbe.record,
      threadKeys: coldThread != null && typeof coldThread === "object" ? Object.keys(coldThread).sort() : [],
      topLevelThreadSettingsPresent: coldResumeResult.threadSettings != null,
      threadCollaborationModePresent: coldThread?.collaborationMode != null,
      newSettingsNotifications: rawEvents.filter((event) => event.method === "thread/settings/updated").length
        - settingsNotificationsBeforeColdResume,
    };
    const coldGoal = await call(session, "thread/goal/get", { threadId: probeThreadId });
    fixture.actions.goalColdReadback = {
      response: coldGoal.record,
      isNull: requireSuccess(coldGoal, "cold thread/goal/get").goal == null,
    };
    const coldTurns = await call(session, "thread/turns/list", {
      threadId: probeThreadId, limit: 10, sortDirection: "desc",
    }, 90_000);
    const coldTurn = (coldTurns.response.result?.data ?? []).find((turn) => turn.id === compactTurnId);
    fixture.actions.compact.cold = {
      response: coldTurns.record,
      sameTurn: coldTurn?.id === compactTurnId,
      status: coldTurn?.status ?? null,
      itemTypes: Array.isArray(coldTurn?.items) ? coldTurn.items.map((item) => item.type) : null,
    };
    const coldItems = await call(session, "thread/items/list", {
      threadId: probeThreadId, turnId: compactTurnId, limit: 100, sortDirection: "asc",
    }, 90_000);
    const coldItemEntries = requireSuccess(coldItems, "cold thread/items/list").data ?? [];
    const coldCompactEntry = coldItemEntries.find((entry) => entry.turnId === compactTurnId
      && entry.item?.id === compactItemId && entry.item?.type === "contextCompaction");
    fixture.actions.compact.coldItems = {
      response: coldItems.record,
      entryCount: coldItemEntries.length,
      sameTurnAndItem: coldCompactEntry != null,
      itemType: coldCompactEntry?.item?.type ?? null,
    };

    const deleted = await call(session, "thread/delete", { threadId: probeThreadId });
    fixture.cleanup.threadDelete = { response: deleted.record, emptyAck: deleted.response.error == null
      && Object.keys(deleted.response.result ?? {}).length === 0 };
  } finally {
    if (session != null) await session.close();
    fixture.cleanup.probeControllerRevoked = await cleanupController(enrollment, observe);
  }
  fixture.observationSummary = {
    count: observations.length,
    kinds: Object.fromEntries([...new Set(observations.map((entry) => entry.kind))].sort()
      .map((kind) => [kind, observations.filter((entry) => entry.kind === kind).length])),
    notificationMethods: Object.fromEntries([...new Set(rawEvents.map((entry) => entry.method))].sort()
      .map((method) => [method, rawEvents.filter((entry) => entry.method === method).length])),
  };
  process.stdout.write(`${JSON.stringify(fixture, null, 2)}\n`);
}

main().catch((error) => {
  process.stderr.write(`${JSON.stringify({ event: "probe_failed", message: error.message })}\n`);
  process.exitCode = 1;
});
