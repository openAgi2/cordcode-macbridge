#!/usr/bin/env node

// Owner-authorized end-to-end proof for authoritative Plan cold readback.
// The probe owns an isolated development runtime, disposable thread, controller
// enrollment, and CODEX_HOME. It never replaces or stops the signed Desktop app.

import { createHash } from "node:crypto";
import { execFileSync, spawn } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { isAbsolute, join } from "node:path";
import {
  cleanupController,
  enrollController,
  openRpcSession,
  pairedEnvironments,
  redactErrorMessage,
} from "./lib/controller_session.mjs";

const helperSource = new URL("./device_key_helper.swift", import.meta.url).pathname;
const startedAt = Date.now();
const events = [];
const runtimeBinary = process.env.CODEX_REMOTE_PROBE_BINARY;
const sourceRoot = process.env.CODEX_REMOTE_PROBE_SOURCE_ROOT;
if (typeof runtimeBinary !== "string" || !isAbsolute(runtimeBinary) || !existsSync(runtimeBinary)) {
  throw new Error("CODEX_REMOTE_PROBE_BINARY must name the patched absolute codex binary");
}
if (typeof sourceRoot !== "string" || !isAbsolute(sourceRoot) || !existsSync(sourceRoot)) {
  throw new Error("CODEX_REMOTE_PROBE_SOURCE_ROOT must name the patched codex checkout");
}

let threadId = null;
let threadDeleted = false;

function ref(value) {
  return typeof value === "string" && value.length > 0
    ? `id-${createHash("sha256").update(value).digest("hex").slice(0, 12)}` : null;
}

function observe(kind, detail = {}) {
  process.stderr.write(`${JSON.stringify({ event: kind, ...detail })}\n`);
}

function onLiveMessage(method, rpc) {
  const params = rpc?.params ?? {};
  const eventThreadId = params.threadId ?? params.thread?.id ?? params.turn?.threadId;
  if (threadId != null && eventThreadId !== threadId) return;
  events.push({ method, params, at: Date.now() });
  observe("notification", { method, thread: ref(eventThreadId), turn: ref(params.turnId ?? params.turn?.id) });
}

async function call(session, method, params, timeoutMs = 60_000) {
  const response = await session.rpc(method, params, { timeoutMs });
  observe("rpc", { method, error: response.error != null, durationMs: response.ms });
  if (response.error != null) throw new Error(`${method} failed code=${response.error.code}`);
  return {
    result: response.result,
    record: {
      method,
      requestKeys: Object.keys(params ?? {}).sort(),
      resultKeys: response.result != null && typeof response.result === "object"
        ? Object.keys(response.result).sort() : [],
      durationMs: response.ms,
      observedAtMs: Date.now() - startedAt,
    },
  };
}

async function waitEvent(predicate, label, timeoutMs = 120_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const event = events.find(predicate);
    if (event != null) return event;
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error(`notification timeout: ${label}`);
}

function settingsSummary(settings) {
  const collaboration = settings?.collaborationMode;
  const modeSettings = collaboration?.settings;
  return {
    threadSettingsKeys: settings != null ? Object.keys(settings).sort() : [],
    mode: collaboration?.mode ?? null,
    model: modeSettings?.model ?? null,
    reasoningEffort: modeSettings?.reasoning_effort ?? null,
    developerInstructionsKind: modeSettings?.developer_instructions === null
      ? "null" : typeof modeSettings?.developer_instructions,
  };
}

function runtimeEnvironment(probeHome) {
  return { ...process.env, CODEX_HOME: probeHome };
}

function findRuntimeSocket(pid) {
  const output = execFileSync("/usr/sbin/lsof", ["-a", "-p", String(pid), "-U", "-Fn"], {
    encoding: "utf8", timeout: 10_000,
  });
  const socket = output.split("\n")
    .filter((line) => line.startsWith("n"))
    .map((line) => line.slice(1))
    .find((path) => path.startsWith("/tmp/codex-rc-") && path.endsWith("/rc.sock"));
  if (socket == null) throw new Error("foreground runtime socket was not discoverable");
  return socket;
}

function startRuntime(probeHome) {
  return new Promise((resolveStart, rejectStart) => {
    const child = spawn(runtimeBinary, ["remote-control", "--json"], {
      cwd: tmpdir(), env: runtimeEnvironment(probeHome), stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    const timeout = setTimeout(() => {
      child.kill("SIGINT");
      rejectStart(new Error("patched runtime startup timed out"));
    }, 30_000);
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => { stderr = `${stderr}${chunk}`.slice(-8_192); });
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      for (;;) {
        const newline = stdout.indexOf("\n");
        if (newline < 0) break;
        const line = stdout.slice(0, newline).trim();
        stdout = stdout.slice(newline + 1);
        if (line === "") continue;
        let ready;
        try { ready = JSON.parse(line); } catch { continue; }
        if (ready.status !== "connected" || typeof ready.environmentId !== "string") continue;
        clearTimeout(timeout);
        try {
          const socketPath = findRuntimeSocket(child.pid);
          observe("runtime_started", { binary: "patched-dev", environment: ref(ready.environmentId) });
          resolveStart({ child, environmentId: ready.environmentId, socketPath });
        } catch (error) {
          child.kill("SIGINT");
          rejectStart(error);
        }
      }
    });
    child.once("exit", (code, signal) => {
      if (code === null && signal === "SIGINT") return;
      clearTimeout(timeout);
      rejectStart(new Error(`patched runtime exited code=${code} signal=${signal}: ${redactErrorMessage(stderr) ?? "unknown"}`));
    });
  });
}

async function stopRuntime(runtime) {
  if (runtime == null || runtime.child.exitCode != null || runtime.child.signalCode != null) return;
  const exited = new Promise((resolveExit) => runtime.child.once("exit", resolveExit));
  runtime.child.kill("SIGINT");
  await Promise.race([
    exited,
    new Promise((_, rejectStop) => setTimeout(() => rejectStop(new Error("patched runtime did not stop after SIGINT")), 10_000)),
  ]);
  observe("runtime_stopped", { binary: "patched-dev" });
}

function startPairing(runtime, probeHome) {
  const controlDir = join(probeHome, "app-server-control");
  const controlSocket = join(controlDir, "app-server-control.sock");
  mkdirSync(controlDir, { recursive: true, mode: 0o700 });
  rmSync(controlSocket, { force: true });
  symlinkSync(runtime.socketPath, controlSocket);
  const stdout = execFileSync(runtimeBinary, ["remote-control", "pair", "--json"], {
    cwd: tmpdir(), env: runtimeEnvironment(probeHome), encoding: "utf8", timeout: 30_000,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const response = JSON.parse(stdout);
  if (response.environmentId !== runtime.environmentId || typeof response.manualPairingCode !== "string") {
    throw new Error("pairing response did not match the isolated runtime");
  }
  observe("pairing_started", { environment: ref(response.environmentId), code: "memory-only" });
  return response.manualPairingCode;
}

function selectEnvironment(items, expectedEnvironmentId) {
  const matches = items.filter((item) => item.env_id === expectedEnvironmentId && item.online === true);
  if (matches.length !== 1) throw new Error("controller did not resolve exactly one isolated runtime environment");
  return matches[0];
}

async function openControllerSession(enrollment, environment) {
  return openRpcSession({ enrollment, environment, observe, onLiveMessage });
}

async function main() {
  // Keep this path short: the official daemon client's control-socket suffix
  // must fit macOS sockaddr_un.sun_path (SUN_LEN).
  const probeHome = mkdtempSync("/tmp/cc-plan-");
  chmodSync(probeHome, 0o700);
  const authSource = join(homedir(), ".codex", "auth.json");
  const modelsSource = join(homedir(), ".codex", "models_cache.json");
  copyFileSync(authSource, join(probeHome, "auth.json"));
  chmodSync(join(probeHome, "auth.json"), 0o600);
  if (existsSync(modelsSource)) {
    copyFileSync(modelsSource, join(probeHome, "models_cache.json"));
    chmodSync(join(probeHome, "models_cache.json"), 0o600);
  }

  const binaryHash = createHash("sha256").update(readFileSync(runtimeBinary)).digest("hex");
  const sourceCommit = execFileSync("git", ["-C", sourceRoot, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  const cliVersion = execFileSync(runtimeBinary, ["--version"], { encoding: "utf8" }).trim();
  const fixture = {
    schemaVersion: 2,
    classification: "LIVE-REDACTED-OBSERVATION",
    target: {
      runtime: "isolated-patched-foreground",
      cliVersion,
      sourceCommit,
      binarySha256: binaryHash,
      signedDesktopBundleModified: false,
    },
    authorization: "owner-authorized-2026-09-10",
    purpose: "Plan authoritative cold-read regression",
    observations: {},
    cleanup: {},
  };

  let runtime = null;
  let enrollment = null;
  let session = null;
  try {
    runtime = await startRuntime(probeHome);
    let pairingCode = startPairing(runtime, probeHome);
    enrollment = await enrollController({ helperSource, observe });
    const environments = await pairedEnvironments(
      enrollment.token, enrollment.accountID, enrollment.clientID,
      async () => pairingCode,
    );
    pairingCode = null;
    const environment = selectEnvironment(environments.body.items, runtime.environmentId);
    fixture.environment = { online: environment.online === true, clientType: environment.client_type };
    session = await openControllerSession(enrollment, environment);

    const catalog = (await call(session, "collaborationMode/list", {})).result.data ?? [];
    const plan = catalog.find((entry) => entry.mode === "plan" || entry.name === "Plan");
    const defaultMode = catalog.find((entry) => entry.mode === "default" || entry.name === "Default");
    if (plan == null || defaultMode == null) throw new Error("Plan or Default preset absent");
    const started = await call(session, "thread/start", {
      cwd: "/tmp", ephemeral: false, approvalPolicy: "never", sandbox: "read-only",
    });
    threadId = started.result.thread?.id;
    if (typeof threadId !== "string") throw new Error("thread/start omitted id");

    const seed = await call(session, "turn/start", {
      threadId, input: [{ type: "text", text: "Reply exactly READY." }],
    });
    const seedTurnId = seed.result.turn?.id;
    await waitEvent((event) => event.method === "turn/completed" && event.params.turn?.id === seedTurnId, "seed turn");

    const currentModel = started.result.model;
    const planMode = {
      mode: plan.mode,
      settings: {
        model: plan.model ?? currentModel,
        reasoning_effort: Object.hasOwn(plan, "reasoning_effort")
          ? plan.reasoning_effort : (started.result.reasoningEffort ?? null),
        developer_instructions: null,
      },
    };
    const planUpdateAt = Date.now();
    const planUpdate = await call(session, "thread/settings/update", { threadId, collaborationMode: planMode });
    const planEvent = await waitEvent((event) => event.method === "thread/settings/updated"
      && event.params.threadId === threadId && event.at >= planUpdateAt, "Plan update");
    fixture.observations.livePlanUpdate = {
      response: planUpdate.record,
      notification: settingsSummary(planEvent.params.threadSettings),
    };

    await session.close();
    session = null;
    await stopRuntime(runtime);
    runtime = await startRuntime(probeHome);
    if (runtime.environmentId !== environment.env_id) throw new Error("cold runtime restart changed environment identity");
    session = await openControllerSession(enrollment, environment);
    const resumedPlan = await call(session, "thread/resume", { threadId, excludeTurns: true });
    const coldPlan = await call(session, "thread/settings/get", { threadId });
    fixture.observations.coldPlanRead = {
      restartEnvironmentStable: runtime.environmentId === environment.env_id,
      resume: resumedPlan.record,
      response: coldPlan.record,
      settings: settingsSummary(coldPlan.result.threadSettings),
    };

    const defaultSettings = coldPlan.result.threadSettings?.collaborationMode?.settings;
    const defaultPayload = {
      mode: defaultMode.mode,
      settings: {
        model: defaultMode.model ?? defaultSettings?.model,
        reasoning_effort: Object.hasOwn(defaultMode, "reasoning_effort")
          ? defaultMode.reasoning_effort : (defaultSettings?.reasoning_effort ?? null),
        developer_instructions: null,
      },
    };
    const defaultUpdateAt = Date.now();
    const defaultUpdate = await call(session, "thread/settings/update", { threadId, collaborationMode: defaultPayload });
    const defaultEvent = await waitEvent((event) => event.method === "thread/settings/updated"
      && event.params.threadId === threadId && event.at >= defaultUpdateAt, "Default update");
    fixture.observations.liveDefaultUpdate = {
      response: defaultUpdate.record,
      notification: settingsSummary(defaultEvent.params.threadSettings),
    };

    await session.close();
    session = null;
    await stopRuntime(runtime);
    runtime = await startRuntime(probeHome);
    if (runtime.environmentId !== environment.env_id) throw new Error("second cold restart changed environment identity");
    session = await openControllerSession(enrollment, environment);
    const resumedDefault = await call(session, "thread/resume", { threadId, excludeTurns: true });
    const coldDefault = await call(session, "thread/settings/get", { threadId });
    fixture.observations.coldDefaultRead = {
      restartEnvironmentStable: runtime.environmentId === environment.env_id,
      resume: resumedDefault.record,
      response: coldDefault.record,
      settings: settingsSummary(coldDefault.result.threadSettings),
    };

    const deleted = await call(session, "thread/delete", { threadId });
    fixture.cleanup.threadDeleteEmptyAck = Object.keys(deleted.result ?? {}).length === 0;
    threadDeleted = true;
  } finally {
    if (session != null && threadId != null && !threadDeleted) {
      const deleted = await session.rpc("thread/delete", { threadId }, { timeoutMs: 30_000 }).catch(() => null);
      fixture.cleanup.threadDeleteAfterFailure = deleted?.error == null;
    }
    if (session != null) await session.close();
    if (enrollment != null) fixture.cleanup.probeControllerRevoked = await cleanupController(enrollment, observe);
    await stopRuntime(runtime).catch((error) => observe("runtime_cleanup_failed", { message: error.message }));
    rmSync(probeHome, { recursive: true, force: true });
    fixture.cleanup.isolatedHomeDeleted = !existsSync(probeHome);
  }
  process.stdout.write(`${JSON.stringify(fixture, null, 2)}\n`);
}

main().catch((error) => {
  process.stderr.write(`${JSON.stringify({ event: "probe_failed", message: redactErrorMessage(error.message) })}\n`);
  process.exitCode = 1;
});
