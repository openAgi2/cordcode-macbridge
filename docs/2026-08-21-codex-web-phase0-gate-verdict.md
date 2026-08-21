# codex-web Phase 0 裁决：PASS

- 日期：2026-08-22（本地）
- 依据：设计 §8.2/§12/§14 Phase 0；证据全部来自官方二进制 `codex-cli 0.149.0-alpha.4`
  （`/Applications/ChatGPT.app/Contents/Resources/codex`，sha256 前 20 `10afbeddd6f951635d8f`）
  与 pinned source `536f86e5cc9ec1ff38457d099bf320b9d08eeeba`
- 证据索引：`scripts/codex-web-phase0/`（schemas/、dumps/、各 validate_*.py + run 输出）

## 1. 判定

**PASS** —— 核心共享运行时 Gate 成立：

1. **daemon 已运行 + 默认启动配置 Terminal TUI**（唯一"必须 PASS"路径）：TUI 选择 LocalDaemon
   （权威证据 `thread/loaded/list` 显示 TUI thread 驻留 daemon 进程）；观察连接（WS-over-UDS 第二
   连接）在**零文件访问**下实时收到同 thread 的 `item/started`、17–40 条 `item/agentMessage/delta`
   （多轮运行含 40/40 全量）、`item/completed`、唯一终态 `turn/completed`、`thread/status/changed`
   与官方用量通知。scene2/3（Embedded 两条隔离对照）符合预期：无伪 live 重放、不串流、
   list/read 边界可用。→ 满足 PASS 第一句。
2. **Desktop/VS Code 实际覆盖范围已分别报告**（不从 TUI 类推）：Desktop（ChatGPT.app）为独立
   stdio app-server 子进程（`-c` 覆盖 → Embedded 语义，无监听端口），live 旁观不可用、store 级
   list/read/续聊接力可用；VS Code 扩展未安装，如实 blocked（附 4 步 owner 矩阵）。→ 满足 PASS
   第二句（报告内容即"独立 runtime / 未安装"）。
3. 历史与续聊身份一致性：`thread/read(includeTurns)` 冷基线、跨连接 resume、断线后 read 冷校准
   均以官方 thread/turn/item identity 工作；写者冲突 `-32600` 语义完整（resume/archive/delete 三者）。

**按计划继续 Phase 1。** 外部 turn 实时旁观能力对 **Terminal TUI（daemon 形态）** 可广告；
对 **Desktop（独立 stdio runtime）** 不可广告实时旁观（store 接力可用）；**VS Code** 待安装后
按 owner 矩阵补测。旧 `agent/codex` 退役门槛不受影响（§15 另有完整门槛，本裁决只是必要条件之一）。

## 2. 版本漂移记录（§3.2）

- 设计时二进制 `0.148.0-alpha.21` → 实施时 `0.149.0-alpha.4`。schema（692 文件）与 pinned source
  typed shape 逐项吻合（27 断言），未触发 §21.2-2；方法面扩展（stable 95 / experimental 150）不推翻
  任何 §7 ✅ 行。`agent/codex-web` 的 contract 输入以本 bundle 为准。

## 3. 与设计断言的分歧（按 §3.0 以样本为准，已回写设计）

| # | 分歧 | 样本/源码裁决 | 回写落点 |
|---|---|---|---|
| 1 | §7/§7.3 称 command approval `availableDecisions`"未开 experimentalApi 时被 server 剥除" | schema stable bundle 确实剥除该字段；但 **wire 上未声明 experimentalApi 的连接也物理收到** `availableDecisions=["accept",{acceptWithExecpolicyAmendment}, "cancel"]`（server 出站 strip 只剥 `additionalPermissions`，transport.rs:189 + item.rs strip 实现 TODO） | §7 行、§7.3 行 |
| 2 | §6.1 "Go 直接连接 Unix domain socket 并讲 app-server JSON-RPC" | control socket 是 **WebSocket over UDS**（unix_socket.rs `accept_async`；裸 newline JSON 连接被立即关闭）；官方 `app-server proxy` 是纯字节中继，客户端仍需自行讲 WS；每 JSON-RPC 消息一个 WS text 帧 | §6.1 |
| 3 | §7.3 `config/read` 行的开放问题（additional 是否含 `model_providers`） | 0.149.0-alpha.4 实测：自定义 provider 配置下 `additional = {}`，**不含** `model_providers` | §7.3 |
| 4 | §8.2 未明 turn 级通知的订阅边界 | `thread/started`、`thread/status/changed` 全局广播（订阅前可达）；`turn/started`、`item/*`、`turn/completed` 仅发订阅连接且**不重放**；mid-turn attach 得到后续 delta + 唯一终态，`turn/started` 错过（源码证实发给全部订阅连接，纯时序边界） | §7.1/§8.2 补充 |

## 4. Phase 0 新事实清单（Phase 1–4 直接消费）

1. `daemon start` 前置：`$CODEX_HOME/packages/standalone/current/codex`（installer 管理副本；
   本实验以符号链接种子）+ control socket 路径 < macOS `SUN_LEN`(104)。
2. 同 daemon 多连接 `thread/resume` **无** writer 冲突（订阅模型）；`-32600 "already has an
   active writer"` 仅跨进程写者。resume 是订阅路径（thread/start/resume/revert 自动 attach）。
3. `thread/resume{excludeTurns:true}` 需 `experimentalApi` 能力（真实 -32600 门控样本）。
4. 挂起 server request 断线后**不向新连接重放**；新连接以 `thread/read` 冷校准。
5. 审批 decision 枚举 `accept`/`cancel`/结构化 `acceptWithExecpolicyAmendment`；`cancel` →
   turn 终态 **`interrupted`**（非 failed）。
6. `turn/interrupt` 必填 `turnId`；turn/start 响应先于 active-turn 注册（同毫秒 interrupt 报
   `no active turn to interrupt`）；interrupt 成功 → 终态 `interrupted`。
7. steer：`expectedTurnId` 用 turn/start 响应的 turn.id 成功（返回 `{turnId}`）；stale →
   `no active turn to steer`。
8. `requestUserInput`：default 模式自动转发不询问；`thread/start.config`
   `features.default_mode_request_user_input=true` 后 1 题 server request 物理到达（isBlocking=false、
   按 `{qid:{answers:[..]}}` 应答、`serverRequest/resolved` 收口）；多题 blocking 需 experimental
   collaborationMode（Phase 4 门控后另采）。
9. `model/list`：custom provider 未实现 `/v1/models` 时回落内置目录（5 项）+ `warning`
   （Model metadata not found）；typed Model 无 provider。
10. TUI 交互（PTY 驱动）：需 winsize/终端能力应答/信任对话框（路径含 `/private` 前缀差异）/
    消息与回车分帧（粘贴启发式）。
11. `item/plan/delta` 在 stable bundle（pinned source 注册仅 doc 注释 EXPERIMENTAL、无 `#[experimental]`
    属性）——消费仍按 🧪 取样门控，机制事实记录在案。

## 5. 未采集 / 遗留（不阻塞 PASS）

- 多题 blocking requestUserInput、MCP elicitation 三 variant 实样本：Phase 4 版本门控后另采
  （schema + 官方 v2 test 已核对）。
- VS Code 宿主：未安装；owner 矩阵 4 步（gate-hosts/README.md）。
- `thread/turns/list`（experimental 分页）：一期不用（§7 🧪 行维持）。
- turn/started 的 mid-turn attach 窗口：codex-web Go 实现用常驻连接 + 通知驱动 attach 消除；
  Phase 3 真实实现时复核（本裁决 3-4 条已给出推导依据：status/changed(active)+item/started）。

## 6. 可复跑验证汇总

| 套件 | 断言数 | 结果 |
|---|---:|---|
| `validate_schemas.py`（schema×pin×§7.3） | 27 | ALL PASS |
| `validate_samples.py`（§12 九组 + wire 断言） | 20 | ALL PASS |
| `validate_gate.py`（TUI 三场景 + 宿主） | 24 | ALL PASS |
| `validate_callsite_index.py`（file:line 锚点） | 65 | ALL PASS |

## 7. 后续

按 exec-plan 队列进入 Phase 1（空目录 `agent/codex-web` 骨架 + 官方服务生命周期 + RPC 客户端）。
Phase 0 全部产物已按 docs/证据规则 commit（见 git log `phase0(codex-web)` 系列）。
