# 架构健康执行计划完成情况 — 独立审计报告

日期：2026-07-04
被审计对象：`docs/2026-07-03-architecture-health-execution-plan完成情况.md`
关联计划：`docs/2026-07-03-architecture-health-execution-plan.md`
关联 exec-plan 状态：`.exec-plan/state/plan-dadda4ec2d90.json`
审计方式：独立 agent 对完成报告逐项核实，重跑可复跑命令、核对产物存在性、对照运行态与 git 历史。

## 结论

**整体可信，无虚报。** 完成报告对 28 个 todo 的 `done+proven` 状态与 exec-plan state 文件一致；A/B1/B2/B2-predelete/E 批次的字面验收命令本轮重跑全部复现；C 批次 shared 包 8/8 测试复现；D 批次产物存在性确认。装机证据（PID/bridgeEpoch/managementUrl/监听端口）与当前 Mac 运行态完全吻合。

报告的诚实性标注（`re-verified` vs `self-attested` vs `owner-verified`）经核实**未发现拔高**：所有标为 `re-verified` 的命令本轮确可复跑通过；标为 `self-attested` 的实施类证据产物真实存在；标为非本轮重跑的项（owner 真机功能验证、D 批次 xcodebuild test）报告已明确披露。

**1 个需提升可见度的环境依赖**（非虚报，已披露但建议更显眼）：`go test ./...` 的"7 包全 ok"**仅在 runtime 等价 PATH（含 `/Applications/Codex.app/Contents/Resources`）下成立**；裸跑（交互终端缺 `codex`）会让 `agent/codex/TestRunDiagnostics_EmitsProgressAndAggregates` 失败。报告第 43 行"环境注记"已如实说明，CHANGELOG 也已标注"runtime 等价 PATH"，但完成报告"各批次交付"表格的 B2 行只写"根 module 全量 `go test ./...` → 7 个包全 ok"，未在表格层显式带 PATH 前置条件，存在被读者误读为"任意环境全绿"的风险。

## 逐项核实

### exec-plan state 与统计声明

| 报告声明 | 核实结果 |
| --- | --- |
| 28 个 required todo 全 `done` | ✅ state 文件 `todos` 数组长度 28，`status` 全为 `done` |
| 全部 proven（`verification.status` 为 present/passed） | ✅ 27 present + 1 passed，`proven=28` |
| `done=28 / proven=28 / blocked=0 / ready=0` | ✅ 完全一致 |
| `based_on_queue_hash` = `a6f5d095...69591a`，与 state 一致 | ✅ state 内 hash 与报告字面完全相同 |
| 完成报告 `completion_report_status: current` | ✅ state 文件确认 |

### B1 / B2 / B2-predelete（legacy config 切断与删除）

| 报告声明 | 核实命令 | 结果 |
| --- | --- | --- |
| 生产代码 0 处 import legacy config | `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' --glob '!*_test.go' .` | ✅ exit=1（空） |
| 全仓唯一引用是守卫字符串 | `rg '...config' --glob '*.go' .` | ✅ 仅 `go-bridge/provider_switch_test.go:363` 守卫字符串 1 处 |
| Feishu/Weixin/EnsureProject 符号 0 命中 | `rg 'Weixin\|Feishu\|EnsureProjectWith...' --glob '*.go' .` | ✅ exit=1（空） |
| `config/` 包已删除 | `ls config/` | ✅ No such file（git status 显示 `D` ×4） |
| `go build ./...` ok | 本轮重跑 | ✅ exit=0 |
| `go vet ./...` clean | 本轮重跑 | ✅ exit=0 |
| 根 module `go test ./...` 7 包全 ok | 本轮重跑（**runtime 等价 PATH**） | ✅ 7 包 ok + 1 cmd 无测试（见下方"环境依赖"注） |
| relay-server module `go test ./...` ok | 本轮重跑 | ✅ `internal/relay` ok |
| `claudecode_test.go` Feishu fixture 中性化 | `rg -i 'feishu\|weixin\|EnsureProject' agent/claudecode/` | ✅ 0 命中 |
| `provider_seed_config.go` 是最小 TOML reader、无 mock/fallback | 通读源码 | ✅ 真实 reader（含 env 展开 + provider ref 解析），无零值伪装 |
| `provider_switch.go` 不再 import legacy config | 通读 imports | ✅ 仅 `fmt/slog/os/path/filepath/strings/core` |
| `agent/providerseedtest/` 测试专用 loader 存在 | `ls` | ✅ `provider_seed.go` + `provider_seed_test.go` |

### A（capability 单源化）

| 报告声明 | 核实结果 |
| --- | --- |
| `backend_capabilities.go` 是唯一能力推导源 | ✅ `deriveBackendCapabilities` 被 `handlers.go:361` 与 `agent_descriptor.go:112` 共用，无第二处推导 |
| Codex `app_server` 在 `compression` 后追加 `question_reply` | ✅ 源码顺序为 `compression` 在前、`question_reply` 在后 |
| `session_pagination` 保持关闭 | ✅ 源码 `TranscriptLocator` 分支整段注释，附振荡原因说明 |
| A 批次测试 re-verified 通过 | ✅ `go test ./go-bridge -run 'Test.*Capabilit\|TestBackendList\|TestBuildAgentDescriptor'` → ok |

### C（web renderer 共享包，iOS 仓 `../cordcode-ios/`）

| 报告声明 | 核实结果 |
| --- | --- |
| iOS 仓在分支 `codex/web-renderer-shared-c1` | ✅ |
| `shared-message-renderer/` 包存在 | ✅ 含 `src/`、`package.json`、`tsconfig.json` |
| shared 包 0 处直连 bridge/relay/opencode | ✅ `rg 'bridge\|relay\|opencode' ../cordcode-ios/shared-message-renderer/src/` exit=1（零匹配） |
| C2 DiffViewer / C3 ToolBlock 迁移产物 | ✅ `components/blocks/DiffViewer.tsx` + `ToolBlock.tsx` + 各自 `.test.tsx` 存在 |
| shared 包 typecheck+test 8/8 | ✅ 本轮 `npm test`：3 文件 8 测试全通过 |
| `host.post` 触发 openDetail/permissionAction/questionAction | ⚠️ 未逐行核实 host adapter 接线（属实现细节，产物存在且测试通过间接支撑）；不在本审计复跑范围 |

### D（god-object characterization）

| 报告声明 | 核实结果 |
| --- | --- |
| `GodObjectCharacterizationTests.swift` 存在 | ✅ `../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/` 下确认 |
| 内容为 characterization（连接策略矩阵 + 生成周期边界） | ✅ 通读：`BridgeProvider.selectConnectionStrategy` 矩阵断言，符合 characterization 测试形态 |
| xcodebuild test 为前序 re-verified、未在本轮重跑 | ✅ 报告"诚实边界"已明确披露，未拔高为本轮 |

### E（工程宪法）

| 报告声明 | 核实结果 |
| --- | --- |
| `docs/engineering-constitution.md` 存在 | ✅ |
| `scripts/check-architecture-hygiene.sh` warning-only | ✅ 脚本输出明确"warning-only ... does not fail this script" |
| `bash -n` + 脚本 exit=0 | ✅ 本轮 `bash -n` 通过、运行 exit=0 |

### 装机证据（macOS App 重建）

| 报告声明 | 核实结果 |
| --- | --- |
| 8777 监听者 = `/Applications/CordCodeLink.app/.../cordcode-bridge-runtime` | ✅ 当前 `lsof` 确认 |
| runtime PID = 48656 | ✅ 当前 `lsof` + `pgrep` 确认 48656 |
| `bridgeEpoch = 1783095718864-48656` | ✅ 当前 `runtime.json` 一致 |
| `managementUrl = http://127.0.0.1:61543` | ✅ 当前 `runtime.json` 一致 |
| 内嵌 runtime commit = `ea20d1ab4e0b` | ✅ 与 git HEAD~ 区间的 `ea20d1a` 一致 |
| CordCodeLink.app 进程在 `/Applications/` | ✅ `pgrep` 确认 PID 48616 |

## 环境依赖（需提升可见度）

完成报告第 39 行声称"根 module 全量 `go test ./... -count=1` → 7 个包全 ok"，并在第 43 行"环境注记"说明"`agent/codex` 的 diagnostics 测试隐含依赖 `codex` CLI 可解析…本轮验收即用此 runtime 等价 PATH 跑 `go test ./...`"。

**核实**：此声明在所披露的条件下成立，但条件是关键前置：

```
$ go test ./... -count=1                      # 裸跑（交互终端）
--- FAIL: TestRunDiagnostics_EmitsProgressAndAggregates
    diagnostics_test.go:36: OverallStatus = "failed", want passed
FAIL  agent/codex

$ PATH="/Applications/Codex.app/Contents/Resources:$PATH" go test ./... -count=1   # runtime 等价 PATH
ok  agent/codex  14.186s   # 其余 6 包也全 ok
```

即"7 包全 ok"**不是任意环境成立**：依赖 `codex` CLI 在 PATH 中。当前交互终端 `which codex` → not found（`/opt/homebrew/bin/codex` 是悬空 symlink，旧 cask 0.141.0 残留），生产 runtime 则通过把 `/Applications/Codex.app/Contents/Resources` 注入子进程 PATH 绕过（`codex-cli 0.142.5`）。

**评估**：这不是虚报——报告与 CHANGELOG 都如实标注了"runtime 等价 PATH"，且"范围外发现"已把悬空 symlink 列为清理候选。但完成报告"各批次交付"表格 B2 行的简短描述（"7 个包全 ok"）未带 PATH 前置条件，读者若只看表格可能误判为环境无关的全绿。**建议**：在表格 B2 行补一句"(runtime 等价 PATH；裸跑缺 codex 时 diagnostics 测试会 FAIL)"，或把"环境注记"前置到表格下方显眼位置。

## 诚实性评估

报告的"诚实边界"小节自述了三类非自证证据：

1. **`re-verified`**（命令实跑）— 本轮抽测的 A/B1/B2/E 字面命令 + C shared 包测试全部复现通过，**无虚报**。
2. **`self-attested`**（实施类证据）— 产物（`backend_capabilities.go`、`provider_seed_config.go`、`agent/providerseedtest/`、`GodObjectCharacterizationTests.swift`、`engineering-constitution.md`、`check-architecture-hygiene.sh`）本轮全部确认存在且内容合理，**无伪造**。
3. **owner-verified / 前序 re-verified**（非本轮）— 报告对 owner 真机功能验证、D 批次 xcodebuild test 均明确标注"非本轮重跑"，**未拔高**。

装机证据（agent-verified）的 PID/bridgeEpoch/managementUrl/监听端口与当前实际运行态逐一吻合，可信度高。

## 范围外发现（被审计报告已自述，本审计确认）

被审计报告第 67-72 行的"范围外发现"四项，本审计复核均属实：

- `/opt/homebrew/bin/codex` 悬空 symlink — ✅ 确认（`which codex` not found 的根因）
- `~/Library/LaunchAgents/com.codex.app-server.plist` legacy 残留 — 未单独复跑，引用报告自述
- 两仓未 commit 累积改动 — ✅ MacBridge 当前分支 `codex/architecture-health-a-b1` 有未提交改动；iOS 仓在 `codex/web-renderer-shared-c1`
- 6 份 docs untracked + `.gitignore` 治理未定 — ✅ 本审计另发现：**完成报告自身**也被 `.gitignore` 第 31 行 `/docs/*` 规则忽略（`git check-ignore` 确认），即这份正向完成报告目前**不在 git 版本控制内**。这是 docs gitignore 治理问题的一部分，但值得单独提示：exec-plan 自动生成条件触发的完成报告若不被跟踪，后续审计只能依赖本地文件，无法从 git 历史回溯。

## 建议（供 owner 决策，不阻塞验收）

1. **(可逆，agent 可做)** 在完成报告 B2 表格行补注 PATH 前置条件，或在表格下加显眼"环境注记"指引。
2. **(治理决策)** 决定 `/docs/*` 的 gitignore 放行规则：当前只 `!` 放行了 web-renderer 一对，完成报告、本审计报告、工程宪法等都被忽略。是否扩大放行或改用 `docs/completed/`、`docs/audit/` 子目录纳入版本控制，需 owner 拍板。
3. **(清理候选，已被报告列出)** 清理 `/opt/homebrew/bin/codex` 悬空 symlink 与 legacy launchctl plist，可消除"裸跑测试失败"的混淆源。
4. **(非阻塞)** C 批次 `host.post` → `openDetail/permissionAction/questionAction` 的 host adapter 接线属实现细节，本审计未逐行核实；既有 shared 包测试 + iOS 真机构建成功 + owner 真机冒烟验证已构成足够证据链，无需额外审计动作。

## 审计边界

- 本审计**未**复跑：iOS 真机构建/安装/启动（需 owner 授权真机）、D 批次 xcodebuild test（simulator 构建代价高）、macOS Release 重建（装机证据已通过运行态吻合间接确认）。
- 本审计**未**逐行核实：C 批次 host adapter 接线、shared 包迁移是否完整覆盖所有 host 调用点（以测试通过 + 产物存在为间接证据）。
- 本审计**未**改变任何运行链路或代码。
