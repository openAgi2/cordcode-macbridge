# Codex apply_patch / fileChange 脱敏样本（Phase 0 硬门）

> **provenance**：`observed-derived-real-shape`。来源为本机 `~/.codex/sessions/` 真实 rollout
> jsonl，按 wire 原始 shape 保留；**uuid / call_id / threadId / turnId / 文件路径 / diff 正文均已脱敏**
> （替换为 `<redacted-*>` 或无意义占位 path，保留 `***` / `@@` 结构与字段名）。无密钥、无绝对个人路径、
> 无真实用户正文。覆盖
> [`docs/2026-08-01-chatgpt-style-tool-activity-display.md`](../../../../cordcode-ios/docs/2026-08-01-chatgpt-style-tool-activity-display.md)
> §6.5 Phase 0 Codex 样本硬门。

## 为什么需要这些样本

Codex 文件变更在 wire 上有两种独立形态，iOS 必须分别处理：

| 形态 | 来源 | 结构化 FileChanges？ | 本方案修改焦点 |
|------|------|---------------------|----------------|
| **A. apply_patch 文本** | `custom_tool_call` name=`apply_patch`，input=`*** Begin Patch\n*** Add/Update/Delete File: <path>…`；output=`Success. Updated the following files:\nA/M/D <path>` | ❌ 不产 structured（macbridge `codexCustomToolUse` 把 name 归一为 `Patch`、把首个 patch target 当 toolInput/title，**不**产 FileChanges） | Phase 1B：iOS 从 output/patch 文本解析 path/kind（双策略交叉） |
| **B. structured fileChange item** | appserver `item/started` type=`fileChange`，`changes` 为 **list 变体** `[{path,kind,diff,movePath}]` 或 **map 变体** `{path:{type/kind,unified_diff,move_path}}` | ✅ 产 structured FileChanges（`appServerFileChanges` / `appServerPatchChanges`），但 **hydration 丢弃** | Phase 1A C-P0a：hydration 透传 fileChanges；iOS 映射 |

## 样本清单

| 文件 | 形态 | 覆盖 | 关键字段 |
|------|------|------|----------|
| `apply_patch_add_file.json` | A 文本 | `*** Add File:`（create） | input=`*** Begin Patch\n*** Add File: …`；output 含 `A <path>` |
| `apply_patch_update_file.json` | A 文本 | `*** Update File:`（edit） | input=`*** Update File:` + `@@` hunk；output 含 `M <path>` |
| `apply_patch_delete_file.json` | A 文本 | `*** Delete File:`（delete） | input=`*** Delete File:`；output 含 `D <path>` |
| `filechange_list_variant.json` | B structured · list | `changes:[{path,kind,diff}]`（update+add） | `appServerFileChanges` 形态 |
| `filechange_map_variant.json` | B structured · map | `changes:{path:{type,unified_diff}}` | `appServerPatchChanges` 形态；`update`→`edit` 归一 |

## 使用

定向测试加载这些 fixture 并断言 path/kind/diffStats 提取（A 形态验证 iOS patch parser；
B 形态验证 macbridge appserver 解析 + hydration 透传 + iOS 映射）。
