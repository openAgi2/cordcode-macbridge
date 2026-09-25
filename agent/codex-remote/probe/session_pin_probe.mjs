#!/usr/bin/env node

// E-6 runtime probe for docs/2026-09-25-codex-remote-session-pinning-plan.md
// (owner "开工" authorization, 2026-09-25). Evidence question: does the real
// controller WSS surface the upstream native Pinned thread section?
//
//   (i)   threadSection/list succeeds (Desktop assembled the state DB) or
//         returns a decisive unsupported error;
//   (ii)  thread/section/move is relayed to app-server — probed with a
//         synthetic all-zero threadId, expecting a validation/not-found error
//         and therefore ZERO state change;
//   (iii) thread/list {sectionId: PINNED, sortKey: "section_position"} real
//         response shape: accepted sortDirection, cursor shape, and whether
//         rows carry section.id / sectionEnteredAt.
//
// Target: installed ChatGPT Desktop + embedded codex (reported in fixture).
// Human input: possible OAuth step-up / manual pairing via the localhost form
// (10 min each, handled by lib/controller_session.mjs). Network ops 15 s,
// WSS init 30 s, per-RPC 30 s. Origin allowlist: chatgpt.com frozen paths in
// lib/controller_session.mjs. Output: stdout = compact redacted fixture
// (hashed ids, structural field names, counts, timestamps); raw payloads stay
// in memory. Cleanup: revoke only this probe controller → verify rejection →
// delete probe key → remove temp helper (cleanupController in finally).
// Expected failure classes: HTTP 409 single-owner (another controller holds
// the slot — stop, never disconnect it); enroll/OAuth abort; unsupported
// thread-section errors (that is a decisive E-6 verdict, not a probe failure).
// Evidence artifact: stdout fixture + stderr event stream, archived by the
// operator under testdata/phase0/ per repo convention.

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

const helperSource = new URL("./device_key_helper.swift", import.meta.url).pathname;
const startedAt = Date.now();
const observations = [];
const PINNED_THREAD_SECTION_ID = "01984de2-8f74-7c91-a3b2-5c5e937cf318";
const SYNTHETIC_THREAD_ID = "00000000-0000-0000-0000-000000000000";

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
  const codex = spawnSync("/Applications/ChatGPT.app/Contents/Resources/codex", ["--version"], { encoding: "utf8" }).stdout.trim();
  return {
    desktopVersion: read(["read", plist, "CFBundleShortVersionString"]),
    bundleVersion: read(["read", plist, "CFBundleVersion"]),
    embeddedCodexVersion: codex,
  };
}

function errorShape(response) {
  if (response.error == null) return null;
  const code = response.error.code;
  const message = typeof response.error.message === "string" ? response.error.message : "";
  return {
    code: typeof code === "string" || typeof code === "number" ? code : null,
    messageLength: message.length,
    // Classify only; never echo the raw upstream message (redaction boundary).
    messageClass: /not found|unknown thread|no thread/i.test(message)
      ? "thread-not-found"
      : /invalid|bad request|parse/i.test(message)
        ? "invalid-params"
        : /unsupported|not supported|unavailable|feature/i.test(message)
          ? "unsupported"
          : "other",
  };
}

async function call(session, method, params) {
  const response = await session.rpc(method, params, { timeoutMs: 30_000 });
  observe("rpc", {
    method,
    requestKeys: Object.keys(params ?? {}).sort(),
    ok: response.error == null,
    error: errorShape(response),
  });
  return response;
}

function rowShapes(response) {
  const rows = response?.result?.threads ?? response?.result?.items ?? [];
  return rows.slice(0, 20).map((row) => ({
    ref: ref(row.id),
    hasSection: row.section != null,
    sectionIdIsPinned: row.section?.id === PINNED_THREAD_SECTION_ID,
    sectionNamePresent: typeof row.section?.name === "string",
    sectionEnteredAt: typeof row.sectionEnteredAt === "number" ? row.sectionEnteredAt : null,
  }));
}

const fixture = {
  probe: "session_pin_probe",
  planGate: "E-6",
  target: installedTarget(),
  steps: {},
  cleanup: {},
};

let enrollment = null;
let session = null;

try {
  enrollment = await enrollController({ helperSource, observe });
  const environments = await pairedEnvironments(
    enrollment.token,
    enrollment.accountID,
    enrollment.clientID,
    () => collectManualPairingCode(
      "Codex session pin probe pairing",
      "<p>Use a fresh Desktop Remote Control pairing code. The probe never prints or stores it.</p>",
    ),
  );
  const selected = selectEnvironment(environments.body.items);
  observe("environment", { count: environments.body.items.length, selectedKind: selected.environment?.client_type ?? null });
  session = await openRpcSession({ enrollment, environment: selected.environment, observe });

  // (i) threadSection/list — does the section store exist behind this controller?
  {
    const response = await call(session, "threadSection/list", {});
    fixture.steps.threadSectionList = {
      ok: response.error == null,
      error: errorShape(response),
      sectionCount: response?.result?.sections?.length ?? null,
      pinnedSectionPresent:
        (response?.result?.sections ?? []).some((s) => s.id === PINNED_THREAD_SECTION_ID) ?? null,
    };
  }

  // (ii) thread/section/move with a synthetic threadId — relay pass-through
  // proof. Expected: error (thread not found / invalid), zero state change.
  {
    const response = await call(session, "thread/section/move", {
      threadId: SYNTHETIC_THREAD_ID,
      sectionId: PINNED_THREAD_SECTION_ID,
    });
    fixture.steps.sectionMoveSynthetic = {
      ok: response.error == null,
      error: errorShape(response),
      // ok=true for an all-zero UUID would be suspicious; surface it loudly.
      zeroStateChangeExpected: response.error != null,
    };
  }

  // (iii) thread/list filtered to the Pinned section, section_position order.
  // Try "asc" first (official UI top-to-bottom); on sort rejection retry "desc".
  {
    const base = { sectionId: PINNED_THREAD_SECTION_ID, sortKey: "section_position", limit: 20 };
    const asc = await call(session, "thread/list", { ...base, sortDirection: "asc" });
    let usedDirection = "asc";
    let response = asc;
    if (asc.error != null && errorShape(asc)?.messageClass === "invalid-params") {
      usedDirection = "desc";
      response = await call(session, "thread/list", { ...base, sortDirection: "desc" });
    }
    const cursor = response?.result?.nextCursor ?? response?.result?.cursor ?? null;
    fixture.steps.pinnedThreadList = {
      ok: response.error == null,
      error: errorShape(response),
      sortDirectionUsed: usedDirection,
      rowCount: (response?.result?.threads ?? response?.result?.items ?? []).length,
      cursorShape: cursor == null ? "absent" : typeof cursor === "string" ? `string(${cursor.length})` : typeof cursor,
      rows: rowShapes(response),
    };
  }
} catch (error) {
  fixture.steps.fatal = { name: error?.name ?? "Error", messageLength: String(error?.message ?? "").length };
  observe("fatal", { name: error?.name ?? "Error" });
} finally {
  if (enrollment != null) {
    fixture.cleanup.probeControllerRevoked = await cleanupController(enrollment, observe);
  }
  observe("done", { elapsedMs: Date.now() - startedAt });
  process.stdout.write(`${JSON.stringify(fixture, null, 2)}\n`);
}
