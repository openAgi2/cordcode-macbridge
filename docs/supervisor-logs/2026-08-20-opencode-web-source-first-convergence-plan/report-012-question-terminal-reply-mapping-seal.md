# 开发报告 12：Question terminal reply-mapping 封口

- 开发报告入口：`docs/2026-08-20-opencode-web-audit012-repair完成情况.md`
- Mac commits：`01ca143`、`b79b273`
- iOS：本指令无改动
- 开发 agent 声明：terminal 后 late asked/stale recovery 不再恢复 reply mapping，两个 owning negatives、20 轮/race/full/sandbox 与 Mac Release 安装均完成；owner UI 矩阵未执行。

## 主要完成主张

- `gateAdmitRequested` 在任何 `pendingQuestions` 写入前判定 lifecycle。
- GET fallback 通过 `notePendingShape` 受同一 terminal lifecycle 封印。
- terminal 后二次 resolve 返回 `interaction_not_found` 且零新增官方 POST。

独立裁决见同号 audit 文件；本文件仅保存开发报告入口与主张。
