#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { isAllowedTopLevelEntry } from "./boundary-policy.mjs";

const scriptPath = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(scriptPath), "../../..");
const remoteRoot = path.join(repoRoot, "agent/codex-remote");
const frozenStart = "224a632e032aea913c78223b7d2231ffa78f39db";
const pairedIOS = "/Users/jacklee/Projects/cordcode-ios-codex-remote";
const pairedIOSBaselineCommit = "932f5fd61fc029aa63db333e3cb6d2eb6889ea38";
const pairedIOSBranch = "codex/codex-remote-backend-ios";

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

for (const entry of readdirSync(remoteRoot)) {
  if (!isAllowedTopLevelEntry(entry)) {
    fail(`codex-remote top-level entry is not allowed: ${entry}`);
  }
}

const readme = readFileSync(path.join(remoteRoot, "README.md"), "utf8");
for (const required of [
  "Phase 1 in progress",
  "product backend registered",
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
  ["legacy codex import", /github\.com\/openAgi2\/cordcode-macbridge\/agent\/codex(?:\/|")/u],
  ["codex-web import", /github\.com\/openAgi2\/cordcode-macbridge\/agent\/codex-web(?:\/|")/u],
];
for (const file of walk(remoteRoot)) {
  if (!codeExtensions.has(path.extname(file))) {
    continue;
  }
  if (file.startsWith(path.join(remoteRoot, "probe")) && /core\.RegisterAgent/u.test(readFileSync(file, "utf8"))) {
    fail(`core.RegisterAgent found in probe ${path.relative(repoRoot, file)}`);
  }
  if (/_test\.(go|js|mjs|ts)$/u.test(file)) {
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
if (run("git", ["-C", pairedIOS, "branch", "--show-current"]) !== pairedIOSBranch) {
  fail("paired iOS worktree is on an unauthorized branch");
}
if (run("git", ["-C", pairedIOS, "status", "--porcelain"]) !== "") {
  fail("paired iOS worktree is dirty");
}
run("git", [
  "-C",
  pairedIOS,
  "merge-base",
  "--is-ancestor",
  pairedIOSBaselineCommit,
  "HEAD",
]);

console.log(
  JSON.stringify(
    {
      result: "PASS",
      phase: 1,
      productBackendRegistered: true,
      gateP0Passed: "first-connect-live-with-known-gaps",
      ignoredTopLevelMetadata: [".DS_Store"],
      frozenBackendDiff: "clean",
      pairedIOS: "authorized-descendant-clean",
    },
    null,
    2,
  ),
);
