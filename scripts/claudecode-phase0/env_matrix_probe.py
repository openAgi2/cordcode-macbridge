#!/usr/bin/env python3
"""Phase 0.6 env priority matrix probe (design §3.4 / §6 Phase 0.6).

Controlled combinations of (process env ANTHROPIC_MODEL) x (settings-layer
env/model via --settings) x (--model flag), judged by the assistant
message.model actually executed (plus system/init .model = requested slot).

Each combo = fresh production-surface spawn + one tiny real turn.
Results appended to dumps/env-matrix.json (machine-readable) and printed.
"""

import json
import time

from control_plane_probe import Probe, DUMPS_DIR

OUT = DUMPS_DIR / "env-matrix.json"

HAIKU = "claude-haiku-4-5-20251001"

COMBOS = [
    # (name, process_env_overrides, extra_spawn_args)
    ("A-baseline", {}, []),
    ("B-procenv-model-haiku", {"ANTHROPIC_MODEL": HAIKU}, []),
    ("C-flag-model-haiku", {}, ["--model", "haiku"]),
    ("D-settings-model-sonnet", {}, ["--settings", '{"model":"sonnet"}']),
    ("E-settings-env-model-haiku", {}, ["--settings", json.dumps({"env": {"ANTHROPIC_MODEL": HAIKU}}, separators=(",", ":"))]),
    ("F-procenv-model-empty", {"ANTHROPIC_MODEL": ""}, []),
]


def run_combo(name, env_over, extra_args):
    p = Probe(f"envmx-{name}")
    p.env_overrides = env_over
    p.start(extra_args=extra_args or None)
    p.meta(combo=name, env_overrides={k: (v if v else "<empty>") for k, v in env_over.items()},
           extra_args=extra_args)
    p.send({"type": "user", "message": {"role": "user", "content": "Reply with exactly one word: pong"}})
    init_model = None
    assistant_models = []
    end = time.monotonic() + 120
    while time.monotonic() < end:
        line = p.read_line(2.0)
        if line is None:
            if p.proc.poll() is not None:
                break
            continue
        p.record("in", line)
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        t, st = obj.get("type"), obj.get("subtype")
        if t == "system" and st == "init":
            init_model = obj.get("model")
        if t == "assistant":
            m = obj.get("message") or {}
            if m.get("model"):
                assistant_models.append(m.get("model"))
        if t == "result":
            break
    p.finish()
    return {"combo": name, "init_model": init_model,
            "assistant_message_model": sorted(set(assistant_models))}


def main():
    DUMPS_DIR.mkdir(parents=True, exist_ok=True)
    results = []
    for name, env_over, extra in COMBOS:
        r = run_combo(name, env_over, extra)
        results.append(r)
        print(json.dumps(r, ensure_ascii=False))
        with open(OUT, "w") as f:
            json.dump({"captured_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                       "cli": "2.1.234", "results": results}, f, ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
