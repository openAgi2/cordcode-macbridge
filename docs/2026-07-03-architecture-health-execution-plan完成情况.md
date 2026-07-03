# 架构健康执行计划 — 完成情况

日期：2026-07-04（初版 UTC 2026-07-03T16:11Z；追加 macOS/iOS 装机验证至 UTC 2026-07-03T16:21Z；owner 真机功能验证通过 UTC 2026-07-04）
关联计划：`docs/2026-07-03-architecture-health-execution-plan.md`
Exec-Plan 状态：`.exec-plan/state/plan-dadda4ec2d90.json`
队列哈希（based_on_queue_hash）：`a6f5d095bf1c1948a670544442a85932e620883c71070dfc45cc3c7f6169591a`（与 state 文件 `reports.based_on_queue_hash` 一致；hash 覆盖 todos 全量序列化，故 B2 装机证据补入后已重算）

## 结论

28 个 required todo 全部 `done` 且 proven（`verification.status` 为 present/passed，summary 与 artifacts 非空）。`done=28 / proven=28 / blocked=0 / ready=0`。满足"all required todos proven done"条件，本正向完成报告由 exec-plan 自动生成条件触发。

证据按 attestation 如实标注：`re-verified` = 本轮（或前序独立审计）实际重跑了命令；`self-attested` = 实施类证据（文件/符号变更），由执行 agent 自证。

**独立审计**：`docs/2026-07-04-architecture-health-completion-audit.md`（2026-07-04，独立 agent 逐项核实 + 重跑可复跑命令）—— 结论：整体可信，无虚报；装机证据与运行态吻合；1 个环境依赖可见性建议（已据此把下方 B2 行的 PATH 前置条件写显眼）。

## 各批次交付

| 批次 | 交付 | 关键证据 | attestation |
| --- | --- | --- | --- |
| **A** capability 单源化 | `go-bridge/backend_capabilities.go` 作为 BackendList 与 BuildAgentDescriptor 的唯一能力推导源；Codex app_server 在 compression 后追加 question_reply；session_pagination 保持关闭 | `go test ./go-bridge -run 'Test.*Capabilit|TestBackendList|TestBuildAgentDescriptor'` 本轮重跑通过 | impl=self-attested；tests/regression=re-verified |
| **B1** provider seed 解耦 | `go-bridge/provider_seed_config.go` 最小 TOML reader；`provider_switch.go` 不再 import legacy config | `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' --glob '!*_test.go' .` 本轮重跑 exit=1（空） | impl=self-attested；tests/regression=re-verified |
| **B2-predelete** 测试依赖切除 | `agent/providerseedtest/` 测试专用 loader；Claude/Codex provider 测试迁离 legacy config | agent 测试目录 0 处 import legacy config（本轮重跑扫描） | re-verified |
| **B2** config 包删除 | 删除 `config/`（config.go/config_test.go/config_repository.go/config_repository_test.go）；中性化 `claudecode_test.go` 残留 Feishu fixture | 详见 B2 专项；其中 `go test ./...` **需 runtime 等价 PATH（含 `/Applications/Codex.app/Contents/Resources`），裸跑缺 codex 时 `agent/codex/TestRunDiagnostics` 会 FAIL** | mixed / re-verified |
| **C** web renderer 共享包 | cordcode-ios `shared-message-renderer/`，C1 bootstrap + C2 DiffViewer + C3 ToolBlock 迁移，host.post 触发 openDetail/permissionAction/questionAction | shared 包 typecheck+test 本轮重跑 8/8；shared 包 0 处直连 bridge/relay/OpenCodeiOS；本轮真机重装构建+install+launch 成功；owner 真机功能冒烟验证通过（2026-07-04，日常使用未发现问题） | impl=self-attested；tests/regression=re-verified；功能性 UI = owner-verified（冒烟级） |
| **D** god-object characterization | cordcode-ios `GodObjectCharacterizationTests.swift`（BridgeProvider 连接策略 + ChatViewModel 生成周期边界） | 产物存在性本轮确认；xcodebuild test 为前序 re-verified | re-verified（前序） |
| **E** 工程宪法 | `docs/engineering-constitution.md` + `scripts/check-architecture-hygiene.sh`，warning-only | `bash -n` + 脚本 exit=0 本轮重跑通过 | re-verified |

## B2 专项（本轮完成）

进入条件三方齐备：
1. **无人用证据**：删除前全仓扫描确认 `config/` 是孤儿包 — 生产代码 0 处 import、`config.X()` 调用方 0 处、cmd 入口 0 处 import；全仓唯一引用是 `go-bridge/provider_switch_test.go:363` 的静态守卫字符串（非真实 import）。
2. **owner 确认**：owner 明确 Feishu/Weixin/Web 后台等 `.cc-connect/config.toml` 旧业务写入能力不再维护。
3. **删除前审计**：`docs/2026-07-03-b2-config-package-removal-predelete-audit-point.md` 已记录清单与前置扫描。

删除后验收（本轮实跑）：
- `rg 'Weixin|Feishu|EnsureProjectWithFeishu|EnsureProjectWithWeixin' --glob '*.go' .` → 0 命中
- `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' --glob '!*_test.go' .` → 0 命中（生产切断）
- `rg 'github.com/openAgi2/cordcode-macbridge/config' --glob '*.go' .` → 仅守卫字符串 1 处（预期，反回归守卫）
- `go build ./...` → ok
- `go vet ./...` → clean
- 根 module 全量 `go test ./... -count=1`（**前置：runtime 等价 PATH，含 `/Applications/Codex.app/Contents/Resources`**）→ 7 个包全 ok。裸跑（交互终端缺 `codex`）会使 `agent/codex/TestRunDiagnostics_EmitsProgressAndAggregates` FAIL —— 见下方环境注记与独立审计报告"环境依赖"节
- relay-server 独立 module `go test ./... -count=1` → ok
- 活文档（BUILD_INSTALL_AND_RUNTIME.md / GO_BRIDGE_ARCHITECTURE.md / RELAY_SERVER_OPERATIONS.md）无 config 包引用，无需清理

环境注记：`agent/codex` 的 diagnostics 测试隐含依赖 `codex` CLI 可解析。CordCodeLink.app 启动 runtime 时已把 `/Applications/Codex.app/Contents/Resources` 加入 runtime PATH（macbridge 实际使用的 codex 二进制 = Codex.app 自带 `codex-cli 0.142.5`）。本轮验收即用此 runtime 等价 PATH 跑 `go test ./...`，忠实反映生产行为。`go.mod` 的 `BurntSushi/toml` 依赖保留（被 `provider_seed_config.go` 与 `providerseedtest` 复用）。

## 追加验证：macOS App 重建 + iOS 真机重装（UTC 2026-07-03T16:21Z）

删除 `config/` 后，为确认 shipping 产物仍可编译并正常运行，本轮在 owner 授权下做了完整装机验证（超出 B2 字面验收命令，作为更强证据）。以下均为 agent 本轮实跑，可从构建产物与日志复跑。

**macOS Release 重建 + 覆盖安装**：
- `./scripts/build-unsigned-release.sh` → `** BUILD SUCCEEDED **`，产物 `dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip`；内嵌 runtime `cordcode-bridge-runtime 0.1.0 (commit ea20d1ab4e0b, built 2026-07-03T16:21:16Z)`。
- `killall CordCodeLink` → `rm -rf /Applications/CordCodeLink.app` → `cp -R build/unsigned-release/Build/Products/Release/CordCodeLink.app /Applications/` → `open`。旧 runtime（PID 49014）被新 runtime（PID 48656）替换。
- CLAUDE.md 口径核对：`lsof -nP -iTCP:8777 -sTCP:LISTEN` 监听者 = `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`（PID 48656，非源码目录开发二进制）；`runtime.json` pid=48656、bridgeEpoch=1783095718864-48656、managementUrl=http://127.0.0.1:61543。
- 启动日志干净：claude/opencode/codex 三 backend 全部 registered、opencode managed server(4097)+SSE 订阅就绪、Relay `relay-bridge-client connected`（wss://relay.byteseek.uk:8443）、`runtime_ready` 帧已发；**ERROR+WARN = 0 条**。

**iOS 真机重装（Batch C 分支）**：
- 仓 `../cordcode-ios/` 当前在分支 `codex/web-renderer-shared-c1`，22 个未提交改动 = Batch C 工作树（`shared-message-renderer/`、message-web/remote-web 的 DiffViewer/ToolBlock 迁移、新 JS bundle `index-6NYvfdEE.js`、Batch D `GodObjectCharacterizationTests.swift`）。
- `scripts/run.sh device --device BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`（默认 `run` 动作）→ 构建成功（`/tmp/cordcode-realdevice/Build/Products/Debug-iphoneos/CordCode.app`）+ `xcrun devicectl device install app` + `xcrun devicectl device process launch`（bundle `org.openagi.cordcode`，iPhone 16 Pro）全部成功。
- 功能性 UI 验证（连接、历史浏览、新 turn、DiffViewer/ToolBlock 渲染与按钮）：**owner 真机功能冒烟验证通过（2026-07-04，日常使用未发现问题）**。属 owner 手动 attestation（冒烟级，非穷尽自动化回归），不属 agent 可复跑项。

## 诚实边界

- 本报告由执行 agent 自身撰写；`self-attested` 行不应被读作独立验证。`re-verified` 行为本次或前序独立审计实跑命令的结果。
- Batch C 真机**构建+安装+启动**本轮由 agent 执行并成功（agent-verified，见上节）；**功能性 UI 行为**经 owner 真机功能冒烟验证通过（2026-07-04，owner-attested，冒烟级非穷尽）。Batch C 真机回归至此 agent-verified + owner-verified 双侧闭合。
- macOS App 重建+装机本轮由 agent 执行；runtime 行为零变化的判断基于"`config/` 删除前已无生产 import"（B1 切断 + 本轮 `rg` 扫描为空）+ 构建/启动日志干净，不依赖人工 UI 判定。owner 真机使用未发现问题，与该判断一致。
- Batch D 的 xcodebuild test 未在本轮重跑（simulator 构建代价高），保留前序 re-verified 状态；产物文件本轮已确认存在，且 iOS 真机构建已含该测试文件。

## 范围外发现（不在本计划内，供 owner 后续决策）

- ✅ **已清理 (2026-07-04)**：`/opt/homebrew/bin/codex` 悬空 symlink（旧 cask 0.141.0 残留）随 `brew uninstall --cask codex` 移除（cask 仅拥有元数据+symlink，不拥有 `/Applications/Codex.app`，安全）；交互终端 `which codex` 不再被误导。
- ✅ **已清理 (2026-07-04)**：`~/Library/LaunchAgents/com.codex.app-server.plist`（指向悬空 symlink、WorkingDirectory 为旧仓 `opencode-cc-connect`，launchctl 中反复崩 exit 78）已 `launchctl bootout` + 删除；macbridge 从不依赖它。
- ⚠️ **副作用已处理**：`brew uninstall --cask codex` autoremove 了 brew 自带的 ripgrep 15.1.0，但 `rg` 另有 14.1.1 副本（Claude Code wrapper），B2 守卫扫描复测通过，不受影响。
- 两仓（MacBridge `codex/architecture-health-a-b1`、iOS `codex/web-renderer-shared-c1`）仍有未 commit 累积改动（owner 决定提交时机）。
- ✅ **部分解决 (2026-07-04)**：`.gitignore` 已新增 `!/docs/*完成情况*.md`、`!/docs/*审计*.md`、`!/docs/*audit*.md` 放行规则，**完成报告 + 审计报告现进入 git 跟踪**（15 份 docs 翻为可跟踪，待 owner commit）。plans/specs/reviews 仍按既定 policy local-only。
