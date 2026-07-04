# 架构健康目标 — 第二轮交付差距分析

日期：2026-07-04
原始目标：`docs/2026-07-03-architecture-health-assessment.md`（评估报告第六节"改进方向"）
第一轮差距分析：`docs/2026-07-04-architecture-health-goal-gap-analysis.md`
第二轮执行：`docs/2026-07-04-architecture-health-second-round-development-brief.md`（P0/P1/P2 三阶段）
第二轮完成情况：`docs/2026-07-04-architecture-health-second-round-development-brief完成情况.md`
第二轮独立审计：`docs/2026-07-04-architecture-health-second-round-completion-audit.md`
本文定位：独立 agent 对第二轮完成情况逐项 fresh 复跑核实，并对照评估原始目标 + 第一轮差距分析的建议（S1-S5）量化最新差距。**仅讨论，不改代码。**
计数/行数均为本轮实测（2026-07-04 fresh），不引自完成报告。

---

## TL;DR

**第二轮精准命中第一轮差距分析建议的 S1+S2+S5，完成度高于第一轮；评估动作 3（拆 god-object）的本体仍未开始，是第三轮的核心缺口。**

- **第一轮差距分析建议的 S1（web 共享包剩余 3 个）**：✅ 完成
- **S2（拆 handlers.go 物理分发）**：✅ 完成（-28%）
- **S5（CI gate 升级试点）**：✅ 完成（BridgeProvider 净增长一条 strict）
- **S3（BridgeProvider 拆分）/ S4（ChatViewModel 拆分）**：❌ 本体未动（正确推迟，因测试保护不够）

按评估自定标杆："做完 1-2 → 高质量单人/小团队系统软件档"（第一轮已达）。**第二轮把动作 4 完全收口（web 共享包 5/5）、动作 3 的低风险子集（handlers.go）做了、动作 5 的首条 gate 升级了**——但离"做完 3-5 → 可维护性接近产品级"仍差**动作 3 的本体（iOS god-object 实际拆分）+ 动作 5 的剩余 gate 升级**。

---

## 一、第二轮声称核实（fresh 复跑）

### exec-plan state 与统计声明

| 完成报告声称 | 本轮核实 | 结果 |
|---|---|---|
| 16 todo 全 `done` | `len(todos)=16`，status 全 done | ✅ |
| 全部 proven（`verification.status`=present） | present ×16 | ✅ |
| `verification.required=True` ×16 | True ×16，False ×0 | ✅ |
| 无空 summary/artifacts/checked_at | 0 处空 | ✅ |
| `based_on_queue_hash` = `05d359b0...` | state 内 hash 与报告字面逐字相同 | ✅ |
| `completion_report_status: current` | 确认 | ✅ |

### P0 web 共享包迁移（iOS 仓）

| 完成报告声称 | 本轮 fresh 复跑 | 结果 |
|---|---|---|
| 5/5 组件迁入 shared 包 | ToolBlock/DiffViewer/ReasoningBlock/ProcessGroup/NarrativeBlock 全部在 `shared-message-renderer/src/` | ✅ |
| 共享包 6 文件 20 测试 | `npm test` → 6 文件 20 测试全过（ReasoningBlock 4 / ProcessGroup 4 / NarrativeBlock 4 新增 characterization） | ✅ |
| message-web build ok（307 模块） | 未复跑 build（耗时），引用报告 + 测试通过间接支撑 | 🟡 未独立复跑 |
| remote-web build ok（343 模块） | 同上 | 🟡 未独立复跑 |
| 迁移后两 app wrapper "仅剩注入值差异" | **见下方"措辞精度"分析** | ⚠️ 技术成立但易误读 |

### P1 handlers.go 物理分发（本仓）

| 完成报告声称 | 本轮 fresh 复跑 | 结果 |
|---|---|---|
| handlers.go 4559→3269（-28%） | 实测 3269 行 | ✅ |
| 拆出 handlers_opencode.go（488，15 func） | 实测 488 行 / `grep -c '^func'` = 15 | ✅ |
| 拆出 handlers_relay.go（829，17 func） | 实测 829 行 / `grep -c '^func'` = 17 | ✅ |
| 同 package gobridge | 三文件均 `package gobridge` | ✅ |
| ocHandle 全在 opencode 文件 | handlers.go 0 处 / opencode 19 处（含调用） | ✅ |
| `go build ./go-bridge` ok | 本轮复跑 exit=0 | ✅ |
| 全量 `go test ./go-bridge/...` ok | 本轮复跑（runtime 等价 PATH）14.882s ok | ✅ |

### P2 BridgeProvider 净增长 gate（本仓）

| 完成报告声称 | 本轮 fresh 复跑 | 结果 |
|---|---|---|
| baseline lines=1967/funcs=88/forTesting=36 | `hygiene-baseline.json` 三值一致 | ✅ |
| strict 分支存在 | `CORDCODE_HYGIENE_STRICT=1` 触发 strict 路径 | ✅ |
| 场景 1：default exit 0 | exit=0 | ✅ |
| 场景 2：strict 无增长 exit 0 + "STRICT passed" | exit=0，输出 "STRICT passed — no BridgeProvider net growth" | ✅ |
| 场景 3：strict 净增 exit 1 + "STRICT FAILED" | 临时下调 baseline 模拟净增 → exit=1，输出 "STRICT FAILED" + "❌ lines net growth" | ✅ |
| 场景 4：iOS 缺失 graceful skip | exit=0，输出 "warning-only (gate_ran=0, strict=1)" | ✅ |
| CI macbridge job 接入 strict step | `.github/workflows/ci.yml:63-64` 确认 `CORDCODE_HYGIENE_STRICT=1` + 跨仓 checkout | ✅ |

### 追加装机验证

完成报告 56-63 行的 Mac/iOS 装机证据为 agent-verified，本轮未复跑装机（需 owner 授权真机），引用报告自述。Mac runtime PID/bridgeEpoch/端口等可从运行态核实的项，与第一轮审计一样可复验，但本轮未单独跑 `lsof`/`runtime.json`——属"前序 agent-verified + 报告诚实标注"范畴。

追加 owner 目测（2026-07-04，iPhone 16 Pro）后，message-web 真机视觉回归已通过：reasoning / process-group / narrative 正常，`Git 操作` 卡片在既有 session 中出现。remote-web（web 客户端）未单独目测，仍靠共享组件同源、薄 wrapper、typecheck/build 间接支撑。

---

## 二、措辞精度问题（非虚报，但需澄清）

### 问题 1：P0 "迁移后两 app wrapper 仅剩注入值差异"容易被误读

完成报告 P0 regression 行写"迁移后两 app wrapper 仅剩注入值差异"。**实测 diff 行数**：

| 组件 | 第一轮快照 diff | 第二轮迁移后 diff | 趋势 |
|---|---|---|---|
| ReasoningBlock | 4 行 | 2 行 | ↓ 缩小 |
| ProcessGroup | 68 行 | **104 行** | ↑ **增大** |
| NarrativeBlock | 75 行 | **80 行** | ↑ 略增 |

表面看，ProcessGroup/NarrativeBlock 的 diff **没缩小反而增大了**，与"漂移止住"的直觉相悖。但深入核实 diff 内容后，这是**正确的迁移结果被措辞掩盖**：

- **迁移前**：两边各有独立的重复实现（ProcessGroup 68 行 diff 是"两套相似但不同的实现"）
- **迁移后**：
  - message-web ProcessGroup = **4 行纯 re-export**（`export { ProcessGroupComponent } from '@cordcode/shared-message-renderer'`，用默认中文策略）
  - remote-web ProcessGroup = **100 行**（import 共享组件 + 注入自己的英文 summarizer 策略）

即**差异性质变了**：从"重复实现的漂移"变成"被显式建模的注入策略差异"。这正是评估动作 4 要的"止住漂移源"——重复实现已收敛到共享包，剩余的是 message-web（中文/全分类）与 remote-web（英文/精简分类）的**真实产品差异**，通过 `summarizers`/`labels`/`transformContent` 注入保留。

**结论**：这不是虚报。完成报告第 31 行"取舍"小节其实已诚实说明"ProcessGroup 的分类粒度与复数语义、NarrativeBlock 的 git directive，均判定为真实产品差异，通过 host 注入保留"。但表格 P0 行的简短措辞"仅剩注入值差异"容易被读者理解为"diff 行数缩小"，**建议第三轮文档把"diff 行数变化"与"差异性质变化"分开表述**。

### 问题 2：forTesting 计数 36 vs 34（非 bug，已诚实记录）

第一轮差距分析用 `grep -co 'ForTesting'`（按行计）= 34；第二轮 baseline.json 用 `grep -o 'ForTesting' | wc -l`（按出现计）= 36。差异源于一行内可能有多个 `ForTesting`。baseline.json 的 `_comment` 已明确记录计数法和"r3 评审曾记 34，本轮用 grep -o 实测为 36（计数法差异）"，**脚本与 baseline 用同一计数法，自洽，无 bug**。

---

## 三、对照评估原始目标（5 动作 + 3 主线）

### 评估的 5 个优先级动作

| # | 评估动作 | 第一轮后状态 | 第二轮后状态 | 完成度变化 |
|---|---|---|---|---|
| 1 | 杀双写 | ✅ 完成 | ✅ 完成 | 维持 |
| 2 | 删 config 死重 | ✅ 完成 | ✅ 完成 | 维持 |
| 3 | 拆 god-object | ❌ 仅 D characterization 测试 | 🟡 **handlers.go -28%（MacBridge 低风险子集）+ BridgeProvider gate 冻结** | **部分推进** |
| 4 | 抽 web 共享包 | 🟡 机制 + 2/5 | ✅ **5/5 完成** | **完全收口** |
| 5 | 工程宪法 + CI 卡 | 🟡 宪法 + warning-only | 🟡 **+1 条 strict gate（BridgeProvider 净增长）** | **试点推进** |

**第二轮最大的进展**：动作 4 从 2/5 推进到 5/5，**完全收口**——这是第一轮差距分析里"性价比最高、机制已就绪、风险最低"的 S1 建议，现已达成。

### 动作 3（拆 god-object）的精确进展

这是评估的核心目标，也是最易被"16/16 done"掩盖的部分。本轮实测各 god-object：

| 目标 | 评估快照 | 第一轮后 | 第二轮后 | 是否拆分 |
|---|---|---|---|---|
| `go-bridge/handlers.go` | 4604 行 | 4559 行 | **3269 行（-28%）** | ✅ 第二轮物理拆出 opencode+relay |
| `agent/claudecode/claudecode.go` | 1908 行 | 1908 行 | 1908 行 | ❌ 零变化 |
| `agent/codex/appserver_session.go` | 1805 行 | 1805 行 | 1805 行 | ❌ 零变化 |
| iOS `BridgeProvider.swift` | 1967 行/78 func | 1967 行/88 func | **1967 行/88 func/36 ForTesting（冻结）** | ❌ 本体零变化，但 gate 已止恶化 |
| iOS `ChatViewModel+Generation.swift` | sendMessage 579 行 | 2270 行 | 2270 行 | ❌ 零变化 |
| iOS `ChatViewModel+CodexStreaming.swift` | handleCodexLiveEvent 563 行 | 1426 行 | 1426 行 | ❌ 零变化 |
| iOS `ChatViewModel+SessionManagement.swift` | switchSession 500 行 | 1021 行 | 1021 行 | ❌ 零变化 |
| iOS `ChatUIKitContainerView.swift` | 4371 行 | 4371 行 | 4371 行 | ❌ 零变化 |

**关键判断**：
- handlers.go 是 MacBridge 侧 god-object 中**唯一被实际拆分的**（-1290 行，拆成 3 文件）
- BridgeProvider 本体一行没拆，但 P2 gate 把它**冻结在 1967/88/36**——这是"止恶化"的正确前置，为第三轮 extract-and-test 铺路
- 其余 5 个 god-object（claudecode.go/appserver_session.go/3 个 ChatViewModel/ChatUIKitContainerView）**零变化**

### 评估的 3 条治理主线

| 主线 | 第一轮后 | 第二轮后 | 差距 |
|---|---|---|---|
| 架构收敛（legacy 清单） | config 删了 | 无新进展 | **全仓 legacy 清单仍未建立**（悬空 symlink、legacy launchctl plist、64667 兼容码等仍在） |
| 状态模型收敛（Mac↔iOS） | 未碰 | 未碰 | **两轮都完全没碰**，跨仓库主题 |
| 质量门禁收敛 | hygiene reminder | **BridgeProvider 净增长 1 条 strict** | 仅 1 条 strict，其余 5 段 inventory 仍 warning-only |

---

## 四、对照第一轮差距分析建议（S1-S5）

第一轮差距分析给出了第二轮建议 S1-S5，现在回看达成情况：

| 建议 | 内容 | 第二轮达成 | 说明 |
|---|---|---|---|
| **S1** | 完成 web 共享包剩余 3 个组件 | ✅ 完成 | 5/5 全迁，characterization 测试守护 |
| **S2** | 拆 handlers.go 物理分发 | ✅ 完成 | -28%，opencode+relay 两块最高凝聚度 |
| **S3** | BridgeProvider 拆分 | ❌ 未做 | **正确推迟**——前置条件（按职责切片的测试）未满足 |
| **S4** | ChatViewModel 巨型方法拆分 | ❌ 未做 | **正确推迟**——风险最高，需 snapshot/merge + streaming 不变量测试 |
| **S5** | CI gate 从 warning 渐进升级 | ✅ 完成 | BridgeProvider 净增长 1 条 strict，试点闭环跑通 |

**第二轮的执行精度高**：没有硬冲 S3/S4（评估和 gap analysis 都说"需配套测试保护后再动"），而是把 S1/S2/S5 这三条"机制已就绪、风险低"的做完。完成报告"本轮明确不做"小节也诚实列出了推迟项。

---

## 五、第三轮的核心差距与建议

### 第三轮的硬缺口（无法继续推迟的）

**G1. BridgeProvider 本体拆分**（iOS 侧最大 god-object，动作 3 核心）
- 1967 行 / 88 func / 36 ForTesting，已被 gate 冻结不能长
- **但拆分前置条件仍未满足**：D 的 characterization 测试只覆盖"连接策略矩阵 + 生成周期边界"两个切片，不够当拆分保护网
- 第三轮启动前**必须先做**完成报告第 79 行列的触发条件 4："为 BridgeProvider 选定一个子域，并列出该子域的可执行不变量测试"
- 建议子域选择（按 brief）：connection strategy / transport creation / recovery ownership（这三个相对独立，extract-and-test 风险可控）

**G2. handlers.go 继续细分**（可选，MacBridge 侧）
- 第二轮只拆了 opencode + relay 两块（-28%），3269 行离理想还远
- sessions / messages / agents / files 等簇可继续按第二轮的 goimports 流程拆
- 风险低、收益递减，可作为第三轮的"低强度持续清理"

### 第三轮的中期缺口（可与 G1 并行）

**G3. CI gate 从 1 条扩展到多条**
- 目前只 BridgeProvider 净增长一条 strict，5 段 inventory（NSLog/print/Logger/CJK/Go 日志）仍 warning-only
- 第二轮已跑通"立法→执法"闭环（baseline + strict + CI 接入 + graceful skip），下一条 gate 的工程接入成本低；但每条 strict 仍需逐条确认债务能否归零、是否需要例外、失败提示是否会误伤 CI
- 建议下一条升级候选：选存量债务最容易归零且例外最少的一条（如 `print` 调试日志清理），先归零再升级

**G4. BridgeProvider 拆分后的 gate 指标下调机制验证**
- baseline.json `_comment` 已写明"拆出独立职责文件后下调对应指标"
- 第三轮第一次实际拆分时，要验证"拆分 → 下调 baseline → CI 仍绿"的闭环真的能跑通

### 第三轮的远期缺口（建议单独立项）

**G5. 状态模型收敛**（评估治理主线 2，两轮完全未碰）
- Mac↔iOS 的连接态/session/turn/history polling/Relay reconnect/capability 状态机
- 需要先形成状态机文档 + 不变量测试，周期长

**G6. ChatViewModel 巨型方法拆分**（风险最高）
- sendMessage(579)/handleCodexLiveEvent(563)/switchSession(500)
- 需先有 snapshot/merge 不变量测试 + streaming 状态机测试
- 不建议在第三轮优先，除非有专门测试加固期

**G7. MacBridge 侧另外两个 god-object**（claudecode.go 1908 / appserver_session.go 1805）
- 评估列为 god-object，但本轮两轮都没碰
- 优先级低于 BridgeProvider（iOS 侧）和 handlers.go（已动）

### P0 视觉回归（message-web 已通过，remote-web 可选补测）

完成报告"未覆盖风险"已更新：P0 三组件迁移的 DOM 契约/className/test id 未改（characterization 守护），iPhone message-web 目测已由 owner 确认通过。remote-web（web 客户端）未单独目测；因为共享组件同源、宿主只注入英文 labels / summarizers 且无 `Git 操作` 卡片，这是非阻塞补测项，不应再作为第三轮启动前硬门槛。

- 已通过：iPhone message-web reasoning / process-group / narrative + git directive 渲染
- 可选补测：Web 客户端 reasoning / process-group / narrative（英文 labels，预期无 `Git 操作` 卡片）

---

## 六、对"完成度"的精确措辞（供对外引用）

**两轮累计，对照评估原始 5 动作的完成度**：
- 动作 1（杀双写）：✅ **100%**
- 动作 2（删 config）：✅ **100%**
- 动作 3（拆 god-object）：🟡 **~20%** — handlers.go 拆了 1/4 个 god-object（MacBridge 侧唯一进展），iOS 侧 4 个 god-object 本体零拆分（BridgeProvider 已冻结止恶化）
- 动作 4（web 共享包）：✅ **100%** — 5/5 迁移完成，机制 + 迁移都到位
- 动作 5（工程宪法 + CI 卡）：🟡 **~30%** — 宪法 + 脚本就绪，1 条 strict gate 试点，5 段 inventory 仍 warning-only

**对照评估治理主线**：
- 主线 1（架构收敛）：🟡 局部（config 删了，无全仓 legacy 清单）
- 主线 2（状态模型收敛）：❌ 未碰
- 主线 3（质量门禁收敛）：🟡 1 条 strict，其余 warning

**净判断**：
- 第二轮执行精度高、范围诚实（明确推迟 S3/S4），完成度高于第一轮
- 评估动作 1/2/4 已完全收口（3/5 动作 100%）
- **最大缺口仍是动作 3 本体**——iOS god-object 实际拆分，第三轮必须启动且需先补按职责切片的测试
- 离评估"做完 3-5 → 可维护性接近产品级"标杆，还差**动作 3 本体（G1）+ 动作 5 剩余 gate 升级（G3）+ 治理主线 2 状态模型（G5）**

---

## 七、审计边界

- 本审计 **fresh 复跑**：P1（build + 全量 test）、P2（5 场景 strict gate）、P0（共享包 vitest 6 文件 20 测试）、exec-plan state 结构与 hash
- 本审计 **未复跑**：P0 的 message-web/remote-web build（耗时，引用报告 + 测试通过间接支撑）、装机验证（需 owner 授权真机）、CHANGELOG 内容审查（条目存在性已确认）
- 本审计完成后追加纳入：owner 提供的 iPhone message-web 目测截图，覆盖 reasoning / process-group / narrative + `Git 操作` 卡片；remote-web 仍未单独目测
- 本审计 **未做**：独立第三方审计（完成报告第 91 行已提示 owner 可指定）；iOS 仓 commit 状态核查（iOS 改动需在 `../cordcode-ios/` 单独 commit，本审计未查 iOS 仓 git 状态）
- 本审计 **未改变**任何运行链路或代码
