# 架构健康目标 — 第一轮交付差距分析

日期：2026-07-04
原始目标：`docs/2026-07-03-architecture-health-assessment.md`（评估报告第六节"改进方向"）
第一轮执行：`docs/2026-07-03-architecture-health-execution-plan.md`（A/B/C/D/E 五批次）
第一轮完成情况：`docs/2026-07-03-architecture-health-execution-plan完成情况.md`
第一轮独立审计：`docs/2026-07-04-architecture-health-completion-audit.md`
本文定位：对照"评估报告的改进目标"与"第一轮实际产出"，逐条量化差距，给出第二轮的优先级建议。**仅讨论，不改代码。**
行数/计数为本轮实测（2026-07-04），非评估快照；评估快照值在括号内标注，便于看漂移。

---

## TL;DR

**第一轮精准命中评估的"第一波"（动作 1-2），完全达成；"第二波"（动作 3-5）只完成前置准备与部分迁移，本体未做。**

按评估自定的两波标杆：
- "做完 1-2 → 高质量单人/小团队系统软件档" ✅ **已到达**
- "做完 3-5 → 可维护性接近产品级" ❌ **未到达**

"28/28 proven done" 是指**执行计划自身拆出的 todo 全完成**，不等于"评估报告的所有改进目标全完成"。第一轮的范围诚实（计划名就叫"第一轮收口"），没有 overclaim；但若以评估报告为完整目标集，**还差一个完整的第二波**。

---

## 一、目标 ↔ 交付对照表

评估第六节列了 5 个优先级动作 + 3 条治理主线，并自己分了两波："第一波（杀双写、切 config）… 适合先做；第二波（拆 god-object、抽 web 共享包）… 需配套测试保护后再动"。执行计划严格按这个分波设计。

| # | 评估目标动作 | 第一轮批次 | 完成度 | 本轮实测证据 |
|---|---|---|---|---|
| 1 | **杀双写**：BackendList/deriveCapabilities 收敛单源 + 修 question_reply | A | ✅ **完全达成** | `deriveBackendCapabilities` 单源，被 `handlers.go:361` + `agent_descriptor.go:112` 共用；codex app_server 一致下发 compression+question_reply |
| 2 | **切 config 死重**：抽 ~200 行进 go-bridge，删 Weixin/Feishu | B1+B2+B2-predelete | ✅ **完全达成** | `config/` 整包删除（6418 行下线）；生产 import 清零；Feishu/Weixin 符号 0 命中 |
| 3 | **拆 god-object**：BridgeProvider/ChatViewModel/ChatUIKitContainerView/handlers.go | D | ❌ **未拆，只铺保护网** | 仅新增 `GodObjectCharacterizationTests.swift`（characterization 测试）；4 个 god-object 行数本轮复测**零变化**（见下节） |
| 4 | **抽 web 共享包**：止住 message-web/remote-web 漂移 | C1+C2+C3 | 🟡 **机制建立，迁移 2/5** | `shared-message-renderer` 包 + host adapter 就绪；仅 DiffViewer+ToolBlock 迁入；ReasoningBlock/ProcessGroup/NarrativeBlock 未迁 |
| 5 | **立工程宪法 + CI 卡** | E | 🟡 **宪法有了，CI 卡没立** | `engineering-constitution.md` + hygiene 脚本存在，但脚本 warning-only，明确"不阻塞 CI" |

---

## 二、逐条差距讨论

### ✅ 动作 1-2（第一波）：完全达成，证据扎实

这两条是评估里"低成本/高收益/不动 UI 与大状态机"的精准命中：
- **杀双写**止住了 bug 工厂——question_reply 在 BackendList 与 hello_ack 之间不一致已修，capability 宣告单源化。
- **删 config**清掉了评估口中的"最显眼屎山信号"——6418 行死重下线，生产路径不再携带 Weixin/Feishu/Cron/Webhook/TTS 等无关业务结构。

按评估自定标杆——"做完 1-2，两个仓库的架构健康度能稳稳进**高质量单人/小团队系统软件**档"——**这个档位已达到**。这部分没有差距。

### ❌ 动作 3（拆 god-object）：只做了前置安全网，没动本体（**最大缺口**）

这是最需要说清的差距。评估要的是"按职责拆"，D 批次交付的是 characterization 测试（固化当前行为，为后续安全重构铺网），**不是重构本身**。

本轮实测 4 个 god-object，行数与评估快照（2026-07-03）对比如下：

| 目标 | 本轮实测 | 评估快照 | 漂移 |
|---|---|---|---|
| `go-bridge/handlers.go` | 4559 行 | 4604 行 | -45（小幅，非刻意拆分） |
| `agent/claudecode/claudecode.go` | 1908 行 | 1908 行 | 0 |
| `agent/codex/appserver_session.go` | 1805 行 | 1805 行 | 0 |
| iOS `BridgeProvider.swift` | 1967 行 / 88 func / 34 ForTesting | 1967 行 / 78 方法 / 34 ForTesting | 行数 0；func 数 +10（说明期间还在往里加方法，没有拆分的迹象） |
| iOS `ChatViewModel+Generation.swift` | 2270 行（含 sendMessage 巨型方法） | sendMessage 单方法 ≈579 行 | 文件继续增长 |
| iOS `ChatViewModel+CodexStreaming.swift` | 1426 行（含 handleCodexLiveEvent） | handleCodexLiveEvent ≈563 行 | 文件继续增长 |
| iOS `ChatViewModel+SessionManagement.swift` | 1021 行（含 switchSession） | switchSession ≈500 行 | 文件继续增长 |
| iOS `ChatUIKitContainerView.swift` | 4371 行 | 4371 行 | 0 |

**关键判断**：func 数从 78→88 说明 BridgeProvider 在第一轮期间**继续承担新职责**，god-object 不仅没拆，还在长肉。D 的 characterization 测试覆盖的是"连接策略矩阵 + 生成周期边界"两个切片，**不是 god-object 拆分的回归保护网**——它保护的是"别在拆之前先把当前行为漂移了"，拆分本身需要更细的、按职责切片的测试。

但这**不算计划失误**：评估明确说第二波"需配套测试保护后再动"，D 是那个前置保护。只是必须明确：**动作 3 的本体（拆）尚未开始。**

### 🟡 动作 4（web 共享包）：机制对了，但只迁了最简单的 2/5

C1 做对的事：建立了 `shared-message-renderer` 包 + host adapter 隔离机制，让 iOS WebKit 与 remote-web 两个宿主通过 adapter 注入差异。这是评估要的"止住漂移源"的正确机制。

但实际迁移只做了 DiffViewer + ToolBlock——**恰恰是评估里说的"字节全等"那两个**。本轮复测三个未迁组件的漂移量：

| 组件 | 评估快照 diff | 本轮实测 diff | 漂移趋势 |
|---|---|---|---|
| ReasoningBlock | 2 行 | 4 行 | ↑ 在扩大 |
| ProcessGroup | 43 行 | 68 行 | ↑ 在扩大 |
| NarrativeBlock | 68 行 | 75 行 | ↑ 在扩大 |

**风险点**：字节全等的迁了，已经漂移且漂移在扩大的反而没迁。这等于把"最容易证明共享包有用"的做了，把"最需要共享包来收敛分歧"的留下了。三个未迁组件的 diff 都比快照时更大，说明**第一轮期间它们又各自演化了一轮**——正是评估预警的"漂移几乎必然"。机制已经能承载后续迁移，成本主要是 resolve 已有 diff，但越拖 resolve 成本越高。

### 🟡 动作 5（工程宪法 + CI 卡）：立了法，没执法

E 交付了宪法文档 + hygiene 脚本，但脚本设计就是 warning-only（宪法原则 4 自述："新增工程规则先 warning-only；只有存量债务归零…才能改成 required gate"）。

这是**审慎的渐进式治理**，不是逃避——在日志/本地化/并发原语/类行数的存量债务远未归零时，直接立 CI 硬卡会造成 CI 持续红，反而逼人绕过。但评估要的是"CI 卡"，目前**没有硬门禁**。从 warning 升级到 required 的路径是清晰的（先收敛存量 → 再改 gate），但收敛存量的工作本轮没做。

---

## 三、3 条治理主线：只碰了第 1 条的边

评估的"治理主线"比 5 个动作更高维，本轮覆盖情况：

| 主线 | 第一轮覆盖 | 差距 |
|---|---|---|
| **架构收敛**（列 legacy 清单，明确保留/迁移/删除） | config 删了 | **没有全仓 legacy 清单**：悬空 `/opt/homebrew/bin/codex` symlink、legacy launchctl plist、64667 兼容码等仍在；完成报告"范围外发现"只列出而未收敛 |
| **状态模型收敛**（Mac↔iOS 连接态/session/turn/capability 状态机） | 未碰 | **本轮完全没碰**，这是更大的跨仓库主题 |
| **质量门禁收敛**（协议/连接/agent 变动必须同步 Mac+iOS+测试+文档） | hygiene 脚本有 reminder | **无强制**，靠纪律 |

---

## 四、第二轮优先级建议（讨论，非执行计划）

评估的性价比排序在第二轮依然成立，但需根据第一轮成果重新排序：

### 第二轮第一梯队（低风险、机制已就绪、收益明确）

**S1. 完成 web 共享包剩余 3 个组件迁移** ← 性价比最高
- 机制（shared 包 + host adapter）第一轮已建好，无需再设计
- 风险低：每个组件独立迁移，可小步回滚
- 收益直接：止住 3 个正在扩大的漂移源（4/68/75 行 diff）
- 成本主要是 resolve 已有 diff，越拖越贵
- 建议顺序：ReasoningBlock（diff 最小，先试水机制）→ ProcessGroup → NarrativeBlock

**S2. 拆 handlers.go 的物理分发**（MacBridge 侧，风险最低的 god-object）
- 4559 行但 dispatch switch 结构本身清晰（评估原话），问题只是物理文件没拆
- 不动逻辑，纯按 RPC case 分文件，回归风险最低
- 可与 transcriptindex / pagination 等已有测试配合验证
- 是 MacBridge 侧唯一不需要跨仓库协调的拆分

### 第二轮第二梯队（高风险，需测试保护护航）

**S3. BridgeProvider 拆分**（iOS 侧最大 god-object）
- 1967 行/88 方法/34 ForTesting，且还在长肉（func 数 78→88）
- 风险高：连接编排/策略选择/transport/recovery 多职责纠缠
- **前置条件不满足**：D 的 characterization 测试只覆盖两个切片，不够当拆分保护网；需先补"按职责切片"的测试（如 transport 创建/recovery 所有权/path 切换各自的可执行不变量）
- 评估原话"需配套测试保护后再动"在此适用

**S4. ChatViewModel 巨型方法拆分**（sendMessage 579/handleCodexLiveEvent 563/switchSession 500）
- 风险最高：直接关系产品行为（发消息/流式渲染/session 切换）
- 需要先有 snapshot/merge 的不变量测试 + streaming 状态机测试
- 不建议在第二轮优先，除非有专门的测试加固期

### 第二轮横向（与具体拆分并行）

**S5. CI gate 从 warning 渐进升级**
- 不是"一次性改成 required"，而是按规则逐条升级：先选存量债务最容易归零的一条（如 `print` 调试日志清理），把它从 warning 升 required，跑通"立法→执法"闭环
- 再逐条推进 NSLog 边界、本地化、类行数上限
- 配合 S1-S4 的拆分，拆完的 god-object 可纳入行数上限 gate

**S6. 状态模型收敛**（评估治理主线 2，本轮完全未碰）
- 跨仓库主题：Mac↔iOS 的连接态/session 列表/turn 流/history polling/Relay reconnect/capability
- 需要先形成状态机文档 + 不变量测试，再谈统一
- 周期长，建议单独立项

### 建议的第二轮范围

如果第二轮仍按"一轮收口"的节奏，**建议聚焦 S1 + S2**：
- S1 完成 web 共享包（动作 4 收尾，性价比最高）
- S2 拆 handlers.go 物理分发（动作 3 的低风险子集）
- 顺带推进 S5 的一条 gate 升级试点

S3/S4 留给第三轮（需测试加固期），S6 单独立项。这样第二轮能把"评估动作 4 完全收口 + 动作 3 启动低风险部分 + 动作 5 试运行"，比第一轮更接近"可维护性接近产品级"。

---

## 五、对"完成度"的精确措辞（供对外引用）

- 评估目标动作 **1-2**：**100% 完成**（第一波全达成，已达"高质量单人/小团队系统软件档"）
- 评估目标动作 **3**：**完成前置保护（D characterization 测试），本体未开始**（4 个 god-object 零拆分，BridgeProvider 还在长肉）
- 评估目标动作 **4**：**机制建立 + 2/5 迁移**（字节全等的迁了，已漂移的 3 个未迁且 diff 在扩大）
- 评估目标动作 **5**：**宪法 + warning-only 脚本就绪，CI 硬卡未立**（渐进式治理路径清晰，存量债务未收敛）
- 评估治理主线 **1-3**：**仅主线 1 局部触达**（config 删了，无全仓 legacy 清单；主线 2/3 未碰）

**净判断**：第一轮是诚实的"第一波收口"，没有 overclaim；但以评估报告为完整目标集，**第二轮是必要的，且最大缺口是 god-object 实际拆分（动作 3 本体）**。
