# Grok Build 命令面板 Phase 0 证据包 — 来源清单与漂移表

> 生成：2026-09-07（实施开工）。每项均为本次实测（`git rev-parse` / `git status --short` / `git diff --stat`），非旧会话继承。

## 0. 参与仓库来源清单（P0 强制清单）

| # | 角色 | 仓库路径 | 分支 | 完整提交 | 未提交状态 | 任务预期分支 |
| --- | --- | --- | --- | --- | --- | --- |
| A | Mac 功能工作树 | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `6b6994a2ab20e911673da9145e112fe642bd9a92` | 干净 | `plan/approval-layer`（方案 §0 指定） |
| B | iOS 功能工作树 | `/Users/jacklee/Projects/cordcode-ios-plan-approval` | `plan/approval-layer-ios` | `a579cccadfb4b875f96432d7db370b1ae82ba781` | 未跟踪 `?? message-web/public/`（用户/其他代理现有文件，不得动） | `plan/approval-layer-ios` |
| C | 上游 Grok Build 源码（只读） | `/Users/jacklee/Projects/grok-build` | `main` | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9` | 干净 | 上游 1.0.16 只读 |
| D | iOS 只读评审基线（本次不用于构建） | `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 | 仅评审参考 |

## 1. 配对复核（A ↔ B）

- A 的分支族 `plan/approval-layer` 与 B 的分支族 `plan/approval-layer-ios` 唯一对应（命名并行的功能分支族；本任务方案/执行指令均指向该功能分支族）。
- D（cordcode-ios main）与 B（plan/approval-layer-ios）差异实测：`git diff a579cca..c3b1d5b0 --stat` 仅 2 文件：
  - `CLAUDE.md`（+1 行）
  - `IOS_SIMULATOR_UI_VERIFICATION.md`（+120 行）
  即 main 是功能分支被 owner 2026-09-07 指令合回后的快照（merge commit `c3b1d5b0`，message: "merge plan/approval-layer-ios：dsh §13 面板 + message-web 滚动稳定性三刀 + 配对双因子修复（owner 2026-09-07 指令合回 main）"）。**两者功能源码同一内容，但 B 是功能工作树，构建/安装必须用 B，不得改用 D(main)。**
- B 的最近提交 `a579ccca`（滚动稳定性三刀，owner 真机验收 ✅）——与 2026-09-06 的 message-web 专项一致，与本次 Grok 面板无冲突面。

## 2. 目标二进制身份核验（P0 前要求）

实测命令：`ls -la ~/.grok/bin/grok`、`file`、`grok --version`、`shasum -a 256`。

| 项 | 实测值 |
| --- | --- |
| 路径 | `/Users/jacklee/.grok/bin/grok`（符号链接 → `../downloads/grok-1.0.13-macos-aarch64`） |
| 版本自报 | `grok 1.0.13 (5e9a58528b76)` |
| 文件类型 | Mach-O 64-bit executable arm64 |
| sha256 | `8669e0fdadceec25b8c159c355f427ffbd82583525d774b6ab1522197ea83b80` |

与方案 §0 预期（`~/.grok/bin/grok` 1.0.13、自报 `5e9a58528b76`）**一致**；上一轮没有运行它，本轮实测运行 `--version` 只读版本串（无模型、无网络副作用）。链接目标（downloads 下的真实文件）与符号链接本身均需在后续样本取证时复用同一路径。

## 3. 上游源码提交核验（C）

- 方案 §3 的所有行号锚点属于本提交 `72a61251fcffb464bcc687aeb5a998e5a98ec0c9`（1.0.16 checkout）。
- `grok-build` 当前在 `main`（此前会话记录为 detached；本次实测 `main` 同提交，已按元数据更新记录）。
- **版本漂移警告**：目标运行二进制为 1.0.13（`5e9a58528b76`），上游 checkout 为 1.0.16 源码。源码行为只能作为设计/实现参考；**1.0.13 的真实协议形状与 Pending 恢复行为必须以本机二进制实测样本为准**（P4/P7 决策依赖实测）。

## 4. 产品源码差异复核（A 内部）

方案 §0 声称"Mac 产品源码与 `de17e6f782ecc1d72a29f7d4e81e6aed2405251d` 无差异"。待复核（见 P0a-pairing 完成项）；`6b6994a` 是本方案 v1.5 修订提交（相对 `7ef00c3` 仅文档差异，评审基线）。

## 5. 约束备忘

- 跨仓修改授权仅覆盖 A+B；C 只读；D 只读。
- 不得 merge/rebase/cherry-pick/reset/switch B 或 A 到其它分支，不得动 B 的 `message-web/public/` 未跟踪文件。
- 构建/安装来源门通过前（A 用 plan/approval-layer @ 6b6994a 工作树、B 用 plan/approval-layer-ios @ a579cca 工作树）不安装。
- iPhone 真机安装必须等"配对与构建来源门通过且 iPhone 已连接"，且只做安装不做 UI 自动化。
