#!/usr/bin/env python3
"""§4.6 evidence-gate probe: AskUserQuestion answer/deny pairing on the live CLI.

Design: docs/2026-09-16-claudecode-userinput-ios-answer-design.md §4.6.
Target: Claude Code 2.1.261 (the version gate that blocks live canRespond flip).

What one scenario captures, end to end:
  1. assistant transcript frame carrying the AskUserQuestion tool_use (id)
  2. control_request {request_id, request:{subtype:can_use_tool, tool_name:
     AskUserQuestion, tool_use_id, input:{questions}}}
  3. our control_response (answer: behavior=allow + updatedInput.answers;
     deny: behavior=deny) — echoed by the CLI
  4. user transcript frame carrying the tool_result (tool_use_id)
  5. the turn's result frame

Cross-checks recorded into the summary (strict-path + recursive-scan parity,
request_id pairing, tool_use_id three-way link):
  response.request_id == request.request_id
  request.tool_use_id == assistant tool_use id == tool_result.tool_use_id

Two scenarios:
  ask-answer  — reply with the first option (behavior allow + updatedInput)
  ask-deny    — reply with behavior deny

Usage:
    ./ask_evidence_probe.py ask-answer
    ./ask_evidence_probe.py ask-deny

Env model, spawn args, redaction and dump format are shared with
control_plane_probe.py (same Probe class, imported from it).
"""

import json
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from control_plane_probe import Probe, req, DUMPS_DIR  # noqa: E402

# One real model turn that is instructed to ask exactly one single-select
# question. The wording pins the option labels so the answer path can pick
# option 1 verbatim; the deny path never references options.
ASK_PROMPT = (
    "Call the AskUserQuestion tool exactly once with a single-select question "
    'asking "Continue?" that has exactly two options: "Yes" and "No". '
    "Do not answer it yourself; just ask, then follow the user's answer."
)

# Owner-provided real-world scenario (2026-09-16): write a story to an
# already-existing file so the CLI raises its native file-conflict
# AskUserQuestion (multi-select box with overwrite/append/... options).
FILE_ASK_PROMPT = (
    "新建一个 /Users/jacklee/Projects/Chat/漫威战斗故事.txt，里面写1个故事大概100字。"
    "如果文件已经存在，应该弹出多选框问我如何处理"
)

TURN_TIMEOUT = 180.0  # wall clock for the whole turn (model latency included)


def find_tool_use(obj: dict) -> dict | None:
    """Return the AskUserQuestion content block from an assistant frame."""
    if obj.get("type") != "assistant":
        return None
    for block in (obj.get("message") or {}).get("content") or []:
        if isinstance(block, dict) and block.get("type") == "tool_use" \
                and block.get("name") == "AskUserQuestion":
            return block
    return None


def find_tool_result(obj: dict, ask_tool_use_id: str | None = None) -> dict | None:
    """Return the first tool_result content block from a user frame.

    With ask_tool_use_id set, only the tool_result belonging to the
    AskUserQuestion tool_use matches (a real turn also carries Bash/Read/
    Write tool_results whose ids must not be confused with the ask's).
    """
    if obj.get("type") != "user":
        return None
    for block in (obj.get("message") or {}).get("content") or []:
        if isinstance(block, dict) and block.get("type") == "tool_result":
            if ask_tool_use_id is None or block.get("tool_use_id") == ask_tool_use_id:
                return block
    return None


def recursive_find(obj, pred):
    """Recursive scan: yield every object matching pred anywhere in the tree."""
    if isinstance(obj, dict):
        if pred(obj):
            yield obj
        for v in obj.values():
            yield from recursive_find(v, pred)
    elif isinstance(obj, list):
        for v in obj:
            yield from recursive_find(v, pred)


def run_scenario(name: str, mode: str, prompt: str = ASK_PROMPT) -> None:
    p = Probe(name)
    p.start()
    p.meta(scenario=name, mode=mode, cli_version_hint="2.1.261 (PATH)")

    # Drain startup frames, then initialize the control plane.
    end = time.monotonic() + 8
    while time.monotonic() < end:
        line = p.read_line(1.0)
        if line is None:
            break
        p.record("in", line)
    p.send(req("req_init", {"subtype": "initialize"}))
    p.expect_response("req_init", "initialize")

    p.send({"type": "user", "message": {"role": "user", "content": prompt}})

    control_request = None
    tool_use = None
    tool_result = None
    tool_use_result_envelope = None
    sent_response = None
    deadline = time.monotonic() + TURN_TIMEOUT

    while time.monotonic() < deadline:
        line = p.read_line(min(2.0, deadline - time.monotonic()))
        if line is None:
            if p.proc.poll() is not None:
                break
            continue
        p.record("in", line)
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue

        if control_request is None and obj.get("type") == "control_request":
            request = obj.get("request") or {}
            if request.get("tool_name") == "AskUserQuestion":
                control_request = obj
                p.meta(ask_control_request_seen={
                    "request_id": obj.get("request_id"),
                    "tool_use_id": request.get("tool_use_id"),
                })
                # Reply immediately: answer = allow + updatedInput.answers;
                # deny = behavior deny. This is the write whose transcript
                # pairing the evidence gate must prove.
                if mode == "answer":
                    questions = (request.get("input") or {}).get("questions") or []
                    first_q = questions[0] if questions else {}
                    first_label = None
                    for opt in first_q.get("options") or []:
                        if isinstance(opt, dict) and opt.get("label"):
                            first_label = opt["label"]
                            break
                    answers = {first_q.get("question", ""): first_label or "Yes"}
                    sent_response = {
                        "behavior": "allow",
                        "updatedInput": {
                            "questions": questions,
                            "answers": answers,
                        },
                    }
                else:
                    sent_response = {
                        "behavior": "deny",
                        "message": "User declined to answer this question.",
                    }
                p.send({
                    "type": "control_response",
                    "response": {
                        "subtype": "success",
                        "request_id": obj.get("request_id"),
                        "response": sent_response,
                    },
                })
                p.meta(control_response_sent={
                    "mode": mode,
                    "request_id": obj.get("request_id"),
                })
                continue

        if tool_use is None:
            block = find_tool_use(obj)
            if block is not None:
                tool_use = block
                p.meta(ask_tool_use_seen={"id": block.get("id")})

        if tool_result is None:
            # Match by the ask's tool_use id when known (turns also carry
            # Bash/Read/Write tool_results with their own ids).
            block = find_tool_result(obj, tool_use.get("id") if tool_use else None)
            if block is not None:
                tool_result = block
                p.meta(ask_tool_result_seen={"tool_use_id": block.get("tool_use_id")})

        # Frame-level tool_use_result (outside message.content): on 2.1.261 this
        # carries the structured questions+answers envelope for answer and an
        # "Error: ..." string for deny — the durable shapes the gate decides on.
        if tool_use_result_envelope is None and obj.get("type") == "user" \
                and "tool_use_result" in obj:
            tool_use_result_envelope = obj["tool_use_result"]
            p.meta(tool_use_result_envelope_seen={
                "type": type(tool_use_result_envelope).__name__,
            })

        if obj.get("type") == "result":
            p.meta(turn_result_subtype=obj.get("subtype"))
            break

    # ---- verification (strict path + recursive scan parity) ------------------

    def verify(label: str, ok: bool, detail: dict) -> None:
        p.verdicts[label] = {"ok": ok, **detail}
        p.meta(verdict=f"{label}:{'ok' if ok else 'FAIL'}")

    verify("captured_control_request", control_request is not None,
           {"request_id": (control_request or {}).get("request_id")})
    verify("captured_tool_use", tool_use is not None,
           {"id": (tool_use or {}).get("id")})
    verify("captured_tool_result", tool_result is not None,
           {"tool_use_id": (tool_result or {}).get("tool_use_id")})

    if control_request and tool_use and tool_result:
        req_id = control_request.get("request_id")
        req_tool_use_id = (control_request.get("request") or {}).get("tool_use_id")
        # The CLI does NOT echo can_use_tool control_responses back on stdout
        # (2.1.261 observed); pairing is proven against OUR archived out frame:
        # the response we sent must carry the same request_id the request used.
        sent_req_id = None
        if sent_response is not None:
            for r in p.records:
                if r.get("dir") != "out":
                    continue
                try:
                    out_obj = json.loads(r["line"])
                except json.JSONDecodeError:
                    continue
                resp = out_obj.get("response") or {}
                if out_obj.get("type") == "control_response" and resp.get("request_id") == req_id:
                    sent_req_id = resp.get("request_id")
                    break
        verify("request_id_pairing",
               sent_req_id == req_id,
               {"request_id": req_id, "sent_request_id": sent_req_id,
                "note": "verified against archived out frame (CLI does not echo can_use_tool responses)"})
        verify("tool_use_id_three_way",
               req_tool_use_id == tool_use.get("id") == tool_result.get("tool_use_id"),
               {"control": req_tool_use_id, "tool_use": tool_use.get("id"),
                "tool_result": tool_result.get("tool_use_id")})

        # Recursive-scan parity: same three objects must be findable by pure
        # recursive traversal of the archived frames (no path assumptions).
        frames = [json.loads(r["line"]) for r in p.records
                  if r.get("dir") == "in" and r["line"].strip().startswith("{")]
        rec_req = [o for o in recursive_find(frames, lambda o: o.get("type") == "control_request"
                                             and (o.get("request") or {}).get("tool_name") == "AskUserQuestion")]
        rec_tu = [o for o in recursive_find(frames, lambda o: o.get("type") == "tool_use"
                                          and o.get("name") == "AskUserQuestion")]
        rec_tr = [o for o in recursive_find(frames, lambda o: o.get("type") == "tool_result"
                                          and o.get("tool_use_id") == req_tool_use_id)]
        verify("recursive_scan_parity",
               len(rec_req) >= 1 and len(rec_tu) >= 1 and len(rec_tr) >= 1
               and rec_req[0].get("request_id") == req_id
               and (rec_req[0].get("request") or {}).get("tool_use_id") == rec_tu[0].get("id")
               and rec_tu[0].get("id") == rec_tr[0].get("tool_use_id"),
               {"recursive_control_requests": len(rec_req),
                "recursive_tool_uses": len(rec_tu),
                "recursive_tool_results": len(rec_tr)})

        # Durable shape of the tool_result content (answer vs deny distinction).
        content = tool_result.get("content")
        verify("tool_result_durable_shape",
               content is not None,
               {"content_type": type(content).__name__,
                "content_preview": json.dumps(content, ensure_ascii=False)[:400]
                if content is not None else None})

        # Frame-level tool_use_result envelope: the gate decision input. answer
        # must yield the structured {questions, answers} dict; deny must yield
        # a distinguishable (Error: ...) string shape.
        if tool_use_result_envelope is not None:
            if mode == "answer":
                env_ok = isinstance(tool_use_result_envelope, dict) \
                    and "answers" in tool_use_result_envelope \
                    and "questions" in tool_use_result_envelope
            else:
                env_ok = isinstance(tool_use_result_envelope, str) \
                    and tool_use_result_envelope.strip().startswith("Error:")
            verify("tool_use_result_envelope_shape",
                   env_ok,
                   {"mode": mode,
                    "envelope_type": type(tool_use_result_envelope).__name__,
                    "envelope_preview": json.dumps(tool_use_result_envelope, ensure_ascii=False)[:400]})
        else:
            verify("tool_use_result_envelope_shape", False,
                   {"mode": mode, "note": "no frame-level tool_use_result captured"})

    p.finish()


SCENARIOS = {
    "ask-answer": lambda: run_scenario("ask-answer", "answer"),
    "ask-deny": lambda: run_scenario("ask-deny", "deny"),
    "ask-file-answer": lambda: run_scenario("ask-file-answer", "answer", FILE_ASK_PROMPT),
    "ask-file-deny": lambda: run_scenario("ask-file-deny", "deny", FILE_ASK_PROMPT),
}

if __name__ == "__main__":
    if len(sys.argv) != 2 or sys.argv[1] not in SCENARIOS:
        print(__doc__)
        sys.exit(2)
    DUMPS_DIR.mkdir(parents=True, exist_ok=True)
    SCENARIOS[sys.argv[1]]()
    print("done")
