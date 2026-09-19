# 指令文件瘦身迁移前备份（2026-09-19）

本目录保存 CLAUDE.md / AGENTS.md 在 2026-09-19 瘦身迁移（叙述与 runbook 外置到 docs/
与根目录活文档，规则原文全部保留在指令文件）之前的逐字节备份。迁移时两文件内容相同
（byte-identical），两份备份亦相同。

- `claude-md-pre-slim-2026-09-19.md`：迁移前 CLAUDE.md（54,958 bytes / 777 行）
- `agents-md-pre-slim-2026-09-19.md`：迁移前 AGENTS.md（与上者逐字节相同）

仅供人工追溯迁移是否丢失规则；**agent 不要阅读或加载本目录内容**，现行规则一律以仓库根
AGENTS.md（CLAUDE.md 为其逐字节同步拷贝）为准。备份文件名刻意不使用 `CLAUDE.md` /
`AGENTS.md`，避免被 agent 工具按文件名递归拾取为指令。

**2026-09-20 更新：owner 决策撤销瘦身，AGENTS.md / CLAUDE.md 已从本备份逐字节还原**
（还原前已用 `git show 92de269^:CLAUDE.md` 复核备份与迁移前提交状态一致）。本目录自此转为
纯历史存档。还原后正文与瘦身期间并入 GO_BRIDGE_ARCHITECTURE.md / RELAY_SERVER_OPERATIONS.md
的内容存在重复，**以指令文件正文为准**；瘦身去重时并入两份活文档的增量（RunDiagnostics
诊断规则、ssh 别名、codex 探针行等）独立有价值，保留不动。

