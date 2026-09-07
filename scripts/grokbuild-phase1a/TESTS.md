# Phase 1a（目录方向·Mac）实现与测试证据

- 工作树：`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（plan/approval-layer）
- 基线：6b6994a2ab20e911673da9145e112fe642bd9a92 + 本单元变更
- 日期：2026-09-07
- 运行日志：`scripts/grokbuild-phase1a/test-run.log`（定向 -v + 三包回归 + build + vet，全 exit=0；回归行为同 commit 前实时跑：grokbuild 29.2s / core 1.6s / go-bridge 78.5s）

## 交付物（文件）

| 文件 | 内容 |
|---|---|
| `agent/grokbuild/acu_state.go` | ACU side-state：`(sessionID,cwd)` 键控条目；notif 槽 + list 槽（list 优先，迟到无序 ACU 不覆盖 list 值）；`markListFailed` 禁用身份；`invalidateSession/invalidateAll`；D1 准入 `applyGrokAdmission`（P6 前官方交集为空=诚实空面板；排除 context/feedback/dream/flush/always-approve） |
| `agent/grokbuild/session_commands.go` | 权威 List = 专用 child（`newGrokSessionACU` per-turn actor）load 后读会话 ACU；settle：≥1 波 + 静默窗，ctx 到期且 ≥1 波取最新，零波=硬失败并 `markListFailed`；成功 `storeListSuccess`（全量表）+ 返回 D1 交集；`ExecuteSessionCommand` fail-closed（1b 接线前 "not enabled"）；`grokCommandsReady`/`SessionCommandsReady` readiness 门 |
| `agent/grokbuild/acp_codec.go` | `parseAvailableCommandsUpdate`：1.0.13 实测形状 `{sessionId, update:{sessionUpdate:"available_commands_update", availableCommands:[{name,description,input:{hint}|null}]}}`（无 commandId——vs 1.0.16 源码漂移，见 phase0 EVIDENCE.md 漂移表） |
| `agent/grokbuild/session.go` | `grokSession` 增加 `cwd` 快照 + `acuObs` 钩子；spawn/load 对齐 cwd；`session/update` 通知先过 ACU 钩子 |
| `agent/grokbuild/grokbuild.go` | Agent 持有 `acu`；`SetProviders`→`invalidateAll`；`SetWorkDir` 不失效（loadSession 每 turn 对齐、键控已隔离）；leader rail `onACU` → storeNotification |
| `agent/grokbuild/session_admin.go` | `DeleteSession` → `invalidateSession` |
| `agent/grokbuild/leader_subscriber.go` | LeaderSubscriber `onACU` 回调（convertSessionUpdate 前解析） |
| `core/session_commands.go` | `SessionCommandReadiness` 接口 + `SessionCommandsAdvertise`（catalog 断言 + readiness 门；无 readiness 接口的后端保持纯断言） |
| `go-bridge/backend_capabilities.go` | capability 广告改走 `core.SessionCommandsAdvertise` |
| `agent/grokbuild/session_commands_acu_test.go` | 本单元定向测试（8 个） |

## 测试矩阵（8/8 绿）

| 测试 | 证明 |
|---|---|
| TestParseAvailableCommandsUpdate | ACU 真实样本形状解析（hint/null input/namespaced 名透传/空表合法/CMU 不误判）——形状锚定 phase0 样本 p2、p7-14 |
| TestACUSideStateSemantics | §9 目录组：空表替换非空表、list 优先且迟到 ACU 不覆盖、失败禁用身份（无旧表回退）、fresh pull 恢复、跨会话/跨 cwd 隔离、invalidateSession 单删、invalidateAll 全清 |
| TestApplyGrokAdmission | D1 官方目录 ∩ 已验证准入集合；P6 前空准入=空面板；排除集合生效 |
| TestSessionCommandsAdvertiseGate | readiness 门：gated 后端 not-ready 不广告、ready 广告；grokbuild 1a 现状（未过 1b+P6 门）不广告；readiness 翻转才广告 |
| TestListSessionCommandsFakeChildWaves | 专用 child e2e：partial 波→200ms→全表波，settle 取最新波；whitelist 缓存全量 3 条（D1 显示为空是准入诚实态） |
| TestListSessionCommandsFakeChildEmptyTable | 空表=成功（合法表），不是错误 |
| TestListSessionCommandsFakeChildZeroWavesFails | 零波=硬失败 + 身份标记不可用 |
| TestExecuteSessionCommandFailsClosed | 1b 前 Execute fail-closed |

fake child 为内部故障注入（perl fake ACP peer），官方形状由 phase0 真样本守护——两路证据分离（方案 §8 证据门）。

## 实现注记

- **fake mode 必须烧进脚本字面量**：session launcher 以 argv `("agent","--no-leader","stdio")` exec CLI，`shift @ARGV` 会取到 `"agent"` 导致三模式全不匹配（曾表现为 waves 测试 "no ACU observed"；silent 测试因未知 mode 恰好静默而"通过"——已修，两处均以真实语义通过）。
- perl fake：`$| = 1` autoflush；Go 反引号内 `\n` 是字面量，真换行须 `"' . \"\\n\";"` 拼接。
- `SetWorkDir` 不触发 `invalidateAll`：loadSession 每 turn 调 SetWorkDir 做全局对齐，交替 cwd 会话会每 turn 清缓存；`(sessionID,cwd)` 键控已结构性隔离（配置级失效走 SetProviders，会话重建走 DeleteSession）。

## live 官方形状锚点（phase0 样本，非本单元重跑）

- 专用 child load 后会话 ACU 43 条（含 feedback/loop/reload-plugins 运行时命令）> catalog `_x.ai/commands/list` 34 条 → List 主通道=child 读 ACU（设计期决策）
- initialize `_meta.availableCommands` 7 条内置；ACU 无 commandId（1.0.16 源码有 → 漂移表已录）
- 样本：`scripts/grokbuild-phase0/samples/`（p7-14 等 17 份脱敏样本）

## 未含 / 后续

- Execute 真实链路 = p1b（共用 turn dispatcher、Events 单消费者、terminal future、取消清理；依赖 P6 终态样本 → p0b）
- readiness 翻正（`grokCommandsReady`）在 1b + P6 门通过前保持 false → capability 不广告（§5.1 门）
- context/feedback 等排除命令的准入扩充在 P6 验证后（D1：官方目录 ∩ 已验证反馈类型）
