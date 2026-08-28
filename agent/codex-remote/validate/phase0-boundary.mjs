#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const scriptPath = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(scriptPath), "../../..");
const remoteRoot = path.join(repoRoot, "agent/codex-remote");
const frozenStart = "224a632e032aea913c78223b7d2231ffa78f39db";
const pairedIOS = "/Users/jacklee/Projects/cordcode-ios-codex-remote";
const pairedIOSCommit = "d0762cb9a05997b615ef4589f39afad8f4b4db04";

function fail(message) {
  throw new Error(message);
}

function run(command, args, cwd = repoRoot) {
  return execFileSync(command, args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function walk(directory) {
  const files = [];
  for (const entry of readdirSync(directory)) {
    const candidate = path.join(directory, entry);
    if (statSync(candidate).isDirectory()) {
      files.push(...walk(candidate));
    } else {
      files.push(candidate);
    }
  }
  return files;
}

const allowedTopLevel = new Set(["README.md", "probe", "testdata", "validate"]);
for (const entry of readdirSync(remoteRoot)) {
  if (!allowedTopLevel.has(entry)) {
    fail(`Phase 0 top-level entry is not allowed: ${entry}`);
  }
}

const readme = readFileSync(path.join(remoteRoot, "README.md"), "utf8");
for (const required of [
  "Phase 0 only",
  "product backend not registered",
  "Gate P0 not passed",
  "ChatGPT Desktop private app-server",
  "independently enrolled MacBridge controller",
  "git diff --exit-code -- agent/codex-web agent/codex",
]) {
  if (!readme.includes(required)) {
    fail(`Phase 0 README is missing required boundary text: ${required}`);
  }
}
for (let gateRow = 1; gateRow <= 14; gateRow += 1) {
  if (!readme.includes(`| ${gateRow} |`)) {
    fail(`Phase 0 README is missing Gate P0 row ${gateRow}`);
  }
}

const codeExtensions = new Set([".go", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".swift", ".rs", ".sh"]);
const forbiddenCodePatterns = [
  ["core.RegisterAgent", /core\.RegisterAgent/u],
  ["legacy codex import", /(?:github\.com\/[^"'\s]+\/)?agent\/codex(?:["'\s]|$)/u],
  ["codex-web import", /(?:github\.com\/[^"'\s]+\/)?agent\/codex-web(?:["'\s]|$)/u],
  ["runtime driver registration", /register(?:ed)?Drivers?.*codex-remote/iu],
];
for (const file of walk(remoteRoot)) {
  if (!file.startsWith(path.join(remoteRoot, "probe")) || !codeExtensions.has(path.extname(file))) {
    continue;
  }
  const contents = readFileSync(file, "utf8");
  for (const [label, pattern] of forbiddenCodePatterns) {
    if (pattern.test(contents)) {
      fail(`${label} found in ${path.relative(repoRoot, file)}`);
    }
  }
}

const frozenDiff = run("git", [
  "diff",
  "--name-only",
  `${frozenStart}..HEAD`,
  "--",
  "agent/codex-web",
  "agent/codex",
]);
if (frozenDiff !== "") {
  fail(`frozen backend directories changed since ${frozenStart}: ${frozenDiff}`);
}
const worktreeDiff = run("git", ["diff", "--name-only", "--", "agent/codex-web", "agent/codex"]);
if (worktreeDiff !== "") {
  fail(`frozen backend directories have worktree changes: ${worktreeDiff}`);
}
if (run("git", ["-C", pairedIOS, "rev-parse", "HEAD"]) !== pairedIOSCommit) {
  fail("paired iOS worktree moved before Phase 3");
}
if (run("git", ["-C", pairedIOS, "status", "--porcelain"]) !== "") {
  fail("paired iOS worktree is dirty before Phase 3");
}

console.log(
  JSON.stringify(
    {
      result: "PASS",
      phase: 0,
      productBackendRegistered: false,
      gateP0Passed: false,
      allowedTopLevel: [...allowedTopLevel].sort(),
      frozenBackendDiff: "clean",
      pairedIOS: "frozen-clean",
    },
    null,
    2,
  ),
);
