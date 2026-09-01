#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

const repo = process.cwd();
const read = (relative) => fs.readFileSync(path.join(repo, relative), "utf8");
const requireText = (text, needle, label) => {
  if (!text.includes(needle)) throw new Error(`${label} missing ${needle}`);
};
const rejectText = (text, needle, label) => {
  if (text.includes(needle)) throw new Error(`${label} unexpectedly contains ${needle}`);
};

const core = read("agent/codex-appserver/rpc/client.go");
const web = read("agent/codex-web/rpc.go");
const remote = read("agent/codex-remote/rpc.go");

for (const forbidden of [
  "agent/codex-web",
  "agent/codex-remote",
  "cordcode-macbridge/core",
  "gorilla/websocket",
]) {
  rejectText(core, forbidden, "shared RPC core");
}

requireText(web, "agent/codex-appserver/rpc", "codex-web wrapper");
requireText(remote, "agent/codex-appserver/rpc", "codex-remote wrapper");
requireText(web, "IsLocallyClosed()", "codex-web close policy");
requireText(remote, "IsTerminated()", "codex-remote close policy");
requireText(web, 'ErrorPrefix: "codexweb"', "codex-web error policy");
requireText(remote, 'ErrorPrefix: "codex-remote"', "codex-remote error policy");

rejectText(web, "agent/codex-remote", "codex-web wrapper");
rejectText(remote, "agent/codex-web", "codex-remote wrapper");

const unexpectedShared = fs
  .readdirSync(path.join(repo, "agent/codex-appserver"), { withFileTypes: true })
  .filter((entry) => entry.isDirectory() && !["rpc", "validate"].includes(entry.name))
  .map((entry) => entry.name);
if (unexpectedShared.length > 0) {
  throw new Error(`unaudited shared packages: ${unexpectedShared.join(", ")}`);
}

console.log("PHASE5-BOUNDARY PASS");
console.log("shared=rpc");
console.log("backendPolicies=independent");
