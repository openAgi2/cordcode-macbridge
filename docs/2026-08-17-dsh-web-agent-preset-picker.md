# dsh-web Agent Preset 选择（设计补篇）

- 日期：2026-08-17
- 状态：补篇（挂在 [2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md) §4.3.8 `list_agents`）
- 范围：iPhone 空白页选出官方 Agent 预设；开聊后锁定。不接复制/删除/创造模式创作。

## 为什么要单独写这段

原设计把 `list_agents` / `agentPreset.*` 标成二期，理由是「单一 preset」。官方 web 现在新建会话就能选标准 / PTC / 极简 / 创造，这不是权限档（齿轮那三项），是**会话装哪套插件和工具**。官方还有「空白才能换、开聊后 `agent-preset-locked`」。不先写死这些，很容易和齿轮、OpenCode 的 Agent 切换混在一起。

## 官方事实（源码，不是猜的）

| 事实 | 出处 |
|---|---|
| 预设 id：`standard` / `code` / `minimal` / `cordis` | `apps/cli/config/agent-presets/*/preset.yml`；界面名 标准 / PTC / 极简 / 创造 |
| 列表：`agentPreset.list` → `{presets[{id,trust,isDefault,name?,description?,broken?}], authorable, hasDocument}` | `packages/host/apiproxy/src/api/agent-presets.ts` |
| 创建：`session.create{agentPreset?}` | 同仓库 `sessions.ts` |
| 空白会话改选：`agentPreset.select{sessionId,agentPreset}`；已开聊回 `agent-preset-locked` | `agent-presets.ts` select 注释 + `rpc.ts` |
| 出厂默认 `standard`；用户可在 web 改默认 | e2e：`defaultId` + settings |
| `read` / `copy` / `remove` / `openDocument` 是创作面 | 本期不做 |

## 产品

- 只出现在 iPhone **空白新会话页**（和 Mac / 目录 / 工作树 / 分支同一组）。
- 列出官方 `agentPreset.list`，标题用 `name`（极简模式），点选改当前选择。
- `broken` 的预设不给选。
- 未选时显示 `isDefault` 那一项。
- **还没发过消息**：可以换。
- **已经有对话**：这一行随空白页一起消失；即使误调官方也会锁。
- 和齿轮权限档无关。

## 接线

1. Mac `ListAgents` = `agentPreset.list`。`name` = 官方 id（select / create 用这个）；`displayName` = `name` 文案；`description` 原样；`isDefault` 原样。
2. iPhone `create_session` 带可选 `agentPreset`（当前选中的 id）。Mac 在 `session.create` 原样传给官方。
3. 空白但已有 sessionId 时（少见）：`set_agent_preset` → `agentPreset.select`。官方锁了就如实报错。
4. 创作类 RPC 不接。
5. capability：实现了 `ListAgents` 即可；iOS DeepSeek Harness 用空白页这一行，不恢复输入条 Agent 标签。
6. 已开聊会话：官方 `session.list` / `get_session` 的 `agentPreset` 经 `AgentSessionInfo` → wire `agentPreset` 到 iPhone。消息页副标题行显示「🧩 标准模式」芯片（和官方 web 标题左一致）。会话列表 / 侧栏 / 预览卡不标预设。只展示，不能在已开聊会话里改。

## 不做什么

- 不在输入条加回 Agent。
- 不把 preset 当成 Read Only / Workspace Write。
- 不在 iPhone 上复制/删除预设。
- 不手写四套预设当真值表，列表永远来自运行时。
