# 评审报告 R11：Claude 长 session 打开架构优化方案（v15 第十一轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R10 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v15**
方案 SHA-256（全文，开工实测）：`689c531dce365ce3d43b148aa21aab0f62fed43410fa515a5800c70ace539993`——与派发记录一致
方案 SHA-256（收工复测）：`689c531dce365ce3d43b148aa21aab0f62fed43410fa515a5800c70ace539993`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`198e9075cec7cb59cb7dfc25c83846cd6a2b18b782f18456e7b8f320d2cdaa55`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=87201）**精确命中**，记账自洽
范围：full（全量第十一轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **1**（F-31）
- advisories: **4**（F-32、F-33、F-34、F-35）
- **R10 两条意见（F-29/F-30）闭合判定：两条全部闭合**，且闭合质量高——F-29 的三处锚点逐字精确、负对照断言可构造；F-30 的状态机入口收敛与「不得改 switchSession clear」约束明确。
- **v14 方向变更 #2（首开全量 + Direct gzip）判定：方向自洽，但存在一处实施缺口（F-31 阻塞）**——`direct_gzip_v1` 未定义**压缩后的 direct 帧如何自描述**。这是本方案第五次出现「方案没写的既有链路约束」型缺陷，且这次落在 S-8 的冻结批次定义上，不修则 S-8 无法实施。
- **v15 定案回填判定：成立**（OD-1/OD-3/OD-7 理由事实基础准确，决策表确已收口无 pending）。
- **v12/v13 的 older 处置判定：成立**（同根因重分类准确、append/prepend 边界声明正确、OD-8(a) 六处一致）。
- **未判 APPROVED 的原因**：除 F-31 阻塞外，另发现四处建议级缺陷，其中 F-32（v14 取消 S-1 后遗留多处悬空引用，含 §7.3 重试指标的可观测前提与 S-5 前置条件）属门控一致性问题，会让实施者无法确定「trace 交付项归谁」。

**本轮自选新核验面（按指令要求声明，共三个）**：

1. **direct 帧的压缩自描述机制**（产出 F-31 阻塞）——relay 的 gzip 标记住在 relay envelope 的 `contentEncoding` 字段（含 AAD），而 direct 的写路径是 `Conn.SendJSON` → `conn.WriteJSON`，`WireMessage` **无任何编码标记字段**；
2. **anchor-newer 与 fence/held 机制的交互**（产出 F-34 建议）——现有 window handler 用 `CompleteProjectionSnapshotWithHeldTurns` 注册 held turns，§3.8 只写「复用 `growWindowRange` 切片」，未提 held 注册；而 older gate 关闭后 held 集合的消费者只剩 detail commit 路由；
3. **v14 全量首开口径下 §7.3 重试指标的可观测性与归属**（产出 F-32 建议）——事故 8 次投递正是 full snapshot 重试，v14 首开=full 使该指标**直接适用**（比 v9 的 window 口径更贴切），但方案仍写「S-1 交付 window 路径 trace」，而 S-1 已被取消。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 | 判定 |
| --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `f868af18bbbbff6e7f47994c11fb76b49406b33e`；`git status --porcelain` = M 方案 + ?? R10 报告 + ?? 调研报告 | 未记（§2.1 仍为 v3 时点快照） | ✓ 与 R10 实测一致；`git show --stat f868af18` 确认**未触任何 `.go`/`.swift`**（docs-only） |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = **`e52daa275946ee0d41749aa7146ec8bbc8b05c29`**；`git status --porcelain` 为**空** | 记 `dfb77ed9…` + 8 项未提交 | 🟡 漂移（与 R10 实测一致，未再推进），锚点洁净性见下 |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | 同 | ✓ 一致 |
| 正式 runtime | 未重测（本轮无运行态结论；方案对 runtime 的告诫仍有效） | PID 8303，监听 8777 | — 未核 |

**iOS 锚点洁净性（逐文件实测）**：`ProjectionStore.swift`、`ProjectionReplica.swift`、`SessionProjectionWindow.swift`、`SessionTimelineStore.swift`、`CCCodeBridgeBackendClient.swift`、`ChatViewModel.swift`、`ProjectionWindowSpecTests.swift` 逐个 `git status --porcelain --` 实测**全部 clean**；且工作树整体为空（此前 8 项未提交已由 `df47e0de` 入库，R10 已确认未触及任何锚点文件）。**核验对象 = 提交内容，身份成立。**

### 2.2 重点一：R10 两条意见闭合核验

| 意见 | 实测 | 判定 |
| --- | --- | --- |
| **F-29** §3.4.2/§3.4.3 是否写入 store 层 `commitIfObserved(lifecycle: .ready)` | §3.4.2 状态机 `BaseCacheHit` 分支改为**两步**：① replica 层 `applyFull(forceReplace: true)`；② store 层 `commitIfObserved(lifecycle: .ready, changeSet:)`，并写明缺②的后果（`State.lifecycle` 停留 `.disabled` → `renderProjectionFromStore` 静默丢弃 → 空白屏）。§3.4.3 第 1 条展开为两步注入 | ✓ 落地 |
| F-29 三处锚点准确性 | `ProjectionStore.swift:1044-1056` = `switch await replica.applyFull(...)` → `case .applied(...)` → `await commitIfObserved(..., lifecycle: .ready, ...)` **逐字命中**；`:620-626` = `applyWindowPage` 的 `commitIfObserved` 调用尾部 **命中**；`ChatViewModel.swift:1080` = `func renderProjectionFromStore(sessionId: String? = nil)`，`:1086-1089` guard 含 `sessionSyncV2Active` **命中** | ✓ 三处精确 |
| F-29「change-set 携带 lifecycle（apply outcome 硬编码 .ready）」表述 | `fullReplacementChangeSet` 内 `:465 lifecycle: .ready` 实测存在 → replica 的 change-set 确实携带 `.ready`，但 store 的 `State.lifecycle` 不因此更新 | ✓ 表述准确 |
| F-29 §7.2.12 断言 + 负对照 | `:7.2.12` 现含「两步注入断言：restore 后 `State.lifecycle == .ready` 且 `renderProjectionFromStore` 实际渲染；**负对照 = 只做 replica 层 applyFull 不做 store 层 commit → 渲染为空**」 | ✓ 可构造（两步都可在测试中独立控制） |
| **F-30** 状态机是否删除 `MemoryResident` | `grep -n "MemoryResident"` 在方案正文（非 §0 历史表）**零命中**；§3.4.2 首分支现为 `BaseCacheHit`，并注明「重开的唯一常规入口……内存态不参与打开状态机」 | ✓ 已删除并收敛 |
| F-30 `projectionStore.clear` 唯一调用点 | `grep -rn "projectionStore.clear"` = **1 处**，`ChatViewModel+SessionManagement.swift:232` | ✓ 与 R10 一致 |
| F-30 §3.4.2 关键点 6「不得改 switchSession clear」 | 关键点 6 存在，明确「**不得为打开路径改动 `switchSession` 的 clear 语义**……改动属需单独论证的设计变更（会重新引入泄漏面）」 | ✓ 明确 |

**R10 两条闭合。**

### 2.3 重点二：v14 方向变更 #2 的自洽性（含首开耗时预算独立分解）

**gzip 压缩比实测（本轮独立复算，方案 §1.1 声明口径）**：

| 项 | 实测 |
| --- | --- |
| 全量 projection 未压缩 wire | 34,427,361 B |
| gzip level 1 | 11,142,266 B（10.63 MiB，32.4%） |
| gzip level 6 | 9,602,913 B（9.16 MiB，27.9%） |

与方案 R-2 写的「gzip 后约 9.6–11MB」**精确吻合**。

**首开耗时预算分解（工程判断，非实测）**：

| 阶段 | 估算依据 | 量级 |
| --- | --- | --- |
| Direct LAN 传输 9.6–11MB | 事故样本 34MB 在 15s cap 内未完成（含 decode），但纯传输未知；千兆 LAN 约 100MB/s、WiFi 5 实测常见 20–40MB/s | 0.3–0.6s（LAN 优）/ 0.3–0.5s（WiFi 20–40MB/s） |
| Mac 侧 gzip 压缩 34MB | level 6 约 0.3–0.8s（Go `klauspost/compress` 更快）；方案未给预算 | 0.3–0.8s |
| iOS gunzip 9.6MB | 约 0.1–0.2s | 0.1–0.2s |
| iOS JSON decode（S-5 单次解析前：现状多轮 parse+re-encode） | 34MB × 多次对象图构建——这是事故的主因之一 | **数秒级**（S-5 前）/ 0.5–1.5s（S-5 后单次 typed decode） |
| replica apply + presentation | 119 turns | 0.1–0.3s |

**结论**：`≤8s` 目标在 **S-5 落地后**有合理余量（合计约 1.5–3.5s + 传输）；**若 S-5 未落地**，仅 decode 一项就可能吃掉 3–6s，加上传输与 gzip 接近或超过 8s，且 15s cap 的余量压缩到 2–3s。方案已识别这一点（§3.6「gzip 只压网络，不消 iOS JSON 对象图与 presentation 成本——S-5 单次解析是首开达标的另一半，两者缺一不可」），且 S-6 的前置写「**S-5/S-8（首开达标，v14）** 最少」——**依赖关系已在切片层体现**。但 **§6 S-5 行的前置条件仍写 `S-1`**（见 F-32），这是 S-5/S-8 依赖链的登记瑕疵。

**判断**：R-2 的 ≤8s/≤10s 目标**在 S-5 + S-8 同时交付的前提下可达**，方案对依赖关系的定性准确。属工程判断，非实测（未 build/未 test，且无真机数据）。

### 2.4 重点二：scalarState 收缩的正确性

| 论断 | 实测 | 判定 |
| --- | --- | --- |
| 「不触碰 frozen v1 字段集——三处冻结断言无需更新」 | `frozenProjectionWindowFields`（`projection_window_spec_test.go:46-57`）只锁三个**具名类型**：`BridgeGetSessionProjectionWindowParams` / `BridgeProjectionWindow` / `BridgeGetSessionProjectionWindowResult`；`:204-211` 遍历该 map 逐类型比对。**新增 anchor-newer 响应类型不在此 map 内 → 不触发断言** | ✓ 成立 |
| fixture 断言（`:73-75`） | `sortedRawKeys(response)` 与 `["resume","syncRev","turns","window"]` 精确比对——**锁的是 `window-response-spec.json` 这份 v1 样本**。若 anchor-newer 用**新样本文件**，此断言不受影响 | ✓ 成立（前提：新样本独立文件） |
| iOS `:48` / `:129-130` | `XCTAssertEqual(Set(response.keys), ["window","turns","syncRev","resume"])`（`:48`）+ `frozenInterfaceFields["BridgeGetSessionProjectionWindowResult"]`（`:130`）——同样只锁 v1 | ✓ 成立 |
| 全量首开原生含 execution | `SessionProjection`（`SessionProjection.swift:675+`）自带 `execution` 字段 | ✓ 成立 |

**判断**：v14 的「真简化」成立——只要 anchor-newer 用**独立响应类型 + 独立样本文件**，三处冻结断言确实无需改动。方案 §3.7 范围注已明确「新增 capability/类型/样本，不触碰 frozen v1 字段集」。**但方案未显式写「anchor-newer 样本必须用独立文件（不得复用 `window-response-spec.json`）」**，而 fixture 断言是按文件名加载的（`projectionWindowFixture` 固定读 `window-response-spec.json`）——若实施者把 anchor 样本追加进同一文件，`:73-75` 会失败。这是 F-33。

### 2.5 重点二：window_0 contingency 可回退性

| 论断 | 实测 | 判定 |
| --- | --- | --- |
| 协议与两端实现保留 | `pullOpenWindowed`（`ProjectionStore.swift:763-777`）仍在；window handler（`projection_window_handler.go`）仍在；`main.go:560 SetProjectionWindowEnabled(true)` 仍启用 | ✓ 一致 |
| 「启用时补 §3.7 v1 响应 scalarState 冻结」可执行 | §3.7 六项批次（①–⑥）完整保留，范围注明确「降级为 contingency 触发项（`window_0` 被启用时执行）」 | ✓ 可执行 |
| F-21 批次降级登记完整性 | 范围注写明触发条件（`window_0` 被启用）、启用时需补什么（v1 响应 scalarState 冻结）、以及 anchor 新类型不触碰 v1 | ✓ 登记完整 |

### 2.6 重点二：v12/v13 older 处置

| 论断 | 实测 | 判定 |
| --- | --- | --- |
| §2.5 同根因重分类 | `:291` 「前四条为同症状不同根因……**第五条（2026-07-12 分页复盘）与本案 older 方向同根因**」；`:297` 逐字复述复盘三条复活条件 | ✓ 准确 |
| append/prepend 边界声明 | 明确「S-1 首开（空屏首批内容，无插入动作）与 v10 重开增量（尾部 append，不改变已渲染内容位置与高度）**不受该复盘约束**」 | ✓ 成立 |
| SSV2 older 同坑锚点 | `SessionProjectionWindow.swift:158` 注释原文「older page's unknown prefix lands ahead of it」，`:161 var updated = turns` → `updated.insert(turn, at: cursor + 1)` | ✓ 逐字命中 |
| `supportsSessionPagination` 硬编码 false | `CCCodeBridgeBackendClient.swift:226` `self.supportsSessionPagination = false` | ✓ 精确 |
| OD-8(a) 六处一致 | §2.5（`:291/:297`）、§3.3（`:439`）、§4（`:669`）、§6 S-1 段（`:41`/`:715` 附近）、§7.2.15（`:781`）、§7.4 场景 8（`:824`）——**六处全部命中且语义一致** | ✓ 成立 |
| 「零 `direction=older` 请求」断言可执行 | 断言 = Mac trace 无 `direction=older`（需 F-25 的 window trace 交付）；`:781` 已写明触发链锚点（`ChatViewModel+MessageSync.swift:981`、`ChatUIKitContainerView.swift:2060`） | ✓ 可执行（依赖 trace 交付） |

### 2.7 重点二：v15 定案回填

| 项 | 实测 | 判定 |
| --- | --- | --- |
| OD-1 理由 | 「A 与 owner 重开 UX 冲突 / C 丢失败显式 / B 安全性已论证」三条，与 §3.4.2 关键点 3 的安全性论证一致 | ✓ 事实基础准确 |
| OD-3 理由 | 四条（时间非正确性判据 / TTL 在最需缓存场景最伤 / 占用与消失各有更准机制 / 仓内先例）——逻辑自洽，且「安全性靠 typed stale + fail-closed」与 §3.4.2 关键点 4 一致 | ✓ 成立 |
| OD-7 引 `projection_window.go:27-31` | 实测 `:27 maxWindowTurns = 256`、`:28 maxWindowEncodedBytes = 4 << 20`、`:29 defaultWindowTurns = 100`、`:30 windowEncodingOverhead = 4 << 10` | ✓ 精确（上限类别对齐成立） |
| OD-7 数值自洽 | 单 session 4MiB/256 turns 与 frozen v1 同类别；总量 128MB + LRU；2 页阈值与 v14 冷路径关系（「新增超过两页意味着用户离开很久」）逻辑成立；anchor limit 100 turns 对齐 `defaultWindowTurns` | ✓ 成立 |
| 决策表收口 | `sed -n '/^| ID | 问题/,/^$/p'` 内 `grep -c pending` = **0** | ✓ 确无 pending |
| §3.4.1 淘汰回退路径 v14 一致性修正 | 已改为「全量冷路径」（不再是 `window_0`） | ✓ 一致 |

### 2.8 重点二：E-10 口径收缩

**方案主张**：「冷路径全量原生含 execution，E-10 自动满足」。

**实测与判断**：
- `SessionProjection` 确实自带 `execution` 字段，全量 `get_session_projection` 返回整棵 projection → **数据层面确实原生包含**，✓ 成立。
- **但「自动满足」不等于「无需断言」**。E-10 的原始目的是「验证 window 冷开不显示空闲、`requires_action` 锁输入框」——即验证**端到端消费链**（`projection.execution` → `isSessionExecuting` → 输入框禁用）。全量路径虽然数据齐备，但**若 S-4 的 restore 或 S-5 的 decode 有缺陷导致 execution 丢失**，冷路径同样会失效。方案把 E-10 收缩到 anchor 路径后，**冷路径的 execution 消费链失去任何门**。
- 更关键的是：**§5 的 E-10 表格行（`:686`）仍写旧口径**「window 冷开 scalar state 正确性……S-1 完成宣告」——与 v14 收缩后的「anchor 路径」矛盾，且「S-1 完成宣告」已随 S-1 取消而悬空。

这是 F-35（建议级）。

### 2.9 数字与锚点抽核

§1.1 数字独立复算：checkpoint 34,537,988 B；turns 119；整棵 34,427,361 B；最大单 turn 6,347,762 B（index 1，不在尾 17）；尾 17 = 3,801,579 B；transcript 101,520,809 B = 96.8178 MiB；gzip1 11,142,266 / gzip6 9,602,913 B（与方案一致）；8 次全量投递（req_18/33/54/58/67/77/82/91，gen 1→5）。**全部维持命中。**

锚点抽核 12 处：`projection_window.go:24-32/52-60/77-82/252-257/278-296/297-301`、`projection_revision_journal.go:8-12/127`、`handlers_projection.go:89/122/175/255`、`projection_snapshot_fence.go:214-216/224/227/230-235/269-276`、`event_publisher.go:1674-1687/1726-1749`、`bridge-v1.md:2266-2272/2328-2332`、`ProjectionStore.swift:145/152/763-777/1044-1056`、`ProjectionReplica.swift:193-212/208/323/465`、`SessionProjectionWindow.swift:96-98/116-176/158/161`、`CCCodeBridgeBackendClient.swift:226`、`ChatViewModel.swift:1080/1086-1089`——**全部维持命中**。

### 2.10 覆盖声明

- **已实测核验**：三仓身份（含 MacBridge docs-only 提交验证、iOS 锚点洁净性逐文件）；方案全文哈希（开工 + 收工）；交接块正文哈希；R10 两条闭合逐点（含三处锚点 + `:465` 硬编码）；v14 gzip 压缩比独立复算；scalarState 收缩的四类断言实测；window_0 contingency 保留验证；v12/v13 六处一致 + 三处源码锚点；v15 三行理由事实基础（含 `projection_window.go:27-31` 逐行）；决策表收口；§6 切片表实际行（S-0/S-2/S-3/S-4/S-5/S-6/S-7/S-8，**无 S-1 行**）；direct 帧写路径与 `WireMessage` 字段（F-31 核心）；held 注册机制（F-34）；§7.3 重试指标行（F-32）。
- **已独立推理（非命令实测，已标注）**：§2.3 的首开耗时预算分解——基于 gzip 实测比、LAN/WiFi 常见吞吐、S-5 前后 decode 成本差异推得，**未运行任何 build/test，无真机数据**；F-31 的「S-8 无法实施」——基于 relay 的 `contentEncoding` 与 direct `WireMessage` 无该字段的两项实测推得；F-35 的「冷路径失去 execution 门」——基于 E-10 原始目的与收缩后口径的对比推得。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 运行态（本轮不涉及）；transcript 逐行 type 分布（对本案无影响）；**未 build / 未 test**（硬边界）。

## 3. 意见

### F-31 [阻塞] `direct_gzip_v1` 未定义压缩帧的自描述机制：direct 的 `WireMessage` 无任何编码标记字段，而 relay 的 `contentEncoding` 住在 relay envelope 内

- **位置**：§3.6 S-8 段（「形态 = **应用层逐帧压缩，镜像 `relay_gzip_v1` 纪律**：≥32KiB 且压缩更小才启用、仅 Mac→client 大响应、经 additive capability 协商（hello echo）；iOS 解压复用 Relay 路径既有 gunzip 代码」）；§6 S-8 行；§7.3「direct gzip 压缩比与耗时」指标行。
- **实测证据**：
  1. **relay 的 gzip 标记住在 relay envelope 的字段里**——`go-bridge/relay_conn.go:296` `contentEncoding := ""` → `:304 contentEncoding = "gzip"` → `:353 ContentEncoding: contentEncoding` 写入 envelope；协议文档 `docs/protocol/relay-frame-compression.md:28` 明写 envelope 增加 `{"contentEncoding":"gzip"}`，且 **`:31`「`contentEncoding` is included in the envelope AEAD additional authenticated data (AAD)」**；`relay-v1.md:195` 同。
  2. **direct 的写路径没有任何编码标记位**——`go-bridge/server.go:108-112`：
     ```go
     func (c *Conn) SendJSON(v interface{}) { _ = c.SendJSONReport(v) }
     ```
     → `:115-146` 内直接 `c.conn.WriteJSON(v)`（gorilla WebSocket），**无压缩、无 encoding 标记、无 binary/text 区分逻辑**。
  3. **direct 的 wire 信封无该字段**——`go-bridge/types.go:13-33` 的 `WireMessage` 字段为 `Type / RequestID / BackendID / SessionID / Method / Operation / Event / BulkCorrelationID / Params / Data / Client / Protocol / Error / Capabilities / LastBridgeEpoch / LastEventID`——**没有 `contentEncoding`**。
  4. **方案全文零处定义该机制**——`grep -n "contentEncoding\|帧级\|自描述\|binary message\|编码标记"` 在方案中**零命中**。
- **影响**：S-8 的核心问题是「iOS 收到一个 frame，如何知道它是 gzip 还是原始 JSON？」。relay 靠 envelope 字段解决，但 **direct 没有 envelope**——`WriteJSON` 直接序列化 `WireMessage` 并作为 text frame 发送。所以 S-8 必须新增一种自描述方式，可选形态至少有四种，各有代价：
  - **(a) 给 `WireMessage` 加 `contentEncoding` 字段** → 但这要求压缩后仍能构造合法 `WireMessage`（即先 gzip 再包一层信封），**等于改变了 direct 的帧结构**，且 iOS 的 transport 层（`CCCodeBridgeTransport`）需要先解信封再 gunzip 再 decode —— 这是一次**direct 传输层的结构性变更**，不是「镜像 relay 纪律」能概括的；
  - **(b) 用 WebSocket binary frame 区分**（text = 原始 JSON，binary = gzip）→ 侵入小，但 gorilla 的 `WriteJSON` 固定写 text，需改 `WriteMessage(BinaryMessage, ...)`，且 iOS 侧 `URLSessionWebSocketTask` 需按 message type 分流；
  - **(c) 帧前缀魔数**（如 `\x1f\x8b` gzip magic）→ 零协议变更，但**脆弱**（JSON 以 `{` 开头，gzip 以 `0x1f` 开头，实际可区分），且违反「不得猜测」的既有纪律；
  - **(d) 复用 permessage-deflate** → 已被方案否决（§3.6「标准 `permessage-deflate`……可控性与可测性低于应用层逐帧」），否决理由成立，但否决后**必须给出替代的自描述方案**，方案未给。
- **为什么这是阻塞而非建议**：S-8 的冻结批次（§6「冻结批次（协议文档/样本/断言/`schemaRevision`）」）**无法在没有该定义的情况下开始**——协议文档要写什么、样本要长什么样、断言要验什么，全都取决于这个机制。方案对 S-8 的所有其它细节（阈值 32KiB、仅 Mac→client、capability 协商、回退）都写清楚了，唯独最核心的「压缩帧如何被识别」缺失。实施者面对此缺口只有两条路：自行发明一个方案（违反 R10「implementing agents MUST NOT guess them in code」），或停下来问（S-8 卡住）。这与 F-21（re-freeze 义务未登记）同型，但更严重——F-21 是「少登记了一个义务」，F-31 是「核心机制未定义」。
- **修订方向**：① 在 §3.6 S-8 段明确**自描述机制**并给出选定理由（建议评估 (a) vs (b)：若选 (b) binary frame，需说明 iOS `URLSessionWebSocketTask` 的 message-type 分流与 relay 路径的复用边界；若选 (a)，需说明它是否构成 direct 帧结构的破坏性变更、是否要 bump protocol major）；② 明确该机制对 **iOS transport 层**的改动点（`CCCodeBridgeTransport` 的 frame 接收路径目前假定 text JSON）；③ 明确 `direct_gzip_v1` 的 **echo 语义与回退**：未协商时 Mac 不压缩（现状），已协商但某帧压缩后更大时不压缩——**同一连接上必须能逐帧混合**，这要求自描述机制是**逐帧**的而非连接级的；④ 若选定机制需要改动 `WireMessage` 或帧类型，把「direct 传输层变更」的影响面（transport decode 顺序、trace、iOS 解码管线 S-5 的衔接）写入 §3.6 与 S-8 的组件列。
- **闭合标准**：§3.6 给出 direct 压缩帧的自描述机制（含逐帧混合语义）、选定理由、对 iOS transport 的改动点，且 S-8 的冻结批次内容与之对应。

### F-32 [建议] v14 取消 S-1 后遗留多处悬空引用：§7.3 重试指标的可观测前提、S-5 前置条件、S-3 前置 OD-2、以及多处以「S-1」为主语的段落

- **位置**：§7.3 重试次数指标行（「**S-1 交付必须包含 window 路径与 full 同构的 `projection_rpc` trace**」）；§6 S-5 行前置条件（`S-1`）；§6 S-3 行前置条件（`OD-2`）；§2.2 多处「S-1 不改」；§3.7 范围注「不得在 S-1 变更边界内顺带修正」；§7.1.4/§7.2.1 的「S-1 交付」表述。
- **实测证据**：
  1. **§6 切片表已无 S-1 行**——`sed -n '/^## 6\./,/^## 7\./p'` 后 `grep -E "^\| S-"` 实测只有 S-0 / S-2 / S-3 / S-5 / S-6，加上加粗格式的 S-7 / S-4 / S-8；**S-1 已不存在**。
  2. **但 S-5 的前置仍写 `S-1`**（`:6` 表内 S-5 行）；**S-3 的前置仍写 `OD-2`**，而 OD-2 已「被 v14 方向取代」（`:663`）。
  3. **§7.3 重试指标行的可观测前提仍写「S-1 交付必须包含 window 路径……trace」**（`:811`），而 v14 已把 trace 交付并入 S-7（`:691`「window 通路修复、scalar（anchor 响应范围）、trace 与双路径 echo 纪律全部并入 S-7」）。
  4. **多处「S-1 不改」「S-1 交付」**（§2.2 的 F-13 已知偏离行、§3.7 范围注、§7.1.4、§7.2.1）未随 v14 更新。
- **影响**：这不会导致实施错误（读者能从 §0.1 与 §6 看出 S-1 已取消），但会让**门控归属不清**：§7.3 说 trace 由 S-1 交付、§6 说 trace 并入 S-7——实施者若照 §7.3 找 S-1 会找不到。同理 S-5 的前置写成已不存在的切片，会让依赖链出现断点（实际依赖应是「无前置」或「S-8 独立」）。**事故 8 次投递正是 full snapshot 重试，v14 首开=full 使该指标比 v9 的 window 口径更直接适用**——这个「更容易观测」的收益被悬空引用掩盖了。
- **修订方向**：① §7.3 重试指标行把「S-1 交付」改为「S-7 交付（v14 并入）」；② §6 S-5 行前置 `S-1` 改为「无（可与 S-8 并行）」或直接删；③ §6 S-3 行前置 `OD-2` 改为「OD-2 已被取代，limit 归 OD-7」；④ 全文 `grep -n "S-1"` 后逐处判断：历史表（§0.x）保留原文，正文活断言改为 S-7 或标注「原 S-1 已并入 S-7」。
- **闭合标准**：正文中不再有指向已取消切片的活断言；§6 依赖链无断点。

### F-33 [建议] anchor-newer 的冻结样本必须用独立文件，方案未显式声明——若追加进 `window-response-spec.json`，v1 的 fixture key 断言会失败

- **位置**：§3.7 范围注（「新增 capability/类型/样本，不触碰 frozen v1 `BridgeGetSessionProjectionWindowResult` 字段集——三处冻结断言无需更新」）；§3.8 re-freeze 批次（「与 §3.7 六项同构（协议章节新增 anchor-newer 小节 / schema / 冻结样本两仓副本 / Mac spec 断言 / iOS spec 断言 / `schemaRevision` bump……」）。
- **实测证据**：
  - Mac fixture 加载器**固定读单一文件**——`projection_window_spec_test.go:31-42`：
    ```go
    raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "samples", "projection-window-v1", "window-response-spec.json"))
    ```
    `:73-75` 对该文件的 `response` keys 做**精确集合断言** `["resume","syncRev","turns","window"]`。
  - iOS 同构——`ProjectionWindowSpecTests.swift:34` `fixtureRoot = .../samples/projection-window-v1`，`:43` 读同一文件名，`:48` 同款精确断言。
  - 即：**只要 anchor-newer 的样本条目被追加进同一 `window-response-spec.json`，`:73-75` 与 iOS `:48` 必然失败**。
- **影响**：v14 的「真简化」结论（三处冻结断言无需更新）**只在「anchor 用独立样本文件」时成立**。方案说「新增……样本」但没说是新文件还是同一文件的新条目，而这两种做法的断言后果完全相反。实施者若图省事追加进同一文件，会撞上 F-21 原本要防的同类失败（只是范围更小）。属建议级（实施期会立刻发现），但应写清。
- **修订方向**：§3.7 范围注与 §3.8 批次明确「anchor-newer 冻结样本使用**独立文件**（如 `anchor-newer-response-spec.json`），不得追加进 `window-response-spec.json`；v1 的 fixture key 断言保持不动」；并在 §7.1.5 的断言项里写明「v1 fixture 断言未变（回归护栏）」。
- **闭合标准**：样本文件归属明确，且有一处断言证明 v1 fixture 未被改动。

### F-34 [建议] §3.8 未说明 anchor-newer 响应是否注册 held turns——现有 window handler 用 `CompleteProjectionSnapshotWithHeldTurns`，older gate 关闭后 held 的消费者只剩 detail commit 路由

- **位置**：§3.8「服务端实现」bullet（只写「切片逻辑复用 `growWindowRange(sizes, limit, boundary+1, false)`（现成）」）。
- **实测证据**：
  1. **现有 window handler 在完成事务里注册 held turns**——`go-bridge/projection_window_handler.go:200-211`：
     ```go
     turnIDs := make([]string, 0, len(response.Turns))
     for _, turn := range response.Turns { turnIDs = append(turnIDs, turn.TurnID) }
     if err := h.eventPublisher.CompleteProjectionSnapshotWithHeldTurns(conn, admission, msg.RequestID, response, turnIDs); err != nil {
     ```
     注释明写这是「turn_detail_lazy_v1 rule 3/4 bookkeeping rides the SAME completion transaction (audit P0-2)」，且「a post-hoc registration window would route this connection a no-op revision patch and permanently strand the detail behind the stale response」。
  2. **held 集合的消费者是 decide=true 链**（`projection_snapshot_fence.go:227`），即 `PublishProjectionDetail` 的 rule-4 分支（turn_detail 家族）。
  3. **OD-8(a) 关闭 older 后**，window 连接只剩 anchor-newer 增量页（重开路径）与 `window_0`（contingency）。若 anchor-newer 不注册 held，则该连接对 detail commit 一律被判「未持有」→ 收到 no-op patch → **detail 永远不到**（正是 P0-2 注释警告的失效模式）。
- **影响**：这是「复用 growWindowRange 就够了吗」的反面——切片逻辑确实现成，但**完成事务的 held 注册是另一段必须同时复用的代码**。§3.8 只提切片，实施者可能只接切片而漏掉 held 注册，导致重开后 turn detail（工具明细）加载不出来。属建议级（有 P0-2 注释与既有测试护栏可发现，且 turn_detail 有独立 fallback），但应在冻结前写清。
- **修订方向**：§3.8 服务端实现补一句「anchor-newer 页的完成事务必须同样走 `CompleteProjectionSnapshotWithHeldTurns`（注册本页 turns 为 held），与 `window_0`/`older` 一致——否则 detail commit 的 rule-4 路由会把该连接判为未持有并投 no-op patch」；并在 §7.1.5 补一条断言（anchor 页投递后，针对其中某 turn 的 detail commit 能到达该连接）。
- **闭合标准**：§3.8 明确 held 注册义务，且 §7.1.5 有对应断言。

### F-35 [建议] E-10 收缩到 anchor 路径后，冷路径（全量首开）的 execution 消费链失去任何门；且 §5 E-10 表格行仍写旧口径「window 冷开 / S-1 完成宣告」

- **位置**：§5 E-10 行（`"window 冷开 scalar state 正确性（F-11）……**S-1 完成宣告**（与 E-3/E-4 并列）"`）；§3.7 范围注（「冷路径（全量首开）原生含 execution」）；§6 S-7 完成宣告（「E-10（anchor 路径 scalar；冷路径全量原生含 execution，自动满足）」）；§9 第一阶段（「运行中 session 首开执行态 / 权限锁不丢（全量原生含 scalar，E-10 冷路径自动满足）」）。
- **实测证据**：
  - `SessionProjection` 自带 `execution`（`SessionProjection.swift:675+` 含 `var execution: SessionExecutionView`）→ **数据层面成立**。
  - 但 E-10 的原始验收对象是**端到端消费链**：`projection.execution.phase` → `isSessionExecuting`（`ChatViewModel.swift:2419-2426`）→ 输入框禁用 / `composerAwaitingUserAction`（`ChatViewModel+TaskDock.swift:33`）。数据齐备 ≠ 消费链正确。
  - **§5 E-10 表格行（`:686`）未随 v14 更新**——仍写「window 冷开 scalar state 正确性」与「**S-1 完成宣告**」，而 S-1 已取消、冷路径已改全量。
- **影响**：v14 把 E-10 收缩到 anchor 路径是合理的（避免重复验收），但「冷路径自动满足」的推论**只在「数据齐备」层面成立**。若 S-4 restore 或 S-5 decode 引入缺陷导致 execution 丢失，冷路径同样会显示「空闲」——而收缩后**没有任何门会捕获**。此外 §5 表格行与正文口径矛盾（一个说 window/S-1、一个说 anchor/冷路径自动满足），属门控不一致。
- **修订方向**：① §5 E-10 行改写为「anchor 路径 scalar 正确性（v14 口径）；冷路径由全量原生承载，**保留一条最小断言**（首开后 `isSessionExecuting` 在 running session 上为 true）」——保留一条即可，不需要重复全套；② 统一 §3.7 范围注 / §6 S-7 / §9 三处「自动满足」的措辞为「数据原生含 + 保留最小消费链断言」。
- **闭合标准**：§5 E-10 行与正文口径一致，且冷路径至少有 1 条 execution 消费链断言。

## 4. 历轮意见闭合复核

### 4.1 R10 两条（本轮重点）

| 意见 | 级别 | 本轮判定 |
| --- | --- | --- |
| F-29 restore 漏 store 层提交 | 建议 | **闭合**（两步注入 + 三处锚点精确 + 负对照可构造 + `:465` 硬编码表述准确） |
| F-30 MemoryResident 死路径 | 建议 | **闭合**（分支已删、入口收敛、clear 唯一调用点维持、关键点 6 明确禁止改 clear） |

### 4.2 历项抽核

| 意见 | 出处 | 判定 |
| --- | --- | --- |
| F-1 ~ F-9 | R1 | **全部闭合**（抽核维持） |
| F-10 | R2 | **闭合** |
| F-11 / F-12 | R3 | **闭合** |
| F-13 ~ F-16 | R3 | **闭合**（F-16 的 8 次投递事实在 v14 口径下更适用，见 F-32） |
| F-17 / F-18 / F-19 | R4 | **闭合** |
| F-20 | R5 | **闭合**（decide 路由五条断言维持） |
| F-21 / F-22 / F-23 | R6 | **闭合**（F-21 降级为 contingency 触发项，登记完整；但见 F-33 的样本文件归属） |
| F-24 / F-25 | R7 | **闭合**（F-25 的 trace 交付项归属需更新，见 F-32） |
| F-26 / F-27 | R8 | **闭合** |
| F-28 | R9 | **闭合** |
| F-29 / F-30 | R10 | **闭合**（本轮确认） |

**回归检查**：v11–v15 改动集中于 §0/§1.2/§2.5/§3.1/§3.3/§3.4/§3.6/§3.7/§3.8/§4/§5/§6/§7/§8/§9/§10，本轮重读这些章节及全部交叉引用。**发现的问题**：F-31（S-8 核心机制缺失）、F-32（S-1 取消后的悬空引用群）、F-33（样本文件归属）、F-34（held 注册）、F-35（E-10 口径矛盾）。**未发现**：架构方向矛盾、数字错误、锚点错位（除 F-31/F-33 涉及的机制缺口外，全部锚点逐字命中）。

## 5. 已定案事项复核

owner 已定案的 OD-1/3/6/7/8 本轮逐项复核**理由的事实基础**，未发现基于错误事实的情况：

- **OD-1 = B**（恢复基底 + 同步标记）：三条理由与 §3.4.2 关键点 3 的安全性论证一致；「A 与 owner 重开 UX 冲突」与 v10 以来的「重开 ≤1s」目标一致。**成立。**
- **OD-3 = A**（无 TTL）：「时间非正确性判据」与 §3.4.2 关键点 4 的 typed stale 兜底逻辑一致；「TTL 在最需缓存场景最伤」与 OD-7 的 LRU 机制互补。**成立。**
- **OD-6 = W1**：维持（连续第七轮）。v14 收缩了交付载体（anchor 新类型而非 v1 响应），但 W1 的「additive 携带 scalar」本质未变。**成立。**
- **OD-7 = A**（4MiB/256 turns、128MB、2 页、100 turns）：引 `projection_window.go:27-31` 的 `maxWindowTurns=256` / `maxWindowEncodedBytes=4<<20` / `defaultWindowTurns=100` **逐行精确**；「不发明新数值类别」的论证成立。**成立。**
- **OD-8 = (a)**（关闭 older 上滑）：六处一致，复盘三条复活条件引用准确，`supportsSessionPagination` 硬编码 false 复核成立。**成立。**

**唯一需注意**：OD-7 的「2 页阈值退全量冷路径」在 v14 口径下是**合理强化**（冷路径现在含 gzip，退全量代价低于 v13），但 §3.4.2 状态机的 `FallbackFull` 命名与 §8 回退边界需保持同步（本轮抽核 `:311`/`:792` 一致）。

## 6. 剩余门

- **证据门**：**E-3**（window 通路未生效根因——**S-7 开工前置**，anchor-newer 依赖同一协商/handler 管道）；**E-4**（anchor 页 ≤4MiB 口径——S-7 完成宣告）；E-5（iOS 阶段耗时——S-5）；E-6（materialization 正确性——S-2）；E-7（基底不污染权威 writer——S-4）；**E-10**（anchor 路径 scalar；冷路径最小断言，见 F-35）；**E-11**（restore + 增量正确性——S-4 完成宣告）。
- **决定门**：**无 pending**（OD-1/3/6/7/8 全部定案；OD-2 被 v14 取代，limit 归 OD-7）。
- **冻结批次前置**：S-7 anchor-newer re-freeze 批次；**S-8 `direct_gzip_v1` 冻结批次（当前被 F-31 阻塞）**。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份、方案哈希、R10 两条闭合、v14 方向自洽性、首开耗时预算分解、direct 帧编码标记、scalarState 收缩、window_0 contingency、v12/v13 older 处置、v15 定案理由、E-10 口径、S-1 悬空引用、held 交互），全部亲核完成后才读 R10 报告与历轮报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与 R10 的差异**：R10 判 REVISION_REQUIRED（0 阻塞 + 2 建议）；v11 修完后 owner 又做了三次方向调整（v12 质询修订、v13 OD-8 定案、v14 方向变更 #2、v15 OD 定案回填），这些**均未经评审**。本轮对 v11 的两条闭合予以确认，对 v12–v15 按全新内容审查，产出 1 阻塞 + 4 建议。
- **本轮新核验面的价值**：F-31（direct 帧自描述）是本轮最重要的发现，属「方案没写的既有链路约束」第五次出现——前四次分别是 fence 语义（R5）、SQLite 选型（R3）、协议冻结义务（R6）、capability 协商机制（R8）。共同模式：**方案描述了「要做什么」，但没核对「做这件事需要动既有系统的哪一层」**。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`git show --stat f868af18 | grep -E "\.go|\.swift"`；逐锚点文件 `git status --porcelain --`；`python3` 交接块哈希切分 + gzip 双级别压缩 + checkpoint 复算；`sed -n`/`awk` 逐段读 `server.go:108-146`、`types.go:13-33`、`relay_conn.go:296/304/353`、`projection_window_handler.go:200-211`、`projection_snapshot_fence.go:214-216/224/227`、`projection_window_spec_test.go:31-42/46-57/72-82/204-211`、`ProjectionWindowSpecTests.swift:34/43/47-51/127-131`、`ProjectionStore.swift:1044-1056/620-626`、`ProjectionReplica.swift:193-212/465`、`ChatViewModel.swift:1080-1089`、`SessionProjectionWindow.swift:158-161`、`CCCodeBridgeBackendClient.swift:226`；`grep -c pending` 决策表收口；`sed -n '/^## 6\./,/^## 7\./p' | grep -E "^\| S-"` 切片表实际行。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 10 份报告 + 调研报告、iOS 工作树一律只读未触碰；**未 build / 未 test / 未部署 / 未安装**。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、三口径字节测量、gzip 双级别、日志计数、fixture key 断言实测、冻结字段集作用域、direct vs relay 传输路径对比、held 注册链路）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: 689c531dce365ce3d43b148aa21aab0f62fed43410fa515a5800c70ace539993
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r11.md
scope: full
blockers: 1
advisories: 4
open_gates: E-3, E-4(anchor 口径), E-5, E-6, E-7, E-10(anchor 口径 + 冷路径最小断言), E-11, S-7 anchor-newer re-freeze 批次, S-8 direct_gzip_v1 冻结批次(被 F-31 阻塞), S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
