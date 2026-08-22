# Codex Web 与 Mac Codex Desktop 双向接力缺口：现状、根因与解决路线

- 日期：2026-08-22
- 状态：问题已由 owner 真机复现；根因已定位；尚无已证明可行的 Desktop 共享运行时入口
- 适用仓库：`cordcode-macbridge-codex-web`
- 上游源码：`/Users/jacklee/Projects/codex`
- 当前官方二进制：`codex-cli 0.149.0-alpha.4`
- 当前结论：**现有 `codex-web` 只在 CordCode 自己或已接入同一 daemon 的 Terminal TUI 范围内成立；它没有实现 Mac Codex Desktop ↔ iOS 的完整双向实时接力。**

## 1. 为什么这是阻断问题

原设计 §1 对 CordCode 的定位不是“在 iPhone 上另开一个 Codex runtime”，而是：

> CordCode 是用户 Mac 上官方 Codex 工作流的 iOS 延伸。

其中不可删减的产品约束包括：

1. 零迁移：继续使用同一工作区、账号、配置和 session；
2. 官方真相：Thread、Turn、Item、模型和权限来自官方 runtime；
3. 双向接力：iOS 创建的 session 能被 Mac 官方客户端继续，Mac 创建的 session 也能在 iOS 继续；
4. 不锁定：停用 CordCode 后，同一批 session 仍可在官方客户端使用；
5. 不自建 runtime/store/model proxy；
6. 缺能力或 ownership 冲突时失败可见，不伪造同步。

本次 owner 真机结果直接击中第 3 条：

- Mac Codex App 创建并完成一次 turn 的 session，iOS 可以读取历史；
- iOS 随后发送消息时，`thread/resume` 返回 `-32600 already has an active writer`；
- Mac Codex App 不退出时，iOS 无法继续该 session；
- Mac Codex App 继续发送时，iOS 不进入执行态、不接收 delta，切走再切回后只能通过冷读看到最终历史；
- iOS 创建的 session 已进入官方 SQLite/store，但 Mac App 不接收另一个 app-server 的 `thread/started`，无法可靠实时出现在列表。

这不是“错误提示是否正确”的问题。错误翻译正确只能证明 fail-closed；从产品验收看，原设计 §13.3 #6/#7/#8 和 §15 #2/#4 尚未通过。

## 2. 当前可复现现象

### 2.1 Mac → iOS

1. 在 Mac Codex App 的工作区创建 session B；
2. 在 B 中发送消息并等待回复完成；
3. iOS Codex Web 能列出并读取 B；
4. Mac 在 B 中继续发送时，iOS 不显示执行态和实时 delta；
5. iOS 切换 session 后再回来，只能看到已落库的最终结果；
6. iOS 向 B 发送消息，收到：

```text
thread/resume
-32600: thread <id> already has an active writer
transport: managed-loopback-ws
```

关键事实：**turn 完成不等于 writer 释放。** Desktop app-server 仍持有 loaded thread；官方卸载延迟常量为 30 分钟，而且 Desktop 保持订阅时未必进入卸载计时。

### 2.2 iOS → Mac

1. iOS 在指定工作区创建 session；
2. iOS 内发送和连续流式正常；
3. session 已存在于官方 `~/.codex/state_5.sqlite`，cwd/source 与 Mac session 一致；
4. Mac Codex App 的独立 app-server 没有收到创建通知，Mac UI 不能可靠实时发现它；
5. 即使通过重启/刷新最终发现，Mac 再尝试写入时也可能遇到相反方向的 writer ownership 冲突。

## 3. 活体进程拓扑

2026-08-22 实测进程：

```text
Mac Codex Desktop
  ChatGPT/Codex UI
    └─ codex -c features.code_mode_host=true app-server --analytics-default-enabled
         transport: stdio
         runtime: Desktop 独占 Embedded app-server

CordCode Link
  ├─ cordcode-bridge-runtime
  └─ codex app-server --listen ws://127.0.0.1:<managed-port>
       transport: loopback WebSocket
       runtime: CordCode 管理的另一个官方 app-server
```

两者共享 `CODEX_HOME`/SQLite/rollout 文件，所以冷态 `thread/list`、`thread/read` 能看到相同持久化事实；但它们不共享：

- 内存中的 loaded thread；
- writer ownership；
- connection subscription；
- `thread/started`、`turn/started`、`item/*`、`turn/completed` 实时通知；
- pending approval/requestUserInput server request registry。

因此“共享 store”不等于“共享 runtime”。当前实现是两个官方 runtime 读取同一持久化目录，不是 dsh-web/opencode-web 的单一服务实例模型。

## 4. 官方源码约束

### 4.1 writer 是跨进程互斥事实

官方实现：

```text
/Users/jacklee/Projects/codex/codex-rs/thread-store/src/local/writer_lock.rs
```

同一 thread 被另一个 app-server 持有时，新的 writer 返回：

```text
thread <id> already has an active writer
```

CordCode 不应删除锁文件、抢锁、杀掉 Desktop 或改写 SQLite。这些做法既违反官方 ownership，也违反设计的“失败可见/不自建真相”。

### 4.2 多客户端订阅只在同一个 app-server 内成立

官方同进程多 connection 机制位于：

```text
/Users/jacklee/Projects/codex/codex-rs/app-server/src/thread_state.rs
```

`try_ensure_connection_subscribed` / `try_add_connection_to_thread` 可以把多个 connection 加到同一个 app-server 内的 thread。这正是正确架构需要的能力，但它不能跨两个 app-server 进程工作。

### 4.3 thread 完成后不会立即卸载

官方实现：

```text
/Users/jacklee/Projects/codex/codex-rs/app-server/src/request_processors/thread_lifecycle.rs
THREAD_UNLOADING_DELAY = 30 * 60 seconds
```

因此“等 Mac 回复完成后由 iOS resume”不是可靠接力协议；“要求用户退出 Mac App”也不满足 CordCode 的无感双向接力初衷。

### 4.4 Terminal 支持共享目标，不等于 Desktop 支持

官方 TUI 有明确的：

```rust
enum AppServerTarget {
    Embedded,
    LocalDaemon,
    Remote,
}
```

并且 CLI/TUI 支持 `--remote` 或默认探测 local daemon。对应源码：

```text
/Users/jacklee/Projects/codex/codex-rs/tui/src/lib.rs
/Users/jacklee/Projects/codex/codex-rs/cli/src/main.rs
```

但当前 Mac Codex Desktop 的实际启动命令固定为 stdio app-server，没有观察到 `--remote`、daemon socket 或可复用监听端点。不能把 Terminal 的 LocalDaemon PASS 推导为 Desktop PASS。

## 5. 此前实施与设计的第一处分歧

### 5.1 Phase 0 裁决扩大了 PASS 的含义

Phase 0 证明的是：

- daemon 已运行时，默认 Terminal TUI 可以选择 LocalDaemon；
- CordCode 作为第二 connection 可以收到同 runtime 的真实增量。

但裁决同时声称 Desktop 虽为独立 stdio runtime，仍有“store 级 list/read/续聊接力”。现在的真机证据表明：

- list/read 成立；
- Desktop 正常打开时的续聊不成立；
- 所以该句把“同一 thread 可读”误写成了“同一 thread 可接力”。

按原设计 §8.2，PARTIAL 的前提仍是“双向串行接力成立”；当前 Desktop 路径连这个前提也没有满足。

### 5.2 managed-loopback-ws 被当成了产品主路径

设计 §6.1 把 managed loopback WS 定义为目标版本没有/不能复用 daemon 时的兼容托管服务，并明确要求不能冒充共享 daemon。

当前 CordCode 确实正确标记了 `managed-loopback-ws`，但产品实现继续把它用于 Mac Desktop session。结果是：

- iOS 自己创建/写入的 session 正常；
- Mac Desktop 自己创建/写入的 session 属于另一 runtime；
- catalog 冷读看起来“同步”，写入和实时事件却分裂。

这是架构能力缺口，不是补一个 handler、重连订阅或 UI 状态映射能够修复的问题。

### 5.3 验收顺序错误

原设计要求 Phase 0 先证明宿主覆盖面，并在 §13.3 做双向真机验收。实际开发在 Desktop #6/#7/#8 未通过时继续实现了完整产品面，导致大量功能在 CordCode 自有 session 上看似正确，但没有验证最初的跨客户端目标。

## 6. 与 dsh-web/opencode-web 的关键差异

### dsh-web

- 官方 DSH Web 服务是 session/event 的唯一服务实例；
- Mac 与 CordCode 都连接该实例；
- agent-level mux pump 接收同一服务产生的事件；
- 不存在两个服务进程争同一个本地 rollout writer 的情况。

### opencode-web

- Desktop 和 CordCode 连接同一个 HTTP server；
- `/global/event` SSE 是全局事实流；
- `session.created`、`session.status`、message delta、permission/question 都来自同一服务；
- CordCode 的全局 subscriber 与 Desktop 是同一服务的两个 client。

### 当前 codex-web

- CordCode 连接 managed WebSocket app-server；
- Desktop 连接自己 spawn 的 stdio app-server；
- 两个进程仅共享磁盘 store；
- 所以它没有复刻 dsh-web/opencode-web 最重要的前提：**单一官方服务实例**。

## 7. 解决方案原则

满足 CordCode 初衷的方案只有一个架构条件：

> **Mac Codex Desktop 与 CordCode 必须成为同一个官方 app-server 的两个 connection。**

一旦同 runtime：

- 两端 subscribe 同一 thread，不产生跨进程 writer 冲突；
- iOS 可以接收 Mac turn 的 started/delta/completed；
- Mac 可以接收 iOS 创建/更新带来的官方 catalog/lifecycle；
- approval/requestUserInput registry 处于同一 server；
- thread/list/read 与 live event 来自同一真相源。

任何仍保留“Desktop stdio app-server + CordCode managed app-server”双进程拓扑的修改，都不是根治。

## 8. 推荐解决路线

### 路线 A：找到并使用 Desktop 官方的共享 app-server attach 能力（唯一首选）

先做只读/隔离 spike，不继续修改产品 adapter：

1. 检查 Mac Codex Desktop 实际宿主代码、启动配置、环境变量和 feature flags，寻找官方支持的 app-server endpoint/daemon attach 入口；
2. 重点验证 Desktop 是否能像 TUI 一样选择：
   - local daemon Unix socket；
   - loopback WebSocket endpoint；
   -受支持的 remote app-server endpoint；
3. 若存在入口：
   - CordCode 只负责 ensure/start 一个官方 daemon；
   - Desktop 连接该 daemon；
   - CordCode 作为第二 connection 连接同一 daemon；
   - managed-loopback-ws 不再作为 Desktop 双向接力路径；
4. 以真实 Desktop UI 完成 §11 的验收矩阵，再恢复产品实施。

完成标准不是“两个进程能 list 同一 session”，而是进程检查必须显示 Desktop 不再拥有独立 Embedded writer，并且两连接可在同一 app-server 的 `thread/loaded/list`/订阅状态中观察到。

### 路线 B：评估新版官方 `remote-control`，但不得预设它等于本地共享服务

当前二进制暴露 experimental：

```text
codex remote-control
codex remote-control start
codex remote-control pair
```

官方源码表明它可以启动带 remote-control 的 daemon，并具有 pairing/client management。但目前没有证据证明：

- Mac Codex Desktop 会连接这个 daemon；
- CordCode 可以作为本地第二 app-server connection 使用它；
- 其配对协议允许 CordCode bridge-v1 直接接入；
- 它不会把内容交给额外的远程控制服务。

因此它只能作为独立安全/协议 spike。必须先抓真实 transport、认证、事件和 Desktop 行为，不能把名字“remote control”当作解决证明。

### 路线 C：若 Desktop 没有官方 attach 能力，诚实收缩产品范围

如果路线 A/B 均没有官方可用入口，那么在“不改官方源码、不注入 Desktop、不伪造事件”的约束下，Mac Desktop 双向实时接力当前不可实现。此时必须：

1. 将 Desktop 宿主 Gate 标为 FAIL，而不是 PASS/PARTIAL；
2. 不把 `codex-web` 宣称为 CordCode 的完整 Codex 替代；
3. 保留旧 backend/回滚入口；
4. `codex-web` 只标为 Terminal shared-daemon 或 CordCode-owned session 的实验能力；
5. 等待官方 Desktop 提供 remote/local daemon attach 后再恢复。

这不是理想结果，但比要求用户退出 App、自动抢锁或伪造实时同步更符合设计。

## 9. 明确拒绝的“修复”

以下方案不能进入正式实现：

- 删除或绕过官方 writer lock；
- 自动终止 Mac Codex Desktop/app-server；
- 修改官方 SQLite、rollout 或配置来转移 ownership；
- 轮询 rollout/SQLite 并包装为实时 delta；
- 用 `thread/read` 轮询伪造执行态；
- 在两个 app-server 间复制 session/turn；
- 要求用户每次在 Mac/iOS 切换前退出另一端；
- 将 managed-loopback-ws 描述成 Desktop 已共享的 daemon；
- 因为 ownership 错误翻译正确，就把双向接力验收标成通过。

## 10. 建议的执行顺序

### Gate 0：纠正状态

1. 将 Phase 0 Desktop “续聊接力可用”降级为被真机反证；
2. 将 Desktop 双向接力 regression 标为 failed；
3. 暂停以 Desktop 完整支持为前提的后续“完成/退役”结论；
4. 保留当前失败日志、进程树、writer lock 和 session ID 作为 artifacts。

### Gate 1：官方 Desktop attach spike

1. 启动一个官方 daemon；
2. 尝试所有有源码/宿主证据的 Desktop endpoint 配置入口；
3. 捕获 Desktop 子进程命令行、FD、socket、initialize 和 thread subscription；
4. 只接受“Desktop 与观察客户端在同一 app-server 内”的证据；
5. 若无入口，输出 FAIL，不写产品 fallback。

### Gate 2：共享服务实现

仅 Gate 1 PASS 后实施：

1. lifecycle 选择顺序改为官方共享 daemon 为产品必需条件；
2. Desktop 场景禁止静默降到 managed-loopback-ws；
3. CordCode 常驻 connection 接收全局 lifecycle 并按 thread subscription 接收 turn/item；
4. 创建、resume、审批、提问全部在同一 server request registry；
5. diagnostics 明确显示 Desktop connection 与 CordCode connection 的同 runtime 证据。

### Gate 3：自动化与真机

自动化必须覆盖：

- 同 app-server 两 connection resume 同 thread 不冲突；
- 两 connection 均收到后续 item delta/terminal；
- 创建事件触发 catalog；
- approval/requestUserInput 先答者与 resolved 清理；
- 断线后 read 冷校准不重复；
- 第二个独立 app-server 仍 fail-closed，用作隔离对照。

不运行高消耗 UI automation；最终功能证明必须由 owner 真机矩阵提供。

## 11. 必须通过的 owner 真机矩阵

以下每项都不能用“退出另一客户端”作为前置条件：

| # | 场景 | 通过标准 |
|---:|---|---|
| 1 | Mac Desktop 创建 session，完成回复后 iOS 发送 | 原 thread 直接继续，无 active writer 冲突 |
| 2 | iOS 创建 session | Mac Desktop 在有界刷新时间内看到同一 thread，无迁移/复制 |
| 3 | Mac Desktop 在已有 session 发长回复 | iOS 自动进入执行态并连续显示真实 delta |
| 4 | iOS 在同 session 发回复 | Mac Desktop 能观察同一官方 turn/history |
| 5 | Mac turn 触发 command/file approval | iOS 显示并可提交，官方 turn 正确继续/拒绝 |
| 6 | Mac turn 触发 requestUserInput | iOS 完整显示问题并一次提交官方 answer map |
| 7 | 两端先后续聊同一 session | 不复制 thread，不改变 cwd/provider/model |
| 8 | CordCode Link 重启/网络重连 | 同一 runtime 重连并冷校准，不抢另一个 writer |

## 12. 给接手 agent 的决策树

```text
Desktop 是否有官方方式连接现有 local/remote app-server？
  ├─ 是
  │   ├─ Desktop 与 CordCode 是否能在同一 server 订阅同一 thread？
  │   │   ├─ 是 → 实施单实例 lifecycle，并跑 §11
  │   │   └─ 否 → 保留证据，判 FAIL
  │   └─ 是否需要非官方注入/修改 Desktop？
  │       ├─ 是 → 拒绝该路线
  │       └─ 否 → 继续
  └─ 否
      ├─ remote-control 是否提供等价、受支持且安全的官方连接面？
      │   ├─ 已用真实样本证明 → 另案设计与安全评审
      │   └─ 未证明 → 不实施
      └─ Desktop Gate FAIL，codex-web 收缩为实验/Terminal shared-daemon 范围
```

## 13. 当前已完成但不解决根因的工作

以下功能在 CordCode 所连接的 app-server 内有效，应保留，但不能用来宣称 Desktop 接力完成：

- 官方 `thread/list/read/start/resume` 与 turn 流式翻译；
- model/list 与 reasoning effort；
- rename/archive/delete；
- ownership fail-closed 与 transport 来源诊断；
- iOS 连接替换后的 observation reattach；
- approval/requestUserInput adapter 的现有实现和测试。

其中 owner 已确认 rename/archive/delete 真机通过；权限审批仍未完成真机证明。

## 14. 权威证据索引

- 原设计：`docs/2026-08-21-codex-web-backend-design.md`
- Phase 0 裁决：`docs/2026-08-21-codex-web-phase0-gate-verdict.md`
- writer lock：`/Users/jacklee/Projects/codex/codex-rs/thread-store/src/local/writer_lock.rs`
- thread unload：`/Users/jacklee/Projects/codex/codex-rs/app-server/src/request_processors/thread_lifecycle.rs`
- 同 server 多 connection：`/Users/jacklee/Projects/codex/codex-rs/app-server/src/thread_state.rs`
- TUI AppServerTarget：`/Users/jacklee/Projects/codex/codex-rs/tui/src/lib.rs`
- CLI remote/daemon/remote-control：`/Users/jacklee/Projects/codex/codex-rs/cli/src/main.rs`
- remote-control 实现：`/Users/jacklee/Projects/codex/codex-rs/cli/src/remote_control_cmd.rs`
- dsh-web 架构：`agent/dsh-web/`
- opencode-web SSE：`agent/opencode-web/events.go`

## 15. 最终判断

当前 `codex-web` 的 adapter 代码并非全部无效；它对“CordCode 自己连接的官方 app-server”实现了大量正确能力。真正的问题是更靠前的运行时拓扑没有满足 CordCode 初衷，却继续把后续功能完成度当成产品完成度。

修复顺序必须倒回来：

> **先证明 Mac Codex Desktop 能与 CordCode 连接同一个官方 app-server，再继续谈实时事件、审批、模型和旧 backend 退役。**

在这个 Gate 通过以前，任何局部 adapter 修补都不能解决 owner 当前报告的 Desktop ↔ iOS 双向接力失败。
