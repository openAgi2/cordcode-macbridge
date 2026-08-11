# Agent 结构化输出 / Git 动作质量门禁

> **状态**：生效（2026-08-07）  
> **范围**：MacBridge agent drivers（Claude / Codex / …）的非交互式结构化输出解析，以及依赖它们的 Git 动作（commit message、PR 内容）。  
> **动机**：多次线上失败是「只接一种外部 JSON 形状」——例如 Codex commit 生成只 `json.Unmarshal` 进 `string`，真实 CLI 写的是 object，报 `cannot unmarshal object into Go value of type string`。

---

## 1. 硬规则（合入前必须满足）

### G1 — 禁止 driver 私有 envelope 假设

- 解析 CLI / tool 结构化输出时，**必须**走 `core` 共享路径：
  - `core.UnwrapJSONPayload` / `core.UnmarshalJSONPayload` — 对象 **或** JSON 字符串包一层
  - `core.ExtractClaudePrintStructuredPayload` / `core.UnmarshalClaudePrintStructured` — Claude `-p --output-format json` 信封
- **禁止**在 driver 内再写「只 Unmarshal 到 string」的 envelope 解析（除非该字段语义真的是纯字符串且有 fixture 证明）。

### G2 — 双形状 fixture 单测

每个 `GenerateCommitMessage` / `GeneratePrContent`（及同类 structured generator）必须有单测覆盖至少：

| 形状 | 示例 |
|---|---|
| 直接 object | `{"message":"feat: x"}` |
| 双层 string | `"{\"message\":\"feat: x\"}"` |

Claude print 路径额外覆盖：`result` object、`structured_output` string、缺字段失败。

测试可放在 `core/`（共享逻辑）+ driver 包内 thin smoke；**不得**仅靠「真机点一下」验收解析。

### G3 — 失败必须可诊断

- 解析失败：错误信息带 driver 名 + 阶段（envelope / payload），**不要**吞成笼统「失败」。
- 空 title/message/body：显式 empty 错误，禁止 silent fallback 到硬编码假正文（产品诚实规则）。

### G4 — Git 动作错误路径

| 路径 | 要求 |
|---|---|
| 脏工作区 checkout | 真实 git 错误；客户端应用中文可行动文案 |
| commit / push / commit_push | `action` 三态有单测；push-only + dirty → 明确错误码 |
| 连接中断 | 客户端不展示无上下文的「未连接」即可；应提示关面板重试 |

### G5 — Runtime 部署自检

覆盖 `/Applications/CordCodeLink.app/.../cordcode-bridge-runtime` 后必须：

```bash
file "$RUNTIME" | grep -q 'Mach-O'   # 禁止 ar archive
"$RUNTIME" -version                  # 必须可执行
```

构建入口固定为：

```bash
go build -o cordcode-bridge-runtime ./go-bridge/cmd/cordcode-bridge-runtime
```

**禁止** `cd go-bridge && go build .`（`package gobridge` 会打出静态库）。

### G6 — 完成定义

| 轨 | 标准 |
|---|---|
| 单测 | `go test ./core/ ./agent/codex/ ./agent/claudecode/ ./go-bridge/ -count=1` 相关包绿 |
| 部署 | G5 自检通过 + 进程 LISTEN 8777 |
| 真机 | owner 矩阵至少：留空 message 提交成功；脏工作区切分支中文提示；三动作可见 |

`self-attested` 不得替代「双形状单测」或 owner 真机行。

---

## 2. Code review 定点清单（比泛 review 有效）

1. **外部边界**：CLI 输出 / gh / git 的形状与失败码是否有 fixture？  
2. **跨 driver**：Claude 与 Codex 是否共用 `core` 解析？有没有复制粘贴分叉？  
3. **失败路径**：空输出、object vs string、脏 worktree、无 upstream、断连。  
4. **部署**：runtime 是否 Mach-O + `-version`？  

---

## 3. 权威实现位置

| 能力 | 路径 |
|---|---|
| 共享 unwrap | `core/structured_output.go` |
| 单测 | `core/structured_output_test.go` |
| Codex commit/PR | `agent/codex/commit_message.go`, `pr_content.go` |
| Claude commit/PR | `agent/claudecode/commit_message.go`, `pr_content.go` |
| Codex tool args | `agent/codex/todos.go` → `core.UnmarshalJSONPayload` |

---

## 4. 修订记录

| 日期 | 说明 |
|---|---|
| 2026-08-07 | 初版：Codex commit envelope 事故后固化门禁；抽出 core 共享解析 |
