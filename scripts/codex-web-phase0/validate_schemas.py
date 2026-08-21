#!/usr/bin/env python3
"""Phase 0 schema 冻结的自动校验（p0-schemas-tests 证据，可复跑）。

对目标二进制生成的 stable/experimental schema bundle 逐项断言设计 §7.3 的 11 个 surface。
断言的"期望值"来自 pinned source /Users/jacklee/Projects/codex @536f86e5 的 typed shape
（交叉核对过程见 section7.3-crosscheck.md）。

用法：python3 scripts/codex-web-phase0/validate_schemas.py
退出码 0 = 全部通过。
"""
import json
import pathlib
import sys

BASE = pathlib.Path(__file__).parent / "schemas"
SRC = pathlib.Path("/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/v2")

failures = []


def check(name, cond, detail=""):
    status = "PASS" if cond else "FAIL"
    print(f"[{status}] {name}" + (f" — {detail}" if detail else ""))
    if not cond:
        failures.append(name)


def load(variant, rel):
    return json.loads((BASE / variant / rel).read_text())


def defs(variant):
    b = json.loads((BASE / variant / "codex_app_server_protocol.v2.schemas.json").read_text())
    return b.get("definitions") or b.get("$defs") or {}


def methods(variant, fname):
    doc = load(variant, fname)

    def consts_of(schema):
        out = []
        for sub in schema.get("oneOf", []):
            if "$ref" in sub:
                node = doc
                for part in sub["$ref"].lstrip("#/").split("/"):
                    node = node.get(part, {}) if isinstance(node, dict) else {}
                m = node.get("properties", {}).get("method", {})
                out.extend(m.get("enum", []) if "enum" in m else ([m["const"]] if "const" in m else []))
            else:
                m = sub.get("properties", {}).get("method", {})
                out.extend(m.get("enum", []) if "enum" in m else ([m["const"]] if "const" in m else []))
        return out

    return set(consts_of(doc))


# 0. 全部 schema 文件可解析
n = 0
for variant in ("stable", "experimental"):
    for p in (BASE / variant).rglob("*.json"):
        json.loads(p.read_text())
        n += 1
check("all-schema-files-parse", True, f"{n} files")

stable_defs = defs("stable")
exp_defs = defs("experimental")

# 1. model/list：data: Vec<Model> + nextCursor；typed Model 无 provider 字段
mlr = stable_defs["ModelListResponse"]
model_props = stable_defs["Model"]["properties"]
check("s1-model-list-shape",
      list(mlr["properties"].keys()) == ["data", "nextCursor"],
      f"keys={list(mlr['properties'].keys())}")
check("s1-model-no-provider",
      "provider" not in model_props and "modelProvider" not in model_props,
      f"Model 字段数={len(model_props)}")

# 2. thread/start：stable 含 model+modelProvider；allowProviderModelFallback 等 experimental 字段被剥除
tsp_s = load("stable", "v2/ThreadStartParams.json")["properties"]
tsp_e = load("experimental", "v2/ThreadStartParams.json")["properties"]
check("s2-thread-start-model-provider-stable",
      "model" in tsp_s and "modelProvider" in tsp_s)
check("s2-thread-start-experimental-stripped",
      "allowProviderModelFallback" not in tsp_s and "allowProviderModelFallback" in tsp_e)

# 3. turn/start：可选 model，无 modelProvider
turn_s = load("stable", "v2/TurnStartParams.json")["properties"]
turn_e = load("experimental", "v2/TurnStartParams.json")["properties"]
check("s3-turn-start-model-optional-no-provider",
      "model" in turn_s and "modelProvider" not in turn_s and "modelProvider" not in turn_e)

# 4. turn/steer：expectedTurnId 必填；experimental 字段 stable 剥除
steer_s = load("stable", "v2/TurnSteerParams.json")
steer_e = load("experimental", "v2/TurnSteerParams.json")["properties"]
req = steer_s.get("required", [])
check("s4-turn-steer-expected-turn-id-required",
      "expectedTurnId" in req and steer_s["properties"]["expectedTurnId"]["type"] == "string",
      f"required={req}")
check("s4-turn-steer-experimental-stripped",
      "responsesapiClientMetadata" not in steer_s["properties"]
      and "responsesapiClientMetadata" in steer_e)

# 5. config/read：请求无 experimental 字段；Config snake_case + typed model_provider + flatten additional；
#    ConfigReadResponse/Config 带 ExperimentalApi（源码断言）
crp = load("stable", "v2/ConfigReadParams.json")["properties"]
check("s5-config-read-params-no-experimental",
      set(crp.keys()) == {"includeLayers", "cwd"}, f"keys={sorted(crp.keys())}")
cfg = stable_defs["Config"]
check("s5-config-snake-case-model-provider",
      "model_provider" in cfg["properties"],
      f"snake keys={[k for k in cfg['properties'] if '_' in k][:3]}...")
check("s5-config-flatten-additional",
      cfg.get("additionalProperties") is True)
apps_in_exp = "apps" in exp_defs["Config"]["properties"]
check("s5-config-apps-field-experimental",
      "apps" not in cfg["properties"] and apps_in_exp)

# 6. requestUserInput：doc-comment EXPERIMENTAL 但无属性门控 → 出现在 stable ServerRequest；
#    questions 批结构 + answers 按 id
sr_stable = methods("stable", "ServerRequest.json")
sr_exp = methods("experimental", "ServerRequest.json")
# ServerRequest 载荷类型生成在 bundle 顶层（非 v2/ 子目录）
ui = load("stable", "ToolRequestUserInputParams.json")["properties"]
ui_resp = load("stable", "ToolRequestUserInputResponse.json")["properties"]
check("s6-request-user-input-in-stable-server-request",
      "item/tool/requestUserInput" in sr_stable)
check("s6-request-user-input-batch-shape",
      "questions" in ui and ui["questions"]["type"] == "array"
      and "isBlocking" in ui and "answers" in ui_resp)
ui_doc = load("stable", "ToolRequestUserInputParams.json")
q_props = ui_doc["definitions"]["ToolRequestUserInputQuestion"]["properties"]
check("s6-question-has-id-options",
      set(["id", "header", "question", "options"]).issubset(q_props.keys()))

# 7. command approval：availableDecisions/additionalPermissions 字段级 experimental（stable schema 剥除）；
#    server 出站 strip 只剥 additionalPermissions（源码事实，见 crosscheck 报告——此断言核对 schema 侧）
cap_s = load("stable", "CommandExecutionRequestApprovalParams.json")["properties"]
cap_e = load("experimental", "CommandExecutionRequestApprovalParams.json")["properties"]
check("s7-command-approval-experimental-fields-schema-stripped",
      "availableDecisions" not in cap_s and "availableDecisions" in cap_e
      and "additionalPermissions" not in cap_s and "additionalPermissions" in cap_e)
check("s7-command-approval-base-stable",
      set(["threadId", "turnId", "itemId", "startedAtMs", "command"]).issubset(cap_s.keys()))

# 8. permission approval：RequestPermissionProfile；响应 GrantedPermissionProfile+scope；无 availableDecisions
pap = load("stable", "PermissionsRequestApprovalParams.json")["properties"]
par = load("stable", "PermissionsRequestApprovalResponse.json")["properties"]
check("s8-permission-approval-shape",
      "permissions" in pap and "availableDecisions" not in pap
      and set(["permissions", "scope"]).issubset(par.keys()))

# 9. elicitation：Form / openai/form / Url 三 variant；capability mcpServerOpenaiFormElicitation
eli = load("stable", "McpServerElicitationRequestParams.json")
eli_variants = {k for k in eli.get("oneOfProperties", {}) or {}} if "oneOfProperties" in eli else set()
# schema gen 可能用 oneOf；兼容两种表达
if not eli_variants:
    variants = set()
    for sub in eli.get("oneOf", []):
        if "$ref" in sub:
            variants.add(sub["$ref"].split("/")[-1])
        else:
            variants.update(sub.get("properties", {}).keys())
else:
    variants = eli_variants
check("s9-elicitation-variants",
      "mcpServer/elicitation/request" in sr_stable
      and ("OpenAiForm" in str(variants) or "openai/form" in json.dumps(eli)),
      f"variants={sorted(variants)[:6]}")

# 10. plan：item/plan/delta 在 stable ServerNotification（doc-comment EXPERIMENTAL、无属性门控）；
#     item/started、item/completed 稳定存在
sn_stable = methods("stable", "ServerNotification.json")
check("s10-plan-delta-in-stable-bundle",
      "item/plan/delta" in sn_stable and "item/started" in sn_stable and "item/completed" in sn_stable)

# 11. turn/completed：Turn{status,error,items,itemsView}；TurnItemsView = NotLoaded/Summary/Full
turn_def = stable_defs["Turn"]
items_view = {v for sub in stable_defs["TurnItemsView"].get("oneOf", []) for v in sub.get("enum", [])}
check("s11-turn-completed-items-view",
      set(items_view) == {"notLoaded", "summary", "full"},
      f"enum={sorted(items_view)}")
check("s11-turn-has-status-error-items",
      set(["status", "error", "items", "itemsView"]).issubset(turn_def["properties"].keys()))

# 12. 方法门控：thread/turns/list 仅 experimental（设计：experimental 分页）
cr_stable = methods("stable", "ClientRequest.json")
cr_exp = methods("experimental", "ClientRequest.json")
check("s12-thread-turns-list-experimental-only",
      "thread/turns/list" not in cr_stable and "thread/turns/list" in cr_exp)
check("s12-core-stable-methods-present",
      set(["thread/list", "thread/read", "thread/start", "thread/resume", "thread/archive",
           "thread/unarchive", "thread/delete", "thread/fork", "thread/unsubscribe",
           "thread/name/set", "turn/start", "turn/steer", "turn/interrupt",
           "model/list", "modelProvider/capabilities/read", "config/read",
           "permissionProfile/list"]).issubset(cr_stable))

# 13. pinned source 事实断言（源码 grep 级）
item_rs = (SRC / "item.rs").read_text()
check("s13-source-strip-only-additional-permissions",
      "self.additional_permissions = None;" in item_rs
      and item_rs.count("fn strip_experimental_fields") == 1,
      "strip_experimental_fields 只置空 additional_permissions（availableDecisions 不剥）")
common_rs = (SRC.parent / "common.rs").read_text()
check("s13-plan-delta-no-attr-gating",
      'PlanDelta => "item/plan/delta"' in common_rs
      and '#[experimental("item/plan/delta")]' not in common_rs,
      "PlanDelta 注册无 #[experimental] 属性，仅 doc 注释")
check("s13-mcp-openai-form-capability",
      '"mcpServerOpenaiFormElicitation": true' in common_rs)

print()
if failures:
    print(f"FAILED {len(failures)}: {failures}")
    sys.exit(1)
print("ALL PASS")
