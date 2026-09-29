# DEPRECATED

此 backend（legacy `deepseek`，SDK stdio 路线）已退役（2026-08-17）：产品 lineup
不挂载。2026-09-29 起源码移入 `deprecated/dsh` 且不再被 go-bridge 生产代码
import（驱动不注册）。**回滚 = git revert 迁移提交**；把 `"deepseek"` 加回
`RuntimeManager.swift` drivers 列表已无效（驱动未注册，解析失败仅记日志跳过）。
现役 DeepSeek 面由 `dsh-web`（`agent/dsh-web`）承接。
详见 `deprecated/README.md`、CLAUDE.md「Backend runtime model」legacy 清单与
GO_BRIDGE_ARCHITECTURE.md 对应节。
