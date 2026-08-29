# Codex Desktop：Mac 上怎么测

安装本分支的 CordCode Link 之后：

1. 打开并登录 ChatGPT Desktop。
2. 打开 CordCode Link，在「AI 工具」里应看到 **Codex Desktop**，状态是未配置。
3. 点 **配对**。浏览器会弹出授权，完成后回到 CordCode Link。
4. 在 ChatGPT Desktop 打开「控制这台 Mac」，切到「电脑」，刷新配对码，填进配对窗口。不要把码发到聊天里。
5. 配对成功后，Codex Desktop 应变为就绪。
6. iPhone 切换到 **Codex Desktop**，打开任意 session，确认投影可加载。
7. 从 iPhone 发送消息，确认回复出现在 iPhone，并且同一回合同步到 Mac 的 Codex App。
8. 从 Mac 的 Codex App 发送消息，确认同一回合同步到 iPhone。

## 2026-08-29 Owner 真机验收

- 设备：owner 的已配对 iPhone + 当前安装的 CordCode Link `a35157d3bcb8`。
- iOS → Codex Desktop：发送成功，收到回复，消息与回复同步到 Mac Codex App。
- Codex Desktop → iOS：Mac 端发送成功，同步到 iOS 会话。
- 会话投影：此前“无法加载会话投影”故障经 stream epoch 修复后未复现。
- 证据级别：owner 手工真机反馈，`self-attested`；本轮未运行 UI test 或模拟器自动化。

已知缺口：cursor 断线续传与官方手机 Remote controller 共存仍保持 fail-closed；不影响本次 CordCode iPhone 双向消息竖切验收。
