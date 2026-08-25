# Codex Web owner 真机验收矩阵

对应设计：`docs/2026-08-21-codex-web-backend-design.md` §13.3。

> 2026-08-25 同步：agent 可执行的模型目录三项 unit-only 测试已在连接的物理 iPhone 上重跑，3/3 PASS；Go adapter、owner-matrix validator 与 Phase 6 fail-closed validator 也已复验通过。owner 已确认“新建/既有 session 首次发送即时显示”和“Desktop 长任务完成后 iOS 终态收口”修复符合预期，这些证据补强第 6–8 行，但不替代下表仍标 `NOT RUN` 的真实交互、Relay、provider、断网或旧入口回滚动作。

## 自动前置核对（已完成）

- Mac Release：`/Applications/CordCodeLink.app` 已覆盖安装并运行，内嵌 runtime commit
  `364dec7ce099`，TCP 8777 监听者来自安装目录。
- Shared runtime：官方 standalone/daemon 与 Desktop 内嵌 CLI 均为 `0.149.0-alpha.4`；
  `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1` 已写入用户 launchd domain；CordCode 主/observer
  connection 与隔离 Desktop 已由 UDS FD 证明连接同一 daemon，且无 managed-loopback 进程。
- Bridge：Release 内嵌 `cordcode-bridge-runtime` 正在监听 TCP 8777，启动参数同时包含独立的 `codex` 与 `codex-web`。
- iOS：`codex/codex-web-backend-ios` 已构建并安装到连接的物理 iPhone；Codex Web 模型目录保留官方
  `model/list` 顺序与 description；物理 iPhone 上的定向 unit test 已通过，测试基础设施提交为 `53e7d79`。
- 运行路径：真机启动后 Bridge 日志已出现 `backendId=codex-web` 的 `list_models`、`get_session_projection`、`set_observation_scope` 与 snapshot hydrate；这只证明接线可达，不代替下列 owner 视觉/交互验收。
- Desktop 前置：当前主 Desktop 在环境写入前已运行，本轮没有自动终止它。执行第 6–8、10–11 行前，
  owner 必须先正常退出并重新打开 Desktop 一次；之后各行不得再以“退出另一客户端”为切换条件。

## 执行规则

1. 只使用专门的验收 session，旧 Codex 与 Codex Web 不得共用写入中的测试 session，除第 12 行专门验证 ownership 冲突外。
2. 每行记录 `PASS`、`FAIL` 或 `BLOCKED`，不得用“看起来正常”代替结果；失败时保留 Bridge 日志时间窗、session id 前 8 位与 iPhone 录屏/截图。
3. 涉及 provider/model 的行，记录界面实际显示值与官方客户端实际值；不要抄配置期望值。
4. Relay 行应关闭 LAN 直连条件后再执行，并从连接状态确认确实走 Relay。
5. 自动安装和日志可达性不计作人工验收通过；owner 的真实失败必须保留为 `FAIL`，修复后在同一行追加复测结果。

## 14 行矩阵

| # | 前提条件 | 动作 | 应看到 | 结果 | 证据/备注 |
|---:|---|---|---|---|---|
| 1 | LAN；Codex Web；新 session | iPhone 发一个足以产生长回答的请求 | 首 token 后连续流式；正文不重复；完成态只收口一次 | FAIL（历史，待重跑） | 首轮 provider 误拒绝已由 `3f10df2` 修复。13:35 owner 复测已有 session `01a0236d…`，官方 API 拒绝历史 assistant item `resp_2026082116260046da0120a8a24374_msg`：Message ID 必须以 `msg` 开头。官方当前源码 `core/src/client.rs::prepare_response_items_for_request` 只调用 `ResponseItemId::is_prefixed`，而后者接受任意非空 prefix/suffix，故 `resp_…_msg` 未被剥离；这是官方 runtime 的历史兼容缺口，不在 iOS/MacBridge wire 映射层。禁止直接改 rollout、自动 rollback 或假 fork。2026-08-25 首发即时显示已通过，但尚未按本行完整重跑新 session 长回答、连续 delta 与唯一终态，故不升级为 PASS。 |
| 2 | Relay；Codex Web；新 session | 使用与第 1 行等价的长回答请求 | 内容与 LAN 一致；无整段延迟后一次性跳出；无重复 | NOT RUN | 记录 Relay 状态、session 前缀、录屏 |
| 3 | LAN；daemon 已运行；Terminal Codex 默认配置；active session | Mac 发长回答，iPhone 打开同一 session | TUI 使用 LocalDaemon；iPhone 实时进入执行中并连续显示 delta | NOT RUN | 记录 daemon/TUI 选择证据与 session 前缀 |
| 4 | LAN；daemon 未运行；先启动 Terminal Codex 默认配置 | Mac 发长回答，再打开 Codex Web | TUI 使用 Embedded；iPhone 不伪造 live stream，只按已验证的 list/read 边界显示 | NOT RUN | 记录进程与事件时间线 |
| 5 | LAN；daemon 已运行；Terminal Codex 带 `-c`、strict 或 non-replayable 覆盖 | Mac 发长回答，iPhone 打开同一 session | TUI 使用 Embedded；iPhone 不串入该隔离 turn | NOT RUN | 写明具体覆盖参数；记录隔离证据 |
| 6 | LAN；Desktop 与 CordCode 已证明同 daemon；双方保持打开 | Desktop 发消息 | iPhone 立即进入执行态，连续显示同 turn delta 与唯一终态；不得切 session/刷新才出现 | PASS | owner 2026-08-22 在重启 Desktop 后复测并确认符合预期；2026-08-25 又确认 Desktop 发起的十几分钟长任务结束后 iOS 唯一终态收口修复符合预期；T0 FD/socket 证据见 `scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md` |
| 7 | 双方保持打开；Codex Web 创建并完成 | 在 Desktop 原地发现并续聊 | 原 session、原 cwd、原 effective provider/model 可继续；不得重启/复制/迁移 | PASS | owner 2026-08-22 确认 T1 双向接力符合预期；2026-08-25 确认新建与既有 session 的首次发送都能立即显示；exec-plan 证据 `p0-topology-v2-hardening-regression` |
| 8 | 双方保持打开；Desktop 创建并完成 | 在 iPhone 发现并续聊 | 原 session 可继续且 Desktop 同时打开不报 active writer；不得退出任一端 | PASS | owner 2026-08-22 确认 T1 双向接力符合预期；无 active writer 冲突 |
| 9 | custom provider 已配置为 effective provider | 新建与续聊各一次 | 继承同一 effective provider；模型目录不被手写替换；iOS 不显示未实现的 provider 切换 | NOT RUN | 记录真实 provider、model 与目录截图 |
| 10 | turn 请求 command/file approval | iPhone 分别执行允许与拒绝 | 官方 turn 对应继续或拒绝；状态由 resolved/completed 收口；卡片消失一次 | NOT RUN | 允许/拒绝各留一条 interaction/session 证据 |
| 11 | turn 发出多题 `requestUserInput` | iPhone 完整作答并提交 | 只发送一次官方 response；turn 继续；题目不丢失、不提前完成 | NOT RUN | 记录题数、答案提交与卡片收口 |
| 12 | Codex Web 正在写一个 thread | 尝试用旧 Codex 打开同一 thread | 明确显示 ownership 冲突；双方不崩溃；旧 Codex 不 kill/steal 写入者 | NOT RUN | 保留冲突错误原文与进程状态 |
| 13 | Codex Web 有 live turn | 中断网络，再恢复连接 | 历史冷校准不重复；active/terminal 状态与官方一致 | NOT RUN | 记录断网/恢复时间、重连前后尾部内容 |
| 14 | 已完成前述 Codex Web 验收；准备独立旧 Codex session | 切回旧 Codex 并发消息 | 旧入口仍能正常创建/发送/完成，回滚通道有效 | NOT RUN | 必须使用独立测试 session |

## 回报模板

```text
行号：
结果：PASS / FAIL / BLOCKED
执行时间：
网络与宿主：LAN / Relay；iPhone / Terminal / Desktop / VS Code
session 前 8 位：
实际看到：
与“应看到”的差异：
证据：截图/录屏路径；Bridge 日志时间窗；错误原文
```

## 完成条件

- 14 行均有 owner 结果；任何 `FAIL` 必须先完成根因分析和回归，再重跑受影响行。
- `BLOCKED` 必须写出具体缺失前提，不能折算为 PASS 或 N/A。
- Phase 6 A/B 观察只在本矩阵全部通过后开始。
