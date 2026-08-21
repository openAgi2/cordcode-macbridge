# 开发报告 11：Question terminal reconciliation

- 开发报告入口：`docs/2026-08-20-opencode-web-audit011-repair完成情况.md`
- Mac commits：`1506628`、`d6ba0b4`、`42702ed`
- iOS：本指令无改动，沿用指令 10 的验证与安装证据
- 开发 agent 声明：Question terminal history 对账、四组 barrier 交错、真实 resolve RPC 全链、真实 sandbox answered/rejected reopen 和 Mac Release 安装均完成；owner UI 矩阵未执行。

## 主要完成主张

- per-session/per-interaction lifecycle 取代 add-only bool claim，terminal 优先于陈旧 pending。
- GET `/question` 缺席不猜终态；本地 pending 缺席时从同一次 A7 history 事务读取 answered/rejected 证据。
- `resolve_user_input` owning tests 穿过具体 opencode-web responder、官方 POST fixture、server broadcast、EventPublisher/Kernel，并返回权威 headRev/status。
- A7 history 不含 resolved interaction 的 `que_…` ID；报告自行采用“fresh process 只保留 tool activity、不伪造 Dock”的边界。

独立裁决见同号 audit 文件；本文件仅保存开发报告入口与主张。
