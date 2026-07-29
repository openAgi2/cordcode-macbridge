# 架构健康治理执行计划完成情况 — 审计报告

- **审计日期**：2026-07-03
- **被审计报告**：[docs/2026-07-03-architecture-health-execution-plan完成情况-审计入口.md](2026-07-03-architecture-health-execution-plan完成情况-审计入口.md)
- **审计者**：审计 agent（以代码与命令实跑为唯一真相源）
- **方法**：独立复核被审计报告的每项 claim，标记 ✅ re-verified（独立复核/实跑通过）/ ⚠️ 部分 / ❌ 不符 / 🔬 self-attested（环境不就绪待 owner 复跑）。Go 侧命令本审计实跑；iOS 侧由独立子 agent 实跑（node v25.9 / vitest 3.2.6 / Xcode 27.0 / iPhone 17 Pro Max 模拟器均就绪）。
- **证据口径**：被审计报告区分 re-verified（执行 agent 复跑）/ self-attested（结构性证据待复核）；本审计把后者独立复核后升级为 ✅ re-verified 或打回。

---

## 总体结论

**claim 兑现度高，无造假、无重大偏差。** Batch A/B1（Go）+ Batch C/D（iOS）+ Batch E 全部独立复核通过；Batch B2 保持 blocked 符合计划；边界（不跑 UI automation / snapshot / simulator UI）未越界。**唯一 self-attested 项**：真机安装 device `BFC431AC-...`（owner 人工操作，agent 无法复跑）。

| Batch | 实质性 claim | ✅ re-verified | ⚠️/❌ | 🔬 self-attested |
|---|---:|---:|---:|---:|
| A capability 单源化 | 4 | 4 | 0 | 0 |
| B1 provider seed 解耦 | 4 | 4 | 0 | 0 |
| C web 共享包 | 7 | 7 | 0 | 0 |
| D characterization | 3 | 3 | 0 | 0 |
| E 工程宪法 | 3 | 3 | 0 | 0 |
| B2 config 删除（blocked） | — | blocked 状态符合 | — | — |
| 真机安装 | 1 | — | — | 1 |

---

## 一、Batch A：capability 单源化 — ✅ 全 re-verified

| claim | 标记 | 证据 |
|---|---|---|
| 新增 `backend_capabilities.go`，BackendList + BuildAgentDescriptor 共用单源 | ✅ | `go-bridge/backend_capabilities.go`（2294B）定义 `deriveBackendCapabilities`；`agent_descriptor.go:112` BuildAgentDescriptor 调它；`handlers.go:361` BackendList 调它——两处不再手写 capability |
| Codex app_server 宣告 compression + question_reply | ✅ | `backend_capabilities.go:57-59` codex app_server 分支追加 compression 后接 question_reply（顺序符合计划 P2-1）—— **A 的核心 bug（hello_ack 漏 question_reply）已修** |
| session_pagination 仍关闭 | ✅ | `backend_capabilities.go:17-25` 注释关闭（含振荡原因说明），未误启用 |
| capability 顺序保持 + 测试不回归 | ✅ | 顺序与原 deriveCapabilities 一致；`go test ./go-bridge -run 'Test.*Capabilit\|TestBackendList\|TestBuildAgentDescriptor'` ok 1.298s；`go test ./go-bridge/...` ok 12.746s；`go vet` exit 0 |

> **澄清**（避免后续误判）：`handlers.go:696 case "question_reply"` 与 `:3998 question_reply_failed` 是 RPC dispatch（处理 iOS 发来的 question_reply 请求），**不是** capability 双写残留。question_reply 的 capability 派发只在 `backend_capabilities.go:59` 单源。

---

## 二、Batch B1：provider seed 解耦 — ✅ 全 re-verified

| claim | 标记 | 证据 |
|---|---|---|
| 新增 `provider_seed_config.go`，最小 TOML seed model | ✅ | `go-bridge/provider_seed_config.go`（6950B），用 `github.com/BurntSushi/toml`（根 module 已有依赖，未引入新 parser） |
| provider_switch.go 改调 loadProviderSeedConfig、不再 import legacy config | ✅ | `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' --glob '!*_test.go' .` exit 1（**空——生产已切断**） |
| TestProviderSeedDoesNotImportLegacyConfigPackage 静态防回归 | ✅ | 存在于 `provider_switch_test.go:351` |
| 测试不回归 | ✅ | 含 `Test.*Provider\|TestProviderSeed` 全绿（见上） |

> **观察**（非问题）：`provider_seed_config.go` 比"最小结构"更丰富——除计划列字段外，还迁移了 `ProviderRefs` / `AgentTypes` / `Endpoints` / `AgentModels` / `AgentModelLists` / `${VAR}` env 解析。这些是原 config 包的真实能力，B1 为行为等价一并迁移（合理），且让后续 B2 删除 config 包更安全。

---

## 三、Batch C：web 共享包 — ✅ 全 re-verified（子 agent 实跑）

| claim | 标记 | 证据 |
|---|---|---|
| `shared-message-renderer/` 结构齐全 | ✅ | `src/components/blocks/{DiffViewer,ToolBlock}.tsx`、`src/host.ts`、`src/types.ts`、`src/utils/{diffParse,diffStats}.ts`、`package.json`、`tsconfig.json` 全在 |
| tsconfig paths + vite alias 源码共享，无 `file:`/link/workspace/copy | ✅ | 两端 tsconfig + vite.config 配 `@cordcode/shared-message-renderer` alias 指向 `../shared-message-renderer/src`；node_modules 无 `@cordcode` 拷贝/符号链接 |
| wrapper thin，不直连 postToNative | ✅ | 两端 wrapper 从共享包 re-export + `host={{ post: postToNative }}` 注入；共享组件内部不直连 |
| host.ts 3 action payload 一致 | ✅ | `RendererHostAction = Extract<WebEvent, openDetail/permissionAction/questionAction>`；payload 与 `message-web/src/types.ts:230-258` 逐行一致 |
| 共享包不直连 postToNative/bridge/relay/OpenCodeiOS | ✅ | `rg ... shared-message-renderer/src` → exit 1（空） |
| NarrativeBlock git directive + ReasoningBlock labels 未迁移 | ✅ | 共享包无这两个组件；message-web NarrativeBlock 仍含 `extractGitDirectives` / `git-directive-*` |
| 命令全跑通过 | ✅ | shared typecheck+test（8 passed）；message-web typecheck+build+build:ios 全绿（304 modules）；remote-web typecheck+build 全绿（340 modules）；message-web test:vitest `-- DiffViewer`(1)/`-- ToolBlock`(1) passed |

---

## 四、Batch D：characterization tests — ✅ 全 re-verified（子 agent 实跑）

| claim | 标记 | 证据 |
|---|---|---|
| GodObjectCharacterizationTests.swift 存在，覆盖策略矩阵 + generation cycle | ✅ | 2 个 test：connection strategy matrix（wifi/cellularOnly/remote/relay/unavailable × local/remote → directPhase/relayOnly/deferredToRecovery）+ generation cycle settled boundary |
| characterization 钉行为，非拆分 | ✅ | 命名带 `BeforeSplitting`；`BridgeProvider.swift` 仍 **1967 行**（未拆）；`sendMessage()` 仍 **580 行（137-716）**（未拆） |
| xcodebuild test 2 tests 0 failures | ✅ | 实跑：`Executed 2 tests, with 0 failures (0 unexpected) in 0.013 seconds`，`** TEST SUCCEEDED **` |

---

## 五、Batch E：工程宪法 — ✅ 全 re-verified

| claim | 标记 | 证据 |
|---|---|---|
| `docs/engineering-constitution.md` 存在 | ✅ | 2457B |
| `scripts/check-architecture-hygiene.sh` warning-only | ✅ | 3602B 可执行；line 80-81 "warning-only... does not fail this script" + `exit 0`；做 inventory（NSLog/print/Logger、CJK 硬编码、ForTesting、长文件 Go>1000/Swift>1000/TS>600、protocol reminder），不改运行路径、不接 required CI gate |
| git diff --check | ✅ | Mac 仓 exit 0 |

---

## 六、Batch B2：blocked 状态 — ✅ 符合计划

- `config/config.go` 仍在（**未误删**）✅
- B1 已切断生产依赖（rg 生产空），但 B2 进入条件尚未全部满足：
  1. test-only 依赖仍在（`agent/claudecode/provider_integration_test.go`、`agent/codex/provider_switch_test.go` 仍 import config）——删除前需迁移或移除
  2. 项目方确认不再维护 `.cc-connect/config.toml` 旧业务写入
  3. 评审同意删除
- **保持 blocked 正确**——不靠实现 agent 拍板删除，需 owner + 评审明确。这与报告自述一致。

---

## 七、边界核验 — ✅ 未越界

- **未跑 UI tests / snapshot / simulator UI automation**：GodObject test 中的 `snapshot` 仅是 `NetworkInterfaceSnapshot`（网络接口数据结构），**非** UI snapshot 框架；无 `SnapshotTesting` / `ios-snapshot-test-case` / `XCUITest` import。git status 干净，无 xcresult/snapshot/真机残留提交。
- **真机安装 device `BFC431AC-...`**：🔬 owner self-attested（agent 无法复跑，属人工验收）。

---

## 八、审计发现

**无造假、无重大偏差。** 两点观察（非问题，仅供后续审计者知悉）：

1. **Batch B1 实现比计划"最小结构"更完整**（迁移了 ProviderRefs/AgentTypes/Endpoints/AgentModels/env 解析等原 config 能力）——属合理的行为等价迁移，且让 B2 删除 config 包更安全。
2. **Batch A 的 handlers.go 中 question_reply 字样是 RPC dispatch**（处理客户端请求），非 capability 双写残留——后续审计者勿误判为双写未清。

---

## 九、小结

完成报告的 claim 兑现度极高：**A/B1/C/D/E 共 21 条实质性 claim 全部独立 re-verified**（Go 侧本审计实跑、iOS 侧子 agent 实跑，命令环境均就绪、无技术性 self-attested 项）；B2 blocked 符合计划；边界未越界。**唯一待 owner 验收的是真机安装**（device `BFC431AC-...`）。

报告质量可信，可作为 B2 进入决策与后续治理的依据。

**未修改任何代码。**
