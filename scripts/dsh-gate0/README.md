# DSH Gate 0 证据 + 真实事件 dump

DSH driver 设计（`docs/2026-08-13-dsh-driver-design.md`）的全部协议证据：连通性、真实事件 dump、assembled composition（sandbox/approval）验证。

## Pinned

- **DSH commit**：`47f943859bef60e4160492346772ded9b24f765a`
- node v25.9.0、go1.26.2 darwin/arm64

## 两种模式（由 `DEEPSEEK_API_KEY` 决定）

- **mock**（`key == dsh-conn-fake-key`）：本地 HTTP server 模拟 DeepSeek API，canned SSE turn，**无需真 key**（Gate 0 连通性）。
- **real**（真 key）：连真实 DeepSeek API，`prompt = args[1]`，把每个 `session.event` 完整 JSON append 到 `DSH_GATE0_DUMP`。

## 复现

```sh
# 1. DSH 源码 pin + install
cd /path/to/deepseek-harness
git checkout 47f943859bef60e4160492346772ded9b24f765a
pnpm install
echo "DEEPSEEK_API_KEY=sk-..." > .env   # real 模式需要；mock 模式不需要

# 2a. mock 模式（Gate 0，无 key）
cd /path/to/cordcode-macbridge/scripts/dsh-gate0
DSH_ROOT=/path/to/deepseek-harness DEEPSEEK_API_KEY=dsh-conn-fake-key go run main.go

# 2b. real 模式（真实事件 dump）
set -a; source /path/to/deepseek-harness/.env; set +a
DSH_ROOT=/path/to/deepseek-harness DSH_GATE0_DUMP=dumps/runX.jsonl \
  go run main.go "your prompt here"

# 2c. assembled composition（§10 sandbox/approval 栈）
DSH_ROOT=... DSH_PERMISSION_MODE=workspace-write \
  DSH_GATE0_CONFIG=/abs/path/to/driver-cordis.yml \
  DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "your prompt"
```

## 本次证据（4 次真实 run，见 `dumps/`）

| Run | composition | 捕获 distinct types | 关键 verified |
|---|---|---|---|
| run1 | jsonrpc-agent | 14 | `todo/write`, `tool/call`, `tool/result`(双层嵌套+isError), `reasoning-delta` chunk, chunk 7 种 discriminant |
| run2 | jsonrpc-agent（maxTokens=24） | 11 | `turn/end(max-tokens)`, `usage`(reasoningTokens, cacheReadTokens) |
| run3 | **driver-cordis.yml** | 16 | §10 可加载；`permission/preset`+`sandbox/mode`+`approval/policy` 激活 |
| run4 | driver-cordis.yml | 16 | bash 写临时区（sandbox 允许） |

**fail-closed 证据**：`bash-local`(unconfined) + `permission-presets` → runtime 拒绝加载（`does not confine ... misconfiguration`）。

**覆盖**：44 种事件中 17 种真实 verified（含权限栈 3 种）。

## 文件

- `main.go` —— 验证脚本（mock/real 双模式，仅 Go 标准库）
- `driver-cordis.yml` —— §10 driver 专用 assembled composition（sandbox/approval 栈，已 verified 可加载）
- `dumps/*.jsonl` —— 4 次真实 run 的 `session.event` 完整 JSON（每行一个 event params）
- `dumps/*.stdout` —— 调试输出（**gitignored**，含本机路径，不入库）
- `run-output.txt` —— Gate 0 mock 模式输出

> `dumps/*.jsonl` 已确认无 key、无本机绝对路径。`*.stdout` 因含本机路径被 `.gitignore` 排除。
