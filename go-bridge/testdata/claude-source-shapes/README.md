# Claude source-shape fixtures（合成；IR-6）

> **作者归属**：agent 生成。**全部为手工合成样本，零用户内容**——uuid/message.id/timestamp/body
> 均为占位符，按 `2026-07-30-remote-web-…-investigation.md` 观察到的 corpus 形状构造。不来自任何
> 真实 `~/.claude` transcript。provenance 统一为 `observed-derived-synthetic`（见 `manifest.json`）。
> 用途：H3/H4 修复的定向测试 fixture + source-shape 行为表（IR-5）验证。CI 测这些 fixture，**不**测
> 当前 `~/.claude`。

## 交付物

| 文件 | 角色 |
|---|---|
| `*.jsonl`（9 个） | 合成 fixture，shape 来自 corpus 审计 |
| `manifest.json` | machine-readable manifest：每文件 sha256 + 每条 record 的 `[byteStart,byteEnd)` + provenance + 采样时间 |
| `recompute_manifest.py` | **CI oracle**：从 fixture 字节重算 sha256 + byte range，断言与 manifest 一致（离线版；CI 内由 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 执行同一校验） |
| `recompute_corpus_stats.py` | **非 CI / owner 本地**：从 live `~/.claude` 重算 H3/H4 corpus 统计；已修复旧 README 内联脚本的两个 bug（见脚本头） |
| `../../claude_source_shapes_test.go` | CI 单测：加载全部 fixture + 校验 manifest + 锁定当前 mapper 行为（characterization） |

## Fixture 清单（canonical table）

| file | shape category | 关键关系 | 当前 mapper 行为（characterization，**pre-D/pre-H4**） |
|---|---|---|---|
| `exact-text-replay.jsonl` | H3 exact replay | row4=row1 同 `uuid`+`message.id`+`timestamp`+body，仅 `parentUuid` 不同（reparent 到 turn B） | **重复**：assistant 正文在 turn A 和 turn B 各发一次（`PreD` 测试锁定 = 2 次）。D 实现后期望 = 1 次 |
| `tool-result-extension.jsonl` | tool_result prefix extension | 同 `uuid`、`type=user`/`role=user`；occ2 在 occ1 的 call-1 前缀上**追加新 tool_use_id call-2** | **重复**：`tool_finished(call-1)` 发 2 次 + call-2 发 1 次（`PreD` 测试锁定）。D prefix-extension 后期望 call-1=1、call-2=1 |
| `branch-fileorder-interleave.jsonl` | H4 file-order ≠ parent-chain | `a-2` parent-chain `a-2→a-1→u-A`=turn A，但 file-order currentTurn=`u-B` | **误归属**：`a-2` 被归到 turn B（`PreH4` 测试锁定）。H4 实现后期望 = turn A |
| `attachment-replay.jsonl` | 顶层 `type=attachment` replay | 同 `uuid` attachment row 出现 2 次，**无 `message` 字段**；attachment 是顶层 type 非 block kind | **inert**：0 projection 事件（控制面） |
| `last-prompt.jsonl` | 顶层 `type=last-prompt` + `leafUuid` | 无 uuid/parent/message；H4 v1 明确**不**用 leafUuid 隐藏 branch | **inert**：0 事件 |
| `queue-operation.jsonl` | 顶层 `type=queue-operation` | 无 uuid/parent；`{operation,timestamp,sessionId,content}` | **inert**：0 事件 |
| `server-tool-use.jsonl` | `server_tool_use`(assistant) + 匹配 `tool_result`(user) | server_tool_use 结果以标准 tool_result 返回（tool_use_id 匹配），无 `server_tool_result` block 类型 | **orphan finish**：不发 tool_started，只发 tool_finished（F6 gap） |
| `image-block.jsonl` | image block 嵌在 tool_result content 内 | `{type:image, source:{type:base64,...}}` 是 block，非顶层 row、非 attachment | image 二进制被丢，只提取兄弟 text block |
| `system-subtypes.jsonl` | 四种真实 system subtype | `compact_boundary` / `stop_hook_summary` / `api_error` / `informational`（非 `system.compact`/`system.stop_hook`） | 仅 `compact_boundary`→`system_message`；其余 inert |
| `cold-start-edit-filepath.jsonl` | Claude 冷启动 Edit turn（R3/1D 硬门） | assistant `tool_use` name=Edit，**`input.file_path` 存在** + 匹配 user `tool_result`（英文 success） | **path 丢失**：cold-start hydrate 读此 transcript 回放，`richHistoryMessageBuilder.addToolUse` 把 title 写成 toolName 且丢弃 input → iOS 冷启动无 path（R5）。L-α 修复后期望 builder 保留 input / title=file_path。**provenance: observed-derived-real-shape**，非 IR-6 合成集 |

每条 fixture 均为合法 Claude JSONL（`type`/`uuid`/`message.{id,role,content}`/`parentUuid`/`timestamp`），LF 结尾、完整 record；`manifest.json` 记录每条 record 的精确字节范围。

## 诚实边界（characterization ≠ D/H4 已实现）

`claude_source_shapes_test.go` 里的 `*_PreD` / `*_PreH4` 测试**锁定的是当前（有 bug 的）mapper 行为**，
不是「D/H4 已实现并通过」。它们的价值是：

1. 证明 fixture 合法、mapper 不崩、manifest 与字节一致；
2. 把重复 / 误归属 bug 钉成可回归基线——当 IR-1a（D）/ H4 落地时，把期望值从「2 次 / turn B」
   翻成「1 次 / turn A」，测试即转为修复的守护。

在 IR-6 测试 + IR-3 schema 关闭前，H3/H4 仍不标 implementation-ready（见调查文档 gate 表）。

## corpus 统计的口径漂移

`recompute_corpus_stats.py` 的 H4 mismatch 总数依赖「哪些 user row 推进 file-order currentTurnID」。
脚本用「非空 text user row 推进」的保守口径，结果会与按 mapper 真实语义（跳过 internal compact /
resume meta）的口径不同。**以 fixture + 单元测试为准**，不以 live corpus 动态总数为准。脚本输出绑定
运行时刻，`~/.claude` 持续写入，不得当永久常量。
