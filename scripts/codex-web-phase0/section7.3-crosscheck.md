# §7.3 逐 surface 交叉核对报告（pinned source × 0.149.0-alpha.4 生成 schema）

- 日期：2026-08-21
- pinned source：`/Users/jacklee/Projects/codex` @ `536f86e5cc9ec1ff38457d099bf320b9d08eeeba`
- 目标二进制：`codex-cli 0.149.0-alpha.4`（`/Applications/ChatGPT.app/Contents/Resources/codex`）
- 自动断言：`validate_schemas.py`（27 项全 PASS，输出 `validate-schemas-run.txt`）

## 0. 机制结论（本次核对的方法学基础）

1. schema 生成器的 stable/experimental 拆分**只由 `#[experimental]` 属性驱动**：
   - 方法级：`common.rs` 注册处 `#[experimental("...")]` → 该方法整体只出现在 `--experimental` bundle；
   - 字段级：结构体字段 `#[experimental("...")]` → stable bundle 中**剥除该字段**（例：
     stable `TurnSteerParams` 只有 `{threadId, clientUserMessageId, input, expectedTurnId}`）；
   - **doc 注释 `/// EXPERIMENTAL` 不影响拆分**（例：`PlanDelta`、`ToolRequestUserInputParams`
     出现在 stable bundle）。
2. 结构体整体 derive `ExperimentalApi` ≠ 方法 experimental：它只是为字段生成"按值出现才报告
   experimental"的 value-gating（`codex-experimental-api-macros/src/lib.rs`）。
3. 因此"schema 出现在 stable"≠稳定承诺；协议稳定性判定必须回到源码属性/注释 + 真实样本。

## 1. 十一个 surface 逐行结论

| §7.3 Surface | pinned source 事实 | 生成 schema（0.149.0-alpha.4） | 结论 |
|---|---|---|---|
| `model/list` | `ModelListResponse{data: Vec<Model>, next_cursor}`；typed `Model`（18 字段）无 provider | `data/nextCursor`；`Model` 无 provider/modelProvider | ✅ 一致 |
| `thread/start` | `model`/`model_provider` 无属性门控（stable）；`allow_provider_model_fallback`/`permissions`/`runtime_workspace_roots` 字段级 experimental | stable 含 model+modelProvider；experimental 字段被剥 | ✅ 一致（0.149 stable 面新增 serviceTier/approvalsReviewer/sandbox/config/serviceName/personality/baseInstructions/developerInstructions/ephemeral/sessionStartSource/threadSource 等，属扩展非冲突） |
| `turn/start` | `model: Option<String>`（"对本 turn 及后续 turn 生效"）；**无 modelProvider 字段** | stable/experimental 均无 modelProvider | ✅ 一致 |
| `turn/steer` | `expected_turn_id: String` 必填（"fails when it does not match the currently active turn"）；结构体 derive ExperimentalApi=字段级 value-gating | stable required=[expectedTurnId,input,threadId]；responsesapiClientMetadata/additionalContext 仅 experimental | ✅ 一致 |
| `config/read` | 请求 `{include_layers, cwd}` 无 experimental；`ConfigReadResponse`/`Config` derive ExperimentalApi；`Config` `#[serde(rename_all="snake_case")]`，typed `model_provider`，`#[serde(default, flatten)] additional` | `Config` snake_case 键 + `model_provider` + `additionalProperties:true`；`apps` 字段仅 experimental bundle | ✅ 一致 |
| `item/tool/requestUserInput` | doc 注释 EXPERIMENTAL、无属性门控；`questions: Vec<Question{id,header,question,isOther,isSecret,options}>` + `is_blocking`（`auto_resolution_ms` @deprecated）；响应 `answers: HashMap<questionId, Answer>` | 出现在 stable ServerRequest；批结构一致 | ✅ 一致（"双重事实"：类型标 experimental 但不被 experimentalApi 剥除，须真实样本冻结） |
| command approval | `available_decisions`/`additional_permissions` 字段级 experimental；**但 `strip_experimental_fields()` 只置空 `additional_permissions`**（`item.rs`，TODO 承认 generic strip 未做）；构造侧 `bespoke_event_handling.rs:714` 无条件 `available_decisions: Some(...)`；出站过滤 `transport.rs filter_outgoing_message_for_connection` 仅在未开 experimentalApi 时调用上述 strip | stable schema 剥除两字段 | ⚠️ **分歧**：设计 §7/§7.3 称"未开 experimentalApi 时被 server 剥除"对 `additionalPermissions` 成立、对 `availableDecisions` **在本 pin 不成立**（schema 剥除 ≠ wire 剥除）。待 p0-samples 真实样本（关 experimentalApi 的审批请求）裁决后按 §3.0 回写设计 |
| permission approval | `PermissionsRequestApprovalParams{...,permissions: RequestPermissionProfile}`（无 availableDecisions）；响应 `{permissions: GrantedPermissionProfile, scope: PermissionGrantScope, strict_auto_review?}` | 一致 | ✅ 一致（新字段 `strictAutoReview` 待样本记录） |
| MCP elicitation | `McpServerElicitationRequest` 三 variant：`Form`/`openai/form`/`Url`（`#[serde(rename="openai/form")]`）；capability `mcpServerOpenaiFormElicitation`；`turn_id: Option`（可空，是 app-server 关联而非协议身份） | params 含三 variant 所需字段 | ✅ 一致 |
| plan | `PlanDelta => "item/plan/delta"` 注册处仅 doc 注释 EXPERIMENTAL、**无 `#[experimental]` 属性**（对比 `ProcessOutputDelta` 有属性） | 出现在 stable ServerNotification（两 variant 相同） | ✅ 无冲突：设计 🧪（取样门控后消费）立场不变；记录"未开 experimentalApi 也会物理到达该通知"这一机制事实，消费决策仍以样本为准 |
| `turn/completed` | `TurnCompletedNotification{thread_id, turn: Turn}`；`Turn{id, items, items_view, status, error, started_at, completed_at, duration_ms}`；`TurnItemsView = NotLoaded/Summary/Full`（serde default=Full） | enum=[notLoaded,summary,full] 一致 | ✅ 一致 |

## 2. 版本漂移记录（0.148.0-alpha.21 → 0.149.0-alpha.4）

- 设计时二进制：`codex-cli 0.148.0-alpha.21`；实施时目标二进制：`0.149.0-alpha.4`（ChatGPT.app
  内嵌，2026-08-21 更新）。按设计 §3.2 以实际安装版本为准。
- 方法面：stable ClientRequest 95（含 `modelProvider/capabilities/read`、`thread/fork`、
  `thread/read`、`threadSection/*`、`fs/*`、`skills/*`、`hooks/list` 等）；experimental 独有 55
  （`thread/turns/list` ✅ 实验确认、`thread/items/list`、`thread/queue/*`、`project/*`、
  `thread/search`、`process/*`、`remoteControl/*`、`environment/*`、`memory/reset`、
  `thread/memoryMode/set`、`thread/realtime/*`、`collaborationMode/list` 等）。
- 字段面：thread/start、turn/start 出现较多 0.149 新增 stable 字段（见上表）；命令审批新增
  `approvalId`（zsh-exec-bridge 子命令审批）/`startedAtMs`/`commandActions`；权限审批响应新增
  `strictAutoReview`。
- **结论**：schema 与 pinned source typed shape 逐项吻合，无 §21.2-2（schema 与 pin 不一致）情形；
  0.149 扩展面不推翻设计任何 ✅ 行。唯一需回写点为 §1 表 command approval 行的"剥除"表述，
  待真实样本裁决。

## 3. 后续动作

1. `p0-samples`：采集关/开 `experimentalApi` 两组命令审批请求 wire 样本，裁决 `availableDecisions`
   物理剥除行为；采集 1–3 题 requestUserInput、elicitation 三 variant、`item/plan/delta` 实际到达。
2. 裁决后按设计 §3.0 回写 §7 command approval 行与 §7.3 对应行（保留现场样本引用）。
3. `agent/codex-web` 实现期以本 bundle 的 stable 面为 contract 输入；experimental 面逐项版本门控。
