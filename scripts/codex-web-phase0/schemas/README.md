# Phase 0 schema 冻结（目标二进制生成）

- 生成时间：2026-08-21（本地时区）
- 目标二进制：`/Applications/ChatGPT.app/Contents/Resources/codex`
  - `codex-cli 0.149.0-alpha.4`
  - sha256 前 20：`10afbeddd6f951635d8f`
- 生成环境：隔离 `CODEX_HOME`（临时目录，非用户真实 `~/.codex`）
- 生成命令：
  - stable：`codex app-server generate-json-schema --out schemas/stable`
  - experimental：`codex app-server generate-json-schema --experimental --out schemas/experimental`
- 设计 pin 记录的二进制为 `codex-cli 0.148.0-alpha.21` → **版本漂移已知**，按设计 §3.2
  以实施时实际安装版本为准；pinned 源码仍为 `536f86e5cc9ec1ff38457d099bf320b9d08eeeba`。
  schema 与 pinned source typed shape 的差异在 callsite/samples 交叉核对中逐项记录，
  若触发设计 §21.2 条件须回写设计 §7.3。

## 方法面枚举（由本 bundle 提取，供 §7.3 对照）

| 面 | stable | experimental-only |
|---|---:|---|
| ClientRequest | 95 | 55（含 `thread/turns/list`、`thread/items/list`、`thread/queue/*`、`project/*`、`thread/search`、`process/*`、`remoteControl/*`、`environment/*`、`memory/reset`、`thread/memoryMode/set`、`thread/increment/decrement_elicitation`、`collaborationMode/list`、`fuzzyFileSearch/session*`、`server/diagnostics`、`account/bedrock/*`、`plugin/search`、`thread/realtime/*`、`thread/backgroundTerminals/*`、`thread/settings/update`、`mock/experimentalMethod`） |
| ServerNotification | 75 | 0 |
| ServerRequest | 10 | 1（`currentTime/read`） |

## 与设计 §7.3 的初步差异点（须 tests 任务核对后裁决）

1. `item/plan/delta` 出现在 stable ServerNotification 枚举（两种 variant 均含）。
   设计 §7.3 标注其为 experimental 🧪。待核对 pinned source
   （`app-server-protocol/src/protocol/v2/`）中该通知的 ExperimentalApi 归属，以及真实
   样本中未开 `experimentalApi` 时是否物理到达；以样本为准并按 §3.0 回写。
2. `thread/turns/list` experimental-only —— 与设计一致 ✅。
3. `item/tool/requestUserInput`、`mcpServer/elicitation/request` 在 stable ServerRequest
   （不随 `--experimental` 剥除）—— 与设计"README 标 experimental 但 core 默认启用、
   不由 experimentalApi 门控"的双重事实方向一致，仍以真实样本冻结。
4. stable ClientRequest 已含 `modelProvider/capabilities/read`、`thread/fork`、
   `thread/read`、`thread/list`、`thread/inject_items`、`threadSection/*`、`fs/*`、
   `skills/*`、`hooks/list`、`mcpServer/*` 等本二进制较 0.148 扩展的面；codex-web
   一期只消费设计 §7 表内 ✅ 面。

## 目录

- `stable/`：无 `--experimental` 的生成 bundle（含 `v1/`、`v2/` 与聚合
  `codex_app_server_protocol*.schemas.json`）
- `experimental/`：`--experimental` 的生成 bundle
