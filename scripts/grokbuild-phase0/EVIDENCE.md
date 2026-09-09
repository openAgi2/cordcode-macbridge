# Grok Build 命令面板 Phase 0a 零模型证据结论

> 取证对象：`~/.grok/bin/grok` → `grok-1.0.13-macos-aarch64`，`grok 1.0.13 (5e9a58528b76)`，sha256 `8669e0fd…83b80`（SOURCES.md）。
> 全部样本为 2026-09-07 本机实测，隔离 `GROK_HOME`（`/tmp/grokbuild-probe-home-*`），认证凭证复制自本机 `~/.grok/auth.json`（内容已脱敏，不进证据）。
> 原始样本：`samples/`（脱敏）；探针：`probe.py` / `recovery.py`。**协议原始样本与源码推论分开标注**：`[样本]` = 1.0.13 实测；`[源码]` = grok-build 1.0.16 @ `72a6125` 推论。

## P1 — initialize 形状

`[样本]` p1-initialize.json：
- `protocolVersion: 1`；`agentCapabilities.loadSession: true`、`sessionCapabilities: {list:{}, resume:{}, close:{}}`、`mcpCapabilities: {http:true, sse:true}`、`promptCapabilities: {image:false, audio:false, embeddedContext:true}`。
- `authMethods: [{id:"grok.com",…}]`（登录必需：不 authenticate 时 `session/new` 报 `-32000 Authentication required`）。
- 顶层 `_meta`：`grokShell:true`、`agentVersion:"1.0.13"`、`currentWorkingDirectory`、`modelState{currentModelId:"grok-4.6", availableModels[…]}`、`x.ai/mcp/sdk`、`x.ai/pluginDirs`。
- **`_meta.availableCommands` 存在**（7 条内置基础命令：compact/always-approve/context/session-info/deep-research/workflow/goal，与 ACU 元素同构、无 commandId）。它是握手期诊断快照，**不是会话完整目录**（完整目录 43 条经 ACU，见 P2/P3）；实现不得把它当 List 返回源。

> 勘误：本节初版曾记录"1.0.13 initialize 无 availableCommands"，系首轮输出截断误读；复核归档样本确认 `_meta.availableCommands` 存在（7 条）。

## P2 — session/load 的 ACU 完整形状

`[样本]` p2-*：
- `session/new {cwd, mcpServers:[]}` → `{sessionId, models{…}}`；空 `mcpServers` 数组必填。
- ACU 通知：`session/update` → `params.update = {sessionUpdate:"available_commands_update", availableCommands:[{name, description, input:{hint}|null}]}`；**无 commandId 字段**；名称含裸名、`bundled:` 前缀、`namespace:name`（`code-review:code-review`）三种形态。
- ACU **分波到达**：session/new 后第一波 27 条（MCP 初始化前），MCP/plugins 就绪后再发完整波（load 后实测 43 条，两条内容一致）。`_meta.tools` 附带会话工具清单。
- 空表/失败形状未在 1.0.13 出现（无空目录 cwd 样本）；实现按"空数组也是合法表"处理，样本留待真实项目 cwd 复核。

## P3 — catalog 通道与等价范围（设计期决策依据）

`[样本]`：
- `_x.ai/commands/list {cwd}`（ext 方法，`_` 前缀）在 catalog 进程（未 load 会话、未认证状态下亦可用）返回 `{commands:[…]}`，与 ACU 同构；对 `~`、`/tmp`、`/tmp/grokbuild-cwd-test` 三个 cwd 均返回 34 条（本机用户全局 skills 生效，cwd 无项目插件时不随 cwd 变化）。
- **等价范围结论：catalog `commands/list`（34 条）≠ 会话 ACU（43 条）**。`commands/list` 是会话 ACU 的真子集：缺 `feedback`、`loop`、`reload-plugins` 等会话运行时命令；会话 ACU 另含 `hooks-*` 等运行时命令且包含全部 skills/bundled 命令。
- 专用 child `session/load` 后读 ACU：**可行**（p7-14 样本：load 后 2 条 ACU 各 43 条，内容一致稳定）。
- **设计期决策（1a 输入）**：List 主通道不能拿 catalog `commands/list` 直接充当会话目录；采用「专用 child load 后读取会话 ACU」或「已 load 的会话 actor ACU side-state」。运行期不得自动互相降级。

## P5 — CMU 三路

`[样本]`：
- **stdout（主路）**：`session/update` → `params.update = {sessionUpdate:"current_mode_update", currentModeId:"plan"}`（set_mode 成功后到达；与 2 条 ACU 同波）。
- **jsonl**：`updates.jsonl` 仅记录 `hook_execution`（method `_x.ai/session/update`，含 timestamp/eventId `_meta`）；`events.jsonl` 仅记录 `mcp_*` 事件。**两者均不记录 CMU/ACU**（零 turn 场景实测）。
- **gateway**：`--no-leader stdio` 模式无 gateway 通道（gateway 属 leader 拓扑）；本轮不适用。
- 结论：CMU 只能靠 stdout 通知 + `plan_mode.json` 文件（§P7）；`updates.jsonl` 不能充当模式确认源——与方案 §5.3「updates.jsonl 最后一条 CMU 绝不充当确认来源」一致，且 1.0.13 实测**根本没有** CMU 进 jsonl。

## P6 — 真实 turn 生命周期（终态/usage/取消/hostTurn 反馈形状）

`[样本]` p6-turns.json（探针 `p6_turn.py`；隔离 home `/tmp/grokbuild-probe-home-p6`，单一测试会话内四 turn 顺序执行；313 处 UUID 脱敏，cwd 路径保留——与既有样本口径一致；owner 已按 P4P6-CONFIRM.md 授权 ≤4 次模型调用）：

| Turn | 输入 | response stopReason | 模型消耗 | 关键形状 |
| --- | --- | --- | --- | --- |
| A | `/hooks-list` | `end_turn` | **0**（totalTokens=0） | 反馈正文经 `session/update` agent_message_chunk、`_meta.hostTurn=true`，正文 "Loaded hooks (9)" |
| B | `/hooks-add relative.sh`（相对路径） | `end_turn` | **0** | 正文 "Failed to add hook path: Hook path must be absolute."，hostTurn=true——失败也是正常 settle，不是 RPC reject |
| C | 真实 prompt「只回复 ok」 | `end_turn` | **1 次**（modelCalls=1，grok-4.6，totalTokens=14691，costUsdTicks=35159400≈$0.0035） | 完整流：user echo → agent_thought_chunk×25 → agent_message_chunk → response_completed → turn_completed → prompt_complete → last_turn_summary → ACU |
| D | 长 prompt 后 ~1s `session/cancel` | `cancelled` | D 自身**无 usage 报告**（turn_completed 无 usage 字段；response `_meta` 沿用 C 的快照值） | 取消后 prompt 响应（id 7）自行以 `stopReason=cancelled` + `_meta.cancellationCategory=MidTurnAbort` 终结 |

关键结论（服务 1b/D1）：

1. **正常终态集合 = `end_turn`**。取消终态实测值 **`cancelled`** + `cancellationCategory=MidTurnAbort`（response `_meta`、ext TurnCompleted `_meta`、prompt_complete 三处一致）。`[源码]` 终态全集 end_turn/cancelled/max_tokens/refusal；硬错误 = RPC reject（无 stopReason、无 turn_completed）——dispatcher 必须两路都终结。
2. **prompt future 在取消时也会终结**：`session/cancel` 是 notification（无 id、无响应帧），取消后原 `session/prompt` 响应以 cancelled 自行返回——1b terminal future 不得只等 end_turn。
3. **usage 三处、无独立 usage 通知**：① `session/prompt` response `result._meta`（totalTokens/inputTokens/outputTokens/cachedReadTokens/reasoningTokens + `usage{modelCalls,apiDurationMs,costUsdTicks,modelUsage{grok-4.6-build{…}}}`）；② ext `_x.ai/session_notification` TurnCompleted `usage.totals`（camelCase 同构 + elapsed_ms）；③ `response_completed` update 的 anthropic 风格 snake_case 键（cache_creation_input_tokens/cache_read_input_tokens/…）。
4. **hostTurn 反馈（§7 正文组）类型判据实证**：A/B 零模型（totalTokens=0）+ agent_message_chunk 正文（含失败文案）+ end_turn settle + `_meta.hostTurn=true`。失败文案与成功文案同轨，无独立错误通知。
5. **ext 三轨全形状**：`_x.ai/sessions/changed`（upsert 会话元数据：title/cwd/modelId/reasoningEffort/activity…）、`_x.ai/queue/changed`（队列条目 kind:"prompt" + position）、`_x.ai/session_notification`（hook_execution / session_summary_generated / TurnCompleted{prompt_id,stop_reason,elapsed_ms}）；终了 `_x.ai/session/prompt_complete {sessionId,promptId,stopReason,agentResult,cancellationCategory}`（取消时 agentResult=null）。
6. 噪音剔除：A/D 中 `hook_execution … exit 127`（SuperIsland 全局 hooks）与 P0 相同，属用户全局配置，与任务无关。
7. **费用口径**：预估上限 4 次、实际 C 证实 1 次 + D 已开始生成（部分流是否计费上游未报告）；A/B 零模型。在授权范围内。

## P7 — Pending 恢复判定（决定性）

`[样本]` 链条（p7-*，隔离 home，session `01a07b3d-8fb7-…`）：
1. idle `session/set_mode {sessionId, modeId:"plan"}` → 响应 `{}`（成功）；**参数字段是 `modeId`**（`sessionModeId` 报 `-32602 missing field`；`sessionId` 必填——两者均为 1.0.13 相对上游 1.0.16 wire 的漂移）。
2. 持久化：session 目录 `plan_mode.json` = `{state:"Pending", was_previously_active:false, reminder_count:0, pending_exit_reminder:false, awaiting_plan_approval:false}`。
3. 关闭（stdin EOF）并 reap：进程退出码 0；`plan_mode.json` 保持 `Pending`。
4. 新 actor `session/load {sessionId, cwd, mcpServers:[]}` 成功：**无 `current_mode_update` 通知**（只有 2×ACU + MCP init 通知）；`plan_mode.json` **仍为 Pending（文件未回写）**。
5. 恢复后重建的 `system_prompt.txt`（load 时重新生成）**不含** plan 角色注入（对照 `bundled/agents/plan.md`：`permission_mode: plan` 的只读架构师角色未被采用）；`prompt_context.json.prompt_mode` 恒为 `"extend"`（set plan 前后不变）。
6. `prompt_context.json` / `summary.json` 均无有效模式字段可读。

`[源码]` 1.0.16 `plan_mode.rs:96 from_snapshot`：Pending→Inactive、ExitPending→Inactive(+reminder)；`:145 session_prompt_mode` 仅 Active→Plan；`spawn.rs:629` 恢复链 `restored_prompt_mode = session_prompt_mode()`，恢复时不 enqueue CMU——与上面 4/5 的 1.0.13 实测行为一致。

### 判定

**1.0.13 与 1.0.16 语义一致：短命 mode-only 路径（idle set plan → 落盘 → 关闭 → 新 actor load）恢复后的有效模式不是 Plan。** 文件字节正确（`plan_mode.json=Pending`）、写盘成功、set_mode RPC 成功、load 成功——全部不能代替「下个 actor 按 Plan 工作」。方案 §2 决定性失败条件触发：**Phase 2 短命 mode-only 方案停止**；不得以加长等待、把 Pending 映射 plan、补 `_meta.mode`、直接写文件或隐式 prompt 造成功；Phase 2 须另有经证明的官方 actor 生命周期方案（P4(b) 外部 actor/常驻路径）才可开通，未证明前只交付明确禁用与诊断。

覆盖边界：零模型覆盖 **Pending**（短命路径的实际输入态）。Active/ExitPending 的恢复需真实 turn 才能到达，其语义目前只有 1.0.16 源码佐证；由于短命路径已阻断，除非 Phase 2 改道官方常驻方案，否则无需补测。

## P8 — 唯一权威读源与恢复语义

- 权威**持久化**源：session 目录 `plan_mode.json`（state ∈ {Pending, Active, ExitPending, Inactive} + 附带字段）。它是唯一记录官方模式状态的文件；`prompt_context.json.prompt_mode`、`summary.json`、`updates.jsonl`、`events.jsonl` 均不能读出模式（实测）。
- 恢复规则（映射到有效模式）必须应用于读取结果之上：`Pending/ExitPending → Inactive → default`；`Active → plan`；`Inactive → default`。未知 state 枚举 → unknown（不强转 default）。`[源码]` from_snapshot；`[样本]` Pending 案例实证。
- CMU 通知只表示 dirty（且 1.0.13 恢复时不发）；不携带"必须等到"的目标值——与方案 §5.3 一致。
- **冷 hydrate / 热验证 / 事务共用读取契约**：`readAuthoritativeMode = read(plan_mode.json) + 官方恢复规则映射`；missing/corrupt/readError 分开返回；文件不存在（新会话）→ 无持久化状态（unknown，除非证明官方 default 语义——1.0.13 新会话 set plan 前 `plan_mode.json` 不存在，可视为官方"无模式状态"，映射 default 需按 §5.3 保守为 unknown）。

## P9 — List 通道重做取证（catalog 单例 `_x.ai/commands/list {cwd}`）

背景：owner 报障「grok build 模式点 ➕ 十几秒才出 goal/compact」；owner 指令先读
grok-build 官方源码与成熟 dsh-web 模式，不再自建轮子。

`[生产日志]`（go-bridge.log，2026-09-07 22:42，旧通道在跑的版本）：

- req_8 `list_session_commands` 22:42:38.525 发起 → 该请求下一个动作（同 id 的
  `list_projects`）22:42:46.387，全程 ≈7.9s；req_15 同形态。每次打开面板都重复全套。
- 分解：专用短命 child `initialize` 1206ms（22:42:39.740 handshake step 行）→
  `session/load` wait_elapsed_ms=3118（22:42:42.859）→ MCP 初始化的 ACU 波等待 +
  acuSettleQuiet 1500ms 静默窗 ≈ 其余 ~3.5s。

`[源码]`（grok-build @ `72a6125`）：

- `xai-grok-shell/src/extensions/session_admin.rs` `handle_commands_list` **cwd 分支**
  注释即 "the pull grok-desktop uses after session start"——官方桌面客户端在 session
  start 后用 `_x.ai/commands/list {cwd}` 拉目录，**不需要把会话 load 进本进程**；
  sessionId 分支才要求 `session_handle_waiting_for_load`（旧通道慢的根源就是选了
  sessionId 语义）。
- `xai-grok-shell/src/session/slash_commands.rs` `ListCommandsRequest`：camelCase
  `{sessionId?, cwd?, kind?}`，kind 省略 = 完整 Build 目录；响应
  `{commands:[AvailableCommand{name, description, input?{hint}}]}`。
- `xai-grok-pager/src/acp/tracker.rs`：官方 pager 把 ACU 波被动存
  `pending_acp_commands` → `AgentSession.available_commands` + generation 计数，
  斜杠菜单读会话内状态，每次打开零 RPC/零进程——官方客户端不存在「每次打开都起
  进程」的形态。

`[样本]` p9-cmdlist-sessionless.json（隔离 GROK_HOME 临时目录、零模型调用；探针
`p9_cmdlist_sessionless.py`）：

- 无会话（无 session/new、无 session/load、无 prompt）进程上：`initialize` 2976ms
  （冷启动一次，进程级单例生命周期只付一次）、`authenticate` 1ms、
  `_x.ai/commands/list {cwd}` **43ms**；18 条目录，`compact`+`goal` 在列且带官方
  description/hint（样本只归档命令描述符，无凭证）。
- 成熟模式对照：dsh-web `ListSessionCommands` = 常驻 mux 连接上单次 `commands/list`
  RPC（agent/dsh-web/commands.go）——无缓存、无 child，与重做后形态同构。

结论：P3 设计期「专用 child load 后读 ACU」的每打开成本（生产 ≈8-10s）不可接受，
重做为 catalog 进程级单例上的官方同款 RPC（warm 43ms）。P3「catalog 34 条 ⊂ 会话
ACU 43 条」的不等价结论仍成立，但对 D1 准入集 {compact, goal} 无影响——两条均在
catalog 目录内；缺的只是 feedback/loop/reload-plugins 等会话运行时命令，它们本来
就不在准入集。红线保持：每次 List 仍是真实官方拉取（无缓存、无 TTL），失败
fail-closed 标记该身份不可用，语义不变。

## 2026-09-09 P10 — pager-local `/plan` 与 resident actor 校正

- `[源码]` 目标上游的 pager 在 `slash/commands/plan.rs` 注册 `/plan [description]`；它不由 agent `_x.ai/commands/list` 广播。
- `[源码]` pager dispatch 对 bare `/plan` 调当前会话 ACP `session/set_mode(plan)`；带 description 时先 set mode，再只发送 description prompt。
- P7 样本仍有效：另起短命 child 做 mode-only 切换后，冷恢复会把 Pending 丢回 Inactive。该证据阻断短命 child，不阻断 CordCode 已持有的 resident conversation actor。
- 实施结论：目录在真实 agent pull 成功后合并 pager descriptor（不作失败 fallback）；resident actor 的 CMU 是其 live effective mode，actor 注销或替换即失效；无 actor 时继续采用 P7/P8 冷恢复映射与 `canSet=false`。

## 漂移表（1.0.13 实测 vs 方案/1.0.16 源码预期）

| 项 | 方案/1.0.16 预期 | 1.0.13 实测 | 处置 |
| --- | --- | --- | --- |
| initialize `availableCommands` | P1 假设可能存在 | **不存在**；目录经 ACU/commands/list | P1 仅诊断，无实现影响 |
| set_mode 参数 | 上游 `SessionModeId`（wire `sessionModeId`?） | `{sessionId, modeId}` | Mac 实现按 1.0.13 实测字段 |
| ext 方法前缀 | `x.ai/…` | stdio 上须 `_x.ai/…`（半包装） | 与既有 session_admin 实测一致 |
| CMU 进 updates.jsonl | 方案 §5.3 禁止充当确认源 | **零 turn 场景根本不落盘** | 按 §5.3 目录/周期重读为主 |
| commands/list ≙ 会话 ACU | P3 要求证明等价范围 | **不等价**（34 ⊂ 43，缺运行时命令） | P3 设计期固定 child load 读 ACU；**2026-09-07 P9 重做为 catalog 单例 commands/list**（D1 准入集不受影响，见 P9） |
| Pending 恢复 | 1.0.16 源码 Pending→Inactive | 一致（无 CMU、无 plan 注入、文件不回写） | **短命路径阻断** |

## 副作用与复位记录

- 隔离 home：`/tmp/grokbuild-probe-home-{p0,p1,p2,p7,p7clean,p3}` 全部为临时目录，未触碰 `~/.grok` 用户会话；可整体删除复位。认证文件为只读复制。
- P6 追加：`/tmp/grokbuild-probe-home-p6`（四 turn 测试会话）取证完成后整目录删除复位（2026-09-07）；无外发消息（feedback 未执行）；`/tmp/grokbuild-p6-out` 中间产物随归档后清理。
- P9 追加：`/tmp/grokbuild-cmdlist-probe-*` 临时 GROK_HOME 由脚本自删复位；零模型调用、无 prompt、无外发；输出仅命令描述符（无凭证）。
- `~/.grok` 唯一 touched：`grok --version`（只读）。未动 leader、真实会话、配置。
- 探针进程全部正常退出（rc=0）并 reap；无残留 grok 进程（`pgrep -fl "grok agent"` 复核见 regression 归档）。
- 隔离 home 内观察到的 `session_start` hook 失败（SuperIsland `cc-event-hook.sh` exit 127）来自**用户全局 hooks 配置**，与本任务无关，仅样本记录。
