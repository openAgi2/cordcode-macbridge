# DSH Gate 0 连通性验证证据

证明 Go 进程能 spawn DSH JSON-RPC agent runtime、驱动 `initialize` + `session/prompt`、消费完整 `session.event` 流 —— DSH driver 设计（`docs/2026-08-13-dsh-driver-design.md`）的地基证据。

## Pinned

- **DSH commit**：`47f943859bef60e4160492346772ded9b24f765a`（`/path/to/deepseek-harness` checkout 到此）
- **方法学**：复刻 DSH 自身 `examples/jsonrpc-agent/tests/keyless-smoke.e2e.ts` —— 本地 HTTP server 模拟 DeepSeek chat-completions API（canned SSE turn），`DEEPSEEK_BASE_URL` 指向它，**无需真实 `DEEPSEEK_API_KEY`**。
- **环境（本轮）**：node v25.9.0、go1.26.2 darwin/arm64、macOS。

## 复现

```sh
# 1. DSH 源码 checkout 并 pin 到上述 commit，pnpm install（提供 tsx + 全 workspace）
cd /path/to/deepseek-harness
git checkout 47f943859bef60e4160492346772ded9b24f765a
pnpm install

# 2. 跑 Gate 0（脚本用 DSH_ROOT 定位 DSH checkout）
cd /path/to/cordcode-macbridge/scripts/dsh-gate0
DSH_ROOT=/path/to/deepseek-harness go run main.go
```

## 本轮结果（VERDICT: PASS）

捕获 **16 个 `session.event`**（seq 0→15），完整覆盖一个 `completed` turn：

```
agent/inbox/spliced → turn/start → agent/inbox/spliced → step/start →
user/message → session/title → request/header → request/context →
assistant/chunk ×5 → assistant/message → step/end → turn/end(completed)
```

- `session.status` running → idle 双通道
- `initialize` → `{serverInfo:{name:'deepseek-harness-sdk-runtime',version:'0.0.1'}}`
- `session/prompt` → `{messageId}`（仅入队回执，不关联 turn 结果）
- 落盘 `<DSH_SESSION_ROOT>/<encoded-project>/<encoded-session>/session.jsonl.zstd`

完整 raw frames 见 `run-output.txt`。

## 覆盖边界（重要）

仅 **1 个 `completed` 纯文本 turn**，覆盖 DSH 44 种已知事件中的 **14 种**。剩余 30 种（`todo/write`、`compaction/*`、`approval/*`、`tool/result.meta` FsDiff、非 completed 五态、错误路径等）**无 runtime dump** —— 需真实 `DEEPSEEK_API_KEY` 或 assembled approval composition 才能补，是"暂不进入实现"的主因（见设计文档 §14）。

committed snapshots（`examples/jsonrpc-agent/tests/snapshots/{text-turn,bash-tool,subagent-spawn-in-process,persistent-tools}`）交叉补证 `tool/call`、`reasoning-delta`、`tool/result` 基本结构，但非本脚本产出。

## 文件

- `main.go` —— Go 验证脚本（spawn + mock API + JSON-RPC 帧往返 + session.event 捕获）
- `run-output.txt` —— 本轮完整输出（raw frames）

> 脚本仅用 Go 标准库，不依赖 macbridge module；`go run main.go` 即可。
