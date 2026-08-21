# Owner 真机验收与监工收口（2026-08-21）

## 结论

owner 在 iPhone + MacBridge 的真实 OpenCode Web 路径上完成集中验收，并明确判断“测试结果基本符合预期，可以收尾”。据此，三条完成轨道均已闭合：exec-plan 75/75 proven、supervisor audit-014 verified、owner 真机产品验收 accepted。OpenCode Web source-first convergence 监工任务正式退出，不再下发后续 directive。

## Owner 验收矩阵

| # | 场景 | Owner 结果 |
|---|---|---|
| 1 | 新 session 首轮普通消息与流式终态 | ✅ |
| 2 | 同 session 第二轮、无重复、模型与 agent 语义保持 | ✅ |
| 3 | 多轮历史重进，完整有序、无闪烁/重复/永久 pending | ✅ |
| 4 | 模型、agent、variant 选择与无效 variant 清除 | ✅ |
| 5 | provider error 的 retry/error、非假成功与运行态退出 | owner 未给该行单独勾选；由“基本符合预期，可以收尾”的整体产品判断覆盖，不伪造为逐项显式 ✅ |
| 6 | 流式 Abort 后双端退出运行态 | ✅ |
| 7 | 官方 Web 外部 turn 在 iPhone 单次流式并持久化 | ✅ |
| 8 | permission 允许一次/始终允许/拒绝及 Dock 收敛 | ✅ |
| 9 | question 本机回答、Web 回答/拒绝及终态不回 pending | ✅ |
| 10 | Todo Dock 顺序、内容与状态同步 | ✅ |
| 11 | rename/archive/delete 及列表/by-ID 语义 | ✅ |
| 12 | 流式中断网恢复，无重复且无永久 running/pending | ✅ |

## 保留边界

- direct-SSE live reasoning 仍缺同版本真实样本，因此继续保持不实现、不广告；本次验收不把它静默升级为 supported。
- 广域 race 命令中的 Claude 测试 fixture 全局状态竞争已在 directive-014 审计中证明为基线既有问题，不属于 OpenCode Web 收口阻塞项。
- 本记录只关闭既定 OpenCode Web convergence 工作；不会覆盖或吸收其他 agent 当前未提交的独立改动。
