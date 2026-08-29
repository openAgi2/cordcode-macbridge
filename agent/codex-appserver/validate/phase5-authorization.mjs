#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

const repo = process.cwd();
const upstream = process.env.CODEX_UPSTREAM_ROOT || "/Users/jacklee/Projects/codex";

function read(relative) {
  const file = path.join(repo, relative);
  if (!fs.existsSync(file)) {
    throw new Error(`missing required artifact: ${relative}`);
  }
  return fs.readFileSync(file, "utf8");
}

function requireText(text, needle, label) {
  if (!text.includes(needle)) {
    throw new Error(`${label} is missing required text: ${needle}`);
  }
}

const plan = read("docs/2026-08-26-codex-remote-backend-implementation-plan.md");
const gate = read("agent/codex-remote/testdata/phase5/authorization-gate.md");
const audit = read("agent/codex-remote/testdata/phase5/authorization-audit.md");

requireText(plan, "只有 `codex-web` 与 `codex-remote` 都完成真实 E2E", "plan gate");
requireText(gate, "The gate was opened on 2026-08-29", "authorization gate");
requireText(audit, "Observation window", "authorization audit");
requireText(audit, "Extract into `agent/codex-appserver/rpc`", "duplication decision");
requireText(audit, "Codec", "negative extraction audit");
requireText(audit, "Interactions", "negative extraction audit");
requireText(audit, "Models", "negative extraction audit");

const upstreamRemotePath = path.join(
  upstream,
  "codex-rs/app-server-client/src/remote.rs",
);
const upstreamFacadePath = path.join(
  upstream,
  "codex-rs/app-server-client/src/lib.rs",
);
for (const file of [upstreamRemotePath, upstreamFacadePath]) {
  if (!fs.existsSync(file)) {
    throw new Error(`missing official Codex source: ${file}`);
  }
}
const upstreamRemote = fs.readFileSync(upstreamRemotePath, "utf8");
const upstreamFacade = fs.readFileSync(upstreamFacadePath, "utf8");
for (const symbol of [
  "pending_requests",
  "resolve_server_request",
  "reject_server_request",
  "next_event",
  "shutdown",
]) {
  requireText(upstreamRemote, symbol, "official remote client");
}
requireText(upstreamFacade, "event_tx", "official app-server facade");
requireText(upstreamFacade, "ClientCommand::Request", "official app-server facade");

console.log("PHASE5-AUTHORIZATION-AUDIT PASS");
console.log(`upstream=${upstream}`);
console.log("scope=rpc-only");
