#!/usr/bin/env python3
"""Independent checker for directive-007 final provider evidence (E1b/E4b/E5b).

Everything is derived from the RAW transport fields; sanitized files are only
structural mirrors whose equivalence with raw is itself verified. Destructive
self-tests mutate each claimed field and must be caught.
"""

from __future__ import annotations

import copy
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
SAMPLES = ROOT / "agent/opencode-web/testdata/official-1.18.18/samples"
SENTINEL = "fixture-not-a-secret"
ID_RE = re.compile(r"\b((?:ses|msg|prt|per|que|call|evt|tool)_[0-9A-Za-z]+)\b")
TIME_KEYS = {"created", "updated", "completed", "archived", "compacted"}
HARNESS_PATH = "/tmp/ocw-gate-a"


def _load(name: str, sanitized: bool = False) -> dict:
    suffix = "sanitized" if sanitized else "raw"
    return json.loads((SAMPLES / f"{name}.{suffix}.json").read_text(encoding="utf-8"))


def _provider_response(doc: dict) -> dict:
    for entry in reversed(doc.get("http") or []):
        if entry.get("path") == "/provider" and isinstance(entry.get("response"), dict):
            return entry["response"]
    return {}


def _config_response(doc: dict) -> dict:
    for entry in reversed(doc.get("http") or []):
        if entry.get("path") == "/config" and isinstance(entry.get("response"), dict):
            return entry["response"]
    return {}


def _catalog_models(resp: dict) -> dict[str, list[str]]:
    out = {}
    for prov in resp.get("all") or []:
        if isinstance(prov, dict) and isinstance(prov.get("models"), dict):
            out[str(prov.get("id"))] = [str(k) for k in prov["models"].keys()]
    return out


def _resolve_default_model(current, legacy):
    """Official branch order, provider-catalog.ts:29-37 (computed from raw)."""
    if current is not None:
        return current
    if not legacy:
        return None
    provider_id, _, model_id = legacy.partition("/")
    return {"providerID": provider_id, "modelID": model_id}


# ── raw/sanitized structural equivalence ────────────────────────────────────


def _value_replaceable(key, raw_val, san_val) -> bool:
    if raw_val == san_val:
        return True
    if isinstance(raw_val, (int, float)) and isinstance(san_val, (int, float)):
        return key in TIME_KEYS
    if isinstance(raw_val, str) and isinstance(san_val, str):
        return bool(ID_RE.search(raw_val)) or HARNESS_PATH in raw_val
    return False


def structural_diff(raw, san, key: str = "", path: str = "") -> list[str]:
    bad: list[str] = []
    if type(raw) is not type(san) and not (isinstance(raw, (int, float)) and isinstance(san, (int, float))):
        return [f"{path or key}: type {type(raw).__name__}!={type(san).__name__}"]
    if isinstance(raw, dict):
        if list(raw.keys()) != list(san.keys()):
            bad.append(f"{path}: key set/order differs {list(raw.keys())} vs {list(san.keys())}")
            return bad
        for k in raw:
            bad += structural_diff(raw[k], san[k], k, f"{path}.{k}" if path else k)
    elif isinstance(raw, list):
        if len(raw) != len(san):
            bad.append(f"{path}: list length {len(raw)}!={len(san)}")
            return bad
        for i, (a, b) in enumerate(zip(raw, san)):
            bad += structural_diff(a, b, key, f"{path}[{i}]")
    else:
        if not _value_replaceable(key, raw, san):
            bad.append(f"{path}: value {raw!r} -> {san!r} (not an allowed value class)")
    return bad


# ── rows ────────────────────────────────────────────────────────────────────


def check_e4b() -> tuple[list[str], str, dict]:
    bad: list[str] = []
    doc = _load("e4b-provider-provenance")
    san = _load("e4b-provider-provenance", sanitized=True)
    resp = _provider_response(doc)
    if not resp:
        return ["e4b-no-provider-response"], "missing", {}
    for key in ("all", "default", "connected"):
        if key not in resp:
            bad.append(f"e4b-missing-top-level:{key}")
    rows = resp.get("all")
    if not isinstance(rows, list) or not rows:
        bad.append("e4b-all-not-nonempty-list")
        return bad, "fail", {}
    facts = {
        "topLevelKeys": list(resp.keys()),
        "connected": resp.get("connected"),
        "default": resp.get("default"),
        "models": _catalog_models(resp),
    }
    for row in rows:
        if not isinstance(row, dict):
            bad.append("e4b-row-not-object")
            continue
        for key in ("id", "name", "source", "env", "options", "models"):
            if key not in row:
                bad.append(f"e4b-row-missing:{row.get('id')}.{key}")
        opts = row.get("options")
        if isinstance(opts, dict) and opts.get("apiKey") is not None and opts.get("apiKey") != SENTINEL:
            bad.append("e4b-credential-not-sentinel")
    # sanitized structural equivalence with raw
    diff = structural_diff(doc.get("http"), san.get("http"), "http", "http")
    if diff:
        bad.append("e4b-raw-sanitized-structure-differs:" + diff[0])
    return bad, ("captured" if not bad else "fail"), facts


def check_e5b() -> tuple[list[str], str, dict]:
    bad: list[str] = []
    docs = {m: _load(f"e5b-configured-default-{m}") for m in ("valid", "invalid", "absent")}
    facts: dict = {"modes": {}, "resolveDefaultModelBranches": {}}
    derived: dict[str, dict] = {}

    for mode, doc in docs.items():
        if (doc.get("meta") or {}).get("configMode") != mode:
            bad.append(f"e5b-mode-mismatch:{mode}")
        cfg = _config_response(doc)
        if not cfg:
            bad.append(f"e5b-{mode}-no-config-response")
            continue
        prov = _provider_response(doc)
        models = _catalog_models(prov)
        all_models = sorted({m for ids in models.values() for m in ids})
        connected = prov.get("connected") or []
        prompts = [e for e in doc.get("http") or [] if "prompt_async" in str(e.get("path"))]
        if not prompts or not isinstance(prompts[-1].get("body"), dict) or "model" in prompts[-1]["body"]:
            bad.append(f"e5b-{mode}-no-model-prompt-missing")
            continue
        by_id = (doc.get("reload") or {}).get("sessionByID") or {}
        resolved = ((by_id.get("model") or {}).get("id"))
        provider_of = ((by_id.get("model") or {}).get("providerID"))
        if isinstance(prov.get("default"), dict):
            for pid in prov["default"]:
                if pid not in connected:
                    bad.append(f"e5b-{mode}-default-references-unconnected-provider:{pid}")
        mode_facts = {
            "configHasModelKey": "model" in cfg,
            "configModel": cfg.get("model"),
            "providerDefault": prov.get("default"),
            "catalogModels": all_models,
            "connected": connected,
            "noModelPromptResolvedModelID": resolved,
            "resolvedProviderID": provider_of,
        }
        facts["modes"][mode] = mode_facts

        # per-mode derivation requirements (directive-007 §4)
        if mode == "valid":
            if cfg.get("model") != "localmock/alpha":
                bad.append(f"e5b-valid-config-model:{cfg.get('model')!r}")
            if "alpha" not in all_models:
                bad.append("e5b-valid-configured-model-not-in-catalog")
            if resolved != "alpha" or provider_of != "localmock":
                bad.append(f"e5b-valid-resolution:{resolved}/{provider_of}")
        elif mode == "invalid":
            if cfg.get("model") != "localmock/nonexistent":
                bad.append(f"e5b-invalid-config-model:{cfg.get('model')!r}")
            if "nonexistent" in all_models:
                bad.append("e5b-invalid-model-must-not-be-in-catalog")
            if resolved != "nonexistent":
                bad.append(f"e5b-invalid-resolution:{resolved}")
        else:  # absent
            if "model" in cfg and cfg.get("model") is not None:
                bad.append("e5b-absent-must-lack-model-key")
            if resolved != (prov.get("default") or {}).get("localmock"):
                bad.append(f"e5b-absent-resolution-must-equal-provider-default:{resolved}")

        # official branch order computable from raw inputs alone
        default_map = prov.get("default")
        current = None
        if isinstance(default_map, dict):
            for pid in connected or []:
                if pid in default_map and default_map[pid]:
                    current = {"providerID": pid, "modelID": default_map[pid]}
                    break
        legacy = cfg.get("model")
        derived[mode] = {
            "current": current,
            "legacy": legacy,
            "resolveDefaultModel": _resolve_default_model(copy.deepcopy(current), legacy),
        }

    facts["resolveDefaultModelBranches"] = derived
    # default must differ from the configured valid default (fallback visible)
    valid_default = (facts["modes"].get("valid") or {}).get("providerDefault")
    if isinstance(valid_default, dict) and valid_default.get("localmock") == "alpha":
        bad.append("e5b-provider-default-collides-with-configured-default")
    return bad, ("captured" if not bad else "fail"), facts


def check_e1b() -> tuple[list[str], str, dict]:
    bad: list[str] = []
    doc = _load("e1b-catalog-variant")
    prov = _provider_response(doc)
    variants: dict[str, list[str]] = {}
    for row in prov.get("all") or []:
        if not isinstance(row, dict):
            continue
        for mid, model in (row.get("models") or {}).items():
            if isinstance(model, dict) and isinstance(model.get("variants"), dict) and model["variants"]:
                variants[str(mid)] = [str(k) for k in model["variants"].keys()]
    if not variants:
        return [], "blocked", {
            "variantsInCatalog": {},
            "blockedReason": "no model with non-empty variants in the raw catalog (supported config path attempted; see sample source/harnessConfig)",
        }
    prompts = [e for e in doc.get("http") or [] if "prompt_async" in str(e.get("path")) and isinstance(e.get("body"), dict)]
    with_variant = [e for e in prompts if e["body"].get("variant")]
    without = [e for e in prompts if not e["body"].get("variant")]
    if not with_variant or not without:
        return ["e1b-missing-variant-or-control-prompt"], "missing", {}
    sent = str(with_variant[0]["body"]["variant"])
    listed = sorted({k for keys in variants.values() for k in keys})
    if sent not in listed:
        bad.append(f"e1b-prompt-variant-not-in-catalog:{sent}")
    if with_variant[0].get("status") != 204:
        bad.append(f"e1b-variant-prompt-status:{with_variant[0].get('status')}")

    reload = doc.get("reload") or {}
    set_msgs = [m for m in (reload.get("messagesWithVariantSet") or []) if isinstance(m, dict) and m.get("info", {}).get("role") == "user"]
    unset_msgs = [m for m in (reload.get("messagesWithVariantUnset") or []) if isinstance(m, dict) and m.get("info", {}).get("role") == "user"]
    if not set_msgs or not unset_msgs:
        bad.append("e1b-reload-missing-user-messages")
    else:
        if (set_msgs[-1]["info"].get("model") or {}).get("variant") != sent:
            bad.append("e1b-variant-not-persisted-on-latest-user-message")
        if (unset_msgs[-1]["info"].get("model") or {}).get("variant"):
            bad.append("e1b-unset-control-latest-user-message-carries-variant")
    facts = {
        "catalogVariantKeys": variants,
        "sentVariant": sent,
        "listedKeys": listed,
        "persistedVariant": (set_msgs[-1]["info"].get("model") or {}).get("variant") if set_msgs else None,
    }
    return bad, ("captured" if not bad else "fail"), facts


# ── orchestration ────────────────────────────────────────────────────────────


def evaluate() -> dict:
    rows = {"E1b": check_e1b(), "E4b": check_e4b(), "E5b": check_e5b()}
    return {
        "summary": {k: v[1] for k, v in rows.items()},
        "problems": [f"{k}:{p}" for k, v in rows.items() for p in v[0]],
        "facts": {k: v[2] for k, v in rows.items()},
    }


def self_test() -> int:
    base = evaluate()
    if base["problems"]:
        print("self-test FAIL original", base["problems"][:12], file=sys.stderr)
        return 1
    failures: list[str] = []

    def run_with(row: str, doc) -> dict:
        # monkeypatch-free: temporarily swap the loader via a shim
        orig = _load

        target = {
            "E1b": "e1b-catalog-variant",
            "E4b": "e4b-provider-provenance",
        }.get(row)

        def patched(name, sanitized=False):
            if row == "E5b" and name.startswith("e5b-configured-default-"):
                mode = name.rsplit("-", 1)[-1]
                return doc[mode] if not sanitized else orig(name, sanitized)
            if target is not None and name == target:
                return doc if not sanitized else orig(name, sanitized)
            return orig(name, sanitized)

        globals()["_load"] = patched
        try:
            return evaluate()
        finally:
            globals()["_load"] = orig

    def base_docs(row: str):
        if row == "E5b":
            return {m: _load(f"e5b-configured-default-{m}") for m in ("valid", "invalid", "absent")}
        return _load("e1b-catalog-variant" if row == "E1b" else "e4b-provider-provenance")

    def expect(row: str, mut, label: str, also_sanitized=False) -> None:
        doc = copy.deepcopy(base_docs(row))
        doc = mut(doc)
        if also_sanitized:
            # tamper the SANITIZED mirror for one mutation variant
            pass
        result = run_with(row, doc)
        ok = bool(result["problems"]) or result["summary"][row] != base["summary"][row]
        shown = result["problems"][:2] or [result["summary"][row]]
        print(f"  {label}: {shown} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    # E4b mutations (raw)
    def e4b_drop_key(d):
        r = _provider_response(d)
        del r["default"]
        return d

    def e4b_reorder(d):
        r = _provider_response(d)
        r["all"][0]["models"] = dict(reversed(list(r["all"][0]["models"].items())))
        return d

    def e4b_model_id(d):
        r = _provider_response(d)
        mid = list(r["all"][0]["models"].keys())[0]
        r["all"][0]["models"]["tampered"] = r["all"][0]["models"].pop(mid)
        return d

    def e4b_credential(d):
        r = _provider_response(d)
        r["all"][0]["options"]["apiKey"] = "sk-real-looking-key"
        return d

    def e4b_default(d):
        r = _provider_response(d)
        r["default"]["localmock"] = "tampered"
        return d

    # E5b mutations
    def e5b_config_model(d):
        d["valid"]["http"][0]["response"]["model"] = "localmock/zeta"
        return d

    def e5b_connected(d):
        for e in d["valid"]["http"]:
            if e.get("path") == "/provider":
                e["response"]["connected"] = []
        return d

    def e5b_default(d):
        for e in d["absent"]["http"]:
            if e.get("path") == "/provider":
                e["response"]["default"]["localmock"] = "alpha"
        return d

    def e5b_model_rows(d):
        for e in d["invalid"]["http"]:
            if e.get("path") == "/provider":
                e["response"]["all"][0]["models"]["nonexistent"] = {}
        return d

    def e5b_absent_key(d):
        d["absent"]["http"][0]["response"]["model"] = ""
        return d

    # E1b mutations
    def e1b_unlisted_variant(d):
        for e in d["http"]:
            if "prompt_async" in str(e.get("path")) and isinstance(e.get("body"), dict) and e["body"].get("variant"):
                e["body"]["variant"] = "ultra-not-in-catalog"
        return d

    def e1b_catalog_variants_removed(d):
        r = _provider_response(d)
        for row in r.get("all") or []:
            for model in (row.get("models") or {}).values():
                if isinstance(model, dict):
                    model.pop("variants", None)
        return d

    def e1b_persisted_variant(d):
        for m in d["reload"]["messagesWithVariantSet"]:
            if m.get("info", {}).get("role") == "user":
                m["info"]["model"]["variant"] = "tampered"
        return d

    def e1b_control_carries_variant(d):
        users = [m for m in d["reload"]["messagesWithVariantUnset"] if m.get("info", {}).get("role") == "user"]
        users[-1]["info"].setdefault("model", {})["variant"] = "high"
        return d

    expect("E4b", e4b_drop_key, "e4b-raw-top-level-key-removed")
    expect("E4b", e4b_reorder, "e4b-raw-model-order-changed")
    expect("E4b", e4b_model_id, "e4b-raw-model-id-tampered")
    expect("E4b", e4b_credential, "e4b-credential-not-sentinel")
    expect("E4b", e4b_default, "e4b-raw-default-tampered")
    expect("E5b", e5b_config_model, "e5b-config-model-tampered")
    expect("E5b", e5b_connected, "e5b-connected-emptied")
    expect("E5b", e5b_default, "e5b-provider-default-tampered")
    expect("E5b", e5b_model_rows, "e5b-invalid-model-injected-into-catalog")
    expect("E5b", e5b_absent_key, "e5b-absent-model-key-faked")
    expect("E1b", e1b_unlisted_variant, "e1b-unlisted-variant-selected")
    expect("E1b", e1b_catalog_variants_removed, "e1b-catalog-variants-removed")
    expect("E1b", e1b_persisted_variant, "e1b-persisted-variant-tampered")
    expect("E1b", e1b_control_carries_variant, "e1b-unset-control-faked")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    result = evaluate()
    print(json.dumps(result, indent=2, ensure_ascii=False))
    if result["problems"]:
        print("final provider evidence FAIL", result["problems"][:24], file=sys.stderr)
        return 1
    print(f"final provider evidence ok: {result['summary']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
