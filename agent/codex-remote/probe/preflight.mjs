#!/usr/bin/env node

import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import path from "node:path";

const scriptPath = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(scriptPath), "../../..");
const appPath = "/Applications/ChatGPT.app";
const codexPath = path.join(appPath, "Contents/Resources/codex");
const nodePath = path.join(appPath, "Contents/Resources/cua_node/bin/node");
const addonPath = path.join(
  appPath,
  "Contents/Resources/native/remote-control-device-key.node",
);
const expectedAddonHash = "0bc9878bad8635d8721accb1f4a4a47666c6e50f4a025b83cf0314750bb8f5a0";

function fail(message) {
  throw new Error(message);
}

function run(command, args) {
  return execFileSync(command, args, {
    cwd: repoRoot,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function portAvailable(port) {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.unref();
    server.once("error", (error) => {
      if (error?.code === "EADDRINUSE") resolve(false);
      else reject(error);
    });
    server.listen(port, "localhost", () => server.close(() => resolve(true)));
  });
}

run(nodePath, [path.join(repoRoot, "agent/codex-remote/validate/controller-fixtures.mjs")]);

if (sha256(readFileSync(addonPath)) !== expectedAddonHash) {
  fail("remote-control device-key addon hash drifted");
}
const addonExports = JSON.parse(
  run(nodePath, [
    "-e",
    "const m=require(process.argv[1]); console.log(JSON.stringify(Object.keys(m).sort()))",
    addonPath,
  ]),
);
const expectedAddonExports = [
  "createDeviceKey",
  "deleteDeviceKey",
  "getDeviceKeyPublic",
  "signDeviceKey",
];
if (JSON.stringify(addonExports) !== JSON.stringify(expectedAddonExports)) {
  fail("remote-control device-key addon API drifted");
}

const loginStatus = spawnSync(codexPath, ["login", "status"], {
  cwd: repoRoot,
  encoding: "utf8",
  stdio: ["ignore", "pipe", "pipe"],
  timeout: 10_000,
});
const loginStatusText = `${loginStatus.stdout ?? ""}\n${loginStatus.stderr ?? ""}`;
const chatgptLoginAvailable =
  loginStatus.status === 0 && loginStatusText.includes("Logged in using ChatGPT");

const callbackPorts = {};
for (const port of [1455, 1457]) callbackPorts[port] = await portAvailable(port);

console.log(
  JSON.stringify(
    {
      result: "PASS",
      classification: "NON-MUTATING-PREFLIGHT",
      target: {
        chatgptVersion: run("/usr/libexec/PlistBuddy", [
          "-c",
          "Print :CFBundleShortVersionString",
          path.join(appPath, "Contents/Info.plist"),
        ]),
        embeddedCodexVersion: run(codexPath, ["--version"]),
        helperNodeVersion: run(nodePath, ["--version"]),
        deviceKeyAddonSha256: expectedAddonHash,
      },
      authHelper: {
        kind: "embedded Codex app-server getAuthStatus includeToken, memory-only",
        chatgptLoginAvailable,
        executed: false,
        note: "Preflight queried login status only; it did not request or expose a token.",
      },
      stepUp: {
        issuer: "https://auth.openai.com",
        callbackPorts,
        executed: false,
      },
      deviceKey: {
        addonExports,
        createSignDeleteExecuted: false,
      },
      networkMutationExecuted: false,
      gateEffect: "does-not-pass-phase0",
    },
    null,
    2,
  ),
);
