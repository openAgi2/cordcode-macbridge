# 开工指令：DSH「/」按钮命令面板（第一期）

发给：**实施 agent**。按 exec-plan 驱动方案做到完成或 blocked。  
**禁止**手写、手改、预创建 `.exec-plan/state/*.json`——队列 JSON 只允许 exec-plan skill 生成与更新。

---

## 0. 工作树（不许换）

```text
Mac 工作树=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
Mac 分支=plan/approval-layer
不要切到 /Users/jacklee/Projects/cordcode-macbridge 的 main 工作树
不要新建分支、不要 merge/rebase 进 main、不要 stash

iOS 工作树=/Users/jacklee/Projects/cordcode-ios-plan-approval
iOS 分支=plan/approval-layer-ios
不要用 /Users/jacklee/Projects/cordcode-ios 的 main（本项不在 main 上改）
```

开工第一件事：自己重新跑一遍路径+分支+完整 commit+未提交状态，写入来源清单。旧会话或方案 §0 的钉位不能代替本次复核。未提交的方案/评审 md 属于当前用户，保留，不要丢弃。

---

## 1. 唯一方案 + 必须用 exec-plan

方案（v1.3，复评无条件通过）：

`docs/2026-09-04-dsh-slash-command-panel-implementation.md`

配套只读：

- 调研终版 `docs/2026-09-04-slash-command-skill-cross-backend-survey.md` v1.2
- 评审 `docs/2026-09-05-dsh-slash-command-panel-implementation-review.md`（含 §8 复评）

**开工动作（按这个顺序，不要先改业务代码）：**

1. 读 exec-plan skill（`~/.grok/skills/exec-plan/SKILL.md` 或本环境等价路径）。
2. 在 Mac 工作树执行：

   ```text
   /exec-plan docs/2026-09-04-dsh-slash-command-panel-implementation.md start
   ```

3. 让 skill **自己**建 `.exec-plan/state/plan-<hash>.json` 并拆 `impl/tests/regression` 三元组。
4. 然后按 ready 队列做到完成或 blocked。

禁止：自己写一份 todos 当真相；禁止把 session todo 写回 JSON；禁止跳过 `-tests`/`-regression` 把 `-impl` 标完成。

---

## 2. 源码优先（方案 §0.1，不读不许写）

改任何 `agent/dsh-web` / `go-bridge` / iOS **之前**：

- 只读 `/Users/jacklee/Projects/deepseek-harness`
- 目标版本 **tag `dsh-v0.1.1-rc.2`（`b150a551b8`）** / 本机装机 0.1.1-rc.2；`master` 只对照
- 先读官方 `commands/list`、`commands/execute`、`parseCommand`、六条命令 handler、durable 事件
- CordCode 只做透传。禁止自造 list/execute 形状。`permission_mode.go` 是先例不是协议真值

实现说明必须带：官方仓路径 + tag + 符号/文件:行。

---

## 3. 范围锁（越界即停）

做：DSH host 命令面板；输入框 `/` 按钮（不要键入 `/`）；`session_commands` capability；`list_session_commands` / `execute_session_command`；点 `/plan` 走 `commands/execute`，不当 `send_message`。

**Phase 1 发布阻断：** codec Class ② 收入 `plan/mode`、`compaction/{start,prune,summary,end}`、`goal/change`、`feedback/record` + 单测（不 reset、不产 timeline）。

不做：技能、另四家、`CommandProvider`、参数 UI、`/plan off`、把 command 事件画进时间线。

D1–D5 按方案执行（permission 走现有菜单；export 成功 iPhone 静默；无参；无会话隐藏按钮）。

List 请求写死 `{args:{agentId}}`，响应官方裸数组，经**中间 wire 类型**映射到 `SessionCommand`（compact/export 无 `input`）。

---

## 4. 验证与纪律

- 成本：D3。定向 `go test ./agent/dsh-web ./go-bridge`；iOS 相关单测。不要全仓测试、不要 UI 自动化（未授权）。
- 改了 Go runtime：Release 覆盖安装 `/Applications`，先 `killall CordCodeLink` 再杀 `cordcode-bridge-runtime`，确认 8777 是新 PID。禁止跑临时 app。
- 真机矩阵（方案 §8）须 owner；agent 做日志/Management 核验。#3/#6 必须证明 **codec 不 reset**，不能只看 RPC 绿灯。
- 测试文件只用 `/tmp` 若需临时文件。
- 不要 stash、不要合 main、不要 push，除非 owner 另说。

---

## 5. 完成标准

exec-plan 队列里所有 **required** todo 有 proof 才算完成。不要手写「完成情况」充数；完成报告只允许 skill 在 proven-complete 时生成。

Blocked 时写清缺什么（官方源码、座位、owner 真机），不要用假成功或 fallback 压住。
