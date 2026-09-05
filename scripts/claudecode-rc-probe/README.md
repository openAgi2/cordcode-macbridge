# Claude Code Remote Control（RC / CCR）客户端接入 probe 证据包

对应报告：`docs/2026-09-05-claudecode-rc-client-probe.md`（调研指令
`docs/2026-09-05-claudecode-rc-research-directive.md`）。

组织方式参照 `scripts/claudecode-phase0/`。本目录只存放脱敏后的取证样本与
静态提取物；**不含任何 token / API key / 凭据值**（红线：凭据值不得出现在
任何产出物）。

## 来源锚（2026-09-05）

| 组件 | 版本 | 取证方式 |
| --- | --- | --- |
| PATH CLI | 2.1.234（npm global，commit 7215ba60b06d） | 本机运行 + `strings` 静态提取 |
| Desktop 内嵌 CLI | 2.1.260（`Claude-3p/claude-code/2.1.260/claude.app`；2.1.258 目录并存但活体进程为 2.1.260） | 活体进程 `ps` / `ps eww` |
| Claude Desktop | 1.46388.3（deploymentMode=3p） | 进程参数 |
| Agent SDK npm | `@anthropic-ai/claude-agent-sdk` 0.3.260（本机 `/Users/jacklee/Projects/claude-agent-sdk-npm/package/`） | `.d.ts` 类型面 |
| 官方文档 | code.claude.com/docs（remote-control.md / mobile.md / desktop.md / llms.txt，2026-09-05 抓取） | WebFetch |

## 样本清单

- `samples/a1-doctor-default.txt` — 默认环境下 `claude doctor` 的 Remote
  Control 面板（六项全红），测试目录 `/tmp/rc-probe/work`。
- `samples/a1-doctor-project-override.txt` — 同上，但测试目录放 project
  settings `.claude/settings.json` 尝试覆盖 `ANTHROPIC_BASE_URL` /
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`；面板不变 → project env 块
  压不过 user env 块。
- `samples/a1-doctor-cleanhome.txt` — `HOME=/tmp/rc-probe/home` 干净基线：
  base URL / feature-flag 两条阻塞消失，仅剩「未登录 claude.ai」。
- `samples/a1-rc-verbose.txt` — `claude remote-control --verbose
  --debug-file`（登录拒绝 + RC auth state 面板；面板只含 set/unset 状态，
  无凭据值）。
- `samples/a1-remote-control-debug-file.jsonl` — 同命令的 `--debug-file`
  原始 JSONL。
- `samples/a1-settings-scope-map.md` — 三层作用域事实表（变量名级别，
  无值）。
- `samples/a1-desktop-host-auth-env.md` — Desktop Code tab 活体子进程的
  host-auth env 机制 + cc-switch 本地代理链路取证（非敏感值）。
- `samples/b2-embedded-api-table.md` — CLI 2.1.234 二进制内嵌的 Sessions /
  Threads API Markdown 表（`strings` 提取）。
- `samples/b3-cli-static-endpoints.md` — CLI 2.1.234 二进制静态提取：RC
  端点族、`ccr-*` 标识符族、`remote_*` / `agent.*` 事件名、传输线索
  （`text/event-stream`、`wss://bridge.claudeusercontent.com`）。
- `samples/d1-credential-shapes.md` — 凭据存储/刷新静态形状
  （`.credentials.json` 字段族、`CLAUDE_CODE_HOST_CREDS_FILE` 安全校验）。

## 未做（阻塞于 A 组资格门，见报告）

- RC host 注册/轮询/SSE 的活体 debug-file 取证（B3 活体层）
- 浏览器远程端（claude.ai/code）真实操作取证（B1 活体层；无头抓取被
  Cloudflare challenge 挡，见报告 B2）
- C2「远程发消息 → Desktop 实时流式」实验
- C3 矩阵活体验证
- D1 无头进程凭据复用/并发刷新活体验证
