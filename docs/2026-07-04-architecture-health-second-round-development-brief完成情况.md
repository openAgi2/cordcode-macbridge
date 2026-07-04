# 架构健康第二轮开发 — 完成情况

日期：2026-07-04（UTC 2026-07-04T03:28Z）
关联 brief：`docs/2026-07-04-architecture-health-second-round-development-brief.md`
Exec-Plan 状态：`.exec-plan/state/plan-f55b6e5f795e.json`
队列哈希（based_on_queue_hash）：`05d359b0b5d5e5b7d28a1e99b9007278fb59cadf5cdd7c77e2cd0ecb8a116d04`（与 state 文件 `reports.based_on_queue_hash` 一致；hash 覆盖 todos 全量序列化）

## 结论

16 个 required todo 全部 `done` 且 proven（`verification.status` = present，summary 与 artifacts 非空，`verification.required=true` × 16，无 `verification.required=false`）。`done=16 / proven=16 / blocked=0 / ready=0`。满足 "all required todos proven done" 条件，本正向完成报告由 exec-plan 自动生成条件触发。

独立审计（`docs/2026-07-04-architecture-health-second-round-completion-audit.md`）已逐项复核并 fresh 复跑 P0/P2/P1 低成本验证，结论通过；唯一 finding 是本段曾把 `verification.required` 误写为顶层 `required`，已修正。

退出审计（start 内部 exit audit）：JSON 结构核查 16/16 done+present、0 空 summary/artifacts/checked_at；可重跑的 tests/regression 已 fresh 重跑——共享包 typecheck+test（6 文件 20 测试）、P2 gate strict（无增长 exit 0 / 模拟增长 exit 1）、P1 `go build` + 全量 `go test ./go-bridge/...`（14.5s）全绿。

证据按 attestation 如实标注：`re-verified` = 本轮实际重跑命令；`self-attested` = 实施类证据（文件/符号变更）或不可重跑的回归（差异清单、视觉目测），由执行 agent 自证。

## 各阶段交付

按 brief 推荐顺序 P0 → P2 → P1 执行（不是章节顺序 P0 → P1 → P2）：先把 web 重复迁移收口，再锁 BridgeProvider 净增长基线，最后做 handlers.go 物理分发。

| 阶段 | 交付（机制建立 vs 行为已迁移） | 关键证据与验证命令 | attestation |
| --- | --- | --- | --- |
| **P0** web shared renderer 5/5（代码在相邻 iOS 仓） | 把剩余 3 个重复组件迁入 `shared-message-renderer`，**行为已迁移**到共享包；机制（host.labels / summarizers / transformContent 注入）建立 | `ReasoningBlock` 迁移前两 app diff=2（实测），迁移后仅剩 labels 值；`ProcessGroup` diff=43（实测，含分类/复数语义真实差异）；`NarrativeBlock` diff=68（实测，全部为 message-web 独有的 git directive feature）。三包 typecheck/build + 共享包 vitest 全绿；共享包新增 12 条定向 vitest；共享 exports 覆盖 5/5 | impl=self-attested；tests=re-verified；regression=self-attested（差异清单 + 三包构建；视觉 owner-pending） |
| **P2** BridgeProvider 净增长 gate（本仓，warning-only → required 试点） | `scripts/hygiene-baseline.json` 冻结基线 lines=1967/funcs=88/forTesting=36；`check-architecture-hygiene.sh` 增 `CORDCODE_HYGIENE_STRICT=1` 分支（**required gate**，仅此一规则）；CI macbridge job 接入 best-effort 跨仓 checkout + strict step；既有 5 个 inventory 段仍 **warning-only** | 5 场景实跑：default exit 0、strict 无增长 exit 0（"STRICT passed"）、strict 模拟净增（baseline 1960→1967）exit 1（"STRICT FAILED"）、iOS 缺失 exit 0、strict+iOS 缺失 exit 0（graceful skip，不破坏 CI）；退出审计 fresh 重跑两关键场景通过 | impl=self-attested；tests=re-verified；regression=self-attested（exit code 行为已 re-verified） |
| **P1** handlers.go 物理分发（本仓，**物理拆分非逻辑解耦**） | 4559 行 `handlers.go` 拆出 `handlers_opencode.go`（488，OpenCode proxy 簇 15 func）+ `handlers_relay.go`（829，relay 簇 17 func 含 4 个 transcript 探测 helper 整组搬迁），`handlers.go` → 3269 行（-1290，-28%）。**未改函数体 / RPC 行为 / session registry / agent driver / protocol 字面契约；未引入新 abstraction 层** | `go build ./go-bridge` ok；定向过滤 `go test ./go-bridge -run 'Test.*Session\|Test.*Message\|Test.*Backend\|Test.*Capability\|Test.*Pagination'` ok 1.9s；全量 `go test ./go-bridge/... -count=1` ok 14.5s（codex CLI 0.142.5 加 PATH）。sanity：ocHandle handlers.go=0/opencode=12；4 transcript helper 全在 handlers_relay.go；3 文件同 `package gobridge` | impl=self-attested；tests=re-verified；regression=re-verified |
| **docs** CHANGELOG | `CHANGELOG.md [Unreleased]` 追加第二轮跨仓架构治理进度条目 | 条目存在、格式遵循现有惯例 | self-attested |

## 为什么这样做（取舍）

- **P0 文案/逻辑差异一律归类保留，不强行收敛**：`ReasoningBlock` 的中/英、`ProcessGroup` 的分类粒度与复数语义、`NarrativeBlock` 的 git directive，均判定为真实产品差异，通过 host 注入（labels / summarizers / transformContent）保留。收敛任一侧都会改变另一 app 行为，超出"纯迁移"边界。意外漂移（如 remote-web `NarrativeBlock` 既有中文字面量）不顺手改——改了会变更 `openDetail` payload，属行为/契约变更，记为 follow-up。
- **P0 共享包默认策略偏向 message-web（中文）**：`ProcessGroup` 默认摘要 = message-web 全分类中文实现；`NarrativeBlock` 默认 transformContent 无（原样渲染）。remote-web 通过 `summarizers` / `transformContent` 注入自己的策略。两 app 输出逐字不变。
- **P0 react-markdown 跨版本分歧（9 vs 10）经实测化解，未阻塞**：共享包加 react-markdown peer `>=9 <11`，9.x（message-web/shared）与 10.x（remote-web）跨大版本 typecheck/build 实测通过——`components` 覆写 API（a/input/li）在两版本稳定。未做穷尽 runtime 测试。
- **P2 gate 只针对 BridgeProvider 三指标净增，不一次性把所有 warning 变 fail**：避免存量债务把 CI 永久打红。gate 缺 iOS 仓时 graceful skip（不破坏 CI），CI strict 实际生效条件 = `cordcode-ios` 对 runner `GITHUB_TOKEN` 可读。
- **P1 只做两块最高凝聚度的物理拆分（OpenCode + relay）**：满足 brief "明显变薄 + 多个同 package 文件" 标准；sessions/messages/agents/files 等更细分文件未拆（可选 follow-up，但当前 -28% 已显著降低导航成本）。
- **本轮明确不做**：BridgeProvider 本体拆分、ChatViewModel 大方法拆分、Mac↔iOS 状态模型统一、protocol/capability 新能力——风险与验证成本高于本轮目标，留给第三轮。

## 验证命令与结果（本仓 vs 相邻 iOS 仓）

**相邻 iOS 仓 `../cordcode-ios/` 验证（P0）**：
- `cd shared-message-renderer && npm run typecheck && npm run test` → 6 文件 20 测试全过（含新增 ReasoningBlock 4 / ProcessGroup 4 / NarrativeBlock 4 characterization）
- `cd message-web && npm run typecheck && npm run build` → ok（305→307 模块）
- `cd remote-web && npm run typecheck && npm run build` → ok（341→343 模块）
- 差异计数法（brief 要求）：`diff -u <msg> <remote> | grep '^[+-]' | grep -v '^[+-][+-]' | wc -l` = 2 / 43 / 68（迁移前），迁移后两 app wrapper 仅剩注入值差异

**本仓验证（P2 / P1）**：
- P2：`./scripts/check-architecture-hygiene.sh`（default exit 0）；`CORDCODE_HYGIENE_STRICT=1 ...`（无增长 exit 0 / 模拟增长 exit 1）；`CORDCODE_IOS_ROOT=/nonexistent ...`（graceful skip exit 0）
- P1：`go build ./go-bridge` ok；`go test ./go-bridge -run 'Test.*Session|Test.*Message|Test.*Backend|Test.*Capability|Test.*Pagination' -count=1` ok 1.9s；`go test ./go-bridge/... -count=1` ok 14.5s（前置：PATH 含 `/Applications/Codex.app/Contents/Resources` 供 codex-cli 0.142.5）

**诚实区分**：
- 机制建立 vs 行为已迁移：P0 行为已迁移到共享包 + 注入机制建立；P2 机制（baseline + strict 分支 + CI 接入）建立、未拆 BridgeProvider 本体；P1 物理拆分不是逻辑解耦。
- warning-only vs required gate：P2 只有 BridgeProvider 净增长一规则是 required（strict），既有 5 段 inventory 仍 warning-only。
- owner-verified vs command-verified：P0/P2/P1 的构建/测试为 command-verified（re-verified）；P0 组件渲染视觉/UX 为 **owner-pending**（未经 owner 明确授权未跑 UI/snapshot/simulator/真机）。
- 本仓验证 vs iOS 仓验证：P0 代码与 typecheck/build/vitest 发生在 iOS 仓；P2 gate 与 P1 拆分发生在 MacBridge 仓；CHANGELOG 在 MacBridge 仓记录跨仓进度。

## 追加装机验证（UTC 2026-07-04T04:00Z，owner 授权后 agent-verified）

owner 明确要求重装两端后，本轮在原 brief "未授权真机" 边界外做了 Mac Release 重建+覆盖安装与 iOS 真机重装（仅构建/安装/启动，未做 UI automation / snapshot / 视觉判定）。

- **Mac Release 重建 + /Applications 覆盖**：`./scripts/build-unsigned-release.sh` → `** BUILD SUCCEEDED **`，dist zip + .app 产物；内嵌 `cordcode-bridge-runtime 0.1.0 (built 2026-07-04T03:59:47Z)`，源自我工作树（含 P1 handlers 拆分）。`killall` 旧 App（PID 48616 / runtime 29695）→ `rm -rf /Applications/CordCodeLink.app` → `cp -R` 新 .app → `open`。新 runtime PID 92480 在 8777 监听（`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，非源码目录开发二进制），bridgeEpoch `1783137655810-92480`；启动日志干净（claude/opencode/codex registered、opencode SSE connected、relay connected、`runtime_ready`），**自新 runtime 启动以来零 ERROR/WARN**。
- **iOS build:ios + 真机重装**：`cd ../cordcode-ios/message-web && npm run build:ios` 把 P0 message-web 改动刷进 `OpenCodeiOS/.../Resources/MessageWeb/`（307 模块，built 637ms）；`scripts/run.sh device --device BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`（iPhone 16 Pro，paired/available）→ Debug 构建成功（`/tmp/cordcode-realdevice/.../CordCode.app`）+ `xcrun devicectl` 安装 + 启动 `org.openagi.cordcode` 全部成功（"全部完成 🎉"）。

agent-verified 边界：仅构建/安装/启动成功；**UI 渲染目测仍 owner-pending**（agent 无法看屏）。

## 未覆盖风险

- **P0 视觉/UX 完整性回归仍 owner-pending**：三组件迁移的 DOM 契约、className、test id 未改（characterization 测试守护）。两端 App 已重装到含 P0 改动的构建（见上"追加装机验证"，agent-verified 构建安装启动），但真机/浏览器的实际渲染目测未经 owner 验收。建议 owner 在 iPhone 与 web 客户端各目测一次 reasoning / process-group / narrative + git directive 渲染。
- **react-markdown 跨版本兼容经 typecheck/build 实测，非穷尽 runtime 验证**：9.x 与 10.x 的 `components` 覆写 API 当前稳定，但若 react-markdown 后续 minor 引入 9/10 差异的运行时行为，共享源码可能需要适配。共享包 peer 范围 `>=9 <11` 已锁定可接受范围。
- **P2 CI strict 实际执法已确认**：`openAgi2/cordcode-ios` 经 `git ls-remote` 无 auth 可读（HEAD=`98c780929…`），是公开仓；CI macbridge job 的跨仓 checkout 会成功（无需额外 token），strict gate 在每个 PR / push to main 上实际执法。checkout 步骤的 `continue-on-error` 仅作 belt-and-suspenders。本地（双仓并存的开发机）同样 strict 生效。
- **P1 sessions/messages/agents/files 等更细分文件未拆**：当前 -28% 已显著降低导航成本，但 handlers.go 仍 3269 行，离理想还远。这是有意为之（本轮只做最高凝聚度两块），不是遗漏。
- **remote-web `NarrativeBlock` 既有中文字面量（`思考过程`/`行思考`/`收起展开`）未收敛**：remote-web 本地化不全（`label` 是英文但其余仍是中文），本次保持原行为，记为独立本地化 follow-up。

## 下一轮应接手的具体入口

brief 第 4 节给出的第三轮触发条件，本轮已满足其中三条：
1. ✅ web shared renderer 5/5 目标组件迁移完成
2. ✅ BridgeProvider 净增长 gate 已在 strict 模式下可跑
3. ✅ handlers.go 已完成物理分发，MacBridge 侧 handler 导航成本下降
4. ⬜ 为 BridgeProvider 选定一个子域，并列出该子域的可执行不变量测试（第三轮启动前需做）

建议第三轮第一个子域（按 brief）：connection strategy / transport creation / recovery ownership / session-history synchronization / capability-backend descriptor mapping。**一次只拆一个子域**，extract-and-test，每次抽取必须有行为不变量测试守护（不要等所有 characterization 测试完美后才开始；边抽边补）。

具体入口：
- **BridgeProvider 拆分**：`../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`（当前 1967 行 / 88 func / 36 ForTesting，已被 P2 strict gate 冻结基线）。拆分子域后下调 `scripts/hygiene-baseline.json` 对应指标并在 PR 说明拆分入口。
- **handlers.go 进一步细分**（可选）：`handlers.go`（3269 行）可继续按 sessions / messages / agents / files 抽出，参照本轮 opencode/relay 的 goimports 流程。
- **remote-web 本地化收尾**（可选）：`remote-web/.../NarrativeBlock` 中文字面量与 `ProcessGroup` 摘要的英文策略统一。

## 诚实边界

- 本报告由执行 agent（同一 session 完成 16 todo）撰写；`self-attested` 行不应被读作独立验证。`re-verified` 行为本 session 实跑命令的结果。
- 本轮未做独立第三方审计（第一轮有 `docs/2026-07-04-architecture-health-completion-audit.md`）；如需更强置信，owner 可在第三轮启动前指定独立 agent 重跑本报告"验证命令与结果"节。
- iOS 仓改动（P0）需在 `../cordcode-ios/` 单独 commit；本仓 CHANGELOG 仅记录跨仓治理进度，不代表 iOS 仓已合并。
