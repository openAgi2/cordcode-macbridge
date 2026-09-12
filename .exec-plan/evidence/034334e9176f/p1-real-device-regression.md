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
- 状态：等待 owner 复测

| # | 前提条件 | 动作 | 应看到 |
| --- | --- | --- | --- |
| R1 | Web App 保持默认 backend，不打开任何 session；选一个从未在 Web App 打开过的 Claude session | 在 Mac 端完成一个新回合，等待最多 10 秒 | 收到该 Claude session 的完成通知；Web App 当前 backend/session 不影响投递 |
| R2 | 仍不打开目标 session；选一个从未在 Web App 打开过的 Codex Desktop session | 在 Mac 端完成一个新回合，等待最多 10 秒 | 收到该 Codex session 的完成通知 |
| R3 | R1/R2 完成后再打开对应 session | 观察通知栏 | 不补发修复版本启动前或 enrollment 前的历史 completion |
| R4 | R1/R2 通过后，iPhone 真正断网 10 分钟 | 同一 session 连续完成至少 3 个回合，然后恢复网络 | 只收到该 session 尚未交付消息中的最新一条 |
