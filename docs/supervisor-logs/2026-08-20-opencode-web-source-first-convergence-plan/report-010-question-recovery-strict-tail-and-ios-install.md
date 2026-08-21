# 开发报告 10：Question recovery、strict tail 与 iOS 安装收口

- 开发报告入口：`docs/2026-08-20-opencode-web-audit010-repair完成情况.md`
- Mac commits：`4a2afb6`、`5b11ba3`、`44cae1a`
- iOS 产品代码：本指令无新增提交；沿用已审计的 `71007a4`
- 开发 agent 声明：Question live/cold correlation、strict tail、真实 sandbox、Mac Release 安装、iOS 定向测试与真机安装均完成；owner UI 矩阵未执行。

## 主要完成主张

- live `question.asked` 同时要求 `tool.messageID`、`tool.callID`，并用 subscriber 观测到的 assistant `parentID` 关联 owning turn。
- StartSession 与 SSE 重连通过 GET `/question` 加权威 history 恢复 pending question；GET/live 使用 claim 收敛。
- reply/reject/external resolution、冷重载、断线重连均有 full-path 覆盖。
- rename/archive/delete 精确限制 HTTP 200；agent/provider/todo 剩余 strict shape 已封死。
- `/agent.description` 依据真实 1.18.18 serve 与官方 optional schema，允许 hidden internal agent 缺席。
- iOS 两个定向类 11/11；paired iPhone 已完成 Debug 安装和启动，无 UI 自动化。

独立裁决见同号 audit 文件；本文件仅保存开发报告入口与主张。
