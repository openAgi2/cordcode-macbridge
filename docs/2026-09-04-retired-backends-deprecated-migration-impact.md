# 退役 backend 目录迁入 deprecated/ 影响清单（2026-09-04，未执行）

> 状态：**调查清单，尚未执行任何文件操作**。Owner 倾向方案：新建 `deprecated/`
> 目录，把 4 个退役 backend 目录（`agent/codex`、`agent/codex-web`、`agent/dsh`、
> `agent/opencode`）移入。本文档列出全部影响面供 owner 与其他 agent 审阅。
> 执行前须 owner 拍板「module 内移动 + 删 dead branch / 保留 branch / 移出
> module」三选一（见 §六）。

## 来源清单（P0 门）

```text
MacBridge 仓库路径=/Users/jacklee/Projects/cordcode-macbridge
MacBridge 分支=main  提交=bbbdbb78df3bb8240a2279e92c2e17a7ff7ab161
未提交状态=2026-09-04 codex-web 退役改动（9 文件，见 docs/2026-09-04-codex-web-backend-retirement-完成情况.md）
  + 未跟踪 docs/2026-09-03-plan-mode-cross-backend-survey.md（owner 在写，未触碰）
任务范围=仅本仓（agent/ + go-bridge/ + CI）；iOS 仓不涉及
调查方式=grep 引用面（复核命令见 §七）
```

退役背景：`opencode` 2026-08-19、`codex` 2026-08-25、`deepseek`（`agent/dsh`）
更早、`codex-web` 2026-09-04（本轮）先后从产品 lineup 退役；源码保留是既有
退役模式（回滚 = drivers 加回 id）。`agent/codex-appserver` **不在迁移范围**：
它是共享 RPC 库（无 `RegisterAgent`），被现役 `agent/codex-remote/rpc.go` import。

包规模：4 包合计 151 个 Go 文件、约 43,000 行（自带测试 81 个文件）。

## 一、生产代码引用面（编译破坏点，共 9 处）

### A. 纯 blank import（删 4 行即可）

| 引用 | 内容 |
| --- | --- |
| `go-bridge/main.go:22` | `_ "github.com/openAgi2/cordcode-macbridge/agent/codex"` |
| `go-bridge/main.go:24` | `_ "github.com/openAgi2/cordcode-macbridge/agent/codex-web"` |
| `go-bridge/main.go:25` | `_ "github.com/openAgi2/cordcode-macbridge/agent/dsh"` |
| `go-bridge/main.go:28` | `_ "github.com/openAgi2/cordcode-macbridge/agent/opencode"` |

同文件相关字符串层（不破坏编译，随方案决定去留）：`defaultDrivers`
（main.go:36，standalone fallback 仍含 `codex,codex-web`）、别名表
（`deepseek` → 注册名 `dsh`）。

### B. 真实函数依赖（退役包代码被 go-bridge 生产路径调用，5 处）

**本清单最关键的判断**：这 5 处经逐点核对，全部只服务已退役 backend 的挂载
路径，退役后为 dead-but-compiling。请审阅 agent 重点复核此判断（复核命令见
§七）。

| 引用点 | 用的什么 | 服务谁 / 为何 dead |
| --- | --- | --- |
| `go-bridge/handlers_relay.go:2557-2604` | `codex.IsTranscriptUserPrompt`、`codex.NormalizeTranscriptCustomToolCall`、`codex.TranscriptToolOutput`、`codex.NormalizeTranscriptFunctionCall` | legacy codex 的 transcript file relay（`startCodexSessionFileRelay`）JSONL 解析；仅挂载 codex backend 时有会话走此路径 |
| `go-bridge/pagination.go:78`（`replayCodexRange`） | `codex.ParseRichHistoryFromReader` | legacy codex 会话的分页回放（transcriptindex range replay）；无 codex backend 即无 codex 会话列表入口 |
| `go-bridge/handlers_codex_catalog.go:30` | `agent.(*codex.Agent)` 类型断言 + `SetCatalogSubprocessRegistrar`（catalog seam） | 仅挂载 codex 实例时断言成功；不挂载则 `ok=false` |
| `go-bridge/agent_descriptor.go:224`（`case "deepseek"` → `detectDSHRuntime`，:269 `dsh.DiscoverRuntime()`） | runtime 发现（PATH → wheel pkg exe → nvm → python wheel） | legacy deepseek 的 hello_ack 状态分支；**现役 `dsh-web` 走独立的 `detectDSHWebInstance`（:230），不经过此函数** |
| `go-bridge/handlers.go:2592`（`agent.Name()=="dsh"`）与 `go-bridge/handlers_projection.go:482`（`backendID=="deepseek"`） | `dsh.StoreHasSession` | legacy deepseek 专属分支（store-bridge resume 拒绝 / 冷水合基线选择）；dsh-web 有独立分支（handlers_projection.go:494 起） |

**已排除的风险（审阅时不必重复调查）**：

- `agent/codex-remote` 的 rollout 历史兜底是自带解析副本，**不 import**
  `agent/codex`（grep 验证：`agent/codex` 的 import 方仅上表 + 测试；codex-remote
  生产代码只 import `core` 与 `agent/codex-appserver`）。
- `go-bridge/opencode-proxy.go` 不 import `agent/opencode`（纯 HTTP proxy）。
- `go-bridge/catalog_native_membership.go` 对 codex-web 仅注释引用（2026-08-23
  sessions_changed 风暴教训），非功能依赖。
- `server.go:454` 等按已注册 backend 列表追加 capability，纯字符串分派，无包依赖。

## 二、测试面

| 类别 | 文件 | 移动时动作 |
| --- | --- | --- |
| go-bridge 测试 import 退役包（编译破坏，11 个） | `session_loading_baseline_test.go`、`pagination_test.go`、`pagination_regression_test.go`、`codexweb_ab_metrics_e2e_test.go`、`handlers_projection_real_test.go`、`projection_codexweb_wiring_test.go`、`projection_codexweb_e2e_test.go`、`projection_webperf_fixture_gen_test.go`、`agent_descriptor_test.go`、`dsh_pipeline_test.go`、`handlers_projection_liveonly_test.go`、`opencode_provider_test.go` | import path 改 `deprecated/…`；若对应功能属 dead branch 删除范围则随删 |
| 现役包的守护/身份测试 | `agent/codex-remote/provenance_test.go`（import codex + codex-web，校验三者身份独立）；`agent/opencode-web/import_guard_test.go`（import agent/opencode，断言 opencode-web 不依赖旧包） | 改 path 或调整断言语义（守护测试对 deprecated 位置的语义需 owner 拍板） |
| 退役包自带测试 | 81 个 `_test.go` 随目录走 | 包内自包含；仅 `agent/codex-web/provenance_test.go` 跨包 import `agent/codex` 需改 path |
| 仅字符串引用（`kind=="codex-web"` 等，不 import 包） | go-bridge 约 30+ 测试文件 | 不破坏编译，无需动 |
| Swift 测试 | `MacBridge/MacBridgeTests/MacBridgeBehaviorTests.swift` 4 处显式 `drivers: ["codex-web"]`（daemon seat 单测） | 不 import Go，不受影响，不动 |
| 校验脚本 / testdata | `agent/codex-appserver/validate/*.mjs`、`agent/codex-remote/testdata/**` 内的退役包路径引用 | 脚本级路径失效，需同步改或声明作废 |

既存行为提示：`agent/codex` 的 `TestRunDiagnostics` 依赖 codex CLI（本机 PATH
无 codex 必失败；CI 装 `@openai/codex` 所以过）——移动后原样跟随，非新增问题。

## 三、CI 面（重要预期差）

CI go job（`.github/workflows/ci.yml:39-41`）跑 `go build ./go-bridge` +
`go test ./go-bridge/... -count=1` + `go test ./... -count=1`。
**deprecated/ 只要还在 root module 内，151 个文件照常参与编译与测试**——
移动本身不减编译面、不减 CI 时间，仅目录归档语义。若目标是真正豁免，需额外
决策：改 CI 显式包列表 / 加 build tag / 移出 module（≈事实删除）。

## 四、文档面

- `CLAUDE.md` component map 的 `agent/{…}` 路径列举；
- `GO_BRIDGE_ARCHITECTURE.md` 各退役 backend 节的路径引用 + 双真值段；
- `BUILD_INSTALL_AND_RUNTIME.md` 相关段；
- `docs/2026-09-04-codex-web-backend-retirement-完成情况.md` 与 `think.md`
  2026-09-04 条目中「回滚 = drivers 加回 id」的表述需随方案更新；
- `CHANGELOG.md` 补条目。

## 五、回滚损失对比

| 方案 | 回滚动作 | 回滚能力 |
| --- | --- | --- |
| 现状（不动） | drivers 加回 id，一行 | 即时 |
| module 内移 deprecated/ + 删 dead branch | git revert + 移回目录 + 恢复 import + 重过编译测试 | 依赖 git 历史，仍可恢复 |
| 移出 module | git revert | 同上，但期间代码完全脱离编译 |

## 六、待 owner 拍板的分叉

1. **纯归档（module 内移动）**：保留 §一B 的 5 处 dead branch → deprecated 包仍被
   go-bridge 生产 import，移动失去大部分意义。**不推荐**。
2. **归档 + 清 dead branch（module 内移动 + 删 §一B 5 处及其测试）**：干净，
   一次性成本 = 9 处生产 import + 11 个测试文件 + 守护测试调整 + 文档同步。
   **推荐**（若动机是归档标记 + 防误用）。
3. **移出 module**：等于事实删除（脱离编译），回滚只靠 git。若动机是减编译/
   CI 负担，只有此方案真正生效。

共同前提：`agent/codex-appserver` 不随迁（现役 codex-remote 依赖）。

## 七、复核命令（供审阅 agent 逐项验证）

```bash
cd /Users/jacklee/Projects/cordcode-macbridge

# 1. 退役包的全部 import 方（§一A/§一B/§二 的来源）
grep -rln 'cordcode-macbridge/agent/codex"' --include='*.go' . | grep -v '^./agent/codex/'
grep -rln 'cordcode-macbridge/agent/codex-web' --include='*.go' . | grep -v '^./agent/codex-web/'
grep -rln 'cordcode-macbridge/agent/dsh"' --include='*.go' . | grep -v '^./agent/dsh/'
grep -rln 'cordcode-macbridge/agent/opencode"' --include='*.go' . | grep -v '^./agent/opencode/'

# 2. §一B 各调用点上下文（确认 dead 判断）
sed -n '2535,2610p' go-bridge/handlers_relay.go        # codex transcript 解析
sed -n '60,85p' go-bridge/pagination.go                # replayCodexRange
sed -n '23,45p' go-bridge/handlers_codex_catalog.go    # catalog seam 断言
sed -n '210,232p' go-bridge/agent_descriptor.go        # deepseek vs dsh-web 分支
sed -n '2580,2600p' go-bridge/handlers.go              # dsh resume 拒绝分支
sed -n '470,500p' go-bridge/handlers_projection.go     # deepseek vs dsh-web 投影分支

# 3. codex-appserver 的现役依赖（不可随迁的依据）
grep -rln 'agent/codex-appserver' --include='*.go' .   # → codex-web(退役) + codex-remote(现役)

# 4. CI 测试范围（§三 预期差的依据）
grep -n 'go test\|go build' .github/workflows/ci.yml
```
