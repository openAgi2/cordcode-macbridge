# codex-web topology monitor implementation plan 完成情况

- 完成日期：2026-08-25
- 分支：`codex/codex-web-backend`
- 结论：**DONE**。实现、测试、Release UI、失败可见、owner 门与默认 on 均已收口；S2/S5 按计划保留 `blocked_manual_owner_close` 诚实记录。

## 实现与 review-fix

- Phase 0/1 的 provider、聚合状态机、Darwin collector、strict DTO、Management API、Mac decode/store/UI 与临时 observer 清理均已完成。
- `1ae92f6`：修复 live `bridgeEpoch` 越界 trap，以 Int64 wire bit-pattern 无损承载 Management v1 UInt64 identity。
- `b2a3e0d`：按 Desktop 151 真实现场识别 stdio Unix socketpair（父链 + FD 0/1/2 IPC），不再只认 `PIPE`。
- `5d28346`：main attachment 以已建立 endpoint client 为准，不把空闲时 `pumpClient == nil` 误报为未附着。
- `702c5d3`：修正两个依赖宿主状态的旧 Mac 测试 fixture，并同步更新 collector fake。
- `480a39b`：owner 门通过后独立切换 topology monitor 默认 on；`CODEX_TOPOLOGY_MONITOR=0` 仍可显式关闭。

## 自动化证据

- `go vet ./go-bridge/...`：通过。
- `go test ./agent/codex-web -count=1`：通过；transport identity 定向 8/8 通过。
- `go test ./go-bridge/... -count=1`：通过，主包 65.010s，所有子包通过。
- Mac `xcodebuild ... test`：191 tests，1 skipped，0 failures，23.337s；结果包：`Test-CordCodeLink-2026.08.25_00-39-22-+0800.xcresult`。
- `git diff --check`：通过。

## Release UI 与 owner 门

- S1：owner Desktop PID 30190 为 `shared_only`，bridge=`shared`、aggregate=`all_shared`、syncHealth=`healthy`；UI 隐藏健康面板。
- S3：隔离 force-stdio Desktop 为 `private_only`，与 owner `shared_only` 聚合为 `mixed/degraded`；UI 显示“仅部分 Codex Desktop 实例处于同步状态”。
- 失败可见：隔离不可读 user-data fixture 产生 `private_stdio=unavailable`，aggregate/syncHealth=`unknown`；UI 显示“无法确定 Desktop 同步状态——诊断失败”，未误报 split。
- S2/S5：需要完全关闭当前承载本任务的 shared Desktop；依计划记 `blocked_manual_owner_close`，没有终止或忽略 owner PID。S4 只允许自然观察，本轮未出现 dual，也未注入伪造。
- 两次隔离 Desktop 与一次 failure fixture 均按精确 PID/launchd label 清理，数据目录移入废纸篓；owner PID 30190 全程保持。
- crash 基线为 00:12 的 epoch trap；安装 review-fix 后没有新增 CordCodeLink crash report。

## 边界

- 本计划没有启动 catalog/per-transport health 或 iOS `topology_status_v1`；二者仍需独立证据与计划。
- topology monitor 只读 control-plane，不写 timeline、不推进 projection revision、不改变 Session Sync v2 单 writer。
