// Shared controller-session machinery for Phase 0 probes.
//
// Extracted verbatim in behavior from probe/live_controller.mjs (which keeps its
// own inline copy; that proven probe is intentionally left untouched). New
// probes import this module instead of duplicating the enrollment flow.
//
// Contract per probe/README.md: in-memory credentials only, official origin and
// frozen path allowlist, independent probe key, deterministic revoke of ONLY the
// probe controller, redacted output only.

import { spawn, spawnSync } from "node:child_process";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const WebSocket = require("/opt/homebrew/lib/node_modules/wscat/node_modules/ws");

export const base = "https://chatgpt.com/backend-api";
export const websocketURL = "wss://chatgpt.com/backend-api/codex/remote/control/client";
export const codexPath = "/Applications/ChatGPT.app/Contents/Resources/codex";
export const scope = "codex.remote_control.enroll";
export const controllerScope = "remote_control_controller_websocket";
export const oauthClientID = "app_EMoamEEZ73f0CkXaXp7hrann";

export function fail(message) {
  throw new Error(message);
}

export function withTimeout(promise, ms, message) {
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

export function jwtClaims(token) {
  const parts = token.split(".");
  if (parts.length < 2) fail("malformed JWT");
  return JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
}

export function authIdentity(token) {
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

export async function jsonRequest(token, accountID, method, pathname, body) {
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
    const error = new Error(`official request failed with HTTP ${response.status} on ${method} ${pathname}`);
    error.status = response.status;
    error.pathname = pathname;
    error.requestMethod = method;
    error.bodyBytes = Buffer.byteLength(text);
    error.bodySha256 = createHash("sha256").update(text).digest("hex");
    if (parsed != null && typeof parsed === "object") {
      error.bodyKeys = Object.keys(parsed).sort();
      const detail = parsed.detail ?? parsed.error ?? parsed.code ?? parsed.message;
      if (typeof detail === "string" && detail.length <= 80 && !/eyJ|Bearer |-----BEGIN/u.test(detail)) {
        error.bodyDetail = detail;
      } else if (detail != null && typeof detail === "object") {
        error.bodyDetailKeys = Object.keys(detail).sort();
      }
    }
    throw error;
  }
  return { status: response.status, body: parsed, bodySha256: createHash("sha256").update(text).digest("hex") };
}

export function loadAuthToken() {
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

export function compileKeyHelper(helperSource) {
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

export function keyCall(binary, request) {
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

export async function stepUp(accountID) {
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
  spawnSync("/usr/bin/open", [authorize.toString()], { stdio: "ignore" });
  try {
    const { code } = await withTimeout(codePromise, 600_000, "step-up timeout");
    const response = await fetch("https://auth.openai.com/oauth/token", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, redirect_uri: redirectURI, client_id: oauthClientID, code_verifier: verifier }), signal: AbortSignal.timeout(15_000) });
    const parsed = await response.json();
    if (!response.ok || typeof parsed.access_token !== "string") fail(`step-up exchange failed with HTTP ${response.status}`);
    return parsed.access_token;
  } finally { await closeLocalServer(server); }
}

export async function collectManualPairingCode(formTitle, instructionsHtml) {
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
          response.end(`<!doctype html><meta charset="utf-8"><title>${formTitle}</title><meta name="referrer" content="no-referrer"><style>body{font:16px system-ui;max-width:42rem;margin:4rem auto;padding:0 1rem}input,button{font:inherit;padding:.7rem}input{width:24rem;max-width:90%}</style><h1>${formTitle}</h1>${instructionsHtml}<form method="post" action="/pair?state=${formToken}" autocomplete="off"><input type="password" name="code" required autofocus autocomplete="off" maxlength="256"><button type="submit">Pair</button></form>`);
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
  spawnSync("/usr/bin/open", [inputURL], { stdio: "ignore" });
  try {
    return await withTimeout(codePromise, 600_000, "pairing input timeout");
  } finally {
    await closeLocalServer(server);
  }
}

export async function pairedEnvironments(token, accountID, clientID, pairingForm) {
  const pathname = `/codex/remote/control/clients/${encodeURIComponent(clientID)}/environments?limit=100`;
  let environments = await jsonRequest(token, accountID, "GET", pathname);
  if (!Array.isArray(environments.body?.items)) fail("client environment list schema mismatch");
  if (environments.body.items.length > 0) return environments;

  let manualPairingCode = await pairingForm();
  try {
    const paired = await jsonRequest(token, accountID, "POST", "/wham/remote/control/client/pair", { client_id: clientID, manual_pairing_code: manualPairingCode });
    void paired;
  } finally {
    manualPairingCode = null;
  }
  for (let attempt = 1; attempt <= 10; attempt += 1) {
    environments = await jsonRequest(token, accountID, "GET", pathname);
    if (!Array.isArray(environments.body?.items)) fail("client environment list schema mismatch after pair");
    if (environments.body.items.length > 0) return environments;
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  fail("paired controller still has no environments");
}

export async function revokeProbeController(token, accountID, clientID) {
  const response = await fetch(`${base}/wham/remote/control/clients/${encodeURIComponent(clientID)}`, { method: "DELETE", headers: headers(token, accountID), redirect: "error", signal: AbortSignal.timeout(15_000) });
  await response.arrayBuffer();
  return response;
}

export function selectEnvironment(items) {
  const onlineDesktop = items
    .map((item, index) => ({ item, index }))
    .filter(({ item }) => item.online === true && item.client_type === "CODEX_DESKTOP_APP");
  const chosen = onlineDesktop.length === 1 ? onlineDesktop[0] : (items.length === 1 ? { item: items[0], index: 0 } : null);
  if (chosen == null) fail("cannot auto-select Desktop environment");
  return { environment: chosen.item, index: chosen.index };
}

export function redactErrorMessage(message) {
  if (typeof message !== "string") return null;
  return message
    .replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/giu, "<id>")
    .replace(/\b[0-9a-f]{16,}\b/giu, "<id>");
}

export function threadStatusType(status) {
  if (typeof status === "string") return status;
  if (status != null && typeof status === "object") return typeof status.type === "string" ? status.type : null;
  return null;
}

// Full official enrollment: enroll/start -> step-up -> enroll/finish ->
// refresh/start -> refresh/finish. Returns controller session credentials.
export async function enrollController({ helperSource, observe = () => {}, pairingForm }) {
  const helper = compileKeyHelper(helperSource);
  try {
    const token = await loadAuthToken();
    const identity = authIdentity(token);
    if (identity.accountID == null && identity.accountId == null) fail("account id claim missing");
    const accountID = identity.accountId ?? identity.accountID;
    observe("auth_loaded", { method: "embedded_app_server_memory_only" });
    const start = await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/enroll/start", {});
    const clientID = start.body?.client_id;
    const challenge = start.body?.device_key_challenge;
    if (typeof clientID !== "string" || challenge == null) fail("enroll/start schema mismatch");
    observe("enroll_start", { status: start.status, response_fields: Object.keys(start.body).sort(), challenge_fields: Object.keys(challenge).sort() });
    const key = keyCall(helper.binary, { op: "create" });
    observe("device_key_created", { algorithm: key.algorithm, protection_class: key.protectionClass, spki_bytes: Buffer.from(key.publicKeySpkiDerBase64, "base64").length });
    let stepUpToken = await stepUp(accountID);
    const stepClaims = jwtClaims(stepUpToken);
    const stepScopes = [...new Set([...(stepClaims.scope?.split(/\s+/u) ?? []), ...(stepClaims.scp ?? [])].filter(Boolean))];
    if (stepScopes.length !== 1 || stepScopes[0] !== scope || Math.floor(Date.now() / 1000) - stepClaims.iat > 300 || Date.now() - stepClaims.pwd_auth_time > 300_000) fail("step-up token validation failed");
    observe("step_up_validated", { scopes: stepScopes, fresh: true });
    const finish = await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/enroll/finish", { client_id: clientID, step_up_token: stepUpToken, device_identity: { key_id: key.keyId, public_key_spki_der_base64: key.publicKeySpkiDerBase64, algorithm: key.algorithm, protection_class: key.protectionClass }, device_key_proof: enrollmentProof({ challenge, clientID, key, helperBinary: helper.binary, expectedPath: "/backend-api/codex/remote/control/client/enroll/finish", requireDeviceIdentityHash: false }) });
    let controllerToken = finish.body?.remote_control_token;
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
    return { helper, token, accountID, clientID, key, controllerToken, expiresAt };
  } catch (error) {
    if (helper.directory.startsWith(path.join(os.tmpdir(), "codex-remote-key-helper."))) rmSync(helper.directory, { recursive: true, force: true });
    throw error;
  }
}

export async function cleanupController(enrollment, observe = () => {}) {
  const { helper, token, accountID, clientID, key } = enrollment;
  let revoked = false;
  if (clientID != null && token != null) {
    try {
      const response = await revokeProbeController(token, accountID, clientID);
      observe("revoke", { status: response.status, ok: response.ok, controller: "client_probe" });
      revoked = response.ok;
      if (response.ok) {
        try {
          await jsonRequest(token, accountID, "POST", "/codex/remote/control/client/refresh/start", { client_id: clientID });
          fail("revoked controller identity was accepted");
        } catch (error) {
          if (![401, 403, 404].includes(error.status)) throw error;
          observe("revoked_identity_rejected", { operation: "refresh_start", http_status: error.status, result: "rejected" });
        }
      }
    } catch (error) {
      observe("controller_cleanup_failed", { error: error.message });
    }
  }
  if (key != null && helper != null) {
    try { const deleted = keyCall(helper.binary, { op: "delete", keyId: key.keyId }); observe("device_key_cleanup", { deleted: deleted.deleted }); }
    catch (error) { observe("device_key_cleanup_failed", { error: error.message }); }
  }
  if (helper != null && helper.directory.startsWith(path.join(os.tmpdir(), "codex-remote-key-helper."))) rmSync(helper.directory, { recursive: true, force: true });
  return revoked;
}

// Promise-based JSON-RPC session over the controller WSS. Mirrors the
// challenge/initialize flow of live_controller.mjs runControllerWSS, but exposes
// a sequential rpc() call API instead of the fixed resume flow.
export function openRpcSession({ enrollment, environment, observe = () => {}, onLiveMessage = null, idleTimeoutMs = 120_000 }) {
  const { token, accountID, clientID, key, controllerToken, expiresAt, helper } = enrollment;
  return new Promise((resolve, reject) => {
    const requestHeaders = headers(token, accountID, {
      "x-codex-client-id": clientID,
      "x-codex-protocol-version": "3",
      "x-codex-client-session-token": `Bearer ${controllerToken}`,
    });
    const ws = new WebSocket(websocketURL, { headers: requestHeaders, handshakeTimeout: 10_000, perMessageDeflate: false });
    const streamID = randomUUID();
    let timeout = setTimeout(() => { ws.terminate(); reject(new Error("controller WSS timeout")); }, 60_000);
    let challengeComplete = false;
    let nextClientSeq = 1;
    let nextRpcId = 2;
    let keepAlive = null;
    let closed = false;
    const pending = new Map();
    const session = {
      streamID,
      environmentPseudonym: "env_selected",
      liveMessages: [],
      rpc: null,
      close: null,
    };
    const armTimeout = (ms) => {
      clearTimeout(timeout);
      timeout = setTimeout(() => { ws.terminate(); reject(new Error("controller WSS timeout")); }, ms);
    };
    const sendClientMessage = (method, message) => {
      const seqID = nextClientSeq;
      nextClientSeq += 1;
      ws.send(JSON.stringify({ type: "client_message", client_id: clientID, env_id: environment.env_id, stream_id: streamID, seq_id: seqID, skip_history: false, message }));
      return seqID;
    };
    session.rpc = (method, params, { timeoutMs = 30_000 } = {}) => {
      if (closed) return Promise.reject(new Error("session closed"));
      const rpcId = nextRpcId;
      nextRpcId += 1;
      const startedAt = Date.now();
      return new Promise((resolveRpc, rejectRpc) => {
        const timer = setTimeout(() => {
          pending.delete(rpcId);
          rejectRpc(new Error(`rpc timeout: ${method}`));
        }, timeoutMs);
        pending.set(rpcId, (message) => {
          clearTimeout(timer);
          const rpc = message.message ?? {};
          resolveRpc({
            method,
            ms: Date.now() - startedAt,
            error: rpc.error ?? null,
            result: rpc.result ?? null,
          });
        });
        sendClientMessage(method, { id: rpcId, method, params: params ?? {} });
      });
    };
    session.close = () => new Promise((resolveClose) => {
      if (closed) { resolveClose(); return; }
      closed = true;
      clearTimeout(timeout);
      if (keepAlive != null) clearInterval(keepAlive);
      for (const settle of pending.values()) settle({ message: { message: { error: { code: -1, message: "session closed" } } } });
      ws.once("close", () => resolveClose());
      ws.close(1000);
      setTimeout(() => ws.terminate(), 2_000).unref();
    });
    ws.on("open", () => observe("websocket_handshake", { status: "open", protocol_version: 3 }));
    ws.on("error", (error) => { clearTimeout(timeout); reject(new Error(`controller WSS error: ${error.message}`)); });
    ws.on("message", (data) => {
      let message;
      try { message = JSON.parse(String(data)); } catch { clearTimeout(timeout); ws.terminate(); reject(new Error("unknown WSS payload")); return; }
      if (!challengeComplete) {
        const expectedHash = createHash("sha256").update(controllerToken).digest("base64url");
        if (message.type !== "device_key_challenge" || message.clientId !== clientID || message.targetOrigin !== "https://chatgpt.com" || message.targetPath !== "/backend-api/codex/remote/control/client" || message.tokenSha256Base64url !== expectedHash || message.tokenExpiresAt !== expiresAt || JSON.stringify(message.scopes) !== JSON.stringify([controllerScope])) {
          clearTimeout(timeout); ws.terminate(); reject(new Error("WSS challenge mismatch")); return;
        }
        const proofBytes = Buffer.from(JSON.stringify({ domain: "codex-device-key-sign-payload/v1", payload: { accountUserId: message.accountUserId, audience: message.audience, clientId: message.clientId, nonce: message.nonce, scopes: message.scopes, sessionId: message.sessionId, targetOrigin: message.targetOrigin, targetPath: message.targetPath, tokenExpiresAt: message.tokenExpiresAt, tokenSha256Base64url: message.tokenSha256Base64url, type: "remoteControlClientConnection" } }), "utf8");
        const signed = keyCall(helper.binary, { op: "sign", keyId: key.keyId, payloadBase64: proofBytes.toString("base64") });
        ws.send(JSON.stringify({ type: "device_key_proof", keyId: key.keyId, signatureDerBase64: signed.signatureDerBase64, signedPayloadBase64: proofBytes.toString("base64"), algorithm: signed.algorithm }));
        observe("device_key_proof", { algorithm: signed.algorithm, key: "probe_key", signature_encoding: "der_base64", signed_payload_encoding: "base64" });
        challengeComplete = true;
        observe("device_key_challenge", { fields: Object.keys(message).sort(), target_origin: message.targetOrigin, target_path: message.targetPath, scopes: message.scopes });
        void session.rpc("initialize", { clientInfo: { name: "codex_remote_phase0_probe", title: "CordCode Phase 0 Probe", version: "0" }, capabilities: { experimentalApi: true } })
          .then((init) => {
            if (init.error != null) throw new Error(`initialize failed: ${redactErrorMessage(init.error.message) ?? "rpc error"}`);
            sendClientMessage("initialized", { method: "initialized", params: {} });
            observe("initialize_result", { result_fields: Object.keys(init.result ?? {}).sort() });
            keepAlive = setInterval(() => {
              if (closed) return;
              const seqID = nextClientSeq;
              nextClientSeq += 1;
              ws.send(JSON.stringify({ type: "ping", client_id: clientID, env_id: environment.env_id, stream_id: streamID, seq_id: seqID, state: "foreground", skip_history: true }));
            }, 10_000);
            armTimeout(idleTimeoutMs);
            resolve(session);
          })
          .catch((error) => { clearTimeout(timeout); ws.terminate(); reject(error); });
        return;
      }
      if (message.type === "server_message") {
        const rpc = message.message ?? {};
        const rpcId = rpc.id;
        const method = typeof rpc.method === "string" ? rpc.method : null;
        if (rpcId != null && pending.has(rpcId)) {
          const settle = pending.get(rpcId);
          pending.delete(rpcId);
          settle(message);
          armTimeout(idleTimeoutMs);
          return;
        }
        if (method != null) {
          session.liveMessages.push(method);
          if (onLiveMessage != null) onLiveMessage(method, rpc);
        }
        return;
      }
      if (message.type === "pong" && message.status !== "active") {
        clearTimeout(timeout); ws.terminate(); reject(new Error("pong status mismatch")); return;
      }
    });
  });
}
