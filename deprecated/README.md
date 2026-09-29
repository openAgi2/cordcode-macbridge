# deprecated/ — 退役 backend 源码归档

本目录存放已从产品 lineup 退役、且**不再被 go-bridge 生产代码 import** 的 backend
源码。它们仍在 root module 内参与编译与 CI 测试（`go test ./...`），仅作历史归档
与回滚源。

| 目录 | 退役时间 | 现役承接者 |
| --- | --- | --- |
| `dsh/` | 2026-08-17 产品入口退役；2026-09-29 移入本目录 | `agent/dsh-web`（官方 `dsh web`） |
| `opencode/` | 2026-08-19 owner 裁决退役；2026-09-29 移入本目录 | `agent/opencode-web`（官方 `opencode serve`） |

## 禁令（防误用）

1. **生产代码不得 import 本目录任何包**——由
   `go-bridge/deprecated_import_guard_test.go` 在 CI 强制：非 `_test.go` 的
   生产文件 import `deprecated/` 即测试失败。
2. **不得在本目录新增功能或修复**：退役包只接受随目录迁移的机械改动；bug 修在
   现役承接者（`dsh-web` / `opencode-web`）。
3. 排障时不要把这里的实现当成现役语义参考；以 `agent/dsh-web`、
   `agent/opencode-web` 与 `GO_BRIDGE_ARCHITECTURE.md` 为准。

## 回滚

**回滚 = `git revert` 迁移提交**（恢复目录、import、注册与死分支）。
「drivers 列表加回 id」的旧回滚路径已失效：迁移同时删除了 blank import 与
`agentAliases` 别名，驱动未注册时 `-drivers` 传 `deepseek` / `opencode` 只会记
一条 `failed to create agent` 日志后跳过（fail-soft），不会挂载。

## 历史背景

- 迁移决策与影响面调查：`docs/2026-09-04-retired-backends-deprecated-migration-impact.md`
  （该文档同时覆盖仍在 `agent/` 的 `codex`、`codex-web`——二者尚未迁移，另行决策）。
- 各包自带 `deprecated.md` 记录各自的退役裁决。
