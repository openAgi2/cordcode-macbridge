# P1 真机离线 Topic 回归

- 状态：`FAIL`（前置投递范围缺陷，尚未进入离线 Topic 折叠断言）
- 测试设备：已连接的 iPhone 16 Pro（真机）
- MacBridge 安装产物：`cordcode-bridge-runtime 0.1.0 (commit: d3031bc593af, built: 2026-09-12T04:30:45Z)`
- 前置条件：iPhone 主屏幕 Web App 已安装且通知权限开启；使用一个测试会话；不要用普通锁屏代替真正断网。

| # | 前提条件（网络、模式、session 状态） | 动作 | 应看到 |
| --- | --- | --- | --- |
| 1 | iPhone Wi-Fi 与蜂窝网络均关闭，进入真正离线状态；主屏幕 Web App 通知已开启；测试会话尚无本轮待发完成通知 | 保持真离线 10 分钟；期间在同一个 `(backendId, sessionId)` 连续完成至少 3 个回合；然后恢复网络并等待推送交付 | 该会话只收到 Push Service 尚未交付消息中的最新一条；通知文案/深链对应最后一个完成回合；不得出现 2 条或更多该会话的离线积压通知 |

## Owner 结果

- 结果：`FAIL`
- 实际观察：iOS Web App 从未打开过的 Claude/Codex session，在 Mac 端完成回合后不产生通知；一旦 Web App 打开过该 session，后续 Mac 端完成回合可以收到通知。
- 对照：Claude session A 打开后可收到 A；未打开的 session B 不通知，打开 B 后可收到 B，随后切回 A 的 Mac 端完成仍可收到 A。
- 判定：推送候选错误依赖 session observation/projection 初始化，导致未打开 session 的 completion 被过滤。原离线折叠矩阵尚不能成立。
- 观察时间：2026-09-12（owner 自述）

## 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 638399047205, built: 2026-09-12T05:48:41Z)`
- 状态：`FAIL`（投递已到达，但 replay-free payload 缺少 `target.eventId`，iOS SW 显示 schema error）

| # | 前提条件 | 动作 | 应看到 |
| --- | --- | --- | --- |
| R1 | Web App 保持默认 backend，不打开任何 session；选一个从未在 Web App 打开过的 Claude session | 在 Mac 端完成一个新回合，等待最多 10 秒 | 收到该 Claude session 的完成通知；Web App 当前 backend/session 不影响投递 |
| R2 | 仍不打开目标 session；选一个从未在 Web App 打开过的 Codex Desktop session | 在 Mac 端完成一个新回合，等待最多 10 秒 | 收到该 Codex session 的完成通知 |
| R3 | R1/R2 完成后再打开对应 session | 观察通知栏 | 不补发修复版本启动前或 enrollment 前的历史 completion |
| R4 | R1/R2 通过后，iPhone 真正断网 10 分钟 | 同一 session 连续完成至少 3 个回合，然后恢复网络 | 只收到该 session 尚未交付消息中的最新一条 |

### R1 修复前失败记录

- Owner 观察：Mac 端在从未由 iOS Web App 打开过的 session E 完成回合后，iOS 收到通知，但标题/正文为「CordCode 推送数据错误 / 打开 CordCode 查看连接状态」。
- 本地 delivery ledger 证据：修复前最新 replay-free 两次投递记录的 `eventId` 均为空字符串；SW `isValidTargetShape` 要求 `eventId` 非空，因此必然落入固定 schema-error 通知。
- 判定：投递覆盖修复有效，payload 契约缺陷真实；不能用客户端放宽校验掩盖。

### R1 EventID 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 8bdbaa805f8c, built: 2026-09-12T06:04:11Z)`
- 状态：等待 owner 复测
- 复测动作：Web App 不打开目标 session，在 Mac 端完成一个新回合，等待最多 10 秒。
- 预期：显示真实的 `CordCode · <会话标题>` 完成通知与回复预览；不得再显示「CordCode 推送数据错误」。
- 已修复的服务端不变量：同一 replay-free turn 的 `target.eventId` 稳定为 `wplive-<hash[:32]>`；缺失 EventID 的 candidate 在服务端出站前被拒绝。

### R1 EventID 后续失败记录

- Owner 观察：session E 一个回合收到两条通知，一条为具体消息内容，另一条为「Mac 上的会话已完成，点击查看结果」。
- Ledger/transcript 取证：同一 Claude turn 可先写一条无正文 `end_turn`，再写一条带正文 `end_turn`；watcher 对两条立即入队，ledger 只能阻止后续重试，不能撤销已在队列中的本次双发。
- 判定：watcher 缺少 per-turn terminal 挂起/去重。

### R2 历史回放失败记录

- Owner 观察：session F 是 Mac 端当天第一次打开、已有数条历史消息的 session；发送消息 2 后，除新消息通知外还收到多条历史 completion 通知。
- Ledger/transcript 取证：14:08 左右连续出站 7 条 `wplive-*`，逐条对应 session F 中既有历史 user turn；该 session 是 watcher 启动后才首次出现在 catalog 的旧文件。
- 判定：watcher 把“首次可见”误判为“启动后新文件”并从 byte 0 消费。首次可见必须建立 baseline，不得回放历史。

### R1/R2 去重与 baseline 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 2b15e870cd7c, built: 2026-09-12T06:23:52Z)`
- 状态：等待 owner 复测
- 复测前先清除 iOS 通知中心中此前已送达的历史通知；这些通知已经交付，服务端修复不会 retroactively 撤回。

| # | 前提条件 | 动作 | 应看到 |
| --- | --- | --- | --- |
| D1 | session E/F 均未在 iOS Web App 打开；session F 保留多轮历史；通知栏已清空 | 在 Mac 端 session E 发送一个新回合 | 只收到 1 条通知；正文为新回复内容，不额外出现 generic「Mac 上的会话已完成」 |
| D2 | 通知栏继续清空或仅保留 D1 | 在 Mac 端 session F 发送一个新回合 | 只收到 message 2 对应的 1 条通知；session F 的历史 turn 全部不推送 |

### D1/D2 Owner 结果

- Claude：`PASS`。多个 Mac 端消息均正常通知，无 schema error、异常通知或多余通知。
- Codex：`FAIL`。iOS Web App 保持 Claude 默认模式且不打开/切换 session 时，Mac 端 Codex session 完成后没有通知。
- 本地取证：Codex Remote catalog fingerprint 在消息后变化，但 passive observer 只看到 `__remote_control_transport__` error 帧，没有 thread-level completion。协议证据要求 `thread/resume(excludeTurns:true)` attach 后才能收到 turn/item 事件；此前的 `SubscribeLive` 只接入中央 pump，未建立 thread 订阅。

### Codex live-attach 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: a0cbbf5174e2, built: 2026-09-12T06:44:02Z)`
- 状态：等待 owner 复测
- 部署证据：runtime ready；Codex Remote passive subscription 成功；日志记录 `codex-remote attached live catalog threads loaded=2 attached=2`。
- 复测动作：iOS Web App 保持默认 Claude 模式且不打开/切换任何 session；Mac 端打开/使用一个 Codex Desktop session 并发送一个新回合，等待最多 10 秒。
- 预期：iOS 收到该 Codex session 的 completion 通知；无历史回放，Claude 已通过的通知行为不受影响。

### Codex live-attach Owner 结果与预览缺陷

- Codex delivery：`PASS`。iOS 保持 Claude 默认模式且未打开/切换 session 时，Mac 端 Codex session 完成后能收到通知。
- Codex preview：`FAIL`。通知正文为固定文案「Mac 上的会话已完成，点击查看结果」，没有 Claude 路径那样的真实回复详情。
- 本地取证：passive log 在同一 Codex session 上收到多帧 `text_delta`，随后 completion 到达；旧 replay-free producer 只在 completion 帧取 `EventResult.Content`，而 Codex 官方 `turn/completed` 终态帧本身不携带正文。
- 判定：必须按 `(backendId,sessionId,turnId)` 累积同一 live source 的 assistant text delta，并在 completion 时消费；不得历史回读或伪造正文。

### Codex replay-preview 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 2bf41b2de382, built: 2026-09-12T06:54:58Z)`
- 状态：等待 owner 复测
- 部署证据：runtime ready；Codex Remote passive subscription 成功；`codex-remote attached live catalog threads loaded=2 attached=2`。
- 复测动作：iOS 保持 Claude 默认模式且不打开/切换 session；Mac 端 Codex session 发送一个会产生文本回复的新回合，等待最多 10 秒。
- 预期：通知正文显示该回合的真实回复预览；只有确实没有 live 文本的回合才允许固定文案。

### Codex replay-preview Owner 结果与 DSH 失败

- Codex preview：`PASS`。iOS 通知正文显示真实回复详情。
- DSH：`FAIL`。Mac 端 3080 Web 里完成 DeepSeek Harness session 后，iOS 未收到通知。
- 本地取证：dsh-web passive stream 已收到同一未打开 session 的 `turn_started`、多帧 `text_delta` 和 `turn_completed`；但 dsh-web 只实现普通 `EventSubscriber`，未提供 replay-free live seam，completion 仍被 observation/kernel gate 挡掉。
- 协议事实：DSH mux stream 覆盖所有 external session 但无 cursor；不能把原始流直接标成 replay-free，必须按 observed turn lifecycle 过滤并消费一次。

### DSH replay-free 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 36406dc34a99, built: 2026-09-12T07:07:26Z)`
- 状态：等待 owner 复测
- 部署证据：runtime ready；`dsh-web` external instance `http://127.0.0.1:3080` resolved；mux/host streams opened；dsh-web passive subscription started。
- 复测动作：iOS 保持 Claude 默认模式且不打开/切换 session；在 Mac 端 3080 Web 的 DeepSeek Harness session 发送一个新回合，等待最多 10 秒。
- 预期：iOS 收到 DSH completion 通知，正文为真实回复预览；无历史回放，Claude/Codex 已通过行为不受影响。

### DSH Owner 结果与 Grok 失败

- DSH：`PASS`。iOS 收到 dsh-web completion 通知。
- Grok Build：`FAIL`。Mac 端发送 Grok Build session 消息后，iOS 未收到通知。
- 本地取证：Grok Build 没有 service-level passive subscriber；默认 inline 模式无 leader socket，外部 turn 依赖 session-scoped `SubscribeSessionEvents`/updates tailer，未打开 session 不会启动。
- 可用真值源：Grok 在 inline 与 leader 模式都追加 per-session `updates.jsonl`，且官方 session/update codec 已过滤 replay 标记。全局只读 journal watcher 可提供 replay-free live 观察而不驱动 Grok。

### Grok replay-free 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 0722548559a4, built: 2026-09-12T08:27:21Z)`
- 状态：等待 owner 复测
- 部署证据：runtime ready；`backend=grokbuild` passive subscription started；启动时对一个 in-progress Grok turn 立即收到 live `text_delta`，说明 journal watcher 已生效并保留未完成 turn 尾部。
- 复测动作：iOS 保持 Claude 默认模式且不打开/切换 session；在 Mac 端 Grok Build session 发送一个新回合，等待最多 10 秒。
- 预期：iOS 收到 Grok completion 通知，正文为真实回复预览；无历史回放，Claude/Codex/DSH 已通过行为不受影响。

### Grok Owner 结果与 OpenCode Web 失败

- Grok Build：`PASS`。iOS 收到 Grok Build completion 通知。
- OpenCode Web：`FAIL`。Mac 端 OpenCode Web session 完成后，iOS 未收到通知。
- 本地取证：opencode-web 全局 SSE 已收到同一未打开 session 的 `turn_started`、多帧 `text_delta` 和 `turn_completed`；但该 backend 只实现普通 `EventSubscriber`，completion 仍被 observation/kernel gate 挡掉。
- 协议事实：OpenCode `/global/event` 无 cursor；不能把原始流直接标成 replay-free，必须按 observed turn lifecycle 过滤并消费一次。

### OpenCode Web replay-free 修复版本复测

- 安装版本：`cordcode-bridge-runtime 0.1.0 (commit: 69722268d0d6, built: 2026-09-12T08:40:58Z)`
- 状态：等待 owner 复测
- 部署证据：runtime ready；opencode-web generation 1.18 `/global/event` SSE subscriber connected；opencode-web passive subscription started。
- 复测动作：iOS 保持 Claude 默认模式且不打开/切换 session；在 Mac 端 OpenCode Web session 发送一个新回合，等待最多 10 秒。
- 预期：iOS 收到 OpenCode Web completion 通知，正文为真实回复预览；无历史回放，Claude/Codex/DSH/Grok 已通过行为不受影响。

### OpenCode Web Owner 结果与标题模板需求

- OpenCode Web：`PASS`。iOS 收到 OpenCode Web completion 通知。
- Owner 后续文案需求：completion title 改为 backend 显示名 + `任务已完成`，固定枚举：`Claude code`、`Codex`、`Grok build`、`Deepseek Harness`、`Opencode`；body 继续为具体回复内容。

### Backend title Owner 结果与来源标识判定

- Backend title：`PASS`。Owner 确认通知已显示具体 backend。
- 剩余视觉项：系统通知仍显示 `from CordCode` 来源标识。
- 代码取证：MacBridge payload title 只生成 `<backend> 任务已完成`；SW 只传 `title/body/tag/data`；manifest 与页面没有 `from CordCode` 字符串。
- 平台判定：`from CordCode` 是 iOS/WebKit 为 Home Screen Web App 推送自动标注的来源应用名，W3C Web Notification API 没有隐藏该标识的选项。把 manifest 名字改成空白只会破坏安装体验或替换成域名，不是删除来源标识；彻底移除需要改走原生 App APNs 通知通道。
