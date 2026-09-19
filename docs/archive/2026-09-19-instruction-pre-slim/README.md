# 指令文件瘦身迁移前备份（2026-09-19）

本目录保存 CLAUDE.md / AGENTS.md 在 2026-09-19 瘦身迁移（叙述与 runbook 外置到 docs/
与根目录活文档，规则原文全部保留在指令文件）之前的逐字节备份。迁移时两文件内容相同
（byte-identical），两份备份亦相同。

- `claude-md-pre-slim-2026-09-19.md`：迁移前 CLAUDE.md（54,958 bytes / 777 行）
- `agents-md-pre-slim-2026-09-19.md`：迁移前 AGENTS.md（与上者逐字节相同）

仅供人工追溯迁移是否丢失规则；**agent 不要阅读或加载本目录内容**，现行规则一律以仓库根
AGENTS.md（CLAUDE.md 为其逐字节同步拷贝）为准。备份文件名刻意不使用 `CLAUDE.md` /
`AGENTS.md`，避免被 agent 工具按文件名递归拾取为指令。
