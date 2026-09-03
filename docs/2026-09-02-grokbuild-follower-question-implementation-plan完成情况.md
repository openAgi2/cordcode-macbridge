# 完成情况：grokbuild-follower-question-implementation-plan

> Plan: `docs/2026-09-02-grokbuild-follower-question-implementation-plan.md`
> 完成时间: 2026-09-03
> Queue: 33/33 done, 0 blocked/pending
> 结论: owner 矩阵四行全部真机验收通过

## 总览

Grok Build follower 问题应答（裁决 B：question only）全链路交付：Mac 端 grok 弹出的
`ask_user_question` 多选/单选卡可在 iPhone 上直接作答，Mac 先答则 iPhone 静默收口，
断线（杀 App）重开后挂起的问题卡随会话水合回来且仍可答，关闭 Leader 模式回退只读
基线。范围按 owner 授权代定的六点裁决执行：question only、permission 不做、无期限
等待、抢答静默收口、不改协议包、exit_plan_mode/mcp/elicit 移出。

## 交付内容

### Phase 1：请求门与 codec
- leader subscriber 方法门从「静默丢弃 REQUEST 帧」改为按 request kind 分发
  （`x.ai/ask_user_question` 进入应答队列），未知 kind 照旧拒绝。
- interaction 编解码：上游 serde 双收 `multi_select`/`multiSelect`（模型
  chat_history 原文 snake_case）；option 无 id 字段，CordCode 以 label 当 id；
  freeform 恒开（官方 TUI modal 从不带 no-freeform 门），上行 label `"Other"` +
  annotations notes。
- 多题一次提问：interactionId = `questionIDFor(toolCallID, i)`（单题裸 id、多题
  `#i` 后缀），live 与 hydrate 跨源对齐。

### Phase 2：应答通道
- 双轨应答路由（`grokbuild.go`）：leader rail（leader socket 活——转发官方
  应答帧）或 driver rail（每 turn ACP 子进程）；`SessionQuestionResponder` +
  `UserInputResponder` 双接口。
- 抢答 first-writer-wins；`Resolved elsewhere` 类结果按官方语义收口，不报错。

### Phase 3：双轨发射与 iOS 面
- `question_events.go`：canonical `user_input_requested`/`user_input_resolved`
  （SSV2 投影 user_input part，v2 权威面）+ derived-legacy `question_asked`/
  `question_resolved`（EventPublisher 不 ingest，仅 v1）。
- capability：`StructuredUserInputProvider` 推导 `structured_user_input_v1`；
  legacy `question_reply` 故意不宣告（与 opencode-web 同策略）。
- iOS 零新渲染逻辑，复用既有 UserInputDock。

### 断线恢复（owner 重测驱动的四轮修复链）

| 轮 | commit | 根因 | 修复 |
| --- | --- | --- | --- |
| D-G3 | `1fc6f1f` | leader 死 → 订阅 ECONNREFUSED → 永久回退 tailer | 订阅统一循环 + 10s dial 探测 reclaim；freeform Other+notes 同轮 |
| D-G4 | `fdb7d97` | 订阅活着但全冷 hydrate 不产 question 帧 | `questionsLive=leaderSocketDialable` 门：未答 `ask_user_question` tool_call 产出 pending user_input part；leader 死回落 tool step 全终态基线 |
| gate | `ceffc9c` | pending turn 故意无终态 + grokbuild 无 §3.1 信号 → 提交门永不 ready → 15s 超时死循环 | `HasSessionSubscriber` 为 live 信号（拉取即订阅，subscribe 先于 hydrate）；窄门在 agent 层 questionsLive |
| panic | `8c1ac9b` | Restore 漏建 `userInputs` map → 首个 live ask 写 nil map → runtime 崩溃重启循环 | Restore 补建 map + 从基线 pending part 重建 interactionId→turn 索引（live 重发沿用 hydrate turn） |

## owner 矩阵验收（2026-09-03，self-attested：owner 真机回报）

| 行 | 场景 | 结果 |
| --- | --- | --- |
| 1 | iPhone 直接作答问题卡（含 type your answer 自由输入） | ✅ 09-02 / 09-03 两轮确认 |
| 2 | Mac 先答，iPhone 卡片静默收口 | ✅ 09-02 |
| 3 | 杀 App 重开 → 挂起问题卡恢复且可答 | ✅ 09-03（经四轮修复链） |
| 4 | 关闭 Leader 模式回退只读基线 | ✅ 09-02 |

## 验证证据

- **单元/回归测试（re-verified，本轮命令可复跑）**：
  - `go test ./agent/grokbuild/ -count=1` ok（含 hydrate pending question、
    codec snake/camel、fail-closed 等新增用例）
  - `go test ./go-bridge/ -run 'TestReducer|TestGrokBuild|TestProjection|TestHydrate|TestSessionProjection' -count=1` ok
  - panic 回归测试反向验证：临时还原 nil map 后复现与生产一致的
    `panic: assignment to entry in nil map`，恢复修复后 ok
- **部署（self-attested）**：四轮 Release 构建均走 build-unsigned-release →
  killall+pkill → 覆盖安装 `/Applications` → open → 四门核对（进程代际晚于构建、
  8777 监听者为内嵌 runtime、无临时产物残留、二进制含当轮 commit）。最终部署
  runtime `8c1ac9b98e9b`（2026-09-03T04:57:47Z）。
- **生产运行态（self-attested，owner session 01a06045）**：panic 修复后 iPhone
  自动重连被新 runtime 完整处理——`hydrate_commit` 10ms → `delta_at_head` 应答 →
  leader live ask 注册 → runtime 稳定零 panic 零重启（此前每轮 panic）。
- **真实 fixture（self-attested，owner 现场只读取证）**：
  `~/.grok/sessions/…/01a06045…/` updates.jsonl 505 行 pending tool_call +
  chat_history.jsonl `multi_select` 原文形状；hydrate probe 验证 interactionId
  与 canRespond 语义（probe 为临时测试进程，已删）。

## 文档同步

- CHANGELOG `[Unreleased]` 新功能条目。
- `GO_BRIDGE_ARCHITECTURE.md` Grok Build 节新增 follower 问题应答 + 断线恢复
  两小节；capability 表补 grokbuild 行。
- `think.md` 总账行状态更新 + 2026-09-03「四层剥洋葱」复盘（教训：修复生效后
  新故障常是揭盖而非回归；同症状先分运行层/行为层；HasSessionSubscriber 当
  live 信号前先查 RPC 自身订阅副作用；Restore 必须重建派生索引）。

## 后续另案（不在本 plan 范围）

- permission 类交互、mid-turn interjection、iOS 作为 leader 客户端的根治路径
  （think.md 总账「Grok follower 交互升级」行）。
- iOS 发送 Grok 模型条目 id（think.md 总账既有行）。
