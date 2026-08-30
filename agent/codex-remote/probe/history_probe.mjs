#!/usr/bin/env node

// Phase 0 lazy-history G0 probe: thread/turns/list + thread/items/list +
// thread/read(historyMode, includeTurns control) + thread/resume(initialTurnsPage)
// against the real Desktop app-server, over the owner-authorized controller path.
//
// Probe contract (per probe/README.md):
// - Evidence question: plan §3.0.5 nine-item fixture set, §3.0.6 anti-misjudgment
//   stats, §3.0.7 negative-result assertions, T0.2 baseline bytes/wall-time by
//   historyMode, T0.6 initialTurnsPage candidate (thread.turns == [] mandatory).
// - Target: the ChatGPT Desktop actually installed (versions DETECTED at
//   runtime from Info.plist and the embedded codex binary, recorded verbatim in
//   the fixture with a drift assessment against the plan-frozen baseline
//   26.825.32147 / codex-cli 0.150.0-alpha.12.2; official behavior baseline is
//   upstream tag rust-v0.150.0-alpha.12.2 anchors — see drift_assessment).
// - Required human action: fresh step-up in the opened browser page; if pairing
//   is required, a one-time localhost form collects a fresh Desktop manual
//   pairing code (never chat/stdout/disk). The owner does NOT need to send any
//   chat message for this probe.
// - Origin allowlist: https://chatgpt.com + frozen controller paths (same as
//   live_controller.mjs). No other network targets.
// - Timeouts: step-up 10 min human input; pairing 10 min human input; per-RPC
//   30 s (control full-read 180 s; resume candidate 60 s); WSS idle 120 s;
//   total run cap 20 min. All pagination loops are bounded by CAPS.
// - Redaction boundary: output contains only method names, structural field
//   names, counts, lengths, whitelisted enum values, relative timestamps and
//   stable per-capture pseudonyms (id-N / cur-N). All user/assistant text,
//   paths, error message bodies and raw identifiers are removed. Credentials
//   never leave memory. Raw ids/cursors live only in process memory.
// - Cleanup: revokes ONLY client_probe, verifies rejection post-revoke, deletes
//   the probe key, removes the temporary helper directory.
// - Expected failures that are legitimate observations: HTTP 409/single-owner
//   (stop; never disconnect another controller); thread/items/list
//   method-not-found on legacy threads (§2.5 legal probe target); rpc error for
//   illegal turnId (expected). §3.0.7 negative findings are recorded and the
//   run still completes — adjudication happens offline in the assert harness.
// - Evidence artifacts: stdout line REDACTED_FIXTURE=<json>; the operator saves
//   it under testdata/phase0/live/attempt-XXX-history-*.json after filling
//   operator_attestation and recording a gitleaks PASS.

import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  enrollController,
  cleanupController,
  collectManualPairingCode,
  pairedEnvironments,
  revokeProbeController,
  selectEnvironment,
  openRpcSession,
  redactErrorMessage,
  threadStatusType,
  withTimeout,
} from "./lib/controller_session.mjs";

const helperSource = new URL("./device_key_helper.swift", import.meta.url).pathname;
const scriptPath = fileURLToPath(import.meta.url);

// Abnormal-exit ledger (owner adjudication 2026-08-30): each run records its
// controller clientID here (identifier only, no secrets); a clean cleanup
// removes it, and the next run revokes whatever previous runs left behind —
// so wedged/aborted processes no longer leave enrollments in the device list.
const LEDGER_DIR = "/tmp/codex-remote-probe-ledger";
function writeLedger(clientID) {
  fs.mkdirSync(LEDGER_DIR, { recursive: true, mode: 0o700 });
  const file = path.join(LEDGER_DIR, `${clientID}.client`);
  fs.writeFileSync(file, clientID, { mode: 0o600 });
  return file;
}
async function revokeOrphanedControllers(enrollment, ownClientID, observe) {
  let names = [];
  try { names = fs.readdirSync(LEDGER_DIR).filter((n) => n.endsWith(".client")); } catch { return; }
  for (const name of names) {
    const clientID = name.replace(/\.client$/u, "");
    if (clientID === ownClientID) continue;
    try {
      const response = await revokeProbeController(enrollment.token, enrollment.accountID, clientID);
      observe("orphan_controller_revoked", { http_status: response.status });
      fs.rmSync(path.join(LEDGER_DIR, name));
    } catch (error) {
      observe("orphan_controller_revoke_failed", { error: redactErrorMessage(error.message) ?? "unknown" });
    }
  }
}

const CAPS = {
  threadListPages: 3,
  threadListLimit: 50,
  // Attempt 3: recency-first sampling missed all 128 legacy threads (recent
  // threads are all paginated), so discovery now sweeps the whole inventory
  // with a stride in addition to the recency head.
  discoveryThreads: 24,
  // Page-1 only since attempt 4: deeper discovery pages cold-load old threads
  // server-side and can blow the RPC timeout (attempt-3 lesson); depth beyond
  // one page is signaled by pageOneHasMore instead.
  discoveryPages: 1,
  // Full-chain pages may also cold-load; give them room under the idle-yield
  // watchdog (a hung page still fails the page, not the run).
  chainPageTimeoutMs: 90_000,
  turnsPageLimit: 30,
  turnsChainPages: 80,
  itemsPageLimit: 5,
  itemsChainPages: 60,
  secondaryThreads: 2,
  secondaryItemsPages: 10,
  rpcTimeoutMs: 30_000,
  // thread/read includeTurns on a cold paginated thread can take minutes
  // server-side (2026-08-30 attempt 1: no response within 180s); the idle
  // watchdog now yields to pending RPCs, so this can safely be generous.
  controlReadTimeoutMs: 240_000,
  resumeTimeoutMs: 60_000,
  illegalTurnId: "00000000-0000-0000-0000-000000000000",
};

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/iu;
const LONG_OPAQUE_RE = /^(?:[0-9a-f]{16,}|[A-Za-z0-9_-]{32,})$/u;
const ENUM_KEYS = new Set([
  "type", "status", "itemsView", "historyMode", "sortDirection", "sortKey",
  "code", "kind", "phase", "source", "view", "mode", "errorType", "turnStatus",
]);

const startedAt = Date.now();
const timeline = [];
const liveSeen = [];

// Pseudonym maps. Forward maps feed the fixture; reverse lookups stay in memory
// so pagination walks can recover raw cursors/ids for subsequent calls.
const rawToRefId = new Map();
const refToRawId = new Map();
const rawToRefCursor = new Map();
const refToRawCursor = new Map();

function pseudonym(value) {
  if (typeof value !== "string") return null;
  if (!UUID_RE.test(value) && !LONG_OPAQUE_RE.test(value)) return null;
  let ref = rawToRefId.get(value);
  if (ref == null) {
    ref = `id-${refToRawId.size + 1}`;
    refToRawId.set(ref, value);
    rawToRefId.set(value, ref);
  }
  return ref;
}
function rawFromRef(ref) {
  return refToRawId.get(ref) ?? null;
}
function cursorRef(value) {
  if (value == null) return null;
  if (typeof value !== "string") return "<non-string>";
  let ref = rawToRefCursor.get(value);
  if (ref == null) {
    ref = `cur-${refToRawCursor.size + 1}`;
    refToRawCursor.set(ref, value);
    refToRawCursor.set(value, ref);
  }
  return ref;
}
function rawCursorFromRef(ref) {
  return refToRawCursor.get(ref) ?? null;
}

function observe(kind, detail = {}) {
  timeline.push({ t_ms: Date.now() - startedAt, kind, ...detail });
  console.error(JSON.stringify({ event: kind, ...detail }));
}

function isSafeEnumValue(value) {
  return value.length > 0 && value.length <= 32 && /^[A-Za-z0-9_.\/-]+$/u.test(value);
}

function tsOffsetSeconds(value) {
  const parsed = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(parsed)) return null;
  return `t+${Math.max(0, Math.round(parsed - Math.floor(startedAt / 1000)))}s`;
}

// Generic redacting structural summarizer: id-shaped strings become pseudonyms,
// enum values survive only for whitelisted keys, every other string becomes
// {len}, *At numbers become relative offsets, string arrays record lengths.
function shape(value, key = null, depth = 0) {
  if (value == null) return null;
  if (typeof value === "boolean") return value;
  if (typeof value === "number") {
    if (key != null && /At$/u.test(key) && Math.abs(value) > 1e9) return tsOffsetSeconds(value);
    return value;
  }
  if (typeof value === "string") {
    const ref = pseudonym(value);
    if (ref != null) return ref;
    if (key != null && ENUM_KEYS.has(key) && isSafeEnumValue(value)) return value;
    return { len: value.length };
  }
  if (Array.isArray(value)) {
    if (value.length > 0 && value.every((entry) => typeof entry === "string")) {
      return { n: value.length, lens: value.map((entry) => pseudonym(entry) ?? entry.length) };
    }
    if (depth >= 6 || value.length > 400) return { n: value.length, truncated: true };
    return { n: value.length, items: value.map((entry) => shape(entry, null, depth + 1)) };
  }
  if (typeof value === "object") {
    const out = {};
    for (const [k, v] of Object.entries(value)) out[k] = shape(v, k, depth + 1);
    return out;
  }
  return null;
}

function resultBytes(result) {
  try { return Buffer.byteLength(JSON.stringify(result ?? null)); } catch { return null; }
}

// §7-4 version discipline: detect the REAL installed target at runtime instead
// of claiming the plan-frozen version. Drift is recorded, never hidden; the
// plan baseline stays rust-v0.150.0-alpha.12.2 and re-anchoring is an owner call.
function detectInstalledTarget() {
  const read = (args) => {
    const out = spawnSync("defaults", args, { encoding: "utf8" });
    return out.status === 0 ? out.stdout.trim() : null;
  };
  const plist = "/Applications/ChatGPT.app/Contents/Info.plist";
  const desktopVersion = read(["read", plist, "CFBundleShortVersionString"]);
  const bundleVersion = read(["read", plist, "CFBundleVersion"]);
  let embeddedVersion = null;
  try {
    const out = spawnSync("/Applications/ChatGPT.app/Contents/Resources/codex", ["--version"], { encoding: "utf8", timeout: 10_000 });
    if (out.status === 0) embeddedVersion = out.stdout.trim().split("\n")[0] ?? null;
  } catch {}
  return {
    chatgptDesktopVersion: desktopVersion,
    bundleVersion,
    embeddedCodexVersion: embeddedVersion,
  };
}

function paramsRecordOf(params) {
  const record = {};
  for (const [k, v] of Object.entries(params)) {
    if (k === "cursor") { record[k] = cursorRef(v); continue; }
    const ref = typeof v === "string" ? pseudonym(v) : null;
    record[k] = ref != null ? ref : shape(v, k);
  }
  return record;
}

function errorRecordOf(error) {
  if (error == null) return null;
  const code = error.code;
  return {
    code: typeof code === "number" || (typeof code === "string" && isSafeEnumValue(code)) ? code : shape(code, "code"),
    messageLen: typeof error.message === "string" ? error.message.length : null,
    messageShape: typeof error.message === "string" ? shape(redactErrorMessage(error.message) ?? "", "message") : null,
  };
}

// RPC wrapper: records pseudonymized params, wall time, result bytes, and the
// redacted structural shape of a successful result.
async function call(session, method, params, { timeoutMs = CAPS.rpcTimeoutMs, note = null, slim = false } = {}) {
  const response = await session.rpc(method, params, { timeoutMs });
  const record = {
    method,
    params: paramsRecordOf(params),
    ms: response.ms,
    resultBytes: resultBytes(response.result),
    error: errorRecordOf(response.error),
    ...(note != null ? { note } : {}),
  };
  if (response.error == null && slim) record.resultFields = Object.keys(response.result ?? {}).sort();
  else if (response.error == null) record.resultShape = shape(response.result);
  observe("rpc", { method, error: response.error != null, error_code: record.error?.code ?? null, ms: response.ms, resultBytes: record.resultBytes, note });
  return { record, response };
}

// Some history reads fail when the Desktop does not have the thread loaded;
// retry once after a metadata-only resume (both attempts recorded).
async function callWithLoadRetry(session, method, params, options = {}) {
  const first = await call(session, method, params, options);
  if (first.response.error == null) return first;
  const message = first.response.error?.message ?? "";
  if (!/not.*(load|found)|unknown|no such/iu.test(message)) return first;
  observe("load_retry", { method, after_error_code: first.record.error?.code ?? null });
  await call(session, "thread/resume", { threadId: params.threadId, excludeTurns: true }, { note: "load-retry-resume" });
  return call(session, method, params, { ...options, note: `${options.note ?? ""}|retried-after-resume` });
}

// Control full-read is allowed to fail (server cold-load can outlive the RPC
// timeout, or the socket may drop first): record the failure as an observation
// and keep the rest of the battery running (attempt-1 lesson).
async function controlFullRead(session, threadId, note, timeoutMs = CAPS.controlReadTimeoutMs) {
  try {
    return await callWithLoadRetry(session, "thread/read", { threadId, includeTurns: true }, { timeoutMs, note, slim: true });
  } catch (error) {
    const message = redactErrorMessage(error.message) ?? "unknown";
    observe("control_read_exception", { note, error: message });
    return {
      record: { method: "thread/read", params: paramsRecordOf({ threadId, includeTurns: true }), error: { code: "rpc-exception", message }, note },
      response: { error: { code: "rpc-exception", message } },
    };
  }
}

function turnsOfResult(result) {
  return Array.isArray(result?.data) ? result.data : [];
}

// Walk thread/turns/list in one direction until EOF, cap, or error.
// Returns page records (fixture-safe) plus raw turn ids (memory-only).
async function walkTurnsPages(session, rawThreadId, { itemsView, sortDirection, rawStartCursor, limit, maxPages, note, timeoutMs }) {
  const pages = [];
  const rawTurnIds = [];
  let cursor = rawStartCursor ?? null;
  for (let page = 1; page <= maxPages; page += 1) {
    const params = { threadId: rawThreadId, limit, sortDirection, itemsView };
    if (cursor != null) params.cursor = cursor;
    const { record, response } = await callWithLoadRetry(session, "thread/turns/list", params, { note: `${note}|page-${page}`, slim: true, ...(timeoutMs != null ? { timeoutMs } : {}) });
    const turns = turnsOfResult(response.result);
    if (response.error == null) for (const turn of turns) rawTurnIds.push(turn.id);
    pages.push({
      page,
      requestCursor: cursorRef(cursor),
      record,
      turns: response.error == null ? turns.map((turn) => shape(turn)) : null,
      turnIds: response.error == null ? turns.map((turn) => pseudonym(turn.id) ?? "<unparsed>") : null,
      nextCursor: cursorRef(response.result?.nextCursor ?? null),
      backwardsCursor: cursorRef(response.result?.backwardsCursor ?? null),
      error: response.error != null,
    });
    if (response.error != null) break;
    const next = response.result?.nextCursor ?? null;
    if (next == null) break;
    if (cursor === next) {
      pages.push({ page: page + 1, repeatedCursorDetected: true, requestCursor: cursorRef(cursor), nextCursor: cursorRef(next) });
      break;
    }
    cursor = next;
  }
  return { pages, rawTurnIds };
}

// Walk thread/items/list for one turn (asc) until EOF, cap, or error.
async function walkItemsPages(session, rawThreadId, rawTurnId, { limit, maxPages, note }) {
  const pages = [];
  let cursor = null;
  for (let page = 1; page <= maxPages; page += 1) {
    const params = { threadId: rawThreadId, turnId: rawTurnId, limit, sortDirection: "asc" };
    if (cursor != null) params.cursor = cursor;
    const { record, response } = await call(session, "thread/items/list", params, { note: `${note}|page-${page}`, slim: true });
    const entries = Array.isArray(response.result?.data) ? response.result.data : [];
    pages.push({
      page,
      requestCursor: cursorRef(cursor),
      record,
      entries: response.error == null ? entries.map((entry) => shape(entry)) : null,
      entryCount: response.error == null ? entries.length : null,
      nextCursor: cursorRef(response.result?.nextCursor ?? null),
      backwardsCursor: cursorRef(response.result?.backwardsCursor ?? null),
      error: response.error != null,
      errorCode: response.error != null ? (response.error.code ?? null) : null,
    });
    if (response.error != null) break;
    const next = response.result?.nextCursor ?? null;
    if (next == null) break;
    if (cursor === next) {
      pages.push({ page: page + 1, repeatedCursorDetected: true });
      break;
    }
    cursor = next;
  }
  return pages;
}

function fixtureObservations() {
  // Distill the timeline: keep kinds and small scalar fields only; heavy data
  // already lives in fixture.data.
  return timeline.map((entry) => {
    const out = { kind: entry.kind, t_ms: entry.t_ms };
    for (const [key, value] of Object.entries(entry)) {
      if (key === "kind" || key === "t_ms") continue;
      if (value == null || typeof value === "boolean" || typeof value === "number") out[key] = value;
      else if (typeof value === "string" && value.length <= 80) out[key] = value;
      else if (Array.isArray(value) && value.every((v) => typeof v === "string") && value.length <= 20) out[key] = value;
    }
    return out;
  });
}

async function main() {
  const detected = detectInstalledTarget();
  const fixture = {
    schema_version: 2,
    classification: "LIVE-REDACTED-OBSERVATION",
    gate_effect: "g0-evidence-input",
    target: {
      detected: {
        chatgpt_desktop_version: detected.chatgptDesktopVersion,
        chatgpt_bundle_version: detected.bundleVersion,
        embedded_codex_version: detected.embeddedCodexVersion,
        controller_protocol_version: 3,
      },
      plan_frozen_baseline: {
        chatgpt_desktop_version: "26.825.32147",
        embedded_codex_version: "codex-cli 0.150.0-alpha.12.2",
        upstream_tag: "rust-v0.150.0-alpha.12.2",
      },
      drift_assessment: {
        drifted: detected.chatgptDesktopVersion !== "26.825.32147" || detected.embeddedCodexVersion !== "codex-cli 0.150.0-alpha.12.2",
        note: "Installed target is recorded as detected. Plan §7-4: version deviation fails closed and is reported; re-anchoring the behavior baseline to the installed version is an owner decision. Protocol-surface check 2026-08-30: diff rust-v0.150.0-alpha.12.2 -> rust-v0.151.0-alpha.7.2 over the anchored files is additive-only (adds usageMetadata on thread.rs, functionCallOutput item variant on item.rs, misalignment error details on thread_data.rs; turns/items/read/resume anchored structs and thread_processor.rs anchored functions unchanged).",
      },
    },
    metadata: {
      captured_at: new Date().toISOString(),
      capture_purpose: "G0 lazy-history fixtures: thread/turns/list (summary/notLoaded, desc pagination to EOF, backwards round-trip), thread/items/list (turnId filter, asc pages, illegal turnId), thread/read metadata historyMode + includeTurns control inventory (probe-only), thread/resume excludeTurns+initialTurnsPage candidate; baseline bytes/wall-time by historyMode.",
      source_classification: "target binary plus official live service",
      operator_attestation: "PENDING-OWNER",
      redaction_procedure: "Pseudonymized all id-shaped strings (id-N) and cursors (cur-N) per capture; kept method names, whitelisted enum values, counts, lengths and relative timestamps only; removed all user/assistant text, paths, error message bodies and raw identifiers.",
      secret_scan_command: "gitleaks dir --redact --config .gitleaks.toml .",
      secret_scan_result: "PENDING",
      cleanup_result: "PENDING",
    },
    probe: {
      entry: path.relative(process.cwd(), scriptPath),
      caps: CAPS,
      upstream_anchor: {
        tag: "rust-v0.150.0-alpha.12.2",
        turns_list: "app-server-protocol/src/protocol/v2/thread.rs:1684-1698; thread_processor.rs:3006-3219",
        items_list: "thread.rs:1718-1732; thread_processor.rs:3365-3422",
        read_include_turns: "thread.rs:1650-1658; thread_processor.rs:2981-3000",
        turn_full_items_invariants: "thread_processor.rs:3222-3258",
        resume_initial_page: "thread.rs:398-449; thread_processor.rs:3292-3332",
      },
    },
    observations: [],
    data: {},
    adjudication: {
      result: "CAPTURED-PENDING-ASSERTIONS",
      note: "Run validate/history-fixture-assert.mjs against this fixture for §3.0.7 negative checks and the §3.0.5 checklist.",
    },
  };

  const enrollment = await enrollController({ helperSource, observe });
  let ledgerFile = writeLedger(enrollment.clientID);
  await revokeOrphanedControllers(enrollment, enrollment.clientID, observe);
  let session = null;
  try {
    const environments = await pairedEnvironments(
      enrollment.token,
      enrollment.accountID,
      enrollment.clientID,
      () => collectManualPairingCode(
        "Phase 0 history probe pairing",
        `<p>在 ChatGPT Desktop 的“控制这台 Mac”授权页切换到“电脑”，刷新生成新配对码，仅在此本机页面输入。不要发送到聊天。</p><p>本探针只读取线程历史元数据与结构，不需要您发送任何消息。</p>`,
      ),
    );
    const selected = selectEnvironment(environments.body?.items ?? []);
    observe("environment_selected", { selected_index: selected.index + 1, online: selected.environment.online === true, client_type: selected.environment.client_type });

    session = await openRpcSession({
      enrollment,
      environment: selected.environment,
      observe,
      onLiveMessage: (method) => liveSeen.push(method),
    });

    // ---- 1) inventory: thread/list pages -> threadId -> historyMode
    const inventoryThreads = [];
    let listCursor = null;
    for (let page = 1; page <= CAPS.threadListPages; page += 1) {
      const params = { limit: CAPS.threadListLimit, sortKey: "recency_at", sortDirection: "desc" };
      if (listCursor != null) params.cursor = listCursor;
      const { record, response } = await call(session, "thread/list", params, { note: `inventory|page-${page}` });
      if (response.error != null) break;
      const data = Array.isArray(response.result?.data) ? response.result.data : [];
      for (const thread of data) {
        const ref = pseudonym(thread.id);
        if (ref == null || inventoryThreads.some((t) => t.thread === ref)) continue;
        inventoryThreads.push({
          thread: ref,
          historyMode: thread.historyMode ?? null,
          statusType: threadStatusType(thread.status),
          previewLen: typeof thread.preview === "string" ? thread.preview.length : null,
          sessionIdPresent: typeof thread.sessionId === "string",
          threadFields: Object.keys(thread).sort(),
        });
      }
      const next = response.result?.nextCursor ?? null;
      if (next == null || data.length === 0) break;
      listCursor = next;
    }
    const inventory = {
      threads: inventoryThreads,
      counts: {
        total: inventoryThreads.length,
        paginated: inventoryThreads.filter((t) => t.historyMode === "paginated").length,
        legacy: inventoryThreads.filter((t) => t.historyMode === "legacy").length,
        unknownMode: inventoryThreads.filter((t) => t.historyMode == null).length,
      },
    };
    fixture.data.inventory = inventory;
    observe("history_inventory", inventory.counts);

    // ---- 2) discovery: recency head + strided sweep for depth/historyMode
    // coverage (legacy threads cluster in older inventory pages).
    const discovery = [];
    fixture.data.discovery = discovery; // live reference: survives a mid-stage failure
    const pool = inventoryThreads.filter((t) => t.statusType !== "active");
    const head = pool.slice(0, 8);
    const stride = Math.max(1, Math.floor(pool.length / Math.max(1, CAPS.discoveryThreads - head.length)));
    const sweep = pool.filter((_, index) => index % stride === 0).filter((t) => !head.includes(t));
    const candidates = [...head, ...sweep].slice(0, CAPS.discoveryThreads);
    for (const candidate of candidates) {
      const raw = rawFromRef(candidate.thread);
      if (raw == null) continue;
      try {
        const { pages, rawTurnIds } = await walkTurnsPages(session, raw, {
          itemsView: "summary", sortDirection: "desc", limit: CAPS.turnsPageLimit,
          maxPages: CAPS.discoveryPages, note: `discovery|${candidate.thread}`,
        });
        const lastPage = pages[pages.length - 1];
        discovery.push({
          thread: candidate.thread,
          historyMode: candidate.historyMode,
          turnsSeen: rawTurnIds.length,
          pagesWalked: pages.length,
          pageOneHasMore: lastPage?.nextCursor != null,
          reachedEofWithinCap: lastPage != null && lastPage.error !== true && lastPage.nextCursor == null && !lastPage.repeatedCursorDetected,
          error: lastPage?.error === true ? (lastPage.record?.error?.code ?? "error") : null,
          discovery: true,
        });
      } catch (error) {
        // One slow/cold candidate must not kill the sweep (attempt-3 lesson).
        discovery.push({
          thread: candidate.thread,
          historyMode: candidate.historyMode,
          turnsSeen: null,
          pagesWalked: 0,
          pageOneHasMore: null,
          reachedEofWithinCap: false,
          error: redactErrorMessage(error.message) ?? "exception",
          discovery: true,
        });
      }
    }
    // Prefer a thread whose page 1 still has a nextCursor — guaranteed deeper
    // than one page (>30 turns at limit 30), which is exactly the multi-page
    // chain the §3.0.5 checklist needs; ties/fallback by page-1 turns seen.
    discovery.sort((a, b) => (b.pageOneHasMore === true) - (a.pageOneHasMore === true) || (b.turnsSeen ?? 0) - (a.turnsSeen ?? 0));
    observe("discovery", { threads: discovery.length, deepest_turns: discovery[0]?.turnsSeen ?? 0 });

    const longest = discovery[0];
    if (longest == null) throw new Error("no probeable thread discovered");
    const longestRaw = rawFromRef(longest.thread);

    // ---- 3) full battery on the longest thread
    const battery = { thread: longest.thread, historyMode: longest.historyMode, discoveryTurnsSeen: longest.turnsSeen };
    fixture.data.longestThread = battery; // live reference: survives a mid-stage failure

    const metaRead = await callWithLoadRetry(session, "thread/read", { threadId: longestRaw }, { note: "meta-read" });
    battery.readMeta = {
      record: metaRead.record,
      historyMode: metaRead.response.result?.thread?.historyMode ?? null,
      threadFields: Object.keys(metaRead.response.result?.thread ?? {}).sort(),
      turnsPresent: Array.isArray(metaRead.response.result?.thread?.turns),
    };

    const controlEntry = (result) => ({
      record: result.record,
      error: result.response.error != null,
      turnIds: Array.isArray(result.response.result?.thread?.turns)
        ? result.response.result.thread.turns.map((turn) => pseudonym(turn.id) ?? "<unparsed>")
        : null,
      turnSummaries: Array.isArray(result.response.result?.thread?.turns)
        ? result.response.result.thread.turns.map((turn) => ({
            id: pseudonym(turn.id) ?? "<unparsed>",
            itemsView: turn.itemsView ?? null,
            itemTypes: Array.isArray(turn.items) ? turn.items.map((item) => item.type) : null,
            itemCount: Array.isArray(turn.items) ? turn.items.length : null,
          }))
        : null,
    });
    // Cold read: attempts 1+2 established the server never answers cold
    // includeTurns on a paginated thread within 240s; 30s re-records the
    // signature without burning the budget. The warm re-attempt below runs
    // after the pagination chains have loaded the thread.
    battery.controlFullRead = controlEntry(await controlFullRead(session, longestRaw, "control-full-read", 30_000));

    const summaryWalk = await walkTurnsPages(session, longestRaw, {
      itemsView: "summary", sortDirection: "desc", limit: CAPS.turnsPageLimit,
      maxPages: CAPS.turnsChainPages, note: "chain|summary|desc", timeoutMs: CAPS.chainPageTimeoutMs,
    });
    battery.summaryChain = summaryWalk.pages;

    battery.notLoadedPage = (await walkTurnsPages(session, longestRaw, {
      itemsView: "notLoaded", sortDirection: "desc", limit: CAPS.turnsPageLimit, maxPages: 1, note: "notLoaded|desc",
    })).pages;

    // backwards round-trip: the last desc page's backwardsCursor anchors at
    // that page's newest turn (official semantics: opposite-direction cursor
    // includes the anchor again); asc walk returns to the newest turn (EOF).
    const lastChainPage = battery.summaryChain[battery.summaryChain.length - 1];
    const anchorRef = lastChainPage?.backwardsCursor ?? null;
    if (anchorRef == null) {
      battery.backwardsChain = { skipped: "no backwardsCursor on last desc page" };
    } else {
      const rawAnchor = rawCursorFromRef(anchorRef);
      const ascWalk = await walkTurnsPages(session, longestRaw, {
        itemsView: "summary", sortDirection: "asc", rawStartCursor: rawAnchor,
        limit: CAPS.turnsPageLimit, maxPages: CAPS.turnsChainPages, note: "chain|summary|asc", timeoutMs: CAPS.chainPageTimeoutMs,
      });
      battery.backwardsChain = { anchoredOn: anchorRef, pages: ascWalk.pages };
    }

    // items samples: newest / middle / oldest turn of the desc chain
    const chainRawTurnIds = summaryWalk.rawTurnIds;
    const sampleIdx = chainRawTurnIds.length >= 3
      ? [0, Math.floor(chainRawTurnIds.length / 2), chainRawTurnIds.length - 1]
      : chainRawTurnIds.map((_, i) => i);
    battery.itemsSamples = [];
    for (const idx of sampleIdx) {
      const rawTurnId = chainRawTurnIds[idx];
      if (rawTurnId == null) continue;
      const turnRef = pseudonym(rawTurnId);
      battery.itemsSamples.push({
        turn: turnRef,
        positionInChain: idx,
        pages: await walkItemsPages(session, longestRaw, rawTurnId, {
          limit: CAPS.itemsPageLimit, maxPages: CAPS.itemsChainPages, note: `items|${turnRef}`,
        }),
      });
    }

    // Warm re-attempt: the summary/backwards/items chains above have loaded
    // the thread in the app-server, so includeTurns can finally answer.
    battery.controlFullReadWarm = controlEntry(await controlFullRead(session, longestRaw, "control-full-read-warm"));

    const illegal = await call(session, "thread/items/list", { threadId: longestRaw, turnId: CAPS.illegalTurnId, sortDirection: "asc" }, { note: "illegal-turn-id" });
    battery.illegalTurnId = { record: illegal.record, expectation: "empty-success-per-official-filter-semantics" };

    // T0.6 candidate — last on this thread: resume attaches live state.
    const liveBeforeResume = liveSeen.length;
    const candidate = await call(session, "thread/resume", {
      threadId: longestRaw,
      excludeTurns: true,
      initialTurnsPage: { limit: CAPS.turnsPageLimit, sortDirection: "desc", itemsView: "summary" },
    }, { timeoutMs: CAPS.resumeTimeoutMs, note: "t0.6-candidate-resume", slim: true });
    const resumeResult = candidate.response.result ?? {};
    const resumeThread = resumeResult.thread ?? null;
    const initialPage = resumeResult.initialTurnsPage ?? null;
    battery.resumeCandidate = {
      record: candidate.record,
      error: candidate.response.error != null,
      errorCode: candidate.response.error?.code ?? null,
      threadTurnsPresent: Array.isArray(resumeThread?.turns),
      threadTurnsCount: Array.isArray(resumeThread?.turns) ? resumeThread.turns.length : null,
      threadTurnsEmpty: Array.isArray(resumeThread?.turns) ? resumeThread.turns.length === 0 : null,
      initialPagePresent: initialPage != null,
      initialPageTurnIds: Array.isArray(initialPage?.data) ? initialPage.data.map((turn) => pseudonym(turn.id) ?? "<unparsed>") : null,
      initialPageNextCursor: cursorRef(initialPage?.nextCursor ?? null),
      turnsBackwardsCursor: cursorRef(resumeResult.turnsBackwardsCursor ?? null),
      itemsBackwardsCursor: cursorRef(resumeResult.itemsBackwardsCursor ?? null),
      liveMethodsAfterResume: [...new Set(liveSeen.slice(liveBeforeResume))],
    };

    fixture.data.longestThread = battery;

    // ---- 4) secondary threads: historyMode contrast + item-type coverage
    fixture.data.secondaryThreads = [];
    const others = discovery.filter((d) => d.thread !== longest.thread);
    const picked = [];
    for (const wanted of ["paginated", "legacy"]) {
      const pick = others.find((d) => d.historyMode === wanted);
      if (pick != null) picked.push(pick);
    }
    for (const extra of others) {
      if (picked.length >= CAPS.secondaryThreads) break;
      if (!picked.includes(extra)) picked.push(extra);
    }
    for (const pick of picked.slice(0, CAPS.secondaryThreads)) {
      const raw = rawFromRef(pick.thread);
      if (raw == null) continue;
      const entry = { thread: pick.thread, historyMode: pick.historyMode };
      const meta = await callWithLoadRetry(session, "thread/read", { threadId: raw }, { note: `secondary|${pick.thread}|meta` });
      entry.readMeta = { record: meta.record, historyMode: meta.response.result?.thread?.historyMode ?? null, threadFields: Object.keys(meta.response.result?.thread ?? {}).sort() };
      const summaryWalk2 = await walkTurnsPages(session, raw, {
        itemsView: "summary", sortDirection: "desc", limit: CAPS.turnsPageLimit, maxPages: 2, note: `secondary|${pick.thread}|summary`,
      });
      entry.summaryFirstPages = summaryWalk2.pages;
      const firstRawTurn = summaryWalk2.rawTurnIds[0];
      if (firstRawTurn != null) {
        entry.itemsSample = {
          turn: pseudonym(firstRawTurn),
          pages: await walkItemsPages(session, raw, firstRawTurn, { limit: CAPS.itemsPageLimit, maxPages: CAPS.secondaryItemsPages, note: `secondary|${pick.thread}|items` }),
        };
      }
      if (pick.historyMode === "legacy") {
        const controlLegacy = await controlFullRead(session, raw, `legacy|${pick.thread}|control-full-read`);
        entry.controlFullRead = {
          record: controlLegacy.record,
          error: controlLegacy.response.error != null,
          turnCount: Array.isArray(controlLegacy.response.result?.thread?.turns) ? controlLegacy.response.result.thread.turns.length : null,
        };
      }
      fixture.data.secondaryThreads.push(entry);
    }

    const legacyThreads = inventoryThreads.filter((t) => t.historyMode === "legacy");
    fixture.data.legacyBattery = legacyThreads.length === 0
      ? { na: "no-legacy-in-inventory", inventoryEvidence: inventory.counts }
      : { threads: legacyThreads.map((t) => t.thread), coveredIn: "secondaryThreads" };

    await session.close();
  } catch (error) {
    if (session != null) { try { await session.close(); } catch {} }
    // Emit the partial fixture instead of losing every captured section to a
    // mid-battery failure; the harness treats missing sections explicitly.
    fixture.data.batteryError = { message: redactErrorMessage(error?.message ?? "") ?? "unknown" };
    fixture.adjudication.result = "PARTIAL-CAPTURE-FAILED";
    fixture.adjudication.note = "Probe failed mid-battery; sections captured before the failure remain valid evidence. Re-run required for missing sections.";
    observe("battery_failed", { error: fixture.data.batteryError.message });
    process.exitCode = 1;
  } finally {
    try {
      const revoked = await cleanupController(enrollment, observe);
      fixture.metadata.cleanup_result = `probe revoked client_probe ok=${revoked}; probe key deleted; helper temp dir removed`;
      if (revoked && ledgerFile != null) {
        try { fs.rmSync(ledgerFile); } catch { /* already removed */ }
        ledgerFile = null;
      }
    } catch (error) {
      fixture.metadata.cleanup_result = `cleanup error: ${error.message}`;
    }
  }

  fixture.data.liveMethods = [...new Set(liveSeen)];
  fixture.observations = fixtureObservations();
  console.log(`REDACTED_FIXTURE=${JSON.stringify(fixture)}`);
}

withTimeout(main(), 20 * 60_000, "history probe total timeout").catch((error) => {
  observe("probe_failed", {
    error: error.message,
    http_status: error.status ?? null,
    pathname: error.pathname ?? null,
  });
  process.exitCode = 1;
});
