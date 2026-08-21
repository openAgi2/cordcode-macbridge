# Phase 0 宿主实测：Codex Desktop（ChatGPT.app）与 VS Code extension

- 日期：2026-08-22（本地）；只读取证（ps/lsof/文件存在性），未启动/停止/写任何用户真实 Codex 状态
- 判定对照：设计 §8.2 第 4/5 行（"记录其实际 app-server endpoint/进程归属…Phase 0 实测，不从 TUI 推断"）

## Codex Desktop（ChatGPT.app）——已实测

**实际形态：独立 stdio app-server 子进程（Embedded 语义），不使用共享 daemon。**

证据（`process-evidence.txt` 原文）：

1. ChatGPT.app（PID 44842）运行中；其 Codex UI 是内嵌 Electron 子框架
   （`Contents/Frameworks/Codex Framework.framework/...` 的 Renderer/Service/crashpad 进程）。
2. **app-server 进程**：PID 44958，父进程 = ChatGPT 主进程，命令行
   `/Applications/ChatGPT.app/Contents/Resources/codex -c features.code_mode_host=true app-server --analytics-default-enabled`：
   - 与 codex-web Phase 0 目标二进制**同一路径同一版本**（0.149.0-alpha.4）；
   - 带 `-c` config 覆盖 → 按 pinned source `can_reuse_implicit_local_daemon`（tui/src/lib.rs:912-925）
     该进程**不能隐式复用 daemon**，为独立 Embedded runtime（与 TUI 场景 3 同构，实测佐证）；
   - `lsof -p 44958`：fd 0/1/2 均为 **unix pipe 连回父进程**（stdio transport），无 TCP/UDS 监听、
     无 control socket 连接 → endpoint = 父进程私有 stdio，第三方客户端不可连接。
3. 用户真实 `~/.codex`：**无** `app-server-control/`、**无** `packages/standalone/` → 本机不存在
   用户 daemon（Desktop 也没在用 daemon）。
4. 另见：PID 2244 `codex app-server` 的父进程是 `cordcode-bridge-runtime`（CordCode 产品旧 codex
   backend 的每-session stdio app-server，非 daemon）。
5. store 共享面：`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` 存在（仅存在性检查）——Desktop
   session 与 CLI/TUI 共享同一持久化 store → **list/read 级接力可用**（现有产品行为亦印证）。

**覆盖面结论（Desktop）**：
- 共享 daemon live 旁观：**不可用**（无 daemon，Desktop runtime 隔离）；
- store 级 list/read/续聊接力：可用（同 CODEX_HOME）；
- 与 Terminal TUI 场景 1 的共享 daemon 路径**互不影响**：TUI 场景 1 的结论对 Desktop 不成立、
  也无需成立——两宿主形态不同，分别记录，不从 TUI 类推（本节即实测）。

## VS Code Codex extension——未安装；**owner 2026-08-22 裁决：不补测**

覆盖面结论固定为「未安装/不测」，codex-web 不对 VS Code 宿主广告实时旁观；下方矩阵仅存档备查。
（原状态：如实记录为 blocked）

- 本机已装 Visual Studio Code.app，但 `~/.vscode/extensions/` 无 openai/chatgpt/codex 相关扩展。
- 未安装 → 无法实测 endpoint/transport/进程归属/事件覆盖；**不从 Terminal/Desktop 类推**。
- 最短 owner 验证矩阵（安装扩展后执行，每步只需观察进程）：

| # | 动作 | 应记录 |
|---:|---|---|
| 1 | VS Code 安装 Codex 扩展并登录 | `ps aux | grep -i codex` 新增进程的完整命令行与父进程 |
| 2 | 扩展内发起一个会话消息 | 该进程 `lsof -p <pid>` 的 fd 形态（pipe=stdio / TCP / UDS） |
| 3 | （可选）`ls ~/.codex/app-server-control/` | 扩展是否使用 daemon（control socket 出现即 daemon 形态） |
| 4 | 回报以上三项输出 | 由 agent 判定覆盖面并回写本节 |

## 对 §8.2 判定的影响

Terminal 场景 1（daemon 已运行 + 默认 TUI）为唯一"必须 PASS"的核心共享运行时路径，已实测成立。
Desktop 为独立 runtime（store 级接力），VS Code 未安装（blocked，不虚报）——两者计入 PASS 的
"Desktop/VS Code 分别报告实际覆盖范围"要求：报告内容即"独立 runtime / 未安装"。
