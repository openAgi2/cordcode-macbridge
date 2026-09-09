# Grok Build `/compact` 后显示历史恢复：完成报告（2026-09-09）

> 修复提交 `d822caa fix(grokbuild): restore display history from durable update
> stream`（MacBridge `plan/approval-layer`）。前序：`979bc04`（goal 卡片重建，本轮
> 收编）与 `7ee42df`/`88f89fcf`（verifying_completion 贯通，与本轮无关）。

## 现象与根因

owner 报障：grok build 会话（`/Users/jacklee/Projects/Chat`，session
`01a07efa-46cc-79f2-8df5-913122004d77`）一直显示「执行中」，十几个 goal 任务全部
不可见；`979bc04` 重建 8 组 goal 卡后复测，回复正文仍丢失。

根因（官方源码结论，锚点见 GO_BRIDGE_ARCHITECTURE.md「Backend 语义锚点表」）：

- `export.rs:3`：`updates.jsonl` 是持久展示真值，`chat_history.jsonl` 仅为 LLM API
  派生缓存；
- `persistence.rs:2031`：compaction 经 `ReplaceChatHistory` 整体重写该缓存
  （"Replacing chat history (compaction)"）；
- `replay.rs:4,79,483,549`：官方重建 = 流式消费 updates.jsonl + CompactionCheckpoint
  边界处理 + hostTurn 整体抑制（官方测试
  `test_replay_suppresses_full_host_turn_and_flushes_preceding_agent`）。

桥接层此前把派生缓存当完整聊天历史 → `/compact` 后正文必然丢失。

## 修复形态（数据源修正优先）

- `agent/grokbuild/updates_history.go`：新增 `readRichDisplayHistoryFromUpdates`，
  从 updates.jsonl 还原完整显示历史（user/assistant/reasoning/tool 生命周期、
  goal 提醒 → `/goal` 命令卡、hostTurn 过滤与边界 flush，镜像官方回放规则）；
  **不应用 compaction 截断**（展示真值保留完整流，与官方 export 语义一致）；
- `session_catalog.go` `readRichSessionHistory`：已发生 compaction 且流内有展示
  内容时走真值源；未 compact 会话维持原缓存读取路径（降低回归面，
  `TestRichHistoryKeepsChatCachePathBeforeAnyCompaction`）；
- `979bc04` 的 goal 卡片重建收编为真值源之上的装饰（`decorateGrokGoalHistory`
  按 args 去重附着/补卡，不再承担正文恢复职责）；
- 不修改 Grok 会话文件（只读消费）。

## 对账数字（受影响会话，真实数据）

| 维度 | 官方 journal 真值 | 修复后投影 |
| --- | --- | --- |
| assistant 消息 | 10（非 hostTurn 消息组） | 10（含 reasoning/tool/text parts，正文可读） |
| user 输入 | 10（2 条真人 + 8 条 goal 激活提醒） | 2 user + 8 张 `/goal` 命令卡 |
| goal | 9（distinct goal_id） | 8 张完成命令卡 + 1 张活跃 goal 卡 |
| workflow run | 47（distinct subagent） | 按 goal 归组的 workflow part（8 附着 + 活跃卡） |
| compaction | 1 checkpoint | 截断不应用于展示历史 |

修复前对照：0 条正文 + 8 组卡片（`979bc04` 部署版实测）。

## 验证分级

- 组件级（真实 session 数据）：上表对账由真实 `updates.jsonl` 经
  `readRichSessionHistory` 实测得出；临时探针测试已删除。
- 定向测试：`TestRichHistoryUsesDurableUpdatesAcrossCompaction`、
  `TestDisplayHistoryMergesContiguousUserChunksWithStableSourceID`、
  `TestRichHistoryKeepsChatCachePathBeforeAnyCompaction`、
  `TestDecorateGrokGoalHistoryRecoversCompactedAwayGoalCommands` +
  全包 `go test ./agent/grokbuild/ -count=1`（27.8s ok）+ `go build ./...` +
  `go vet` 干净。
- 生产路径运行态：Release 构建（runtime commit `d822caab12e9`）→ 覆盖安装
  `/Applications`（旧版备份 `~/.Trash/CordCodeLink.app.pre-d822caa`）→ 双进程名
  终止重启 → 核验：app PID 24624 / runtime PID 24867 均在 `/Applications` 下、
  8777 由新 runtime 监听、安装二进制 SHA == 构建产物、二进制含 `d822caab12e9`
  特征串、无临时产物进程。
- 最终 UI 收口：owner 在 iPhone 重开该会话确认（预期：10 条回复正文 + 8 张 goal
  完成卡 + 1 张活跃 goal 卡全量可见，状态不再「执行中」）。

## 来源清单

- MacBridge 工作树 `cordcode-macbridge-plan-approval` @ `d822caa`（构建时无其他
  未提交源码改动；另有 3 个与本修复无关的未跟踪 handoff/docs md）。
- grok-build checkout `/Users/jacklee/Projects/grok-build` @ `75810042`
  （origin/main，工作树干净）；安装版官方 `grok 1.0.24 (68e414c661e3)`。
- 受影响会话目录
  `~/.grok/sessions/%2FUsers%2Fjacklee%2FProjects%2FChat/01a07efa-46cc-79f2-8df5-913122004d77`
  （updates.jsonl 957 行 / chat_history.jsonl 44KB post-compact 缓存）。
