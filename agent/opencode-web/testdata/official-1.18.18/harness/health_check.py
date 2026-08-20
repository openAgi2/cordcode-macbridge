#!/usr/bin/env python3
"""Harness health: 1.18.18 + connected={localmock} + prompt hits 127.0.0.1:4399.

Does not fail if /provider.all contains the full official registry.
Never talks to 127.0.0.1:4096.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
import uuid
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
import base64

FORBIDDEN = "4096"
VERSION = "1.18.18"


def die(msg: str, evidence: dict | None = None) -> None:
    if evidence is not None:
        print(json.dumps(evidence, indent=2, ensure_ascii=False)[:4000], file=sys.stderr)
    print(f"health_check FAIL: {msg}", file=sys.stderr)
    raise SystemExit(1)


def http(base: str, auth: str, method: str, path: str, directory: str, body=None, timeout=20):
    if FORBIDDEN in base:
        die("refusing managed serve port 4096")
    q = {"directory": directory}
    url = base.rstrip("/") + path + "?" + urlencode(q)
    headers = {
        "Authorization": auth,
        "Accept": "application/json",
        "x-opencode-directory": directory,
    }
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = Request(url, data=data, method=method, headers=headers)
    try:
        with urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            parsed = json.loads(raw.decode("utf-8") or "null") if raw else None
            return resp.status, parsed
    except HTTPError as exc:
        raw = exc.read()
        try:
            parsed = json.loads(raw.decode("utf-8") or "null")
        except json.JSONDecodeError:
            parsed = raw.decode("utf-8", "replace")
        return exc.code, parsed


def listen_pid(port: int) -> str | None:
    try:
        out = subprocess.check_output(
            ["lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN", "-t"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except subprocess.CalledProcessError:
        return None
    return out.splitlines()[0] if out else None


def listen_cmd(port: int) -> str:
    try:
        return subprocess.check_output(
            ["lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
    except subprocess.CalledProcessError:
        return ""


def msgid() -> str:
    alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
    n = int(time.time() * 1000) * 0x1000 + (uuid.uuid4().int & 0xFFF)
    out = []
    x = n
    for _ in range(16):
        out.append(alphabet[x % 62])
        x //= 62
    return "msg" + "".join(reversed(out)) + uuid.uuid4().hex[:10]


def connected_ids(catalog: dict) -> list[str]:
    raw = catalog.get("connected")
    if isinstance(raw, list):
        out = []
        for item in raw:
            if isinstance(item, str):
                out.append(item)
            elif isinstance(item, dict) and item.get("id"):
                out.append(str(item["id"]))
        return out
    return []


def all_ids(catalog: dict) -> list[str]:
    raw = catalog.get("all")
    if not isinstance(raw, list):
        return []
    out = []
    for item in raw:
        if isinstance(item, dict) and item.get("id"):
            out.append(str(item["id"]))
        elif isinstance(item, str):
            out.append(item)
    return out


def localmock_models(catalog: dict) -> list[str]:
    models = []
    for item in catalog.get("all") or []:
        if not isinstance(item, dict) or item.get("id") != "localmock":
            continue
        md = item.get("models") or {}
        if isinstance(md, dict):
            models.extend(sorted(md.keys()))
    return models


def credential_files(root: Path) -> list[str]:
    hits = []
    for p in root.rglob("*"):
        if not p.is_file():
            continue
        name = p.name.lower()
        if name in {"auth.json", "credentials.json", "mcp-auth.json"}:
            hits.append(str(p))
    return hits


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:4398")
    ap.add_argument("--user", default="gatea")
    ap.add_argument("--password", default="gatea-pass")
    ap.add_argument("--directory", required=True)
    ap.add_argument("--root", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    if FORBIDDEN in args.base:
        die("refusing 4096")

    root = Path(args.root)
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    auth = "Basic " + base64.b64encode(f"{args.user}:{args.password}".encode()).decode()
    pid_4096_before = listen_pid(4096)
    evidence: dict = {
        "meta": {
            "opencodeVersionExpected": VERSION,
            "base": args.base,
            "directory": "/tmp/ocw-gate-a/workspace",
            "managed4096PidBefore": pid_4096_before,
        }
    }

    code, health = http(args.base, auth, "GET", "/global/health", args.directory)
    evidence["health"] = {"status": code, "body": health}
    if code != 200 or not isinstance(health, dict) or health.get("version") != VERSION:
        die(f"/global/health expected 200 version={VERSION}", evidence)

    code, catalog = http(args.base, auth, "GET", "/provider", args.directory)
    evidence["providerStatus"] = code
    if code != 200 or not isinstance(catalog, dict):
        die("/provider failed", evidence)
    connected = connected_ids(catalog)
    all_provider_ids = all_ids(catalog)
    models = localmock_models(catalog)
    evidence["provider"] = {
        "allCount": len(all_provider_ids),
        "allIdsSample": all_provider_ids[:20],
        "connected": connected,
        "localmockModels": models,
        "note": "all may include the full official registry; only connected is the isolation gate",
    }
    if connected != ["localmock"]:
        die(f"connected must be exactly [localmock], got {connected}", evidence)
    if "echo" not in models:
        die(f"localmock connected catalog missing echo, models={models}", evidence)

    creds = credential_files(root)
    evidence["isolatedCredentialFiles"] = creds
    if creds:
        die("isolated XDG/HOME must not contain provider credential files", evidence)
    host_home = Path.home()
    if str(host_home) in str(root.resolve()):
        die("sandbox root is inside host home; isolation broken", evidence)

    # Minimal prompt to prove the serve actually calls 127.0.0.1:4399.
    mock_log = root / "logs" / "mock.jsonl"
    before_lines = mock_log.read_text(encoding="utf-8").splitlines() if mock_log.exists() else []
    code, created = http(args.base, auth, "POST", "/session", args.directory, body={})
    evidence["create"] = {"status": code, "idPresent": isinstance(created, dict) and bool(created.get("id"))}
    if code >= 300 or not isinstance(created, dict):
        die(f"create failed {code}", evidence)
    sid = created["id"]
    prompt = {
        "messageID": msgid(),
        "agent": "build",
        "model": {"providerID": "localmock", "modelID": "echo"},
        "parts": [{"type": "text", "text": "HARNESS_HEALTH_PROMPT SANDBOX_OK"}],
    }
    code, prompt_resp = http(
        args.base, auth, "POST", f"/session/{sid}/prompt_async", args.directory, body=prompt, timeout=20
    )
    evidence["prompt"] = {"status": code, "response": prompt_resp, "messageID": prompt["messageID"]}
    if code not in (200, 204):
        die(f"prompt_async HTTP {code}", evidence)

    # Bounded wait for the mock to see chat/completions.
    hit = False
    mock_rows = []
    for _ in range(40):
        time.sleep(0.25)
        if not mock_log.exists():
            continue
        mock_rows = [json.loads(line) for line in mock_log.read_text(encoding="utf-8").splitlines() if line.strip()]
        for row in mock_rows:
            if (
                row.get("path") in ("/v1/chat/completions", "/chat/completions")
                and row.get("method") == "POST"
                and row.get("scenario") == "text"
            ):
                hit = True
                break
        if hit:
            break
    new_rows = mock_rows[len(before_lines) :]
    evidence["mock"] = {
        "hitChatCompletions": hit,
        "newRequestCount": len(new_rows),
        "newRequests": [
            {k: r.get(k) for k in ("ts", "method", "path", "host", "scenario", "model", "userChars")}
            for r in new_rows
        ],
    }
    if not hit:
        die("mock at 127.0.0.1:4399 did not receive POST /v1/chat/completions", evidence)

    pid_4096_after = listen_pid(4096)
    evidence["managed4096"] = {
        "pidBefore": pid_4096_before,
        "pidAfter": pid_4096_after,
        "listen": listen_cmd(4096).strip(),
        "writesFromThisScript": False,
        "note": "health_check never opens 4096; pid must remain the owner managed listener",
    }
    if pid_4096_before and pid_4096_after and pid_4096_before != pid_4096_after:
        die("managed 4096 listener pid changed during harness health", evidence)

    evidence["ok"] = True
    out.write_text(json.dumps(evidence, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"health_check PASS: {out}")
    print(f"connected={connected} allCount={len(all_provider_ids)} mockHits={len(new_rows)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
