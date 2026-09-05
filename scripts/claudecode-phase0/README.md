# claudecode-phase0 — Claude Code backend 官方能力收敛升级 Phase 0 证据包

设计来源：`docs/2026-09-04-claudecode-official-capability-upgrade-design.md` §6 Phase 0。
对标 `scripts/dsh-gate0/`（DSH_TURN_REPRO）惯例：探针脚本 + dumps 入仓，供 CLI 升级后复测。

## 来源与保真度注记

- 探针 env 捕自活体 `claude --input-format stream-json` 子进程（PID 78768）。事后核对其
  变量集含 `CLAUDE_CODE_OAUTH_TOKEN`/`CLAUDE_CODE_HOST_*` 等 harness 变量——该子进程实为
  **Desktop 宿主 spawn**，非 CordCode runtime spawn。两路在本机汇聚于同一 settings/env
  材料（bigmodel 直连 + 别名族），控制协议/hooks/目录形状结论不受宿主差异影响；CordCode
  生产 spawn 的 env 公式见 `agent/claudecode/session.go:212-222`（10 变量 allowlist +
  provider env + ENTRYPOINT）。
- 归档已做 secret scan：`ANTHROPIC_AUTH_TOKEN` 值与 `sk-ant-` 模式在全部 dumps/脚本中
  零命中（2026-09-04）；`runtime-env.mirror` 本身 0600 且 gitignore。

## §7.1 红线清单核对（Phase 0 收口）

| 红线项 | 状态 | 证据 |
|---|---|---|
| `list_models` 成功体 | ✅ 已 dump | dumps/main.jsonl req_2（`{"models":[ModelInfo×5]}`） |
| 本机 streaming spawn 的 `initialize` 成功体 | ✅ 已 dump | dumps/main.jsonl req_1（21KB 载荷） |
| `set_model` control_response 原文 | ✅ 已 dump | req_3/req_4（裸成功体） |
| `set_permission_mode` control_response 原文 | ✅ 已 dump | req_5/req_6（含 bypass 场景 req_c2） |
| `interrupt` control_response 原文 | ✅ 已 dump | req_7（`still_queued`+`cancelled`） |
| `rename_session` 成功体 | ✅ 已 dump | req_8（裸成功体） |
| Stop/SessionStart HTTP POST 原文 | ✅/❌ | Stop ✅（hooks-posts.jsonl）；SessionStart 在 `--settings` 层**不触发**（2.1.234 实测边界，非遗漏） |
| PostModelSwitch POST 原文 | ➖ 不可得 | 需 ≥2.1.251；本机 2.1.234 文档级不存在（设计 §3.2，不冒充 unknown） |
| `--settings` 与 user hooks 合并后的 effective hooks | ✅ 行为级 | 用户层 2 hooks 与 settings 层 hooks 并存触发（hooks.jsonl hook 帧 + posts）；合并清单本身无官方 dump 面 |

## 运行方式

```bash
./capture_runtime_env.sh        # 从活体生产进程捕获 provider env → runtime-env.mirror（0600，不入仓）
./control_plane_probe.py main   # 控制面主序列
./control_plane_probe.py bare-list   # 裸 list_models 对照（M1）
./control_plane_probe.py bypass      # bypassPermissions 单列（R2-S6）
./control_plane_probe.py turn        # 真实 turn：system/init capabilities + message.model
```

探针 spawn 逐字复用生产 `baseClaudeInnerArgs`（agent/claudecode/session.go:108）：
`--output-format stream-json --input-format stream-json --permission-prompt-tool stdio
--include-partial-messages --verbose`，**无 `-p`**，stdin 保持打开。
env = 生产 allowlist 10 变量 + 活体进程捕获的 provider env + `CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p`。
`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` 值在写入磁盘前已在内存中替换为 `***REDACTED***`。

## 2026-09-04 探针结论（CLI 2.1.234，PATH npm 全局）

来源清单：本仓 `claudecode/official-capability` @ bdc2cda；env 捕自活体生产 claude 子进程
（PID 78768，cordcode-bridge-runtime 90179 之后代）；`claude --version` = 2.1.234。

### 控制面（p0-1，dumps/{main,bare-list,bypass}-summary.json）

| subtype | verdict | 成功体形状（原文见 dumps） |
|---|---|---|
| `initialize` | success | `response.response` 载荷含 `models`(ModelInfo[])、`commands`、`agents`、`account{apiProvider,tokenSource}`、`current_permission_mode`、`output_style`、`available_output_styles`、`fast_mode_state`、`pid` 等；裸请求（无 hooks）时 `hooks_applied` 缺席（与 SDK 契约一致） |
| `list_models` | success | `{"models":[ModelInfo…]}`——与 initialize.models **逐字节同构**；**无需先 initialize**（bare-list 场景 before==after） |
| `set_model` | success | 裸成功体 `{"subtype":"success","request_id":…}`，无载荷 |
| `set_model "default"` | success | 同上（重置语义生效） |
| `set_permission_mode acceptEdits` / `default` | success | 载荷 `{"mode":…}`；生效确认走 **system/status 广播帧**（`permissionMode` 字段，带 session_id/uuid） |
| `set_permission_mode bypassPermissions` | success | 载荷 `{"mode":"bypassPermissions"}`。⚠️ 本机语境：用户级 `~/.claude/settings.json` 已有 `defaultMode=bypassPermissions` + `skipDangerousModePermissionPrompt`，干净机器（无该设置）可能被拒——不外推 |
| `interrupt (cancel_queued:true)` | success | 载荷 `{"still_queued":[],"cancelled":[]}`——2.1.234 **已有** `cancelled` 字段（设计 M6「旧 CLI 可能无 still_queued」未发生）；空闲会话发送合法 |
| `rename_session` | success | 裸成功体，无载荷 |

信封双向均按 §3.1：请求 `{"type":"control_request","request_id":…,"request":{…}}`；
响应 `{"type":"control_response","response":{"subtype":"success","request_id":…,"response":{…}}}`
（**载荷嵌套在第二层 `response`**）。

### capabilities（p0-4，dumps/turn.jsonl）

`system/init` 帧**只在首个真实 turn 时出现**（启动后、输入前不发射；生产 handleSystem 只取
session_id，不受影响）。本机 capabilities：

```json
["interrupt_receipt_v1", "interrupt_cancel_queued_v1", "msg_lifecycle_v1"]
```

- **无 `modelCatalog` 字符串**（M2 证实），但 list_models 实发 success——「无 cap 字符串 ≠ 无
  list_models」成立，能力探测必须以实发为准。
- `interrupt_receipt_v1` + `interrupt_cancel_queued_v1` 均在——interrupt 回执两档可用。

### 目录真值（p0-3）

- 目录 = 标准 Anthropic 槽位 5 条：`default`/`opus[1m]`/`sonnet`/`sonnet[1m]`/`haiku`；
  `resolvedModel` 全为 canonical（`claude-opus-5[1m]`、`claude-sonnet-5`、
  `claude-haiku-4-5-20251001`）。
- **cc-switch 别名（env `ANTHROPIC_DEFAULT_*_MODEL=glm-*`）不改变目录映射**：sonnet 槽 resolved
  仍是 `claude-sonnet-5`，不是 glm-5.3。请求侧名与执行侧名分离：turn 场景请求
  `claude-opus-5[1m]`（system/init.model），实际执行 `glm-5.3`（assistant message.model）。
- ModelInfo 字段全集：`value`、`resolvedModel`、`displayName`、`description`、`supportsEffort`、
  `supportedEffortLevels`、`supportsAdaptiveThinking`、`supportsFastMode`、`supportsAutoMode`
  （haiku 无 effort 族字段——可选字段，解析必须容忍缺席）。

### 代际矩阵（p0-2）

| 通道 | CLI 代际（实测锚） | PostModelSwitch/PreModelSwitch | 控制面结论 |
|---|---|---|---|
| CordCode 自 spawn worker（PATH CLI） | **2.1.234**（`claude --version` + system/init `claude_code_version` 双锚） | **文档级不存在**（官方需 ≥2.1.251）；不消耗探针预算冒充 unknown | 六 subtype 全 success；capabilities=`interrupt_receipt_v1`+`interrupt_cancel_queued_v1`+`msg_lifecycle_v1`，无 `modelCatalog` 字符串但 list_models 实发可用 |
| 外部 Claude App 会话 | **2.1.258**（`~/.claude/sessions/25813.json`，2026-09-04 12:35 起，entrypoint=claude-desktop-3p，kind=interactive，peerFeatures=notify_idle/reply_across_default_dirs/artifact_yield） | 存在（文档门） | 不可控（随 Desktop 更新）；CordCode 只经文件面旁观，事件注入按 M4 默认关闭 |

两行结论**不得合并成单一真值**（设计 §3.2）。SDK 类型契约配对代际为 2.1.260（参照物，本机无
此 CLI）。

### hooks（p0-5，dumps/hooks-posts.jsonl + hooks.jsonl）

`--settings` 内联 JSON（`{"hooks":{"<Event>":[{"hooks":[{"type":"http","url":…,"timeout":…}]}]}}`）
+ 本地 token 鉴权接收端（**token 走 URL 路径段**——本机活样本证明 HTTP hook 配置无 headers
字段；错 token 路径返回 404 时 hook 不失败会话）。事件 POST 原文已归档。

| 事件 | `--settings` 层触发 | POST 体关键字段 |
|---|---|---|
| `SessionStart` | **❌ 不触发**（无 matcher 与显式 `matcher:"startup"` 均不触发；启动 hooks 派发先于 `--settings` 层生效，2.1.234 实测） | — |
| `UserPromptSubmit` | ✅ | session_id, transcript_path, cwd, permission_mode, prompt, prompt_id |
| `Stop` | ✅ | session_id, transcript_path, cwd, permission_mode, effort, prompt_id, **last_assistant_message**, stop_hook_active, background_tasks, session_crons |
| `ConfigChange` | ✅（**文件必须在 spawn 前已在 cascade 中**；中途新建不算变更，2.1.234 实测） | session_id, transcript_path, cwd, prompt_id, **source**（如 `project_settings`）, **file_path** |
| `SessionEnd` | ✅ | session_id, transcript_path, cwd, prompt_id, reason |
| `PostModelSwitch`/`PreModelSwitch` | 未测（需 ≥2.1.251；本机 2.1.234 文档级不存在） | — |

- **R2-S7 对照成立**：裸 initialize（无 hooks 字段，req_h1 success）后 settings 层 HTTP hooks
  照常触发——`hooks_applied` 语义只覆盖 SDK callback hooks。
- **hooks 跨层 merge 实证**：用户层 SessionStart hooks（死脚本 exit=127 + ai-reminder exit=0）
  与 `--settings` 层 hooks 并存于同一会话；stdout 的 `system/hook_started`/`hook_response` 帧
  可观测 hook 执行（--verbose）。
- **S3 静默失效 fixture**：`dumps/fixture-hook-silent-failure.jsonl`（真实 transcript 的
  `attachment.type=hook_non_blocking_error` + `exitCode:127` + stderr，会话继续）；7823 死端点
  2026-09-04 复查仍无监听。
- **Managed 层**：`/Library/Application Support/ClaudeCode/` 不存在——本机**无可测环境**（非硬门
  失败，按设计记档）。

### 网关 /v1/models 复测（p0-7，dumps/gateway-retest.json + gateway-raw-*.json）

cc-switch（PID 95162，127.0.0.1:15721）仍在监听。2026-09-04 复测相对设计 §0.4 快照
**两处漂移**：

| 目标 | 鉴权头 | 结果 |
|---|---|---|
| `http://127.0.0.1:15721/claude-desktop/v1/models` | `x-api-key` | 401「缺少 Authorization 头」 |
| 同上 | `Authorization: Bearer` | **200 + 模型列表**（claude-fable-5、claude-haiku-4-5⊗supports1m、claude-opus-5⊗supports1m、claude-sonnet-5；OpenAI 风格 `{data,first_id,has_more,last_id}`）——设计快照「bigmodel key 被拒」**不再成立** |
| `http://127.0.0.1:15721/claude/v1/models` | `x-api-key` | 404 空（维持「无响应体」） |
| `https://open.bigmodel.cn/api/anthropic/v1/models` | 双头各测 | 200 但 **`data:[]` 空**——设计快照「10 模型」**不再成立** |

推论：/v1/models 网关面**随供应商配置漂移**，任何"网关目录拉取"只能是降级链的一级，
不能当稳定真值（Phase 1.4 的 S7 修正与此一致）。

### 顺带取证

- 启动时用户级 SessionStart hooks 在 stdout 发射 `system/hook_started`/`system/hook_response`
  帧（--verbose）；本机活样本 `SessionStart:startup` exit=127（死的 cc-event-hook.sh）——
  S3 静默失效证据在控制流探针里也可见。
- `system/init.apiKeySource="none"`（env 双 key 存在时仍报 none——字段语义待 Phase 1 不依赖）。

## 2026-09-05 复测：CLI 升级 2.1.234 → 2.1.261（六项全绿 + 两个新证据面）

背景：owner 指令三项收尾（选择器直调 / CLI 升级评估解锁 PostModelSwitch /
get_context_usage 升 A）。评估路径：隔离安装 `npm i --prefix /tmp/claude-261
@anthropic-ai/claude-code@2.1.261` → 探针复测 → changelog 234→261 无控制面 /
stream-json 输出格式变更 → transcript type 集与形状锁 fixture 零新 type →
全局升级（`claude --version` = 2.1.261）。

复测结果（本节 dumps 为 2.1.261 覆写，2.1.234 原始证据在 git 31a5afe）：

| 项 | 结果 | 证据 |
|---|---|---|
| 控制面六 subtype | 全 success | dumps/main-summary.json |
| `get_context_usage`（新场景 `ctx`：init → 真实 turn → summary/full/default 三模式） | 全 success，富载荷 | dumps/ctx.jsonl req_x2/x3/x4；fixture `agent/claudecode/testdata/context-usage/get_context_usage-summary-2.1.261.json`（total 19626 / max 200000 / 分类明细；2.1.261 字段超出 SDK 0.3.260 类型声明：autoCompactThreshold / messageBreakdown / skills / slashCommands / apiUsage 等） |
| PostModelSwitch / PreModelSwitch hooks | **实证触发**（set_model 直达引发，default→slot→default 两轮） | dumps/hooks-posts.jsonl：body 含 `from_model`/`to_model`（**网关改写后观测名**，glm-5.3 → glm-5.3[1M]）、`requested_model`（default 重置时 null）、`source:"sdk"` |
| Stop / ConfigChange / UserPromptSubmit / SessionEnd | 照常 | 同上 |
| transcript 形状 | 与 fixture 零新 type（atis-latch/attachment/queue-operation 等既有枚举内） | 当日 workdir-* 会话 type 集合 diff |
| interrupt 早期 turn 忽略 | 2.1.261 官方修复（changelog） | 上游 CHANGELOG |

**env mirror 重建注记**：本日 mirror 曾失效（刷新误抓 Desktop 会话 env——无
ANTHROPIC 键；`CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p` 下 CLI 走 Desktop 认证
通道，无显式 provider env 即 "Not logged in"）。重建 = 从 `~/.claude/settings.json`
env 块复制 ANTHROPIC_*/API_TIMEOUT 等（即当前 cc-switch 解析态，bigmodel 直连）。
失败症状（196ms `<synthetic>` + "Not logged in" + Stop 不触发）已入档，供诊断。

**成功体嵌套提醒**：initialize/list_models/get_context_usage 的成功载荷在
`control_response.response.response` 双层（controlChannel/controlPayload 处理）；
interrupt/set_model 等裸键在第一层 response。探针脚本 scenario 解析已按此对齐。
