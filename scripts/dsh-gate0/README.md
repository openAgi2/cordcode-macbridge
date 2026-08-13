# DSH Gate 0 证据 + 真实事件 dump

DSH driver 设计（`docs/2026-08-13-dsh-driver-design.md`）的全部协议证据：连通性、真实事件 dump、assembled composition、环境切换、source.kind 双 shape。

## Pinned

- **DSH commit**：`47f943859bef60e4160492346772ded9b24f765a`
- node v25.9.0、go1.26.2 darwin/arm64

## 两种模式（由 `DEEPSEEK_API_KEY` 决定）

- **mock**（`key == dsh-conn-fake-key`）：本地 HTTP canned SSE turn，无真 key。
- **real**（真 key）：真实 DeepSeek API；`prompt = args[1]`；每个 `session.event` 覆盖写到 `DSH_GATE0_DUMP`。

## 复现

```sh
cd /path/to/deepseek-harness
git checkout 47f943859bef60e4160492346772ded9b24f765a
pnpm install
echo "DEEPSEEK_API_KEY=sk-..." > .env

cd /path/to/cordcode-macbridge/scripts/dsh-gate0
set -a; source /path/to/deepseek-harness/.env; DSH_ROOT=/path/to/deepseek-harness; set +a; export DSH_ROOT
DEEPSEEK_API_KEY=dsh-conn-fake-key go run main.go                      # mock
DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "prompt"                 # real
DSH_PERMISSION_MODE=danger-full-access DSH_GATE0_CONFIG=/abs/driver-cordis.yml \
  DSH_GATE0_DUMP=dumps/runX.jsonl go run main.go "prompt"               # assembled + env
```

## 本次证据（4 次真实 run）

| Run | composition | env | distinct | 关键 |
|---|---|---|---|---|
| Gate0(mock) | jsonrpc-agent | — | 11 | 连通 |
| run1 | jsonrpc-agent | — | 14 | todo **pending+completed**, tool/call, tool/result, reasoning-delta |
| run2 | jsonrpc-agent | maxTokens=24 | 11 | turn/end(max-tokens), usage |
| run3 | driver-cordis.yml | workspace-write | 16 | §10 可加载；preset/mode/policy；**user+plugin user/message 双 shape** |
| run4 | driver-cordis.yml | danger-full-access | 16 | 环境切换；user+plugin 双 shape |
| union | — | — | 17 | runtime-dump verified |

**user/message 双 shape**（round4 P0-1）：assembled composition 产生 `source.kind=user`（真实 prompt）+ `source.kind=plugin`（权限运行时上下文），同 role+UUID，须按 source.kind 分流（plugin 不进 user timeline）。
**fail-closed**：`bash-local`(unconfined)+`permission-presets` → 拒绝加载。
**环境切换**：run3↔run4 preset/mode/policy 真实切换。
**计数**：裸 mock=11，union=17。

## 净化 + peer 一致性断言（round4 P1）

`sanitize.py`：
1. **递归 scrub** 所有 JSON string 值（`/private/var/folders/.../dsh-conn-cwd-NNN`→`<CWD>`、`/tmp/dsh-*.txt`→`<TMPFILE>`）
2. **peer-existence + equality 断言**：每 `(turn,step)` 有 text/reasoning delta **必须**有 assembled peer（缺 peer 即失败，不再空通过）；chunk usage **必须**有 assembled usage peer；内容相等；无路径残留

```sh
python3 sanitize.py
# → assert: chunk==assembled, usage peer exists+equal, no host path — ALL PASS
```

## main.go（v6 修复，round4 P1）

- ✅ 转发 `DSH_PERMISSION_MODE` / `DSH_SYSTEM_PROMPT`
- ✅ dump `O_TRUNC` 覆盖
- ✅ `sendAndWait` 先注册后写（消除竞态）
- ✅ JSON-RPC error response → 返回 nil（调用 fatal），**不打印 OK**
- ✅ **PARTIAL → `os.Exit(1)`**（不再静默成功）

> 仍非完整 CI gate：malformed stdout 断言、dispatcher 显式 join 属 driver 测试基建。

## 文件

- `main.go` —— 验证脚本（mock/real 双模式，仅 Go 标准库）
- `driver-cordis.yml` —— §10 driver assembled composition（verified 可加载）
- `sanitize.py` —— 递归净化 + peer 一致性断言
- `dumps/*.jsonl` —— 4 次真实 run（已净化 + peer 断言通过）
- `dumps/*.stdout` —— 调试输出（**gitignored**）
- `run-output.txt` —— Gate 0 mock 输出
