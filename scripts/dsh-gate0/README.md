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
cd /path/to/deepseek-harness
git checkout 47f943859bef60e4160492346772ded9b24f765a
pnpm install
echo "DEEPSEEK_API_KEY=sk-..." > .env   # real 模式需要

cd /path/to/cordcode-macbridge/scripts/dsh-gate0
# mock
DSH_ROOT=/path/to/deepseek-harness DEEPSEEK_API_KEY=dsh-conn-fake-key go run main.go
# real
set -a; source /path/to/deepseek-harness/.env; set +a
DSH_ROOT=... DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "prompt"
# assembled composition
DSH_ROOT=... DSH_PERMISSION_MODE=workspace-write DSH_GATE0_CONFIG=/abs/path/driver-cordis.yml \
  DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "prompt"
```

## 本次证据（4 次真实 run）

| Run | composition | distinct types | 关键 verified |
|---|---|---|---|
| Gate0(mock) | jsonrpc-agent | **11** | 协议连通（mock） |
| run1 | jsonrpc-agent | 14 | todo/write, tool/call, tool/result, reasoning-delta, chunk 7 discriminant |
| run2 | jsonrpc-agent(maxTokens=24) | 11 | turn/end(max-tokens), usage |
| run3 | **driver-cordis.yml** | 16 | §10 可加载；permission/sandbox/approval 激活 |
| run4 | driver-cordis.yml | 16 | bash 写临时区 |
| **union** | — | **17** | runtime-dump verified |

> 计数澄清：裸 Gate 0 mock = **11 类**（不是 14）；四次 run union = 17 类。

**fail-closed**：`bash-local`(unconfined) + `permission-presets` → runtime 拒绝加载（`does not confine ... misconfiguration`）。

## 净化（重要）

`dumps/*.jsonl` **经 `sanitize.py` 处理**：`/private/var/folders/.../dsh-conn-cwd-NNN`→`<CWD>`、`/var/folders/...`→`<CWD>`、`/tmp/dsh-*.txt`→`<TMPFILE>`，保留 JSON 字段与嵌套形状。净化后广 grep 验证**无本机绝对路径、无 API key**。

```sh
python3 sanitize.py   # 幂等；处理 dumps/*.jsonl 并验证无残留
```

> 已知限制（round2 审计）：helper 现状足以产出 dump，但尚非严格 CI gate —— `send()`/`waitResp()` 有快速响应竞态、`VERDICT:PARTIAL` 仍退出码 0、append 写可能拼多次 session、malformed stdout 静默跳过、未转发 `DSH_PERMISSION_MODE`。这些在 driver 实现的测试基建中修复。

## 文件

- `main.go` —— 验证脚本（mock/real 双模式，仅 Go 标准库）
- `driver-cordis.yml` —— §10 driver assembled composition（sandbox/approval 栈，已 verified 可加载）
- `sanitize.py` —— dump 净化器（host 路径→占位符）
- `dumps/*.jsonl` —— 4 次真实 run 的 `session.event`（已净化）
- `dumps/*.stdout` —— 调试输出（**gitignored**，含本机路径）
- `run-output.txt` —— Gate 0 mock 输出
