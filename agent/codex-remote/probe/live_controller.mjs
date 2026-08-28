#!/usr/bin/env node

import { spawn, spawnSync } from "node:child_process";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import readline from "node:readline/promises";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const WebSocket = require("/opt/homebrew/lib/node_modules/wscat/node_modules/ws");
const base = "https://chatgpt.com/backend-api";
const websocketURL = "wss://chatgpt.com/backend-api/codex/remote/control/client";
const codexPath = "/Applications/ChatGPT.app/Contents/Resources/codex";
const helperSource = new URL("./device_key_helper.swift", import.meta.url).pathname;
const scope = "codex.remote_control.enroll";
const controllerScope = "remote_control_controller_websocket";
const oauthClientID = "app_EMoamEEZ73f0CkXaXp7hrann";
const timeline = [];

function observe(kind, detail = {}) {
  timeline.push({ t_ms: Math.max(0, Date.now() - startedAt), kind, ...detail });
  console.log(JSON.stringify({ event: kind, ...detail }));
}

function fail(message) {
  throw new Error(message);
}

function withTimeout(promise, ms, message) {
  let timer;
  const timeout = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(message)), ms); });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

function closeLocalServer(server) {
  return new Promise((resolve) => {
    const drop = setTimeout(() => server.closeAllConnections(), 2_000);
    drop.unref();
    server.close(() => { clearTimeout(drop); resolve(); });
  });
}

function jwtClaims(token) {
  const parts = token.split(".");
  if (parts.length < 2) fail("malformed JWT");
  return JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
}

function authIdentity(token) {
  const claims = jwtClaims(token);
  const auth = claims["https://api.openai.com/auth"] ?? {};
  return {
    accountId: auth.chatgpt_account_id ?? auth.account_id ?? null,
    accountUserId: auth.chatgpt_account_user_id ?? auth.account_user_id ?? null,
    authUserId: auth.user_id ?? null,
  };
}

function headers(token, accountID, extra = {}) {
  return {
    Authorization: `Bearer ${token}`,
    "ChatGPT-Account-Id": accountID,
    originator: "Codex Desktop",
    ...extra,
  };
}

async function jsonRequest(token, accountID, method, pathname, body) {
  const response = await fetch(`${base}${pathname}`, {
    method,
    headers: headers(token, accountID, body === undefined ? {} : { "Content-Type": "application/json" }),
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: "error",
    signal: AbortSignal.timeout(15_000),
  });
  const text = await response.text();
  let parsed = null;
  try { parsed = text.length === 0 ? null : JSON.parse(text); } catch {}
  if (!response.ok) {
    const error = new Error(`official request failed with HTTP ${response.status}`);
    error.status = response.status;
    error.bodyBytes = Buffer.byteLength(text);
    error.bodySha256 = createHash("sha256").update(text).digest("hex");
    throw error;
  }
  return { status: response.status, body: parsed, bodySha256: createHash("sha256").update(text).digest("hex") };
}

function loadAuthToken() {
  return new Promise((resolve, reject) => {
    const child = spawn(codexPath, ["app-server", "--stdio"], { stdio: ["pipe", "pipe", "ignore"] });
    let buffer = "";
    let initialized = false;
    const timeout = setTimeout(() => { child.kill(); reject(new Error("auth helper timeout")); }, 15_000);
    child.on("error", (error) => { clearTimeout(timeout); reject(error); });
    child.stdout.on("data", (chunk) => {
      buffer += chunk;
      for (;;) {
        const newline = buffer.indexOf("\n");
        if (newline < 0) break;
        const line = buffer.slice(0, newline);
        buffer = buffer.slice(newline + 1);
        let message;
        try { message = JSON.parse(line); } catch { continue; }
        if (message.id === 1 && !initialized) {
          initialized = true;
          child.stdin.write(`${JSON.stringify({ method: "initialized", params: {} })}\n`);
          child.stdin.write(`${JSON.stringify({ id: 2, method: "getAuthStatus", params: { includeToken: true, refreshToken: false } })}\n`);
        } else if (message.id === 2) {
          clearTimeout(timeout);
          const token = message.result?.authToken;
          child.stdin.end();
          child.stdout.destroy();
          child.kill();
          child.unref();
          if (message.result?.authMethod !== "chatgpt" || typeof token !== "string") reject(new Error("ChatGPT auth unavailable"));
          else resolve(token);
        }
      }
    });
    child.stdin.write(`${JSON.stringify({ id: 1, method: "initialize", params: { clientInfo: { name: "codex_remote_phase0_probe", title: "CordCode Phase 0 Probe", version: "0" }, capabilities: { experimentalApi: true } } })}\n`);
  });
}

function compileKeyHelper() {
  const directory = mkdtempSync(path.join(os.tmpdir(), "codex-remote-key-helper."));
  const binary = path.join(directory, "device-key-helper");
  const compile = spawnSync("swiftc", ["-framework", "Security", helperSource, "-o", binary], { encoding: "utf8" });
  if (compile.status !== 0) fail("device-key helper compilation failed");
  const identities = spawnSync("security", ["find-identity", "-v", "-p", "codesigning"], { encoding: "utf8" });
  const identity = identities.stdout.match(/\b[0-9A-F]{40}\b/u)?.[0];
  if (identity == null) fail("Apple Development signing identity unavailable");
  const sign = spawnSync("codesign", ["--force", "--sign", identity, binary], { encoding: "utf8" });
  if (sign.status !== 0) fail("device-key helper signing failed");
  return { directory, binary };
}

function keyCall(binary, request) {
  const result = spawnSync(binary, [], { input: `${JSON.stringify(request)}\n`, encoding: "utf8" });
  const parsed = JSON.parse(result.stdout || "{}");
  if (result.status !== 0 || parsed.ok !== true) fail(parsed.error ?? "device-key helper failed");
  return parsed;
}

function signingBytes(payload) {
  return Buffer.from(JSON.stringify({ domain: "codex-device-key-sign-payload/v1", payload }), "utf8");
}

function signPayload(binary, keyID, payload) {
  const bytes = signingBytes(payload);
  const signed = keyCall(binary, { op: "sign", keyId: keyID, payloadBase64: bytes.toString("base64") });
  return { algorithm: signed.algorithm, signatureDerBase64: signed.signatureDerBase64, signedPayloadBase64: bytes.toString("base64") };
}

function enrollmentProof({ challenge, clientID, key, helperBinary, expectedPath, requireDeviceIdentityHash }) {
  const identityObject = { algorithm: key.algorithm, keyId: key.keyId, protectionClass: key.protectionClass, publicKeySpkiDerBase64: key.publicKeySpkiDerBase64 };
  const identityHash = createHash("sha256").update(JSON.stringify(identityObject)).digest("base64url");
  if (challenge.purpose !== "remote_control_client_enrollment" || challenge.audience !== "remote_control_client_enrollment" || challenge.client_id !== clientID || challenge.target_origin !== "https://chatgpt.com" || challenge.target_path !== expectedPath) fail("enrollment challenge mismatch");
  if (requireDeviceIdentityHash && challenge.device_identity_hash == null) fail("refresh challenge device identity hash missing");
  const challengeIdentityHash = challenge.device_identity_hash ?? identityHash;
  if (challengeIdentityHash !== identityHash) fail("enrollment challenge identity mismatch");
  const payload = { accountUserId: challenge.account_user_id, audience: "remote_control_client_enrollment", challengeExpiresAt: challenge.challenge_expires_at, challengeId: challenge.challenge_id, clientId: challenge.client_id, deviceIdentitySha256Base64url: challengeIdentityHash, nonce: challenge.nonce, targetOrigin: challenge.target_origin, targetPath: challenge.target_path, type: "remoteControlClientEnrollment" };
  const signed = signPayload(helperBinary, key.keyId, payload);
  return { challenge_token: challenge.challenge_token, key_id: key.keyId, signature_der_base64: signed.signatureDerBase64, signed_payload_base64: signed.signedPayloadBase64, algorithm: signed.algorithm };
}

async function stepUp(accountID) {
  const state = randomBytes(32).toString("base64url");
  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  let server;
  let resolveCode;
  let rejectCode;
  const codePromise = new Promise((resolve, reject) => { resolveCode = resolve; rejectCode = reject; });
  for (const port of [1455, 1457]) {
    try {
      server = http.createServer((request, response) => {
        const url = new URL(request.url ?? "/", `http://localhost:${port}`);
        if (url.pathname !== "/auth/callback" || url.searchParams.get("state") !== state) {
          response.writeHead(400).end("Invalid callback");
          return;
        }
        const code = url.searchParams.get("code");
        if (code == null) {
          response.writeHead(400).end("Authorization failed");
          rejectCode(new Error("step-up authorization failed"));
          return;
        }
        response.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" }).end("Remote controller authorization received. Return to Codex.");
        resolveCode({ code, redirectURI: `http://localhost:${port}/auth/callback` });
      });
      await new Promise((resolve, reject) => { server.once("error", reject); server.listen(port, "localhost", resolve); });
      break;
    } catch { server?.close(); server = null; }
  }
  if (server == null) fail("step-up callback ports unavailable");
  const redirectURI = `http://localhost:${server.address().port}/auth/callback`;
  const authorize = new URL("https://auth.openai.com/oauth/authorize");
  authorize.search = new URLSearchParams({ response_type: "code", client_id: oauthClientID, redirect_uri: redirectURI, scope, code_challenge: challenge, code_challenge_method: "S256", state, originator: "Codex Desktop", reauth: "remote_control", max_age: "0", codex_cli_simplified_flow: "true", allowed_workspace_id: accountID, current_workspace_id: accountID }).toString();
  observe("step_up_browser_opened", { callback_port: server.address().port });
  spawnSync("/usr/bin/open", [authorize.toString()], { stdio: "ignore" });
  try {
    const { code } = await withTimeout(codePromise, 600_000, "step-up timeout");
    const response = await fetch("https://auth.openai.com/oauth/token", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, redirect_uri: redirectURI, client_id: oauthClientID, code_verifier: verifier }), signal: AbortSignal.timeout(15_000) });
    const parsed = await response.json();
    if (!response.ok || typeof parsed.access_token !== "string") fail(`step-up exchange failed with HTTP ${response.status}`);
    return parsed.access_token;
  } finally { await closeLocalServer(server); }
}

async function collectManualPairingCode() {
  const formToken = randomBytes(32).toString("base64url");
  let server;
  let resolveCode;
  const codePromise = new Promise((resolve) => { resolveCode = resolve; });
  for (const port of [1455, 1457]) {
    try {
      server = http.createServer((request, response) => {
        const url = new URL(request.url ?? "/", `http://localhost:${port}`);
        if (request.method === "GET" && url.pathname === "/pair" && url.searchParams.get("state") === formToken) {
          response.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer" });
          response.end(`<!doctype html><meta charset="utf-8"><title>Phase 0 controller pairing</title><meta name="referrer" content="no-referrer"><style>body{font:16px system-ui;max-width:42rem;margin:4rem auto;padding:0 1rem}input,button{font:inherit;padding:.7rem}input{width:24rem;max-width:90%}</style><h1>Phase 0 controller pairing</h1><p>在 ChatGPT Desktop 的“控制这台 Mac”授权页切换到“电脑”，刷新生成新配对码，仅在此本机页面输入。不要发送到聊天。</p><form method="post" action="/pair?state=${formToken}" autocomplete="off"><input type="password" name="code" required autofocus autocomplete="off" maxlength="256"><button type="submit">Pair</button></form>`);
          return;
        }
        if (request.method === "POST" && url.pathname === "/pair" && url.searchParams.get("state") === formToken) {
          const chunks = [];
          let bytes = 0;
          request.on("data", (chunk) => {
            bytes += chunk.length;
            if (bytes > 1024) request.destroy();
            else chunks.push(chunk);
          });
          request.on("end", () => {
            const code = new URLSearchParams(Buffer.concat(chunks).toString("utf8")).get("code")?.trim();
            if (typeof code !== "string" || code.length === 0 || code.length > 256) {
              response.writeHead(400, { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" }).end("Invalid code");
              return;
            }
            response.writeHead(200, { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" }).end("Pairing code received in probe memory. Return to Codex.");
            resolveCode(code);
          });
          return;
        }
        response.writeHead(404, { "Cache-Control": "no-store" }).end();
      });
      await new Promise((resolve, reject) => { server.once("error", reject); server.listen(port, "localhost", resolve); });
      break;
    } catch { server?.close(); server = null; }
  }
  if (server == null) fail("pairing input ports unavailable");
  const inputURL = `http://localhost:${server.address().port}/pair?state=${formToken}`;
  observe("pairing_input_opened", { input_port: server.address().port, transport: "localhost_one_time_form" });
  spawnSync("/usr/bin/open", [inputURL], { stdio: "ignore" });
  try {
    return await withTimeout(codePromise, 600_000, "pairing input timeout");
  } finally {
    await closeLocalServer(server);
  }
}

async function pairedEnvironments(token, accountID, clientID) {
  const pathname = `/codex/remote/control/clients/${encodeURIComponent(clientID)}/environments?limit=100`;
  let environments = await jsonRequest(token, accountID, "GET", pathname);
  if (!Array.isArray(environments.body?.items)) fail("client environment list schema mismatch");
  observe("environment_list", { count: environments.body.items.length, response_fields: Object.keys(environments.body).sort(), pairing_required: environments.body.items.length === 0 });
  if (environments.body.items.length > 0) return environments;

  let manualPairingCode = await collectManualPairingCode();
  try {
    const paired = await jsonRequest(token, accountID, "POST", "/wham/remote/control/client/pair", { client_id: clientID, manual_pairing_code: manualPairingCode });
    observe("controller_pair", { status: paired.status, response_fields: Object.keys(paired.body ?? {}).sort() });
  } finally {
    manualPairingCode = null;
  }
  for (let attempt = 1; attempt <= 10; attempt += 1) {
    environments = await jsonRequest(token, accountID, "GET", pathname);
    if (!Array.isArray(environments.body?.items)) fail("client environment list schema mismatch after pair");
    if (environments.body.items.length > 0) {
      observe("environment_list", { count: environments.body.items.length, response_fields: Object.keys(environments.body).sort(), item_fields: [...new Set(environments.body.items.flatMap((item) => Object.keys(item)))].sort(), poll_attempt: attempt });
      return environments;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  fail("paired controller still has no environments");
}

async function revokeProbeController(token, accountID, clientID) {
  const response = await fetch(`${base}/wham/remote/control/clients/${encodeURIComponent(clientID)}`, { method: "DELETE", headers: headers(token, accountID), redirect: "error", signal: AbortSignal.timeout(15_000) });
  await response.arrayBuffer();
  return response;
}

async function selectEnvironment(items) {
  const safe = items.map((item, index) => ({ index: index + 1, display_name: item.display_name ?? item.name ?? "unnamed", host_name: item.host_name ?? null, online: item.online, busy: item.busy, os: item.os, client_type: item.client_type, app_server_version: item.app_server_version }));
  console.log(JSON.stringify({ environments: safe }, null, 2));
  const terminal = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
    const answer = await terminal.question("Select the current ChatGPT Desktop environment number: ");
    const index = Number(answer) - 1;
    if (!Number.isInteger(index) || index < 0 || index >= items.length) fail("invalid environment selection");
    return { environment: items[index], index };
  } finally { terminal.close(); }
}

function runControllerWSS({ token, accountID, controllerToken, controllerExpiresAt, clientID, key, helperBinary, environment, streamID, subscribeCursor = null, initialize }) {
  return new Promise((resolve, reject) => {
    const requestHeaders = headers(token, accountID, {
      "x-codex-client-id": clientID,
      "x-codex-protocol-version": "3",
      "x-codex-client-session-token": `Bearer ${controllerToken}`,
      ...(subscribeCursor == null ? {} : { "x-codex-subscribe-cursor": subscribeCursor }),
    });
    const ws = new WebSocket(websocketURL, { headers: requestHeaders, handshakeTimeout: 10_000, perMessageDeflate: false });
    let timeout = setTimeout(() => { ws.terminate(); reject(new Error("controller WSS timeout")); }, 60_000);
    let challengeComplete = false;
    let initializeResultFields = null;
    let pingSent = false;
    let nextClientSeq = initialize ? 1 : 5;
    let cursorlessPongs = 0;
    let settled = false;
    let observedCursor = subscribeCursor;
    let keepAlive = null;
    let turnWait = null;
    const liveRpc = [];
    const finish = (result) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      if (keepAlive != null) clearInterval(keepAlive);
      if (turnWait != null) clearTimeout(turnWait);
      ws.once("close", () => resolve(result));
      ws.close(1000);
      setTimeout(() => ws.terminate(), 2_000).unref();
    };
    const armTimeout = (ms) => {
      clearTimeout(timeout);
      timeout = setTimeout(() => { ws.terminate(); reject(new Error("controller WSS timeout")); }, ms);
    };
    const sendClientMessage = (method, message) => {
      const seqID = nextClientSeq;
      nextClientSeq += 1;
      ws.send(JSON.stringify({ type: "client_message", client_id: clientID, env_id: environment.env_id, stream_id: streamID, seq_id: seqID, skip_history: false, message }));
      observe("client_message", { method, env: "env_selected", stream: "stream_primary", seq_id: seqID });
      return seqID;
    };
    const sendPing = () => {
      const seqID = nextClientSeq;
      nextClientSeq += 1;
      ws.send(JSON.stringify({ type: "ping", client_id: clientID, env_id: environment.env_id, stream_id: streamID, seq_id: seqID, state: "foreground", skip_history: true }));
      pingSent = true;
      observe("ping", { env: "env_selected", stream: "stream_primary", seq_id: seqID, reconnect: !initialize });
    };
    const takeEnvelopeCursor = (message) => {
      if (message.cursor == null || settled) return false;
      observedCursor = message.cursor;
      observe("cursor_observed", { on: message.type ?? "unknown_wss", seq_id: message.seq_id ?? null, cursor_chars: String(message.cursor).length });
      finish({ streamID, cursor: message.cursor, initializeResultFields, liveRpc: [...new Set(liveRpc)] });
      return true;
    };
    ws.on("open", () => observe(subscribeCursor == null ? "websocket_handshake" : "reconnect_handshake", { status: "open", protocol_version: 3, cursor_header_present: subscribeCursor != null }));
    ws.on("error", (error) => { clearTimeout(timeout); reject(new Error(`controller WSS error: ${error.message}`)); });
    ws.on("message", (data) => {
      let message;
      try { message = JSON.parse(String(data)); } catch { clearTimeout(timeout); ws.terminate(); reject(new Error("unknown WSS payload")); return; }
      if (!challengeComplete) {
        const expectedHash = createHash("sha256").update(controllerToken).digest("base64url");
        if (message.type !== "device_key_challenge" || message.clientId !== clientID || message.targetOrigin !== "https://chatgpt.com" || message.targetPath !== "/backend-api/codex/remote/control/client" || message.tokenSha256Base64url !== expectedHash || message.tokenExpiresAt !== controllerExpiresAt || JSON.stringify(message.scopes) !== JSON.stringify([controllerScope])) {
          clearTimeout(timeout); ws.terminate(); reject(new Error("WSS challenge mismatch")); return;
        }
        const proof = signPayload(helperBinary, key.keyId, { accountUserId: message.accountUserId, audience: message.audience, clientId: message.clientId, nonce: message.nonce, scopes: message.scopes, sessionId: message.sessionId, targetOrigin: message.targetOrigin, targetPath: message.targetPath, tokenExpiresAt: message.tokenExpiresAt, tokenSha256Base64url: message.tokenSha256Base64url, type: "remoteControlClientConnection" });
        ws.send(JSON.stringify({ type: "device_key_proof", keyId: key.keyId, signatureDerBase64: proof.signatureDerBase64, signedPayloadBase64: proof.signedPayloadBase64, algorithm: proof.algorithm }));
        observe("device_key_proof", { algorithm: proof.algorithm, key: "probe_key", signature_encoding: "der_base64", signed_payload_encoding: "base64" });
        challengeComplete = true;
        observe("device_key_challenge", { fields: Object.keys(message).sort(), target_origin: message.targetOrigin, target_path: message.targetPath, scopes: message.scopes });
        if (initialize) {
          sendClientMessage("initialize", { id: 1, method: "initialize", params: { clientInfo: { name: "codex_remote_phase0_probe", title: "CordCode Phase 0 Probe", version: "0" }, capabilities: { experimentalApi: true } } });
        } else {
          sendPing();
        }
        return;
      }
      const safe = { type: message.type, fields: Object.keys(message).sort(), env_matches: message.env_id === environment.env_id, stream_matches: message.stream_id === streamID, seq_id: message.seq_id, has_cursor: message.cursor != null };
      observe(message.type ?? "unknown_wss", safe);
      if (takeEnvelopeCursor(message)) return;
      if (initialize && message.type === "server_message" && message.message?.id === 1 && initializeResultFields == null) {
        initializeResultFields = Object.keys(message.message.result ?? {}).sort();
        sendClientMessage("initialized", { method: "initialized", params: {} });
        sendClientMessage("thread/list", { id: 2, method: "thread/list", params: { limit: 5 } });
        sendPing();
      }
      if (initialize && message.type === "server_message" && message.message?.id === 2) {
        const result = message.message.result ?? {};
        observe("thread_list", {
          error: message.message.error != null,
          result_fields: Object.keys(result).sort(),
          data_count: Array.isArray(result.data) ? result.data.length : null,
          pagination_cursor_present: result.cursor != null,
        });
      }
      if (initialize && message.type === "server_message") {
        const method = typeof message.message?.method === "string" ? message.message.method : null;
        if (method != null && method !== "initialize") {
          liveRpc.push(method);
          observe("live_rpc", { method, has_params: message.message?.params != null, envelope_cursor: message.cursor != null });
        }
      }
      if (message.type === "pong") {
        if (message.status !== "active") {
          clearTimeout(timeout); ws.terminate(); reject(new Error("pong status mismatch")); return;
        }
        if (initialize && observedCursor == null) {
          cursorlessPongs += 1;
          if (cursorlessPongs < 3) {
            setTimeout(sendPing, 1_000);
            return;
          }
          if (turnWait == null) {
            observe("awaiting_desktop_turn", { timeout_ms: 180_000 });
            keepAlive = setInterval(() => { if (!settled) sendPing(); }, 10_000);
            turnWait = setTimeout(() => {
              observe("desktop_turn_wait_elapsed", { live_rpc_methods: [...new Set(liveRpc)] });
              finish({ streamID, cursor: observedCursor, initializeResultFields, liveRpc: [...new Set(liveRpc)] });
            }, 180_000);
            armTimeout(200_000);
            return;
          }
          return;
        }
        finish({ streamID, cursor: observedCursor, initializeResultFields, liveRpc: [...new Set(liveRpc)] });
      }
    });
  });
}

async function openControllerWSS(options) {
  const streamID = randomUUID();
  const first = await runControllerWSS({ ...options, streamID, initialize: true });
  if (first.initializeResultFields == null) fail("initial controller stream proof incomplete");
  if (first.cursor == null) {
    observe("reconnect_blocked", { reason: "no_cursor_after_initialized_thread_list_pongs_and_desktop_turn_wait", cursor_header_present: false, live_rpc_methods: first.liveRpc ?? [] });
    return { streamID, cursor: null, initializeResultFields: first.initializeResultFields, reconnectStatus: "blocked-no-cursor" };
  }
  const reconnected = await runControllerWSS({ ...options, streamID, subscribeCursor: first.cursor, initialize: false });
  return { streamID, cursor: reconnected.cursor, initializeResultFields: first.initializeResultFields, reconnectStatus: "passed" };
}

const startedAt = Date.now();
let token = null;
let stepUpToken = null;
let controllerToken = null;
let clientID = null;
let key = null;
let helper = null;
let cleanupStatus = null;
let controllerRevoked = false;

try {
  helper = compileKeyHelper();
  token = await loadAuthToken();
  const identity = authIdentity(token);
  if (identity.accountID == null && identity.accountId == null) fail("account id claim missing");
  const accountID = identity.accountId;
  observe("auth_loaded", { method: "embedded_app_server_memory_only" });
  const start = await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/enroll/start", {});
  clientID = start.body?.client_id;
  const challenge = start.body?.device_key_challenge;
  if (typeof clientID !== "string" || challenge == null) fail("enroll/start schema mismatch");
  observe("enroll_start", { status: start.status, response_fields: Object.keys(start.body).sort(), challenge_fields: Object.keys(challenge).sort() });
  key = keyCall(helper.binary, { op: "create" });
  observe("device_key_created", { algorithm: key.algorithm, protection_class: key.protectionClass, spki_bytes: Buffer.from(key.publicKeySpkiDerBase64, "base64").length });
  stepUpToken = await stepUp(accountID);
  const stepClaims = jwtClaims(stepUpToken);
  const stepScopes = [...new Set([...(stepClaims.scope?.split(/\s+/u) ?? []), ...(stepClaims.scp ?? [])].filter(Boolean))];
  if (stepScopes.length !== 1 || stepScopes[0] !== scope || Math.floor(Date.now() / 1000) - stepClaims.iat > 300 || Date.now() - stepClaims.pwd_auth_time > 300_000) fail("step-up token validation failed");
  observe("step_up_validated", { scopes: stepScopes, fresh: true });
  const finish = await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/enroll/finish", { client_id: clientID, step_up_token: stepUpToken, device_identity: { key_id: key.keyId, public_key_spki_der_base64: key.publicKeySpkiDerBase64, algorithm: key.algorithm, protection_class: key.protectionClass }, device_key_proof: enrollmentProof({ challenge, clientID, key, helperBinary: helper.binary, expectedPath: "/backend-api/codex/remote/control/client/enroll/finish", requireDeviceIdentityHash: false }) });
  controllerToken = finish.body?.remote_control_token;
  let expiresAt = Math.floor(Date.parse(finish.body?.expires_at) / 1000);
  if (typeof controllerToken !== "string" || !Number.isFinite(expiresAt) || JSON.stringify(finish.body?.scopes) !== JSON.stringify([controllerScope])) fail("enroll/finish schema mismatch");
  observe("enroll_finish", { status: finish.status, response_fields: Object.keys(finish.body).sort(), scopes: finish.body.scopes, expires_at_present: true });
  stepUpToken = null;
  const refreshStart = await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/refresh/start", { client_id: clientID });
  const refreshChallenge = refreshStart.body?.device_key_challenge;
  if (refreshStart.body?.client_id !== clientID || refreshChallenge == null) fail("refresh/start schema mismatch");
  observe("refresh_start", { status: refreshStart.status, response_fields: Object.keys(refreshStart.body).sort(), challenge_fields: Object.keys(refreshChallenge).sort(), device_identity_hash_present: refreshChallenge.device_identity_hash != null });
  const refreshFinish = await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/refresh/finish", { client_id: clientID, device_key_proof: enrollmentProof({ challenge: refreshChallenge, clientID, key, helperBinary: helper.binary, expectedPath: "/backend-api/codex/remote/control/client/refresh/finish", requireDeviceIdentityHash: true }) });
  controllerToken = refreshFinish.body?.remote_control_token;
  expiresAt = Math.floor(Date.parse(refreshFinish.body?.expires_at) / 1000);
  if (refreshFinish.body?.client_id !== clientID || typeof controllerToken !== "string" || !Number.isFinite(expiresAt) || JSON.stringify(refreshFinish.body?.scopes) !== JSON.stringify([controllerScope])) fail("refresh/finish schema mismatch");
  observe("refresh_finish", { status: refreshFinish.status, response_fields: Object.keys(refreshFinish.body).sort(), scopes: refreshFinish.body.scopes, expires_at_present: true });
  const environments = await pairedEnvironments(token, accountID, clientID);
  const items = environments.body?.items;
  if (!Array.isArray(items) || items.length === 0) fail("no paired environments returned");
  const selected = await selectEnvironment(items);
  observe("environment_selected", { env: "env_selected", selected_index: selected.index + 1, online: selected.environment.online, client_type: selected.environment.client_type, os: selected.environment.os });
  const wssResult = await openControllerWSS({ token, accountID, controllerToken, controllerExpiresAt: expiresAt, clientID, key, helperBinary: helper.binary, environment: selected.environment });
  observe("initialize_complete", { result_fields: wssResult.initializeResultFields, cursor_present: wssResult.cursor != null, reconnect_status: wssResult.reconnectStatus });
  const revoke = await revokeProbeController(token, accountID, clientID);
  cleanupStatus = revoke.status;
  if (!revoke.ok) fail(`probe controller revoke failed with HTTP ${revoke.status}`);
  controllerRevoked = true;
  observe("revoke", { status: revoke.status, ok: true, controller: "client_probe" });
  try {
    await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/refresh/start", { client_id: clientID });
    fail("revoked controller identity was accepted");
  } catch (error) {
    if (![401, 403, 404].includes(error.status)) throw error;
    observe("revoked_identity_rejected", { operation: "refresh_start", http_status: error.status, result: "rejected" });
  }
  console.log(`REDACTED_FIXTURE=${JSON.stringify({ schema_version: 1, classification: "LIVE-REDACTED-OBSERVATION", target: { chatgpt_desktop_version: "26.825.32147", embedded_codex_version: "codex-cli 0.150.0-alpha.12.2", controller_protocol_version: 3 }, timeline })}`);
  if (wssResult.reconnectStatus !== "passed") fail("cursor reconnect evidence unavailable");
} catch (error) {
  observe("probe_failed", { error: error.message, http_status: error.status ?? null, body_bytes: error.bodyBytes ?? null, body_sha256: error.bodySha256 ?? null });
  process.exitCode = 1;
} finally {
  if (!controllerRevoked && clientID != null && token != null) {
    try {
      const identity = authIdentity(token);
      const response = await revokeProbeController(token, identity.accountId, clientID);
      cleanupStatus = response.status;
      observe("controller_cleanup", { status: response.status, ok: response.ok });
    } catch (error) { observe("controller_cleanup_failed", { error: error.message }); }
  }
  if (key != null && helper != null) {
    try { const deleted = keyCall(helper.binary, { op: "delete", keyId: key.keyId }); observe("device_key_cleanup", { deleted: deleted.deleted }); }
    catch (error) { observe("device_key_cleanup_failed", { error: error.message }); }
  }
  if (helper != null && helper.directory.startsWith(path.join(os.tmpdir(), "codex-remote-key-helper."))) rmSync(helper.directory, { recursive: true, force: true });
  token = null;
  stepUpToken = null;
  controllerToken = null;
}
