# DSH「/」命令面板 实现说明（源码锚点 + 来源清单）

- 日期：2026-09-05
- 性质：实现说明。方案 =
  [docs/2026-09-04-dsh-slash-command-panel-implementation.md](2026-09-04-dsh-slash-command-panel-implementation.md)
  v1.4；本文件承载方案 §0.1「开工源码优先门」的锚点记录与 kickoff §0 来源清单复核。
- 官方仓：`/Users/jacklee/Projects/deepseek-harness`（**只读**，工作树停在 master
  `d347e70`，未做任何 checkout 切换）
- **目标版本：tag `dsh-v0.1.1-rc.2` = `b150a551b8d465e31e418e1b2eaf5e79bbb7d28e`**
  （本会话 2026-09-05 `git rev-parse` 实测；与本机装机 0.1.1-rc.2 一致）。master 仅对照，
  本文所有行号均取自 tag `b150a551b8`（`git show b150a551b8:<path>`）。

## 1. 来源清单（本会话 2026-09-05 复核，替代方案 §0 钉位）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=6197da5b8f89a805f7342bba8cd9f2e755c1fbd5
未提交状态=未跟踪 docs/{方案,评审,开工指令} 三份 md（属当前用户，保留）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-plan-approval
配套分支=plan/approval-layer-ios
配套提交=b94289df3ee41ab5a7ced651140312da13d7685f
配套未提交=干净
上游 DSH checkout=master d347e703908d0406b7a7ef80e3a0e594d86b2215（干净）
  协议真值钉 tag dsh-v0.1.1-rc.2 = b150a551b8d465e31e418e1b2eaf5e79bbb7d28e
```

## 2. 官方源码锚点（tag `dsh-v0.1.1-rc.2` / `b150a551b8`）

### 2.1 命令注册表与解析（`packages/interaction/commands/src/index.ts`）

| 符号 | 位置 | 要点 |
| --- | --- | --- |
| `parseCommand(line)` | `index.ts:116`（正则在 :117） | `/^\/([a-z][a-z0-9_-]*)(?=$|[\t\n\r ])/u`；不匹配返回 `undefined`。`/plan` 不会误配 `/plan/path` |
| `CommandRuntime.list(agent)` | `index.ts:238`（`@Remote`） | 返回 **name 排序的 `readonly CommandDescriptor[]` 裸数组**；descriptor = `{name, description, input?: {hint, images?}}`（构造在 `normalizeDefinition`，`index.ts:186-218`：无 input 的命令 **没有 `input` 键**） |
| `CommandRuntime.execute(agent, line, images, signal)` | `index.ts:271`（`@Remote`） | 解析失败或名字未命中 → 返回 `undefined` **且不写任何日志**（:274-276：Admission misses log nothing）；命中则先 append `command/run`（:341）再调 handler，settle 后 append `command/done`（:348）。返回 `CommandExecution = {commandId, result}` |
| `normalizeResult` | `index.ts:221-247` | `result.kind` 只有 `'success'`（text 可选）/ `'error'`（text 必填非空） |
| `appendLifecycle` | `index.ts:422` | `command/run`\|`command/done` 为 log-only append，无 turn 包裹 |
| commandId 格式 | `mintCommandId` `index.ts:412` | `cmd-<8位实例token>-<序号>` |

### 2.2 六条 host 命令注册（descriptor 实测值）

| name | 注册位置 | description | input | 备注 |
| --- | --- | --- | --- | --- |
| `plan` | `packages/plan/plan-mode/src/index.ts:296` | `Enter or leave plan mode` | `{hint:'[off|message]', images:true}` | 裸执行 → `set(agent,true)`，成功文案 `Plan mode on. Use /plan off to leave.`（committed）或 `Entering plan mode (applies from the next step)...`（queued，open turn 时挂 pendingIntent） |
| `compact` | `packages/compaction/command-compact/src/index.ts:100` | `Compact older conversation history` | **无** | 无参命令；错误码 → 人类文案在 `expectedFailure`（:23-55） |
| `goal` | `packages/goal/command-goal/src/index.ts:190` | `set or view the goal for a long-running task` | `{hint:'[<objective>\|clear\|edit <objective>\|pause\|resume]', images:true}` | 裸执行=查看可用命令（error 文案 :181） |
| `feedback` | `packages/feedback/command-feedback/src/index.ts:101` | `record feedback about this session` | `{hint:'<text>'}`（无 images） | `recordInput:false`；裸执行错误可见（spec :139-150 事件序 `command/run, feedback/record, command/done`） |
| `permission` | `packages/interaction/permission-presets/src/index.ts:274` | `Switch the permission preset (sandbox mode + approval policy)` | `{hint:'<preset>'}` | 裸执行=查看当前 preset（success）；未知 preset=error（:283-288） |
| `export` | `packages/session-query/session-log-export/src/index.ts:19` | `Download this Session log as a ZIP archive` | **无** | 成功固定文案 `Session log download requested.`（:9-12）；带参数=error `The Web /export command does not accept a path.` |

### 2.3 网关（`packages/api/gateway/src/index.ts`）

| 符号 | 位置 | 要点 |
| --- | --- | --- |
| `TypertGatewayService` 拦截 `/api` | `index.ts:104-111` | endpoint 恰两段：`commands/list`、`commands/execute` = namespace `commands` + method |
| `invokeRpc` payload 校验 | `index.ts:194-222`（**拒包在 :201-208**） | payload 必须是**恰含一个 `args` 键**的 plain object，`args` 本身是 plain object → 请求形状 `{args:{…}}`；违者报 `Remote payload must contain exactly one plain-object args field` |
| 结果信封 | `index.ts:215-218` | `{ok:true, value}`；业务值在 `value` 字段（list 的 value = 裸数组；execute 未命中命令时 value 缺席 = undefined） |
| 生成的客户端 | `packages/api/gateway/src/client/index.ts:408` | `connection.rpc.call('/api', endpoint, { args })` |

### 2.4 RPC 参数名（官方客户端调用实证）

`packages/client/connection/tests/fixture-commands.client.spec.ts`：

- list（:32, :44）：`rpc.call('/api', 'commands/list', { args: { agentId } })` —— **键名 `agentId`**（直传 `{agentId}` 或 `{args:{sessionId}}` 均被 2.3 的校验/描述符拒绝）
- execute（:60, :118-135）：`rpc.call('/api', 'commands/execute', { args: { agentId, line, images? } })` —— `line` 为完整 slash 行（如 `'/plan'`、`'/plan off'`）；`images` 可选
- fixture 分发（`packages/client/connection/src/client/fixture.ts:3109-3110`）：list 取 `args.agentId`；execute 取 `args.agentId` + `args.line ?? ''` + `args.images ?? []`

### 2.5 durable 事件（`KNOWN_SESSION_EVENT_TYPES` 全收录，rc.2）

- 注册表：`packages/core/session/src/known-event-types.ts:19`（生成自
  `scripts/gen-persistence-catalog.ts:420`）。**本方案关注的类型全部在内**：
  `plan/mode`、`compaction/{start,prune,summary,end}`、`goal/change`、`feedback/record`、
  `command/run`、`command/done`。
- 追加锚点：
  - `plan/mode`：`packages/plan/plan-mode/src/index.ts:477,493`（`session.append('plan/mode', {active})`；类型声明 :53）
  - `feedback/record`：`packages/feedback/command-feedback/src/index.ts:75`
  - `goal/change`：`packages/goal/goal/src/domain.ts:25`（经 goal service append；spec 断言事件序 `command/run → goal/change → command/done`，`packages/goal/command-goal/tests/command-goal.spec.ts:128`）
  - `compaction/*`：`packages/compaction/compaction/src/index.ts:123` 起（`compaction/start` 先于 summarize；checkpoint 家族校验在 `invariant.ts:52-151`）
  - `command/run|done`：`packages/interaction/commands/src/index.ts:341,348`
- 读取端拒绝语义：`packages/session/session-persistence/src/coordinator.ts:1063` ——
  类型不在 `KNOWN_SESSION_EVENT_TYPES` 且不带 `ignorable` 即拒读（= CordCode dsh-web codec
  default reset 的官方对应物）。**这四族是 rc.2 注册表内事件，`ignorable` 旁路救不了，
  必须 known-drop**（方案 §4.7 判断成立）。

## 3. 对 CordCode 实现的直接结论

1. list 请求写死 `{args:{agentId:<sessionId>}}`；响应解 `value` 的**裸数组**到中间 wire
   类型 `{name, description, input?: {hint?, images?}}`（`input` 用指针/omitempty，
   compact/export 无此键）再映射 `SessionCommand`。
2. execute 请求 `{args:{agentId, line}}`（本项不带 images）；**value 缺席（undefined）= 未命中
   命令 = 失败**，与 permission 现路径语义一致。
3. `result.kind=="error"` → RPC 失败、message 用官方 `result.text`；`kind=="success"` → 成功
   （iPhone 侧 `/export` 静默即此路径）。
4. codec Class ② 必须追加的七个 type：`plan/mode`、`compaction/start`、`compaction/prune`、
   `compaction/summary`、`compaction/end`、`goal/change`、`feedback/record`（`command/run|done`
   已在，保持）。
