# DSH Gate 0 证据 + 真实事件 dump

DSH driver 设计（`docs/2026-08-13-dsh-driver-design.md`）的全部协议证据：连通性、真实事件 dump、assembled composition（sandbox/approval）、环境切换验证。

## Pinned

- **DSH commit**：`47f943859bef60e4160492346772ded9b24f765a`
- node v25.9.0、go1.26.2 darwin/arm64

## 两种模式（由 `DEEPSEEK_API_KEY` 决定）

- **mock**（`key == dsh-conn-fake-key`）：本地 HTTP canned SSE turn，无真 key。
- **real**（真 key）：真实 DeepSeek API；`prompt = args[1]`；每个 `session.event` 完整 JSON 覆盖写到 `DSH_GATE0_DUMP`。

## 复现

```sh
cd /path/to/deepseek-harness
git checkout 47f943859bef60e4160492346772ded9b24f765a
pnpm install
echo "DEEPSEEK_API_KEY=sk-..." > .env

cd /path/to/cordcode-macbridge/scripts/dsh-gate0
set -a; source /path/to/deepseek-harness/.env; DSH_ROOT=/path/to/deepseek-harness; set +a; export DSH_ROOT
# mock
DEEPSEEK_API_KEY=dsh-conn-fake-key go run main.go
# real
DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "prompt"
# assembled + 环境切换
DSH_PERMISSION_MODE=workspace-write DSH_GATE0_CONFIG=/abs/path/driver-cordis.yml \
  DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "prompt"
```

## 本次证据（4 次真实 run，无路径 prompt 重新生成）

| Run | composition | env | distinct | 关键 |
|---|---|---|---|---|
| Gate0(mock) | jsonrpc-agent | — | 11 | 连通 |
| run1 | jsonrpc-agent | — | 14 | todo/write, tool/call, tool/result, reasoning-delta, chunk 7 discriminant |
| run2 | jsonrpc-agent | maxTokens=24 | 11 | turn/end(max-tokens), usage |
| run3 | driver-cordis.yml | **workspace-write** | 16 | §10 可加载；preset=workspace-write, policy=ask |
| run4 | driver-cordis.yml | **danger-full-access** | 16 | **环境切换 verified**：preset=danger-full-access, policy=**never** |
| union | — | — | 17 | runtime-dump verified |

**fail-closed**：`bash-local`(unconfined) + `permission-presets` → 拒绝加载。
**环境切换**：main.go 转发 `DSH_PERMISSION_MODE` 后，run3↔run4 preset/mode/policy 真实切换。
**计数**：裸 mock=11，union=17。

## 净化 + 一致性断言（round3 P0-2）

`sanitize.py`：
1. **递归 scrub** 所有 JSON string 值（不只逐行）：`/private/var/folders/.../dsh-conn-cwd-NNN`→`<CWD>`、`/tmp/dsh-*.txt`→`<TMPFILE>`
2. **净化后机器断言**：每 `(turn,step)` chunk text/reasoning 拼接 == assembled、chunk usage == assembled usage、无路径残留

```sh
python3 sanitize.py
# → assert: chunk==assembled, usage double-source equal, no host path — ALL PASS
```

> dumps 用无路径 prompt 重新生成，路径不在 token delta 中（连续字符串 scrub 即足够，不破坏 chunk↔assembled 等价）。

## main.go（v5 修复，round3 P1-1）

- ✅ 转发 `DSH_PERMISSION_MODE` / `DSH_SYSTEM_PROMPT` 到子进程
- ✅ dump `O_TRUNC` 覆盖写（不再 append）
- ✅ `sendAndWait` 先 `regPending` 再写 stdin（消除快速响应竞态）
- ✅ JSON-RPC error response 捕获 + 影响 VERDICT（PARTIAL 不静默成功）

> 仍非完整 CI gate：malformed stdout 断言、dispatcher 显式 join 属 driver 测试基建，实现时补。

## 文件

- `main.go` —— 验证脚本（mock/real 双模式，仅 Go 标准库）
- `driver-cordis.yml` —— §10 driver assembled composition（sandbox/approval 栈，verified 可加载）
- `sanitize.py` —— 递归净化 + 一致性断言
- `dumps/*.jsonl` —— 4 次真实 run 的 `session.event`（已净化 + 断言通过）
- `dumps/*.stdout` —— 调试输出（**gitignored**）
- `run-output.txt` —— Gate 0 mock 输出
