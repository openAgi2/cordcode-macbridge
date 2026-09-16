#!/usr/bin/env python3
"""Offline re-verification for ask_evidence_probe dumps.

Re-runs the fixed pairing/shape checks (ask-scoped tool_result matching,
out-frame request_id pairing, envelope shape) over an existing dump WITHOUT
re-running the model turn. Produces the same verdict block the probe writes,
so archived dumps captured before a probe fix can still yield clean gate
verdicts.

Usage:
    ./reverify_ask_dump.py ask-file-answer   # reads dumps/ask-file-answer.jsonl
"""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from ask_evidence_probe import recursive_find  # noqa: E402

DUMPS = Path(__file__).resolve().parent / "dumps"


def reverify(name: str) -> dict:
    frames = []
    for ln in (DUMPS / f"{name}.jsonl").read_text().splitlines():
        rec = json.loads(ln)
        if rec.get("dir") in ("in", "out") and rec.get("line", "").strip().startswith("{"):
            frames.append(rec)

    ins = [json.loads(r["line"]) for r in frames if r["dir"] == "in"]
    outs = [json.loads(r["line"]) for r in frames if r["dir"] == "out"]

    control_request = next(
        (o for o in ins if o.get("type") == "control_request"
         and (o.get("request") or {}).get("tool_name") == "AskUserQuestion"), None)
    tool_use = next(
        (o for o in recursive_find(ins, lambda o: o.get("type") == "tool_use"
                                  and o.get("name") == "AskUserQuestion")), None)
    verdicts = {}
    if not (control_request and tool_use):
        verdicts["captured"] = {"ok": False,
                                "control_request": control_request is not None,
                                "tool_use": tool_use is not None}
        return verdicts

    ask_id = tool_use.get("id")
    req_id = control_request.get("request_id")
    req_tool_use_id = (control_request.get("request") or {}).get("tool_use_id")

    tool_result = next(
        (o for o in recursive_find(ins, lambda o: o.get("type") == "tool_result"
                                  and o.get("tool_use_id") == ask_id)), None)

    sent = next(
        (o for o in outs if o.get("type") == "control_response"
         and (o.get("response") or {}).get("request_id") == req_id), None)
    behavior = ((sent or {}).get("response") or {}).get("response", {}).get("behavior")

    # Frame-level envelope on the user frame that owns the ask's tool_result.
    envelope = None
    for o in ins:
        if o.get("type") == "user" and "tool_use_result" in o:
            for b in (o.get("message") or {}).get("content") or []:
                if isinstance(b, dict) and b.get("type") == "tool_result" \
                        and b.get("tool_use_id") == ask_id:
                    envelope = o["tool_use_result"]
                    break
        if envelope is not None:
            break

    verdicts["captured_control_request"] = {"ok": True, "request_id": req_id}
    verdicts["captured_tool_use"] = {"ok": True, "id": ask_id}
    verdicts["captured_ask_tool_result"] = {
        "ok": tool_result is not None,
        "tool_use_id": ask_id,
        "content_preview": (json.dumps(tool_result.get("content"), ensure_ascii=False)[:300]
                            if tool_result else None)}
    verdicts["request_id_pairing"] = {
        "ok": sent is not None,
        "request_id": req_id,
        "behavior_sent": behavior,
        "note": "verified against archived out frame"}
    verdicts["tool_use_id_three_way"] = {
        "ok": tool_result is not None
        and req_tool_use_id == ask_id == tool_result.get("tool_use_id"),
        "control": req_tool_use_id, "tool_use": ask_id,
        "tool_result": (tool_result or {}).get("tool_use_id")}
    verdicts["tool_use_result_envelope_shape"] = {
        "ok": envelope is not None,
        "envelope_type": type(envelope).__name__ if envelope is not None else None,
        "envelope_preview": (json.dumps(envelope, ensure_ascii=False)[:400]
                             if envelope is not None else None)}

    # Strict-path + recursive parity on the ask-scoped objects.
    rec_req = [o for o in recursive_find(ins, lambda o: o.get("type") == "control_request"
                                       and (o.get("request") or {}).get("tool_name") == "AskUserQuestion")]
    rec_tu = [o for o in recursive_find(ins, lambda o: o.get("type") == "tool_use"
                                      and o.get("name") == "AskUserQuestion")]
    rec_tr = [o for o in recursive_find(ins, lambda o: o.get("type") == "tool_result"
                                      and o.get("tool_use_id") == req_tool_use_id)]
    verdicts["recursive_scan_parity"] = {
        "ok": (len(rec_req) >= 1 and len(rec_tu) >= 1 and len(rec_tr) >= 1
              and rec_req[0].get("request_id") == req_id
              and (rec_req[0].get("request") or {}).get("tool_use_id") == rec_tu[0].get("id")
              and rec_tu[0].get("id") == rec_tr[0].get("tool_use_id")),
        "recursive_control_requests": len(rec_req),
        "recursive_tool_uses": len(rec_tu),
        "recursive_ask_tool_results": len(rec_tr)}
    return verdicts


if __name__ == "__main__":
    name = sys.argv[1]
    verdicts = reverify(name)
    out = DUMPS / f"{name}-reverify.json"
    out.write_text(json.dumps({"scenario": name, "verdicts": verdicts},
                              ensure_ascii=False, indent=2))
    print(json.dumps(verdicts, ensure_ascii=False, indent=2))
    print(f"\nwritten: {out}")
