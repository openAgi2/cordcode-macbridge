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
