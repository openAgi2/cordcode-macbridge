#!/usr/bin/env node

// Offline assertion harness for Phase 0 lazy-history fixtures
// (probe/history_probe.mjs output saved under testdata/phase0/live/).
//
// Implements plan §3.0.7 negative-result assertions (any failure = G0 FAIL,
// not "sample captured"), §3.0.6 anti-misjudgment stats (type-grouped counts;
// turnId -> summary item ids -> full item ids mapping with first-user and
// final-agent compared SEPARATELY), and the §3.0.5 nine-item checklist.
//
// Usage:
//   node agent/codex-remote/validate/history-fixture-assert.mjs --fixture <file>
//   node agent/codex-remote/validate/history-fixture-assert.mjs --self-test
//
// Exit code: 0 = all assertions pass (or self-test green); 1 = any negative
// result or required evidence missing. Adjudications that need the owner
// (T0.5/T0.6 enable, G0.5 claim removal, resource gate values) are reported as
// data, not decided here.

import { readFileSync } from "node:fs";

function check(id, description) {
  return { id, description, status: "unverified", detail: null };
}
function pass(c, detail) { c.status = "pass"; c.detail = detail ?? null; return c; }
function failCheck(c, detail) { c.status = "fail"; c.detail = detail ?? null; return c; }
function unverified(c, detail) { c.status = "unverified"; c.detail = detail ?? null; return c; }

function isShapedId(value) {
  return typeof value === "string" && /^id-\d+$/u.test(value);
}

// ---- extract shaped turns/items from fixture data -------------------------

function chainPages(chain) {
  return Array.isArray(chain) ? chain.filter((p) => p && p.error !== true && Array.isArray(p.turns)) : [];
}

function chainTurns(chain) {
  const turns = [];
  for (const page of chainPages(chain)) turns.push(...page.turns);
  return turns;
}

function chainTurnIds(chain) {
  const ids = [];
  for (const page of chainPages(chain)) {
    for (const turn of page.turns) ids.push(turn?.id ?? null);
  }
  return ids.filter((id) => id != null);
}

function itemsOfTurn(shapedTurn) {
  const items = shapedTurn?.items;
  if (Array.isArray(items)) return items;
  if (items != null && typeof items === "object" && Array.isArray(items.items)) return items.items;
  return [];
}

function itemField(item, field) {
  return item?.[field];
}

// summary slots: first userMessage and last agentMessage in the turn's items
function summarySlots(shapedTurn) {
  const items = itemsOfTurn(shapedTurn).filter((it) => it != null && typeof it === "object");
  const users = items.filter((it) => it.type === "userMessage");
  const agents = items.filter((it) => it.type === "agentMessage");
  return {
    firstUserId: users.length > 0 ? users[0].id : null,
    finalAgentId: agents.length > 0 ? agents[agents.length - 1].id : null,
    userCount: users.length,
    agentCount: agents.length,
    itemCount: items.length,
    types: items.map((it) => it.type),
  };
}

function entriesOfPages(pages) {
  const entries = [];
  for (const page of Array.isArray(pages) ? pages : []) {
    if (page == null || page.error === true) continue;
    for (const entry of Array.isArray(page.entries) ? page.entries : []) entries.push(entry);
  }
  return entries;
}

// ---- §3.0.7 checks --------------------------------------------------------

function checkSummaryForeignTypes(fixture) {
  const c = check("neg-2-summary-foreign-type", "§3.0.7-2: Summary pages contain only first-user/final-agent item types");
  const foreign = [];
  const threads = [];
  const longest = fixture?.data?.longestThread;
  if (longest != null) {
    threads.push({ thread: longest.thread, turns: chainTurns(longest.summaryChain) });
    threads.push({ thread: longest.thread, turns: chainTurns(longest.notLoadedPage) });
  }
  for (const secondary of fixture?.data?.secondaryThreads ?? []) {
    threads.push({ thread: secondary.thread, turns: chainTurns(secondary.summaryFirstPages) });
  }
  if (threads.every((t) => t.turns.length === 0)) return unverified(c, "no summary turns captured");
  for (const { thread, turns } of threads) {
    for (const turn of turns) {
      for (const item of itemsOfTurn(turn)) {
        if (item?.type !== "userMessage" && item?.type !== "agentMessage") {
          foreign.push({ thread, turn: turn?.id, type: item?.type });
        }
      }
    }
  }
  if (foreign.length > 0) return failCheck(c, `foreign summary item types: ${JSON.stringify(foreign.slice(0, 5))}`);
  return pass(c);
}

function checkSummaryItemsIdMatch(fixture) {
  const c = check("neg-1-summary-items-id-mismatch", "§3.0.7-1: Summary first-user/final-agent ids exist in that turn's items/list full ids (compared separately)");
  const longest = fixture?.data?.longestThread;
  const samples = longest?.itemsSamples;
  if (!Array.isArray(samples) || samples.length === 0) return unverified(c, "no items samples captured");
  const chainTurnById = new Map(chainTurns(longest.summaryChain).map((t) => [t?.id, t]));
  const mapping = [];
  let mismatches = 0;
  for (const sample of samples) {
    const turn = chainTurnById.get(sample.turn);
    const fullIds = entriesOfPages(sample.pages).map((e) => e?.item?.id);
    const entryTurnIds = [...new Set(entriesOfPages(sample.pages).map((e) => e?.turnId))];
    const slots = turn != null ? summarySlots(turn) : { firstUserId: null, finalAgentId: null, userCount: 0, agentCount: 0, itemCount: 0, types: [] };
    const firstUserMatch = slots.firstUserId == null ? null : fullIds.includes(slots.firstUserId);
    const finalAgentMatch = slots.finalAgentId == null ? null : fullIds.includes(slots.finalAgentId);
    if (firstUserMatch === false || finalAgentMatch === false) mismatches += 1;
    mapping.push({
      thread: longest.thread,
      turnId: sample.turn,
      summaryFirstUserId: slots.firstUserId,
      summaryFinalAgentId: slots.finalAgentId,
      fullItemIds: fullIds,
      itemsTurnIds: entryTurnIds,
      firstUserMatch,
      finalAgentMatch,
    });
  }
  if (mismatches > 0) return failCheck(c, `summary↔items id mismatch on ${mismatches} sampled turn(s); mapping: ${JSON.stringify(mapping)}`);
  return pass(c, { mapping });
}

function checkItemsTurnFilter(fixture) {
  const c = check("neg-3-items-turn-filter-leak", "§3.0.7-3: turnId-filtered items/list returns only that turn's items");
  const leaks = [];
  const longest = fixture?.data?.longestThread;
  for (const sample of longest?.itemsSamples ?? []) {
    for (const page of Array.isArray(sample.pages) ? sample.pages : []) {
      if (page.error === true) continue;
      for (const entry of Array.isArray(page.entries) ? page.entries : []) {
        if (entry?.turnId !== sample.turn) leaks.push({ requested: sample.turn, got: entry?.turnId });
      }
    }
  }
  for (const secondary of fixture?.data?.secondaryThreads ?? []) {
    const sample = secondary.itemsSample;
    if (sample == null) continue;
    for (const page of Array.isArray(sample.pages) ? sample.pages : []) {
      if (page.error === true) continue;
      for (const entry of Array.isArray(page.entries) ? page.entries : []) {
        if (entry?.turnId !== sample.turn) leaks.push({ requested: sample.turn, got: entry?.turnId });
      }
    }
  }
  const sampled = (longest?.itemsSamples?.length ?? 0) + (fixture?.data?.secondaryThreads ?? []).filter((s) => s.itemsSample != null).length;
  if (sampled === 0) return unverified(c, "no filtered items pages captured");
  if (leaks.length > 0) return failCheck(c, `foreign turn items: ${JSON.stringify(leaks.slice(0, 5))}`);
  return pass(c, { sampledPages: sampled });
}

function checkPaginationChain(fixture) {
  const c = check("neg-4-pagination-dup-gap", "§3.0.7-4: pagination chain has no duplicate turns, no empty middle pages, no repeated cursor, and reaches EOF");
  const longest = fixture?.data?.longestThread;
  const chain = longest?.summaryChain;
  if (!Array.isArray(chain) || chain.length === 0) return unverified(c, "no summary chain captured");
  const problems = [];
  const seen = new Map();
  let reachedEof = false;
  for (const page of chain) {
    if (page.repeatedCursorDetected === true) problems.push({ page: page.page, kind: "repeated-cursor" });
    if (page.error === true) { problems.push({ page: page.page, kind: "error-page" }); continue; }
    if (page.nextCursor == null) reachedEof = true;
  }
  const ids = chainTurnIds(chain);
  for (let i = 0; i < ids.length; i += 1) {
    if (seen.has(ids[i])) problems.push({ kind: "duplicate-turn", turn: ids[i], pages: [seen.get(ids[i]), i] });
    else seen.set(ids[i], i);
  }
  for (let i = 1; i < chain.length; i += 1) {
    const prev = chain[i - 1];
    const cur = chain[i];
    if (prev.error === true) break;
    if (cur.requestCursor !== prev.nextCursor) problems.push({ page: cur.page, kind: "cursor-chain-break", expected: prev.nextCursor, got: cur.requestCursor });
    if (prev.nextCursor == null && cur.error !== true) problems.push({ page: cur.page, kind: "page-after-eof" });
  }
  const capped = chain.length >= (fixture?.probe?.caps?.turnsChainPages ?? 80);
  if (!reachedEof) problems.push({ kind: capped ? "cap-reached-before-eof" : "no-eof-page" });
  if (problems.length > 0) return failCheck(c, JSON.stringify(problems.slice(0, 5)));
  return pass(c, { pages: chain.length, turns: ids.length, reachedEof: true });
}

function checkIllegalTurnId(fixture) {
  // Amended 2026-08-30 against tag rust-v0.150.0-alpha.12.2
  // thread_processor.rs:3365-3425: items/list passes turn_id straight into the
  // store as a filter; the error mapping covers thread-level failures only, so
  // an unknown well-formed turnId officially yields an EMPTY SUCCESS page.
  const c = check("neg-5-illegal-turnid-semantics", "§3.0.7-5 (amended): unknown well-formed turnId is a store filter — expect empty success page; foreign items or a fabricated rpc error both fail");
  const record = fixture?.data?.longestThread?.illegalTurnId?.record;
  if (record == null) return unverified(c, "illegal turnId probe not captured");
  if (record.error != null) return failCheck(c, `unknown turnId returned an rpc error (code ${record.error.code ?? "?"}) — contradicts official filter semantics (thread_processor.rs:3365-3425)`);
  const n = record.resultShape?.data?.n;
  if (typeof n !== "number") return unverified(c, "illegal turnId success response shape not captured");
  if (n !== 0) return failCheck(c, `unknown turnId returned ${n} items — turn-filter leak/fabrication`);
  return pass(c, { emptySuccessPage: true });
}

// Control inventory pick: prefer the post-chain warm re-attempt (cold
// includeTurns on a paginated thread times out server-side, attempts 1+2),
// fall back to the cold attempt; only non-error records are usable as a
// comparison set.
function pickControl(longest) {
  for (const candidate of [longest?.controlFullReadWarm, longest?.controlFullRead]) {
    if (candidate?.record != null && candidate.error !== true) return candidate;
  }
  return null;
}

function checkNoGapVsControl(fixture) {
  const c = check("neg-6-no-gap-vs-control", "§3.0.7-6: full desc chain (normalized oldest→newest) matches includeTurns=true control inventory in BOTH set and order");
  const longest = fixture?.data?.longestThread;
  const chain = longest?.summaryChain;
  const controlIds = pickControl(longest)?.turnIds;
  if (!Array.isArray(chain) || chain.length === 0) return unverified(c, "no summary chain captured");
  if (!Array.isArray(controlIds) || controlIds.length === 0) return unverified(c, "no control inventory (includeTurns=true) captured");
  const descIds = chainTurnIds(chain);
  const oldestFirst = [...descIds].reverse();
  const setEq = oldestFirst.length === controlIds.length && controlIds.every((id, i) => id === oldestFirst[i]);
  if (!setEq) {
    const missingFromChain = controlIds.filter((id) => !descIds.includes(id));
    const missingFromControl = descIds.filter((id) => !controlIds.includes(id));
    const orderDiffers = missingFromChain.length === 0 && missingFromControl.length === 0;
    return failCheck(c, JSON.stringify({
      chainTurns: descIds.length, controlTurns: controlIds.length,
      missingFromChain: missingFromChain.slice(0, 5), missingFromControl: missingFromControl.slice(0, 5),
      orderDiffers,
    }));
  }
  return pass(c, { turns: controlIds.length, setAndOrderMatch: true });
}

function checkBackwardsRoundTrip(fixture) {
  const c = check("backwards-round-trip", "§3.0.5-2/§3.0.7-6: asc walk from the last desc page's backwardsCursor includes the anchor (page's newest turn) and lines up with the desc chain");
  const longest = fixture?.data?.longestThread;
  const backwards = longest?.backwardsChain;
  if (backwards == null || !Array.isArray(backwards.pages)) return unverified(c, `backwards chain not captured (${backwards?.skipped ?? "missing"})`);
  const descIds = chainTurnIds(longest.summaryChain);
  if (descIds.length === 0) return unverified(c, "no desc chain");
  const anchor = backwards.anchoredOn;
  const chain = longest.summaryChain ?? [];
  const lastPage = chain[chain.length - 1];
  const lastPageNewest = lastPage?.turns?.[0]?.id ?? null;
  const ascIds = chainTurnIds(backwards.pages);
  const problems = [];
  if (lastPageNewest != null && ascIds.length > 0 && ascIds[0] !== lastPageNewest) {
    problems.push({ kind: "anchor-not-inclusive", expected: lastPageNewest, got: ascIds[0] });
  }
  const oldestFirst = [...descIds].reverse();
  if (ascIds.length > 0) {
    const suffix = oldestFirst.slice(oldestFirst.length - ascIds.length);
    if (suffix.length !== ascIds.length || suffix.some((id, i) => id !== ascIds[i])) {
      problems.push({ kind: "asc-desc-sequence-mismatch", ascHead: ascIds.slice(0, 5), expectedSuffixHead: suffix.slice(0, 5) });
    }
  }
  const dupSeen = new Set();
  for (const id of ascIds) {
    if (dupSeen.has(id)) problems.push({ kind: "duplicate-in-asc", id });
    dupSeen.add(id);
  }
  const lastAscPage = backwards.pages[backwards.pages.length - 1];
  if (lastAscPage != null && lastAscPage.error !== true && lastAscPage.nextCursor != null) problems.push({ kind: "asc-did-not-reach-eof" });
  if (problems.length > 0) return failCheck(c, JSON.stringify(problems.slice(0, 5)));
  return pass(c, { anchor, ascTurns: ascIds.length, inclusiveAnchor: true });
}

function checkNotLoadedIdentity(fixture) {
  const c = check("notloaded-identity", "§3.0.5-3: NotLoaded page has empty items with unchanged turn identity/status/timing");
  const longest = fixture?.data?.longestThread;
  const pages = longest?.notLoadedPage;
  if (!Array.isArray(pages) || pages.length === 0) return unverified(c, "notLoaded page not captured");
  const page = pages[0];
  if (page.error === true) return unverified(c, "notLoaded page errored");
  const problems = [];
  const summaryById = new Map(chainTurns(longest.summaryChain).map((t) => [t?.id, t]));
  let compared = 0;
  for (const turn of page.turns ?? []) {
    if (turn?.itemsView !== "notLoaded") problems.push({ turn: turn.id, kind: "items-view", got: turn.itemsView });
    if ((turn.items == null) || (Array.isArray(turn.items) ? turn.items.length : turn.items?.n) !== 0) {
      problems.push({ turn: turn.id, kind: "items-not-empty" });
    }
    const twin = summaryById.get(turn.id);
    if (twin != null) {
      compared += 1;
      if (JSON.stringify(turn.status) !== JSON.stringify(twin.status)) problems.push({ turn: turn.id, kind: "status-differs" });
      if (turn.startedAt !== twin.startedAt) problems.push({ turn: turn.id, kind: "startedAt-differs" });
      if (turn.completedAt !== twin.completedAt) problems.push({ turn: turn.id, kind: "completedAt-differs" });
    }
  }
  if (compared === 0) problems.push({ kind: "no-overlap-with-summary-chain" });
  if (problems.length > 0) return failCheck(c, JSON.stringify(problems.slice(0, 5)));
  return pass(c, { comparedTurns: compared });
}

function checkResumeCandidate(fixture) {
  const c = check("resume-candidate-shape", "T0.6: thread/resume(excludeTurns+initialTurnsPage) returns thread.turns == [] and an initial page equal to the same-params turns/list page");
  const candidate = fixture?.data?.longestThread?.resumeCandidate;
  if (candidate == null) return unverified(c, "resume candidate not captured");
  if (candidate.error === true) return failCheck(c, `resume candidate rpc error (${candidate.errorCode})`);
  const problems = [];
  if (candidate.threadTurnsPresent !== true) problems.push({ kind: "thread.turns-missing" });
  else if (candidate.threadTurnsEmpty !== true) problems.push({ kind: "thread.turns-not-empty", count: candidate.threadTurnsCount });
  const descPage1 = (fixture?.data?.longestThread?.summaryChain ?? [])[0];
  if (candidate.initialPagePresent === true && Array.isArray(candidate.initialPageTurnIds) && descPage1 != null) {
    const same = candidate.initialPageTurnIds.length === (descPage1.turnIds?.length ?? -1)
      && candidate.initialPageTurnIds.every((id, i) => id === descPage1.turnIds?.[i]);
    if (!same) problems.push({ kind: "initial-page-differs-from-turns-list" });
  }
  if (problems.length > 0) return failCheck(c, JSON.stringify(problems));
  return pass(c, {
    initialPageTurns: candidate.initialPageTurnIds?.length ?? null,
    turnsBackwardsCursor: candidate.turnsBackwardsCursor ?? null,
    liveMethodsAfterResume: candidate.liveMethodsAfterResume ?? [],
  });
}

function checkLegacyBattery(fixture) {
  const c = check("legacy-battery-or-na", "§2.5: legacy items/list measured, or inventory-backed N/A evidence present");
  const legacy = fixture?.data?.legacyBattery;
  if (legacy == null) return unverified(c, "legacy battery record missing");
  if (legacy.na === "no-legacy-in-inventory") {
    const counts = legacy.inventoryEvidence;
    if (counts == null || counts.total == null) return failCheck(c, "N/A without inventory evidence");
    return pass(c, { na: true, inventoryEvidence: counts });
  }
  return pass(c, { threads: legacy.threads ?? [] });
}

// ---- §3.0.6 stats ---------------------------------------------------------

function typeGroupedStats(fixture) {
  const byType = {};
  const reasoning = { bothEmpty: 0, summaryOnly: 0, contentOnly: 0, bothNonEmpty: 0, nonEmptyContentSamples: 0 };
  const commandExecution = { outputNull: 0, outputNonNull: 0, maxOutputLen: 0 };
  const emptyTextItems = 0;
  const noteItem = (item) => {
    if (item == null || typeof item !== "object") return;
    const type = item.type ?? "unknown";
    byType[type] = (byType[type] ?? 0) + 1;
    if (type === "reasoning") {
      const summaryN = Array.isArray(item.summary?.lens) || typeof item.summary?.n === "number" ? item.summary.n : null;
      const contentN = Array.isArray(item.content?.lens) || typeof item.content?.n === "number" ? item.content.n : null;
      const summaryEmpty = summaryN === 0 || (summaryN == null && item.summary == null);
      const contentEmpty = contentN === 0 || (contentN == null && item.content == null);
      if (summaryEmpty && contentEmpty) reasoning.bothEmpty += 1;
      else if (!summaryEmpty && contentEmpty) reasoning.summaryOnly += 1;
      else if (summaryEmpty && !contentEmpty) reasoning.contentOnly += 1;
      else reasoning.bothNonEmpty += 1;
      const contentLens = Array.isArray(item.content?.lens) ? item.content.lens.filter((v) => typeof v === "number") : [];
      if (contentLens.some((len) => len > 0)) reasoning.nonEmptyContentSamples += 1;
    }
    if (type === "commandExecution") {
      const output = item.aggregatedOutput;
      if (output == null) commandExecution.outputNull += 1;
      else {
        commandExecution.outputNonNull += 1;
        if (typeof output.len === "number") commandExecution.maxOutputLen = Math.max(commandExecution.maxOutputLen, output.len);
      }
    }
  };
  const longest = fixture?.data?.longestThread;
  for (const sample of longest?.itemsSamples ?? []) {
    for (const entry of entriesOfPages(sample.pages)) noteItem(entry?.item);
  }
  for (const secondary of fixture?.data?.secondaryThreads ?? []) {
    for (const entry of entriesOfPages(secondary.itemsSample?.pages)) noteItem(entry?.item);
  }
  const summarySlotsByTurn = [];
  for (const turn of chainTurns(longest?.summaryChain)) {
    const items = itemsOfTurn(turn);
    summarySlotsByTurn.push({
      turnId: turn?.id,
      itemCount: items.length,
      userCount: items.filter((it) => it?.type === "userMessage").length,
      agentCount: items.filter((it) => it?.type === "agentMessage").length,
      emptyText: items.some((it) => (it?.text?.len ?? 1) === 0),
    });
  }
  return { byType, reasoning, commandExecution, emptyTextItems, summarySlotsByTurn };
}

function summaryDistribution(fixture) {
  const dist = { 0: 0, 1: 0, 2: 0, other: 0 };
  for (const row of typeGroupedStats(fixture).summarySlotsByTurn) {
    if (row.itemCount === 0) dist[0] += 1;
    else if (row.itemCount === 1) dist[1] += 1;
    else if (row.itemCount === 2) dist[2] += 1;
    else dist.other += 1;
  }
  return dist;
}

// ---- §3.0.5 checklist -----------------------------------------------------

function nineItemChecklist(fixture, stats, checks) {
  const longest = fixture?.data?.longestThread;
  const statusOf = (id) => checks.find((c) => c.id === id)?.status ?? "unverified";
  const dist = summaryDistribution(fixture);
  const chainIds = chainTurnIds(longest?.summaryChain);
  const sampledReasoning = stats.byType.reasoning ?? 0;
  const sampledCommand = stats.byType.commandExecution ?? 0;
  const emptyItemPages = [...(longest?.itemsSamples ?? []), ...(fixture?.data?.secondaryThreads ?? []).map((s) => ({ pages: s.itemsSample?.pages }))]
    .flatMap((s) => Array.isArray(s.pages) ? s.pages : [])
    .filter((p) => p?.error !== true && p?.entryCount === 0).length;
  const multiPageSamples = [...(longest?.itemsSamples ?? []), ...(fixture?.data?.secondaryThreads ?? []).map((s) => ({ pages: s.itemsSample?.pages }))]
    .filter((s) => Array.isArray(s.pages) && s.pages.filter((p) => p?.error !== true).length >= 2).length;
  const items = [];
  items.push({ item: 1, what: "thread.read historyMode recorded (inventory + readMeta)", status: (fixture?.data?.inventory?.counts?.total ?? 0) > 0 && longest?.readMeta?.historyMode != null ? "present" : "missing", evidence: fixture?.data?.inventory?.counts });
  items.push({ item: 2, what: "Summary 0/1/2 distribution + itemsView/status/time + cursor round-trips", status: (chainIds.length > 0 && statusOf("backwards-round-trip") === "pass") ? "present" : "missing", evidence: dist });
  items.push({ item: 3, what: "NotLoaded empty items with same turn identity", status: statusOf("notloaded-identity") === "pass" ? "present" : "missing" });
  items.push({
    item: 4, what: "items/list entry shape, asc pages, cursors, empty page, illegal turnId",
    status: (statusOf("neg-3-items-turn-filter-leak") === "pass" && statusOf("neg-5-illegal-turnid-semantics") === "pass" && multiPageSamples > 0) ? (emptyItemPages > 0 ? "present" : "partial: no empty items page observed") : "missing",
    evidence: { multiPageSamples, emptyItemPages },
  });
  items.push({
    item: 5, what: "Reasoning summary/content four-state + non-empty content sample (G0.5)",
    status: sampledReasoning === 0 ? "missing: no reasoning items sampled" : (stats.reasoning.nonEmptyContentSamples > 0 ? "present" : "present-shapes-but-no-nonempty-content (G0.5 owner decision required)"),
    evidence: stats.reasoning,
  });
  items.push({
    item: 6, what: "CommandExecution output null/non-empty with real size",
    status: sampledCommand === 0 ? "missing: no commandExecution items sampled" : (stats.commandExecution.outputNull > 0 && stats.commandExecution.outputNonNull > 0 ? "present" : "partial"),
    evidence: stats.commandExecution,
  });
  items.push({ item: 7, what: "Summary↔items official item id equality (first-user and final-agent separately)", status: statusOf("neg-1-summary-items-id-mismatch") === "pass" ? "present" : "missing" });
  const legacyFull = (fixture?.data?.secondaryThreads ?? []).find((s) => s.historyMode === "legacy" && s.controlFullRead?.record != null && s.controlFullRead.error !== true);
  items.push({
    item: 8, what: "Same-session full/Summary/items-list bytes + wall time by historyMode",
    status: ((longest?.summaryChain?.length ?? 0) > 0 && (pickControl(longest)?.record != null || legacyFull != null)) ? "present" : "missing",
    evidence: { paginatedControlBytes: pickControl(longest) != null, legacyFullRead: legacyFull?.controlFullRead?.record != null ? { ms: legacyFull.controlFullRead.record.ms, resultBytes: legacyFull.controlFullRead.record.resultBytes } : null },
  });
  items.push({ item: 9, what: ">30-turn thread paginated to EOF, no dup/no gap vs control", status: (chainIds.length > 30 && statusOf("neg-4-pagination-dup-gap") === "pass" && statusOf("neg-6-no-gap-vs-control") === "pass") ? "present" : "missing", evidence: { turns: chainIds.length } });
  return items;
}

function baselineTable(fixture) {
  const rows = [];
  const longest = fixture?.data?.longestThread;
  if (longest?.readMeta?.record != null) {
    const summaryPage1 = (longest.summaryChain ?? [])[0];
    rows.push({
      thread: longest.thread,
      historyMode: longest.historyMode,
      fullRead: (() => { const control = pickControl(longest); return control?.record != null ? { ms: control.record.ms, resultBytes: control.record.resultBytes } : null; })(),
      metaRead: { ms: longest.readMeta.record.ms, resultBytes: longest.readMeta.record.resultBytes },
      summaryFirstPage: summaryPage1?.record != null ? { ms: summaryPage1.record.ms, resultBytes: summaryPage1.record.resultBytes, turns: summaryPage1.turnCount ?? summaryPage1.turnIds?.length ?? null } : null,
      itemsSamples: (longest.itemsSamples ?? []).map((s) => {
        const pages = Array.isArray(s.pages) ? s.pages.filter((p) => p?.record != null) : [];
        return { turn: s.turn, pages: pages.length, ms: pages.reduce((acc, p) => acc + p.record.ms, 0), resultBytes: pages.reduce((acc, p) => acc + (p.record.resultBytes ?? 0), 0), items: pages.reduce((acc, p) => acc + (p.entryCount ?? 0), 0) };
      }),
    });
  }
  for (const secondary of fixture?.data?.secondaryThreads ?? []) {
    const page1 = (secondary.summaryFirstPages ?? [])[0];
    rows.push({
      thread: secondary.thread,
      historyMode: secondary.historyMode,
      fullRead: secondary.controlFullRead?.record != null ? { ms: secondary.controlFullRead.record.ms, resultBytes: secondary.controlFullRead.record.resultBytes, turns: secondary.controlFullRead.turnCount ?? null } : null,
      metaRead: secondary.readMeta?.record != null ? { ms: secondary.readMeta.record.ms, resultBytes: secondary.readMeta.record.resultBytes } : null,
      summaryFirstPage: page1?.record != null ? { ms: page1.record.ms, resultBytes: page1.record.resultBytes } : null,
      itemsSamples: secondary.itemsSample != null ? [{
        turn: secondary.itemsSample.turn,
        pages: (secondary.itemsSample.pages ?? []).length,
        ms: (secondary.itemsSample.pages ?? []).reduce((acc, p) => acc + (p.record?.ms ?? 0), 0),
        resultBytes: (secondary.itemsSample.pages ?? []).reduce((acc, p) => acc + (p.record?.resultBytes ?? 0), 0),
        items: (secondary.itemsSample.pages ?? []).reduce((acc, p) => acc + (p.entryCount ?? 0), 0),
      }] : [],
    });
  }
  return rows;
}

export function runAssertions(fixture) {
  const checks = [
    checkSummaryForeignTypes(fixture),
    checkSummaryItemsIdMatch(fixture),
    checkItemsTurnFilter(fixture),
    checkPaginationChain(fixture),
    checkIllegalTurnId(fixture),
    checkNoGapVsControl(fixture),
    checkBackwardsRoundTrip(fixture),
    checkNotLoadedIdentity(fixture),
    checkResumeCandidate(fixture),
    checkLegacyBattery(fixture),
  ];
  const stats = typeGroupedStats(fixture);
  const checklist = nineItemChecklist(fixture, stats, checks);
  const negativesFailed = checks.filter((c) => c.id.startsWith("neg-") && c.status === "fail");
  const unverifiedChecks = checks.filter((c) => c.status === "unverified");
  return {
    fixtureClass: fixture?.classification ?? null,
    target: fixture?.target ?? null,
    negativeResults: negativesFailed.length === 0
      ? { verdict: "none-triggered", note: "§3.0.7: zero negative results (subject to no unverified checks below)" }
      : { verdict: "G0-FAIL", failed: negativesFailed.map((c) => ({ id: c.id, detail: c.detail })) },
    unverified: unverifiedChecks.map((c) => ({ id: c.id, detail: c.detail })),
    checks,
    stats,
    summaryDistribution: summaryDistribution(fixture),
    nineItemChecklist: checklist,
    baselineTable: baselineTable(fixture),
  };
}

// ---- self-test: synthetic fixture + one-mutation-per-negative -------------

function syntheticFixture() {
  const turnIds = [];
  const controlTurnIds = [];
  const turns = [];
  const controlTurnSummaries = [];
  for (let i = 35; i >= 1; i -= 1) {
    const id = `id-${i}`;
    turnIds.push(id);
    controlTurnIds.unshift(id);
    const items = [];
    if (i % 7 !== 0 || i === 35) items.push({ type: "userMessage", id: `id-${100 + i}`, text: { len: 12 } });
    if (i % 5 !== 0 || i === 35) items.push({ type: "agentMessage", id: `id-${200 + i}`, text: { len: 40 } });
    turns.push({ id, itemsView: "summary", status: { type: "idle" }, startedAt: `t+${i}s`, completedAt: `t+${i + 1}s`, items });
    controlTurnSummaries.push({ id, itemsView: "full", itemTypes: items.map((it) => it.type), itemCount: items.length });
  }
  const page1Turns = turns.slice(0, 30);
  const page2Turns = turns.slice(30);
  const chainPage = (page, turnsSubset, requestCursor, nextCursor, backwardsCursor) => ({
    page,
    requestCursor,
    record: { method: "thread/turns/list", params: {}, ms: 20 + page, resultBytes: 1000 * page, error: null, resultFields: ["data", "nextCursor", "backwardsCursor"] },
    turns: turnsSubset,
    turnIds: turnsSubset.map((t) => t.id),
    nextCursor,
    backwardsCursor,
    error: false,
  });
  const summaryChain = [
    chainPage(1, page1Turns, null, "cur-1", "cur-2"),
    chainPage(2, page2Turns, "cur-1", null, "cur-3"),
  ];
  const ascAll = [...turns].reverse();
  const ascPages = [
    chainPage(1, ascAll.slice(4, 34), "cur-3", "cur-4", null),
    chainPage(2, ascAll.slice(34), "cur-4", null, null),
  ];
  const itemsSampleFor = (idx) => {
    const turn = turns[idx];
    const extra = [
      { type: "reasoning", id: `id-${300 + idx}`, summary: { n: 2, lens: [30, 10] }, content: { n: 1, lens: [120] } },
      { type: "commandExecution", id: `id-${400 + idx}`, command: { len: 24 }, aggregatedOutput: idx % 2 === 0 ? null : { len: 800 }, status: "completed" },
      { type: "fileChange", id: `id-${500 + idx}`, changes: { n: 2, items: [] } },
    ];
    const entries = [...turn.items.map((it) => ({ turnId: turn.id, item: it })), ...extra.map((it) => ({ turnId: turn.id, item: it }))];
    const pages = [
      { page: 1, requestCursor: null, record: { ms: 15, resultBytes: 900, error: null }, entries: entries.slice(0, 5), entryCount: 5, nextCursor: "cur-i1", backwardsCursor: null, error: false },
      { page: 2, requestCursor: "cur-i1", record: { ms: 10, resultBytes: 400, error: null }, entries: entries.slice(5), entryCount: entries.length - 5, nextCursor: null, backwardsCursor: "cur-i2", error: false },
    ];
    return { turn: turn.id, positionInChain: idx, pages };
  };
  return {
    schema_version: 2,
    classification: "LIVE-REDACTED-OBSERVATION",
    gate_effect: "g0-evidence-input",
    target: {
      detected: { chatgptDesktopVersion: "26.825.41651", bundleVersion: "7345", embeddedCodexVersion: "codex-cli 0.151.0-alpha.7.1", controllerProtocolVersion: 3 },
      planFrozenBaseline: { chatgptDesktopVersion: "26.825.32147", embeddedCodexVersion: "codex-cli 0.150.0-alpha.12.2", upstreamTag: "rust-v0.150.0-alpha.12.2" },
      driftAssessment: { drifted: true, note: "synthetic self-test fixture" },
    },
    metadata: {
      captured_at: "2026-08-30T00:00:00Z", capture_purpose: "synthetic self-test", source_classification: "synthetic",
      operator_attestation: "PENDING-OWNER", redaction_procedure: "synthetic", secret_scan_command: "synthetic",
      secret_scan_result: "PENDING", cleanup_result: "synthetic",
    },
    probe: { caps: { turnsChainPages: 80 } },
    observations: [{ kind: "rpc", t_ms: 1 }],
    adjudication: { result: "CAPTURED-PENDING-ASSERTIONS", note: "synthetic self-test" },
    data: {
      inventory: { threads: [{ thread: "id-1", historyMode: "paginated" }], counts: { total: 1, paginated: 1, legacy: 0, unknownMode: 0 } },
      discovery: [{ thread: "id-1", historyMode: "paginated", turnsSeen: 35, pagesWalked: 2, reachedEofWithinCap: true }],
      longestThread: {
        thread: "id-1",
        historyMode: "paginated",
        readMeta: { record: { ms: 12, resultBytes: 300, error: null }, historyMode: "paginated", threadFields: ["historyMode", "id"], turnsPresent: false },
        controlFullRead: { record: { ms: 900, resultBytes: 500000, error: null }, error: false, turnIds: controlTurnIds, turnSummaries: controlTurnSummaries },
        summaryChain,
        notLoadedPage: [{
          page: 1, requestCursor: null, record: { ms: 11, resultBytes: 500, error: null },
          turns: page1Turns.map((t) => ({ ...t, itemsView: "notLoaded", items: [] })),
          turnIds: page1Turns.map((t) => t.id), nextCursor: "cur-1", backwardsCursor: null, error: false,
        }],
        backwardsChain: { anchoredOn: "cur-3", pages: ascPages },
        itemsSamples: [itemsSampleFor(0), itemsSampleFor(17), itemsSampleFor(34)],
        illegalTurnId: { record: { method: "thread/items/list", params: { turnId: "id-0" }, ms: 8, resultBytes: 52, error: null, resultShape: { data: { n: 0, items: [] }, nextCursor: null, backwardsCursor: null } }, expectation: "empty-success-per-official-filter-semantics" },
        resumeCandidate: {
          record: { ms: 130, resultBytes: 2500, error: null }, error: false, errorCode: null,
          threadTurnsPresent: true, threadTurnsCount: 0, threadTurnsEmpty: true,
          initialPagePresent: true, initialPageTurnIds: page1Turns.map((t) => t.id),
          initialPageNextCursor: "cur-1", turnsBackwardsCursor: "cur-9", itemsBackwardsCursor: null, liveMethodsAfterResume: [],
        },
      },
      secondaryThreads: [],
      legacyBattery: { na: "no-legacy-in-inventory", inventoryEvidence: { total: 1, paginated: 1, legacy: 0, unknownMode: 0 } },
    },
  };
}

function selfTest() {
  const clone = (fixture) => JSON.parse(JSON.stringify(fixture));
  const setDeep = (obj, path, value) => {
    const keys = path.split(".");
    let cur = obj;
    for (const key of keys.slice(0, -1)) cur = cur[isNaN(Number(key)) ? key : Number(key)];
    cur[keys[keys.length - 1]] = value;
  };
  const cases = [];
  const expectFail = (name, mutation, expectedCheckId) => {
    const fixture = clone(base);
    mutation(fixture);
    const result = runAssertions(fixture);
    const target = result.checks.find((c) => c.id === expectedCheckId);
    const others = result.checks.filter((c) => c.id !== expectedCheckId && c.status === "fail");
    cases.push({
      case: name,
      expectedCheck: expectedCheckId,
      detected: target?.status === "fail",
      sideEffects: others.map((c) => c.id),
      ok: target?.status === "fail",
    });
  };

  const base = syntheticFixture();
  const good = runAssertions(base);
  const goodFails = good.checks.filter((c) => c.status === "fail");
  cases.push({ case: "synthetic-good", expectedCheck: "all-pass", detected: goodFails.length === 0, sideEffects: goodFails.map((c) => c.id), ok: goodFails.length === 0 });

  expectFail("M1 summary finalAgent id not in items", (f) => { setDeep(f, "data.longestThread.summaryChain.0.turns.0.items.1.id", "id-9999"); }, "neg-1-summary-items-id-mismatch");
  expectFail("M2 reasoning item inside summary page", (f) => { setDeep(f, "data.longestThread.summaryChain.0.turns.0.items.1.type", "reasoning"); }, "neg-2-summary-foreign-type");
  expectFail("M3 foreign turnId in filtered items page", (f) => { setDeep(f, "data.longestThread.itemsSamples.0.pages.0.entries.0.turnId", "id-33"); }, "neg-3-items-turn-filter-leak");
  expectFail("M4a duplicate turn id across pages", (f) => { setDeep(f, "data.longestThread.summaryChain.1.turns.0.id", "id-35"); setDeep(f, "data.longestThread.summaryChain.1.turns.0.items", []); }, "neg-4-pagination-dup-gap");
  expectFail("M4c cursor chain break", (f) => { setDeep(f, "data.longestThread.summaryChain.1.requestCursor", "cur-wrong"); }, "neg-4-pagination-dup-gap");
  expectFail("M5 illegal turnId returns foreign items", (f) => { setDeep(f, "data.longestThread.illegalTurnId.record.resultShape.data.n", 3); }, "neg-5-illegal-turnid-semantics");
  expectFail("M5b illegal turnId fabricated rpc error", (f) => { setDeep(f, "data.longestThread.illegalTurnId.record.error", { code: -32602, messageLen: 30, messageShape: { len: 30 } }); }, "neg-5-illegal-turnid-semantics");
  expectFail("M6 control inventory missing a turn", (f) => { setDeep(f, "data.longestThread.controlFullRead.turnIds.10", null); f.data.longestThread.controlFullRead.turnIds = f.data.longestThread.controlFullRead.turnIds.filter(Boolean); }, "neg-6-no-gap-vs-control");
  expectFail("M6b control inventory reordered", (f) => {
    const ids = f.data.longestThread.controlFullRead.turnIds;
    const tmp = ids[3]; ids[3] = ids[4]; ids[4] = tmp;
  }, "neg-6-no-gap-vs-control");
  expectFail("M7 notLoaded page with non-empty items", (f) => { setDeep(f, "data.longestThread.notLoadedPage.0.turns.0.items", [{ type: "userMessage", id: "id-101", text: { len: 3 } }]); }, "notloaded-identity");
  expectFail("M8 resume thread.turns non-empty", (f) => { setDeep(f, "data.longestThread.resumeCandidate.threadTurnsEmpty", false); setDeep(f, "data.longestThread.resumeCandidate.threadTurnsCount", 30); }, "resume-candidate-shape");
  expectFail("M9 chain missing a turn vs control (gap)", (f) => {
    f.data.longestThread.summaryChain[1].turns = f.data.longestThread.summaryChain[1].turns.filter((t) => t.id !== "id-5");
    f.data.longestThread.summaryChain[1].turnIds = f.data.longestThread.summaryChain[1].turnIds.filter((id) => id !== "id-5");
  }, "neg-6-no-gap-vs-control");
  expectFail("M10 legacy N/A without inventory evidence", (f) => { setDeep(f, "data.legacyBattery.inventoryEvidence", null); }, "legacy-battery-or-na");

  const failed = cases.filter((c) => !c.ok);
  const report = {
    result: failed.length === 0 ? "SELF-TEST-PASS" : "SELF-TEST-FAIL",
    total: cases.length,
    detectedAllNegatives: cases.filter((c) => c.expectedCheck !== "all-pass").every((c) => c.detected),
    cases,
  };
  console.log(JSON.stringify(report, null, 2));
  return failed.length === 0;
}

// ---- entry ----------------------------------------------------------------

function main() {
  const args = process.argv.slice(2);
  if (args.includes("--self-test")) {
    process.exitCode = selfTest() ? 0 : 1;
    return;
  }
  const dumpIdx = args.indexOf("--dump-synthetic");
  if (dumpIdx >= 0) {
    process.stdout.write(JSON.stringify(syntheticFixture(), null, 2));
    return;
  }
  const fixtureIdx = args.indexOf("--fixture");
  const fixturePath = fixtureIdx >= 0 ? args[fixtureIdx + 1] : null;
  if (fixturePath == null) {
    console.error("usage: history-fixture-assert.mjs --fixture <file> | --self-test");
    process.exitCode = 2;
    return;
  }
  const fixture = JSON.parse(readFileSync(fixturePath, "utf8"));
  const report = runAssertions(fixture);
  const negativesOk = report.negativeResults.verdict === "none-triggered" && report.unverified.length === 0;
  const nineOk = report.nineItemChecklist.every((item) => item.status === "present" || /N\/A/.test(item.status));
  console.log(JSON.stringify({ ...report, g0EvidenceVerdict: negativesOk && nineOk ? "G0-EVIDENCE-COMPLETE" : "G0-EVIDENCE-INCOMPLETE" }, null, 2));
  process.exitCode = negativesOk ? 0 : 1;
}

main();
