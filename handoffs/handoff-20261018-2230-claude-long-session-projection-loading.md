# 交接：Claude 长 session 投影打开优化（方案 v30 实施中段）

> **2026-10-19 更新**：S-2 已全部完成（freeze/impl/tests/regression 4 项，14/23 done）。
> bbolt 物化存储已部署正式 runtime（41523b1f，14:12 起）；realdata 证据：事故
> transcript 101MB 上种子保存 128ms / 单 turn 差分 **36ms**（旧路径每次全量重写
> 34MB JSON）。剩余队列全部为 iOS 门控（s7-tests iOS spec 运行 / s7-regression
> 真机 / S-4 / S-3 / S-6）——owner 指示 timeline 测试期间不动 iOS App 端，暂停等
> 放行。提交链新增：41523b1f（S-2 实现）→ edab8660（realdata+收口）。


日期：2026-10-18 22:30
分支：Mac `feat/claude-long-session-projection-loading` @ 455d49e4；iOS 同名分支 @ 89f0a369
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` v30（R25 APPROVED；正文 SHA-256 `88dede7e…` 已机械校验）
执行队列：`.exec-plan/state/plan-b70583acde4f.json`（23 todos；**勿动** iOS 仓 `plan-b4b554001ea8.json`——并行任务）

## 恢复命令

```
exec-plan 恢复：直接以 exec-plan skill sync 该 plan 路径即可（状态持久化在 JSON）
```

## 已完成（10/23 done + 1 in_progress）

| 切片 | 状态 | 关键证据 |
| --- | --- | --- |
| S-0 取证（含 E-3） | ✅ | `docs/2026-10-18-s0-runtime-identity-evidence.md`；E-3 根因=旧 iOS 安装包无 S4C 声明（残留：owner USB 连一次或新包装机后闭环） |
| S-5 解码单次化 | ✅ | 33.6MiB 帧 decode 4.34×；iOS 测试 16/16；`docs/2026-10-18-phase1-s5-s8-implementation-evidence.md` |
| S-8 direct_gzip_v1 | ✅ | 协议冻结+双端实现+Mac 4/4+iOS 4/4+Release 部署核验 |
| S-7 冻结批次 | ✅ | bridge-v1.md §Anchor-Newer（FROZEN）+ 3 新类型 + 独立样本（sha256 `191cc871…`）+ Mac spec 3/3 + iOS spec 就绪（**未运行**，见调度约束） |
| S-7 实现 | ✅ | 切片/打包/对账/协商/projectionMaxBytes/fence scalar/held/trace/turnTokens 全落地；Mac 行为测试 18 例 PASS；Release 部署核验（PID 43301，commit 455d49e47231） |
| S-7 测试 | 🔶 in_progress | Mac 侧完成；**iOS spec 运行后置**（见调度约束）；⑫trace 计数/⑬held 负对照/⑦full.oversized handler 级归 s7-regression 补强 |
| S-7 regression | 🔶 部分 | Mac 部署核验完成；E-4 wire/E-10 真机部分待新 iOS 包 |

## owner 调度约束（2026-10-18 指示，已登记 plan JSON）

另一 agent 做 timeline 优化可能真机测试 CordCode iOS——**本任务所有 iOS 真机构建/安装/测试推到最后**（S-6 收口或 owner 明确窗口）；iOS 模拟器测试用不同设备（如 iPhone 18 Pro Max）或错峰批量；iOS 文件层工作不受限。Mac 侧照常。

## 环境要点

- Go 构建：沙箱 `GOSUMDB=off` 阻断 toolchain 校验——用 `/tmp/gobin/go` wrapper（`GOTOOLCHAIN=local` + 缓存 1.26.6）或直接 `~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.6.darwin-arm64/bin/go`；Release 脚本传 `PATH=/tmp/gobin:$PATH`。
- iOS：`OpenCodeiOS/` 下 `xcodegen generate` 后 build；新文件必须先 generate。模拟器 iPhone 17 Pro Max（UDID E156A3E3…）曾被并行任务压坏态——重启后恢复。
- 显示层有 secret 脱敏（`{{SECRET_…}}`），源码本体不受影响。

## 已知问题（非本任务引入）

- `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 预存失败（system-subtypes.jsonl scanner/manifest mismatch；native-timeline worktree 基线同样红）——待另行处置。
- 正式 iPhone 安装的 iOS app 为旧构建（E-3 根因）；新版 iOS 包装机后 hello 将声明 anchor/scalar/gzip → 运行态 echo 与对账链路首次可用。

## 下一步（按队列依赖）

1. `s7-anchor-newer-tests` 收口：iOS spec 批量跑（错峰/异设备）+ ⑦/⑫/⑬ 补强。
2. `s7-anchor-newer-regression`：E-4 wire + E-10 + echo 实测（新 iOS 包窗口）。
3. `s2-storage-freeze` → `s2-materialization-impl/tests/regression`（纯 Mac，bbolt，OD-9 按默认档 (a)）。
4. `s4-ios-base-cache-*`（打开状态机/采用事务/恢复门/older gate/截断提示；iOS spec 批量在此跑）。
5. `s3-window-v2-*`、`s6-owner-matrix-prep`（材料；真机矩阵由 owner 执行）。

## 提交记录（本阶段）

Mac：8aca6662（S-0）、0bae9bea（S-8）、380c6939（S-7 冻结）、455d49e4（S-7 实现）
iOS：e5be7197（S-5+S-8）、819b5a86（xcodegen）、c238dc7b（S-7 mirror+spec）、89f0a369（S-7 wire）
运行时：/Applications 已部署 455d49e47231（22:21 起，epoch 8cb0e6a6…）
