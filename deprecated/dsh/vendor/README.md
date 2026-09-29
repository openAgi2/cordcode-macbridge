# Vendored DeepSeek Harness SDK layer (rc.6)

这些包是 `@deepseek-ai/dsh`（用户 `npm i -g` 的 DeepSeek Harness CLI）依赖闭包之外
的 SDK stdio 层，用户全局树里没有它们，因此由 MacBridge 随应用分发（MIT）。

- 来源：`npm pack @deepseek-ai/<pkg>@0.1.0-rc.6`（2026-08-15 拉取，未做任何修改）
  - `dsh-sdk-jsonrpc-demo`：启动胶水（bin/runner/invariant），spawn 入口 `lib/bin.js`
  - `dsh-sdk-jsonrpc-server`：stdio JSON-RPC server 插件（driver 协议面）
  - `dsh-sdk-protocol`：SDK 协议类型与行传输（server 的运行时依赖）
  - `dsh-agent-spine-demo`：agent spine 组合（driver-cordis.yml 的 agent core 条目）
- 运行方式：driver 在自己的数据目录构建「影子 node_modules」——上述 4 包为真实文件，
  其余 `@deepseek-ai/*` 家族包（cordis/dsh-agent/dsh-llm/dsh-session/…，含
  schemastery）symlink 到探测到的用户全局 `@deepseek-ai/dsh` 树；Node 默认按
  realpath 解析，家族依赖全部回到用户已安装的版本。MacBridge 不安装、不下载、
  不写用户全局目录。
- 版本兼容：rc.6 的 server/demo 胶水针对同代 app-boot 的 boot() 签名；用户 dsh
  升级导致签名漂移时 spawn 会失败——按 fail-closed 呈现（见 discovery 注释）。
