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
