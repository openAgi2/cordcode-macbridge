# Phase 0 §12 真实样本包（隔离 CODEX_HOME）

> 注：本 README 在最终全量重采（harness 定稿版）后恢复重写；样本裁决事实与首次采集一致
> （availableDecisions/config additional/-32600 等关键断言由 `validate_samples.py` 复核）。

- 采集时间：2026-08-22（本地，harness 定稿版全量重采）
- CLI：`codex-cli 0.149.0-alpha.4`（`/Applications/ChatGPT.app/Contents/Resources/codex`）
- transport：stdio（newline JSON-RPC）；lifecycle 组含 daemon 子命令与 managed WS
- 模型上游：本地 mock Responses provider（`../mock_provider.py`）——只控制上游脚本
  （delta 数、reasoning、exec_command/request_user_input/request_permissions 调用、失败路径）；
  **app-server 全部 wire 行为为真实官方二进制**。内容全为 `MOCK:*` 合成文本。
- 采集器：`../collect_samples.py`（可复跑：`python3 collect_samples.py [--only 组名]`）
- 脱敏：CODEX_HOME/workspace 路径 → `$CODEX_HOME`/`$WORKSPACE`（含 /private 前缀变体）

## 组清单与关键事实

| 组 | 覆盖 | 关键真实事实（详见 raw.jsonl / meta.json） |
|---|---|---|
| initialize | initialize 请求/响应（普通 + experimentalApi 两组）、initialized | userAgent 回显 clientInfo；codexHome 回显 |
| lifecycle | daemon version(absent)/start/running/stop、managed WS healthz/readyz、stdio 断开 | daemon start 前置：standalone 副本 + socket 路径 < SUN_LEN(104)（首轮长路径实录该错误，见 git 历史）；start 返回 JSON（pid/socketPath/版本） |
| catalog | 空/多 thread 列表、archive 列表、cursor 分页、thread/read(includeTurns)、rename、turn 全生命周期、reasoning turn、失败 turn、steer、interrupt | turn/started→item/*→delta(10)→completed 全链；steer 成功返回 {turnId}、stale 报 no active turn；interrupt 需 turnId、终态 interrupted；失败 turn 终态 failed；cursor 为时间戳 |
| interaction | command approval（accept/cancel）、file approval、permission approval、requestUserInput 1 题 | availableDecisions 物理到达（§7.3 分歧裁决）；permission RequestPermissionProfile{network,fileSystem}；file approval 无 availableDecisions；cancel→interrupted；requestUserInput 批结构+resolved；default 模式自动转发 |
| models-config | model/list、config/read、permissionProfile/list | Model 18 字段无 provider；custom provider 未实现 /v1/models 时回落内置目录+warning；config typed model_provider=mockpi、flatten additional 为空（不含 model_providers） |
| ownership | 双进程写者 resume/archive/delete 冲突、只读可用性、unsubscribe、loaded 保持 | 三者均 -32600 "already has an active writer"；冲突期 thread/read 成功；unsubscribe 返回 unsubscribed 且 thread 保持 loaded（30min TTL 即时证据） |
| reconnect | 连接关闭 → 新连接 initialize → thread/read 冷校准 → resume；挂起审批断线 | read 恢复 turns；resume ok；挂起审批不向新连接重放（§7.2 证实） |

## 样本裁决（已回写设计 §7/§7.3，见设计 v1.6 §22）

1. command approval `availableDecisions` 未声明 experimentalApi 也物理到达（`additionalPermissions` 被剥除）。
2. `config/read` flatten `additional` 实测为空，不含 `model_providers`。
3. 审批 decision 枚举 `accept`/`cancel`/结构化；`cancel` → turn 终态 `interrupted`。
4. `turn/interrupt` 必填 `turnId`；turn/start 响应先于 active-turn 注册（同毫秒 interrupt 报 no active turn）。
5. `requestUserInput` default 模式自动转发；`features.default_mode_request_user_input=true` 后 1 题
   server request 物理到达；多题 blocking 需 experimental collaborationMode（Phase 4 另采）。

## 未采集 / 诚实边界

- 多题 blocking requestUserInput、MCP elicitation 三 variant 实样本：Phase 4 版本门控后另采。
- external host 组：`gate-terminal/`、`gate-hosts/`（独立目录）。
- `thread/turns/list`（experimental 分页）：一期不用（§7 🧪 行维持）。
