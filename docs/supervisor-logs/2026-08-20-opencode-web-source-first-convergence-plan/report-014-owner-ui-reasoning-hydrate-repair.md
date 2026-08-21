# 开发报告 14 号：Owner UI reasoning hydrate 修复

> **接收时间**：2026-08-21T04:58:21Z  
> **实现提交**：`be6a1d7`  
> **报告提交**：`eba2a94`  
> **开发侧完整报告**：`docs/2026-08-20-opencode-web-audit014-repair完成情况.md`

开发 agent 声明：E2b 红灯在旧实现同时复现 adapter `errUnsupportedReasoning` 与 full-path `projection.hydrate_failed`；修复后 populated HTTP history reasoning 映射为 first-class part，保持 part 顺序且不污染 answer text，`step-start/step-finish/patch` 显式跳过，direct-SSE reasoning 仍 unsupported/unadvertised。

其自测包括定向/full/vet/build/canonical checker、真实 owner 数据只读 preflight 与 Mac Release 重装。报告如实披露 broad `-race` 正则命中 Claude file-relay 测试夹具的既有 race，并称在基线可复现；owner UI 未自行执行。

本文件只是报告接收记录，不构成监工验证。独立结论见 `audit-014-owner-ui-reasoning-hydrate-repair.md`。
