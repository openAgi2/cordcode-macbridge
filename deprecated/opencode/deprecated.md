# DEPRECATED

此 backend 已退役（owner 2026-08-19 裁决）：产品 lineup 不挂载。2026-09-29 起
源码移入 `deprecated/opencode` 且不再被 go-bridge 生产代码 import（驱动不注册）。
**回滚 = git revert 迁移提交**；把 `"opencode"` 加回 `RuntimeManager.swift`
drivers 列表已无效（驱动未注册，解析失败仅记日志跳过）。现役 OpenCode 面由
`opencode-web`（`agent/opencode-web`）承接。
详见 `deprecated/README.md`、CLAUDE.md「Backend runtime model」legacy 清单与
GO_BRIDGE_ARCHITECTURE.md 对应节。
