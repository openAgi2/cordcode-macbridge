# 监工指令 14 号完成情况（Owner UI reasoning hydrate 修复）——directive-014 待监工审计

> **指令**：directive-014-owner-ui-reasoning-hydrate-repair.md（owner-acceptance repair，依据 canonical §6.3 修正 + E2b 证据）
> **实现提交**：`be6a1d7`（产品修复 + 红/绿 owning tests）、本报告提交
> **声明**：exec-plan proven ≠ supervisor verified ≠ owner done。提交本报告后停止，等待 audit-014；不自行执行 owner UI。

## 1. 红灯（before）与修复（after）

红灯在修改产品代码前落盘并在旧实现确认：

| 层 | 红灯（旧实现） | 修复后（绿） |
|---|---|---|
| adapter `GetRichSessionHistory`（E2b fixture：user/assistant ×2，assistant 顺序 `step-start, reasoning, text, step-finish`） | `opencode-web: history row 1: unsupported content.reasoning for verified 1.18.18 shape`（整页失败） | 4 行全部映射；每个 assistant 行恰一个 `{type:"reasoning", content:<精确正文>}` 且先于 text；reasoning 不并入 `Content` |
| full-path `get_session_projection`（同 fixture，真实 Handlers + hydrate + Kernel） | 同一错误链浮出（`projection.hydrate_failed` 族，owner UI 所见） | hydrate 成功；每 assistant turn reasoning 恰一次；`step-start/step-finish` 零 part；answer text 无污染 |
| 对照（同一 fixture 去掉 reasoning） | 旧实现即成功——失败与 reasoning 存在一一对应 | 保持成功 |

## 2. 实现边界（指令 §1 逐条）

- `mapRichHistoryEntry`：非空 reasoning → `Parts {type:"reasoning", content:<exact text>}`，服务端顺序保留；不并入 Content、不丢弃、不截断、不造 ID。
- `text` 缺失或非 string → fail closed（整页失败）；空白 text → 跳过（与 live population 判定一致，官方 store 保留空 part 但 hydrate 链路本就丢空 chunk）。
- `step-start`/`step-finish`/`patch` 按官方 Web skip list（E2b `officialWeb.skipParts`）显式跳过——不是"未知 part 均可忽略"的通用特赦。
- 仍由现有 private Kernel hydrate transaction + 唯一 ingest 写入；未新增 reducer、history fallback、raw iOS writer、去重或协议字段。
- **未顺手实现 live reasoning**：`errUnsupportedReasoning` 收窄为 live 载体专属（错误文案改为 `…live shape`），live mapper/advertisement 原样缺席；本轮未观察到任何 live reasoning 样本。

钉住旧 E2 结论的测试同步更新：`TestGetRichSessionHistoryMapsParts`（映射 + 缺失/非 string/空白负向）、`TestReasoningExplicitlyUnsupportedOnAllCarriers` 更名 `…OnLiveCarriers`（live 三载体仍报 unsupported、永不 EventThinking）。

## 3. 回归输出（引号正则，zsh 直接可复现）

- `go test ./agent/opencode-web -run 'Reasoning|RichSessionHistory|History' -count=1 -timeout 3m`：**PASS**。
- `go test ./go-bridge -run 'OpenCodeWeb|Projection|Hydrate|Audit01' -count=1 -timeout 5m`：**PASS**。
- `go test ./... -count=1 -timeout 10m`：**全 PASS**。
- `go test -race ./agent/opencode-web ./go-bridge -run 'Reasoning|Projection|Hydrate' -count=1 -timeout 5m`：agent 腿 PASS；go-bridge 腿命中 **既有** race——`TestClaudeFileRelayStartedBeforeHydrate`（`claude_file_relay_test.go:23` fixture 写 vs `handlers_relay.go:725` 读）。已证与本轮无关：把本轮改动 stash 后在基线 HEAD 上同样复现 WARNING；`-skip` 该用例后整条命令 PASS（15.9s，无 race）。该 Claude relay 测试面不在 directive-014 范围内，仅如实上报，未动。
- `go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_canonical_execution_design.py --self-test`：**PASS**。

## 4. 只读 projection preflight（真实 owner 数据，同一产品构建）

方式：用**同一 Release 构建**的 runtime 在本地 8799 端口起只读实例（auth 开发模式），`-opencode-web-url` 指向 owner 自管 4096（仅 GET/history/SSE 读，零写入），WS 发 `list_sessions` → 只读 GET 分类 history → 对目标 shape 发 `get_session_projection`。**输出只含结构计数，无正文/路径/凭据/session ID。**

- 列出 100 个 session；其中 **87 个**含 populated reasoning history——失败面比两个抽样宽（所有含 reasoning 的 session 均在失败类）。
- **精确匹配被定罪 shape（4 行 / 2 assistant 行 / 2 个 populated reasoning part）的 3 个 session：全部 `hydrate=OK`，`turns=2, snapshot_reasoning_parts=2, turns_with_multiple_reasoning=0`**（每 turn 恰一次）。
- 重度 shape 抽样（643/383/378/324/279 个 reasoning part）：全部 `hydrate=OK`，`turns_with_multiple_reasoning=0`，零 `hydrate_failed`。
- 4096 全程未触碰写路径（PID 48742 不变）；本地实例按 PID 回收；4398/4399 本轮未使用。

## 5. 安装与状态

- Mac Release 重建（runtime commit `be6a1d7`）并按规定流程重装：**8777 = PID 32592，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`**。
- iOS 按指令未改未装。双仓本报告提交后 clean。

## 6. 三轨

- exec-plan proven：`owner-ui-reasoning-hydrate-repair-{impl,tests,regression}` 三元组 done（self-attested；测试项可独立复跑）。
- supervisor verified：**待 audit-014**。
- owner 真机 UI：**未执行**——preflight 已满足指令 §3 前置，等待监工放行后由 owner 重试。
